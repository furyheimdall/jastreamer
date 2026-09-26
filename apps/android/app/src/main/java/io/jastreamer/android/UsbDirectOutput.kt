package io.jastreamer.android

import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.hardware.usb.UsbConstants
import android.hardware.usb.UsbDevice
import android.hardware.usb.UsbDeviceConnection
import android.hardware.usb.UsbManager
import android.os.Build
import androidx.core.content.ContextCompat
import java.nio.ByteBuffer
import kotlinx.coroutines.suspendCancellableCoroutine
import org.json.JSONException
import org.json.JSONObject

/** One streaming format a USB Audio Class device advertises in its descriptors. */
internal data class UsbDirectFormat(
    val alt: Int,
    val bits: Int,
    val subslotBytes: Int,
    val channels: Int,
    val sync: String,
    val feedback: Boolean,
    val maxPacketBytes: Int,
    val interval: Int,
    val rates: List<Int>,
)

/** What the DAC's descriptors (and, for UAC2, its clock source range request) actually report. */
internal data class UsbDirectCapabilities(
    val uacVersion: Int,
    val highSpeed: Boolean,
    val product: String,
    val controlInterface: Int,
    val streamingInterface: Int,
    val formats: List<UsbDirectFormat>,
)

/** The stream the device was actually opened with, read back from the device where possible. */
internal data class UsbDirectActual(
    val sampleRate: Int,
    val actualSampleRate: Int,
    val channels: Int,
    val bits: Int,
    val subslotBytes: Int,
    val alt: Int,
    val sync: String,
    val feedback: Boolean,
    val packetFrames: Int,
    val highSpeed: Boolean,
    val ringBytes: Int,
)

/** Live isochronous counters, polled by the developer panel while the test runs. */
internal data class UsbDirectStatus(
    val running: Boolean,
    val packets: Long,
    val underruns: Long,
    val frames: Long,
    val bufferedBytes: Int,
    val feedbackRate: Int,
    val error: String,
)

internal class UsbDirectException(val code: String, message: String) : Exception(message)

/**
 * Drives a USB Audio Class DAC straight from `libusb`, bypassing the Android audio stack.
 *
 * While [start] is in effect the DAC's streaming interface is claimed, so nothing else on the
 * phone can play through it. Every path releases the device - [close] is safe to call repeatedly
 * and is called from every failure branch.
 */
internal class UsbDirectOutput(context: Context) {
    private val application = context.applicationContext
    private val usbManager = application.getSystemService(UsbManager::class.java)

    /**
     * Serialises every JNI call. The decode thread writes while the main thread can stop or close,
     * and a native handle must never be freed underneath an in-flight call.
     */
    private val nativeLock = Any()
    private var connection: UsbDeviceConnection? = null
    @Volatile
    private var handle: Long = 0L
    private var running = false

    @Volatile
    var device: UsbDevice? = null
        private set

    val opened: Boolean get() = handle != 0L

    /** Every attached device that declares a USB audio streaming interface. */
    fun devices(): List<UsbDevice> =
        usbManager?.deviceList?.values?.filter(::hasAudioStreaming).orEmpty()

    /**
     * The first attached device that declares a USB audio function. Composite dongles expose the
     * AudioControl interface on the same device as their HID volume keys, so the class check looks
     * at every interface rather than the device class.
     */
    fun candidate(): UsbDevice? = devices().firstOrNull()

    fun hasPermission(target: UsbDevice): Boolean = usbManager?.hasPermission(target) == true

    /**
     * Asks the user for access to [target]. Android shows its own dialog; this never grants itself
     * access and never retries silently.
     */
    suspend fun requestPermission(target: UsbDevice): Boolean {
        val manager = usbManager ?: throw UsbDirectException("usb_unsupported", "This phone has no USB host support.")
        if (manager.hasPermission(target)) return true
        return suspendCancellableCoroutine { continuation ->
            val receiver = object : BroadcastReceiver() {
                override fun onReceive(receiverContext: Context?, intent: Intent?) {
                    if (intent?.action != PERMISSION_ACTION) return
                    try {
                        application.unregisterReceiver(this)
                    } catch (_: IllegalArgumentException) {
                        // Already unregistered by cancellation; the result below still applies.
                    }
                    if (continuation.isActive) {
                        continuation.resumeWith(
                            Result.success(intent.getBooleanExtra(UsbManager.EXTRA_PERMISSION_GRANTED, false)),
                        )
                    }
                }
            }
            ContextCompat.registerReceiver(
                application,
                receiver,
                IntentFilter(PERMISSION_ACTION),
                ContextCompat.RECEIVER_NOT_EXPORTED,
            )
            continuation.invokeOnCancellation {
                try {
                    application.unregisterReceiver(receiver)
                } catch (_: IllegalArgumentException) {
                    // The broadcast already removed it.
                }
            }
            // The framework writes the device and the grant into this intent, so it must stay
            // mutable on API 31+; it is explicitly addressed at this package so nothing else can
            // deliver it.
            val flags = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
                PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_MUTABLE
            } else {
                PendingIntent.FLAG_UPDATE_CURRENT
            }
            val permissionIntent = PendingIntent.getBroadcast(
                application,
                0,
                Intent(PERMISSION_ACTION).setPackage(application.packageName),
                flags,
            )
            manager.requestPermission(target, permissionIntent)
        }
    }

    /**
     * Opens [target] and hands its file descriptor to libusb. The connection stays owned here.
     *
     * Every native call below runs under [nativeLock] so the feeder thread cannot be inside a
     * transfer while the main thread frees the device.
     */
    fun open(target: UsbDevice) {
        if (!UsbDirectNative.available) {
            throw UsbDirectException(
                "usb_direct_unavailable",
                UsbDirectNative.loadFailure ?: "The USB direct native library is unavailable.",
            )
        }
        val manager = usbManager ?: throw UsbDirectException("usb_unsupported", "This phone has no USB host support.")
        if (!manager.hasPermission(target)) {
            throw UsbDirectException("usb_permission_denied", "USB access for ${label(target)} was not granted.")
        }
        close()
        synchronized(nativeLock) {
            val opened = manager.openDevice(target)
                ?: throw UsbDirectException("usb_open_failed", "Android refused to open ${label(target)}.")
            val nativeHandle = UsbDirectNative.open(opened.fileDescriptor)
            if (nativeHandle == 0L) {
                opened.close()
                throw UsbDirectException("usb_open_failed", nativeError("libusb could not adopt ${label(target)}."))
            }
            connection = opened
            handle = nativeHandle
            device = target
        }
    }

    fun capabilities(): UsbDirectCapabilities {
        val raw = synchronized(nativeLock) { UsbDirectNative.capabilities(requireHandle()) }
        if (raw.isEmpty()) {
            throw UsbDirectException("usb_descriptors_failed", nativeError("The device descriptors could not be read."))
        }
        val value = parse(raw)
        val formats = mutableListOf<UsbDirectFormat>()
        val reported = value.optJSONArray("formats")
        if (reported != null) {
            for (index in 0 until reported.length()) {
                val format = reported.optJSONObject(index) ?: continue
                val rates = mutableListOf<Int>()
                format.optJSONArray("rates")?.let { values ->
                    for (rateIndex in 0 until values.length()) {
                        val rate = values.optInt(rateIndex, 0)
                        if (rate > 0) rates.add(rate)
                    }
                }
                formats.add(
                    UsbDirectFormat(
                        alt = format.optInt("alt", 0),
                        bits = format.optInt("bits", 0),
                        subslotBytes = format.optInt("subslot_bytes", 0),
                        channels = format.optInt("channels", 0),
                        sync = format.optString("sync"),
                        feedback = format.optBoolean("feedback", false),
                        maxPacketBytes = format.optInt("max_packet_bytes", 0),
                        interval = format.optInt("interval", 0),
                        rates = rates,
                    ),
                )
            }
        }
        return UsbDirectCapabilities(
            uacVersion = value.optInt("uac_version", 0),
            highSpeed = value.optBoolean("high_speed", false),
            product = value.optString("product"),
            controlInterface = value.optInt("control_interface", 0),
            streamingInterface = value.optInt("streaming_interface", 0),
            formats = formats,
        )
    }

    /** Claims the streaming interface, sets the sample rate and starts the isochronous transfers. */
    fun start(sampleRate: Int, channels: Int, bits: Int): UsbDirectActual {
        val raw = synchronized(nativeLock) {
            UsbDirectNative.start(requireHandle(), sampleRate, channels, bits)
        }
        if (raw.isEmpty()) {
            throw UsbDirectException(
                "usb_start_failed",
                nativeError("The device did not accept $sampleRate Hz / $channels ch / $bits-bit."),
            )
        }
        running = true
        val value = parse(raw)
        return UsbDirectActual(
            sampleRate = value.optInt("sample_rate", sampleRate),
            actualSampleRate = value.optInt("actual_sample_rate", 0),
            channels = value.optInt("channels", channels),
            bits = value.optInt("bits", bits),
            subslotBytes = value.optInt("subslot_bytes", 0),
            alt = value.optInt("alt", 0),
            sync = value.optString("sync"),
            feedback = value.optBoolean("feedback", false),
            packetFrames = value.optInt("packet_frames", 0),
            highSpeed = value.optBoolean("high_speed", false),
            ringBytes = value.optInt("ring_bytes", 0),
        )
    }

    /**
     * Copies up to [length] bytes of interleaved little-endian integer PCM into the native ring
     * buffer, widening samples into the negotiated subslot and duplicating a mono source onto a
     * stereo device where the driver agreed to, and returns how many source bytes were accepted.
     * A short result means the ring is full; the caller retries rather than dropping audio.
     */
    fun write(buffer: ByteBuffer, length: Int, sourceSampleBytes: Int, sourceChannels: Int): Int {
        require(buffer.isDirect) { "USB direct writes need a direct ByteBuffer" }
        val accepted = synchronized(nativeLock) {
            UsbDirectNative.write(requireHandle(), buffer, length, sourceSampleBytes, sourceChannels)
        }
        if (accepted < 0) throw UsbDirectException("usb_write_failed", nativeError("The USB ring buffer rejected audio."))
        return accepted
    }

    /**
     * Stops and resumes feeding audio. The isochronous stream keeps running on silence, so the DAC
     * stays locked and claimed across a pause instead of being handed back and re-acquired.
     */
    fun setPaused(paused: Boolean) {
        synchronized(nativeLock) {
            if (handle == 0L) return
            UsbDirectNative.setPaused(handle, paused)
        }
    }

    /** Drops the audio still queued, for a seek or a track change. */
    fun flush() {
        synchronized(nativeLock) {
            if (handle == 0L) return
            UsbDirectNative.flush(handle)
        }
    }

    /** Frames the device has actually consumed, excluding silence and flushed audio. */
    fun playedFrames(): Long {
        val frames = synchronized(nativeLock) {
            if (handle == 0L) return 0L
            UsbDirectNative.playedFrames(handle)
        }
        return frames.coerceAtLeast(0L)
    }

    fun status(): UsbDirectStatus? {
        val raw = synchronized(nativeLock) {
            if (handle == 0L) return null
            UsbDirectNative.status(handle)
        }
        if (raw.isEmpty()) return null
        val value = parse(raw)
        return UsbDirectStatus(
            running = value.optBoolean("running", false),
            packets = value.optLong("packets", 0L),
            underruns = value.optLong("underruns", 0L),
            frames = value.optLong("frames", 0L),
            bufferedBytes = value.optInt("buffered_bytes", 0),
            feedbackRate = value.optInt("feedback_rate", 0),
            error = value.optString("error"),
        )
    }

    /** Stops the transfers and returns the interface to its zero-bandwidth alt setting. */
    fun stop() {
        synchronized(nativeLock) {
            if (handle == 0L || !running) return
            running = false
            UsbDirectNative.stop(handle)
        }
    }

    /** Releases the interface and the Android connection. Safe to call when nothing is open. */
    fun close() {
        synchronized(nativeLock) {
            val active = handle
            handle = 0L
            running = false
            device = null
            if (active != 0L) UsbDirectNative.close(active)
            connection?.close()
            connection = null
        }
    }

    fun label(target: UsbDevice): String {
        val product = target.productName?.trim().orEmpty()
        if (product.isNotEmpty()) return product
        val manufacturer = target.manufacturerName?.trim().orEmpty()
        if (manufacturer.isNotEmpty()) return manufacturer
        return String.format("USB %04x:%04x", target.vendorId, target.productId)
    }

    private fun requireHandle(): Long =
        handle.takeIf { it != 0L } ?: throw UsbDirectException("usb_not_open", "No USB audio device is open.")

    private fun nativeError(fallback: String): String =
        UsbDirectNative.lastError().takeIf { it.isNotBlank() } ?: fallback

    private fun parse(raw: String): JSONObject = try {
        JSONObject(raw)
    } catch (error: JSONException) {
        throw UsbDirectException("usb_direct_failed", "The USB direct driver returned an unreadable report.")
    }

    private fun hasAudioStreaming(candidate: UsbDevice): Boolean {
        for (index in 0 until candidate.interfaceCount) {
            val usbInterface = candidate.getInterface(index)
            if (usbInterface.interfaceClass == UsbConstants.USB_CLASS_AUDIO &&
                usbInterface.interfaceSubclass == AUDIO_STREAMING_SUBCLASS
            ) {
                return true
            }
        }
        return false
    }

    private companion object {
        const val PERMISSION_ACTION = "io.jastreamer.android.USB_DIRECT_PERMISSION"
        const val AUDIO_STREAMING_SUBCLASS = 2
    }
}
