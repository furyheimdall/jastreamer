package io.jastreamer.android

import androidx.media3.common.C
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class UsbDirectClockTest {
    /** One second of 44.1 kHz stereo 24-bit audio, as the decoder hands it over. */
    private val frameBytes = 6
    private val secondBytes = 44_100 * frameBytes

    private fun started(startUs: Long = 0L): UsbDirectClock = UsbDirectClock().apply {
        configure(44_100, frameBytes, playedFrames = 1_000L)
        onFirstBuffer(startUs)
    }

    @Test
    fun `position is unset until the first buffer arrives`() {
        val clock = UsbDirectClock()
        clock.configure(44_100, frameBytes, playedFrames = 0L)

        assertEquals(C.TIME_UNSET, clock.positionUs(0L))
        clock.onFirstBuffer(5_000_000L)
        assertEquals(5_000_000L, clock.positionUs(0L))
    }

    @Test
    fun `position follows the frames the device consumed, measured from the stream start`() {
        val clock = started(startUs = 2_000_000L)
        clock.accept(secondBytes)

        // The device counter carries the frames played before this stream started.
        assertEquals(2_000_000L, clock.positionUs(1_000L))
        assertEquals(2_500_000L, clock.positionUs(1_000L + 22_050L))
        assertEquals(3_000_000L, clock.positionUs(1_000L + 44_100L))
    }

    @Test
    fun `position never runs past the audio that was actually queued`() {
        val clock = started()
        clock.accept(secondBytes)

        // A device counter beyond the queued audio (silence after an underrun) cannot move the
        // media position past the last frame handed over.
        assertEquals(1_000_000L, clock.positionUs(1_000L + 88_200L))
    }

    @Test
    fun `a partial write only counts whole frames`() {
        val clock = started()
        clock.accept(frameBytes * 10 + 3)

        assertEquals(10L, clock.writtenFrames)
    }

    @Test
    fun `pending data clears only once the device consumed everything queued`() {
        val clock = started()
        clock.accept(secondBytes)

        assertTrue(clock.hasPendingData(1_000L))
        assertTrue(clock.hasPendingData(1_000L + 44_099L))
        assertFalse(clock.hasPendingData(1_000L + 44_100L))
    }

    @Test
    fun `end of stream drains before the sink reports ended`() {
        val clock = started()
        clock.accept(secondBytes)
        assertFalse(clock.isEnded(1_000L))

        clock.endOfStream()
        assertFalse(clock.isEnded(1_000L + 44_099L))
        assertTrue(clock.isEnded(1_000L + 44_100L))
    }

    @Test
    fun `a flush restarts the accounting at the device counter it happened on`() {
        val clock = started(startUs = 1_000_000L)
        clock.accept(secondBytes)
        clock.endOfStream()

        clock.reset(playedFrames = 1_000L + 30_000L)
        assertEquals(0L, clock.writtenFrames)
        assertFalse(clock.hasPendingData(1_000L + 30_000L))
        assertFalse(clock.isEnded(1_000L + 30_000L))
        assertEquals(C.TIME_UNSET, clock.positionUs(1_000L + 30_000L))

        // The seek target becomes the new stream start.
        clock.onFirstBuffer(20_000_000L)
        clock.accept(secondBytes)
        assertEquals(20_500_000L, clock.positionUs(1_000L + 30_000L + 22_050L))
    }
}
