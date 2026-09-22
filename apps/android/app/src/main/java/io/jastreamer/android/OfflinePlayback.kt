package io.jastreamer.android

import android.content.Context
import android.content.Intent
import android.os.Handler
import android.os.Looper
import androidx.media3.common.Player
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

/** Independent, device-owned playback facade. Completed music never depends on a Server session. */
object OfflinePlayback {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private var applicationContext: Context? = null
    private val queuePersistMutex = Mutex()
    private var queuePersistRevision = 0L
    private val mutableState = MutableStateFlow(OfflinePlaybackState())
    val state: StateFlow<OfflinePlaybackState> = mutableState.asStateFlow()

    suspend fun restore(context: Context) {
        applicationContext = context.applicationContext
        val queue = withContext(Dispatchers.IO) {
            OfflinePlaybackPolicy.normalized(OfflineLibrary.get(context.applicationContext).loadQueue())
        }
        withContext(Dispatchers.Main.immediate) {
            val service = NativePlaybackRegistry.service
            when {
                service == null -> mutableState.value = OfflinePlaybackState(queue = queue)
                mutableState.value.owner == OfflinePlaybackPolicy.OWNER_NONE ->
                    service.replaceRestoredOfflineQueue(queue)
            }
        }
    }

    suspend fun play(
        context: Context,
        trackIds: List<String>,
        startIndex: Int = 0,
        confirmHandoff: Boolean = false,
    ) {
        val requestGeneration = OfflinePlaybackRequestFence.beginRequest()
        val appContext = context.applicationContext
        applicationContext = appContext
        val tracks = loadAvailableTracks(appContext, trackIds)
        requireCurrentRequest(requestGeneration)
        val service = withContext(Dispatchers.Main.immediate) {
            try {
                appContext.startService(Intent(appContext, NativePlaybackService::class.java))
            } catch (error: RuntimeException) {
                throw NativePlaybackException("service_unavailable", "Saved music playback is unavailable.", error)
            }
            try {
                withTimeout(SERVICE_START_TIMEOUT_MILLIS) { NativePlaybackRegistry.awaitService() }
            } catch (error: TimeoutCancellationException) {
                throw NativePlaybackException("service_unavailable", "Saved music playback is unavailable.", error)
            }
        }
        requireCurrentRequest(requestGeneration)
        withContext(Dispatchers.Main.immediate) {
            service.playOffline(tracks, startIndex, confirmHandoff, requestGeneration)
        }
    }

    suspend fun enqueue(context: Context, trackIds: List<String>, next: Boolean) {
        if (trackIds.isEmpty()) {
            throw NativePlaybackException("invalid_queue", "At least one saved track is required.")
        }
        val requestGeneration = OfflinePlaybackRequestFence.beginRequest()
        val appContext = context.applicationContext
        applicationContext = appContext
        val tracks = loadAvailableTracks(appContext, trackIds)
        requireCurrentRequest(requestGeneration)
        val handledByService = withContext(Dispatchers.Main.immediate) {
            val service = NativePlaybackRegistry.service ?: return@withContext false
            service.enqueueOffline(
                savedQueue = mutableState.value.queue,
                tracks = tracks,
                next = next,
                requestGeneration = requestGeneration,
            )
            true
        }
        if (handledByService) return

        val (revision, baseQueue) = withContext(Dispatchers.Main.immediate) {
            requireCurrentRequest(requestGeneration)
            ++queuePersistRevision to mutableState.value.queue
        }
        val updated = withContext(Dispatchers.IO) {
            queuePersistMutex.withLock {
                requireCurrentRequest(requestGeneration)
                if (revision != queuePersistRevision) {
                    throw NativePlaybackException("superseded", "A newer playback action replaced this request.")
                }
                val updated = OfflinePlaybackPolicy.enqueue(
                    OfflinePlaybackPolicy.normalized(baseQueue),
                    trackIds,
                    next,
                )
                OfflineLibrary.get(appContext).saveQueue(updated)
                updated
            }
        }
        requireCurrentRequest(requestGeneration)
        withContext(Dispatchers.Main.immediate) {
            requireCurrentRequest(requestGeneration)
            val service = NativePlaybackRegistry.service
            if (service == null) {
                mutableState.value = mutableState.value.copy(queue = updated)
            } else {
                service.replaceRestoredOfflineQueue(updated)
            }
        }
    }

    suspend fun playEntry(
        context: Context,
        entryId: String,
        confirmHandoff: Boolean = false,
    ) {
        if (entryId.isBlank()) {
            throw NativePlaybackException("queue_entry_unavailable", "The saved queue entry is no longer available.")
        }
        val requestGeneration = OfflinePlaybackRequestFence.beginRequest()
        val appContext = context.applicationContext
        applicationContext = appContext
        val stateQueue = withContext(Dispatchers.Main.immediate) { mutableState.value.queue }
        val queue = withContext(Dispatchers.IO) {
            val library = OfflineLibrary.get(appContext)
            val candidate = stateQueue.takeIf { saved -> saved.entries.any { it.id == entryId } }
                ?: OfflinePlaybackPolicy.normalized(library.loadQueue())
            val entry = candidate.entries.firstOrNull { it.id == entryId }
                ?: throw NativePlaybackException(
                    "queue_entry_unavailable",
                    "The saved queue entry is no longer available.",
                )
            library.track(entry.trackId)?.takeUnless { it.pendingDelete }
                ?: throw NativePlaybackException("track_unavailable", "Saved audio is no longer available.")
            candidate
        }
        requireCurrentRequest(requestGeneration)
        val service = withContext(Dispatchers.Main.immediate) {
            try {
                appContext.startService(Intent(appContext, NativePlaybackService::class.java))
            } catch (error: RuntimeException) {
                throw NativePlaybackException("service_unavailable", "Saved music playback is unavailable.", error)
            }
            try {
                withTimeout(SERVICE_START_TIMEOUT_MILLIS) { NativePlaybackRegistry.awaitService() }
            } catch (error: TimeoutCancellationException) {
                throw NativePlaybackException("service_unavailable", "Saved music playback is unavailable.", error)
            }
        }
        requireCurrentRequest(requestGeneration)
        withContext(Dispatchers.Main.immediate) {
            service.playOfflineEntry(queue, entryId, confirmHandoff, requestGeneration)
        }
    }

    fun pause() = withService(NativePlaybackService::pauseOffline)
    fun resume() {
        val requestGeneration = OfflinePlaybackRequestFence.beginRequest()
        val run = resume@{
            if (!OfflinePlaybackRequestFence.isCurrent(requestGeneration)) return@resume
            val existing = NativePlaybackRegistry.service
            if (existing != null && mutableState.value.owner != OfflinePlaybackPolicy.OWNER_NONE) {
                existing.resumeOffline()
                return@resume
            }
            val context = applicationContext ?: return@resume
            val queue = mutableState.value.queue
            scope.launch {
                try {
                    val service = existing ?: run {
                        context.startService(Intent(context, NativePlaybackService::class.java))
                        withTimeout(SERVICE_START_TIMEOUT_MILLIS) {
                            NativePlaybackRegistry.awaitService()
                        }
                    }
                    service.resumeRestoredOffline(queue, requestGeneration)
                } catch (error: NativePlaybackException) {
                    if (OfflinePlaybackRequestFence.isCurrent(requestGeneration)) {
                        mutableState.value = mutableState.value.copy(
                            errorCode = error.code,
                            errorMessage = error.message,
                        )
                    }
                } catch (_: Throwable) {
                    if (OfflinePlaybackRequestFence.isCurrent(requestGeneration)) {
                        mutableState.value = mutableState.value.copy(
                            errorCode = "service_unavailable",
                            errorMessage = "Saved music playback is unavailable.",
                        )
                    }
                }
            }
        }
        if (Looper.myLooper() == Looper.getMainLooper()) run() else Handler(Looper.getMainLooper()).post(run)
    }
    fun stop() = withService(NativePlaybackService::stopOffline)
    fun seekTo(positionMs: Long) = withService { it.seekOffline(positionMs) }
    fun next() = withService(NativePlaybackService::nextOffline)
    fun previous() = withService(NativePlaybackService::previousOffline)
    fun setShuffle(enabled: Boolean) = mutateQueue(
        transform = { it.copy(shuffle = enabled) },
        activeCommand = { it.setOfflineShuffle(enabled) },
    )

    fun setRepeat(mode: Int) {
        require(mode == Player.REPEAT_MODE_OFF || mode == Player.REPEAT_MODE_ONE || mode == Player.REPEAT_MODE_ALL)
        mutateQueue(
            transform = { it.copy(repeatMode = mode) },
            activeCommand = { it.setOfflineRepeat(mode) },
        )
    }

    fun moveEntry(entryId: String, offset: Int) = mutateQueue(
        transform = { OfflinePlaybackPolicy.move(it, entryId, offset) },
        activeCommand = { it.moveOfflineEntry(entryId, offset) },
    )

    fun removeEntry(entryId: String) = mutateQueue(
        transform = { OfflinePlaybackPolicy.remove(it, entryId) },
        activeCommand = { it.removeOfflineEntry(entryId) },
    )

    internal fun publish(state: OfflinePlaybackState) {
        queuePersistRevision++
        mutableState.value = state
    }

    private fun mutateQueue(
        transform: (OfflineQueue) -> OfflineQueue,
        activeCommand: (NativePlaybackService) -> Unit,
    ) {
        val requestGeneration = OfflinePlaybackRequestFence.beginRequest()
        val run = mutate@{
            if (!OfflinePlaybackRequestFence.isCurrent(requestGeneration)) return@mutate
            val service = NativePlaybackRegistry.service
            if (mutableState.value.owner == OfflinePlaybackPolicy.OWNER_LOCAL && service != null) {
                activeCommand(service)
                return@mutate
            }
            val updated = OfflinePlaybackPolicy.normalized(transform(mutableState.value.queue))
            if (updated == mutableState.value.queue) return@mutate
            mutableState.value = mutableState.value.copy(queue = updated)
            if (service != null) {
                service.replaceRestoredOfflineQueue(updated)
            } else {
                persistRestoredQueue(updated)
            }
        }
        if (Looper.myLooper() == Looper.getMainLooper()) run() else Handler(Looper.getMainLooper()).post(run)
    }

    private fun persistRestoredQueue(queue: OfflineQueue) {
        val context = applicationContext ?: return
        val revision = ++queuePersistRevision
        scope.launch(Dispatchers.IO) {
            var replacement: OfflineQueue? = null
            queuePersistMutex.withLock {
                if (revision != queuePersistRevision) return@withLock
                try {
                    OfflineLibrary.get(context).saveQueue(queue)
                } catch (_: OfflineLibraryException) {
                    replacement = OfflinePlaybackPolicy.normalized(OfflineLibrary.get(context).loadQueue())
                }
            }
            replacement?.let { restored ->
                withContext(Dispatchers.Main.immediate) {
                    if (revision == queuePersistRevision && NativePlaybackRegistry.service == null) {
                        mutableState.value = mutableState.value.copy(queue = restored)
                    }
                }
            }
        }
    }

    private inline fun withService(crossinline command: (NativePlaybackService) -> Unit) {
        val requestGeneration = OfflinePlaybackRequestFence.beginRequest()
        val run = {
            if (OfflinePlaybackRequestFence.isCurrent(requestGeneration)) {
                NativePlaybackRegistry.service?.let(command)
            }
        }
        if (Looper.myLooper() == Looper.getMainLooper()) run() else Handler(Looper.getMainLooper()).post(run)
    }

    private suspend fun loadAvailableTracks(context: Context, trackIds: List<String>): List<OfflineTrack> =
        withContext(Dispatchers.IO) {
            val library = OfflineLibrary.get(context)
            trackIds.map { id ->
                library.track(id)?.takeUnless { it.pendingDelete }
                    ?: throw NativePlaybackException("track_unavailable", "Saved audio is no longer available.")
            }
        }

    private fun requireCurrentRequest(requestGeneration: Long) {
        if (!OfflinePlaybackRequestFence.isCurrent(requestGeneration)) {
            throw NativePlaybackException("superseded", "A newer playback action replaced this request.")
        }
    }

    private const val SERVICE_START_TIMEOUT_MILLIS = 5_000L
}
