package io.jastreamer.android

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

class UsbDirectTestPolicyTest {
    @Test
    fun theDacIsNeverClaimedBeforeTheSourceDecodes() {
        expectOrderFailure(sourceOpen = false, decodedBytes = 0)
        expectOrderFailure(sourceOpen = false, decodedBytes = 4_096)
        expectOrderFailure(sourceOpen = true, decodedBytes = 0)

        UsbDirectTestPolicy.requireClaimAllowed(sourceOpen = true, decodedBytes = 1)
    }

    @Test
    fun theRunOpensTheSourceBeforeTheDevice() {
        val source = UsbDirectTestPolicy.requireTransition(
            UsbDirectTestPolicy.PHASE_IDLE,
            UsbDirectTestPolicy.PHASE_SOURCE,
        )
        val device = UsbDirectTestPolicy.requireTransition(source, UsbDirectTestPolicy.PHASE_DEVICE)
        assertEquals(
            UsbDirectTestPolicy.PHASE_PLAYING,
            UsbDirectTestPolicy.requireTransition(device, UsbDirectTestPolicy.PHASE_PLAYING),
        )

        // Skipping the source phase, or running the phases backwards, is refused.
        expectTransitionFailure(UsbDirectTestPolicy.PHASE_IDLE, UsbDirectTestPolicy.PHASE_DEVICE)
        expectTransitionFailure(UsbDirectTestPolicy.PHASE_IDLE, UsbDirectTestPolicy.PHASE_PLAYING)
        expectTransitionFailure(UsbDirectTestPolicy.PHASE_SOURCE, UsbDirectTestPolicy.PHASE_PLAYING)
        expectTransitionFailure(UsbDirectTestPolicy.PHASE_PLAYING, UsbDirectTestPolicy.PHASE_DEVICE)
        expectTransitionFailure(UsbDirectTestPolicy.PHASE_ERROR, UsbDirectTestPolicy.PHASE_SOURCE)
    }

    @Test
    fun stopAndFailureEndAnyPhase() {
        UsbDirectTestPolicy.phases.forEach { phase ->
            assertEquals(
                UsbDirectTestPolicy.PHASE_IDLE,
                UsbDirectTestPolicy.requireTransition(phase, UsbDirectTestPolicy.PHASE_IDLE),
            )
            assertEquals(
                UsbDirectTestPolicy.PHASE_ERROR,
                UsbDirectTestPolicy.requireTransition(phase, UsbDirectTestPolicy.PHASE_ERROR),
            )
        }
    }

    @Test
    fun onlyAnActiveRunIsBusy() {
        assertTrue(UsbDirectTestPolicy.busy(UsbDirectTestPolicy.PHASE_SOURCE))
        assertTrue(UsbDirectTestPolicy.busy(UsbDirectTestPolicy.PHASE_DEVICE))
        assertTrue(UsbDirectTestPolicy.busy(UsbDirectTestPolicy.PHASE_PLAYING))
        assertFalse(UsbDirectTestPolicy.busy(UsbDirectTestPolicy.PHASE_IDLE))
        assertFalse(UsbDirectTestPolicy.busy(UsbDirectTestPolicy.PHASE_ERROR))
    }

    @Test
    fun sourceFailuresNameTheHttpStatus() {
        val (missingCode, missingMessage) = UsbDirectTestPolicy.sourceFailure(404)
        assertEquals("usb_source_missing", missingCode)
        assertTrue(missingMessage, missingMessage.endsWith("(HTTP 404)"))

        assertEquals("usb_source_unauthorized", UsbDirectTestPolicy.sourceFailure(401).first)
        assertEquals("usb_source_unauthorized", UsbDirectTestPolicy.sourceFailure(403).first)
        assertEquals("usb_source_missing", UsbDirectTestPolicy.sourceFailure(410).first)
        assertEquals("usb_source_failed", UsbDirectTestPolicy.sourceFailure(500).first)

        // Without a response there is no status to report, and the detail is kept as it is.
        val (code, message) = UsbDirectTestPolicy.sourceFailure(null, "Connection reset")
        assertEquals("usb_source_failed", code)
        assertEquals("Connection reset", message)

        // A nonsense status never becomes part of the message.
        assertFalse(UsbDirectTestPolicy.sourceFailure(0).second.contains("HTTP"))

        // A detail that already carries the status is reported once, not twice.
        assertEquals(
            "Server download request failed (HTTP 404)",
            UsbDirectTestPolicy.sourceFailure(404, "Server download request failed (HTTP 404)").second,
        )
    }

    @Test
    fun preparationIsBoundedAndPollingBacksOff() {
        assertFalse(UsbDirectTestPolicy.prepareExpired(0))
        assertFalse(UsbDirectTestPolicy.prepareExpired(UsbDirectTestPolicy.PREPARE_TIMEOUT_MILLIS - 1))
        assertTrue(UsbDirectTestPolicy.prepareExpired(UsbDirectTestPolicy.PREPARE_TIMEOUT_MILLIS))

        var previous = 0L
        (0..12).forEach { attempt ->
            val delay = UsbDirectTestPolicy.preparePollDelayMillis(attempt)
            assertTrue("attempt $attempt delayed ${delay}ms", delay in 1..2_000)
            assertTrue("attempt $attempt went backwards", delay >= previous)
            previous = delay
        }
        assertEquals(
            UsbDirectTestPolicy.preparePollDelayMillis(6),
            UsbDirectTestPolicy.preparePollDelayMillis(60),
        )
    }

    private fun expectOrderFailure(sourceOpen: Boolean, decodedBytes: Long) {
        try {
            UsbDirectTestPolicy.requireClaimAllowed(sourceOpen, decodedBytes)
            fail("claiming the DAC with sourceOpen=$sourceOpen decoded=$decodedBytes must fail")
        } catch (failure: UsbDirectException) {
            assertEquals("usb_direct_order", failure.code)
        }
    }

    private fun expectTransitionFailure(current: String, next: String) {
        try {
            UsbDirectTestPolicy.requireTransition(current, next)
            fail("$current must not advance to $next")
        } catch (failure: UsbDirectException) {
            assertEquals("usb_direct_order", failure.code)
        }
    }
}
