package io.jastreamer.android

import android.content.Context
import android.media.AudioFormat
import android.media.MediaFormat
import android.os.Build
import android.os.Handler
import androidx.annotation.OptIn
import androidx.media3.common.C
import androidx.media3.common.Format
import androidx.media3.common.PlaybackException
import androidx.media3.common.PlaybackParameters
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.DefaultRenderersFactory
import androidx.media3.exoplayer.Renderer
import androidx.media3.exoplayer.audio.AudioRendererEventListener
import androidx.media3.exoplayer.audio.AudioSink
import androidx.media3.exoplayer.audio.ForwardingAudioSink
import androidx.media3.exoplayer.audio.MediaCodecAudioRenderer
import androidx.media3.exoplayer.mediacodec.MediaCodecSelector
import java.nio.ByteBuffer

/**
 * The Media3 sink for Server-owned playback.
 *
 * With the USB direct option on it feeds the decoded PCM straight to the USB Audio Class DAC
 * through [UsbDirectEngine], bypassing the Android mixer entirely. With the option off - and for a
 * track this DAC cannot take unchanged while `unsupported_format` is `system_output` - it forwards
 * to the ordinary [androidx.media3.exoplayer.audio.DefaultAudioSink] instead. It never resamples,
 * dithers, downmixes or converts float to integer itself.
 */
@OptIn(UnstableApi::class)
internal class UsbDirectAudioSink(
    fallback: AudioSink,
    private val engine: UsbDirectEngine,
) : ForwardingAudioSink(fallback) {
    private val clock = UsbDirectClock()
    private var usbMode = false
    private var inputFormat: Format? = null
    private var sourceSampleBytes = 0
    private var sourceFrameBytes = 0
    private var sourceChannels = 0
    private var reportedUnderruns = 0L
    private var listener: AudioSink.Listener? = null

    /** The rejection that pushed this track onto the Android output, for the panel. */
    var fallbackRejection: UsbDirectRejection? = null
        private set

    val usbActive: Boolean get() = usbMode

    override fun setListener(listener: AudioSink.Listener) {
        this.listener = listener
        super.setListener(listener)
    }

    override fun configure(audioSinkConfig: AudioSink.AudioSinkConfig) {
        val format = audioSinkConfig.format
        if (!engine.enabled || !isLinearPcm(format)) {
            configureFallback(audioSinkConfig, rejection = null)
            return
        }
        val stream = sourceStream(format)
        when (val opened = engine.open(stream)) {
            is UsbDirectOpen.Started -> {
                if (usbMode) {
                    // Nothing else may hold the Android output while the DAC carries the audio.
                    super.reset()
                }
                usbMode = true
                inputFormat = format
                sourceChannels = format.channelCount
                sourceSampleBytes = opened.plan.sourceSampleBytes
                sourceFrameBytes = sourceSampleBytes * sourceChannels
                clock.configure(format.sampleRate, sourceFrameBytes, engine.playedFrames())
                fallbackRejection = null
                reportedUnderruns = engine.counters()?.underruns ?: 0L
            }

            is UsbDirectOpen.Rejected -> {
                if (engine.unsupportedFormat == UsbDirectPolicy.UNSUPPORTED_SKIP) {
                    usbMode = false
                    engine.release()
                    throw AudioSink.ConfigurationException(
                        "${opened.rejection.code}: ${opened.rejection.detail}",
                        format,
                    )
                }
                // The user asked for the Android output rather than a skipped track.
                engine.release()
                configureFallback(audioSinkConfig, opened.rejection)
            }
        }
    }

    private fun configureFallback(config: AudioSink.AudioSinkConfig, rejection: UsbDirectRejection?) {
        usbMode = false
        inputFormat = config.format
        fallbackRejection = rejection
        super.configure(config)
    }

    override fun handleBuffer(buffer: ByteBuffer, presentationTimeUs: Long, encodedAccessUnitCount: Int): Boolean {
        if (!usbMode) return super.handleBuffer(buffer, presentationTimeUs, encodedAccessUnitCount)
        clock.onFirstBuffer(presentationTimeUs)
        if (!buffer.hasRemaining()) return true
        val accepted = try {
            engine.write(buffer.slice(), buffer.remaining(), sourceSampleBytes, sourceChannels)
        } catch (failure: UsbDirectException) {
            throw writeFailure(UsbDirectPolicy.FAILURE_DEVICE_LOST, failure.message.orEmpty())
        }
        if (accepted < 0) {
            throw writeFailure(UsbDirectPolicy.FAILURE_DEVICE_LOST, "The USB audio stream stopped.")
        }
        if (accepted == 0) {
            reportUnderruns()
            return false
        }
        buffer.position(buffer.position() + accepted)
        clock.accept(accepted)
        reportUnderruns()
        return !buffer.hasRemaining()
    }

    override fun getCurrentPositionUs(sourceEnded: Boolean): Long {
        if (!usbMode) return super.getCurrentPositionUs(sourceEnded)
        val positionUs = clock.positionUs(engine.playedFrames())
        return if (positionUs == C.TIME_UNSET) AudioSink.CURRENT_POSITION_NOT_SET else positionUs
    }

    override fun play() {
        if (usbMode) engine.setPaused(false) else super.play()
    }

    override fun pause() {
        // The stream keeps running on silence, so the DAC stays claimed and locked across a pause.
        if (usbMode) engine.setPaused(true) else super.pause()
    }

    override fun flush() {
        if (!usbMode) {
            super.flush()
            return
        }
        engine.flush()
        clock.reset(engine.playedFrames())
    }

    override fun handleDiscontinuity() {
        if (!usbMode) super.handleDiscontinuity()
    }

    override fun playToEndOfStream() {
        if (!usbMode) {
            super.playToEndOfStream()
            return
        }
        clock.endOfStream()
    }

    override fun isEnded(): Boolean {
        if (!usbMode) return super.isEnded()
        return clock.isEnded(engine.playedFrames())
    }

    override fun hasPendingData(): Boolean {
        if (!usbMode) return super.hasPendingData()
        return clock.hasPendingData(engine.playedFrames())
    }

    override fun setPlaybackParameters(playbackParameters: PlaybackParameters) {
        // Speed and pitch changes would have to resample; Server playback never asks for them.
        if (!usbMode) super.setPlaybackParameters(playbackParameters)
    }

    override fun getPlaybackParameters(): PlaybackParameters =
        if (usbMode) PlaybackParameters.DEFAULT else super.getPlaybackParameters()

    override fun setVolume(volume: Float) {
        // The USB path is fixed at unity; the Server keeps the app volume at 100% while it is on.
        if (!usbMode) super.setVolume(volume)
    }

    override fun setSkipSilenceEnabled(skipSilenceEnabled: Boolean) {
        if (!usbMode) super.setSkipSilenceEnabled(skipSilenceEnabled)
    }

    override fun getSkipSilenceEnabled(): Boolean = if (usbMode) false else super.getSkipSilenceEnabled()

    override fun getAudioTrackBufferSizeUs(): Long =
        if (usbMode) C.TIME_UNSET else super.getAudioTrackBufferSizeUs()

    override fun reset() {
        if (usbMode) {
            usbMode = false
            engine.stopStream()
            clock.reset(0L)
        }
        super.reset()
    }

    override fun release() {
        usbMode = false
        engine.release()
        super.release()
    }

    /** Surfaces device underruns through the usual Media3 listener, for diagnostics. */
    private fun reportUnderruns() {
        val underruns = engine.counters()?.underruns ?: return
        if (underruns <= reportedUnderruns) return
        reportedUnderruns = underruns
        listener?.onUnderrun(0, 0L, 0L)
    }

    private fun requireFormat(): Format = inputFormat ?: Format.Builder().build()

    /** Records why the DAC stopped carrying audio and fails the item through the usual path. */
    private fun writeFailure(code: String, detail: String): AudioSink.WriteException {
        engine.recordFailure(code, detail)
        return AudioSink.WriteException(
            PlaybackException.ERROR_CODE_AUDIO_TRACK_WRITE_FAILED,
            requireFormat(),
            false,
        )
    }

    /**
     * What the decoder hands over. Provenance - lossless and Server-reported transformation - is
     * not needed to pick a stream, so the transparency verdict is left to the service, which knows
     * the resource the Server sent.
     */
    private fun sourceStream(format: Format): SourceStream = SourceStream(
        sampleRate = format.sampleRate,
        channelCount = format.channelCount,
        encoding = pcmEncoding(format.pcmEncoding),
        lossless = false,
        transformed = null,
    )

    private fun isLinearPcm(format: Format): Boolean =
        format.sampleRate > 0 && format.channelCount > 0 && pcmEncoding(format.pcmEncoding) != null

    internal companion object {
        /** `AudioFormat` and Media3's `C.ENCODING_PCM_*` constants share these values. */
        fun pcmEncoding(value: Int): PcmEncoding? = when (value) {
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

/**
 * Asks the decoder for the source's own precision while the USB direct option is on. Media3 only
 * ever requests float PCM, which no integer DAC can take unchanged, so a 24-bit FLAC would
 * otherwise arrive at the sink already reduced to 16-bit.
 */
@OptIn(UnstableApi::class)
internal class UsbDirectAudioRenderer(
    context: Context,
    codecAdapterFactory: androidx.media3.exoplayer.mediacodec.MediaCodecAdapter.Factory,
    mediaCodecSelector: MediaCodecSelector,
    enableDecoderFallback: Boolean,
    eventHandler: Handler?,
    eventListener: AudioRendererEventListener?,
    audioSink: AudioSink,
    private val engine: UsbDirectEngine,
) : MediaCodecAudioRenderer(
    context,
    codecAdapterFactory,
    mediaCodecSelector,
    enableDecoderFallback,
    eventHandler,
    eventListener,
    audioSink,
) {
    override fun getMediaFormat(
        format: Format,
        codecMimeType: String,
        codecMaxInputSize: Int,
        codecOperatingRate: Float,
    ): MediaFormat {
        val mediaFormat = super.getMediaFormat(format, codecMimeType, codecMaxInputSize, codecOperatingRate)
        if (!engine.enabled || Build.VERSION.SDK_INT < Build.VERSION_CODES.S) return mediaFormat
        val requested = UsbDirectPolicy.decoderPcmEncoding(UsbDirectAudioSink.pcmEncoding(format.pcmEncoding))
            ?: return mediaFormat
        mediaFormat.setInteger(
            MediaFormat.KEY_PCM_ENCODING,
            UsbDirectAudioSink.androidEncoding(requested),
        )
        return mediaFormat
    }
}

/** Builds the Server-owned engine's renderers around [UsbDirectAudioSink]. */
@OptIn(UnstableApi::class)
internal class UsbDirectRenderersFactory(
    context: Context,
    private val engine: UsbDirectEngine,
) : DefaultRenderersFactory(context) {
    /** The sink the service reports the actual path from. */
    var sink: UsbDirectAudioSink? = null
        private set

    override fun buildAudioSink(
        context: Context,
        enableFloatOutput: Boolean,
        enableAudioTrackPlaybackParams: Boolean,
    ): AudioSink {
        val fallback = requireNotNull(super.buildAudioSink(context, enableFloatOutput, enableAudioTrackPlaybackParams))
        return UsbDirectAudioSink(fallback, engine).also { sink = it }
    }

    override fun buildAudioRenderers(
        context: Context,
        extensionRendererMode: Int,
        mediaCodecSelector: MediaCodecSelector,
        enableDecoderFallback: Boolean,
        audioSink: AudioSink,
        eventHandler: Handler,
        eventListener: AudioRendererEventListener,
        out: ArrayList<Renderer>,
    ) {
        out.add(
            UsbDirectAudioRenderer(
                context,
                codecAdapterFactory,
                mediaCodecSelector,
                enableDecoderFallback,
                eventHandler,
                eventListener,
                audioSink,
                engine,
            ),
        )
    }
}
