package io.jastreamer.android

/** Linear PCM representations this client can describe, decoupled from `android.media.AudioFormat`. */
internal enum class PcmEncoding(val containerBits: Int, val validBits: Int, val label: String) {
    PCM_8(8, 8, "pcm_8"),
    PCM_16(16, 16, "pcm_16"),
    PCM_24(24, 24, "pcm_24"),
    PCM_32(32, 32, "pcm_32"),
    PCM_FLOAT(32, 32, "pcm_float"),
    ;

    val highResolution: Boolean get() = this == PCM_24 || this == PCM_32 || this == PCM_FLOAT
}

/** An exact audio output stream description: what an `AudioTrack` is created with. */
internal data class PcmStream(
    val encoding: PcmEncoding,
    val sampleRate: Int,
    val channelCount: Int,
    val channelMask: Int,
)

/**
 * One raw entry of `AudioManager.getSupportedMixerAttributes`, kept as the framework reported it so
 * the settings panel can show what a device actually offers.
 *
 * An `AudioFormat` carries either a channel position mask or a channel index mask — AOSP's
 * `nativeAudioConfigBaseToJavaAudioFormat` fills in exactly one of them, depending on the HAL's
 * channel representation — so both are recorded and unusable entries are reported, not dropped.
 */
internal data class MixerEntry(
    val bitPerfect: Boolean,
    val encoding: Int,
    val sampleRate: Int,
    val channelMask: Int,
    val channelIndexMask: Int,
)

/** A reported mixer entry this client can actually open, paired with its mixer behaviour. */
internal data class MixerOption(val stream: PcmStream, val bitPerfect: Boolean)

/** What the decoded media actually carries, as far as the Server and the extractor report it. */
internal data class SourceStream(
    val sampleRate: Int,
    val channelCount: Int,
    val encoding: PcmEncoding?,
    val lossless: Boolean,
    val transformed: Boolean?,
)

internal data class BitPerfectVerdict(val transparent: Boolean, val reason: String)

/**
 * Pure decision rules for the opt-in USB bit-perfect output path. `UsbBitPerfectController`
 * maps Android audio types onto these values so the rules stay unit testable.
 */
internal object UsbBitPerfectPolicy {
    const val MEDIA_MIN_API = 34

    const val UNAVAILABLE_REQUIRES_ANDROID_14 = "requires_android_14"
    const val UNAVAILABLE_NO_USB_DEVICE = "no_usb_device"
    const val UNAVAILABLE_NO_BIT_PERFECT_MIXER = "no_bit_perfect_mixer"
    const val UNAVAILABLE_NO_USABLE_BIT_PERFECT_FORMAT = "no_usable_bit_perfect_format"

    const val REJECT_NONE = ""
    const val REJECT_ENCODING_UNRECOGNIZED = "encoding_unrecognized"
    const val REJECT_CHANNEL_INDEX_MASK = "channel_index_mask"
    const val REJECT_NO_CHANNEL_MASK = "no_channel_mask"
    const val REJECT_INVALID_SAMPLE_RATE = "invalid_sample_rate"

    const val REASON_QUALIFIED = "qualified"
    const val REASON_NOT_BIT_PERFECT = "mixer_not_bit_perfect"
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

    /**
     * The PCM encoding Media3's `DefaultAudioSink` writes to its `AudioTrack`. Integer PCM other
     * than 16-bit is converted to 16-bit, unless high-resolution (float) output is enabled, and a
     * source whose precision is unknown is decoded as 16-bit PCM.
     */
    fun plannedEncoding(source: PcmEncoding?, floatOutput: Boolean): PcmEncoding = when {
        source == null -> PcmEncoding.PCM_16
        floatOutput && source.highResolution -> PcmEncoding.PCM_FLOAT
        else -> PcmEncoding.PCM_16
    }

    /** The exact stream the sink will open for [source], or null when the format is unusable. */
    fun plannedStream(source: SourceStream, channelMask: Int, floatOutput: Boolean): PcmStream? {
        if (source.sampleRate <= 0 || source.channelCount <= 0 || channelMask == 0) return null
        return PcmStream(
            encoding = plannedEncoding(source.encoding, floatOutput),
            sampleRate = source.sampleRate,
            channelCount = source.channelCount,
            channelMask = channelMask,
        )
    }

    /**
     * Why a reported mixer entry cannot back an `AudioTrack` this client opens, or [REJECT_NONE].
     * Media3 always opens a channel *position* mask, so an index-mask entry is recorded and
     * reported instead of silently matched against a positional layout.
     */
    fun rejection(entry: MixerEntry, encodingKnown: Boolean): String = when {
        !encodingKnown -> REJECT_ENCODING_UNRECOGNIZED
        entry.sampleRate <= 0 -> REJECT_INVALID_SAMPLE_RATE
        entry.channelMask != 0 -> REJECT_NONE
        entry.channelIndexMask != 0 -> REJECT_CHANNEL_INDEX_MASK
        else -> REJECT_NO_CHANNEL_MASK
    }

    /** The stream an entry can back, or null when [rejection] explains why it cannot. */
    fun usableStream(entry: MixerEntry, encoding: PcmEncoding?): PcmStream? {
        if (rejection(entry, encoding != null) != REJECT_NONE || encoding == null) return null
        return PcmStream(
            encoding = encoding,
            sampleRate = entry.sampleRate,
            channelCount = Integer.bitCount(entry.channelMask),
            channelMask = entry.channelMask,
        )
    }

    /**
     * The bit-perfect mixer attributes that exactly match [planned]. A near match is never
     * substituted: the mixer only stays bit-perfect while the track format is identical.
     */
    fun select(planned: PcmStream, options: List<MixerOption>): MixerOption? =
        options.firstOrNull { it.bitPerfect && it.stream == planned }

    /**
     * Why the option cannot be used right now, or an empty string when it can. A device that
     * reports bit-perfect entries this client cannot open is distinguished from one that reports
     * none at all, because the two need different answers from the user.
     */
    fun unavailableReason(
        apiLevel: Int,
        usbDeviceCount: Int,
        bitPerfectEntryCount: Int,
        usableBitPerfectCount: Int = bitPerfectEntryCount,
    ): String = when {
        apiLevel < MEDIA_MIN_API -> UNAVAILABLE_REQUIRES_ANDROID_14
        usbDeviceCount <= 0 -> UNAVAILABLE_NO_USB_DEVICE
        bitPerfectEntryCount <= 0 -> UNAVAILABLE_NO_BIT_PERFECT_MIXER
        usableBitPerfectCount <= 0 -> UNAVAILABLE_NO_USABLE_BIT_PERFECT_FORMAT
        else -> ""
    }

    /**
     * Whether the application path left every sample untouched. This mirrors the Windows exclusive
     * rules: an explicitly untransformed lossless source, unchanged rate, layout and precision,
     * unity gain, and a mixer that is actually bit-perfect. It says nothing about what the DAC does.
     */
    fun transparency(
        source: SourceStream,
        actual: PcmStream,
        bitPerfectActive: Boolean,
        unityGain: Boolean,
    ): BitPerfectVerdict {
        val reason = when {
            !bitPerfectActive -> REASON_NOT_BIT_PERFECT
            source.transformed == null -> REASON_SOURCE_PROVENANCE_UNKNOWN
            source.transformed -> REASON_SOURCE_TRANSFORMED
            !source.lossless -> REASON_SOURCE_LOSSY
            !unityGain -> REASON_GAIN_CHANGED
            source.sampleRate != actual.sampleRate -> REASON_RATE_CHANGED
            source.channelCount != actual.channelCount -> REASON_LAYOUT_CHANGED
            source.encoding == null -> REASON_SOURCE_PRECISION_UNKNOWN
            source.encoding.validBits > actual.encoding.validBits -> REASON_PRECISION_REDUCED
            source.encoding != actual.encoding -> REASON_ENCODING_CHANGED
            else -> REASON_QUALIFIED
        }
        return BitPerfectVerdict(reason == REASON_QUALIFIED, reason)
    }
}
