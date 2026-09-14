package io.jastreamer.android

import java.io.ByteArrayOutputStream
import java.io.IOException
import java.io.InterruptedIOException
import java.net.SocketTimeoutException
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLException
import kotlinx.coroutines.CancellableContinuation
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.intOrNull
import okhttp3.Call
import okhttp3.Callback
import okhttp3.CookieJar
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.ResponseBody
import okhttp3.HttpUrl.Companion.toHttpUrl

class ServerProbe(client: OkHttpClient = OkHttpClient()) {
    private val httpClient = client.newBuilder()
        .callTimeout(PROBE_TIMEOUT_MILLIS, TimeUnit.MILLISECONDS)
        .connectTimeout(PROBE_TIMEOUT_MILLIS, TimeUnit.MILLISECONDS)
        .readTimeout(PROBE_TIMEOUT_MILLIS, TimeUnit.MILLISECONDS)
        .writeTimeout(PROBE_TIMEOUT_MILLIS, TimeUnit.MILLISECONDS)
        .followRedirects(false)
        .followSslRedirects(false)
        .cookieJar(CookieJar.NO_COOKIES)
        .build()

    suspend fun probe(input: String, expectedId: String? = null): ServerEndpoint {
        val origin = EndpointPolicy.normalize(input)
        val requiredId = expectedId?.let(EndpointPolicy::normalizeServerId)
        val discoveryUrl = origin.toHttpUrl().newBuilder()
            .encodedPath(DISCOVERY_PATH)
            .query(null)
            .fragment(null)
            .build()
        val request = Request.Builder()
            .get()
            .url(discoveryUrl)
            .header("Accept", "application/json")
            .header("Cache-Control", "no-store")
            .build()

        return suspendCancellableCoroutine { continuation ->
            val call = httpClient.newCall(request)
            continuation.invokeOnCancellation { call.cancel() }
            call.enqueue(object : Callback {
                override fun onFailure(call: Call, error: IOException) {
                    if (!continuation.isActive) return
                    continuation.fail(classifyNetworkFailure(error))
                }

                override fun onResponse(call: Call, response: Response) {
                    if (!continuation.isActive) {
                        response.close()
                        return
                    }
                    try {
                        response.use {
                            val endpoint = parseResponse(it, origin, requiredId)
                            continuation.succeed(endpoint)
                        }
                    } catch (error: ClientException) {
                        continuation.fail(error)
                    } catch (error: IOException) {
                        if (continuation.isActive) continuation.fail(classifyNetworkFailure(error))
                    } catch (error: Exception) {
                        continuation.fail(
                            ClientException(ClientErrorCode.INVALID_METADATA, "Invalid discovery response", error),
                        )
                    }
                }
            })
        }
    }

    private fun parseResponse(response: Response, origin: String, requiredId: String?): ServerEndpoint {
        if (response.code in 300..399) {
            throw ClientException(ClientErrorCode.REDIRECT, "Discovery redirects are not allowed")
        }
        if (!response.isSuccessful) {
            throw ClientException(ClientErrorCode.HTTP_STATUS, response.code.toString())
        }

        val contentType = response.header("Content-Type")?.lowercase().orEmpty()
        if (contentType.isNotEmpty() && "application/json" !in contentType) {
            throw ClientException(ClientErrorCode.INVALID_METADATA, "Discovery response is not JSON")
        }

        val body = response.body ?: throw ClientException(
            ClientErrorCode.INVALID_METADATA,
            "Discovery response has no body",
        )
        val raw = readBoundedBody(body)
        val value = try {
            Json.parseToJsonElement(raw)
        } catch (error: Exception) {
            throw ClientException(ClientErrorCode.INVALID_METADATA, "Discovery response is not valid JSON", error)
        }
        val metadata = validateMetadata(value as? JsonObject)
        if (requiredId != null && metadata.id != requiredId) {
            throw ClientException(ClientErrorCode.IDENTITY_MISMATCH, "The server identity changed")
        }
        return metadata.copy(origin = origin)
    }

    private fun readBoundedBody(body: ResponseBody): String {
        val declaredLength = body.contentLength()
        if (declaredLength > MAX_RESPONSE_BYTES) {
            throw ClientException(ClientErrorCode.INVALID_METADATA, "Discovery response exceeds 32 KiB")
        }

        val output = ByteArrayOutputStream(
            if (declaredLength in 0L..MAX_RESPONSE_BYTES.toLong()) declaredLength.toInt() else 1_024,
        )
        val buffer = ByteArray(8_192)
        body.byteStream().use { input ->
            var total = 0
            while (true) {
                val read = input.read(buffer)
                if (read < 0) break
                total += read
                if (total > MAX_RESPONSE_BYTES) {
                    throw ClientException(ClientErrorCode.INVALID_METADATA, "Discovery response exceeds 32 KiB")
                }
                output.write(buffer, 0, read)
            }
        }
        return output.toString(Charsets.UTF_8.name())
    }

    private fun validateMetadata(value: JsonObject?): ServerEndpoint {
        if (value == null) invalidMetadata("Discovery metadata must be an object")

        val product = value.string("product")
        val protocol = value["protocol"] as? JsonPrimitive
        if (product != "jastreamer" || protocol == null || protocol.isString || protocol.intOrNull != 1) {
            throw ClientException(ClientErrorCode.INCOMPATIBLE_SERVER, "Unsupported discovery protocol")
        }

        val id = try {
            EndpointPolicy.normalizeServerId(value.string("id") ?: "")
        } catch (error: ClientException) {
            throw ClientException(ClientErrorCode.INVALID_METADATA, "Invalid server identity", error)
        }
        val name = cleanDisplayText(value.string("name"), MAX_NAME_LENGTH)
            ?: invalidMetadata("Invalid server name")
        val version = cleanDisplayText(value.string("version"), MAX_VERSION_LENGTH)
            ?: invalidMetadata("Invalid server version")
        return ServerEndpoint(id = id, name = name, version = version, origin = "")
    }

    private fun JsonObject.string(key: String): String? {
        val primitive = this[key] as? JsonPrimitive ?: return null
        return primitive.takeIf { it.isString }?.content
    }

    private fun cleanDisplayText(value: String?, maximum: Int): String? {
        val text = value?.trim().orEmpty()
        if (text.isEmpty() || text.length > maximum || text.any { it.isISOControl() }) return null
        return text
    }

    private fun classifyNetworkFailure(error: IOException): ClientException = when {
        error is SSLException -> ClientException(ClientErrorCode.TLS, "TLS validation failed", error)
        error is SocketTimeoutException ||
            (error is InterruptedIOException && error.message?.contains("timeout", ignoreCase = true) == true) ->
            ClientException(ClientErrorCode.TIMEOUT, "Server probe timed out", error)
        else -> ClientException(ClientErrorCode.UNREACHABLE, "Server is unreachable", error)
    }

    private fun invalidMetadata(detail: String): Nothing =
        throw ClientException(ClientErrorCode.INVALID_METADATA, detail)

    private fun CancellableContinuation<ServerEndpoint>.succeed(value: ServerEndpoint) {
        if (isActive) resumeWith(Result.success(value))
    }

    private fun CancellableContinuation<ServerEndpoint>.fail(error: Throwable) {
        if (isActive) resumeWith(Result.failure(error))
    }

    companion object {
        private const val DISCOVERY_PATH = "/api/v1/discovery"
        private const val PROBE_TIMEOUT_MILLIS = 4_000L
        private const val MAX_RESPONSE_BYTES = 32 * 1_024
        private const val MAX_NAME_LENGTH = 128
        private const val MAX_VERSION_LENGTH = 64
    }
}
