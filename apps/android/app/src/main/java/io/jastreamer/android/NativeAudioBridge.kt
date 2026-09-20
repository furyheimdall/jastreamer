package io.jastreamer.android

import android.net.Uri
import android.os.Looper
import android.webkit.WebView
import androidx.annotation.MainThread
import androidx.webkit.JavaScriptReplyProxy
import androidx.webkit.WebMessageCompat
import androidx.webkit.WebViewCompat
import java.nio.charset.StandardCharsets
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.cancelChildren
import kotlinx.coroutines.launch
import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject

/** The deliberately narrow, origin-bound JavaScript surface for native phone playback. */
@MainThread
internal class NativeAudioBridge(
    private val webView: WebView,
    private val server: ServerEndpoint,
    private val canConnect: () -> Boolean,
) : WebViewCompat.WebMessageListener {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private var documentGeneration = 0L
    private var documentReady = false
    private var disposed = false
    private var unsubscribeState: (() -> Unit)? = null
    private val pendingConnectJobs = LinkedHashSet<Job>()
    private var hostInteractive = false

    @MainThread
    fun setHostInteractive(interactive: Boolean) {
        requireMainThread()
        if (disposed) return
        hostInteractive = interactive
        if (!interactive) cancelPendingConnects()
    }

    @MainThread
    fun documentStarted() {
        requireMainThread()
        if (disposed) return
        documentGeneration++
        documentReady = true
        scope.coroutineContext.cancelChildren()
        pendingConnectJobs.clear()
        unsubscribeState?.invoke()
        unsubscribeState = null
    }

    @MainThread
    fun documentCommitted() {
        requireMainThread()
        if (!disposed) documentReady = true
    }

    @MainThread
    fun invalidateDocument() {
        requireMainThread()
        if (disposed) return
        documentGeneration++
        documentReady = false
        scope.coroutineContext.cancelChildren()
        pendingConnectJobs.clear()
        unsubscribeState?.invoke()
        unsubscribeState = null
    }

    @MainThread
    fun dispose() {
        requireMainThread()
        if (disposed) return
        disposed = true
        documentGeneration++
        documentReady = false
        unsubscribeState?.invoke()
        unsubscribeState = null
        pendingConnectJobs.clear()
        scope.cancel()
    }

    override fun onPostMessage(
        view: WebView,
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
            reply(replyProxy, generation, errorResponse("", "invalid_request", "Request must be a string"))
            return
        }
        val raw = message.data ?: return
        if (raw.toByteArray(StandardCharsets.UTF_8).size > MAX_PAYLOAD_BYTES) {
            reply(replyProxy, generation, errorResponse("", "invalid_request", "Request is too large"))
            return
        }

        val request = try {
            parseRequest(raw)
        } catch (_: JSONException) {
            reply(replyProxy, generation, errorResponse("", "invalid_request", "Invalid native playback request"))
            return
        } catch (failure: RequestFailure) {
            reply(replyProxy, generation, errorResponse(failure.id, "invalid_request", failure.safeMessage))
            return
        }

        when (request.action) {
            Action.STATUS -> {
                try {
                    val state = NativePlayback.state(server)
                    if (!reply(replyProxy, generation, response(request.id, state))) return
                    observeState(generation, replyProxy)
                } catch (failure: NativePlaybackException) {
                    reply(replyProxy, generation, nativeFailure(request.id, failure.code, failure.message))
                } catch (_: Throwable) {
                    reply(replyProxy, generation, errorResponse(request.id, "unavailable", "Native playback is unavailable"))
                }
            }
            Action.CONNECT -> {
                if (!hostInteractive || !canConnect()) {
                    reply(
                        replyProxy,
                        generation,
                        errorResponse(request.id, "not_foreground", "Open the app to connect native playback"),
                    )
                    return
                }
                lateinit var job: Job
                job = scope.launch(start = CoroutineStart.LAZY) {
                    try {
                        val device = NativePlayback.connect(webView.context, server, requireNotNull(request.name))
                        if (hostInteractive && canConnect()) {
                            reply(replyProxy, generation, deviceResponse(request.id, device))
                        }
                    } catch (cancelled: CancellationException) {
                        throw cancelled
                    } catch (failure: NativePlaybackException) {
                        if (hostInteractive && canConnect()) {
                            reply(replyProxy, generation, nativeFailure(request.id, failure.code, failure.message))
                        }
                    } catch (_: Throwable) {
                        if (hostInteractive && canConnect()) {
                            reply(
                                replyProxy,
                                generation,
                                errorResponse(request.id, "unavailable", "Native playback is unavailable"),
                            )
                        }
                    } finally {
                        pendingConnectJobs.remove(job)
                    }
                }
                pendingConnectJobs.add(job)
                job.start()
            }
            Action.RENAME -> scope.launch {
                try {
                    val device = NativePlayback.rename(server, requireNotNull(request.name))
                    reply(replyProxy, generation, deviceResponse(request.id, device))
                } catch (cancelled: CancellationException) {
                    throw cancelled
                } catch (failure: NativePlaybackException) {
                    reply(replyProxy, generation, nativeFailure(request.id, failure.code, failure.message))
                } catch (_: Throwable) {
                    reply(replyProxy, generation, errorResponse(request.id, "unavailable", "Native playback is unavailable"))
                }
            }
        }
    }

    private fun observeState(generation: Long, replyProxy: JavaScriptReplyProxy) {
        unsubscribeState?.invoke()
        var initial = true
        unsubscribeState = NativePlayback.observe(server, listener@{ state ->
            if (initial) {
                initial = false
                return@listener
            }
            if (!isCurrent(generation)) return@listener
            post(replyProxy, stateEvent(state))
        })
    }

    private fun cancelPendingConnects() {
        pendingConnectJobs.toList().forEach(Job::cancel)
        pendingConnectJobs.clear()
    }

    private fun parseRequest(raw: String): Request {
        val value = JSONObject(raw)
        val id = value.opt("id") as? String
            ?: throw RequestFailure("", "Request id is required")
        if (id.isEmpty() || id.length > MAX_ID_CHARACTERS || id.any(Char::isISOControl)) {
            throw RequestFailure("", "Request id is invalid")
        }
        val actionValue = value.opt("action") as? String
            ?: throw RequestFailure(id, "Action is required")
        val action = when (actionValue) {
            "status" -> Action.STATUS
            "connect" -> Action.CONNECT
            "rename" -> Action.RENAME
            else -> throw RequestFailure(id, "Action is not supported")
        }
        val allowedKeys = if (action == Action.STATUS) STATUS_KEYS else NAMED_ACTION_KEYS
        val keys = value.keys()
        while (keys.hasNext()) {
            if (keys.next() !in allowedKeys) throw RequestFailure(id, "Request contains unsupported fields")
        }
        if (action == Action.STATUS) return Request(id, action, null)

        val rawName = value.opt("name") as? String
            ?: throw RequestFailure(id, "Name is required")
        val name = rawName.trim()
        if (
            name.isEmpty() ||
            name.any(Char::isISOControl) ||
            name.toByteArray(StandardCharsets.UTF_8).size > MAX_NAME_BYTES
        ) {
            throw RequestFailure(id, "Name is invalid")
        }
        return Request(id, action, name)
    }

    private fun response(id: String, state: JSONObject): JSONObject = JSONObject().apply {
        put("id", id)
        put("device", sanitizeDevice(state.optJSONObject("device")))
        if (state.optBoolean("recovering", false)) put("recovering", true)
        sanitizeError(state.optJSONObject("error"))?.let { put("error", it) }
    }

    private fun stateEvent(state: JSONObject): JSONObject = JSONObject().apply {
        put("event", "state")
        put("device", sanitizeDevice(state.optJSONObject("device")))
        put("recovering", state.optBoolean("recovering", false))
        sanitizeError(state.optJSONObject("error"))?.let { put("error", it) }
    }

    private fun deviceResponse(id: String, device: JSONObject): JSONObject = JSONObject().apply {
        put("id", id)
        put("device", sanitizeDevice(device))
    }

    private fun nativeFailure(id: String, code: String, message: String?): JSONObject = errorResponse(
        id,
        safeCode(code),
        message?.takeIf { it.isNotBlank() }?.take(MAX_ERROR_MESSAGE_CHARACTERS)
            ?: "Native playback request failed",
    )

    private fun errorResponse(id: String, code: String, message: String): JSONObject = JSONObject().apply {
        put("id", id)
        put("device", JSONObject.NULL)
        put("error", JSONObject().put("code", safeCode(code)).put("message", message.take(MAX_ERROR_MESSAGE_CHARACTERS)))
    }

    private fun sanitizeError(error: JSONObject?): JSONObject? {
        error ?: return null
        val code = error.optString("code").takeIf { it.isNotBlank() } ?: return null
        val message = error.optString("message").takeIf { it.isNotBlank() } ?: return null
        return JSONObject()
            .put("code", safeCode(code))
            .put("message", message.take(MAX_ERROR_MESSAGE_CHARACTERS))
    }

    private fun sanitizeDevice(device: JSONObject?): Any {
        device ?: return JSONObject.NULL
        val capabilities = device.optJSONObject("capabilities") ?: JSONObject()
        val protocolInfo = JSONArray()
        device.optJSONArray("protocol_info")?.let { values ->
            for (index in 0 until values.length()) {
                val value = values.opt(index)
                if (value is String) protocolInfo.put(value)
            }
        }
        return JSONObject().apply {
            put("id", device.optString("id"))
            put("name", device.optString("name"))
            put("protocol", device.optString("protocol"))
            put("manufacturer", device.optString("manufacturer"))
            put("model", device.optString("model"))
            put("address", device.optString("address"))
            put("online", device.optBoolean("online", false))
            put("last_seen", device.optString("last_seen"))
            put("capabilities", JSONObject().apply {
                put("play", capabilities.optBoolean("play", false))
                put("pause", capabilities.optBoolean("pause", false))
                put("stop", capabilities.optBoolean("stop", false))
                put("seek", capabilities.optBoolean("seek", false))
            })
            put("protocol_info", protocolInfo)
            put("pairing_required", device.optBoolean("pairing_required", false))
            put("password_required", device.optBoolean("password_required", false))
        }
    }

    private fun reply(proxy: JavaScriptReplyProxy, generation: Long, value: JSONObject): Boolean {
        if (!isCurrent(generation)) return false
        return post(proxy, value)
    }

    private fun post(proxy: JavaScriptReplyProxy, value: JSONObject): Boolean = try {
        proxy.postMessage(value.toString())
        true
    } catch (_: RuntimeException) {
        false
    }

    private fun isCurrent(generation: Long): Boolean =
        !disposed && documentReady && generation == documentGeneration

    private fun isExactOrigin(sourceOrigin: Uri): Boolean = try {
        EndpointPolicy.normalizeOrigin(sourceOrigin.toString()) == server.origin
    } catch (_: ClientException) {
        false
    }

    private fun safeCode(code: String): String =
        code.takeIf { SAFE_CODE.matches(it) } ?: "unavailable"

    private fun requireMainThread() {
        check(Looper.myLooper() == Looper.getMainLooper()) {
            "NativeAudioBridge must be used on the main thread"
        }
    }

    private enum class Action { STATUS, CONNECT, RENAME }

    private data class Request(val id: String, val action: Action, val name: String?)

    private class RequestFailure(val id: String, val safeMessage: String) : Exception()

    companion object {
        const val OBJECT_NAME = "JastreamerAndroidAudio"
        private const val MAX_PAYLOAD_BYTES = 4_096
        private const val MAX_ID_CHARACTERS = 80
        private const val MAX_NAME_BYTES = 80
        private const val MAX_ERROR_MESSAGE_CHARACTERS = 240
        private val STATUS_KEYS = setOf("id", "action")
        private val NAMED_ACTION_KEYS = setOf("id", "action", "name")
        private val SAFE_CODE = Regex("[a-z0-9_.-]{1,64}")
    }
}
