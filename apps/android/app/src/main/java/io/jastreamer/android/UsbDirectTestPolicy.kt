package io.jastreamer.android

/**
 * The pure rules of the debug-only USB direct test: which phase may follow which, when the DAC may
 * be claimed, how long the Server source may take, and how a failed HTTP response is reported.
 *
 * The DAC belongs to Android until the very last moment. A play-grant media URL is revoked the
 * moment Server playback stops, so the test sources its bytes from the stable per-track download
 * artifact instead, and only takes the device once those bytes decode.
 */
internal object UsbDirectTestPolicy {
    const val PHASE_IDLE = "idle"

    /** Preparing and opening the Server source; nothing has touched the USB device yet. */
    const val PHASE_SOURCE = "opening_source"

    /** Claiming the DAC. Only reachable once the source is open and audio has decoded. */
    const val PHASE_DEVICE = "opening"
    const val PHASE_PLAYING = "playing"
    const val PHASE_ERROR = "error"

    /** The whole ordered run, shortest first. Every phase may also fall back to idle or error. */
    private val ORDER = listOf(PHASE_IDLE, PHASE_SOURCE, PHASE_DEVICE, PHASE_PLAYING)

    val phases: Set<String> = setOf(PHASE_IDLE, PHASE_SOURCE, PHASE_DEVICE, PHASE_PLAYING, PHASE_ERROR)

    /** True while the test owns the run and a second press must be refused. */
    fun busy(phase: String): Boolean = phase == PHASE_SOURCE || phase == PHASE_DEVICE || phase == PHASE_PLAYING

    /**
     * Returns [next] when it may follow [current]. Idle and error are always reachable (Stop and
     * failures end a run from anywhere); forward progress may never skip a phase or run backwards.
     */
    fun requireTransition(current: String, next: String): String {
        require(current in phases) { "unknown phase $current" }
        require(next in phases) { "unknown phase $next" }
        if (next == PHASE_ERROR || next == PHASE_IDLE) return next
        if (current == PHASE_ERROR) {
            throw UsbDirectException("usb_direct_order", "A failed USB direct run must be reset before it can continue.")
        }
        val from = ORDER.indexOf(current)
        val to = ORDER.indexOf(next)
        if (to != from + 1) {
            throw UsbDirectException(
                "usb_direct_order",
                "The USB direct test cannot move from $current to $next.",
            )
        }
        return next
    }

    /**
     * Guards the one ordering rule that matters for the user: the DAC is never taken away from
     * Android speculatively. The source must be open and the decoder must have produced audio.
     */
    fun requireClaimAllowed(sourceOpen: Boolean, decodedBytes: Long) {
        if (!sourceOpen) {
            throw UsbDirectException(
                "usb_direct_order",
                "The USB direct test may not open the DAC before the Server track is open.",
            )
        }
        if (decodedBytes <= 0L) {
            throw UsbDirectException(
                "usb_direct_order",
                "The USB direct test may not open the DAC before the track has decoded.",
            )
        }
    }

    /** Reports a source failure, naming the HTTP status whenever the Server answered with one. */
    fun sourceFailure(status: Int?, detail: String? = null): Pair<String, String> {
        val reason = detail?.trim()?.takeIf(String::isNotEmpty)
        val explanation = reason ?: "The Server track could not be opened for decoding."
        val known = status != null && status in 100..599
        // The transfer client already words its own failures with the status; do not repeat it.
        val message = if (known && !explanation.contains("(HTTP ")) "$explanation (HTTP $status)" else explanation
        val code = when (status) {
            401, 403 -> "usb_source_unauthorized"
            404, 410 -> "usb_source_missing"
            else -> "usb_source_failed"
        }
        return code to message
    }

    /** True once preparing the download artifact has taken longer than a test should wait. */
    fun prepareExpired(elapsedMillis: Long): Boolean = elapsedMillis >= PREPARE_TIMEOUT_MILLIS

    /** Polls quickly at first and then settles, so a short track is ready almost immediately. */
    fun preparePollDelayMillis(attempt: Int): Long {
        if (attempt <= 0) return POLL_MINIMUM_MILLIS
        val scaled = POLL_MINIMUM_MILLIS shl minOf(attempt, 5)
        return minOf(scaled, POLL_MAXIMUM_MILLIS)
    }

    /** Resolving the track and creating plus polling the artifact, end to end. */
    const val PREPARE_TIMEOUT_MILLIS = 120_000L

    /** Handing the prepared URL to `MediaExtractor`, which offers no timeout of its own. */
    const val OPEN_TIMEOUT_MILLIS = 30_000L

    /** Asking the Server which track is loaded. */
    const val RESOLVE_TIMEOUT_MILLIS = 15_000L

    private const val POLL_MINIMUM_MILLIS = 250L
    private const val POLL_MAXIMUM_MILLIS = 2_000L
}
