package io.jastreamer.android

import android.content.Context
import android.content.SharedPreferences
import android.hardware.usb.UsbDevice
import java.nio.ByteBuffer

/** Whether the direct USB path can be selected at all, and what is missing when it cannot. */
internal data class UsbDirectAvailability(
    val supported: Boolean,
    val available: Boolean,
    val reason: String,
    val devices: List<UsbAudioDevice>,
)

/** A USB audio device as the settings panel lists it. */
internal data class UsbAudioDevice(val id: String, val name: String)

/** Live counters of the running isochronous stream. */
internal data class UsbDirectCounters(
    val underruns: Long,
    val feedbackRate: Int,
    val bufferedBytes: Int,
    val error: String,
)

/** The outcome of asking the attached DAC to carry one track. */
internal sealed interface UsbDirectOpen {
    data class Started(val plan: UsbDirectPlan) : UsbDirectOpen

    /** The DAC cannot take this track unchanged; the `unsupported_format` choice decides. */
    data class Rejected(val rejection: UsbDirectRejection) : UsbDirectOpen
}

/**
 * Owns the USB Audio Class device for Server-owned playback: the opt-in settings, the device and
 * permission state the panel reports, and the claim/start/stop lifecycle [UsbDirectAudioSink]
 * drives from the playback thread.
 *
 * Nothing here opens a device on its own. The sink asks for one when a track is configured while
 * the option is on, and every failure path hands the DAC back to Android.
 */
internal class UsbDirectEngine(context: Context, private val onChanged: () -> Unit) {
    private val application = context.applicationContext
    private val output = UsbDirectOutput(application)
    private val preferences: SharedPreferences =
        application.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE)

    /** Serialises claim, start, stop and release across the playback thread and the main thread. */
    private val deviceLock = Any()

    @Volatile
    private var streaming = false

    /** The stream the DAC is actually running, or null when the USB path is not carrying audio. */
    @Volatile
    var activeStream: UsbActualStream? = null
        private set

    /** The last reason the USB path could not be used, kept for the panel and the failure report. */
    @Volatile
    var rejection: UsbDirectRejection? = null
        private set

    /** The opt-in setting, stored per app rather than per Server, and off by default. */
    val enabled: Boolean get() = preferences.getBoolean(USB_DIRECT, false)

    /** What to do with a track this DAC cannot take unchanged. */
    val unsupportedFormat: String
        get() = UsbDirectPolicy.unsupportedFormatChoice(preferences.getString(UNSUPPORTED_FORMAT, null))

    fun setSettings(enabled: Boolean, unsupportedFormat: String) {
        preferences.edit()
            .putBoolean(USB_DIRECT, enabled)
            .putString(UNSUPPORTED_FORMAT, UsbDirectPolicy.unsupportedFormatChoice(unsupportedFormat))
            .apply()
        rejection = null
        if (!enabled) release()
    }

    fun availability(): UsbDirectAvailability {
        if (!UsbDirectNative.available) {
            return UsbDirectAvailability(
                supported = false,
                available = false,
                reason = UsbDirectPolicy.UNAVAILABLE_DRIVER,
                devices = emptyList(),
            )
        }
        val devices = output.devices()
        val reason = when {
            devices.isEmpty() -> UsbDirectPolicy.UNAVAILABLE_NO_USB_DEVICE
            devices.none(output::hasPermission) -> UsbDirectPolicy.UNAVAILABLE_PERMISSION
            else -> UsbDirectPolicy.UNAVAILABLE_NONE
        }
        return UsbDirectAvailability(
            supported = true,
            available = reason.isEmpty(),
            reason = reason,
            devices = devices.map { UsbAudioDevice(deviceId(it), output.label(it)) },
        )
    }

    /** The device the panel names as the requested path, whether or not anything is playing. */
    fun deviceLabel(): String =
        activeStream?.deviceName ?: output.candidate()?.let(output::label).orEmpty()

    /**
     * Asks the user for USB access from the settings panel rather than from playback: a dialog in
     * the middle of a track would either block the decoder or silently fail the item.
     */
    suspend fun requestPermission(): Boolean {
        val target = output.candidate() ?: return false
        if (output.hasPermission(target)) return true
        val granted = output.requestPermission(target)
        onChanged()
        return granted
    }

    /** True while [device] is the DAC this engine holds. */
    fun holds(device: UsbDevice): Boolean = output.device?.deviceName == device.deviceName

    /** Whether [device] is a USB audio device at all, detached ones included. */
    fun isAudioDevice(device: UsbDevice): Boolean = output.isAudioDevice(device)

    /** True while the DAC is actually carrying audio. */
    val streamingActive: Boolean get() = activeStream != null

    /**
     * Applies [UsbDirectPolicy.detachOutcome] for a device that went away: the DAC is handed back
     * and the option is persisted off, because Android drops the USB permission with the device
     * and a switch left on could never open it again without being toggled.
     */
    fun handleDetached(usbAudioDevice: Boolean, heldByEngine: Boolean): UsbDetachOutcome {
        val outcome = UsbDirectPolicy.detachOutcome(
            usbAudioDevice = usbAudioDevice,
            heldByEngine = heldByEngine,
            settingEnabled = enabled,
            playingThroughUsb = streamingActive,
        )
        if (!outcome.handled) return outcome
        if (outcome.disableSetting) {
            preferences.edit().putBoolean(USB_DIRECT, false).apply()
        }
        if (outcome.releaseDevice) release()
        return outcome
    }

    /**
     * Claims the DAC and starts the isochronous stream for [source], or explains why it cannot
     * carry the track unchanged. Runs on the playback thread and never converts anything itself.
     */
    fun open(source: SourceStream): UsbDirectOpen {
        synchronized(deviceLock) {
            stopStreamLocked()
            if (!UsbDirectNative.available) {
                return reject(UsbDirectPolicy.FAILURE_DRIVER, driverFailure())
            }
            val target = output.candidate() ?: run {
                // The device went away while nothing was watching (a detach during an app
                // restart). Turning the option off here keeps every later track from failing
                // against a device that is not there.
                if (enabled) preferences.edit().putBoolean(USB_DIRECT, false).apply()
                return reject(
                    UsbDirectPolicy.FAILURE_DEVICE_DETACHED,
                    application.getString(R.string.native_audio_usb_detached),
                )
            }
            if (!output.hasPermission(target)) {
                return reject(
                    UsbDirectPolicy.FAILURE_PERMISSION,
                    application.getString(R.string.native_audio_usb_permission, output.label(target)),
                )
            }
            val capabilities = try {
                if (!output.opened || output.device?.deviceName != target.deviceName) {
                    output.open(target)
                }
                output.capabilities()
            } catch (failure: UsbDirectException) {
                output.close()
                return reject(UsbDirectPolicy.FAILURE_OPEN, failure.message.orEmpty())
            }
            val decision = UsbDirectPolicy.plan(source, capabilities.formats)
            if (decision is UsbDirectDecision.Unsupported) {
                rejection = decision.rejection
                onChanged()
                return UsbDirectOpen.Rejected(decision.rejection)
            }
            val plan = (decision as UsbDirectDecision.Direct).plan
            val actual = try {
                output.start(plan.sampleRate, plan.deviceChannels, plan.validBits)
            } catch (failure: UsbDirectException) {
                return reject(UsbDirectPolicy.FAILURE_START, failure.message.orEmpty())
            }
            // The device reports the alternate setting it really selected; a driver that did not
            // honour the plan fails the track instead of playing a different format.
            if (actual.channels != plan.deviceChannels ||
                actual.subslotBytes * 8 != plan.containerBits ||
                actual.bits < plan.validBits
            ) {
                output.stop()
                return reject(
                    UsbDirectPolicy.FAILURE_START,
                    "The USB device opened a stream other than the one it advertised.",
                )
            }
            streaming = true
            activeStream = UsbActualStream(
                engine = UsbDirectPolicy.ENGINE_USB_DIRECT,
                deviceName = capabilities.product.ifBlank { output.label(target) },
                sampleRate = actual.actualSampleRate.takeIf { it > 0 } ?: actual.sampleRate,
                channels = actual.channels,
                validBits = plan.validBits,
                containerBits = actual.subslotBytes * 8,
                encoding = encodingLabel(plan.validBits),
            )
            sync = actual.sync
            rejection = null
            onChanged()
            return UsbDirectOpen.Started(plan)
        }
    }

    /** The synchronisation type of the running stream, as the descriptors declare it. */
    @Volatile
    var sync: String = ""
        private set

    fun write(buffer: ByteBuffer, length: Int, sourceSampleBytes: Int, sourceChannels: Int): Int {
        if (!streaming) return -1
        return output.write(buffer, length, sourceSampleBytes, sourceChannels)
    }

    fun setPaused(paused: Boolean) {
        if (streaming) output.setPaused(paused)
    }

    fun flush() {
        if (streaming) output.flush()
    }

    fun playedFrames(): Long = if (streaming) output.playedFrames() else 0L

    fun counters(): UsbDirectCounters? {
        val status = if (streaming) output.status() else null
        return status?.let {
            UsbDirectCounters(
                underruns = it.underruns,
                feedbackRate = it.feedbackRate,
                bufferedBytes = it.bufferedBytes,
                error = it.error,
            )
        }
    }

    /** Stops the transfers but keeps the claim, so the next track of the same run restarts faster. */
    fun stopStream() {
        val wasStreaming = synchronized(deviceLock) {
            val active = streaming
            stopStreamLocked()
            active
        }
        if (wasStreaming) onChanged()
    }

    /** Hands the DAC back to Android. Safe to call when nothing is open. */
    fun release() {
        val hadDevice = synchronized(deviceLock) {
            val had = output.opened
            stopStreamLocked()
            output.close()
            had
        }
        if (hadDevice) onChanged()
    }

    /** Records a failure raised outside [open], such as the DAC being unplugged mid-track. */
    fun recordFailure(code: String, detail: String) {
        rejection = UsbDirectRejection(code, detail)
        onChanged()
    }

    private fun stopStreamLocked() {
        if (streaming) {
            output.stop()
            streaming = false
        }
        activeStream = null
        sync = ""
    }

    private fun reject(code: String, detail: String): UsbDirectOpen.Rejected {
        val value = UsbDirectRejection(code, detail)
        rejection = value
        onChanged()
        return UsbDirectOpen.Rejected(value)
    }

    private fun driverFailure(): String =
        UsbDirectNative.loadFailure ?: "The USB direct driver is unavailable on this phone."

    private fun deviceId(device: UsbDevice): String =
        String.format("%04x:%04x", device.vendorId, device.productId)

    private fun encodingLabel(validBits: Int): String = when (validBits) {
        16 -> PcmEncoding.PCM_16.label
        24 -> PcmEncoding.PCM_24.label
        32 -> PcmEncoding.PCM_32.label
        else -> "pcm_$validBits"
    }

    private companion object {
        const val PREFERENCES = "native_audio_settings"
        const val USB_DIRECT = "usb_direct"
        const val UNSUPPORTED_FORMAT = "unsupported_format"
    }
}
