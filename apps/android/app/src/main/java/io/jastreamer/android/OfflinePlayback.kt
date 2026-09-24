package io.jastreamer.android

import android.content.Context
import android.content.Intent
import android.os.Handler
import android.os.Looper
import androidx.media3.common.Player
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CancellationException
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
    private val queueMutationMutex = Mutex()
    private val mutableState = MutableStateFlow(OfflinePlaybackState())
    val state: StateFlow<OfflinePlaybackState> = mutableState.asStateFlow()
    internal val hasPendingMutation: Boolean
        get() = queueMutationMutex.isLocked

    suspend fun restore(context: Context) = queueMutationMutex.withLock {
        applicationContext = context.applicationContext
        val queue = withContext(Dispatchers.IO) {
            OfflinePlaybackPolicy.normalized(OfflineLibrary.get(context.applicationContext).loadQueue())
        }
        withContext(Dispatchers.Main.immediate) {
            val service = NativePlaybackRegistry.service
            when {
                service == null -> mutableState.value = OfflinePlaybackState(queue = queue)
                mutableState.value.owner == OfflinePlaybackPolicy.OWNER_NONE ->
                    service.adoptPersistedOfflineQueue(queue)
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
        queueMutationMutex.withLock {
            requireCurrentRequest(requestGeneration)
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
    }

    suspend fun enqueue(context: Context, trackIds: List<String>, next: Boolean) = queueMutationMutex.withLock {
        if (trackIds.isEmpty()) {
            throw NativePlaybackException("invalid_queue", "At least one saved track is required.")
        }
        val appContext = context.applicationContext
        applicationContext = appContext
        withContext(Dispatchers.Main.immediate) {
            val service = NativePlaybackRegistry.service
            if (service == null) {
                commitInactiveQueue(appContext) { queue -> OfflinePlaybackPolicy.enqueue(queue, trackIds, next) }
            } else {
                service.enqueueOffline(trackIds, next)
            }
        }
    }

    suspend fun playEntry(
        context: Context,
        entryId: String,
        confirmHandoff: Boolean = false,
        startPositionMs: Long = 0L,
    ) {
        if (entryId.isBlank()) {
            throw NativePlaybackException("queue_entry_unavailable", "The saved queue entry is no longer available.")
        }
        val requestGeneration = OfflinePlaybackRequestFence.beginRequest()
        queueMutationMutex.withLock {
            requireCurrentRequest(requestGeneration)
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
                service.playOfflineEntry(queue, entryId, confirmHandoff, requestGeneration, startPositionMs)
            }
        }
    }

    fun pause() = withService(NativePlaybackService::pauseOffline)
    fun resume() {
        val requestGeneration = OfflinePlaybackRequestFence.beginRequest()
        scope.launch {
            try {
                queueMutationMutex.withLock {
                    requireCurrentRequest(requestGeneration)
                    val existing = NativePlaybackRegistry.service
                    if (existing != null && mutableState.value.owner != OfflinePlaybackPolicy.OWNER_NONE) {
                        existing.resumeOffline()
                    } else {
                        val context = applicationContext ?: return@withLock
                        val service = existing ?: try {
                            context.startService(Intent(context, NativePlaybackService::class.java))
                            withTimeout(SERVICE_START_TIMEOUT_MILLIS) { NativePlaybackRegistry.awaitService() }
                        } catch (error: TimeoutCancellationException) {
                            throw NativePlaybackException("service_unavailable", "Saved music playback is unavailable.", error)
                        }
                        service.resumeRestoredOffline(mutableState.value.queue, requestGeneration)
                    }
                }
            } catch (error: Throwable) {
                if (error is CancellationException) throw error
                if (OfflinePlaybackRequestFence.isCurrent(requestGeneration)) publishFailure(error)
            }
        }
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
        mutableState.value = state
    }

    private fun mutateQueue(
        transform: (OfflineQueue) -> OfflineQueue,
        activeCommand: (NativePlaybackService) -> Unit,
    ) {
        val requestGeneration = OfflinePlaybackRequestFence.beginRequest()
        scope.launch {
            try {
                queueMutationMutex.withLock {
                    requireCurrentRequest(requestGeneration)
                    val service = NativePlaybackRegistry.service
                    if (mutableState.value.owner == OfflinePlaybackPolicy.OWNER_LOCAL && service != null) {
                        activeCommand(service)
                    } else {
                        val context = applicationContext ?: return@withLock
                        commitInactiveQueue(context, transform)
                    }
                }
            } catch (error: Throwable) {
                if (error is CancellationException) throw error
                if (OfflinePlaybackRequestFence.isCurrent(requestGeneration)) publishFailure(error)
            }
        }
    }

    private suspend fun commitInactiveQueue(context: Context, transform: (OfflineQueue) -> OfflineQueue) {
        val service = NativePlaybackRegistry.service
        if (service != null) {
            service.mutateInactiveOfflineQueue(transform)
            return
        }
        val updated = withContext(Dispatchers.IO) { OfflineLibrary.get(context).updateQueue(transform) }
        val currentService = NativePlaybackRegistry.service
        if (currentService == null) {
            mutableState.value = mutableState.value.copy(queue = updated)
        } else {
            currentService.adoptPersistedOfflineQueue(updated)
        }
    }

    private fun publishFailure(error: Throwable) {
        mutableState.value = mutableState.value.copy(
            errorCode = when (error) {
                is NativePlaybackException -> error.code
                is OfflineLibraryException -> error.code
                else -> "service_unavailable"
            },
            errorMessage = error.message ?: "Saved music playback is unavailable.",
        )
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
