package io.jastreamer.android

import android.content.Context
import android.media.AudioFormat
import android.media.MediaCodec
import android.media.MediaExtractor
import android.media.MediaFormat
import android.os.Build
import java.io.IOException
import java.nio.ByteBuffer
import java.nio.ByteOrder
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** The Server track the developer test decodes, resolved natively so no URL reaches the Web UI. */
internal data class UsbDirectTrack(
    val url: String,
    val headers: Map<String, String>,
    val mime: String,
    val label: String,
)

/**
 * The debug-only "USB direct test": it decodes the Server track this phone last loaded with
 * `MediaExtractor` + `MediaCodec` and streams the decoded PCM straight to a USB Audio Class DAC
 * through [UsbDirectOutput], bypassing Android's audio stack entirely.
 *
 * Nothing here runs on its own. [scan] and [start] happen only on an explicit press in the
 * developer panel, and every exit path releases the DAC.
 */
internal class UsbDirectTestSession(
    context: Context,
    private val scope: CoroutineScope,
    private val onChanged: () -> Unit,
) {
    private val output = UsbDirectOutput(context)
    private var job: Job? = null

    /** Set while the user's own Stop tears the session down, so that teardown is not an error. */
    @Volatile
    private var stopRequested = false

    @Volatile
    var state: String = STATE_IDLE
        private set
    @Volatile
    var deviceName: String = ""
        private set
    @Volatile
    var capabilities: UsbDirectCapabilities? = null
        private set
    @Volatile
    var requested: UsbDirectStream? = null
        private set
    @Volatile
    var actual: UsbDirectActual? = null
        private set
    @Volatile
    var decoder: String = ""
        private set
    @Volatile
    var decoderEncoding: String = ""
        private set
    @Volatile
    var error: Pair<String, String>? = null
        private set

    val supported: Boolean get() = UsbDirectNative.available

    val busy: Boolean get() = state == STATE_OPENING || state == STATE_PLAYING

    fun status(): UsbDirectStatus? = output.status()

    /** Finds the DAC, asks for USB access once, opens it and parses its descriptors. */
    suspend fun scan() {
        if (busy) throw UsbDirectException("usb_direct_busy", "The USB direct test is already running.")
        openDevice()
        onChanged()
    }

    private suspend fun openDevice() {
        if (!UsbDirectNative.available) {
            throw fail(
                "usb_direct_unavailable",
                UsbDirectNative.loadFailure ?: "The USB direct native library is unavailable.",
            )
        }
        error = null
        val target = output.candidate()
            ?: throw fail("usb_no_device", "No USB audio device is attached.")
        deviceName = output.label(target)
        if (!output.hasPermission(target) && !output.requestPermission(target)) {
            throw fail("usb_permission_denied", "USB access for $deviceName was not granted.")
        }
        try {
            output.open(target)
            capabilities = output.capabilities()
        } catch (failure: UsbDirectException) {
            output.close()
            throw fail(failure.code, failure.message.orEmpty())
        }
        capabilities?.product?.takeIf(String::isNotBlank)?.let { deviceName = it }
    }

    /**
     * Decodes [track] and plays it through the DAC at the decoder's own sample rate and precision.
     * The DAC is opened (and the user prompted) first when [scan] has not run yet.
     */
    fun start(track: UsbDirectTrack) {
        if (busy) throw UsbDirectException("usb_direct_busy", "The USB direct test is already running.")
        error = null
        requested = null
        actual = null
        decoder = ""
        decoderEncoding = ""
        stopRequested = false
        state = STATE_OPENING
        onChanged()
        job = scope.launch {
            try {
                if (!output.opened) openDevice()
                withContext(Dispatchers.IO) { play(track) }
                releaseDevice()
                if (state != STATE_ERROR) state = STATE_IDLE
            } catch (cancelled: CancellationException) {
                releaseDevice()
                if (state != STATE_ERROR) state = STATE_IDLE
                throw cancelled
            } catch (failure: UsbDirectException) {
                releaseDevice()
                // A failure caused by the user's own Stop is not an error worth reporting.
                if (stopRequested) state = STATE_IDLE else fail(failure.code, failure.message.orEmpty())
            } catch (failure: Throwable) {
                releaseDevice()
                if (stopRequested) state = STATE_IDLE
                else fail("usb_direct_failed", failure.message ?: "The USB direct test failed.")
            } finally {
                job = null
                onChanged()
            }
        }
    }

    /** Stops the test and hands the DAC back to Android. */
    fun stop() {
        val active = job
        job = null
        stopRequested = true
        active?.cancel()
        releaseDevice()
        if (state != STATE_ERROR) state = STATE_IDLE
        onChanged()
    }

    /** Releases everything this session holds; used on service shutdown and on Server handoff. */
    fun release() {
        stop()
        capabilities = null
        deviceName = ""
    }

    private fun releaseDevice() {
        output.stop()
        output.close()
    }

    private suspend fun play(track: UsbDirectTrack) {
        val extractor = MediaExtractor()
        var codec: MediaCodec? = null
        try {
            try {
                extractor.setDataSource(track.url, track.headers)
            } catch (failure: IOException) {
                throw UsbDirectException("usb_source_failed", "The Server track could not be opened for decoding.")
            }
            val trackIndex = (0 until extractor.trackCount).firstOrNull { index ->
                extractor.getTrackFormat(index).getString(MediaFormat.KEY_MIME)?.startsWith("audio/") == true
            } ?: throw UsbDirectException("usb_source_failed", "The Server track has no audio stream.")
            extractor.selectTrack(trackIndex)
            val inputFormat = extractor.getTrackFormat(trackIndex)
            val mime = inputFormat.getString(MediaFormat.KEY_MIME)
                ?: throw UsbDirectException("usb_source_failed", "The Server track has no audio stream.")
            // API 31 is the first release where a decoder may answer with more than 16-bit PCM.
            // Below that, and whenever the decoder declines, the actual encoding is reported as it is.
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
                inputFormat.setInteger(MediaFormat.KEY_PCM_ENCODING, AudioFormat.ENCODING_PCM_24BIT_PACKED)
            }
            val created = MediaCodec.createDecoderByType(mime)
            codec = created
            decoder = created.name
            created.configure(inputFormat, null, null, 0)
            created.start()
            decodeInto(extractor, created)
        } finally {
            codec?.let { active ->
                try {
                    active.stop()
                } catch (_: IllegalStateException) {
                    // A codec that never started, or already errored, needs no stop.
                }
                active.release()
            }
            extractor.release()
        }
    }

    private suspend fun decodeInto(extractor: MediaExtractor, codec: MediaCodec) {
        val info = MediaCodec.BufferInfo()
        var inputDone = false
        var started = false
        var sourceBytesPerSample = 0
        var targetBytesPerSample = 0
        var staging: ByteBuffer? = null

        while (true) {
            currentCoroutineContext().ensureActive()
            if (!inputDone) {
                val inputIndex = codec.dequeueInputBuffer(DEQUEUE_TIMEOUT_MICROS)
                if (inputIndex >= 0) {
                    val buffer = codec.getInputBuffer(inputIndex)
                    val read = if (buffer == null) -1 else extractor.readSampleData(buffer, 0)
                    if (read < 0) {
                        codec.queueInputBuffer(inputIndex, 0, 0, 0L, MediaCodec.BUFFER_FLAG_END_OF_STREAM)
                        inputDone = true
                    } else {
                        codec.queueInputBuffer(inputIndex, 0, read, extractor.sampleTime, 0)
                        extractor.advance()
                    }
                }
            }

            val outputIndex = codec.dequeueOutputBuffer(info, DEQUEUE_TIMEOUT_MICROS)
            if (outputIndex < 0) continue

            if (!started) {
                val format = codec.outputFormat
                val encoding = pcmEncoding(format)
                decoderEncoding = encodingLabel(encoding)
                sourceBytesPerSample = bytesPerSample(encoding)
                val sampleRate = format.getInteger(MediaFormat.KEY_SAMPLE_RATE)
                val channels = format.getInteger(MediaFormat.KEY_CHANNEL_COUNT)
                val bits = sourceBytesPerSample * 8
                requested = UsbDirectStream(sampleRate, bits, channels)
                val opened = output.start(sampleRate, channels, bits)
                actual = opened
                targetBytesPerSample = opened.subslotBytes
                if (targetBytesPerSample != sourceBytesPerSample && targetBytesPerSample != 4) {
                    throw UsbDirectException(
                        "usb_format_mismatch",
                        "The DAC wants a ${targetBytesPerSample}-byte subslot, which this test cannot " +
                            "produce from ${sourceBytesPerSample * 8}-bit decoder output.",
                    )
                }
                if (targetBytesPerSample != sourceBytesPerSample) {
                    staging = ByteBuffer.allocateDirect(STAGING_BYTES).order(ByteOrder.LITTLE_ENDIAN)
                }
                state = STATE_PLAYING
                started = true
                onChanged()
            }

            val outputBuffer = codec.getOutputBuffer(outputIndex)
            if (outputBuffer != null && info.size > 0) {
                outputBuffer.position(info.offset)
                outputBuffer.limit(info.offset + info.size)
                if (targetBytesPerSample == sourceBytesPerSample) {
                    writeFully(outputBuffer.slice(), info.size)
                } else {
                    writeWidened(outputBuffer.slice(), info.size, sourceBytesPerSample, requireNotNull(staging))
                }
            }
            codec.releaseOutputBuffer(outputIndex, false)
            if (info.flags and MediaCodec.BUFFER_FLAG_END_OF_STREAM != 0) break
        }
        if (started) drain()
    }

    /** Pushes every byte into the native ring, waiting when it is full rather than dropping audio. */
    private suspend fun writeFully(buffer: ByteBuffer, length: Int) {
        var remaining = length
        var offset = 0
        while (remaining > 0) {
            currentCoroutineContext().ensureActive()
            buffer.position(offset)
            val accepted = output.write(buffer.slice(), remaining)
            if (accepted <= 0) {
                delay(RING_WAIT_MILLIS)
                continue
            }
            offset += accepted
            remaining -= accepted
        }
    }

    /**
     * Places packed 24-bit samples in the low three bytes of a 4-byte subslot, exactly as USB Audio
     * Class devices expect a 24-in-32 stream, and leaves the padding byte zero.
     */
    private suspend fun writeWidened(
        buffer: ByteBuffer,
        length: Int,
        sourceBytesPerSample: Int,
        staging: ByteBuffer,
    ) {
        val samples = length / sourceBytesPerSample
        var sample = 0
        while (sample < samples) {
            currentCoroutineContext().ensureActive()
            staging.clear()
            val batch = minOf(samples - sample, staging.capacity() / 4)
            for (index in 0 until batch) {
                val base = (sample + index) * sourceBytesPerSample
                for (byteIndex in 0 until sourceBytesPerSample) {
                    staging.put(buffer.get(base + byteIndex))
                }
                repeat(4 - sourceBytesPerSample) { staging.put(0) }
            }
            val produced = staging.position()
            staging.position(0)
            writeFully(staging, produced)
            sample += batch
        }
    }

    /** Lets the ring empty so the last packets actually reach the DAC before the interface closes. */
    private suspend fun drain() {
        repeat(DRAIN_ATTEMPTS) {
            val buffered = output.status()?.bufferedBytes ?: return
            if (buffered <= 0) return
            delay(RING_WAIT_MILLIS)
        }
    }

    private fun fail(code: String, message: String): UsbDirectException {
        state = STATE_ERROR
        error = code to message
        onChanged()
        return UsbDirectException(code, message)
    }

    private fun pcmEncoding(format: MediaFormat): Int =
        if (format.containsKey(MediaFormat.KEY_PCM_ENCODING)) {
            format.getInteger(MediaFormat.KEY_PCM_ENCODING)
        } else {
            AudioFormat.ENCODING_PCM_16BIT
        }

    private fun bytesPerSample(encoding: Int): Int = when (encoding) {
        AudioFormat.ENCODING_PCM_16BIT -> 2
        AudioFormat.ENCODING_PCM_24BIT_PACKED -> 3
        AudioFormat.ENCODING_PCM_32BIT -> 4
        else -> throw UsbDirectException(
            "usb_encoding_unsupported",
            "The decoder produced ${encodingLabel(encoding)}, which this test does not send to a DAC.",
        )
    }

    private fun encodingLabel(encoding: Int): String = when (encoding) {
        AudioFormat.ENCODING_PCM_8BIT -> "pcm_8"
        AudioFormat.ENCODING_PCM_16BIT -> "pcm_16"
        AudioFormat.ENCODING_PCM_24BIT_PACKED -> "pcm_24"
        AudioFormat.ENCODING_PCM_32BIT -> "pcm_32"
        AudioFormat.ENCODING_PCM_FLOAT -> "pcm_float"
        else -> "pcm_$encoding"
    }

    internal companion object {
        const val STATE_IDLE = "idle"
        const val STATE_OPENING = "opening"
        const val STATE_PLAYING = "playing"
        const val STATE_ERROR = "error"

        private const val DEQUEUE_TIMEOUT_MICROS = 10_000L
        private const val RING_WAIT_MILLIS = 3L
        private const val DRAIN_ATTEMPTS = 400
        private const val STAGING_BYTES = 16_384
    }
}

/** The stream the test asked the DAC for, before the device answered. */
internal data class UsbDirectStream(val sampleRate: Int, val bits: Int, val channels: Int)
