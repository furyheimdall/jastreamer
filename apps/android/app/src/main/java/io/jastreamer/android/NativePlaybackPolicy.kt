package io.jastreamer.android

import java.nio.charset.StandardCharsets
import javax.net.ssl.SSLException
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

internal object NativePlaybackPolicy {
    const val OWNER_HEADER = "X-Jastreamer-Browser-Token"
    const val MAX_LEASE_MILLIS = 60_000L
    const val MEDIA_RECOVERY_DEADLINE_MILLIS = 12_000L
    const val MAX_MEDIA_RETRIES = 3
    const val MAX_ARTWORK_BYTES = 1024 * 1024

    val protocolInfo = listOf(
        "audio/mpeg",
        "audio/mp4; codecs=mp4a.40.2",
        "audio/flac",
        "audio/ogg; codecs=vorbis",
        "audio/ogg; codecs=opus",
        "audio/wav",
    )

    fun serverKey(server: ServerEndpoint): String =
        "${EndpointPolicy.normalizeServerId(server.id)}\u0000${EndpointPolicy.normalizeOrigin(server.origin)}"

    fun requireName(input: String): String {
        val name = input.trim()
        if (name.isEmpty() || name.toByteArray(StandardCharsets.UTF_8).size > 80 || name.any(Char::isISOControl)) {
            throw NativePlaybackException("invalid_name", "Enter a name between 1 and 80 bytes.")
        }
        return name
    }

    fun mediaUrl(origin: String, value: String): HttpUrl? {
        if (value.any { it.isISOControl() || it.isWhitespace() || it == '\\' }) return null
        val base = EndpointPolicy.normalizeOrigin(origin).toHttpUrlOrNull() ?: return null
        val target = base.resolve(value) ?: return null
        if (target.scheme != base.scheme || target.host != base.host || target.port != base.port) return null
        if (target.encodedUsername.isNotEmpty() || target.encodedPassword.isNotEmpty()) return null
        if (!target.encodedPath.startsWith("/media/") || target.query != null || target.fragment != null) return null
        return target
    }

    fun sameOriginUrl(origin: String, value: String): HttpUrl? {
        if (value.any { it.isISOControl() || it.isWhitespace() || it == '\\' }) return null
        val base = EndpointPolicy.normalizeOrigin(origin).toHttpUrlOrNull() ?: return null
        val target = base.resolve(value) ?: return null
        if (target.scheme != base.scheme || target.host != base.host || target.port != base.port) return null
        if (target.encodedUsername.isNotEmpty() || target.encodedPassword.isNotEmpty()) return null
        if (target.query != null || target.fragment != null) return null
        return target
    }

    fun leaseDeadline(startedAtMillis: Long, durationMillis: Long): Long? {
        if (startedAtMillis < 0L || durationMillis !in 1..MAX_LEASE_MILLIS) return null
        if (startedAtMillis > Long.MAX_VALUE - durationMillis) return null
        return startedAtMillis + durationMillis
    }

    fun isTransientMediaError(errorCode: Int, httpStatus: Int? = null, cause: Throwable? = null): Boolean {
        if (causeChain(cause).any { it is SSLException }) return false
        if (httpStatus != null) return httpStatus in 500..599 || httpStatus == 408 || httpStatus == 429
        return errorCode == 2000 || errorCode == 2001 || errorCode == 2002 || errorCode == 1003
    }

    fun recoveryDelayMillis(retry: Int): Long? = when (retry) {
        1 -> 250L
        2 -> 750L
        3 -> 1_500L
        else -> null
    }

    private fun causeChain(cause: Throwable?): Sequence<Throwable> = sequence {
        var current = cause
        val seen = HashSet<Throwable>()
        while (current != null && seen.add(current)) {
            yield(current)
            current = current.cause
        }
    }
}

internal class SequenceFence {
    var completedSequence: Long = 0
        private set
    var cancelBeforeSequence: Long = 0
        private set

    fun shouldExecute(sequence: Long): Boolean =
        sequence > completedSequence && sequence > cancelBeforeSequence

    fun complete(sequence: Long) {
        if (sequence > completedSequence) completedSequence = sequence
    }

    fun cancelBefore(sequence: Long): Boolean {
        if (sequence > cancelBeforeSequence) cancelBeforeSequence = sequence
        return completedSequence <= cancelBeforeSequence
    }
}
