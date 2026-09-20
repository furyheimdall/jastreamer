package io.jastreamer.android

import android.content.Context
import android.content.Intent
import android.os.Looper
import androidx.annotation.MainThread
import java.util.LinkedHashMap
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withTimeout
import org.json.JSONObject

class NativePlaybackException(
    val code: String,
    message: String,
    cause: Throwable? = null,
) : Exception(message, cause)

object NativePlayback {
    @MainThread
    suspend fun connect(context: Context, server: ServerEndpoint, name: String): JSONObject {
        requireMainThread()
        val safeName = NativePlaybackPolicy.requireName(name)
        val verified = try {
            ServerProbe().probe(server.origin, server.id)
        } catch (error: ClientException) {
            throw error.asNativePlaybackException()
        }
        val normalizedServer = server.copy(id = verified.id, origin = verified.origin)
        val appContext = context.applicationContext
        try {
            appContext.startService(Intent(appContext, NativePlaybackService::class.java))
        } catch (error: RuntimeException) {
            throw NativePlaybackException("service_unavailable", "Phone playback is unavailable.", error)
        }
        val service = try {
            withTimeout(SERVICE_START_TIMEOUT_MILLIS) { NativePlaybackRegistry.awaitService() }
        } catch (error: TimeoutCancellationException) {
            throw NativePlaybackException("service_unavailable", "Phone playback is unavailable.", error)
        }
        return service.connect(normalizedServer, safeName)
    }

    @MainThread
    suspend fun rename(server: ServerEndpoint, name: String): JSONObject {
        requireMainThread()
        val service = NativePlaybackRegistry.service
            ?: throw NativePlaybackException("not_connected", "Phone playback is not connected.")
        return service.rename(server, NativePlaybackPolicy.requireName(name))
    }

    @MainThread
    fun state(server: ServerEndpoint): JSONObject {
        requireMainThread()
        return NativePlaybackRegistry.service?.state(server) ?: emptyState()
    }

    @MainThread
    fun observe(server: ServerEndpoint, listener: (JSONObject) -> Unit): () -> Unit {
        requireMainThread()
        return NativePlaybackRegistry.observe(server, listener)
    }

    internal fun emptyState(): JSONObject = JSONObject()
        .put("device", JSONObject.NULL)
        .put("recovering", false)

    private fun requireMainThread() {
        check(Looper.myLooper() == Looper.getMainLooper()) { "NativePlayback must be used on the main thread" }
    }

    private fun ClientException.asNativePlaybackException(): NativePlaybackException {
        val pair = when (code) {
            ClientErrorCode.INVALID_ENDPOINT -> "invalid_server" to "The server address is invalid."
            ClientErrorCode.TIMEOUT -> "server_timeout" to "The server did not respond in time."
            ClientErrorCode.TLS -> "server_tls" to "The server certificate could not be verified."
            ClientErrorCode.IDENTITY_MISMATCH -> "server_identity" to "The server identity changed."
            ClientErrorCode.WEBVIEW_UNSUPPORTED -> "profile_unavailable" to "The isolated server profile is unavailable."
            else -> "server_unavailable" to "The server could not be verified."
        }
        return NativePlaybackException(pair.first, pair.second, this)
    }

    private const val SERVICE_START_TIMEOUT_MILLIS = 5_000L
}

internal object NativePlaybackRegistry {
    var service: NativePlaybackService? = null
        private set

    private val waiting = LinkedHashMap<Long, kotlin.coroutines.Continuation<NativePlaybackService>>()
    private val observers = LinkedHashMap<Long, Pair<ServerEndpoint, (JSONObject) -> Unit>>()
    private var nextId = 1L

    fun attach(value: NativePlaybackService) {
        service = value
        val continuations = waiting.values.toList()
        waiting.clear()
        continuations.forEach { it.resumeWith(Result.success(value)) }
        notifyObservers()
    }

    fun detach(value: NativePlaybackService) {
        if (service === value) {
            service = null
            notifyObservers()
        }
    }

    suspend fun awaitService(): NativePlaybackService {
        service?.let { return it }
        return suspendCancellableCoroutine { continuation ->
            val id = nextId++
            waiting[id] = continuation
            continuation.invokeOnCancellation { waiting.remove(id) }
            service?.let {
                waiting.remove(id)
                if (continuation.isActive) continuation.resumeWith(Result.success(it))
            }
        }
    }

    fun observe(server: ServerEndpoint, listener: (JSONObject) -> Unit): () -> Unit {
        val id = nextId++
        observers[id] = server to listener
        runCatching { listener(service?.state(server) ?: NativePlayback.emptyState()) }
        return unsubscribe@{
            if (Looper.myLooper() != Looper.getMainLooper()) {
                android.os.Handler(Looper.getMainLooper()).post { observers.remove(id) }
                return@unsubscribe
            }
            observers.remove(id)
        }
    }

    fun notifyObservers() {
        observers.values.toList().forEach { (server, listener) ->
            runCatching { listener(service?.state(server) ?: NativePlayback.emptyState()) }
        }
    }
}
