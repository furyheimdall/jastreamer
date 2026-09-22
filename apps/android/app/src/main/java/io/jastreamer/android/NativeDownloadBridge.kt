package io.jastreamer.android

import android.app.AlertDialog
import android.content.res.Configuration
import android.net.Uri
import android.os.Looper
import android.view.View
import android.widget.CheckBox
import android.widget.LinearLayout
import android.widget.RadioButton
import android.widget.RadioGroup
import android.widget.ScrollView
import android.widget.TextView
import androidx.annotation.MainThread
import androidx.webkit.JavaScriptReplyProxy
import androidx.webkit.WebMessageCompat
import androidx.webkit.WebViewCompat
import java.nio.charset.StandardCharsets
import java.util.Locale
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.cancelChildren
import kotlinx.coroutines.launch
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject
import kotlin.coroutines.resume

/** Narrow, origin-bound server document interface for explicit imports only. */
@MainThread
internal class NativeDownloadBridge(
    private val webView: android.webkit.WebView,
    private val server: ServerEndpoint,
    private val canDownload: () -> Boolean,
    private val onOpenLibrary: () -> Unit,
    private val onOpenDownloads: () -> Unit,
) : WebViewCompat.WebMessageListener {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private var documentGeneration = 0L
    private var documentReady = false
    private var hostInteractive = false
    private var disposed = false
    private var dialog: AlertDialog? = null
    private var confirmationPending = false
    private val documentJobs = LinkedHashSet<String>()

    fun setHostInteractive(interactive: Boolean) {
        requireMainThread()
        if (disposed) return
        hostInteractive = interactive
        if (!interactive) {
            dismissDialog()
            scope.coroutineContext.cancelChildren()
        }
    }

    fun documentStarted() {
        requireMainThread()
        if (disposed) return
        documentGeneration++
        documentReady = true
        documentJobs.clear()
        confirmationPending = false
        dismissDialog()
        scope.coroutineContext.cancelChildren()
    }

    fun documentCommitted() {
        requireMainThread()
        if (!disposed) documentReady = true
    }

    fun invalidateDocument() {
        requireMainThread()
        if (disposed) return
        documentGeneration++
        documentReady = false
        documentJobs.clear()
        confirmationPending = false
        dismissDialog()
        scope.coroutineContext.cancelChildren()
    }

    fun dispose() {
        requireMainThread()
        if (disposed) return
        disposed = true
        documentGeneration++
        documentReady = false
        documentJobs.clear()
        confirmationPending = false
        dismissDialog()
        scope.cancel()
    }

    override fun onPostMessage(
        view: android.webkit.WebView,
        message: WebMessageCompat,
        sourceOrigin: Uri,
        isMainFrame: Boolean,
        replyProxy: JavaScriptReplyProxy,
    ) {
        requireMainThread()
        if (
            disposed ||
            view !== webView ||
            !isMainFrame ||
            !documentReady ||
            !isExactOrigin(sourceOrigin)
        ) return
        val generation = documentGeneration
        if (message.type != WebMessageCompat.TYPE_STRING) {
            reply(replyProxy, generation, error("", "invalid_request", "Request must be a string"))
            return
        }
        val raw = message.data ?: return
        if (raw.toByteArray(StandardCharsets.UTF_8).size > MAX_PAYLOAD_BYTES) {
            reply(replyProxy, generation, error("", "invalid_request", "Request is too large"))
            return
        }
        val request = try {
            parseRequest(raw)
        } catch (_: JSONException) {
            reply(replyProxy, generation, error("", "invalid_request", "Invalid download request"))
            return
        } catch (failure: RequestFailure) {
            reply(replyProxy, generation, error(failure.id, "invalid_request", failure.safeMessage))
            return
        }
        when (request.action) {
            Action.CAPABILITIES -> reply(
                replyProxy,
                generation,
                result(request.id, JSONObject().put("version", 1)),
            )
            Action.STATUS -> {
                val permitted = (request.jobIds ?: documentJobs).filterTo(LinkedHashSet()) { it in documentJobs }
                val values = JSONArray()
                OfflineDownloads.jobValues(webView.context, permitted).forEach { values.put(statusValue(it)) }
                reply(replyProxy, generation, result(request.id, JSONObject().put("jobs", values)))
            }
            Action.LOGOUT -> {
                OfflineDownloads.logout(webView.context, server)
                documentJobs.clear()
                reply(replyProxy, generation, result(request.id, JSONObject()))
                confirmationPending = false
                scope.coroutineContext.cancelChildren()
                dismissDialog()
            }
            Action.OPEN_LIBRARY -> {
                if (!hostInteractive || !canDownload()) {
                    reply(
                        replyProxy,
                        generation,
                        error(request.id, "not_foreground", "Open the app to view saved music"),
                    )
                } else if (reply(replyProxy, generation, result(request.id, JSONObject()))) {
                    webView.post {
                        if (isCurrent(generation) && hostInteractive && canDownload()) onOpenLibrary()
                    }
                }
            }
            Action.OPEN_DOWNLOADS -> {
                if (!hostInteractive || !canDownload()) {
                    reply(
                        replyProxy,
                        generation,
                        error(request.id, "not_foreground", "Open the app to view downloads"),
                    )
                } else if (reply(replyProxy, generation, result(request.id, JSONObject()))) {
                    webView.post {
                        if (isCurrent(generation) && hostInteractive && canDownload()) onOpenDownloads()
                    }
                }
            }
            Action.CONFIGURE_NETWORK -> configureNetwork(request, generation, replyProxy)
            Action.DOWNLOAD -> startDownload(request, generation, replyProxy)
        }
    }

    private fun startDownload(request: Request, generation: Long, replyProxy: JavaScriptReplyProxy) {
        if (!hostInteractive || !canDownload()) {
            reply(replyProxy, generation, error(request.id, "not_foreground", "Open the app to confirm the download"))
            return
        }
        if (confirmationPending) {
            reply(replyProxy, generation, error(request.id, "busy", "Finish the current download confirmation first"))
            return
        }
        confirmationPending = true
        scope.launch {
            try {
                val target = requireNotNull(request.target)
                val quality = requireNotNull(request.quality)
                val initialNetwork = OfflineDownloads.networkState(webView.context)
                if (
                    initialNetwork.errorCode == "network_roaming" ||
                    initialNetwork.errorCode == "network_unavailable"
                ) {
                    throw OfflineDownloadException(
                        requireNotNull(initialNetwork.errorCode),
                        requireNotNull(initialNetwork.errorMessage),
                    )
                }
                if (initialNetwork.errorCode == "network_unmetered_required") {
                    val allowed = confirmMeteredAccess(initialNetwork, generation)
                    if (!allowed) throw OfflineDownloadException("cancelled", "Download was not confirmed")
                    if (!isCurrent(generation) || !hostInteractive || !canDownload()) return@launch
                    OfflineDownloads.setAllowMetered(webView.context, true)
                }
                val previewNetwork = OfflineDownloads.networkState(webView.context)
                if (!previewNetwork.allowed) {
                    throw OfflineDownloadException(
                        requireNotNull(previewNetwork.errorCode),
                        requireNotNull(previewNetwork.errorMessage),
                    )
                }
                val preview = OfflineDownloads.preview(webView.context, server, target)
                if (!isCurrent(generation) || !hostInteractive || !canDownload()) return@launch
                val selection = chooseDestination(
                    preview,
                    quality,
                    generation,
                    OfflineDownloads.networkState(webView.context),
                ) ?: throw OfflineDownloadException("cancelled", "Download was not confirmed")
                if (!isCurrent(generation) || !hostInteractive || !canDownload()) return@launch
                if (selection.allowMetered) OfflineDownloads.setAllowMetered(webView.context, true)
                val network = OfflineDownloads.networkState(webView.context)
                if (!network.allowed) {
                    throw OfflineDownloadException(
                        requireNotNull(network.errorCode),
                        requireNotNull(network.errorMessage),
                    )
                }
                val jobId = OfflineDownloads.enqueue(webView.context, server, target, quality, selection.folderId)
                if (!isCurrent(generation)) return@launch
                documentJobs.add(jobId)
                reply(replyProxy, generation, result(request.id, JSONObject().put("job_id", jobId)))
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (failure: OfflineDownloadException) {
                reply(replyProxy, generation, error(request.id, safeCode(failure.code), failure.message))
            } catch (_: Throwable) {
                reply(replyProxy, generation, error(request.id, "unavailable", "Download could not be started"))
            } finally {
                confirmationPending = false
            }
        }
    }

    private fun configureNetwork(request: Request, generation: Long, replyProxy: JavaScriptReplyProxy) {
        val jobId = requireNotNull(request.jobId)
        if (jobId !in documentJobs) {
            reply(replyProxy, generation, error(request.id, "invalid_request", "The download is not available to this page"))
            return
        }
        if (!hostInteractive || !canDownload()) {
            reply(replyProxy, generation, error(request.id, "not_foreground", "Open the app to change download network access"))
            return
        }
        if (confirmationPending) {
            reply(replyProxy, generation, error(request.id, "busy", "Finish the current download confirmation first"))
            return
        }
        confirmationPending = true
        scope.launch {
            try {
                val confirmed = confirmMeteredAccess(
                    OfflineDownloads.networkState(webView.context),
                    generation,
                )
                if (!confirmed) {
                    if (isCurrent(generation) && hostInteractive && canDownload() && jobId in documentJobs) {
                        reply(replyProxy, generation, result(request.id, JSONObject()))
                    }
                    return@launch
                }
                if (!isCurrent(generation) || !hostInteractive || !canDownload() || jobId !in documentJobs) return@launch
                OfflineDownloads.setAllowMetered(webView.context, true)
                if (!isCurrent(generation) || jobId !in documentJobs) return@launch
                reply(replyProxy, generation, result(request.id, JSONObject()))
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (failure: OfflineDownloadException) {
                reply(replyProxy, generation, error(request.id, safeCode(failure.code), failure.message))
            } catch (_: Throwable) {
                reply(replyProxy, generation, error(request.id, "unavailable", "Download network access could not be changed"))
            } finally {
                confirmationPending = false
            }
        }
    }

    private suspend fun confirmMeteredAccess(network: OfflineNetworkState, generation: Long): Boolean {
        val strings = localizedStrings()
        return suspendCancellableCoroutine { continuation ->
            if (!isCurrent(generation) || !hostInteractive || !canDownload()) {
                continuation.resume(false)
                return@suspendCancellableCoroutine
            }
            val density = webView.resources.displayMetrics.density
            val padding = (20 * density).toInt()
            val content = TextView(webView.context).apply {
                text = strings.meteredConfirmation(network)
                textSize = 16f
                setPadding(padding, padding / 2, padding, padding / 2)
                importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_YES
            }
            var completed = false
            val candidate = AlertDialog.Builder(webView.context)
                .setTitle(strings.meteredTitle)
                .setView(ScrollView(webView.context).apply { addView(content) })
                .setNegativeButton(strings.cancel) { _, _ ->
                    if (!completed && continuation.isActive) {
                        completed = true
                        continuation.resume(false)
                    }
                }
                .setPositiveButton(strings.allowMetered) { _, _ ->
                    if (!completed && continuation.isActive) {
                        completed = true
                        continuation.resume(true)
                    }
                }
                .setOnCancelListener {
                    if (!completed && continuation.isActive) {
                        completed = true
                        continuation.resume(false)
                    }
                }
                .create()
            dialog = candidate
            continuation.invokeOnCancellation {
                webView.post { if (dialog === candidate) candidate.dismiss() }
            }
            candidate.setOnDismissListener {
                if (dialog === candidate) dialog = null
                if (!completed && continuation.isActive) {
                    completed = true
                    continuation.resume(false)
                }
            }
            candidate.show()
        }
    }

    private suspend fun chooseDestination(
        preview: DownloadPreview,
        quality: String,
        generation: Long,
        network: OfflineNetworkState,
    ): DestinationSelection? {
        val strings = localizedStrings()
        val folders = withContext(Dispatchers.IO) {
            val library = OfflineLibrary.get(webView.context)
            buildList {
                fun addChildren(parent: String, prefix: String, depth: Int) {
                    if (size >= MAX_FOLDER_CHOICES || depth > MAX_FOLDER_DEPTH) return
                    library.folders(parent).forEach { folder ->
                        if (size >= MAX_FOLDER_CHOICES) return@forEach
                        val name = if (folder.id == OfflineLibrary.IMPORT_FOLDER_ID) {
                            strings.importedFolder
                        } else {
                            folder.name
                        }
                        add(folder to "$prefix$name")
                        addChildren(folder.id, "$prefix$name / ", depth + 1)
                    }
                }
                library.folder(OfflineLibrary.ROOT_FOLDER_ID)?.let { root ->
                    add(root to strings.rootFolder)
                }
                addChildren(OfflineLibrary.ROOT_FOLDER_ID, "", 0)
            }
        }
        if (folders.isEmpty()) throw OfflineDownloadException("destination_missing", "No download folder is available")
        val initial = folders.indexOfFirst { it.first.id == OfflineLibrary.IMPORT_FOLDER_ID }.coerceAtLeast(0)
        return suspendCancellableCoroutine { continuation ->
            if (!isCurrent(generation) || !hostInteractive || !canDownload()) {
                continuation.resume(null)
                return@suspendCancellableCoroutine
            }
            val density = webView.resources.displayMetrics.density
            val padding = (20 * density).toInt()
            val content = LinearLayout(webView.context).apply {
                orientation = LinearLayout.VERTICAL
                setPadding(padding, padding / 2, padding, padding / 2)
            }
            content.addView(TextView(webView.context).apply {
                text = strings.summary(preview.title, preview.trackCount, quality)
                textSize = 16f
                importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_YES
            })
            content.addView(TextView(webView.context).apply {
                text = strings.networkStatus(network)
                setPadding(0, padding / 2, 0, 0)
                importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_YES
            })
            val meteredChoice = if (network.errorCode == "network_unmetered_required") {
                CheckBox(webView.context).apply {
                    text = strings.allowMetered
                    minHeight = (48 * density).toInt()
                }.also(content::addView)
            } else {
                null
            }
            content.addView(TextView(webView.context).apply {
                text = strings.chooseFolder
                setPadding(0, padding / 2, 0, padding / 4)
            })
            val group = RadioGroup(webView.context).apply { orientation = RadioGroup.VERTICAL }
            folders.forEachIndexed { index, (_, label) ->
                group.addView(RadioButton(webView.context).apply {
                    id = View.generateViewId()
                    text = label
                    tag = index
                    isChecked = index == initial
                    minHeight = (48 * density).toInt()
                })
            }
            content.addView(group)
            var completed = false
            val candidate = AlertDialog.Builder(webView.context)
                .setTitle(strings.title)
                .setView(ScrollView(webView.context).apply {
                    isFillViewport = true
                    addView(content)
                })
                .setNegativeButton(strings.cancel) { _, _ ->
                    if (!completed && continuation.isActive) {
                        completed = true
                        continuation.resume(null)
                    }
                }
                .setPositiveButton(strings.download) { _, _ ->
                    if (!completed && continuation.isActive) {
                        completed = true
                        val selected = group.findViewById<RadioButton>(group.checkedRadioButtonId)
                        val index = selected?.tag as? Int ?: initial
                        continuation.resume(
                            DestinationSelection(
                                folders[index].first.id,
                                meteredChoice?.isChecked == true,
                            ),
                        )
                    }
                }
                .setOnCancelListener {
                    if (!completed && continuation.isActive) {
                        completed = true
                        continuation.resume(null)
                    }
                }
                .create()
            dialog = candidate
            continuation.invokeOnCancellation {
                webView.post { if (dialog === candidate) candidate.dismiss() }
            }
            candidate.setOnDismissListener {
                if (dialog === candidate) dialog = null
                if (!completed && continuation.isActive) {
                    completed = true
                    continuation.resume(null)
                }
            }
            candidate.show()
        }
    }

    private fun localizedStrings(): DialogStrings {
        val language = try {
            RecentServerStore(webView.context).language()
        } catch (_: Exception) {
            "en"
        }
        val configuration = Configuration(webView.resources.configuration).apply {
            setLocale(Locale.forLanguageTag(if (language == "ko") "ko" else "en"))
        }
        val context = webView.context.createConfigurationContext(configuration)
        fun networkStatus(state: OfflineNetworkState): String = context.getString(
            when {
                state.errorCode == "network_unavailable" -> R.string.offline_download_network_unavailable
                state.errorCode == "network_roaming" -> R.string.offline_download_network_roaming
                state.errorCode == "network_unmetered_required" -> R.string.offline_download_network_metered_blocked
                state.metered -> R.string.offline_download_network_metered_allowed
                else -> R.string.offline_download_network_unmetered_available
            },
        )
        return DialogStrings(
            title = context.getString(R.string.offline_download_confirm_title),
            chooseFolder = context.getString(R.string.offline_download_choose_folder),
            download = context.getString(R.string.offline_download_confirm),
            cancel = context.getString(android.R.string.cancel),
            importedFolder = context.getString(R.string.offline_download_imported_folder),
            rootFolder = context.getString(R.string.offline_download_root_folder),
            allowMetered = context.getString(R.string.offline_download_allow_metered),
            meteredTitle = context.getString(R.string.offline_download_network_confirm_title),
            networkStatus = ::networkStatus,
            meteredConfirmation = { state ->
                context.getString(
                    R.string.offline_download_network_confirm_summary,
                    networkStatus(state),
                    context.getString(R.string.offline_download_network_roaming_guard),
                )
            },
            summary = { title, count, quality ->
                context.getString(
                    R.string.offline_download_confirm_summary,
                    title,
                    context.resources.getQuantityString(R.plurals.offline_download_track_count, count, count),
                    if (quality == "original") context.getString(R.string.offline_download_quality_original)
                    else context.getString(R.string.offline_download_quality_aac),
                )
            },
        )
    }

    private fun parseRequest(raw: String): Request {
        val value = JSONObject(raw)
        val id = value.opt("id") as? String ?: throw RequestFailure("", "Request id is required")
        if (id.isEmpty() || id.length > MAX_ID_CHARACTERS || id.any(Char::isISOControl)) {
            throw RequestFailure("", "Request id is invalid")
        }
        val action = when (value.opt("action") as? String) {
            "capabilities" -> Action.CAPABILITIES
            "download" -> Action.DOWNLOAD
            "status" -> Action.STATUS
            "logout" -> Action.LOGOUT
            "open_library" -> Action.OPEN_LIBRARY
            "configure_network" -> Action.CONFIGURE_NETWORK
            "open_downloads" -> Action.OPEN_DOWNLOADS
            null -> throw RequestFailure(id, "Action is required")
            else -> throw RequestFailure(id, "Action is not supported")
        }
        val allowed = when (action) {
            Action.DOWNLOAD -> DOWNLOAD_KEYS
            Action.STATUS -> STATUS_KEYS
            Action.CONFIGURE_NETWORK -> CONFIGURE_NETWORK_KEYS
            else -> BASIC_KEYS
        }
        value.keys().forEach { if (it !in allowed) throw RequestFailure(id, "Request contains unsupported fields") }
        return when (action) {
            Action.DOWNLOAD -> {
                val target = value.optJSONObject("target") ?: throw RequestFailure(id, "Download target is required")
                try {
                    OfflineTransferPolicy.requireTarget(target)
                } catch (failure: OfflineDownloadException) {
                    throw RequestFailure(id, failure.message)
                }
                val quality = value.opt("quality") as? String ?: throw RequestFailure(id, "Quality is required")
                try {
                    OfflineTransferPolicy.requireQuality(quality)
                } catch (failure: OfflineDownloadException) {
                    throw RequestFailure(id, failure.message)
                }
                Request(id, action, JSONObject(target.toString()), quality, null, null)
            }
            Action.STATUS -> {
                if (value.has("job_ids") && value.optJSONArray("job_ids") == null) {
                    throw RequestFailure(id, "Job ids must be an array")
                }
                val ids = value.optJSONArray("job_ids")?.let { array ->
                    if (array.length() > MAX_STATUS_IDS) throw RequestFailure(id, "Too many job ids")
                    LinkedHashSet<String>().apply {
                        for (index in 0 until array.length()) {
                            val jobId = array.opt(index) as? String ?: throw RequestFailure(id, "Job id is invalid")
                            if (jobId.isBlank() || jobId.length > MAX_JOB_ID_CHARACTERS || jobId.any(Char::isISOControl)) {
                                throw RequestFailure(id, "Job id is invalid")
                            }
                            add(jobId)
                        }
                    }
                }
                Request(id, action, null, null, ids, null)
            }
            Action.CONFIGURE_NETWORK -> {
                val jobId = value.opt("job_id") as? String ?: throw RequestFailure(id, "Job id is required")
                if (jobId.isBlank() || jobId.length > MAX_JOB_ID_CHARACTERS || jobId.any(Char::isISOControl)) {
                    throw RequestFailure(id, "Job id is invalid")
                }
                Request(id, action, null, null, null, jobId)
            }
            else -> Request(id, action, null, null, null, null)
        }
    }

    private fun statusValue(job: OfflineDownloadJob): JSONObject = JSONObject().apply {
        put("id", job.id)
        put("status", job.status)
        put("completed_tracks", job.completedTracks)
        put("total_tracks", job.totalTracks)
        put("failed_tracks", job.failedTracks)
        put("received_bytes", job.receivedBytes)
        put("total_bytes", job.totalBytes)
        if (!job.errorCode.isNullOrBlank() && !job.errorMessage.isNullOrBlank()) {
            put("error", JSONObject().put("code", safeCode(job.errorCode)).put("message", job.errorMessage.take(MAX_ERROR_CHARACTERS)))
        }
    }

    private fun result(id: String, value: JSONObject): JSONObject = JSONObject().put("id", id).put("result", value)

    private fun error(id: String, code: String, message: String): JSONObject = JSONObject()
        .put("id", id)
        .put("error", JSONObject().put("code", safeCode(code)).put("message", message.take(MAX_ERROR_CHARACTERS)))

    private fun reply(proxy: JavaScriptReplyProxy, generation: Long, value: JSONObject): Boolean {
        if (!isCurrent(generation)) return false
        return try {
            proxy.postMessage(value.toString())
            true
        } catch (_: RuntimeException) {
            false
        }
    }

    private fun dismissDialog() {
        dialog?.dismiss()
        dialog = null
    }

    private fun isCurrent(generation: Long): Boolean = !disposed && documentReady && generation == documentGeneration

    private fun isExactOrigin(sourceOrigin: Uri): Boolean = try {
        EndpointPolicy.normalizeOrigin(sourceOrigin.toString()) == server.origin
    } catch (_: ClientException) {
        false
    }

    private fun safeCode(value: String): String = value.takeIf(SAFE_CODE::matches) ?: "unavailable"

    private fun requireMainThread() {
        check(Looper.myLooper() == Looper.getMainLooper()) { "NativeDownloadBridge must be used on the main thread" }
    }

    private data class DialogStrings(
        val title: String,
        val chooseFolder: String,
        val download: String,
        val cancel: String,
        val importedFolder: String,
        val rootFolder: String,
        val allowMetered: String,
        val meteredTitle: String,
        val networkStatus: (OfflineNetworkState) -> String,
        val meteredConfirmation: (OfflineNetworkState) -> String,
        val summary: (String, Int, String) -> String,
    )

    private data class DestinationSelection(val folderId: String, val allowMetered: Boolean)

    private data class Request(
        val id: String,
        val action: Action,
        val target: JSONObject?,
        val quality: String?,
        val jobIds: Set<String>?,
        val jobId: String?,
    )

    private class RequestFailure(val id: String, val safeMessage: String) : Exception()
    private enum class Action {
        CAPABILITIES,
        DOWNLOAD,
        STATUS,
        LOGOUT,
        OPEN_LIBRARY,
        CONFIGURE_NETWORK,
        OPEN_DOWNLOADS,
    }

    companion object {
        const val OBJECT_NAME = "JastreamerDownloads"
        private const val MAX_PAYLOAD_BYTES = 16 * 1024
        private const val MAX_ID_CHARACTERS = 80
        private const val MAX_JOB_ID_CHARACTERS = 100
        private const val MAX_STATUS_IDS = 100
        private const val MAX_ERROR_CHARACTERS = 240
        private const val MAX_FOLDER_CHOICES = 200
        private const val MAX_FOLDER_DEPTH = 20
        private val BASIC_KEYS = setOf("id", "action")
        private val DOWNLOAD_KEYS = setOf("id", "action", "target", "quality")
        private val STATUS_KEYS = setOf("id", "action", "job_ids")
        private val CONFIGURE_NETWORK_KEYS = setOf("id", "action", "job_id")
        private val SAFE_CODE = Regex("[a-z0-9_.-]{1,64}")
    }
}
