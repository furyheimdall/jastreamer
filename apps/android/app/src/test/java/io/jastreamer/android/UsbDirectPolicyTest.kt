package io.jastreamer.android

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class UsbDirectPolicyTest {
    private fun format(
        alt: Int,
        bits: Int,
        subslotBytes: Int,
        channels: Int = 2,
        rates: List<Int> = listOf(44_100, 48_000, 88_200, 96_000),
    ) = UsbDirectFormat(
        alt = alt,
        bits = bits,
        subslotBytes = subslotBytes,
        channels = channels,
        sync = "async",
        feedback = true,
        maxPacketBytes = 1024,
        interval = 1,
        rates = rates,
    )

    private fun source(
        encoding: PcmEncoding? = PcmEncoding.PCM_24,
        sampleRate: Int = 44_100,
        channels: Int = 2,
        lossless: Boolean = true,
        transformed: Boolean? = false,
    ) = SourceStream(
        sampleRate = sampleRate,
        channelCount = channels,
        encoding = encoding,
        lossless = lossless,
        transformed = transformed,
    )

    private fun plan(decision: UsbDirectDecision): UsbDirectPlan {
        assertTrue("expected a direct plan but got $decision", decision is UsbDirectDecision.Direct)
        return (decision as UsbDirectDecision.Direct).plan
    }

    private fun rejection(decision: UsbDirectDecision): UsbDirectRejection {
        assertTrue("expected a rejection but got $decision", decision is UsbDirectDecision.Unsupported)
        return (decision as UsbDirectDecision.Unsupported).rejection
    }

    @Test
    fun `exact alternate setting wins over a wider container`() {
        val decision = UsbDirectPolicy.plan(
            source(),
            listOf(format(1, 16, 2), format(2, 32, 4), format(3, 24, 3)),
        )

        val chosen = plan(decision)
        assertEquals(3, chosen.alt)
        assertEquals(24, chosen.validBits)
        assertEquals(24, chosen.containerBits)
        assertFalse(chosen.duplicateMono)
    }

    @Test
    fun `a 24-bit source widens into a 16 and 32 bit only device`() {
        // The dongle from the field report offers nothing between 16 and 32 bit.
        val decision = UsbDirectPolicy.plan(source(), listOf(format(1, 16, 2), format(2, 32, 4)))

        val chosen = plan(decision)
        assertEquals(2, chosen.alt)
        assertEquals(24, chosen.validBits)
        assertEquals(32, chosen.containerBits)
        assertEquals(3, chosen.sourceSampleBytes)
    }

    @Test
    fun `a 16-bit source widens into a 24 or 32 bit device and stays transparent`() {
        val into24 = plan(UsbDirectPolicy.plan(source(encoding = PcmEncoding.PCM_16), listOf(format(1, 24, 3))))
        assertEquals(16, into24.validBits)
        assertEquals(24, into24.containerBits)

        val into32 = plan(UsbDirectPolicy.plan(source(encoding = PcmEncoding.PCM_16), listOf(format(1, 32, 4))))
        assertEquals(16, into32.validBits)
        assertEquals(32, into32.containerBits)

        val verdict = UsbDirectPolicy.transparency(
            source = source(encoding = PcmEncoding.PCM_16),
            actual = actual(validBits = 16, containerBits = 32),
            unityGain = true,
        )
        assertTrue(verdict.transparent)
        assertEquals(UsbDirectPolicy.REASON_QUALIFIED, verdict.reason)
    }

    @Test
    fun `a 16-bit only device refuses a 24-bit source rather than dropping bits`() {
        val failure = rejection(UsbDirectPolicy.plan(source(), listOf(format(1, 16, 2))))
        assertEquals(UsbDirectPolicy.FAILURE_PRECISION, failure.code)
    }

    @Test
    fun `an unsupported rate is reported separately from an unsupported precision`() {
        val failure = rejection(
            UsbDirectPolicy.plan(
                source(sampleRate = 192_000),
                listOf(format(1, 24, 3), format(2, 32, 4)),
            ),
        )
        assertEquals(UsbDirectPolicy.FAILURE_RATE, failure.code)
    }

    @Test
    fun `a device that cannot carry the channel layout is reported as a channel failure`() {
        val failure = rejection(
            UsbDirectPolicy.plan(source(channels = 6), listOf(format(1, 24, 3), format(2, 32, 4))),
        )
        assertEquals(UsbDirectPolicy.FAILURE_CHANNELS, failure.code)
    }

    @Test
    fun `a mono source uses a mono setting when the device has one`() {
        val chosen = plan(
            UsbDirectPolicy.plan(
                source(channels = 1),
                listOf(format(1, 24, 3, channels = 2), format(2, 24, 3, channels = 1)),
            ),
        )
        assertEquals(2, chosen.alt)
        assertEquals(1, chosen.deviceChannels)
        assertFalse(chosen.duplicateMono)
    }

    @Test
    fun `a mono source is duplicated onto a stereo only device and is not transparent`() {
        val chosen = plan(UsbDirectPolicy.plan(source(channels = 1), listOf(format(1, 24, 3, channels = 2))))
        assertEquals(2, chosen.deviceChannels)
        assertEquals(1, chosen.sourceChannels)
        assertTrue(chosen.duplicateMono)

        val verdict = UsbDirectPolicy.transparency(
            source = source(channels = 1),
            actual = actual(channels = 2),
            unityGain = true,
        )
        assertFalse(verdict.transparent)
        assertEquals(UsbDirectPolicy.REASON_LAYOUT_CHANGED, verdict.reason)
    }

    @Test
    fun `float decoder output never reaches an integer device`() {
        val failure = rejection(
            UsbDirectPolicy.plan(source(encoding = PcmEncoding.PCM_FLOAT), listOf(format(1, 32, 4))),
        )
        assertEquals(UsbDirectPolicy.FAILURE_ENCODING, failure.code)
    }

    @Test
    fun `an unknown decoded format is refused`() {
        assertEquals(
            UsbDirectPolicy.FAILURE_FORMAT_UNKNOWN,
            rejection(UsbDirectPolicy.plan(source(encoding = null), listOf(format(1, 32, 4)))).code,
        )
        assertEquals(
            UsbDirectPolicy.FAILURE_FORMAT_UNKNOWN,
            rejection(UsbDirectPolicy.plan(source(sampleRate = 0), listOf(format(1, 32, 4)))).code,
        )
    }

    @Test
    fun `a device that does not enumerate its rates is tried at the requested rate`() {
        val chosen = plan(
            UsbDirectPolicy.plan(source(sampleRate = 352_800), listOf(format(1, 32, 4, rates = emptyList()))),
        )
        assertEquals(352_800, chosen.sampleRate)
    }

    @Test
    fun `transparency requires a running USB stream, provenance, unity gain and no conversion`() {
        val lossless = source()

        assertEquals(
            UsbDirectPolicy.REASON_NOT_USB_DIRECT,
            UsbDirectPolicy.transparency(lossless, null, unityGain = true).reason,
        )
        assertEquals(
            UsbDirectPolicy.REASON_NOT_USB_DIRECT,
            UsbDirectPolicy.transparency(
                lossless,
                actual(engine = UsbDirectPolicy.ENGINE_SYSTEM),
                unityGain = true,
            ).reason,
        )
        assertEquals(
            UsbDirectPolicy.REASON_SOURCE_PROVENANCE_UNKNOWN,
            UsbDirectPolicy.transparency(source(transformed = null), actual(), unityGain = true).reason,
        )
        assertEquals(
            UsbDirectPolicy.REASON_SOURCE_TRANSFORMED,
            UsbDirectPolicy.transparency(source(transformed = true), actual(), unityGain = true).reason,
        )
        assertEquals(
            UsbDirectPolicy.REASON_SOURCE_LOSSY,
            UsbDirectPolicy.transparency(source(lossless = false), actual(), unityGain = true).reason,
        )
        assertEquals(
            UsbDirectPolicy.REASON_GAIN_CHANGED,
            UsbDirectPolicy.transparency(lossless, actual(), unityGain = false).reason,
        )
        assertEquals(
            UsbDirectPolicy.REASON_RATE_CHANGED,
            UsbDirectPolicy.transparency(lossless, actual(sampleRate = 48_000), unityGain = true).reason,
        )
        assertEquals(
            UsbDirectPolicy.REASON_PRECISION_REDUCED,
            UsbDirectPolicy.transparency(lossless, actual(validBits = 16, containerBits = 16), unityGain = true).reason,
        )
        assertEquals(
            UsbDirectPolicy.REASON_SOURCE_PRECISION_UNKNOWN,
            UsbDirectPolicy.transparency(source(encoding = null), actual(), unityGain = true).reason,
        )

        val qualified = UsbDirectPolicy.transparency(lossless, actual(containerBits = 32), unityGain = true)
        assertTrue(qualified.transparent)
        assertEquals(UsbDirectPolicy.REASON_QUALIFIED, qualified.reason)
    }

    @Test
    fun `the unsupported format choice defaults to skipping the track`() {
        assertEquals(UsbDirectPolicy.UNSUPPORTED_SKIP, UsbDirectPolicy.unsupportedFormatChoice(null))
        assertEquals(UsbDirectPolicy.UNSUPPORTED_SKIP, UsbDirectPolicy.unsupportedFormatChoice(""))
        assertEquals(UsbDirectPolicy.UNSUPPORTED_SKIP, UsbDirectPolicy.unsupportedFormatChoice("nonsense"))
        assertEquals(
            UsbDirectPolicy.UNSUPPORTED_SYSTEM_OUTPUT,
            UsbDirectPolicy.unsupportedFormatChoice("system_output"),
        )
    }

    @Test
    fun `the decoder is asked for high resolution integer PCM only when the source carries it`() {
        assertEquals(PcmEncoding.PCM_24, UsbDirectPolicy.decoderPcmEncoding(PcmEncoding.PCM_24))
        assertEquals(PcmEncoding.PCM_32, UsbDirectPolicy.decoderPcmEncoding(PcmEncoding.PCM_32))
        assertEquals(null, UsbDirectPolicy.decoderPcmEncoding(PcmEncoding.PCM_16))
        assertEquals(null, UsbDirectPolicy.decoderPcmEncoding(PcmEncoding.PCM_FLOAT))
        assertEquals(null, UsbDirectPolicy.decoderPcmEncoding(null))
    }

    @Test
    fun `only lossless container types count as an untouched source`() {
        assertTrue(UsbDirectPolicy.isLosslessMime("audio/flac"))
        assertTrue(UsbDirectPolicy.isLosslessMime("AUDIO/WAV; charset=binary"))
        assertFalse(UsbDirectPolicy.isLosslessMime("audio/mpeg"))
        assertFalse(UsbDirectPolicy.isLosslessMime(null))
    }

    @Test
    fun `unplugging a USB audio device releases it and turns the option off`() {
        val playing = UsbDirectPolicy.detachOutcome(
            usbAudioDevice = true,
            heldByEngine = true,
            settingEnabled = true,
            playingThroughUsb = true,
        )
        assertTrue(playing.handled)
        assertTrue(playing.releaseDevice)
        assertTrue(playing.disableSetting)
        assertTrue(playing.failPlayback)

        // Stopped is the case the field report hit: nothing is playing and the device is not even
        // held any more, but the option has to go off or a replug can never work again.
        val stopped = UsbDirectPolicy.detachOutcome(
            usbAudioDevice = true,
            heldByEngine = false,
            settingEnabled = true,
            playingThroughUsb = false,
        )
        assertTrue(stopped.handled)
        assertTrue(stopped.releaseDevice)
        assertTrue(stopped.disableSetting)
        assertFalse(stopped.failPlayback)
    }

    @Test
    fun `a track playing through the Android output survives the DAC being unplugged`() {
        val outcome = UsbDirectPolicy.detachOutcome(
            usbAudioDevice = true,
            heldByEngine = false,
            settingEnabled = true,
            playingThroughUsb = false,
        )
        assertFalse(outcome.failPlayback)
    }

    @Test
    fun `unplugging something that is not the audio device changes nothing`() {
        val outcome = UsbDirectPolicy.detachOutcome(
            usbAudioDevice = false,
            heldByEngine = false,
            settingEnabled = true,
            playingThroughUsb = true,
        )
        assertFalse(outcome.handled)
        assertFalse(outcome.releaseDevice)
        assertFalse(outcome.disableSetting)
        assertFalse(outcome.failPlayback)
    }

    @Test
    fun `an already off option is left alone while the device is still released`() {
        val outcome = UsbDirectPolicy.detachOutcome(
            usbAudioDevice = true,
            heldByEngine = true,
            settingEnabled = false,
            playingThroughUsb = false,
        )
        assertTrue(outcome.handled)
        assertTrue(outcome.releaseDevice)
        assertFalse(outcome.disableSetting)
    }

    @Test
    fun `only a format problem is handed to the unsupported format choice`() {
        listOf(
            UsbDirectPolicy.FAILURE_RATE,
            UsbDirectPolicy.FAILURE_CHANNELS,
            UsbDirectPolicy.FAILURE_PRECISION,
            UsbDirectPolicy.FAILURE_ENCODING,
            UsbDirectPolicy.FAILURE_FORMAT_UNKNOWN,
        ).forEach { assertTrue(it, UsbDirectPolicy.isFormatRejection(it)) }

        listOf(
            UsbDirectPolicy.FAILURE_PERMISSION,
            UsbDirectPolicy.FAILURE_NO_DEVICE,
            UsbDirectPolicy.FAILURE_DRIVER,
            UsbDirectPolicy.FAILURE_OPEN,
            UsbDirectPolicy.FAILURE_START,
            UsbDirectPolicy.FAILURE_DEVICE_LOST,
            UsbDirectPolicy.FAILURE_DEVICE_DETACHED,
        ).forEach { assertFalse(it, UsbDirectPolicy.isFormatRejection(it)) }
    }

    @Test
    fun `the allow USB access action appears only when the option is on and permission is missing`() {
        assertTrue(
            UsbDirectPolicy.needsPermissionPrompt(true, UsbDirectPolicy.UNAVAILABLE_PERMISSION),
        )
        assertFalse(
            UsbDirectPolicy.needsPermissionPrompt(false, UsbDirectPolicy.UNAVAILABLE_PERMISSION),
        )
        assertFalse(UsbDirectPolicy.needsPermissionPrompt(true, UsbDirectPolicy.UNAVAILABLE_NONE))
        assertFalse(
            UsbDirectPolicy.needsPermissionPrompt(true, UsbDirectPolicy.UNAVAILABLE_NO_USB_DEVICE),
        )
    }

    private fun actual(
        engine: String = UsbDirectPolicy.ENGINE_USB_DIRECT,
        sampleRate: Int = 44_100,
        channels: Int = 2,
        validBits: Int = 24,
        containerBits: Int = 24,
    ) = UsbActualStream(
        engine = engine,
        deviceName = "USB-Audio - Protocol Max",
        sampleRate = sampleRate,
        channels = channels,
        validBits = validBits,
        containerBits = containerBits,
        encoding = "pcm_$validBits",
    )
}
