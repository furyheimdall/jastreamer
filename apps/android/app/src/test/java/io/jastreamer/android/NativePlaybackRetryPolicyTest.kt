package io.jastreamer.android

import java.io.IOException
import java.net.SocketTimeoutException
import javax.net.ssl.SSLHandshakeException
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class NativePlaybackRetryPolicyTest {
    @Test
    fun `recovery backoff has exactly three bounded retries`() {
        assertEquals(250L, NativePlaybackPolicy.recoveryDelayMillis(1))
        assertEquals(750L, NativePlaybackPolicy.recoveryDelayMillis(2))
        assertEquals(1_500L, NativePlaybackPolicy.recoveryDelayMillis(3))
        assertNull(NativePlaybackPolicy.recoveryDelayMillis(0))
        assertNull(NativePlaybackPolicy.recoveryDelayMillis(4))
        assertEquals(3, NativePlaybackPolicy.MAX_MEDIA_RETRIES)
        assertTrue(NativePlaybackPolicy.MEDIA_RECOVERY_DEADLINE_MILLIS <= 12_000L)
    }

    @Test
    fun `only transient transport failures enter media recovery`() {
        assertTrue(NativePlaybackPolicy.isTransientMediaError(2000))
        assertTrue(NativePlaybackPolicy.isTransientMediaError(2001))
        assertTrue(NativePlaybackPolicy.isTransientMediaError(2002))
        assertTrue(NativePlaybackPolicy.isTransientMediaError(2004, httpStatus = 503))
        assertTrue(NativePlaybackPolicy.isTransientMediaError(2004, httpStatus = 429))

        assertFalse(NativePlaybackPolicy.isTransientMediaError(2004, httpStatus = 401))
        assertFalse(NativePlaybackPolicy.isTransientMediaError(2004, httpStatus = 404))
        assertFalse(NativePlaybackPolicy.isTransientMediaError(4005))
        assertFalse(
            NativePlaybackPolicy.isTransientMediaError(
                2000,
                cause = IOException("transport", SSLHandshakeException("identity")),
            ),
        )
    }

    @Test
    fun `terminal item media failures include only confirmed parser decoder and runtime boundaries`() {
        listOf(1004, 3001, 3002, 3003, 3004, 4003, 4004, 4005).forEach { errorCode ->
            assertTrue(
                "Expected Media3 error $errorCode to fail the current item",
                NativePlaybackPolicy.isTerminalItemMediaError(errorCode),
            )
        }

        listOf(0, 3000, 3005, 4001, 4002, 4006, 9999).forEach { errorCode ->
            assertFalse(
                "Expected Media3 error $errorCode to remain non-skippable",
                NativePlaybackPolicy.isTerminalItemMediaError(errorCode),
            )
        }
    }

    @Test
    fun `exhausted transient media recovery never becomes an item failure`() {
        listOf(1003, 2000, 2001, 2002).forEach { errorCode ->
            assertTrue(NativePlaybackPolicy.isTransientMediaError(errorCode))
            assertFalse(NativePlaybackPolicy.isTerminalItemMediaError(errorCode))
        }
        assertTrue(NativePlaybackPolicy.isTransientMediaError(2004, httpStatus = 503))
        assertFalse(NativePlaybackPolicy.isTerminalItemMediaError(3001, httpStatus = 503))
    }

    @Test
    fun `transport authentication permission and TLS take precedence over item failure codes`() {
        listOf(401, 403, 404, 408, 429, 503).forEach { httpStatus ->
            assertFalse(
                NativePlaybackPolicy.isTerminalItemMediaError(3001, httpStatus = httpStatus),
            )
        }
        assertFalse(
            NativePlaybackPolicy.isTerminalItemMediaError(
                4003,
                cause = RuntimeException("outer", SSLHandshakeException("identity")),
            ),
        )
        assertFalse(
            NativePlaybackPolicy.isTerminalItemMediaError(
                1004,
                cause = SecurityException("permission denied"),
            ),
        )
        assertFalse(
            NativePlaybackPolicy.isTerminalItemMediaError(
                1004,
                cause = SocketTimeoutException("transport timeout"),
            ),
        )
        assertFalse(
            NativePlaybackPolicy.isTerminalItemMediaError(
                1004,
                hasTransportCause = true,
            ),
        )
    }

    @Test
    fun `lease deadlines reject invalid or unbounded server durations`() {
        assertEquals(16_000L, NativePlaybackPolicy.leaseDeadline(1_000L, 15_000L))
        assertNull(NativePlaybackPolicy.leaseDeadline(1_000L, 0L))
        assertNull(NativePlaybackPolicy.leaseDeadline(1_000L, 60_001L))
        assertNull(NativePlaybackPolicy.leaseDeadline(Long.MAX_VALUE, 15_000L))
    }

    @Test
    fun `sequence fence prevents replay and applies cancellation monotonically`() {
        val fence = SequenceFence()
        assertTrue(fence.shouldExecute(1))
        fence.complete(1)
        assertFalse(fence.shouldExecute(1))
        assertTrue(fence.shouldExecute(2))

        fence.cancelBefore(2)
        assertFalse(fence.shouldExecute(2))
        assertTrue(fence.shouldExecute(3))
        fence.cancelBefore(1)
        assertFalse(fence.shouldExecute(2))
    }
}
