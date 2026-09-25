package io.jastreamer.android

import android.content.Context
import android.content.SharedPreferences
import android.media.AudioAttributes
import android.media.AudioDeviceCallback
import android.media.AudioDeviceInfo
import android.media.AudioFormat
import android.media.AudioManager
import android.media.AudioMixerAttributes
import android.os.Build
import android.os.Handler
import android.os.Looper
import androidx.annotation.MainThread
import androidx.annotation.RequiresApi

/** An eligible USB output, as shown in the local audio settings panel. */
internal data class UsbAudioDevice(val id: String, val name: String)

/** What the bit-perfect path currently is, from the device and the framework rather than the request. */
internal data class UsbBitPerfectStatus(
    val supported: Boolean,
    val available: Boolean,
    val reason: String,
    val devices: List<UsbAudioDevice>,
)

/** The applied output, reported after an `AudioTrack` exists. */
internal data class UsbBitPerfectActive(
    val deviceId: String,
    val deviceName: String,
    val bitPerfect: Boolean,
    val stream: PcmStream,
)

internal class UsbBitPerfectException(val code: String, message: String) : Exception(message)

/**
 * The Android side of the opt-in USB bit-perfect output: it resolves eligible USB devices, matches
 * their supported mixer attributes against the stream Media3 will open, and applies or clears the
 * preferred mixer attributes. Every decision rule lives in [UsbBitPerfectPolicy].
 */
@MainThread
internal class UsbBitPerfectController(
    context: Context,
    private val onDevicesChanged: () -> Unit,
) {
    private val application = context.applicationContext
    private val audioManager = application.getSystemService(AudioManager::class.java)
    private val preferences: SharedPreferences =
        application.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE)
    private val mediaAttributes = AudioAttributes.Builder()
        .setUsage(AudioAttributes.USAGE_MEDIA)
        .setContentType(AudioAttributes.CONTENT_TYPE_MUSIC)
        .build()
    private var appliedDevice: AudioDeviceInfo? = null
    private var appliedStream: PcmStream? = null

    /** Incremented whenever the framework preference changes, so a stale `AudioTrack` is detected. */
    var appliedGeneration: Long = 0L
        private set

    private val deviceCallback = object : AudioDeviceCallback() {
        override fun onAudioDevicesAdded(addedDevices: Array<out AudioDeviceInfo>?) {
            onDevicesChanged()
        }

        override fun onAudioDevicesRemoved(removedDevices: Array<out AudioDeviceInfo>?) {
            val applied = appliedDevice
            if (applied != null && removedDevices?.any { it.id == applied.id } == true) clear()
            onDevicesChanged()
        }
    }

    init {
        audioManager?.registerAudioDeviceCallback(deviceCallback, Handler(Looper.getMainLooper()))
    }

    /** The opt-in setting, stored per app rather than per Server, and off by default. */
    val enabled: Boolean get() = preferences.getBoolean(USB_BIT_PERFECT, false)

    val supported: Boolean get() = Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE && audioManager != null

    /** The bit-perfect mixer attributes stay applied only while this returns true. */
    val applied: Boolean get() = appliedStream != null

    fun setEnabled(value: Boolean) {
        if (enabled == value) return
        preferences.edit().putBoolean(USB_BIT_PERFECT, value).apply()
        if (!value) clear()
    }

    fun status(): UsbBitPerfectStatus {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.UPSIDE_DOWN_CAKE || audioManager == null) {
            return UsbBitPerfectStatus(
                supported = false,
                available = false,
                reason = UsbBitPerfectPolicy.UNAVAILABLE_REQUIRES_ANDROID_14,
                devices = emptyList(),
            )
        }
        val devices = usbDevices()
        val bitPerfectOptions = devices.sumOf { device -> options(device).count(MixerOption::bitPerfect) }
        val reason = UsbBitPerfectPolicy.unavailableReason(
            apiLevel = Build.VERSION.SDK_INT,
            usbDeviceCount = devices.size,
            bitPerfectOptionCount = bitPerfectOptions,
        )
        return UsbBitPerfectStatus(
            supported = true,
            available = reason.isEmpty(),
            reason = reason,
            devices = devices.map { UsbAudioDevice(it.id.toString(), deviceName(it)) },
        )
    }

    /**
     * Applies the bit-perfect mixer attributes for [planned] and returns the USB device that will
     * carry it. Nothing falls back to a mixed stream: an unsupported format or a missing device
     * fails instead.
     */
    fun apply(planned: PcmStream): AudioDeviceInfo {
        val manager = audioManager
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.UPSIDE_DOWN_CAKE || manager == null) {
            throw UsbBitPerfectException(
                "bit_perfect_unsupported",
                "USB bit-perfect output needs Android 14 or newer.",
            )
        }
        val device = usbDevices().firstOrNull()
            ?: throw UsbBitPerfectException(
                "bit_perfect_no_device",
                "Connect a USB audio device to use bit-perfect output.",
            )
        val selected = UsbBitPerfectPolicy.select(planned, options(device))
            ?: throw UsbBitPerfectException(
                "bit_perfect_format_unsupported",
                "${deviceName(device)} does not accept ${describe(planned)} as a bit-perfect stream.",
            )
        if (appliedDevice?.id == device.id && appliedStream == selected.stream) return device
        val mixerAttributes = AudioMixerAttributes.Builder(audioFormat(selected.stream))
            .setMixerBehavior(AudioMixerAttributes.MIXER_BEHAVIOR_BIT_PERFECT)
            .build()
        val accepted = try {
            manager.setPreferredMixerAttributes(mediaAttributes, device, mixerAttributes)
        } catch (error: RuntimeException) {
            throw UsbBitPerfectException(
                "bit_perfect_rejected",
                "${deviceName(device)} rejected the bit-perfect request: ${error.message.orEmpty()}",
            )
        }
        if (!accepted) {
            throw UsbBitPerfectException(
                "bit_perfect_rejected",
                "${deviceName(device)} rejected ${describe(planned)} as a bit-perfect stream.",
            )
        }
        appliedDevice = device
        appliedStream = selected.stream
        appliedGeneration++
        return device
    }

    /**
     * The mixer attributes the framework reports for the applied device, so the panel describes the
     * active path instead of the request.
     */
    fun activeBitPerfect(): Boolean {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.UPSIDE_DOWN_CAKE) return false
        val manager = audioManager ?: return false
        val device = appliedDevice ?: return false
        val stream = appliedStream ?: return false
        if (usbDevices().none { it.id == device.id }) return false
        val current = try {
            manager.getPreferredMixerAttributes(mediaAttributes, device)
        } catch (_: RuntimeException) {
            null
        } ?: return false
        return current.mixerBehavior == AudioMixerAttributes.MIXER_BEHAVIOR_BIT_PERFECT &&
            mixerStream(current) == stream
    }

    fun active(stream: PcmStream): UsbBitPerfectActive? {
        val device = appliedDevice ?: return null
        return UsbBitPerfectActive(
            deviceId = device.id.toString(),
            deviceName = deviceName(device),
            bitPerfect = activeBitPerfect() && appliedStream == stream,
            stream = stream,
        )
    }

    fun deviceLabel(): String = appliedDevice?.let(::deviceName)
        ?: usbDevices().firstOrNull()?.let(::deviceName)
        ?: ""

    /** Releases the preferred mixer attributes; playback returns to the normal mixed output. */
    fun clear() {
        val manager = audioManager
        val device = appliedDevice
        appliedDevice = null
        appliedStream = null
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.UPSIDE_DOWN_CAKE || manager == null || device == null) return
        appliedGeneration++
        try {
            manager.clearPreferredMixerAttributes(mediaAttributes, device)
        } catch (_: RuntimeException) {
            // A detached device already dropped its preference; nothing else holds it.
        }
    }

    fun release() {
        clear()
        audioManager?.unregisterAudioDeviceCallback(deviceCallback)
    }

    fun channelMask(channelCount: Int): Int = when (channelCount) {
        1 -> AudioFormat.CHANNEL_OUT_MONO
        2 -> AudioFormat.CHANNEL_OUT_STEREO
        3 -> AudioFormat.CHANNEL_OUT_STEREO or AudioFormat.CHANNEL_OUT_FRONT_CENTER
        4 -> AudioFormat.CHANNEL_OUT_QUAD
        5 -> AudioFormat.CHANNEL_OUT_QUAD or AudioFormat.CHANNEL_OUT_FRONT_CENTER
        6 -> AudioFormat.CHANNEL_OUT_5POINT1
        7 -> AudioFormat.CHANNEL_OUT_5POINT1 or AudioFormat.CHANNEL_OUT_BACK_CENTER
        8 -> AudioFormat.CHANNEL_OUT_7POINT1_SURROUND
        else -> 0
    }

    private fun usbDevices(): List<AudioDeviceInfo> {
        val manager = audioManager ?: return emptyList()
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.UPSIDE_DOWN_CAKE) return emptyList()
        return manager.getDevices(AudioManager.GET_DEVICES_OUTPUTS)
            .filter { it.type == AudioDeviceInfo.TYPE_USB_DEVICE || it.type == AudioDeviceInfo.TYPE_USB_HEADSET }
    }

    @RequiresApi(Build.VERSION_CODES.UPSIDE_DOWN_CAKE)
    private fun options(device: AudioDeviceInfo): List<MixerOption> {
        val manager = audioManager ?: return emptyList()
        val supportedAttributes = try {
            manager.getSupportedMixerAttributes(device)
        } catch (_: RuntimeException) {
            return emptyList()
        }
        return supportedAttributes.mapNotNull { attributes ->
            val stream = mixerStream(attributes) ?: return@mapNotNull null
            MixerOption(stream, attributes.mixerBehavior == AudioMixerAttributes.MIXER_BEHAVIOR_BIT_PERFECT)
        }
    }

    @RequiresApi(Build.VERSION_CODES.UPSIDE_DOWN_CAKE)
    private fun mixerStream(attributes: AudioMixerAttributes): PcmStream? {
        val format = attributes.format
        val encoding = encoding(format.encoding) ?: return null
        val mask = format.channelMask
        if (format.sampleRate <= 0 || mask == 0) return null
        return PcmStream(encoding, format.sampleRate, Integer.bitCount(mask), mask)
    }

    private fun audioFormat(stream: PcmStream): AudioFormat = AudioFormat.Builder()
        .setEncoding(androidEncoding(stream.encoding))
        .setChannelMask(stream.channelMask)
        .setSampleRate(stream.sampleRate)
        .build()

    private fun deviceName(device: AudioDeviceInfo): String {
        val product = device.productName?.toString()?.trim().orEmpty()
        return product.ifEmpty { application.getString(R.string.native_audio_usb_device) }
    }

    private fun describe(stream: PcmStream): String =
        "${stream.sampleRate} Hz / ${stream.channelCount} ch / ${stream.encoding.label}"

    companion object {
        private const val PREFERENCES = "native_audio_settings"
        private const val USB_BIT_PERFECT = "usb_bit_perfect"

        /** `AudioFormat` and Media3's `C.ENCODING_PCM_*` constants share these values. */
        fun encoding(value: Int): PcmEncoding? = when (value) {
            AudioFormat.ENCODING_PCM_8BIT -> PcmEncoding.PCM_8
            AudioFormat.ENCODING_PCM_16BIT -> PcmEncoding.PCM_16
            AudioFormat.ENCODING_PCM_24BIT_PACKED -> PcmEncoding.PCM_24
            AudioFormat.ENCODING_PCM_32BIT -> PcmEncoding.PCM_32
            AudioFormat.ENCODING_PCM_FLOAT -> PcmEncoding.PCM_FLOAT
            else -> null
        }

        fun androidEncoding(value: PcmEncoding): Int = when (value) {
            PcmEncoding.PCM_8 -> AudioFormat.ENCODING_PCM_8BIT
            PcmEncoding.PCM_16 -> AudioFormat.ENCODING_PCM_16BIT
            PcmEncoding.PCM_24 -> AudioFormat.ENCODING_PCM_24BIT_PACKED
            PcmEncoding.PCM_32 -> AudioFormat.ENCODING_PCM_32BIT
            PcmEncoding.PCM_FLOAT -> AudioFormat.ENCODING_PCM_FLOAT
        }
    }
}
