package io.jastreamer.android

import java.util.concurrent.atomic.AtomicBoolean
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.sync.Mutex

/** Legacy NSD owns a resolution until its callback, even after its caller is cancelled. */
internal class NsdResolutionGate {
    private val mutex = Mutex()

    suspend fun <T> resolve(start: ((Result<T>) -> Unit) -> Unit): T {
        val completed = AtomicBoolean(false)
        mutex.lock(completed)
        return suspendCancellableCoroutine { continuation ->
            if (!continuation.isActive) {
                mutex.unlock(completed)
                return@suspendCancellableCoroutine
            }
            val finish: (Result<T>) -> Unit = { result ->
                if (completed.compareAndSet(false, true)) {
                    mutex.unlock(completed)
                    if (continuation.isActive) continuation.resumeWith(result)
                }
            }
            try {
                start(finish)
            } catch (failure: Exception) {
                finish(Result.failure(failure))
            }
        }
    }
}
