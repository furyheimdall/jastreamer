package io.jastreamer.android

import android.os.SystemClock
import kotlinx.coroutines.delay
import kotlinx.coroutines.withTimeout

/** A prepared, authenticated per-track URL the decoder can read until [release] is called. */
internal data class UsbDirectSourceHandle(
    val url: String,
    val headers: Map<String, String>,
    val mime: String,
    val byteSize: Long,
)

/**
 * Resolves the bytes of one Server track for the debug USB direct test.
 *
 * Playback media URLs are scoped to a play grant, which the Server revokes the instant playback
 * stops — exactly the state the test requires — so they 404. This uses the same per-track download
 * artifact that Android's Saved music import already uses: authenticated with the profile cookies,
 * owned by the signed-in account, and unaffected by transport state. The prepared artifact is
 * deleted again in [release], so running the test leaves no download behind.
 */
internal class UsbDirectSource(server: ServerEndpoint) {
    private val origin = EndpointPolicy.normalizeOrigin(server.origin)
    private val client = OfflineTransferClient(server)
    private var jobId: String? = null
    private var principal: String? = null

    /**
     * Prepares [trackId] and returns a URL the decoder can open. Bounded end to end: a Server that
     * never finishes preparing fails the test instead of hanging the panel on "opening".
     */
    suspend fun open(trackId: String, shouldContinue: () -> Boolean): UsbDirectSourceHandle {
        val startedAt = SystemClock.elapsedRealtime()
        val owner = try {
            client.principal()
        } catch (failure: OfflineDownloadException) {
            throw sourceFailure(failure)
        }
        principal = owner
        val created = try {
            client.create("track", trackId, QUALITY, owner, shouldContinue)
        } catch (failure: OfflineDownloadException) {
            throw sourceFailure(failure)
        }
        jobId = created.remoteId

        var manifest = created
        var attempt = 0
        while (manifest.status == "preparing" || manifest.tracks.firstOrNull()?.status.let { it == "pending" || it == "preparing" }) {
            if (!shouldContinue()) throw UsbDirectException("usb_direct_stopped", "The USB direct test was stopped.")
            if (UsbDirectTestPolicy.prepareExpired(SystemClock.elapsedRealtime() - startedAt)) {
                throw UsbDirectException(
                    "usb_source_timeout",
                    "The Server did not finish preparing this track in time.",
                )
            }
            delay(UsbDirectTestPolicy.preparePollDelayMillis(attempt))
            attempt++
            manifest = try {
                client.poll(created.remoteId, "track", QUALITY, owner, shouldContinue)
            } catch (failure: OfflineDownloadException) {
                throw sourceFailure(failure)
            }
        }

        val item = manifest.tracks.firstOrNull()
            ?: throw UsbDirectException("usb_source_failed", "The Server prepared no file for this track.")
        if (item.status != "ready" || item.mediaPath.isEmpty()) {
            val detail = item.errorMessage ?: "The Server could not prepare this track."
            throw UsbDirectException(item.errorCode?.let { "usb_source_$it" } ?: "usb_source_failed", detail)
        }
        val authorization = try {
            client.requirePrincipal(owner)
        } catch (failure: OfflineDownloadException) {
            throw sourceFailure(failure)
        }
        val url = try {
            OfflineTransferPolicy.fileUrl(origin, created.remoteId, item.index, item.mediaPath)
        } catch (failure: OfflineDownloadException) {
            throw sourceFailure(failure)
        }
        // Prove the URL actually serves before `MediaExtractor` swallows the status code.
        try {
            withTimeout(UsbDirectTestPolicy.OPEN_TIMEOUT_MILLIS) {
                client.probeFile(url, authorization, item.byteSize, item.mime)
            }
        } catch (failure: OfflineDownloadException) {
            throw sourceFailure(failure)
        }
        return UsbDirectSourceHandle(
            url = url.toString(),
            headers = if (authorization.isBlank()) emptyMap() else mapOf("Cookie" to authorization),
            mime = item.mime,
            byteSize = item.byteSize,
        )
    }

    /** Deletes the prepared artifact so the test never shows up as a download the user kept. */
    suspend fun release() {
        val id = jobId ?: return
        val owner = principal
        jobId = null
        principal = null
        if (owner == null) return
        try {
            client.cancel(id, owner) { true }
        } catch (_: OfflineDownloadException) {
            // The artifact expires on the Server by itself; a failed cleanup must not fail the test.
        } catch (_: java.io.IOException) {
            // Same for a transport that dropped while the DAC was already handed back.
        }
    }

    private fun sourceFailure(failure: OfflineDownloadException): UsbDirectException {
        val (code, message) = UsbDirectTestPolicy.sourceFailure(failure.httpStatus, failure.message)
        return UsbDirectException(code, message)
    }

    private companion object {
        /** The original file, byte for byte: a transcode would defeat the point of the test. */
        const val QUALITY = "original"
    }
}
