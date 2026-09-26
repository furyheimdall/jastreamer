package io.jastreamer.android

import androidx.annotation.OptIn
import androidx.media3.common.C
import androidx.media3.common.util.UnstableApi

/**
 * Frame accounting for [UsbDirectAudioSink]: how far the DAC has actually played, how much audio is
 * still queued for it and when the stream has drained.
 *
 * The device counter only ever moves forward, so a flush records the counter it happened at and
 * everything afterwards is measured from there. Pure logic, with no Media3 or USB state, so the
 * position rules can be exercised without a device.
 */
@OptIn(UnstableApi::class)
internal class UsbDirectClock {
    private var sampleRate = 0
    private var sourceFrameBytes = 0
    private var playedBase = 0L
    private var startMediaTimeUs = C.TIME_UNSET
    private var endOfStream = false

    var writtenFrames = 0L
        private set

    /** Starts a new stream at the device's current frame counter. */
    fun configure(sampleRate: Int, sourceFrameBytes: Int, playedFrames: Long) {
        this.sampleRate = sampleRate
        this.sourceFrameBytes = sourceFrameBytes
        reset(playedFrames)
    }

    /** Drops the queued audio for a seek or a track change; the device counter keeps running. */
    fun reset(playedFrames: Long) {
        playedBase = playedFrames
        writtenFrames = 0L
        startMediaTimeUs = C.TIME_UNSET
        endOfStream = false
    }

    /** The first buffer of a run fixes where the media timeline starts. */
    fun onFirstBuffer(presentationTimeUs: Long) {
        if (startMediaTimeUs == C.TIME_UNSET) startMediaTimeUs = presentationTimeUs.coerceAtLeast(0L)
    }

    fun accept(acceptedBytes: Int) {
        if (acceptedBytes <= 0 || sourceFrameBytes <= 0) return
        writtenFrames += acceptedBytes / sourceFrameBytes
    }

    fun endOfStream() {
        endOfStream = true
    }

    /** Frames the device consumed out of what this run queued. */
    fun playedFrames(deviceFrames: Long): Long =
        (deviceFrames - playedBase).coerceIn(0L, writtenFrames)

    /**
     * The media position, or [C.TIME_UNSET] before the first buffer, which Media3 reads as
     * "not set yet" through [androidx.media3.exoplayer.audio.AudioSink.CURRENT_POSITION_NOT_SET].
     */
    fun positionUs(deviceFrames: Long): Long {
        if (startMediaTimeUs == C.TIME_UNSET || sampleRate <= 0) return C.TIME_UNSET
        return startMediaTimeUs + playedFrames(deviceFrames) * C.MICROS_PER_SECOND / sampleRate
    }

    fun hasPendingData(deviceFrames: Long): Boolean = playedFrames(deviceFrames) < writtenFrames

    fun isEnded(deviceFrames: Long): Boolean = endOfStream && !hasPendingData(deviceFrames)
}
