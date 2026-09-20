package io.jastreamer.android

import android.content.BroadcastReceiver
import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.media.AudioManager
import android.os.Process
import android.os.SystemClock
import android.webkit.CookieManager
import androidx.annotation.OptIn
import androidx.core.content.ContextCompat
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.ForwardingPlayer
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.FlagSet
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.datasource.HttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.exoplayer.source.MediaSource
import androidx.media3.exoplayer.upstream.DefaultLoadErrorHandlingPolicy
import androidx.media3.exoplayer.upstream.LoadErrorHandlingPolicy
import androidx.media3.session.DefaultMediaNotificationProvider
import androidx.media3.session.MediaSession
import androidx.media3.session.MediaSessionService
import androidx.media3.session.SessionCommands
import androidx.webkit.ProfileStore
import androidx.webkit.WebViewFeature
import java.io.IOException
import java.util.IdentityHashMap
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLException
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import okhttp3.Call
import okhttp3.Callback
import okhttp3.CookieJar
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import okhttp3.ResponseBody
import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject

@OptIn(UnstableApi::class)
class NativePlaybackService : MediaSessionService(), Player.Listener {
    private val serviceJob = SupervisorJob()
    private val scope = CoroutineScope(serviceJob + Dispatchers.Main.immediate)
    private lateinit var player: ExoPlayer
    private lateinit var session: MediaSession
    private lateinit var routedPlayer: ServerRoutedPlayer
    private var registration: Registration? = null
    private var stateKey: String? = null
    private var currentResource: ActiveResource? = null
    private var activeExecution: Job? = null
    private var activeExecutionSequence = 0L
    private var recoveryJob: Job? = null
    private var pollJob: Job? = null
    private var leaseJob: Job? = null
    private var leaseWatchdogJob: Job? = null
    private var identityJob: Job? = null
    private var preparing = false
    private var recovering = false
    private var publicError: PublicError? = null
    private var leaseExpiryElapsed = 0L
    private var sequenceFence = SequenceFence()
    private val controlMutex = Mutex()
    private val playerErrors = ArrayDeque<PlaybackException>()
    private var pendingTerminal: TerminalEvent? = null
    private var acknowledgingCommand = false
    private var deferredPlaybackError: PlaybackException? = null

    private val noisyReceiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context?, intent: Intent?) {
            if (intent?.action != AudioManager.ACTION_AUDIO_BECOMING_NOISY || currentResource == null) return
            currentResource?.wantsPlayback = false
            player.pause()
            sendPlayerControl("pause")
        }
    }

    override fun onCreate() {
        super.onCreate()
        player = ExoPlayer.Builder(this)
            .setWakeMode(C.WAKE_MODE_NETWORK)
            .build()
            .also {
                it.setAudioAttributes(
                    AudioAttributes.Builder()
                        .setUsage(C.USAGE_MEDIA)
                        .setContentType(C.AUDIO_CONTENT_TYPE_MUSIC)
                        .build(),
                    true,
                )
                it.setHandleAudioBecomingNoisy(false)
                it.repeatMode = Player.REPEAT_MODE_OFF
                it.addListener(this)
            }
        routedPlayer = ServerRoutedPlayer(player)
        session = MediaSession.Builder(this, routedPlayer)
            .setCallback(SessionCallback())
            .build()
        val notificationChannel = NotificationChannel(
            NOTIFICATION_CHANNEL_ID,
            getString(R.string.native_playback_notification_channel),
            NotificationManager.IMPORTANCE_LOW,
        ).apply {
            description = getString(R.string.native_playback_notification_description)
        }
        getSystemService(NotificationManager::class.java).createNotificationChannel(notificationChannel)
        setMediaNotificationProvider(
            DefaultMediaNotificationProvider.Builder(this)
                .setChannelId(NOTIFICATION_CHANNEL_ID)
                .setChannelName(R.string.native_playback_notification_channel)
                .build(),
        )
        setShowNotificationForIdlePlayer(SHOW_NOTIFICATION_FOR_IDLE_PLAYER_NEVER)
        ContextCompat.registerReceiver(
            this,
            noisyReceiver,
            IntentFilter(AudioManager.ACTION_AUDIO_BECOMING_NOISY),
            ContextCompat.RECEIVER_NOT_EXPORTED,
        )
        addSession(session)
        NativePlaybackRegistry.attach(this)
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        super.onStartCommand(intent, flags, startId)
        return START_NOT_STICKY
    }

    override fun onGetSession(controllerInfo: MediaSession.ControllerInfo): MediaSession? {
        if (registration == null) return null
        return if (controllerInfo.isTrusted || controllerInfo.uid == Process.myUid()) session else null
    }

    override fun onDestroy() {
        NativePlaybackRegistry.detach(this)
        try {
            unregisterReceiver(noisyReceiver)
        } catch (_: IllegalArgumentException) {
            // Receiver was already removed by the framework.
        }
        pollJob?.cancel()
        leaseJob?.cancel()
        leaseWatchdogJob?.cancel()
        identityJob?.cancel()
        activeExecution?.cancel()
        recoveryJob?.cancel()
        serviceJob.cancel()
        session.release()
        player.removeListener(this)
        player.release()
        registration = null
        currentResource = null
        super.onDestroy()
    }

    internal suspend fun connect(server: ServerEndpoint, name: String): JSONObject {
        val key = NativePlaybackPolicy.serverKey(server)
        stateKey = key
        registration?.let { existing ->
            if (existing.key == key) return copyJson(existing.device)
            if (currentResource?.terminalReported != true && currentResource != null) {
                throw NativePlaybackException("stop_required", "Stop phone playback before changing servers.")
            }
            disconnectRegistration(existing)
        }

        val client = try {
            AuthenticatedServerClient(server)
        } catch (error: Throwable) {
            val failure = NativePlaybackException("profile_unavailable", "The isolated server profile is unavailable.", error)
            setError(failure.code, failure.message.orEmpty())
            throw failure
        }
        val registrationStartedAt = SystemClock.elapsedRealtime()
        val response = try {
            client.json(
                method = "POST",
                path = REGISTRATIONS_PATH,
                body = JSONObject()
                    .put("name", name)
                    .put("protocol_info", JSONArray(NativePlaybackPolicy.protocolInfo))
                    .toString(),
            )
        } catch (error: CancellationException) {
            throw error
        } catch (error: Throwable) {
            val failure = error.toPublicFailure("registration_failed", "Phone playback could not connect.")
            setError(failure.code, failure.message.orEmpty())
            throw failure
        }
        val parsed = try {
            parseRegistration(response, server, key, client, registrationStartedAt)
        } catch (error: Throwable) {
            val id = response.optString("registration_id")
            val owner = response.optString("owner_token")
            if (id.isNotBlank() && owner.isNotBlank()) {
                try {
                    client.json(
                        method = "DELETE",
                        path = registrationPath(id),
                        ownerToken = owner,
                        allowEmpty = true,
                    )
                } catch (_: Throwable) {
                    // A malformed registration that cannot be released expires with its short lease.
                }
            }
            val failure = error.toPublicFailure("invalid_response", "The Server returned an invalid playback response.")
            setError(failure.code, failure.message.orEmpty())
            throw failure
        }
        registration = parsed
        leaseExpiryElapsed = parsed.leaseDeadlineElapsed
        sequenceFence = SequenceFence()
        publicError = null
        recovering = false
        startTransportLoops(parsed)
        notifyState()
        return copyJson(parsed.device)
    }

    internal suspend fun rename(server: ServerEndpoint, name: String): JSONObject {
        val existing = requireRegistration(server)
        val response = try {
            existing.client.json(
                method = "PUT",
                path = registrationPath(existing.id),
                ownerToken = existing.ownerToken,
                body = JSONObject().put("name", name).toString(),
            )
        } catch (error: CancellationException) {
            throw error
        } catch (error: Throwable) {
            handleTransportFailure(existing, error)
            throw error.toPublicFailure("rename_failed", "The phone output name could not be changed.")
        }
        val device = sanitizeDevice(response)
        existing.device = device
        publicError = null
        notifyState()
        return copyJson(device)
    }

    internal fun state(server: ServerEndpoint): JSONObject {
        val existing = registration
        val matches = try {
            (existing?.key ?: stateKey) == NativePlaybackPolicy.serverKey(server)
        } catch (_: RuntimeException) {
            false
        }
        if (!matches) return NativePlayback.emptyState()
        return JSONObject()
            .put("device", existing?.device?.let(::copyJson) ?: JSONObject.NULL)
            .put("recovering", recovering)
            .also { value ->
                publicError?.let { value.put("error", JSONObject().put("code", it.code).put("message", it.message)) }
            }
    }

    private fun requireRegistration(server: ServerEndpoint): Registration {
        val existing = registration
        if (existing == null || existing.key != NativePlaybackPolicy.serverKey(server)) {
            throw NativePlaybackException("not_connected", "Phone playback is not connected to this server.")
        }
        return existing
    }

    private fun startTransportLoops(expected: Registration) {
        pollJob?.cancel()
        leaseJob?.cancel()
        leaseWatchdogJob?.cancel()
        identityJob?.cancel()
        pollJob = scope.launch { pollCommands(expected) }
        leaseJob = scope.launch { renewLease(expected) }
        leaseWatchdogJob = scope.launch {
            while (registration === expected) {
                val remaining = leaseExpiryElapsed - SystemClock.elapsedRealtime()
                if (remaining <= 0L) {
                    loseRegistration(expected, "lease_expired", "The phone playback connection expired.")
                    return@launch
                }
                delay(remaining)
            }
        }
        identityJob = scope.launch { verifyIdentity(expected) }
    }

    private suspend fun pollCommands(expected: Registration) {
        var delayMillis = expected.pollAfterMillis
        while (currentCoroutineContext().isActive && registration === expected) {
            val startedAt = SystemClock.elapsedRealtime()
            try {
                val response = expected.client.json(
                    method = "GET",
                    path = "${registrationPath(expected.id)}/commands",
                    ownerToken = expected.ownerToken,
                )
                applyLease(expected, response, startedAt)
                if (registration !== expected) return
                delayMillis = response.optLong("poll_after_ms", expected.pollAfterMillis).coerceIn(100L, 1_000L)
                response.optJSONObject("command")?.let { command ->
                    val sequence = command.strictPositiveLong("sequence")
                    if (sequenceFence.shouldExecute(sequence)) executeSerialized(expected, command, sequence)
                }
            } catch (error: CancellationException) {
                throw error
            } catch (error: Throwable) {
                if (handleTransportFailure(expected, error)) return
                delayMillis = 1_000L
            }
            if (registration === expected) delay(delayMillis)
        }
    }

    private suspend fun renewLease(expected: Registration) {
        while (currentCoroutineContext().isActive && registration === expected) {
            delay(LEASE_RENEW_INTERVAL_MILLIS)
            val startedAt = SystemClock.elapsedRealtime()
            try {
                val response = expected.client.json(
                    method = "PUT",
                    path = "${registrationPath(expected.id)}/lease",
                    ownerToken = expected.ownerToken,
                    body = EMPTY_OBJECT,
                )
                applyLease(expected, response, startedAt)
                if (currentResource != null && activeExecution == null) {
                    sendObservationBestEffort(expected, "timeupdate")
                }
            } catch (error: CancellationException) {
                throw error
            } catch (error: Throwable) {
                if (handleTransportFailure(expected, error)) return
                if (SystemClock.elapsedRealtime() >= leaseExpiryElapsed) {
                    loseRegistration(expected, "lease_expired", "The phone playback connection expired.")
                    return
                }
            }
        }
    }

    private suspend fun verifyIdentity(expected: Registration) {
        while (currentCoroutineContext().isActive && registration === expected) {
            delay(IDENTITY_CHECK_INTERVAL_MILLIS)
            try {
                ServerProbe().probe(expected.server.origin, expected.server.id)
            } catch (error: ClientException) {
                when (error.code) {
                    ClientErrorCode.IDENTITY_MISMATCH,
                    ClientErrorCode.TLS,
                    ClientErrorCode.INVALID_METADATA,
                    ClientErrorCode.INCOMPATIBLE_SERVER -> {
                        loseRegistration(expected, "server_identity", "The server identity could not be verified.")
                        return
                    }
                    else -> Unit
                }
            }
        }
    }

    private fun applyLease(expected: Registration, response: JSONObject, startedAt: Long) {
        if (registration !== expected) return
        val duration = response.optLong("lease_duration_ms", -1L)
        val deadline = NativePlaybackPolicy.leaseDeadline(startedAt, duration)
        if (deadline == null) {
            loseRegistration(expected, "invalid_lease", "The phone playback connection ended.")
            return
        }
        leaseExpiryElapsed = maxOf(leaseExpiryElapsed, deadline)
        val cancelBefore = response.optLong("cancel_before_sequence", 0L)
        if (cancelBefore > 0) {
            sequenceFence.cancelBefore(cancelBefore)
            val active = activeExecution
            if (active != null && activeExecutionSequence <= cancelBefore) {
                active.cancel(CancellationException("Server cancelled media command"))
                cancelMediaWork(clearResource = true)
            } else if (currentResource?.sequence?.let { it <= cancelBefore } == true) {
                cancelMediaWork(clearResource = true)
            }
        }
    }

    private suspend fun executeSerialized(expected: Registration, command: JSONObject, sequence: Long) {
        val job = scope.launch(start = CoroutineStart.LAZY) {
            executeCommand(expected, command, sequence)
        }
        activeExecution = job
        activeExecutionSequence = sequence
        job.start()
        try {
            job.join()
        } finally {
            if (activeExecution === job) {
                activeExecution = null
                activeExecutionSequence = 0L
            }
        }
    }

    private suspend fun executeCommand(expected: Registration, command: JSONObject, sequence: Long) {
        val action = command.optString("action")
        try {
            val observation = when (action) {
                "set_uri" -> executeSetUri(expected, command, sequence)
                "play" -> executePlay(expected, command, sequence)
                "pause" -> executePause(command, sequence)
                "stop" -> executeStop(command, sequence)
                "seek" -> executeSeek(expected, command, sequence)
                else -> throw CommandFailure("action_failed", "Unsupported media action")
            }
            val body = reportBody(sequence, "succeeded", observation = observation)
            acknowledgingCommand = true
            try {
                sendFrozenReport(expected, body)
            } finally {
                acknowledgingCommand = false
            }
            sequenceFence.complete(sequence)
            currentResource?.sequence = sequence
            deferredPlaybackError?.let { deferred ->
                deferredPlaybackError = null
                currentResource?.let { recoverAfterCommand(expected, it, deferred) }
            }
            flushPendingTerminal(expected, sequence)
        } catch (error: CancellationException) {
            throw error
        } catch (error: Throwable) {
            if (action == "set_uri") cancelMediaWork(clearResource = true)
            val code = when (error) {
                is CommandFailure -> error.reportCode
                is PlaybackFailure -> error.reportCode
                else -> "action_failed"
            }
            val body = reportBody(sequence, "failed", errorCode = code)
            try {
                sendFrozenReport(expected, body)
                sequenceFence.complete(sequence)
            } catch (_: CancellationException) {
                throw CancellationException("Command report cancelled")
            }
            setError("playback_failed", getString(R.string.native_playback_error))
        }
    }

    private suspend fun executeSetUri(
        expected: Registration,
        command: JSONObject,
        sequence: Long,
    ): JSONObject {
        val playId = command.optString("play_id").takeIf { it.isNotBlank() }
            ?: throw CommandFailure("action_failed", "Missing media identity")
        val resource = command.optJSONObject("resource")
            ?: throw CommandFailure("action_failed", "Missing media resource")
        val mediaUrl = NativePlaybackPolicy.mediaUrl(expected.server.origin, resource.optString("url"))
            ?: throw CommandFailure("media_unsupported", "Unsafe media resource")
        val mime = resource.optString("mime").trim().lowercase()
        if (mime.isEmpty() || NativePlaybackPolicy.protocolInfo.none { supported ->
                supported.substringBefore(';').equals(mime.substringBefore(';'), ignoreCase = true)
            }
        ) {
            throw CommandFailure("media_unsupported", "Unsupported media type")
        }
        recoveryJob?.cancel()
        pendingTerminal = null
        recovering = false
        publicError = null
        player.stop()
        player.clearMediaItems()

        val metadata = MediaMetadata.Builder()
            .setTitle(resource.optString("title").take(512))
            .setArtist(resource.optString("artist").take(512))
            .setAlbumTitle(resource.optString("album").take(512))
            .setDurationMs(resource.optLong("duration_ms", 0L).coerceAtLeast(0L))
        val artworkValue = resource.optString("artwork_url")
        val artworkUrl = artworkValue.takeIf(String::isNotBlank)
            ?.let { NativePlaybackPolicy.sameOriginUrl(expected.server.origin, it) }
        if (artworkUrl != null) {
            val artwork = try {
                withTimeoutOrNull(ARTWORK_TIMEOUT_MILLIS) {
                    expected.client.bytes(artworkUrl, NativePlaybackPolicy.MAX_ARTWORK_BYTES)
                }
            } catch (error: CancellationException) {
                throw error
            } catch (error: Throwable) {
                if ((error as? ServerHttpException)?.status in listOf(401, 403) || hasCause<SSLException>(error)) {
                    handleTransportFailure(expected, error)
                    throw CancellationException("Artwork authentication was lost")
                }
                null
            }
            artwork?.let { metadata.setArtworkData(it, MediaMetadata.PICTURE_TYPE_FRONT_COVER) }
        }
        val item = MediaItem.Builder()
            .setMediaId(playId)
            .setUri(mediaUrl.toString())
            .setMimeType(mime)
            .setMediaMetadata(metadata.build())
            .build()
        val active = ActiveResource(playId, item, sequence)
        currentResource = active
        player.setMediaSource(expected.client.mediaSource(item))
        prepareWithRecovery(expected, active, startPositionMillis = 0L, playWhenReady = false)
        notifyState()
        return observation("loaded", playId)
    }

    private suspend fun executePlay(expected: Registration, command: JSONObject, sequence: Long): JSONObject {
        val active = requireActive(command, sequence)
        val interruptedRecovery = cancelBackgroundRecovery()
        active.wantsPlayback = true
        drainPlayerErrors()
        if (interruptedRecovery || player.playbackState == Player.STATE_IDLE) {
            prepareWithRecovery(expected, active, player.currentPosition.coerceAtLeast(0L), true)
        } else {
            player.play()
            val error = awaitPlayerEffect(EFFECT_TIMEOUT_MILLIS) { player.isPlaying }
            if (error != null) {
                recoverCommandMedia(expected, active, error, player.currentPosition.coerceAtLeast(0L), true)
            }
        }
        return observation("playing", active.playId)
    }

    private suspend fun executePause(command: JSONObject, sequence: Long): JSONObject {
        val active = requireActive(command, sequence)
        active.wantsPlayback = false
        player.pause()
        val error = awaitPlayerEffect(EFFECT_TIMEOUT_MILLIS) { !player.playWhenReady }
        if (error != null) throw playbackFailure(error)
        return observation("pause", active.playId)
    }

    private fun executeStop(command: JSONObject, sequence: Long): JSONObject {
        if (sequence <= sequenceFence.cancelBeforeSequence) throw CancellationException("Command cancelled")
        val playId = command.optString("play_id")
        if (currentResource?.playId?.let { it != playId } == true) {
            throw CommandFailure("action_failed", "Media identity changed")
        }
        val result = observation("stopped", playId)
        cancelMediaWork(clearResource = true)
        publicError = null
        notifyState()
        return result
    }

    private suspend fun executeSeek(expected: Registration, command: JSONObject, sequence: Long): JSONObject {
        val active = requireActive(command, sequence)
        val requested = command.optLong("position_ms", -1L)
        if (requested < 0L) throw CommandFailure("action_failed", "Invalid seek position")
        val interruptedRecovery = cancelBackgroundRecovery()
        val shouldPlay = active.wantsPlayback
        if (interruptedRecovery || player.playbackState == Player.STATE_IDLE) {
            player.setMediaSource(expected.client.mediaSource(active.mediaItem), requested)
            prepareWithRecovery(expected, active, requested, shouldPlay)
        } else {
            player.seekTo(requested)
            val error = awaitPlayerEffect(EFFECT_TIMEOUT_MILLIS) {
                kotlin.math.abs(player.currentPosition - requested) <= SEEK_TOLERANCE_MILLIS ||
                    (player.duration > 0L && requested >= player.duration && player.currentPosition >= player.duration - SEEK_TOLERANCE_MILLIS)
            }
            if (error != null) recoverCommandMedia(expected, active, error, requested, shouldPlay)
        }
        return observation("seeked", active.playId)
    }

    private suspend fun cancelBackgroundRecovery(): Boolean {
        val job = recoveryJob ?: return false
        job.cancelAndJoin()
        if (recoveryJob === job) recoveryJob = null
        return true
    }

    private fun requireActive(command: JSONObject, sequence: Long): ActiveResource {
        if (sequence <= sequenceFence.cancelBeforeSequence) throw CancellationException("Command cancelled")
        val active = currentResource ?: throw CommandFailure("action_failed", "No active media")
        if (command.optString("play_id") != active.playId) {
            throw CommandFailure("action_failed", "Media identity changed")
        }
        return active
    }
    private suspend fun recoverCommandMedia(
        expected: Registration,
        active: ActiveResource,
        error: PlaybackException,
        positionMillis: Long,
        playWhenReady: Boolean,
    ) {
        val classification = classifyPlayback(error)
        if (classification.fatalRegistration) {
            loseRegistration(expected, classification.publicCode, classification.publicMessage)
            throw CancellationException("Registration invalidated by media transport")
        }
        if (!classification.transient) {
            throw PlaybackFailure(classification.reportCode, classification.publicMessage)
        }
        prepareWithRecovery(
            expected,
            active,
            startPositionMillis = positionMillis,
            playWhenReady = playWhenReady,
            initialRetry = 1,
        )
    }


    private suspend fun prepareWithRecovery(
        expected: Registration,
        active: ActiveResource,
        startPositionMillis: Long,
        playWhenReady: Boolean,
        initialRetry: Int = 0,
    ) {
        val deadline = SystemClock.elapsedRealtime() + NativePlaybackPolicy.MEDIA_RECOVERY_DEADLINE_MILLIS
        var retry = initialRetry
        var position = startPositionMillis
        active.wantsPlayback = playWhenReady
        preparing = true
        try {
            while (registration === expected && currentResource === active) {
                currentCoroutineContext().ensureActive()
                if (retry > 0) {
                    val delayMillis = NativePlaybackPolicy.recoveryDelayMillis(retry)
                        ?: throw PlaybackFailure("media_error", "Media recovery exhausted")
                    val remaining = deadline - SystemClock.elapsedRealtime()
                    if (remaining <= delayMillis) throw PlaybackFailure("media_error", "Media recovery timed out")
                    setRecovering(true)
                    delay(delayMillis)
                    player.stop()
                    player.setMediaSource(expected.client.mediaSource(active.mediaItem), position)
                }
                drainPlayerErrors()
                player.prepare()
                if (active.wantsPlayback) player.play() else player.pause()
                val remaining = deadline - SystemClock.elapsedRealtime()
                if (remaining <= 0L) throw PlaybackFailure("media_error", "Media recovery timed out")
                val error = awaitPlayerEffect(remaining) {
                    player.playbackState == Player.STATE_READY && (!active.wantsPlayback || player.isPlaying)
                }
                if (error == null) {
                    setRecovering(false)
                    return
                }
                position = player.currentPosition.coerceAtLeast(position)
                val classification = classifyPlayback(error)
                if (!classification.transient || retry >= NativePlaybackPolicy.MAX_MEDIA_RETRIES) {
                    if (classification.fatalRegistration) {
                        loseRegistration(expected, classification.publicCode, classification.publicMessage)
                        throw CancellationException("Registration invalidated by media transport")
                    }
                    throw PlaybackFailure(classification.reportCode, classification.publicMessage)
                }
                retry++
            }
            throw CancellationException("Media source changed")
        } finally {
            preparing = false
        }
    }

    private suspend fun awaitPlayerEffect(timeoutMillis: Long, predicate: () -> Boolean): PlaybackException? {
        val deadline = SystemClock.elapsedRealtime() + timeoutMillis.coerceAtLeast(1L)
        while (!predicate()) {
            currentCoroutineContext().ensureActive()
            if (playerErrors.isNotEmpty()) return playerErrors.removeFirst()
            if (SystemClock.elapsedRealtime() >= deadline) {
                return PlaybackException("Media operation timed out", null, PlaybackException.ERROR_CODE_TIMEOUT)
            }
            delay(25L)
        }
        return null
    }

    private fun drainPlayerErrors() {
        playerErrors.clear()
    }

    override fun onPlayerError(error: PlaybackException) {
        playerErrors.addLast(error)
        if (preparing) return
        if (activeExecution != null) {
            if (acknowledgingCommand) deferredPlaybackError = error
            return
        }
        val expected = registration ?: return
        val active = currentResource ?: return
        recoverAfterCommand(expected, active, error)
    }

    private fun recoverAfterCommand(
        expected: Registration,
        active: ActiveResource,
        error: PlaybackException,
    ) {
        if (recoveryJob != null || currentResource !== active || active.terminalReported) return
        val classification = classifyPlayback(error)
        if (!classification.transient) {
            if (classification.fatalRegistration) {
                loseRegistration(expected, classification.publicCode, classification.publicMessage)
            } else {
                finishTerminalError(expected, active, classification.publicMessage)
            }
            return
        }
        val position = player.currentPosition.coerceAtLeast(0L)
        val shouldPlay = active.wantsPlayback
        recoveryJob = scope.launch {
            setRecovering(true)
            try {
                prepareWithRecovery(expected, active, position, shouldPlay, initialRetry = 1)
            } catch (error: CancellationException) {
                throw error
            } catch (error: Throwable) {
                finishTerminalError(expected, active, getString(R.string.native_playback_error))
            } finally {
                if (recoveryJob === currentCoroutineContext()[Job]) recoveryJob = null
                if (registration === expected && currentResource === active) setRecovering(false)
            }
        }
    }

    override fun onPlaybackStateChanged(playbackState: Int) {
        if (playbackState != Player.STATE_ENDED || currentResource == null || preparing || recovering) return
        val expected = registration ?: return
        val active = currentResource ?: return
        if (active.terminalReported) return
        active.terminalReported = true
        val event = TerminalEvent("ended", active.playId, active.sequence)
        if (activeExecution != null) {
            pendingTerminal = event
        } else {
            scope.launch { sendTerminalObservation(expected, event) }
        }
    }

    override fun onPlayWhenReadyChanged(playWhenReady: Boolean, reason: Int) {
        if (!playWhenReady && reason == Player.PLAY_WHEN_READY_CHANGE_REASON_AUDIO_FOCUS_LOSS && currentResource != null) {
            currentResource?.wantsPlayback = false
            sendPlayerControl("pause")
        }
    }

    private fun finishTerminalError(expected: Registration, active: ActiveResource, message: String) {
        if (active.terminalReported || currentResource !== active) return
        active.terminalReported = true
        player.stop()
        setError("media_error", message)
        val event = TerminalEvent("error", active.playId, active.sequence)
        if (activeExecution != null) pendingTerminal = event else scope.launch { sendTerminalObservation(expected, event) }
    }

    private suspend fun flushPendingTerminal(expected: Registration, sequence: Long) {
        val terminal = pendingTerminal ?: return
        pendingTerminal = null
        sendTerminalObservation(expected, terminal.copy(sequence = sequence))
    }

    private suspend fun sendTerminalObservation(expected: Registration, event: TerminalEvent) {
        if (registration !== expected || event.sequence <= 0L) return
        val active = currentResource?.takeIf { it.playId == event.playId && it.sequence == event.sequence } ?: return
        val body = reportBody(event.sequence, observation = observation(event.event, event.playId))
        try {
            sendFrozenReport(expected, body, active)
        } catch (_: Throwable) {
            // Lease/auth handling in sendFrozenReport owns the terminal failure path.
        }
    }

    private fun sendObservationBestEffort(expected: Registration, event: String) {
        val active = currentResource ?: return
        if (active.sequence <= 0L || active.terminalReported) return
        val body = reportBody(active.sequence, observation = observation(event, active.playId, includeState = true))
        scope.launch {
            try {
                expected.client.json(
                    method = "POST",
                    path = "${registrationPath(expected.id)}/reports",
                    ownerToken = expected.ownerToken,
                    body = body,
                    allowEmpty = true,
                )
            } catch (error: Throwable) {
                handleTransportFailure(expected, error)
            }
        }
    }

    private suspend fun sendFrozenReport(expected: Registration, body: String, expectedResource: ActiveResource? = null) {
        var retryDelay = REPORT_RETRY_INITIAL_MILLIS
        while (registration === expected) {
            currentCoroutineContext().ensureActive()
            if (expectedResource != null && currentResource !== expectedResource) {
                throw CancellationException("Reported media was superseded")
            }
            if (SystemClock.elapsedRealtime() >= leaseExpiryElapsed) {
                loseRegistration(expected, "lease_expired", "The phone playback connection expired.")
                throw CancellationException("Lease expired")
            }
            try {
                expected.client.json(
                    method = "POST",
                    path = "${registrationPath(expected.id)}/reports",
                    ownerToken = expected.ownerToken,
                    body = body,
                    allowEmpty = true,
                )
                return
            } catch (error: CancellationException) {
                throw error
            } catch (error: Throwable) {
                val http = error as? ServerHttpException
                if (http?.serverCode == "STALE_BROWSER_REPORT" || http?.status == 409) {
                    if (expectedResource != null) return
                    throw CancellationException("Server rejected stale media report")
                }
                if (handleTransportFailure(expected, error)) throw CancellationException("Registration lost")
                val remaining = leaseExpiryElapsed - SystemClock.elapsedRealtime()
                if (remaining <= retryDelay) {
                    loseRegistration(expected, "lease_expired", "The phone playback connection expired.")
                    throw CancellationException("Report retry exceeded lease")
                }
                delay(retryDelay)
                retryDelay = (retryDelay * 2).coerceAtMost(REPORT_RETRY_MAX_MILLIS)
            }
        }
        throw CancellationException("Registration changed")
    }

    private fun observation(event: String, playId: String, includeState: Boolean = false): JSONObject {
        val position = player.currentPosition.takeIf { it >= 0L } ?: 0L
        val duration = player.duration.takeIf { it > 0L && it != C.TIME_UNSET }
            ?: currentResource?.mediaItem?.mediaMetadata?.durationMs?.takeIf { it > 0L }
            ?: 0L
        return JSONObject()
            .put("event", event)
            .put("play_id", playId)
            .put("position_ms", position)
            .put("duration_ms", duration)
            .put("has_position", currentResource != null)
            .also { value ->
                // Like HTML audio.paused, transport intent survives buffering; position is never synthesized.
                if (includeState || event == "seeked") value.put("state", if (currentResource?.wantsPlayback == true) "playing" else "paused")
            }
    }

    private fun reportBody(
        sequence: Long,
        result: String? = null,
        errorCode: String? = null,
        observation: JSONObject? = null,
    ): String = JSONObject()
        .put("sequence", sequence)
        .also { value ->
            if (result != null) value.put("result", result)
            if (errorCode != null) value.put("error_code", errorCode)
            if (observation != null) value.put("observation", observation)
        }
        .toString()

    private fun sendPlayerControl(action: String, positionMillis: Long? = null) {
        val expected = registration ?: return
        scope.launch {
            controlMutex.withLock {
                if (registration !== expected) return@withLock
                try {
                    expected.client.json(
                        method = "POST",
                        path = PLAYER_PATH,
                        body = JSONObject()
                            .put("action", action)
                            .also { if (positionMillis != null) it.put("position_ms", positionMillis) }
                            .toString(),
                    )
                    publicError = null
                    notifyState()
                } catch (error: Throwable) {
                    if (!handleTransportFailure(expected, error)) {
                        setError("control_failed", "The Server could not apply the media control.")
                    }
                }
            }
        }
    }

    private fun handleTransportFailure(expected: Registration, error: Throwable): Boolean {
        if (registration !== expected) return true
        val http = error as? ServerHttpException
        val fatal = http?.status in listOf(401, 403, 404) || hasCause<SSLException>(error)
        if (fatal) {
            val code = if (hasCause<SSLException>(error)) "server_tls" else "authentication_required"
            val message = if (code == "server_tls") "The server certificate could not be verified." else "Sign in again to use phone playback."
            loseRegistration(expected, code, message)
        }
        return fatal
    }

    private fun loseRegistration(expected: Registration, code: String, message: String) {
        if (registration !== expected) return
        registration = null
        pollJob?.cancel()
        leaseJob?.cancel()
        leaseWatchdogJob?.cancel()
        identityJob?.cancel()
        activeExecution?.cancel()
        cancelMediaWork(clearResource = true)
        setError(code, message)
    }

    private fun cancelMediaWork(clearResource: Boolean) {
        recoveryJob?.cancel()
        recoveryJob = null
        preparing = false
        recovering = false
        pendingTerminal = null
        acknowledgingCommand = false
        deferredPlaybackError = null
        player.stop()
        player.clearMediaItems()
        if (clearResource) currentResource = null
        notifyState()
    }

    private suspend fun disconnectRegistration(existing: Registration) {
        pollJob?.cancel()
        leaseJob?.cancel()
        leaseWatchdogJob?.cancel()
        identityJob?.cancel()
        activeExecution?.cancel()
        recoveryJob?.cancel()
        acknowledgingCommand = false
        deferredPlaybackError = null
        try {
            existing.client.json(
                method = "DELETE",
                path = registrationPath(existing.id),
                ownerToken = existing.ownerToken,
                allowEmpty = true,
            )
        } catch (_: Throwable) {
            // The lease bounds stale registrations when a best-effort disconnect cannot arrive.
        }
        if (registration === existing) registration = null
        cancelMediaWork(clearResource = true)
    }

    private fun setRecovering(value: Boolean) {
        if (recovering == value) return
        recovering = value
        if (value) publicError = null
        notifyState()
    }

    private fun setError(code: String, message: String) {
        recovering = false
        publicError = PublicError(code, message)
        notifyState()
    }

    private fun notifyState() {
        NativePlaybackRegistry.notifyObservers()
    }

    private fun parseRegistration(
        value: JSONObject,
        server: ServerEndpoint,
        key: String,
        client: AuthenticatedServerClient,
        startedAt: Long,
    ): Registration {
        val id = value.strictString("registration_id", 256)
        val ownerToken = value.strictString("owner_token", 256)
        val leaseDuration = value.optLong("lease_duration_ms", -1L)
        val leaseDeadline = NativePlaybackPolicy.leaseDeadline(startedAt, leaseDuration)
            ?: throw NativePlaybackException("invalid_response", "The Server returned an invalid playback lease.")
        if (leaseDeadline <= SystemClock.elapsedRealtime()) {
            throw NativePlaybackException("invalid_response", "The Server returned an expired playback lease.")
        }
        val pollAfter = value.optLong("poll_after_ms", 250L).coerceIn(100L, 1_000L)
        return Registration(
            server = server,
            key = key,
            id = id,
            ownerToken = ownerToken,
            device = sanitizeDevice(value.getJSONObject("device")),
            leaseDeadlineElapsed = leaseDeadline,
            pollAfterMillis = pollAfter,
            client = client,
        )
    }

    private fun sanitizeDevice(source: JSONObject): JSONObject {
        val result = JSONObject()
        listOf("id", "name", "manufacturer", "model", "address", "last_seen", "protocol").forEach { key ->
            result.put(key, source.optString(key))
        }
        listOf("online", "pairing_required", "password_required").forEach { key ->
            result.put(key, source.optBoolean(key))
        }
        val sourceCapabilities = source.optJSONObject("capabilities") ?: JSONObject()
        val capabilities = JSONObject()
        listOf("play", "pause", "stop", "seek").forEach { key ->
            capabilities.put(key, sourceCapabilities.optBoolean(key))
        }
        val protocolInfo = JSONArray()
        source.optJSONArray("protocol_info")?.let { values ->
            for (index in 0 until values.length()) {
                values.optString(index).takeIf { it.isNotBlank() && it.length <= 256 }?.let(protocolInfo::put)
            }
        }
        result.put("capabilities", capabilities)
        result.put("protocol_info", protocolInfo)
        if (result.optString("id").isBlank() || result.optString("name").isBlank()) {
            throw NativePlaybackException("invalid_response", "The Server returned an invalid phone output.")
        }
        return result
    }

    private fun classifyPlayback(error: PlaybackException): PlaybackClassification {
        val httpStatus = causeChain(error).filterIsInstance<HttpDataSource.InvalidResponseCodeException>()
            .firstOrNull()?.responseCode
        val transient = NativePlaybackPolicy.isTransientMediaError(error.errorCode, httpStatus, error)
        val unsupported = error.errorCode in 3001..4005
        val fatalRegistration = httpStatus == 401 || hasCause<SSLException>(error)
        return PlaybackClassification(
            transient = transient,
            fatalRegistration = fatalRegistration,
            reportCode = if (unsupported) "media_unsupported" else "media_error",
            publicCode = if (fatalRegistration) "authentication_required" else "media_error",
            publicMessage = if (unsupported) "This audio format is not supported." else getString(R.string.native_playback_error),
        )
    }

    private fun playbackFailure(error: PlaybackException): PlaybackFailure {
        val classified = classifyPlayback(error)
        return PlaybackFailure(classified.reportCode, classified.publicMessage)
    }

    private inner class ServerRoutedPlayer(player: Player) : ForwardingPlayer(player) {
        private val commands = Player.Commands.Builder()
            .addAllReadOnlyCommands()
            .addAll(
                Player.COMMAND_PLAY_PAUSE,
                Player.COMMAND_STOP,
                Player.COMMAND_SEEK_IN_CURRENT_MEDIA_ITEM,
                Player.COMMAND_SEEK_TO_PREVIOUS,
                Player.COMMAND_SEEK_TO_PREVIOUS_MEDIA_ITEM,
                Player.COMMAND_SEEK_TO_NEXT,
                Player.COMMAND_SEEK_TO_NEXT_MEDIA_ITEM,
            )
            .build()
        private val listenerWrappers = IdentityHashMap<Player.Listener, Player.Listener>()

        override fun addListener(listener: Player.Listener) {
            synchronized(listenerWrappers) {
                val wrapped = listenerWrappers.getOrPut(listener) {
                    object : Player.Listener by listener {
                        override fun onAvailableCommandsChanged(availableCommands: Player.Commands) {
                            // Server controls stay available when the single-item decoder changes commands.
                        }

                        override fun onEvents(player: Player, events: Player.Events) {
                            if (!events.contains(Player.EVENT_AVAILABLE_COMMANDS_CHANGED)) {
                                listener.onEvents(player, events)
                                return
                            }
                            if (events.size() == 1) return
                            val flags = FlagSet.Builder()
                            for (index in 0 until events.size()) {
                                val event = events.get(index)
                                if (event != Player.EVENT_AVAILABLE_COMMANDS_CHANGED) flags.add(event)
                            }
                            listener.onEvents(player, Player.Events(flags.build()))
                        }
                    }
                }
                super.addListener(wrapped)
            }
        }

        override fun removeListener(listener: Player.Listener) {
            synchronized(listenerWrappers) {
                val wrapped = listenerWrappers.remove(listener) ?: return
                super.removeListener(wrapped)
            }
        }

        override fun getAvailableCommands(): Player.Commands = commands
        override fun isCommandAvailable(command: Int): Boolean = commands.contains(command)
        override fun play() = sendPlayerControl("play")
        override fun pause() = sendPlayerControl("pause")
        override fun setPlayWhenReady(playWhenReady: Boolean) = sendPlayerControl(if (playWhenReady) "play" else "pause")
        override fun stop() = sendPlayerControl("stop")
        override fun seekTo(positionMs: Long) = sendPlayerControl("seek", positionMs.coerceAtLeast(0L))
        override fun seekTo(mediaItemIndex: Int, positionMs: Long) = seekTo(positionMs)
        override fun seekToNext() = sendPlayerControl("next")
        override fun seekToNextMediaItem() = sendPlayerControl("next")
        override fun seekToPrevious() = sendPlayerControl("previous")
        override fun seekToPreviousMediaItem() = sendPlayerControl("previous")
        override fun hasNextMediaItem(): Boolean = registration != null
        override fun hasPreviousMediaItem(): Boolean = registration != null
        override fun prepare() = Unit
    }

    private inner class SessionCallback : MediaSession.Callback {
        override fun onConnect(
            session: MediaSession,
            controller: MediaSession.ControllerInfo,
        ): MediaSession.ConnectionResult {
            val allowed = controller.isTrusted || controller.uid == Process.myUid() ||
                session.isMediaNotificationController(controller)
            if (!allowed) return MediaSession.ConnectionResult.reject()
            return MediaSession.ConnectionResult.AcceptedResultBuilder()
                .setAvailableSessionCommands(SessionCommands.EMPTY)
                .setAvailablePlayerCommands(SYSTEM_PLAYER_COMMANDS)
                .build()
        }
    }

    private data class Registration(
        val server: ServerEndpoint,
        val key: String,
        val id: String,
        val ownerToken: String,
        var device: JSONObject,
        val leaseDeadlineElapsed: Long,
        val pollAfterMillis: Long,
        val client: AuthenticatedServerClient,
    )

    private data class ActiveResource(
        val playId: String,
        val mediaItem: MediaItem,
        var sequence: Long,
        var wantsPlayback: Boolean = false,
        var terminalReported: Boolean = false,
    )

    private data class PublicError(val code: String, val message: String)
    private data class TerminalEvent(val event: String, val playId: String, val sequence: Long)
    private data class PlaybackClassification(
        val transient: Boolean,
        val fatalRegistration: Boolean,
        val reportCode: String,
        val publicCode: String,
        val publicMessage: String,
    )

    private class CommandFailure(val reportCode: String, message: String) : Exception(message)
    private class PlaybackFailure(val reportCode: String, message: String) : Exception(message)

    companion object {
        private val SYSTEM_PLAYER_COMMANDS = Player.Commands.Builder()
            .addAllReadOnlyCommands()
            .addAll(
                Player.COMMAND_PLAY_PAUSE,
                Player.COMMAND_STOP,
                Player.COMMAND_SEEK_IN_CURRENT_MEDIA_ITEM,
                Player.COMMAND_SEEK_TO_PREVIOUS,
                Player.COMMAND_SEEK_TO_PREVIOUS_MEDIA_ITEM,
                Player.COMMAND_SEEK_TO_NEXT,
                Player.COMMAND_SEEK_TO_NEXT_MEDIA_ITEM,
            )
            .build()
        private const val REGISTRATIONS_PATH = "/api/v1/browser-output/registrations"
        private const val PLAYER_PATH = "/api/v1/player"
        private const val EMPTY_OBJECT = "{}"
        private const val NOTIFICATION_CHANNEL_ID = "jastreamer_playback"
        private const val LEASE_RENEW_INTERVAL_MILLIS = 2_000L
        private const val IDENTITY_CHECK_INTERVAL_MILLIS = 10_000L
        private const val REPORT_RETRY_INITIAL_MILLIS = 150L
        private const val REPORT_RETRY_MAX_MILLIS = 1_000L
        private const val EFFECT_TIMEOUT_MILLIS = 4_000L
        private const val ARTWORK_TIMEOUT_MILLIS = 1_500L
        private const val SEEK_TOLERANCE_MILLIS = 250L

        private fun registrationPath(id: String): String = "$REGISTRATIONS_PATH/${java.net.URLEncoder.encode(id, Charsets.UTF_8.name())}"
        private fun copyJson(value: JSONObject): JSONObject = JSONObject(value.toString())
    }
}

@OptIn(UnstableApi::class)
private class AuthenticatedServerClient(val server: ServerEndpoint) {
    private val origin = EndpointPolicy.normalizeOrigin(server.origin)
    private val originUrl = origin.toHttpUrl()
    private val cookieManager: CookieManager = run {
        if (!WebViewFeature.isFeatureSupported(WebViewFeature.MULTI_PROFILE)) {
            throw NativePlaybackException("profile_unavailable", "The isolated server profile is unavailable.")
        }
        ProfileStore.getInstance().getOrCreateProfile(EndpointPolicy.profileName(server)).cookieManager
    }
    private val client = OkHttpClient.Builder()
        .connectTimeout(4, TimeUnit.SECONDS)
        .readTimeout(4, TimeUnit.SECONDS)
        .writeTimeout(4, TimeUnit.SECONDS)
        .callTimeout(5, TimeUnit.SECONDS)
        .followRedirects(false)
        .followSslRedirects(false)
        .cookieJar(CookieJar.NO_COOKIES)
        .addInterceptor { chain ->
            val request = chain.request()
            val url = request.url
            if (url.scheme != originUrl.scheme || url.host != originUrl.host || url.port != originUrl.port) {
                throw ServerHttpException(0, "INVALID_URL")
            }
            val authenticated = request.newBuilder()
            val cookie = cookieManager.getCookie(url.toString())
            if (cookie.isNullOrBlank()) authenticated.removeHeader("Cookie")
            else authenticated.header("Cookie", cookie)
            chain.proceed(authenticated.build())
        }
        .build()
    private val mediaClient = client.newBuilder()
        .connectTimeout(3, TimeUnit.SECONDS)
        .readTimeout(3, TimeUnit.SECONDS)
        .callTimeout(0, TimeUnit.SECONDS)
        .retryOnConnectionFailure(false)
        .build()
    private val mediaFactory = DefaultMediaSourceFactory(
        OkHttpDataSource.Factory(mediaClient).setUserAgent("jastreamer-android"),
    ).setLoadErrorHandlingPolicy(object : DefaultLoadErrorHandlingPolicy(0) {
        override fun getRetryDelayMsFor(loadErrorInfo: LoadErrorHandlingPolicy.LoadErrorInfo): Long = C.TIME_UNSET
    })

    fun mediaSource(item: MediaItem): MediaSource = mediaFactory.createMediaSource(item)

    suspend fun json(
        method: String,
        path: String,
        ownerToken: String? = null,
        body: String? = null,
        allowEmpty: Boolean = false,
    ): JSONObject {
        val target = originUrl.resolve(path) ?: throw ServerHttpException(0, "INVALID_URL")
        return execute(request(target, method, ownerToken, body)) { response ->
            if (!response.isSuccessful) throw httpFailure(response)
            val raw = readBounded(response.body, MAX_JSON_BYTES).toString(Charsets.UTF_8)
            if (raw.isEmpty() && allowEmpty) JSONObject()
            else try {
                JSONObject(raw)
            } catch (error: JSONException) {
                throw ServerHttpException(response.code, "INVALID_RESPONSE", error)
            }
        }
    }

    suspend fun bytes(url: HttpUrl, maximum: Int): ByteArray =
        execute(request(url, "GET", null, null)) { response ->
            if (!response.isSuccessful) throw httpFailure(response)
            readBounded(response.body, maximum)
        }

    private fun request(url: HttpUrl, method: String, ownerToken: String?, body: String?): Request {
        val builder = Request.Builder()
            .url(url)
            .header("Accept", "application/json")
            .header("Cache-Control", "no-store")
        ownerToken?.let { builder.header(NativePlaybackPolicy.OWNER_HEADER, it) }
        if (method != "GET" && method != "HEAD") {
            builder.header("Origin", origin)
            builder.header("X-Jastreamer-Request", "web")
        }
        val requestBody = body?.toRequestBody(JSON_MEDIA_TYPE)
        return when (method) {
            "GET" -> builder.get().build()
            "POST" -> builder.post(requestBody ?: EMPTY_REQUEST_BODY).build()
            "PUT" -> builder.put(requestBody ?: EMPTY_REQUEST_BODY).build()
            "DELETE" -> builder.delete().build()
            else -> throw IllegalArgumentException("Unsupported HTTP method")
        }
    }

    private suspend fun <T> execute(request: Request, consume: (Response) -> T): T =
        suspendCancellableCoroutine { continuation ->
            val call = client.newCall(request)
            continuation.invokeOnCancellation { call.cancel() }
            call.enqueue(object : Callback {
                override fun onFailure(call: Call, error: IOException) {
                    if (continuation.isActive) continuation.resumeWith(Result.failure(error))
                }

                override fun onResponse(call: Call, response: Response) {
                    if (!continuation.isActive) {
                        response.close()
                        return
                    }
                    // Consume on OkHttp's worker while cancellation still owns the whole call.
                    val result = runCatching {
                        response.use {
                            it.headers("Set-Cookie").forEach { cookie -> cookieManager.setCookie(origin, cookie) }
                            consume(it)
                        }
                    }
                    if (continuation.isActive) continuation.resumeWith(result)
                }
            })
        }

    private fun httpFailure(response: Response): ServerHttpException {
        val raw = try {
            readBounded(response.body, MAX_JSON_BYTES).toString(Charsets.UTF_8)
        } catch (_: Throwable) {
            ""
        }
        val code = try {
            JSONObject(raw).optJSONObject("error")?.optString("code").orEmpty()
        } catch (_: JSONException) {
            ""
        }
        return ServerHttpException(response.code, code.ifBlank { "HTTP_${response.code}" })
    }

    private fun readBounded(body: ResponseBody?, maximum: Int): ByteArray {
        if (body == null) return ByteArray(0)
        if (body.contentLength() > maximum) throw ServerHttpException(0, "RESPONSE_TOO_LARGE")
        val source = body.source()
        source.request(maximum.toLong() + 1)
        if (source.buffer.size > maximum) throw ServerHttpException(0, "RESPONSE_TOO_LARGE")
        return source.readByteArray()
    }

    companion object {
        private const val MAX_JSON_BYTES = 64 * 1024
        private val JSON_MEDIA_TYPE = "application/json; charset=utf-8".toMediaType()
        private val EMPTY_REQUEST_BODY = ByteArray(0).toRequestBody(null)
    }
}

private class ServerHttpException(
    val status: Int,
    val serverCode: String,
    cause: Throwable? = null,
) : IOException(serverCode, cause)

private fun JSONObject.strictString(key: String, maximum: Int): String {
    val value = optString(key)
    if (value.isBlank() || value.length > maximum || value.any(Char::isISOControl)) {
        throw NativePlaybackException("invalid_response", "The Server returned an invalid playback response.")
    }
    return value
}

private fun JSONObject.strictPositiveLong(key: String): Long {
    val value = optLong(key, -1L)
    if (value <= 0L) throw NativePlaybackException("invalid_response", "The Server returned an invalid playback command.")
    return value
}

private inline fun <reified T : Throwable> hasCause(error: Throwable): Boolean =
    causeChain(error).any { it is T }

private fun causeChain(error: Throwable): Sequence<Throwable> = sequence {
    var current: Throwable? = error
    val seen = HashSet<Throwable>()
    while (current != null && seen.add(current)) {
        yield(current)
        current = current.cause
    }
}

private fun Throwable.toPublicFailure(defaultCode: String, defaultMessage: String): NativePlaybackException = when (this) {
    is NativePlaybackException -> this
    is ServerHttpException -> when (status) {
        401, 403 -> NativePlaybackException("authentication_required", "Sign in again to use phone playback.", this)
        404 -> NativePlaybackException("registration_expired", "The phone playback connection expired.", this)
        else -> NativePlaybackException(defaultCode, defaultMessage, this)
    }
    else -> if (hasCause<SSLException>(this)) {
        NativePlaybackException("server_tls", "The server certificate could not be verified.", this)
    } else {
        NativePlaybackException(defaultCode, defaultMessage, this)
    }
}
