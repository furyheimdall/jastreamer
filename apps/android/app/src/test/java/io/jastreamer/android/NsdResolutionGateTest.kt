package io.jastreamer.android

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.async
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.yield
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test

class NsdResolutionGateTest {
    @Test
    fun cancelledCallerRetainsNativeOwnershipUntilCallbackAcrossNewObservers() = runBlocking {
        val gate = NsdResolutionGate()
        lateinit var finishFirst: (Result<String>) -> Unit
        lateinit var finishSecond: (Result<String>) -> Unit
        val secondStarted = CompletableDeferred<Unit>()
        var thirdStarted = false
        val first = async(start = CoroutineStart.UNDISPATCHED) {
            gate.resolve<String> { finishFirst = it }
        }
        first.cancelAndJoin()
        val second = async(start = CoroutineStart.UNDISPATCHED) {
            gate.resolve<String> {
                finishSecond = it
                secondStarted.complete(Unit)
            }
        }
        assertFalse("A cancelled coroutine must not overlap the still-active native request", secondStarted.isCompleted)
        finishFirst(Result.success("discarded cancelled result"))
        secondStarted.await()
        val third = async(start = CoroutineStart.UNDISPATCHED) {
            gate.resolve<String> {
                thirdStarted = true
                it(Result.success("third"))
            }
        }
        finishFirst(Result.failure(IllegalStateException("duplicate stale callback")))
        yield()
        assertFalse("A stale callback must not release the next request's ownership", thirdStarted)
        finishSecond(Result.success("second"))
        assertEquals("second", second.await())
        assertEquals("third", third.await())
    }

    @Test
    fun cancellationWhileQueuedDoesNotStartAnAbandonedNativeRequest() = runBlocking {
        val gate = NsdResolutionGate()
        lateinit var finish: (Result<String>) -> Unit
        val first = async(start = CoroutineStart.UNDISPATCHED) { gate.resolve<String> { finish = it } }
        var abandonedStarted = false
        val abandoned = async(start = CoroutineStart.UNDISPATCHED) {
            gate.resolve<String> {
                abandonedStarted = true
                it(Result.success("abandoned"))
            }
        }
        abandoned.cancelAndJoin()
        finish(Result.success("first"))
        first.await()
        yield()
        assertFalse(abandonedStarted)
        assertEquals("next", gate.resolve<String> { it(Result.success("next")) })
    }
}
