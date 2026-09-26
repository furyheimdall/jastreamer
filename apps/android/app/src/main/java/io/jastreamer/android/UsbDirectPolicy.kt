package io.jastreamer.android

/** A PCM representation a decoder can hand to the USB direct sink. */
internal enum class PcmEncoding(val containerBits: Int, val validBits: Int, val label: String) {
    PCM_8(8, 8, "pcm_8"),
    PCM_16(16, 16, "pcm_16"),
    PCM_24(24, 24, "pcm_24"),
    PCM_32(32, 32, "pcm_32"),
    PCM_FLOAT(32, 32, "pcm_float"),
    ;

    val bytesPerSample: Int get() = containerBits / 8

    /** Only signed little-endian integer PCM can be widened into a USB subslot unchanged. */
    val integer: Boolean get() = this == PCM_16 || this == PCM_24 || this == PCM_32

    val highResolution: Boolean get() = this == PCM_24 || this == PCM_32 || this == PCM_FLOAT
}

/** What the decoded media actually carries, as far as the Server and the decoder report it. */
internal data class SourceStream(
    val sampleRate: Int,
    val channelCount: Int,
    val encoding: PcmEncoding?,
    val lossless: Boolean,
    val transformed: Boolean?,
)

/** The stream the sink will open on the DAC, and what it costs in transparency. */
internal data class UsbDirectPlan(
    val alt: Int,
    val sampleRate: Int,
    val deviceChannels: Int,
    val sourceChannels: Int,
    /** Bits that survive end to end: the source's own precision. */
    val validBits: Int,
    /** The alternate setting's declared resolution. */
    val deviceBits: Int,
    /** Subslot size in bits; wider than [validBits] means the sample is zero-padded. */
    val containerBits: Int,
    val sourceSampleBytes: Int,
    val duplicateMono: Boolean,
)

/** Why the attached device cannot take this track without changing the samples. */
internal data class UsbDirectRejection(val code: String, val detail: String)

internal sealed interface UsbDirectDecision {
    data class Direct(val plan: UsbDirectPlan) : UsbDirectDecision
    data class Unsupported(val rejection: UsbDirectRejection) : UsbDirectDecision
}

/** The path that is actually carrying audio right now, as the panel reports it. */
internal data class UsbActualStream(
    val engine: String,
    val deviceName: String,
    val sampleRate: Int,
    val channels: Int,
    val validBits: Int,
    val containerBits: Int,
    val encoding: String,
)

internal data class BitPerfectVerdict(val transparent: Boolean, val reason: String)

/**
 * Pure decision rules for the opt-in direct USB output. [UsbDirectEngine] maps Android and USB
 * descriptor types onto these values so every rule stays unit testable.
 *
 * The one conversion this path performs is widening: a sample is left-justified into a wider
 * subslot and the unused low bits are zero, which leaves its value untouched. Everything else -
 * resampling, bit reduction, dithering, downmixing and float-to-integer conversion - is refused
 * here and handed to the user's `unsupported_format` choice instead.
 */
internal object UsbDirectPolicy {
    /** What to do with a track this DAC cannot take unchanged. */
    const val UNSUPPORTED_SKIP = "skip"
    const val UNSUPPORTED_SYSTEM_OUTPUT = "system_output"

    const val ENGINE_USB_DIRECT = "usb_direct"
    const val ENGINE_SYSTEM = "system"

    const val UNAVAILABLE_NONE = ""
    const val UNAVAILABLE_DRIVER = "driver_unavailable"
    const val UNAVAILABLE_NO_USB_DEVICE = "no_usb_device"
    const val UNAVAILABLE_PERMISSION = "permission_required"

    const val FAILURE_NO_DEVICE = "usb_direct_no_device"
    const val FAILURE_PERMISSION = "usb_direct_permission_required"
    const val FAILURE_DRIVER = "usb_direct_driver_unavailable"
    const val FAILURE_DEVICE_LOST = "usb_direct_device_lost"
    const val FAILURE_OPEN = "usb_direct_open_failed"
    const val FAILURE_START = "usb_direct_start_failed"
    const val FAILURE_RATE = "usb_direct_unsupported_rate"
    const val FAILURE_CHANNELS = "usb_direct_unsupported_channels"
    const val FAILURE_PRECISION = "usb_direct_unsupported_precision"
    const val FAILURE_ENCODING = "usb_direct_unsupported_encoding"
    const val FAILURE_FORMAT_UNKNOWN = "usb_direct_format_unknown"

    const val REASON_QUALIFIED = "qualified"
    const val REASON_NOT_USB_DIRECT = "not_usb_direct"
    const val REASON_SOURCE_PROVENANCE_UNKNOWN = "source_provenance_unknown"
    const val REASON_SOURCE_TRANSFORMED = "source_transformed"
    const val REASON_SOURCE_LOSSY = "source_lossy"
    const val REASON_SOURCE_PRECISION_UNKNOWN = "source_precision_unknown"
    const val REASON_GAIN_CHANGED = "gain_changed"
    const val REASON_RATE_CHANGED = "rate_changed"
    const val REASON_LAYOUT_CHANGED = "layout_changed"
    const val REASON_PRECISION_REDUCED = "precision_reduced"
    const val REASON_ENCODING_CHANGED = "encoding_changed"

    private val LOSSLESS_MIME_TYPES = setOf(
        "audio/flac",
        "audio/x-flac",
        "audio/raw",
        "audio/wav",
        "audio/x-wav",
        "audio/wave",
        "audio/vnd.wave",
    )

    /** The Server and the extractor both describe media by MIME type; only these keep every sample. */
    fun isLosslessMime(mime: String?): Boolean {
        val value = mime?.substringBefore(';')?.trim()?.lowercase() ?: return false
        return value in LOSSLESS_MIME_TYPES
    }

    /** Normalises the user's stored choice; anything unknown behaves like the default. */
    fun unsupportedFormatChoice(value: String?): String =
        if (value == UNSUPPORTED_SYSTEM_OUTPUT) UNSUPPORTED_SYSTEM_OUTPUT else UNSUPPORTED_SKIP

    /**
     * The integer precision the decoder should be asked for so the source reaches the sink at its
     * own resolution, or null to leave Media3's 16-bit default in place. Media3 only ever requests
     * float, which no integer DAC can take unchanged.
     */
    fun decoderPcmEncoding(source: PcmEncoding?): PcmEncoding? = when (source) {
        PcmEncoding.PCM_24 -> PcmEncoding.PCM_24
        PcmEncoding.PCM_32 -> PcmEncoding.PCM_32
        else -> null
    }

    /**
     * Picks the alternate setting that carries [source] unchanged, or explains why none can.
     *
     * A setting qualifies when its declared resolution is at least the source's precision - the
     * feeder can widen, never narrow - and its subslot holds that resolution. The least padding
     * wins, so an exact match beats widening. A mono source is only duplicated onto a stereo
     * device when the device has no mono setting at all.
     */
    fun plan(source: SourceStream, formats: List<UsbDirectFormat>): UsbDirectDecision {
        if (source.sampleRate <= 0 || source.channelCount <= 0) {
            return unsupported(FAILURE_FORMAT_UNKNOWN, "The decoded audio format is unknown.")
        }
        val encoding = source.encoding
            ?: return unsupported(FAILURE_FORMAT_UNKNOWN, "The decoded audio format is unknown.")
        if (!encoding.integer) {
            return unsupported(
                FAILURE_ENCODING,
                "The decoder produced ${encoding.label}, which cannot reach an integer DAC unchanged.",
            )
        }

        select(source, encoding, formats, source.channelCount, duplicateMono = false)?.let {
            return UsbDirectDecision.Direct(it)
        }
        if (source.channelCount == 1) {
            select(source, encoding, formats, 2, duplicateMono = true)?.let {
                return UsbDirectDecision.Direct(it)
            }
        }

        val layoutMatches = formats.filter { usableChannels(it, source.channelCount) }
        if (layoutMatches.isEmpty()) {
            return unsupported(
                FAILURE_CHANNELS,
                "This USB device has no ${source.channelCount}-channel output.",
            )
        }
        val depthMatches = layoutMatches.filter { holds(it, encoding.validBits) }
        if (depthMatches.isEmpty()) {
            return unsupported(
                FAILURE_PRECISION,
                "This USB device cannot take ${encoding.validBits}-bit audio without dropping bits.",
            )
        }
        return unsupported(
            FAILURE_RATE,
            "This USB device cannot run ${source.sampleRate} Hz at ${encoding.validBits}-bit.",
        )
    }

    /**
     * Whether the application path left every sample untouched. This mirrors the Windows exclusive
     * rules: an explicitly untransformed lossless source, unchanged rate, layout and precision,
     * unity gain, and a direct USB stream actually running. It says nothing about what the DAC does
     * with the samples afterwards.
     */
    fun transparency(
        source: SourceStream?,
        actual: UsbActualStream?,
        unityGain: Boolean,
    ): BitPerfectVerdict {
        val reason = when {
            actual == null || actual.engine != ENGINE_USB_DIRECT -> REASON_NOT_USB_DIRECT
            source == null || source.transformed == null -> REASON_SOURCE_PROVENANCE_UNKNOWN
            source.transformed -> REASON_SOURCE_TRANSFORMED
            !source.lossless -> REASON_SOURCE_LOSSY
            !unityGain -> REASON_GAIN_CHANGED
            source.sampleRate != actual.sampleRate -> REASON_RATE_CHANGED
            source.channelCount != actual.channels -> REASON_LAYOUT_CHANGED
            source.encoding == null -> REASON_SOURCE_PRECISION_UNKNOWN
            !source.encoding.integer -> REASON_ENCODING_CHANGED
            source.encoding.validBits > actual.validBits -> REASON_PRECISION_REDUCED
            else -> REASON_QUALIFIED
        }
        return BitPerfectVerdict(reason == REASON_QUALIFIED, reason)
    }

    private fun select(
        source: SourceStream,
        encoding: PcmEncoding,
        formats: List<UsbDirectFormat>,
        deviceChannels: Int,
        duplicateMono: Boolean,
    ): UsbDirectPlan? {
        val candidate = formats
            .filter { it.channels == deviceChannels && holds(it, encoding.validBits) && runs(it, source.sampleRate) }
            // Least padding first: the narrowest subslot, then the smallest resolution inside it.
            .minWithOrNull(compareBy({ it.subslotBytes }, { it.bits }))
            ?: return null
        return UsbDirectPlan(
            alt = candidate.alt,
            sampleRate = source.sampleRate,
            deviceChannels = deviceChannels,
            sourceChannels = source.channelCount,
            validBits = encoding.validBits,
            deviceBits = candidate.bits,
            containerBits = candidate.subslotBytes * 8,
            sourceSampleBytes = encoding.bytesPerSample,
            duplicateMono = duplicateMono,
        )
    }

    private fun usableChannels(format: UsbDirectFormat, sourceChannels: Int): Boolean =
        format.channels == sourceChannels || (sourceChannels == 1 && format.channels == 2)

    private fun holds(format: UsbDirectFormat, validBits: Int): Boolean =
        format.bits >= validBits && format.subslotBytes * 8 >= format.bits

    /** An empty rate list means the device did not enumerate its rates; it decides at start time. */
    private fun runs(format: UsbDirectFormat, sampleRate: Int): Boolean =
        format.rates.isEmpty() || format.rates.contains(sampleRate)

    private fun unsupported(code: String, detail: String): UsbDirectDecision.Unsupported =
        UsbDirectDecision.Unsupported(UsbDirectRejection(code, detail))
}
