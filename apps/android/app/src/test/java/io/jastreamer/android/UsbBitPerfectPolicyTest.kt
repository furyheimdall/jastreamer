package io.jastreamer.android

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

private const val STEREO_MASK = 12
private const val MONO_MASK = 4

class UsbBitPerfectPolicyTest {
    private fun source(
        sampleRate: Int = 44_100,
        channelCount: Int = 2,
        encoding: PcmEncoding? = PcmEncoding.PCM_16,
        lossless: Boolean = true,
        transformed: Boolean? = false,
    ) = SourceStream(sampleRate, channelCount, encoding, lossless, transformed)

    private fun stream(
        encoding: PcmEncoding = PcmEncoding.PCM_16,
        sampleRate: Int = 44_100,
        channelCount: Int = 2,
        channelMask: Int = STEREO_MASK,
    ) = PcmStream(encoding, sampleRate, channelCount, channelMask)

    @Test
    fun `integer sources above 16 bits are written as 16-bit unless high resolution output is enabled`() {
        assertEquals(PcmEncoding.PCM_16, UsbBitPerfectPolicy.plannedEncoding(PcmEncoding.PCM_24, floatOutput = false))
        assertEquals(PcmEncoding.PCM_16, UsbBitPerfectPolicy.plannedEncoding(PcmEncoding.PCM_32, floatOutput = false))
        assertEquals(PcmEncoding.PCM_16, UsbBitPerfectPolicy.plannedEncoding(PcmEncoding.PCM_16, floatOutput = true))
        assertEquals(PcmEncoding.PCM_FLOAT, UsbBitPerfectPolicy.plannedEncoding(PcmEncoding.PCM_24, floatOutput = true))
    }

    @Test
    fun `an undeclared source precision is planned as the 16-bit decoder output`() {
        assertEquals(PcmEncoding.PCM_16, UsbBitPerfectPolicy.plannedEncoding(null, floatOutput = false))
        assertEquals(PcmEncoding.PCM_16, UsbBitPerfectPolicy.plannedEncoding(null, floatOutput = true))
    }

    @Test
    fun `the planned stream keeps the source rate and layout`() {
        val planned = UsbBitPerfectPolicy.plannedStream(
            source(sampleRate = 96_000, channelCount = 2, encoding = PcmEncoding.PCM_24),
            channelMask = STEREO_MASK,
            floatOutput = false,
        )

        assertEquals(stream(sampleRate = 96_000), planned)
    }

    @Test
    fun `an unusable channel layout has no planned stream`() {
        assertNull(
            UsbBitPerfectPolicy.plannedStream(source(channelCount = 9), channelMask = 0, floatOutput = false),
        )
        assertNull(
            UsbBitPerfectPolicy.plannedStream(source(sampleRate = 0), channelMask = STEREO_MASK, floatOutput = false),
        )
    }

    @Test
    fun `only an identical bit-perfect mixer format is selected`() {
        val exact = MixerOption(stream(), bitPerfect = true)
        val options = listOf(
            MixerOption(stream(encoding = PcmEncoding.PCM_24), bitPerfect = true),
            MixerOption(stream(sampleRate = 48_000), bitPerfect = true),
            MixerOption(stream(channelCount = 1, channelMask = MONO_MASK), bitPerfect = true),
            exact,
        )

        assertEquals(exact, UsbBitPerfectPolicy.select(stream(), options))
    }

    @Test
    fun `a default mixer behaviour is never substituted for a bit-perfect one`() {
        val options = listOf(MixerOption(stream(), bitPerfect = false))

        assertNull(UsbBitPerfectPolicy.select(stream(), options))
    }

    @Test
    fun `availability reports the platform before the device and the device before the mixer`() {
        assertEquals(
            UsbBitPerfectPolicy.UNAVAILABLE_REQUIRES_ANDROID_14,
            UsbBitPerfectPolicy.unavailableReason(apiLevel = 33, usbDeviceCount = 1, bitPerfectOptionCount = 4),
        )
        assertEquals(
            UsbBitPerfectPolicy.UNAVAILABLE_NO_USB_DEVICE,
            UsbBitPerfectPolicy.unavailableReason(apiLevel = 34, usbDeviceCount = 0, bitPerfectOptionCount = 0),
        )
        assertEquals(
            UsbBitPerfectPolicy.UNAVAILABLE_NO_BIT_PERFECT_MIXER,
            UsbBitPerfectPolicy.unavailableReason(apiLevel = 36, usbDeviceCount = 1, bitPerfectOptionCount = 0),
        )
        assertEquals(
            "",
            UsbBitPerfectPolicy.unavailableReason(apiLevel = 34, usbDeviceCount = 1, bitPerfectOptionCount = 1),
        )
    }

    @Test
    fun `an untransformed lossless source played unchanged at unity gain qualifies`() {
        val verdict = UsbBitPerfectPolicy.transparency(
            source = source(),
            actual = stream(),
            bitPerfectActive = true,
            unityGain = true,
        )

        assertTrue(verdict.transparent)
        assertEquals(UsbBitPerfectPolicy.REASON_QUALIFIED, verdict.reason)
    }

    @Test
    fun `a reduced precision never qualifies even on a bit-perfect mixer`() {
        val verdict = UsbBitPerfectPolicy.transparency(
            source = source(sampleRate = 96_000, encoding = PcmEncoding.PCM_24),
            actual = stream(sampleRate = 96_000),
            bitPerfectActive = true,
            unityGain = true,
        )

        assertFalse(verdict.transparent)
        assertEquals(UsbBitPerfectPolicy.REASON_PRECISION_REDUCED, verdict.reason)
    }

    @Test
    fun `changed rate, layout or gain disqualify the application path`() {
        assertEquals(
            UsbBitPerfectPolicy.REASON_RATE_CHANGED,
            UsbBitPerfectPolicy.transparency(source(sampleRate = 88_200), stream(), true, true).reason,
        )
        assertEquals(
            UsbBitPerfectPolicy.REASON_LAYOUT_CHANGED,
            UsbBitPerfectPolicy.transparency(source(channelCount = 1), stream(), true, true).reason,
        )
        assertEquals(
            UsbBitPerfectPolicy.REASON_GAIN_CHANGED,
            UsbBitPerfectPolicy.transparency(source(), stream(), bitPerfectActive = true, unityGain = false).reason,
        )
    }

    @Test
    fun `server provenance and lossy sources are reported before the format comparison`() {
        assertEquals(
            UsbBitPerfectPolicy.REASON_SOURCE_PROVENANCE_UNKNOWN,
            UsbBitPerfectPolicy.transparency(source(transformed = null), stream(), true, true).reason,
        )
        assertEquals(
            UsbBitPerfectPolicy.REASON_SOURCE_TRANSFORMED,
            UsbBitPerfectPolicy.transparency(source(transformed = true), stream(), true, true).reason,
        )
        assertEquals(
            UsbBitPerfectPolicy.REASON_SOURCE_LOSSY,
            UsbBitPerfectPolicy.transparency(source(lossless = false), stream(), true, true).reason,
        )
        assertEquals(
            UsbBitPerfectPolicy.REASON_SOURCE_PRECISION_UNKNOWN,
            UsbBitPerfectPolicy.transparency(source(encoding = null), stream(), true, true).reason,
        )
    }

    @Test
    fun `a mixed output is never transparent even for an unchanged lossless source`() {
        val verdict = UsbBitPerfectPolicy.transparency(
            source = source(),
            actual = stream(),
            bitPerfectActive = false,
            unityGain = true,
        )

        assertFalse(verdict.transparent)
        assertEquals(UsbBitPerfectPolicy.REASON_NOT_BIT_PERFECT, verdict.reason)
    }

    @Test
    fun `lossless media types are recognised with parameters and case differences`() {
        assertTrue(UsbBitPerfectPolicy.isLosslessMime("audio/flac"))
        assertTrue(UsbBitPerfectPolicy.isLosslessMime("AUDIO/WAV; codecs=1"))
        assertTrue(UsbBitPerfectPolicy.isLosslessMime("audio/raw"))
        assertFalse(UsbBitPerfectPolicy.isLosslessMime("audio/mpeg"))
        assertFalse(UsbBitPerfectPolicy.isLosslessMime("audio/mp4; codecs=mp4a.40.2"))
        assertFalse(UsbBitPerfectPolicy.isLosslessMime(null))
    }
}
