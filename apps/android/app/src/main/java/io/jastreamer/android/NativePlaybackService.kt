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
import androidx.media3.common.ForwardingSimpleBasePlayer
import androidx.media3.common.Format
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.datasource.HttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.analytics.AnalyticsListener
import androidx.media3.exoplayer.audio.AudioSink
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
import com.google.common.util.concurrent.Futures
import com.google.common.util.concurrent.ListenableFuture
import java.io.Closeable
import java.io.IOException
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
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.withContext
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
    private lateinit var routedPlayer: OwnerRoutedPlayer
    private lateinit var offlineLibrary: OfflineLibrary
    private lateinit var offlineMediaFactory: DefaultMediaSourceFactory
    private var owner = PlaybackOwner.NONE
    private var ownershipGeneration = 0L
    private var offlineQueue = OfflineQueue()
    private var offlineError: PublicError? = null
    private var offlineGeneration = 0L
    private var offlineCommandGeneration = 0L
    private var offlinePersistRevision = 0L
    private val offlinePersistMutex = Mutex()
    private var offlineTickerJob: Job? = null
    private var offlineLibraryJob: Job? = null
    private val offlineFailedEntryIds = LinkedHashSet<String>()
    private var offlinePendingEntryId: String? = null
    private var offlineRetainedEntryId: String? = null
    private var offlineRetention: Closeable? = null
    private var registration: Registration? = null
    private var handoffRegistration: Registration? = null
    private var handoffPollRemoval: Registration? = null
    private var stateKey: String? = null
    private var currentResource: ActiveResource? = null
    private var activeExecution: Job? = null
    private var activeExecutionSequence = 0L
    private var recoveryJob: Job? = null
    private var pollJob: Job? = null
    private var leaseJob: Job? = null
    private var leaseWatchdogJob: Job? = null
    private var identityJob: Job? = null
    private val serverSideJobs = LinkedHashSet<Job>()
    private var preparing = false
    private var recovering = false
    private var publicError: PublicError? = null
    private var leaseExpiryElapsed = 0L
    private var sequenceFence = SequenceFence()
    private val controlMutex = Mutex()
    private val playerErrors = ArrayDeque<PlayerFailure>()
    private var pendingTerminal: TerminalEvent? = null
    private var acknowledgingCommand = false
    private var deferredPlaybackError: PlayerFailure? = null
    private lateinit var bitPerfect: UsbBitPerfectController
    private var playerReleased = false
    private var audioTrackStream: PcmStream? = null
    private var audioTrackGeneration = -1L
    private var bitPerfectSource: SourceStream? = null
    private var bitPerfectError: PublicError? = null

    private val noisyReceiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context?, intent: Intent?) {
            if (intent?.action != AudioManager.ACTION_AUDIO_BECOMING_NOISY) return
            when (owner) {
                PlaybackOwner.LOCAL -> pauseOffline()
                PlaybackOwner.SERVER -> {
                    if (currentResource == null) return
                    currentResource?.wantsPlayback = false
                    player.pause()
                    sendPlayerControl("pause")
                }
                PlaybackOwner.NONE -> Unit
            }
        }
    }

    override fun onCreate() {
        super.onCreate()
        offlineLibrary = OfflineLibrary.get(applicationContext)
        offlineMediaFactory = DefaultMediaSourceFactory(OfflineAudioDataSource.Factory(offlineLibrary))
            .setLoadErrorHandlingPolicy(object : DefaultLoadErrorHandlingPolicy(0) {
                override fun getRetryDelayMsFor(loadErrorInfo: LoadErrorHandlingPolicy.LoadErrorInfo): Long = C.TIME_UNSET
            })
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
                it.addAnalyticsListener(AudioTrackObserver())
            }
        bitPerfect = UsbBitPerfectController(this) { notifyState() }
        routedPlayer = OwnerRoutedPlayer(player)
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
        setShowNotificationForIdlePlayer(SHOW_NOTIFICATION_FOR_IDLE_PLAYER_AFTER_STOP_OR_ERROR)
        ContextCompat.registerReceiver(
            this,
            noisyReceiver,
            IntentFilter(AudioManager.ACTION_AUDIO_BECOMING_NOISY),
            ContextCompat.RECEIVER_NOT_EXPORTED,
        )
        addSession(session)
        NativePlaybackRegistry.attach(this)
        offlineLibraryJob = scope.launch {
            offlineLibrary.changes.collect { reconcileOfflineLibrary() }
        }
        val restoreGeneration = offlineGeneration
        scope.launch {
            val restored = withContext(Dispatchers.IO) {
                OfflinePlaybackPolicy.normalized(offlineLibrary.loadQueue())
            }
            if (restoreGeneration == offlineGeneration && offlineQueue.entries.isEmpty()) {
                offlineQueue = restored
                publishOfflineState()
            }
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        super.onStartCommand(intent, flags, startId)
        return START_NOT_STICKY
    }

    override fun onGetSession(controllerInfo: MediaSession.ControllerInfo): MediaSession? {
        if (owner == PlaybackOwner.NONE) return null
        return if (controllerInfo.isTrusted || controllerInfo.uid == Process.myUid()) session else null
    }

    override fun onDestroy() {
        NativePlaybackRegistry.detach(this)
        try {
            unregisterReceiver(noisyReceiver)
        } catch (_: IllegalArgumentException) {
            // Receiver was already removed by the framework.
        }
        cancelServerTransport()
        offlineTickerJob?.cancel()
        offlineLibraryJob?.cancel()
        player.stop()
        releaseOfflineRetention()
        serviceJob.cancel()
        session.release()
        player.removeListener(this)
        // The USB preference outlives an AudioTrack, so it is released before the player is.
        bitPerfect.release()
        playerReleased = true
        routedPlayer.release()
        setOwner(PlaybackOwner.NONE)
        registration = null
        currentResource = null
        publishOfflineState()
        super.onDestroy()
    }

    internal fun prepareServerHandoff(
        confirmHandoff: Boolean,
        requestGeneration: Long,
    ): OfflinePlaybackPolicy.OwnershipRequestToken {
        requireCurrentOfflineRequest(requestGeneration)
        if (!OfflinePlaybackPolicy.requiresHandoff(ownerName(), OfflinePlaybackPolicy.OWNER_SERVER)) {
            return OfflinePlaybackPolicy.OwnershipRequestToken(ownershipGeneration, requestGeneration)
        }
        if (!confirmHandoff) {
            throw NativePlaybackException(
                "handoff_required",
                "Stop saved music and switch this phone to Server playback?",
            )
        }
        offlineCommandGeneration++
        captureOfflinePosition()
        persistOfflineQueue()
        offlineGeneration++
        offlineTickerJob?.cancel()
        offlineTickerJob = null
        offlineFailedEntryIds.clear()
        setOwner(PlaybackOwner.NONE)
        player.stop()
        player.clearMediaItems()
        player.repeatMode = Player.REPEAT_MODE_OFF
        player.shuffleModeEnabled = false
        releaseOfflineRetention()
        offlinePendingEntryId?.let { offlineQueue = OfflinePlaybackPolicy.remove(offlineQueue, it) }
        offlinePendingEntryId = null
        player.setWakeMode(C.WAKE_MODE_NETWORK)
        offlineError = null
        publishOfflineState()
        return OfflinePlaybackPolicy.OwnershipRequestToken(ownershipGeneration, requestGeneration)
    }

    internal suspend fun playOffline(
        tracks: List<OfflineTrack>,
        startIndex: Int,
        confirmHandoff: Boolean,
        requestGeneration: Long,
    ) {
        require(tracks.isNotEmpty() && startIndex in tracks.indices)
        requireCurrentOfflineRequest(requestGeneration)
        if (tracks.any { it.pendingDelete }) {
            throw NativePlaybackException("track_unavailable", "Saved audio is pending deletion.")
        }
        val commandGeneration = ++offlineCommandGeneration
        if (OfflinePlaybackPolicy.requiresHandoff(ownerName(), OfflinePlaybackPolicy.OWNER_LOCAL)) {
            val existing = registration
                ?: throw NativePlaybackException("handoff_failed", "Server playback ownership is unavailable.")
            if (!confirmHandoff) {
                throw NativePlaybackException(
                    "handoff_required",
                    "Disconnect Server playback and switch this phone to saved music?",
                )
            }
            disconnectRegistrationForHandoff(existing)
        }
        if (owner == PlaybackOwner.SERVER) {
            throw NativePlaybackException("handoff_failed", "Server playback could not be disconnected.")
        }

        val expectedOwnership = ownershipGeneration
        val operationToken = OfflinePlaybackPolicy.OperationToken(expectedOwnership, commandGeneration)
        val newQueue = OfflinePlaybackPolicy.newQueue(tracks.map(OfflineTrack::id), startIndex)
        val initialEntry = newQueue.entries[startIndex]
        val mediaSources = newQueue.entries.zip(tracks).map { (entry, track) ->
            offlineMediaSource(entry, track)
        }
        val committed = acquireOfflineRetention(initialEntry.trackId) { retention ->
            if (!OfflinePlaybackRequestFence.isCurrent(requestGeneration) ||
                !OfflinePlaybackPolicy.operationIsCurrent(
                    operationToken,
                    ownershipGeneration,
                    offlineCommandGeneration,
                ) || owner == PlaybackOwner.SERVER
            ) {
                false
            } else {
                cancelServerTransport()
                cancelMediaWork()
                offlineGeneration++
                setOwner(PlaybackOwner.LOCAL)
                player.setWakeMode(C.WAKE_MODE_LOCAL)
                offlineQueue = newQueue
                replaceOfflineRetention(initialEntry.id, retention)
                offlineError = null
                offlineFailedEntryIds.clear()
                offlinePendingEntryId = null
                player.setMediaSources(mediaSources, startIndex, 0L)
                player.shuffleModeEnabled = false
                player.repeatMode = Player.REPEAT_MODE_OFF
                persistOfflineQueue()
                publishOfflineState()
                startOfflineTicker()
                player.prepare()
                player.play()
                true
            }
        }
        if (!committed) throw NativePlaybackException("superseded", "A newer playback action replaced this request.")
    }

    internal suspend fun enqueueOffline(trackIds: List<String>, next: Boolean) {
        require(trackIds.isNotEmpty())
        if (owner != PlaybackOwner.LOCAL) {
            mutateInactiveOfflineQueue { queue -> OfflinePlaybackPolicy.enqueue(queue, trackIds, next) }
            return
        }
        val tracks = withContext(Dispatchers.IO) {
            trackIds.map { id ->
                offlineLibrary.track(id)?.takeUnless { it.pendingDelete }
                    ?: throw NativePlaybackException("track_unavailable", "Saved audio is no longer available.")
            }
        }
        if (owner != PlaybackOwner.LOCAL) {
            mutateInactiveOfflineQueue { queue -> OfflinePlaybackPolicy.enqueue(queue, trackIds, next) }
            return
        }
        captureOfflinePosition()
        val baseQueue = offlineQueue
        val updated = OfflinePlaybackPolicy.enqueue(baseQueue, trackIds, next)
        val existingIds = baseQueue.entries.mapTo(
            HashSet<String>(baseQueue.entries.size),
            OfflineQueueEntry::id,
        )
        val additions = updated.entries.filterNot { it.id in existingIds }
        check(additions.size == tracks.size)
        val insertionIndex = updated.entries.indexOfFirst { it.id == additions.first().id }
        val mediaSources = additions.zip(tracks).map { (entry, track) -> offlineMediaSource(entry, track) }
        offlineQueue = updated
        player.addMediaSources(insertionIndex, mediaSources)
        publishOfflineState()
        persistOfflineQueueNow()
    }

    internal suspend fun playOfflineEntry(
        savedQueue: OfflineQueue,
        entryId: String,
        confirmHandoff: Boolean,
        requestGeneration: Long,
        startPositionMs: Long,
    ) {
        requireCurrentOfflineRequest(requestGeneration)
        val positionMs = startPositionMs.coerceAtLeast(0L)
        val initialQueue = if (offlineQueue.entries.any { it.id == entryId }) {
            offlineQueue
        } else {
            OfflinePlaybackPolicy.normalized(savedQueue)
        }
        val initialEntry = initialQueue.entries.firstOrNull { it.id == entryId }
            ?: throw NativePlaybackException(
                "queue_entry_unavailable",
                "The saved queue entry is no longer available.",
            )
        val initialTrack = withContext(Dispatchers.IO) {
            offlineLibrary.track(initialEntry.trackId)?.takeUnless { it.pendingDelete }
        } ?: throw NativePlaybackException("track_unavailable", "Saved audio is no longer available.")
        requireCurrentOfflineRequest(requestGeneration)

        val commandGeneration = ++offlineCommandGeneration
        if (OfflinePlaybackPolicy.requiresHandoff(ownerName(), OfflinePlaybackPolicy.OWNER_LOCAL)) {
            val existing = registration
                ?: throw NativePlaybackException("handoff_failed", "Server playback ownership is unavailable.")
            if (!confirmHandoff) {
                throw NativePlaybackException(
                    "handoff_required",
                    "Disconnect Server playback and switch this phone to saved music?",
                )
            }
            disconnectRegistrationForHandoff(existing)
        }
        if (owner == PlaybackOwner.SERVER) {
            throw NativePlaybackException("handoff_failed", "Server playback could not be disconnected.")
        }

        val operationToken = OfflinePlaybackPolicy.OperationToken(ownershipGeneration, commandGeneration)
        if (owner == PlaybackOwner.LOCAL) {
            val committed = acquireOfflineRetention(initialTrack.id) { retention ->
                val currentIndex = offlineQueue.entries.indexOfFirst { it.id == entryId }
                if (!OfflinePlaybackRequestFence.isCurrent(requestGeneration) ||
                    !OfflinePlaybackPolicy.operationIsCurrent(
                        operationToken,
                        ownershipGeneration,
                        offlineCommandGeneration,
                    ) || owner != PlaybackOwner.LOCAL || currentIndex < 0
                ) {
                    false
                } else {
                    offlineError = null
                    offlineFailedEntryIds.clear()
                    player.seekTo(currentIndex, positionMs)
                    replaceOfflineRetention(entryId, retention)
                    if (player.playbackState == Player.STATE_IDLE) player.prepare()
                    player.play()
                    captureOfflinePosition()
                    persistOfflineQueue()
                    publishOfflineState()
                    true
                }
            }
            if (!committed) {
                throw NativePlaybackException("superseded", "A newer playback action replaced this request.")
            }
            return
        }

        val queue = if (offlineQueue.entries.any { it.id == entryId }) offlineQueue else initialQueue
        val available = withContext(Dispatchers.IO) {
            queue.entries.map { entry -> entry to offlineLibrary.track(entry.trackId) }
        }.filter { (_, track) -> track != null && !track.pendingDelete }
        if (!OfflinePlaybackRequestFence.isCurrent(requestGeneration) ||
            !OfflinePlaybackPolicy.operationIsCurrent(
                operationToken,
                ownershipGeneration,
                offlineCommandGeneration,
            ) || owner == PlaybackOwner.SERVER
        ) {
            throw NativePlaybackException("superseded", "A newer playback action replaced this request.")
        }
        val selectedIndex = available.indexOfFirst { (entry, _) -> entry.id == entryId }
        if (selectedIndex < 0) {
            throw NativePlaybackException("track_unavailable", "Saved audio is no longer available.")
        }
        val restored = OfflinePlaybackPolicy.normalized(
            queue.copy(
                entries = available.map { it.first },
                currentEntryId = entryId,
                positionMs = positionMs,
            ),
        )
        val mediaSources = available.map { (entry, track) ->
            offlineMediaSource(entry, requireNotNull(track))
        }
        val committed = acquireOfflineRetention(initialTrack.id) { retention ->
            if (!OfflinePlaybackRequestFence.isCurrent(requestGeneration) ||
                !OfflinePlaybackPolicy.operationIsCurrent(
                    operationToken,
                    ownershipGeneration,
                    offlineCommandGeneration,
                ) || owner == PlaybackOwner.SERVER
            ) {
                false
            } else {
                cancelServerTransport()
                cancelMediaWork()
                offlineGeneration++
                setOwner(PlaybackOwner.LOCAL)
                player.setWakeMode(C.WAKE_MODE_LOCAL)
                offlineQueue = restored
                replaceOfflineRetention(entryId, retention)
                offlineError = null
                offlineFailedEntryIds.clear()
                offlinePendingEntryId = null
                player.setMediaSources(mediaSources, selectedIndex, positionMs)
                player.shuffleModeEnabled = restored.shuffle
                player.repeatMode = restored.repeatMode
                persistOfflineQueue()
                publishOfflineState()
                startOfflineTicker()
                player.prepare()
                player.play()
                true
            }
        }
        if (!committed) throw NativePlaybackException("superseded", "A newer playback action replaced this request.")
    }

    internal suspend fun resumeRestoredOffline(savedQueue: OfflineQueue, requestGeneration: Long) {
        requireCurrentOfflineRequest(requestGeneration)
        val commandGeneration = ++offlineCommandGeneration
        val expectedOwnership = ownershipGeneration
        val operationToken = OfflinePlaybackPolicy.OperationToken(expectedOwnership, commandGeneration)
        if (owner == PlaybackOwner.SERVER) {
            throw NativePlaybackException(
                "handoff_required",
                "Disconnect Server playback before resuming saved music.",
            )
        }
        val normalized = OfflinePlaybackPolicy.normalized(savedQueue)
        val available = withContext(Dispatchers.IO) {
            normalized.entries.map { entry -> entry to offlineLibrary.track(entry.trackId) }
        }.filter { (_, track) -> track != null && !track.pendingDelete }
        if (!OfflinePlaybackRequestFence.isCurrent(requestGeneration) ||
            !OfflinePlaybackPolicy.operationIsCurrent(
                operationToken,
                ownershipGeneration,
                offlineCommandGeneration,
            ) || owner == PlaybackOwner.SERVER
        ) {
            throw NativePlaybackException("superseded", "A newer playback action replaced this request.")
        }
        if (available.isEmpty()) {
            throw NativePlaybackException("track_unavailable", "No saved queue audio is available.")
        }
        val entries = available.map { it.first }
        val currentId = normalized.currentEntryId?.takeIf { id -> entries.any { it.id == id } }
            ?: entries.first().id
        val restored = normalized.copy(
            entries = entries,
            currentEntryId = currentId,
            positionMs = normalized.positionMs.takeIf { currentId == normalized.currentEntryId } ?: 0L,
        )
        val currentIndex = restored.entries.indexOfFirst { it.id == currentId }
        val mediaSources = available.map { (entry, track) ->
            offlineMediaSource(entry, requireNotNull(track))
        }
        val committed = acquireOfflineRetention(restored.entries[currentIndex].trackId) { retention ->
            if (!OfflinePlaybackRequestFence.isCurrent(requestGeneration) ||
                !OfflinePlaybackPolicy.operationIsCurrent(
                    operationToken,
                    ownershipGeneration,
                    offlineCommandGeneration,
                ) || owner == PlaybackOwner.SERVER
            ) {
                false
            } else {
                cancelServerTransport()
                cancelMediaWork()
                offlineGeneration++
                setOwner(PlaybackOwner.LOCAL)
                player.setWakeMode(C.WAKE_MODE_LOCAL)
                offlineQueue = restored
                replaceOfflineRetention(currentId, retention)
                offlineError = null
                offlineFailedEntryIds.clear()
                offlinePendingEntryId = null
                player.setMediaSources(mediaSources, currentIndex, restored.positionMs)
                player.shuffleModeEnabled = restored.shuffle
                player.repeatMode = restored.repeatMode
                persistOfflineQueue()
                publishOfflineState()
                startOfflineTicker()
                player.prepare()
                player.play()
                true
            }
        }
        if (!committed) throw NativePlaybackException("superseded", "A newer playback action replaced this request.")
    }

    private fun offlineMediaSource(entry: OfflineQueueEntry, track: OfflineTrack): MediaSource {
        val metadata = MediaMetadata.Builder()
            .setTitle(track.title)
            .setArtist(track.artist)
            .setAlbumTitle(track.album)
            .setDurationMs(track.durationMs.coerceAtLeast(0L))
            .build()
        val item = MediaItem.Builder()
            .setMediaId(entry.id)
            .setUri(OfflineAudioDataSource.uri(track.id))
            .setMimeType(track.mime)
            .setMediaMetadata(metadata)
            .build()
        return offlineMediaFactory.createMediaSource(item)
    }

    internal fun adoptPersistedOfflineQueue(queue: OfflineQueue) {
        if (owner == PlaybackOwner.LOCAL) {
            throw NativePlaybackException("superseded", "Local playback replaced the saved queue update.")
        }
        offlineQueue = OfflinePlaybackPolicy.normalized(queue)
        offlineError = null
        publishOfflineState()
    }

    internal suspend fun mutateInactiveOfflineQueue(transform: (OfflineQueue) -> OfflineQueue) {
        if (owner == PlaybackOwner.LOCAL) {
            throw NativePlaybackException("superseded", "Local playback replaced the saved queue update.")
        }
        offlinePersistRevision++
        val updated = withContext(Dispatchers.IO) {
            offlinePersistMutex.withLock { offlineLibrary.updateQueue(transform) }
        }
        adoptPersistedOfflineQueue(updated)
    }

    internal fun pauseOffline() {
        beginOfflineCommand()
        if (owner != PlaybackOwner.LOCAL) return
        player.pause()
        captureOfflinePosition()
        persistOfflineQueue()
        publishOfflineState()
    }

    internal fun resumeOffline() {
        beginOfflineCommand()
        if (owner == PlaybackOwner.SERVER) {
            offlineError = PublicError("handoff_required", "Confirm switching this phone to saved music.")
            publishOfflineState()
            return
        }
        if (owner != PlaybackOwner.LOCAL || player.mediaItemCount == 0) return
        offlineError = null
        offlineFailedEntryIds.clear()
        val entryId = player.currentMediaItem?.mediaId ?: return
        prepareOfflineEntry(entryId, playWhenReady = true)
    }

    internal fun stopOffline() {
        beginOfflineCommand()
        if (owner != PlaybackOwner.LOCAL) return
        captureOfflinePosition()
        player.stop()
        val pending = offlinePendingEntryId
        offlinePendingEntryId = null
        if (pending != null) removeOfflineEntryInternal(pending)
        releaseOfflineRetention()
        persistOfflineQueue()
        publishOfflineState()
    }

    internal fun seekOffline(positionMillis: Long) {
        beginOfflineCommand()
        if (owner != PlaybackOwner.LOCAL || player.mediaItemCount == 0) return
        player.seekTo(positionMillis.coerceAtLeast(0L))
        captureOfflinePosition()
        persistOfflineQueue()
        publishOfflineState()
    }

    internal fun nextOffline() {
        beginOfflineCommand()
        if (owner != PlaybackOwner.LOCAL || player.mediaItemCount == 0) return
        offlineFailedEntryIds.clear()
        offlineError = null
        val index = when {
            player.hasNextMediaItem() -> player.nextMediaItemIndex
            offlineQueue.repeatMode == Player.REPEAT_MODE_ALL -> 0
            else -> C.INDEX_UNSET
        }
        if (index != C.INDEX_UNSET) switchOfflineEntry(index, 0L, true)
    }

    internal fun previousOffline() {
        beginOfflineCommand()
        if (owner != PlaybackOwner.LOCAL || player.mediaItemCount == 0) return
        offlineFailedEntryIds.clear()
        offlineError = null
        val index = when {
            player.currentPosition > PREVIOUS_RESTART_THRESHOLD_MILLIS -> player.currentMediaItemIndex
            player.hasPreviousMediaItem() -> player.previousMediaItemIndex
            else -> 0
        }
        switchOfflineEntry(index, 0L, true)
    }

    internal fun setOfflineShuffle(enabled: Boolean) {
        beginOfflineCommand()
        if (owner != PlaybackOwner.LOCAL) return
        offlineQueue = offlineQueue.copy(shuffle = enabled)
        player.shuffleModeEnabled = enabled
        persistOfflineQueue()
        publishOfflineState()
    }

    internal fun setOfflineRepeat(mode: Int) {
        beginOfflineCommand()
        if (owner != PlaybackOwner.LOCAL || mode !in Player.REPEAT_MODE_OFF..Player.REPEAT_MODE_ALL) return
        offlineQueue = offlineQueue.copy(repeatMode = mode)
        player.repeatMode = if (offlinePendingEntryId == offlineQueue.currentEntryId) {
            Player.REPEAT_MODE_OFF
        } else {
            mode
        }
        persistOfflineQueue()
        publishOfflineState()
    }

    internal fun moveOfflineEntry(entryId: String, offset: Int) {
        beginOfflineCommand()
        if (owner != PlaybackOwner.LOCAL || offset == 0) return
        val from = offlineQueue.entries.indexOfFirst { it.id == entryId }
        val updated = OfflinePlaybackPolicy.move(offlineQueue, entryId, offset)
        val to = updated.entries.indexOfFirst { it.id == entryId }
        if (from < 0 || from == to) return
        offlineQueue = updated
        player.moveMediaItem(from, to)
        captureOfflinePosition()
        persistOfflineQueue()
        publishOfflineState()
    }

    internal fun removeOfflineEntry(entryId: String) {
        beginOfflineCommand()
        if (owner != PlaybackOwner.LOCAL) return
        removeOfflineEntryInternal(entryId)
        persistOfflineQueue()
        publishOfflineState()
    }

    private fun removeOfflineEntryInternal(entryId: String) {
        val index = offlineQueue.entries.indexOfFirst { it.id == entryId }
        if (index < 0) return
        val wasCurrent = player.currentMediaItem?.mediaId == entryId
        val wasActive = player.playbackState != Player.STATE_IDLE
        offlineQueue = OfflinePlaybackPolicy.remove(offlineQueue, entryId)
        offlineFailedEntryIds.remove(entryId)
        if (index < player.mediaItemCount) player.removeMediaItem(index)
        if (player.mediaItemCount == 0) player.stop()
        if (wasCurrent && (!wasActive || player.mediaItemCount == 0)) releaseOfflineRetention()
        captureOfflinePosition()
    }

    private fun captureOfflinePosition() {
        if (owner != PlaybackOwner.LOCAL) return
        val currentId = player.currentMediaItem?.mediaId
            ?.takeIf { id -> offlineQueue.entries.any { it.id == id } }
            ?: offlineQueue.currentEntryId
        offlineQueue = offlineQueue.copy(
            currentEntryId = currentId,
            positionMs = player.currentPosition.coerceAtLeast(0L),
        )
    }

    private fun persistOfflineQueue() {
        val snapshot = OfflinePlaybackPolicy.normalized(offlineQueue)
        offlineQueue = snapshot
        val diskSnapshot = offlinePendingEntryId?.let { OfflinePlaybackPolicy.remove(snapshot, it) } ?: snapshot
        val revision = ++offlinePersistRevision
        scope.launch(Dispatchers.IO) {
            offlinePersistMutex.withLock {
                if (revision != offlinePersistRevision) return@withLock
                try {
                    offlineLibrary.saveQueue(diskSnapshot)
                } catch (_: OfflineLibraryException) {
                    // A concurrent explicit deletion owns reference removal. Reconciliation
                    // publishes and persists the surviving queue on the next library change.
                }
            }
        }
    }

    private suspend fun persistOfflineQueueNow() {
        withContext(Dispatchers.IO) {
            offlinePersistMutex.withLock {
                val diskSnapshot = withContext(Dispatchers.Main.immediate) {
                    offlinePersistRevision++
                    val snapshot = OfflinePlaybackPolicy.normalized(offlineQueue)
                    offlineQueue = snapshot
                    offlinePendingEntryId?.let { OfflinePlaybackPolicy.remove(snapshot, it) } ?: snapshot
                }
                offlineLibrary.saveQueue(diskSnapshot)
            }
        }
    }

    private fun prepareOfflineEntry(entryId: String, playWhenReady: Boolean) {
        val commandGeneration = offlineCommandGeneration
        val generation = offlineGeneration
        val entry = offlineQueue.entries.firstOrNull { it.id == entryId } ?: return
        if (offlineRetainedEntryId == entryId && offlineRetention != null) {
            if (player.playbackState == Player.STATE_IDLE) player.prepare()
            if (playWhenReady) player.play() else player.pause()
            publishOfflineState()
            return
        }
        scope.launch {
            try {
                acquireOfflineRetention(entry.trackId) { retention ->
                    if (owner != PlaybackOwner.LOCAL || generation != offlineGeneration ||
                        commandGeneration != offlineCommandGeneration ||
                        player.currentMediaItem?.mediaId != entryId
                    ) {
                        false
                    } else {
                        replaceOfflineRetention(entryId, retention)
                        if (player.playbackState == Player.STATE_IDLE) player.prepare()
                        if (playWhenReady) player.play() else player.pause()
                        publishOfflineState()
                        true
                    }
                }
            } catch (error: OfflineLibraryException) {
                if (owner == PlaybackOwner.LOCAL && generation == offlineGeneration) {
                    offlineError = PublicError(error.code, getString(R.string.offline_playback_missing))
                    publishOfflineState()
                }
            }
        }
    }

    private fun switchOfflineEntry(index: Int, positionMillis: Long, playWhenReady: Boolean) {
        val entry = offlineQueue.entries.getOrNull(index) ?: return
        if (offlineRetainedEntryId == entry.id && offlineRetention != null) {
            player.seekTo(index, positionMillis.coerceAtLeast(0L))
            if (player.playbackState == Player.STATE_IDLE) player.prepare()
            if (playWhenReady) player.play() else player.pause()
            captureOfflinePosition()
            persistOfflineQueue()
            publishOfflineState()
            return
        }
        val generation = offlineGeneration
        val commandGeneration = offlineCommandGeneration
        scope.launch {
            try {
                acquireOfflineRetention(entry.trackId) { retention ->
                    val currentIndex = offlineQueue.entries.indexOfFirst { it.id == entry.id }
                    if (owner != PlaybackOwner.LOCAL || generation != offlineGeneration ||
                        commandGeneration != offlineCommandGeneration || currentIndex < 0
                    ) {
                        false
                    } else {
                        player.seekTo(currentIndex, positionMillis.coerceAtLeast(0L))
                        replaceOfflineRetention(entry.id, retention)
                        if (player.playbackState == Player.STATE_IDLE) player.prepare()
                        if (playWhenReady) player.play() else player.pause()
                        captureOfflinePosition()
                        persistOfflineQueue()
                        publishOfflineState()
                        true
                    }
                }
            } catch (error: OfflineLibraryException) {
                if (owner == PlaybackOwner.LOCAL && generation == offlineGeneration) {
                    offlineError = PublicError(error.code, getString(R.string.offline_playback_missing))
                    publishOfflineState()
                }
            }
        }
    }

    private fun replaceOfflineRetention(entryId: String, retention: Closeable) {
        if (offlineRetainedEntryId == entryId) {
            retention.close()
            return
        }
        val previous = offlineRetention
        offlineRetention = retention
        offlineRetainedEntryId = entryId
        previous?.close()
    }

    private fun releaseOfflineRetention() {
        val retention = offlineRetention
        offlineRetention = null
        offlineRetainedEntryId = null
        retention?.close()
    }

    private suspend fun acquireOfflineRetention(
        trackId: String,
        commit: (Closeable) -> Boolean,
    ): Boolean {
        var acquired: Closeable? = null
        return try {
            withContext(Dispatchers.IO) {
                offlineLibrary.retainTrack(trackId).also { acquired = it }
            }
            val retention = requireNotNull(acquired)
            if (!commit(retention)) {
                false
            } else {
                acquired = null
                true
            }
        } finally {
            acquired?.close()
        }
    }

    private fun handleOfflineTransition(entryId: String) {
        val generation = offlineGeneration
        val commandGeneration = offlineCommandGeneration
        val entry = offlineQueue.entries.firstOrNull { it.id == entryId } ?: return
        val pendingPrevious = offlinePendingEntryId?.takeIf { it != entryId }
        scope.launch {
            try {
                acquireOfflineRetention(entry.trackId) { retention ->
                    if (owner != PlaybackOwner.LOCAL || generation != offlineGeneration ||
                        commandGeneration != offlineCommandGeneration ||
                        player.currentMediaItem?.mediaId != entryId ||
                        player.playbackState == Player.STATE_IDLE ||
                        player.playbackState == Player.STATE_ENDED
                    ) {
                        false
                    } else {
                        replaceOfflineRetention(entryId, retention)
                        if (pendingPrevious != null) {
                            offlinePendingEntryId = null
                            removeOfflineEntryInternal(pendingPrevious)
                            player.repeatMode = offlineQueue.repeatMode
                        }
                        captureOfflinePosition()
                        persistOfflineQueue()
                        publishOfflineState()
                        true
                    }
                }
            } catch (error: OfflineLibraryException) {
                if (owner == PlaybackOwner.LOCAL && generation == offlineGeneration &&
                    commandGeneration == offlineCommandGeneration
                ) {
                    offlineError = PublicError(error.code, getString(R.string.offline_playback_missing))
                    player.pause()
                    publishOfflineState()
                }
            }
        }
    }

    private fun beginOfflineCommand() {
        OfflinePlaybackRequestFence.beginRequest()
        offlineCommandGeneration++
    }

    private fun requireCurrentOfflineRequest(requestGeneration: Long) {
        if (!OfflinePlaybackRequestFence.isCurrent(requestGeneration)) {
            throw NativePlaybackException("superseded", "A newer playback action replaced this request.")
        }
    }

    private fun setOwner(value: PlaybackOwner) {
        if (owner == value) return
        // Bit-perfect output belongs to Server-owned playback; Saved music keeps the normal mixer.
        if (value != PlaybackOwner.SERVER) releaseBitPerfect()
        owner = value
        ownershipGeneration++
    }

    /** Drops the preferred mixer attributes and the pinned USB route for the shared player. */
    private fun releaseBitPerfect() {
        bitPerfect.clear()
        bitPerfectSource = null
        bitPerfectError = null
        if (::player.isInitialized && !playerReleased) player.setPreferredAudioDevice(null)
    }

    private fun ownerName(): String = when (owner) {
        PlaybackOwner.NONE -> OfflinePlaybackPolicy.OWNER_NONE
        PlaybackOwner.SERVER -> OfflinePlaybackPolicy.OWNER_SERVER
        PlaybackOwner.LOCAL -> OfflinePlaybackPolicy.OWNER_LOCAL
    }

    private fun publishOfflineState() {
        if (owner == PlaybackOwner.LOCAL) captureOfflinePosition()
        OfflinePlayback.publish(
            OfflinePlaybackState(
                owner = ownerName(),
                playing = owner == PlaybackOwner.LOCAL && player.isPlaying,
                queue = offlineQueue,
                errorCode = offlineError?.code,
                errorMessage = offlineError?.message,
            ),
        )
    }

    private fun startOfflineTicker() {
        offlineTickerJob?.cancel()
        offlineTickerJob = scope.launch {
            var ticks = 0
            while (owner == PlaybackOwner.LOCAL) {
                delay(1_000L)
                if (owner != PlaybackOwner.LOCAL) return@launch
                publishOfflineState()
                if (++ticks % 5 == 0) persistOfflineQueue()
            }
        }
    }

    private suspend fun reconcileOfflineLibrary() {
        if (owner != PlaybackOwner.LOCAL || offlineQueue.entries.isEmpty()) return
        val generation = offlineGeneration
        val entries = offlineQueue.entries
        val availability = withContext(Dispatchers.IO) {
            entries.associate { entry -> entry.id to offlineLibrary.track(entry.trackId) }
        }
        if (owner != PlaybackOwner.LOCAL || generation != offlineGeneration) return
        val currentId = player.currentMediaItem?.mediaId
        var changed = false
        entries.asReversed().forEach { entry ->
            val track = availability[entry.id]
            when {
                track?.pendingDelete == true && entry.id == currentId -> {
                    offlinePendingEntryId = entry.id
                    player.repeatMode = Player.REPEAT_MODE_OFF
                }
                track == null && entry.id == currentId -> {
                    offlineError = PublicError("local_missing", getString(R.string.offline_playback_missing))
                    player.pause()
                }
                track == null || track.pendingDelete -> {
                    removeOfflineEntryInternal(entry.id)
                    changed = true
                }
            }
        }
        if (changed) persistOfflineQueue()
        publishOfflineState()
    }

    internal suspend fun connect(
        server: ServerEndpoint,
        name: String,
        operationToken: OfflinePlaybackPolicy.OwnershipRequestToken,
    ): JSONObject = connectInternal(server, name, operationToken, onlyIfAvailable = false)
        ?: throw NativePlaybackException("superseded", "A newer playback action replaced this request.")

    internal suspend fun connectIfAvailable(
        server: ServerEndpoint,
        name: String,
        operationToken: OfflinePlaybackPolicy.OwnershipRequestToken,
    ): JSONObject? = connectInternal(server, name, operationToken, onlyIfAvailable = true)

    private suspend fun connectInternal(
        server: ServerEndpoint,
        name: String,
        operationToken: OfflinePlaybackPolicy.OwnershipRequestToken,
        onlyIfAvailable: Boolean,
    ): JSONObject? {
        if (!OfflinePlaybackRequestFence.isCurrent(operationToken.requestGeneration) ||
            operationToken.ownershipGeneration != ownershipGeneration || owner == PlaybackOwner.LOCAL ||
            (onlyIfAvailable && OfflinePlayback.hasPendingMutation)
        ) {
            if (onlyIfAvailable) return null
            throw NativePlaybackException("handoff_required", "Saved music ownership changed.")
        }
        var commitGeneration = operationToken.ownershipGeneration
        val key = NativePlaybackPolicy.serverKey(server)
        registration?.let { existing ->
            if (existing.key == key) return copyJson(existing.device)
            if (onlyIfAvailable) return null
            if (currentResource?.terminalReported != true && currentResource != null) {
                throw NativePlaybackException("stop_required", "Stop phone playback before changing servers.")
            }
            disconnectRegistration(existing)
            commitGeneration = ownershipGeneration
        }
        if (!OfflinePlaybackRequestFence.isCurrent(operationToken.requestGeneration) ||
            commitGeneration != ownershipGeneration || owner == PlaybackOwner.LOCAL
        ) {
            if (onlyIfAvailable) return null
            throw NativePlaybackException("handoff_required", "Saved music ownership changed.")
        }
        if (!onlyIfAvailable) stateKey = key

        val client = try {
            AuthenticatedServerClient(server)
        } catch (error: Throwable) {
            val failure = NativePlaybackException("profile_unavailable", "The isolated server profile is unavailable.", error)
            if (!onlyIfAvailable) setError(failure.code, failure.message.orEmpty())
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
            if (!onlyIfAvailable) setError(failure.code, failure.message.orEmpty())
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
            if (!onlyIfAvailable) setError(failure.code, failure.message.orEmpty())
            throw failure
        }
        if (!OfflinePlaybackRequestFence.isCurrent(operationToken.requestGeneration) ||
            commitGeneration != ownershipGeneration || owner == PlaybackOwner.LOCAL ||
            (onlyIfAvailable && OfflinePlayback.hasPendingMutation)
        ) {
            try {
                parsed.client.json(
                    method = "DELETE",
                    path = registrationPath(parsed.id),
                    ownerToken = parsed.ownerToken,
                    allowEmpty = true,
                )
            } catch (_: Throwable) {
                // The unclaimed short lease bounds a failed stale-registration release.
            }
            if (onlyIfAvailable) return null
            throw NativePlaybackException("handoff_required", "Saved music ownership changed.")
        }
        registration = parsed
        stateKey = key
        setOwner(PlaybackOwner.SERVER)
        player.setWakeMode(C.WAKE_MODE_NETWORK)
        player.repeatMode = Player.REPEAT_MODE_OFF
        player.shuffleModeEnabled = false
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

    internal suspend fun disconnect(server: ServerEndpoint) {
        val existing = registration ?: return
        if (existing.key != NativePlaybackPolicy.serverKey(server)) return
        cancelServerTransport()
        registration = null
        publicError = null
        if (owner == PlaybackOwner.SERVER) {
            setOwner(PlaybackOwner.NONE)
            cancelMediaWork()
        } else {
            notifyState()
        }
        try {
            existing.client.json(
                method = "DELETE",
                path = registrationPath(existing.id),
                ownerToken = existing.ownerToken,
                allowEmpty = true,
            )
        } catch (_: Throwable) {
            // The lease bounds a registration that could not be released explicitly.
        }
    }

    internal fun setVolume(server: ServerEndpoint, volume: Float): JSONObject {
        if (!volume.isFinite() || volume !in 0f..1f) {
            throw NativePlaybackException("invalid_volume", "Volume must be between 0 and 1.")
        }
        requireRegistration(server)
        if (owner != PlaybackOwner.SERVER) {
            throw NativePlaybackException("not_connected", "Phone playback is not owned by this server.")
        }
        if (bitPerfect.enabled) {
            throw NativePlaybackException(
                "bit_perfect_fixed_volume",
                "Volume is fixed at 100% for USB bit-perfect output; adjust it on the DAC.",
            )
        }
        player.volume = volume
        notifyState()
        return state(server)
    }

    /**
     * Turns the opt-in USB bit-perfect path on or off. It applies from the next track and keeps the
     * same Server output registration, so it is only accepted while phone playback is stopped.
     */
    internal fun configure(server: ServerEndpoint, usbBitPerfect: Boolean): JSONObject {
        requireRegistration(server)
        if (owner != PlaybackOwner.SERVER) {
            throw NativePlaybackException("not_connected", "Phone playback is not owned by this server.")
        }
        if (currentResource != null || preparing || recovering) {
            throw NativePlaybackException("stop_required", "Stop phone playback before changing audio settings.")
        }
        if (usbBitPerfect) {
            val status = bitPerfect.status()
            if (!status.available) {
                throw NativePlaybackException(
                    "bit_perfect_unavailable",
                    when (status.reason) {
                        UsbBitPerfectPolicy.UNAVAILABLE_REQUIRES_ANDROID_14 ->
                            "USB bit-perfect output needs Android 14 or newer."
                        UsbBitPerfectPolicy.UNAVAILABLE_NO_USB_DEVICE ->
                            "Connect a USB audio device to use bit-perfect output."
                        else -> "This USB audio device does not offer a bit-perfect mixer."
                    },
                )
            }
        }
        bitPerfect.setEnabled(usbBitPerfect)
        bitPerfectError = null
        if (usbBitPerfect) player.volume = 1f else releaseBitPerfect()
        publicError = null
        notifyState()
        return state(server)
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
            .put(
                "volume",
                if (existing != null && owner == PlaybackOwner.SERVER && !bitPerfect.enabled) {
                    player.volume.toDouble()
                } else {
                    JSONObject.NULL
                },
            )
            .put("audio", audioState())
            .also { value ->
                publicError?.let { value.put("error", JSONObject().put("code", it.code).put("message", it.message)) }
            }
    }

    /** The requested and the actually active local audio path, as the settings panel reports them. */
    private fun audioState(): JSONObject {
        val status = bitPerfect.status()
        val devices = JSONArray()
        status.devices.forEach { device ->
            devices.put(JSONObject().put("id", device.id).put("name", device.name))
        }
        return JSONObject()
            .put("supported", status.supported)
            .put("available", status.available)
            .put("reason", status.reason)
            .put("devices", devices)
            .put("api_level", status.apiLevel)
            .put("mixer_report", mixerReport(status.reports))
            .put("enabled", bitPerfect.enabled)
            .put("requested", JSONObject().put("bit_perfect", bitPerfect.enabled))
            .put("state", engineState())
            .put("can_configure", owner == PlaybackOwner.SERVER && currentResource == null && !preparing && !recovering)
            .put("actual", actualAudio() ?: JSONObject.NULL)
            .also { value ->
                bitPerfectError?.let {
                    value.put("error", JSONObject().put("code", it.code).put("message", it.message))
                }
            }
    }

    /**
     * The raw `getSupportedMixerAttributes` list per USB device, bounded, so a device that reports
     * nothing usable can be told apart from one that reports nothing at all.
     */
    private fun mixerReport(reports: List<UsbMixerReport>): JSONArray {
        val value = JSONArray()
        var remaining = MAX_REPORTED_MIXER_ENTRIES
        reports.forEach { report ->
            val entries = JSONArray()
            report.entries.take(remaining.coerceAtLeast(0)).forEach { reported ->
                entries.put(
                    JSONObject()
                        .put("bit_perfect", reported.entry.bitPerfect)
                        .put("encoding", reported.entry.encoding)
                        .put("encoding_label", reported.encodingLabel)
                        .put("sample_rate", reported.entry.sampleRate)
                        .put("channel_mask", reported.entry.channelMask)
                        .put("channel_index_mask", reported.entry.channelIndexMask)
                        .put("rejection", reported.rejection),
                )
            }
            remaining -= entries.length()
            value.put(
                JSONObject()
                    .put("device_id", report.deviceId)
                    .put("device_name", report.deviceName)
                    .put("device_type", report.deviceType)
                    .put("total", report.total)
                    .put("bit_perfect", report.bitPerfect)
                    .put("usable_bit_perfect", report.usableBitPerfect)
                    .put("rejected", report.rejected)
                    .put("entries", entries),
            )
        }
        return value
    }

    private fun engineState(): String = when {
        owner != PlaybackOwner.SERVER || currentResource == null -> "stopped"
        publicError != null -> "error"
        player.isPlaying -> "playing"
        currentResource?.everPlayed == true -> "paused"
        else -> "loaded"
    }

    private fun actualAudio(): JSONObject? {
        if (owner != PlaybackOwner.SERVER) return null
        val stream = audioTrackStream ?: return null
        val active = bitPerfect.active(stream)
        val source = bitPerfectSource
        val verdict = if (source == null) {
            BitPerfectVerdict(false, UsbBitPerfectPolicy.REASON_SOURCE_PROVENANCE_UNKNOWN)
        } else {
            UsbBitPerfectPolicy.transparency(
                source = source,
                actual = stream,
                bitPerfectActive = active?.bitPerfect == true,
                unityGain = player.volume == 1f,
            )
        }
        return JSONObject()
            .put("device_id", active?.deviceId.orEmpty())
            .put("name", active?.deviceName ?: bitPerfect.deviceLabel())
            .put("mode", if (active?.bitPerfect == true) "bit_perfect" else "mixed")
            .put("sample_rate", stream.sampleRate)
            .put("channels", stream.channelCount)
            .put("container_bits", stream.encoding.containerBits)
            .put("valid_bits", stream.encoding.validBits)
            .put("encoding", stream.encoding.label)
            .put("bit_transparent", verdict.transparent)
            .put("reason", verdict.reason)
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
                if (handleTransportFailure(expected, error, registrationPoll = true)) return
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
            // Revocation can precede the Server's replacement command. Stop the
            // decoder now, retaining only its timeline until replacement or terminal loss.
            if (active != null && activeExecutionSequence <= cancelBefore) {
                active.cancel(CancellationException("Server cancelled media command"))
                cancelMediaWork(clearMediaItems = false)
            } else if (currentResource?.sequence?.let { it <= cancelBefore } == true) {
                cancelMediaWork(clearMediaItems = false)
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
            val playbackErrors = playbackErrorsForFailure(error, action, sequence)
            if (action == "set_uri") cancelMediaWork()
            val code = when (error) {
                is CommandFailure -> error.reportCode
                is PlaybackFailure -> if (
                    error.reportCode == "media_failed" &&
                    action != "set_uri" &&
                    action != "play"
                ) {
                    "media_error"
                } else {
                    error.reportCode
                }
                else -> "action_failed"
            }
            if (code == "media_failed" && action == "play") player.stop()
            val body = reportBody(sequence, "failed", errorCode = code, playbackErrors = playbackErrors)
            try {
                sendFrozenReport(expected, body)
                sequenceFence.complete(sequence)
            } catch (_: CancellationException) {
                throw CancellationException("Command report cancelled")
            }
            setError(if (code == "media_failed") code else "playback_failed", getString(R.string.native_playback_error))
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
        // Keep the prior timeline while preparing the replacement. Clearing it here
        // removes Media3's notification and foreground protection between tracks.
        player.stop()

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
        try {
            player.setMediaSource(expected.client.mediaSource(item))
            prepareWithRecovery(expected, active, startPositionMillis = 0L, playWhenReady = false)
        } catch (error: CancellationException) {
            throw error
        } catch (error: PlaybackFailure) {
            throw error
        } catch (error: Throwable) {
            throw preparationFailure(active, error)
        }
        applyBitPerfect(expected, active, resource)
        notifyState()
        return observation("loaded", playId)
    }

    /**
     * Configures the USB bit-perfect mixer for the freshly prepared track. The mixer attributes must
     * exist before the `AudioTrack` is created, so a track opened under the previous preference is
     * reopened once. An unusable format or device fails the item instead of falling back to the
     * shared mixer.
     */
    private suspend fun applyBitPerfect(expected: Registration, active: ActiveResource, resource: JSONObject) {
        bitPerfectSource = sourceStream(resource)
        bitPerfectError = null
        if (!bitPerfect.enabled) {
            if (bitPerfect.applied) releaseBitPerfect()
            return
        }
        val source = bitPerfectSource
            ?: throw bitPerfectFailure("bit_perfect_format_unknown", "The decoded audio format is unknown, so bit-perfect output cannot be requested.")
        val planned = UsbBitPerfectPolicy.plannedStream(
            source = source,
            channelMask = bitPerfect.channelMask(source.channelCount),
            floatOutput = false,
        ) ?: throw bitPerfectFailure("bit_perfect_layout_unsupported", "This channel layout cannot be sent to a USB audio device.")
        val device = try {
            bitPerfect.apply(planned)
        } catch (failure: UsbBitPerfectException) {
            throw bitPerfectFailure(failure.code, failure.message.orEmpty())
        }
        player.setPreferredAudioDevice(device)
        player.volume = 1f
        if (audioTrackStream != planned || audioTrackGeneration != bitPerfect.appliedGeneration) {
            try {
                player.stop()
                player.setMediaSource(expected.client.mediaSource(active.mediaItem), 0L)
                prepareWithRecovery(expected, active, startPositionMillis = 0L, playWhenReady = false)
            } catch (error: CancellationException) {
                throw error
            } catch (error: PlaybackFailure) {
                throw error
            } catch (error: Throwable) {
                throw preparationFailure(active, error)
            }
        }
        if (audioTrackStream != planned || !bitPerfect.activeBitPerfect()) {
            releaseBitPerfect()
            throw bitPerfectFailure(
                "bit_perfect_not_applied",
                "USB bit-perfect output could not be applied to this track.",
            )
        }
    }

    /** Keeps the specific bit-perfect reason in the settings panel while the item fails normally. */
    private fun bitPerfectFailure(code: String, message: String): CommandFailure {
        bitPerfectError = PublicError(code, message)
        notifyState()
        return CommandFailure("media_unsupported", message)
    }

    /** What the Server delivered and what the extractor decoded, used for the transparency verdict. */
    private fun sourceStream(resource: JSONObject): SourceStream? {
        val format = player.audioFormat ?: return null
        if (format.sampleRate == Format.NO_VALUE || format.channelCount == Format.NO_VALUE) return null
        val transformed = if (resource.opt("transformed") is Boolean) resource.optBoolean("transformed") else null
        val lossless = UsbBitPerfectPolicy.isLosslessMime(resource.optString("mime")) &&
            UsbBitPerfectPolicy.isLosslessMime(format.sampleMimeType)
        return SourceStream(
            sampleRate = format.sampleRate,
            channelCount = format.channelCount,
            encoding = UsbBitPerfectController.encoding(format.pcmEncoding),
            lossless = lossless,
            transformed = transformed,
        )
    }

    private suspend fun executePlay(expected: Registration, command: JSONObject, sequence: Long): JSONObject {
        val active = requireActive(command, sequence)
        val interruptedRecovery = cancelBackgroundRecovery()
        active.wantsPlayback = true
        active.everPlayed = true
        drainPlayerErrors()
        if (interruptedRecovery || player.playbackState == Player.STATE_IDLE) {
            prepareWithRecovery(expected, active, player.currentPosition.coerceAtLeast(0L), true)
        } else {
            player.play()
            val error = awaitPlayerEffect(EFFECT_TIMEOUT_MILLIS, NativePlaybackErrorStage.COMMAND) {
                player.isPlaying
            }
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
        val error = awaitPlayerEffect(EFFECT_TIMEOUT_MILLIS, NativePlaybackErrorStage.COMMAND) {
            !player.playWhenReady
        }
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
        // The Server also sends Stop between tracks. Keep only the stopped
        // timeline; terminal registration loss still removes it and the notification.
        cancelMediaWork(clearMediaItems = false)
        publicError = null
        notifyState()
        return result
    }

    private suspend fun executeSeek(expected: Registration, command: JSONObject, sequence: Long): JSONObject {
        val active = requireActive(command, sequence)
        // The Server omits position_ms when its value is zero.
        val requested = if (command.has("position_ms")) command.optLong("position_ms", -1L) else 0L
        if (requested < 0L) throw CommandFailure("action_failed", "Invalid seek position")
        val interruptedRecovery = cancelBackgroundRecovery()
        val shouldPlay = active.wantsPlayback
        if (interruptedRecovery || player.playbackState == Player.STATE_IDLE) {
            player.setMediaSource(expected.client.mediaSource(active.mediaItem), requested)
            prepareWithRecovery(expected, active, requested, shouldPlay)
        } else {
            player.seekTo(requested)
            val error = awaitPlayerEffect(EFFECT_TIMEOUT_MILLIS, NativePlaybackErrorStage.COMMAND) {
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
        error: PlayerFailure,
        positionMillis: Long,
        playWhenReady: Boolean,
    ) {
        active.playbackErrors.add(error.diagnostic)
        val classification = classifyPlayback(error)
        if (classification.fatalRegistration) {
            loseRegistration(expected, classification.publicCode, classification.publicMessage)
            throw CancellationException("Registration invalidated by media transport")
        }
        if (!classification.transient) {
            throw PlaybackFailure(
                classification.reportCode,
                classification.publicMessage,
                error.diagnostic,
                error.error,
            )
        }
        prepareWithRecovery(
            expected,
            active,
            startPositionMillis = positionMillis,
            playWhenReady = playWhenReady,
            initialRetry = 1,
            priorFailure = error,
        )
    }


    private suspend fun prepareWithRecovery(
        expected: Registration,
        active: ActiveResource,
        startPositionMillis: Long,
        playWhenReady: Boolean,
        initialRetry: Int = 0,
        priorFailure: PlayerFailure? = null,
    ) {
        val deadline = SystemClock.elapsedRealtime() + NativePlaybackPolicy.MEDIA_RECOVERY_DEADLINE_MILLIS
        var retry = initialRetry
        var position = startPositionMillis
        var lastFailure = priorFailure
        active.wantsPlayback = playWhenReady
        preparing = true
        try {
            while (registration === expected && currentResource === active) {
                currentCoroutineContext().ensureActive()
                if (retry > 0) {
                    val delayMillis = NativePlaybackPolicy.recoveryDelayMillis(retry)
                        ?: throw playbackFailure(lastFailure ?: effectTimeoutFailure(NativePlaybackErrorStage.PREPARE))
                    val remaining = deadline - SystemClock.elapsedRealtime()
                    if (remaining <= delayMillis) {
                        throw playbackFailure(lastFailure ?: effectTimeoutFailure(NativePlaybackErrorStage.PREPARE))
                    }
                    setRecovering(true)
                    delay(delayMillis)
                    player.stop()
                    player.setMediaSource(expected.client.mediaSource(active.mediaItem), position)
                }
                drainPlayerErrors()
                player.prepare()
                if (active.wantsPlayback) player.play() else player.pause()
                val remaining = deadline - SystemClock.elapsedRealtime()
                if (remaining <= 0L) {
                    throw playbackFailure(lastFailure ?: effectTimeoutFailure(NativePlaybackErrorStage.PREPARE))
                }
                val error = awaitPlayerEffect(remaining, NativePlaybackErrorStage.PREPARE) {
                    player.playbackState == Player.STATE_READY && (!active.wantsPlayback || player.isPlaying)
                }
                if (error == null) {
                    active.playbackErrors.clear()
                    setRecovering(false)
                    return
                }
                active.playbackErrors.add(error.diagnostic)
                lastFailure = error
                position = player.currentPosition.coerceAtLeast(position)
                val classification = classifyPlayback(error)
                if (!classification.transient || retry >= NativePlaybackPolicy.MAX_MEDIA_RETRIES) {
                    if (classification.fatalRegistration) {
                        loseRegistration(expected, classification.publicCode, classification.publicMessage)
                        throw CancellationException("Registration invalidated by media transport")
                    }
                    throw PlaybackFailure(
                        classification.reportCode,
                        classification.publicMessage,
                        error.diagnostic,
                        error.error,
                    )
                }
                retry++
            }
            throw CancellationException("Media source changed")
        } catch (error: CancellationException) {
            throw error
        } catch (error: PlaybackFailure) {
            throw error
        } catch (error: Throwable) {
            throw preparationFailure(active, error)
        } finally {
            preparing = false
        }
    }

    private fun preparationFailure(active: ActiveResource, error: Throwable): PlaybackFailure {
        val diagnostic = NativePlaybackError.capture(
            NativePlaybackErrorStage.PREPARE,
            error,
            player.currentPosition.coerceAtLeast(0L),
        )
        active.playbackErrors.add(diagnostic)
        return PlaybackFailure(
            reportCode = "action_failed",
            message = getString(R.string.native_playback_error),
            diagnostic = diagnostic,
            cause = error,
        )
    }
    private suspend fun awaitPlayerEffect(
        timeoutMillis: Long,
        stage: NativePlaybackErrorStage,
        predicate: () -> Boolean,
    ): PlayerFailure? {
        val deadline = SystemClock.elapsedRealtime() + timeoutMillis.coerceAtLeast(1L)
        while (!predicate()) {
            currentCoroutineContext().ensureActive()
            if (playerErrors.isNotEmpty()) return playerErrors.removeFirst()
            if (SystemClock.elapsedRealtime() >= deadline) return effectTimeoutFailure(stage)
            delay(25L)
        }
        return null
    }

    private fun effectTimeoutFailure(stage: NativePlaybackErrorStage): PlayerFailure {
        val error = PlaybackException("Media operation timed out", null, PlaybackException.ERROR_CODE_TIMEOUT)
        return PlayerFailure(
            error = error,
            diagnostic = NativePlaybackError.capture(stage, error, player.currentPosition.coerceAtLeast(0L)),
        )
    }

    private fun drainPlayerErrors() {
        playerErrors.clear()
    }

    override fun onPlayerError(error: PlaybackException) {
        if (owner == PlaybackOwner.LOCAL) {
            handleOfflinePlayerError(error)
            return
        }
        val stage = when {
            preparing -> NativePlaybackErrorStage.PREPARE
            activeExecution != null -> NativePlaybackErrorStage.COMMAND
            else -> NativePlaybackErrorStage.PLAYBACK
        }
        val failure = PlayerFailure(
            error = error,
            diagnostic = NativePlaybackError.capture(stage, error, player.currentPosition.coerceAtLeast(0L)),
        )
        currentResource?.playbackErrors?.add(failure.diagnostic)
        playerErrors.addLast(failure)
        if (preparing) return
        if (activeExecution != null) {
            if (acknowledgingCommand) deferredPlaybackError = failure
            return
        }
        val expected = registration ?: return
        val active = currentResource ?: return
        recoverAfterCommand(expected, active, failure)
    }

    private fun handleOfflinePlayerError(error: PlaybackException) {
        val currentId = player.currentMediaItem?.mediaId
        val currentIndex = player.currentMediaItemIndex
        val entry = offlineQueue.entries.firstOrNull { it.id == currentId }
        val decodeFailure = OfflinePlaybackPolicy.isConfirmedLocalDecodeError(
            error.errorCode,
            hasCause<SecurityException>(error) || hasCause<SSLException>(error),
        )
        scope.launch(Dispatchers.IO) {
            offlineLibrary.recordError(
                entry?.trackId,
                if (decodeFailure) "decode" else "playback",
                "Media3 playback error ${error.errorCode}",
            )
        }
        if (!decodeFailure || entry == null || currentIndex == C.INDEX_UNSET) {
            captureOfflinePosition()
            player.stop()
            val pending = offlinePendingEntryId
            offlinePendingEntryId = null
            if (pending != null) removeOfflineEntryInternal(pending)
            releaseOfflineRetention()
            offlineError = PublicError("local_playback", getString(R.string.offline_playback_error))
            persistOfflineQueue()
            publishOfflineState()
            return
        }

        offlineFailedEntryIds += entry.id
        offlineError = PublicError("local_decode", getString(R.string.offline_playback_decode_error))
        val failedIndices = offlineQueue.entries.mapIndexedNotNull { index, value ->
            index.takeIf { value.id in offlineFailedEntryIds }
        }.toSet()
        val candidateIds = OfflinePlaybackPolicy.failoverIndices(
            offlineQueue.entries.size,
            currentIndex,
            failedIndices,
            offlineQueue.repeatMode,
        ).mapNotNull { index -> offlineQueue.entries.getOrNull(index)?.id }
        val generation = offlineGeneration
        val commandGeneration = offlineCommandGeneration
        player.stop()
        scope.launch {
            for (candidateId in candidateIds) {
                val candidate = offlineQueue.entries.firstOrNull { it.id == candidateId } ?: continue
                val committed = try {
                    acquireOfflineRetention(candidate.trackId) { retention ->
                        val candidateIndex = offlineQueue.entries.indexOfFirst { it.id == candidate.id }
                        if (owner != PlaybackOwner.LOCAL || generation != offlineGeneration ||
                            commandGeneration != offlineCommandGeneration || candidateIndex < 0
                        ) {
                            false
                        } else {
                            player.seekTo(candidateIndex, 0L)
                            replaceOfflineRetention(candidate.id, retention)
                            player.prepare()
                            player.play()
                            captureOfflinePosition()
                            persistOfflineQueue()
                            publishOfflineState()
                            true
                        }
                    }
                } catch (_: OfflineLibraryException) {
                    false
                }
                if (committed) return@launch
                if (owner != PlaybackOwner.LOCAL || generation != offlineGeneration ||
                    commandGeneration != offlineCommandGeneration
                ) {
                    return@launch
                }
            }
            val pending = offlinePendingEntryId
            offlinePendingEntryId = null
            if (pending != null) removeOfflineEntryInternal(pending)
            releaseOfflineRetention()
            offlineError = PublicError("local_decode", getString(R.string.offline_playback_all_failed))
            captureOfflinePosition()
            persistOfflineQueue()
            publishOfflineState()
        }
    }

    private fun recoverAfterCommand(
        expected: Registration,
        active: ActiveResource,
        error: PlayerFailure,
    ) {
        if (recoveryJob != null || currentResource !== active || active.terminalReported) return
        active.playbackErrors.add(error.diagnostic)
        val classification = classifyPlayback(error)
        if (!classification.transient) {
            if (classification.fatalRegistration) {
                loseRegistration(expected, classification.publicCode, classification.publicMessage)
            } else {
                finishTerminalError(
                    expected,
                    active,
                    classification.reportCode,
                    classification.publicMessage,
                    error.diagnostic,
                )
            }
            return
        }
        val position = player.currentPosition.coerceAtLeast(0L)
        val shouldPlay = active.wantsPlayback
        recoveryJob = scope.launch {
            setRecovering(true)
            try {
                prepareWithRecovery(
                    expected,
                    active,
                    position,
                    shouldPlay,
                    initialRetry = 1,
                    priorFailure = error,
                )
            } catch (error: CancellationException) {
                throw error
            } catch (error: Throwable) {
                if (error is PlaybackFailure) {
                    finishTerminalError(
                        expected,
                        active,
                        error.reportCode,
                        error.message ?: getString(R.string.native_playback_error),
                        error.diagnostic,
                    )
                } else {
                    finishTerminalError(
                        expected,
                        active,
                        "media_error",
                        getString(R.string.native_playback_error),
                        NativePlaybackError.capture(
                            NativePlaybackErrorStage.PREPARE,
                            error,
                            player.currentPosition.coerceAtLeast(0L),
                        ),
                    )
                }
            } finally {
                if (recoveryJob === currentCoroutineContext()[Job]) recoveryJob = null
                if (registration === expected && currentResource === active) setRecovering(false)
            }
        }
    }

    override fun onPlaybackStateChanged(playbackState: Int) {
        if (owner == PlaybackOwner.LOCAL) {
            if (playbackState == Player.STATE_READY) {
                offlineError = null
            } else if (playbackState == Player.STATE_ENDED) {
                offlineFailedEntryIds.clear()
                captureOfflinePosition()
                val pending = offlinePendingEntryId
                if (pending != null) {
                    player.pause()
                    offlinePendingEntryId = null
                    removeOfflineEntryInternal(pending)
                    releaseOfflineRetention()
                    player.repeatMode = offlineQueue.repeatMode
                    if (offlineQueue.repeatMode == Player.REPEAT_MODE_ALL && offlineQueue.entries.isNotEmpty()) {
                        switchOfflineEntry(0, 0L, true)
                    }
                } else {
                    releaseOfflineRetention()
                }
                persistOfflineQueue()
            }
            publishOfflineState()
            return
        }
        if (playbackState != Player.STATE_ENDED || currentResource == null || preparing || recovering) return
        val expected = registration ?: return
        val active = currentResource ?: return
        if (active.terminalReported) return
        active.terminalReported = true
        val event = TerminalEvent("ended", active.playId, active.sequence, null, emptyList())
        if (activeExecution != null) {
            pendingTerminal = event
        } else {
            launchServerJob { sendTerminalObservation(expected, event) }
        }
    }

    override fun onMediaItemTransition(mediaItem: MediaItem?, reason: Int) {
        if (owner != PlaybackOwner.LOCAL || mediaItem == null) return
        if (reason == Player.MEDIA_ITEM_TRANSITION_REASON_AUTO) offlineFailedEntryIds.clear()
        offlineQueue = offlineQueue.copy(currentEntryId = mediaItem.mediaId, positionMs = 0L)
        if (player.playWhenReady || player.playbackState == Player.STATE_BUFFERING ||
            player.playbackState == Player.STATE_READY
        ) {
            handleOfflineTransition(mediaItem.mediaId)
        }
        publishOfflineState()
    }

    override fun onPositionDiscontinuity(
        oldPosition: Player.PositionInfo,
        newPosition: Player.PositionInfo,
        reason: Int,
    ) {
        if (owner != PlaybackOwner.LOCAL) return
        captureOfflinePosition()
        persistOfflineQueue()
        publishOfflineState()
    }

    override fun onIsPlayingChanged(isPlaying: Boolean) {
        if (owner == PlaybackOwner.LOCAL) publishOfflineState()
    }

    override fun onPlayWhenReadyChanged(playWhenReady: Boolean, reason: Int) {
        if (owner == PlaybackOwner.LOCAL) {
            if (!playWhenReady && reason == Player.PLAY_WHEN_READY_CHANGE_REASON_AUDIO_FOCUS_LOSS) {
                beginOfflineCommand()
            }
            captureOfflinePosition()
            persistOfflineQueue()
            publishOfflineState()
            return
        }
        if (!playWhenReady && reason == Player.PLAY_WHEN_READY_CHANGE_REASON_AUDIO_FOCUS_LOSS && currentResource != null) {
            currentResource?.wantsPlayback = false
            sendPlayerControl("pause")
        }
    }

    private fun finishTerminalError(
        expected: Registration,
        active: ActiveResource,
        errorCode: String,
        message: String,
        diagnostic: NativePlaybackError? = null,
    ) {
        if (active.terminalReported || currentResource !== active) return
        diagnostic?.let(active.playbackErrors::add)
        active.terminalReported = true
        player.stop()
        setError(if (errorCode == "media_failed") errorCode else "media_error", message)
        val event = TerminalEvent(
            "error",
            active.playId,
            active.sequence,
            errorCode.takeIf { it == "media_failed" },
            active.playbackErrors.snapshot(),
        )
        if (activeExecution != null) {
            pendingTerminal = event
        } else {
            launchServerJob { sendTerminalObservation(expected, event) }
        }
    }

    private suspend fun flushPendingTerminal(expected: Registration, sequence: Long) {
        val terminal = pendingTerminal ?: return
        pendingTerminal = null
        sendTerminalObservation(expected, terminal.copy(sequence = sequence))
    }

    private suspend fun sendTerminalObservation(expected: Registration, event: TerminalEvent) {
        if (registration !== expected || event.sequence <= 0L) return
        val active = currentResource?.takeIf { it.playId == event.playId && it.sequence == event.sequence } ?: return
        val body = reportBody(
            event.sequence,
            errorCode = event.errorCode,
            observation = observation(event.event, event.playId),
            playbackErrors = event.playbackErrors.takeIf { event.event == "error" },
        )
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
        launchServerJob {
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
        playbackErrors: List<NativePlaybackError>? = null,
    ): String = JSONObject()
        .put("sequence", sequence)
        .also { value ->
            if (result != null) value.put("result", result)
            if (errorCode != null) value.put("error_code", errorCode)
            if (observation != null) value.put("observation", observation)
            if (!playbackErrors.isNullOrEmpty()) {
                value.put("playback_errors", JSONArray(playbackErrors.map(NativePlaybackError::toJson)))
            }
        }
        .toString()

    private fun playbackErrorsForFailure(
        error: Throwable,
        action: String,
        sequence: Long,
    ): List<NativePlaybackError> {
        val diagnostic = when (error) {
            is PlaybackFailure -> error.diagnostic
            is CommandFailure -> null
            else -> NativePlaybackError.capture(
                NativePlaybackErrorStage.COMMAND,
                error,
                player.currentPosition.coerceAtLeast(0L),
            )
        } ?: return emptyList()
        val active = currentResource?.takeIf { action != "set_uri" || it.sequence == sequence }
            ?: return listOf(diagnostic)
        active.playbackErrors.add(diagnostic)
        return active.playbackErrors.snapshot()
    }

    private fun sendPlayerControl(action: String, positionMillis: Long? = null) {
        val expected = registration ?: return
        launchServerJob {
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

    private fun handleTransportFailure(
        expected: Registration,
        error: Throwable,
        registrationPoll: Boolean = false,
    ): Boolean {
        if (registration !== expected) return true
        val http = error as? ServerHttpException
        val fatal = http?.status in listOf(401, 403, 404) || hasCause<SSLException>(error)
        if (fatal) {
            if (registrationPoll && http?.status == 404 && handoffRegistration === expected) {
                handoffPollRemoval = expected
            }
            val code = if (hasCause<SSLException>(error)) "server_tls" else "authentication_required"
            val message = if (code == "server_tls") "The server certificate could not be verified." else "Sign in again to use phone playback."
            loseRegistration(expected, code, message)
        }
        return fatal
    }

    private fun loseRegistration(expected: Registration, code: String, message: String) {
        if (registration !== expected) return
        registration = null
        cancelServerTransport()
        cancelMediaWork()
        if (owner == PlaybackOwner.SERVER) setOwner(PlaybackOwner.NONE)
        if (handoffRegistration === expected) {
            publicError = null
            notifyState()
            return
        }
        setError(code, message)
    }

    private fun cancelMediaWork(clearMediaItems: Boolean = true) {
        recoveryJob?.cancel()
        recoveryJob = null
        preparing = false
        recovering = false
        pendingTerminal = null
        acknowledgingCommand = false
        deferredPlaybackError = null
        playerErrors.clear()
        player.stop()
        if (clearMediaItems) player.clearMediaItems()
        currentResource = null
        notifyState()
    }

    private fun launchServerJob(block: suspend CoroutineScope.() -> Unit): Job {
        lateinit var job: Job
        job = scope.launch(start = CoroutineStart.LAZY) {
            try {
                block()
            } finally {
                serverSideJobs.remove(job)
            }
        }
        serverSideJobs += job
        job.start()
        return job
    }

    private fun cancelServerTransport() {
        pollJob?.cancel()
        leaseJob?.cancel()
        leaseWatchdogJob?.cancel()
        identityJob?.cancel()
        activeExecution?.cancel()
        recoveryJob?.cancel()
        serverSideJobs.toList().forEach(Job::cancel)
        serverSideJobs.clear()
        pollJob = null
        leaseJob = null
        leaseWatchdogJob = null
        identityJob = null
        activeExecution = null
        recoveryJob = null
    }

    private suspend fun disconnectRegistrationForHandoff(existing: Registration) {
        handoffRegistration = existing
        handoffPollRemoval = null
        var removalObserved = false
        try {
            try {
                existing.client.json(
                    method = "DELETE",
                    path = registrationPath(existing.id),
                    ownerToken = existing.ownerToken,
                    allowEmpty = true,
                )
                removalObserved = true
            } catch (error: CancellationException) {
                throw error
            } catch (error: Throwable) {
                val removedByPoll = handoffPollRemoval === existing
                val deleteObservedMissing = (error as? ServerHttpException)?.status == 404
                if (removedByPoll || deleteObservedMissing) {
                    removalObserved = true
                } else if (registration === existing) {
                    val failure = error.toPublicFailure(
                        "handoff_failed",
                        "Server playback could not be disconnected.",
                    )
                    setError(failure.code, failure.message.orEmpty())
                    throw failure
                } else {
                    throw NativePlaybackException("handoff_failed", "Server playback ownership changed.", error)
                }
            }
            if (!OfflinePlaybackPolicy.confirmedDisconnectCanComplete(
                    removalObserved = removalObserved,
                    currentOwner = ownerName(),
                    registrationIsMissingOrExpected = registration == null || registration === existing,
                )
            ) {
                throw NativePlaybackException("handoff_failed", "Server playback ownership changed.")
            }
            cancelServerTransport()
            registration = null
            cancelMediaWork()
            setOwner(PlaybackOwner.NONE)
            publicError = null
            notifyState()
        } finally {
            if (handoffRegistration === existing) handoffRegistration = null
            if (handoffPollRemoval === existing) handoffPollRemoval = null
        }
    }

    private suspend fun disconnectRegistration(existing: Registration) {
        cancelServerTransport()
        try {
            existing.client.json(
                method = "DELETE",
                path = registrationPath(existing.id),
                ownerToken = existing.ownerToken,
                allowEmpty = true,
            )
        } catch (_: Throwable) {
            // The lease bounds stale registrations when a best-effort server-to-server disconnect cannot arrive.
        }
        if (registration === existing) registration = null
        if (owner == PlaybackOwner.SERVER) {
            setOwner(PlaybackOwner.NONE)
            cancelMediaWork()
        }
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
        publishOfflineState()
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

    private fun classifyPlayback(error: PlayerFailure): PlaybackClassification {
        val playbackException = error.error
        val httpStatus = causeChain(playbackException).filterIsInstance<HttpDataSource.InvalidResponseCodeException>()
            .firstOrNull()?.responseCode
        val errorCode = playbackException.errorCode
        val transient = NativePlaybackPolicy.isTransientMediaError(errorCode, httpStatus, playbackException)
        val terminalItemFailure = NativePlaybackPolicy.isTerminalItemMediaError(
            errorCode = errorCode,
            httpStatus = httpStatus,
            cause = playbackException,
            hasTransportCause = causeChain(playbackException).any {
                it is HttpDataSource.HttpDataSourceException
            },
        )
        val unsupported = errorCode in 3001..4005
        val fatalRegistration = httpStatus == 401 || hasCause<SSLException>(playbackException)
        return PlaybackClassification(
            transient = transient,
            fatalRegistration = fatalRegistration,
            reportCode = when {
                terminalItemFailure -> "media_failed"
                unsupported -> "media_unsupported"
                else -> "media_error"
            },
            publicCode = if (fatalRegistration) "authentication_required" else "media_error",
            publicMessage = if (unsupported) "This audio format is not supported." else getString(R.string.native_playback_error),
        )
    }

    private fun playbackFailure(error: PlayerFailure): PlaybackFailure {
        val classified = classifyPlayback(error)
        return PlaybackFailure(
            classified.reportCode,
            classified.publicMessage,
            error.diagnostic,
            error.error,
        )
    }

    private inner class OwnerRoutedPlayer(player: Player) : ForwardingSimpleBasePlayer(player) {
        private val serverCommands = Player.Commands.Builder()
            .addAllReadOnlyCommands()
            .addAll(
                Player.COMMAND_PLAY_PAUSE,
                Player.COMMAND_STOP,
                Player.COMMAND_RELEASE,
                Player.COMMAND_SEEK_IN_CURRENT_MEDIA_ITEM,
                Player.COMMAND_SEEK_TO_PREVIOUS,
                Player.COMMAND_SEEK_TO_PREVIOUS_MEDIA_ITEM,
                Player.COMMAND_SEEK_TO_NEXT,
                Player.COMMAND_SEEK_TO_NEXT_MEDIA_ITEM,
            )
            .build()
        private val localCommands = Player.Commands.Builder()
            .addAllReadOnlyCommands()
            .addAll(
                Player.COMMAND_PLAY_PAUSE,
                Player.COMMAND_STOP,
                Player.COMMAND_RELEASE,
                Player.COMMAND_SEEK_IN_CURRENT_MEDIA_ITEM,
                Player.COMMAND_SEEK_TO_PREVIOUS,
                Player.COMMAND_SEEK_TO_PREVIOUS_MEDIA_ITEM,
                Player.COMMAND_SEEK_TO_NEXT,
                Player.COMMAND_SEEK_TO_NEXT_MEDIA_ITEM,
                Player.COMMAND_SET_REPEAT_MODE,
                Player.COMMAND_SET_SHUFFLE_MODE,
            )
            .build()

        override fun getState() = super.getState().buildUpon()
            .setAvailableCommands(if (owner == PlaybackOwner.LOCAL) localCommands else serverCommands)
            .build()

        override fun handleSetPlayWhenReady(playWhenReady: Boolean): ListenableFuture<*> {
            beginOfflineCommand()
            when (owner) {
                PlaybackOwner.LOCAL -> if (playWhenReady) resumeOffline() else pauseOffline()
                PlaybackOwner.SERVER -> sendPlayerControl(if (playWhenReady) "play" else "pause")
                PlaybackOwner.NONE -> Unit
            }
            return Futures.immediateVoidFuture()
        }

        override fun handleStop(): ListenableFuture<*> {
            beginOfflineCommand()
            when (owner) {
                PlaybackOwner.LOCAL -> stopOffline()
                PlaybackOwner.SERVER -> sendPlayerControl("stop")
                PlaybackOwner.NONE -> Unit
            }
            return Futures.immediateVoidFuture()
        }

        override fun handleSeek(mediaItemIndex: Int, positionMs: Long, seekCommand: Int): ListenableFuture<*> {
            beginOfflineCommand()
            if (owner == PlaybackOwner.LOCAL) {
                when (seekCommand) {
                    Player.COMMAND_SEEK_TO_NEXT, Player.COMMAND_SEEK_TO_NEXT_MEDIA_ITEM -> nextOffline()
                    Player.COMMAND_SEEK_TO_PREVIOUS, Player.COMMAND_SEEK_TO_PREVIOUS_MEDIA_ITEM -> previousOffline()
                    else -> if (mediaItemIndex != player.currentMediaItemIndex) {
                        switchOfflineEntry(mediaItemIndex, positionMs, player.playWhenReady)
                    } else {
                        seekOffline(positionMs)
                    }
                }
            } else if (owner == PlaybackOwner.SERVER) {
                when (seekCommand) {
                    Player.COMMAND_SEEK_TO_NEXT, Player.COMMAND_SEEK_TO_NEXT_MEDIA_ITEM -> sendPlayerControl("next")
                    Player.COMMAND_SEEK_TO_PREVIOUS, Player.COMMAND_SEEK_TO_PREVIOUS_MEDIA_ITEM -> sendPlayerControl("previous")
                    else -> sendPlayerControl("seek", positionMs.coerceAtLeast(0L))
                }
            }
            return Futures.immediateVoidFuture()
        }

        override fun handleSetRepeatMode(repeatMode: Int): ListenableFuture<*> {
            beginOfflineCommand()
            if (owner == PlaybackOwner.LOCAL) setOfflineRepeat(repeatMode)
            return Futures.immediateVoidFuture()
        }

        override fun handleSetShuffleModeEnabled(shuffleModeEnabled: Boolean): ListenableFuture<*> {
            beginOfflineCommand()
            if (owner == PlaybackOwner.LOCAL) setOfflineShuffle(shuffleModeEnabled)
            return Futures.immediateVoidFuture()
        }
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
                .setAvailablePlayerCommands(LOCAL_SYSTEM_PLAYER_COMMANDS)
                .build()
        }
    }

    private enum class PlaybackOwner { NONE, SERVER, LOCAL }

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
        var everPlayed: Boolean = false,
        val playbackErrors: NativePlaybackErrorBuffer = NativePlaybackErrorBuffer(),
    )

    /** Records the stream the sink actually opened, which is what the settings panel reports. */
    private inner class AudioTrackObserver : AnalyticsListener {
        override fun onAudioTrackInitialized(
            eventTime: AnalyticsListener.EventTime,
            audioTrackConfig: AudioSink.AudioTrackConfig,
        ) {
            audioTrackStream = UsbBitPerfectController.encoding(audioTrackConfig.encoding)?.let { encoding ->
                PcmStream(
                    encoding = encoding,
                    sampleRate = audioTrackConfig.sampleRate,
                    channelCount = Integer.bitCount(audioTrackConfig.channelConfig),
                    channelMask = audioTrackConfig.channelConfig,
                )
            }
            audioTrackGeneration = bitPerfect.appliedGeneration
            notifyState()
        }

        override fun onAudioTrackReleased(
            eventTime: AnalyticsListener.EventTime,
            audioTrackConfig: AudioSink.AudioTrackConfig,
        ) {
            audioTrackStream = null
            notifyState()
        }
    }

    private data class PublicError(val code: String, val message: String)
    private data class TerminalEvent(
        val event: String,
        val playId: String,
        val sequence: Long,
        val errorCode: String?,
        val playbackErrors: List<NativePlaybackError>,
    )
    private data class PlaybackClassification(
        val transient: Boolean,
        val fatalRegistration: Boolean,
        val reportCode: String,
        val publicCode: String,
        val publicMessage: String,
    )

    private data class PlayerFailure(
        val error: PlaybackException,
        val diagnostic: NativePlaybackError,
    )
    private class CommandFailure(val reportCode: String, message: String) : Exception(message)
    private class PlaybackFailure(
        val reportCode: String,
        message: String,
        val diagnostic: NativePlaybackError,
        cause: Throwable?,
    ) : Exception(message, cause)

    companion object {
        private val LOCAL_SYSTEM_PLAYER_COMMANDS = Player.Commands.Builder()
            .addAllReadOnlyCommands()
            .addAll(
                Player.COMMAND_PLAY_PAUSE,
                Player.COMMAND_STOP,
                Player.COMMAND_SEEK_IN_CURRENT_MEDIA_ITEM,
                Player.COMMAND_SEEK_TO_PREVIOUS,
                Player.COMMAND_SEEK_TO_PREVIOUS_MEDIA_ITEM,
                Player.COMMAND_SEEK_TO_NEXT,
                Player.COMMAND_SEEK_TO_NEXT_MEDIA_ITEM,
                Player.COMMAND_SET_REPEAT_MODE,
                Player.COMMAND_SET_SHUFFLE_MODE,
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
        private const val PREVIOUS_RESTART_THRESHOLD_MILLIS = 3_000L
        private const val MAX_REPORTED_MIXER_ENTRIES = 32

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
