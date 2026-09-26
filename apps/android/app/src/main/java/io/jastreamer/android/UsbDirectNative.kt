package io.jastreamer.android

import java.nio.ByteBuffer

/**
 * The raw JNI surface of `libusb_direct.so`.
 *
 * Every entry point is deliberately narrow and takes a handle produced by [open]; the native side
 * validates handles against its own registry, so a stale value fails instead of crashing. JSON
 * strings are returned rather than object graphs because the same payloads are forwarded to the
 * developer panel unchanged. An empty string means failure and [lastError] carries the reason for
 * the calling thread.
 */
internal object UsbDirectNative {
    /** Null when the native libraries are missing from this build or ABI, else the failure reason. */
    val loadFailure: String? = try {
        // libusb1.0.so stays a separate LGPL-2.1 shared library; load it before its dependant so a
        // missing dependency is reported here rather than as an opaque relocation failure.
        System.loadLibrary("usb1.0")
        System.loadLibrary("usb_direct")
        null
    } catch (error: UnsatisfiedLinkError) {
        error.message ?: "The USB direct native library is unavailable."
    }

    val available: Boolean get() = loadFailure == null

    external fun open(fd: Int): Long

    external fun lastError(): String

    external fun capabilities(handle: Long): String

    external fun start(handle: Long, sampleRate: Int, channels: Int, bits: Int): String

    external fun write(handle: Long, buffer: ByteBuffer, length: Int): Int

    external fun status(handle: Long): String

    external fun stop(handle: Long)

    external fun close(handle: Long)
}
