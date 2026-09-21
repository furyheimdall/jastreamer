package io.jastreamer.android

import androidx.webkit.ProfileStore
import androidx.webkit.WebViewFeature
import java.io.File
import java.io.IOException
import java.io.RandomAccessFile
import java.util.Locale
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withContext
import okhttp3.Call
import okhttp3.Callback
import okhttp3.CookieJar
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject

internal data class DownloadPreview(val title: String, val trackCount: Int)

internal data class DownloadManifest(
    val remoteId: String,
    val kind: String,
    val title: String,
    val quality: String,
    val status: String,
    val tracks: MutableList<StoredDownloadTrack>,
)

internal class OfflineTransferClient(
    private val server: ServerEndpoint,
) {
    private val origin = EndpointPolicy.normalizeOrigin(server.origin)
    private val originUrl = origin.toHttpUrl()
    private val client = OkHttpClient.Builder()
        .connectTimeout(10, TimeUnit.SECONDS)
        .readTimeout(30, TimeUnit.SECONDS)
        .writeTimeout(30, TimeUnit.SECONDS)
        .callTimeout(40, TimeUnit.SECONDS)
        .followRedirects(false)
        .followSslRedirects(false)
        .retryOnConnectionFailure(false)
        .cookieJar(CookieJar.NO_COOKIES)
        .build()
    private val mediaClient = client.newBuilder()
        .readTimeout(45, TimeUnit.SECONDS)
        .callTimeout(0, TimeUnit.SECONDS)
        .build()

    suspend fun verifyServerIdentity() {
        val value = json("GET", path("api", "v1", "discovery"), authenticated = false)
        val product = value.opt("product") as? String
        val id = value.opt("id") as? String
        if (product != "jastreamer" || id == null) {
            throw OfflineDownloadException("invalid_server", "The download server is incompatible")
        }
        val actual = try {
            EndpointPolicy.normalizeServerId(id)
        } catch (failure: ClientException) {
            throw OfflineDownloadException("invalid_server", "The download server identity is invalid", failure)
        }
        if (actual != EndpointPolicy.normalizeServerId(server.id)) {
            throw OfflineDownloadException("identity_changed", "The download server identity changed")
        }
    }

    suspend fun principal(): String = principalAndCookie().first

    suspend fun requirePrincipal(expected: String): String {
        val (actual, authorization) = principalAndCookie()
        if (actual != expected) {
            throw OfflineDownloadException("principal_changed", "This download belongs to a different signed-in account")
        }
        return authorization
    }

    private suspend fun principalAndCookie(): Pair<String, String> {
        val authorization = cookie(originUrl)
        val session = json(
            "GET",
            path("api", "v1", "session"),
            authenticated = false,
            authorization = authorization,
        )
        if (!session.optBoolean("authenticated", false)) {
            throw OfflineDownloadException("auth_required", "Sign in to continue this download")
        }
        val id = session.optJSONObject("user")?.opt("id") as? String
        if (id.isNullOrBlank() || id.length > 200 || id.any(Char::isISOControl)) {
            throw OfflineDownloadException("auth_required", "The authenticated account is unavailable")
        }
        return id to authorization
    }

    suspend fun preview(kind: String, targetId: String, expectedPrincipal: String): DownloadPreview {
        val authorization = requirePrincipal(expectedPrincipal)
        return when (kind) {
            "track" -> {
                val value = json(
                    "GET",
                    path("api", "v1", "library", "tracks", targetId),
                    authenticated = false,
                    authorization = authorization,
                )
                DownloadPreview(safeTitle(value.optString("title"), "Track"), 1)
            }
            "album" -> {
                val url = path("api", "v1", "library", "tracks").newBuilder()
                    .addQueryParameter("album_id", targetId)
                    .addQueryParameter("offset", "0")
                    .addQueryParameter("limit", "1")
                    .build()
                val value = json("GET", url, authenticated = false, authorization = authorization)
                val count = pageCount(value)
                val title = value.optJSONArray("items")?.optJSONObject(0)?.optString("album").orEmpty()
                DownloadPreview(safeTitle(title, "Album"), count)
            }
            "playlist" -> {
                val value = json(
                    "GET",
                    path("api", "v1", "playlists", targetId),
                    authenticated = false,
                    authorization = authorization,
                )
                val entries = value.optJSONArray("track_ids") ?: value.optJSONArray("tracks") ?: JSONArray()
                DownloadPreview(safeTitle(value.optString("name"), "Playlist"), entries.length())
            }
            else -> throw OfflineDownloadException("invalid_request", "Unsupported download target")
        }
    }

    suspend fun create(
        kind: String,
        targetId: String,
        quality: String,
        expectedPrincipal: String,
        shouldContinue: () -> Boolean,
    ): DownloadManifest {
        val authorization = requirePrincipal(expectedPrincipal)
        if (!shouldContinue()) throw OfflineDownloadException("stopped", "Download stopped")
        val body = JSONObject().put("kind", kind).put("id", targetId).put("quality", quality)
        val value = json(
            "POST",
            path("api", "v1", "downloads"),
            body,
            authenticated = false,
            authorization = authorization,
        )
        return parseManifest(value, kind, quality)
    }

    suspend fun poll(
        remoteId: String,
        expectedKind: String,
        expectedQuality: String,
        expectedPrincipal: String,
        shouldContinue: () -> Boolean,
    ): DownloadManifest {
        val authorization = requirePrincipal(expectedPrincipal)
        if (!shouldContinue()) throw OfflineDownloadException("stopped", "Download stopped")
        return parseManifest(
            json(
                "GET",
                path("api", "v1", "downloads", remoteId),
                authenticated = false,
                authorization = authorization,
            ),
            expectedKind,
            expectedQuality,
        )
    }

    suspend fun cancel(remoteId: String, expectedPrincipal: String, shouldContinue: () -> Boolean) {
        val authorization = requirePrincipal(expectedPrincipal)
        if (!shouldContinue()) throw OfflineDownloadException("stopped", "Download stopped")
        execute(
            request(
                "DELETE",
                path("api", "v1", "downloads", remoteId),
                body = null,
                authenticated = false,
                authorization = authorization,
            ),
        ) { response ->
            if (!response.isSuccessful && response.code != 404 && response.code != 410) throw httpFailure(response)
        }
    }


    suspend fun download(
        remoteId: String,
        item: StoredDownloadTrack,
        destination: File,
        expectedPrincipal: String,
        shouldContinue: () -> Boolean,
        onProgress: (Long) -> Unit,
    ) {
        if (!shouldContinue()) throw OfflineDownloadException("stopped", "Download stopped")
        val url = OfflineTransferPolicy.fileUrl(origin, remoteId, item.index, item.mediaPath)
        val existing = destination.takeIf(File::isFile)?.length() ?: 0L
        if (existing < 0 || existing > item.byteSize) {
            destination.delete()
            throw OfflineDownloadException("range_mismatch", "Partial download size is invalid")
        }
        if (existing == item.byteSize) return
        destination.parentFile?.mkdirs()
        val authorization = requirePrincipal(expectedPrincipal)
        if (!shouldContinue()) throw OfflineDownloadException("stopped", "Download stopped")
        val builder = requestBuilder(url, authenticated = false, authorization = authorization)
            .header("Accept", item.mime.ifBlank { "application/octet-stream" })
            .header("Accept-Encoding", "identity")
        if (existing > 0) {
            builder.header("Range", "bytes=$existing-")
            builder.header("If-Range", "\"${item.sha256}\"")
        }
        execute(mediaClient, builder.get().build()) { response ->
            val expectedCode = if (existing > 0) 206 else 200
            if (response.code != expectedCode) {
                if (response.isSuccessful) {
                    throw OfflineDownloadException("range_mismatch", "Server did not honor the requested byte range")
                }
                if (response.code in 300..399) throw OfflineDownloadException("redirect", "Download redirects are not allowed")
                throw httpFailure(response)
            }
            if (!OfflineTransferPolicy.strongEtagMatches(response.header("ETag"), item.sha256)) {
                throw OfflineDownloadException("source_changed", "The server download version changed")
            }
            val responseMime = response.header("Content-Type").orEmpty().substringBefore(';').trim()
            if (item.mime.isNotBlank() && !responseMime.equals(item.mime, ignoreCase = true)) {
                throw OfflineDownloadException("invalid_manifest", "Server returned a different media type")
            }
            val remaining = item.byteSize - existing
            val body = response.body ?: throw OfflineDownloadException("transfer", "The server returned no download body")
            val bodyLength = body.contentLength()
            if (bodyLength != remaining) {
                throw OfflineDownloadException("size_mismatch", "The server returned an unexpected download size")
            }
            if (existing > 0) {
                OfflineTransferPolicy.validateContentRange(response.header("Content-Range"), existing, item.byteSize, bodyLength)
            }
            RandomAccessFile(destination, "rw").use { output ->
                output.seek(existing)
                body.byteStream().buffered().use { input ->
                    val buffer = ByteArray(64 * 1024)
                    var written = existing
                    while (true) {
                        if (!shouldContinue()) throw OfflineDownloadException("stopped", "Download stopped")
                        val count = input.read(buffer)
                        if (count < 0) break
                        if (written + count > item.byteSize) {
                            throw OfflineDownloadException("size_mismatch", "The server sent more data than declared")
                        }
                        output.write(buffer, 0, count)
                        written += count
                        onProgress(written)
                    }
                    output.fd.sync()
                    if (written != item.byteSize) {
                        throw OfflineDownloadException("size_mismatch", "The server download ended early")
                    }
                }
            }
        }
    }

    suspend fun artwork(
        path: String,
        destination: File,
        expectedPrincipal: String,
        shouldContinue: () -> Boolean,
    ): File? {
        val url = OfflineTransferPolicy.artworkUrl(origin, path) ?: return null
        if (!shouldContinue()) return null
        return try {
            val authorization = requirePrincipal(expectedPrincipal)
            if (!shouldContinue()) throw OfflineDownloadException("stopped", "Download stopped")
            execute(
                mediaClient,
                requestBuilder(url, authenticated = false, authorization = authorization).get().build(),
            ) { response ->
                if (!response.isSuccessful) return@execute null
                val contentType = response.header("Content-Type").orEmpty().substringBefore(';').trim()
                if (!contentType.startsWith("image/")) return@execute null
                val body = response.body ?: return@execute null
                val length = body.contentLength()
                if (length !in 1..MAX_ARTWORK_BYTES.toLong()) return@execute null
                destination.parentFile?.mkdirs()
                destination.outputStream().buffered().use { output ->
                    val input = body.byteStream()
                    val buffer = ByteArray(32 * 1024)
                    var total = 0L
                    while (true) {
                        if (!shouldContinue()) throw OfflineDownloadException("stopped", "Download stopped")
                        val count = input.read(buffer)
                        if (count < 0) break
                        total += count
                        if (total > MAX_ARTWORK_BYTES) throw OfflineDownloadException("artwork_too_large", "Artwork is too large")
                        output.write(buffer, 0, count)
                    }
                }
                destination
            }
        } catch (failure: OfflineDownloadException) {
            destination.delete()
            if (failure.code in setOf("stopped", "auth_required", "principal_changed", "profile_unavailable")) {
                throw failure
            }
            null
        } catch (_: Exception) {
            destination.delete()
            null
        }
    }

    private fun parseManifest(value: JSONObject, expectedKind: String, expectedQuality: String): DownloadManifest {
        val remoteId = OfflineTransferPolicy.requireRemoteJobId(value.optString("id"))
        val kind = value.optString("kind")
        val quality = OfflineTransferPolicy.requireQuality(value.optString("quality"))
        if (kind != expectedKind || quality != expectedQuality) {
            throw OfflineDownloadException("invalid_manifest", "Download manifest does not match the request")
        }
        val status = value.optString("status")
        if (status !in MANIFEST_STATES) throw OfflineDownloadException("invalid_manifest", "Download status is invalid")
        val values = value.optJSONArray("tracks")
            ?: throw OfflineDownloadException("invalid_manifest", "Download manifest tracks are missing")
        if (values.length() > MAX_TRACKS || (values.length() == 0 && kind != "playlist")) {
            throw OfflineDownloadException("invalid_manifest", "Download manifest track count is invalid")
        }
        val tracks = MutableList(values.length()) { position -> parseTrack(values.getJSONObject(position), position, quality) }
        val fallbackTitle = kind.replaceFirstChar { it.uppercase() }
        return DownloadManifest(remoteId, kind, safeTitle(value.optString("title"), fallbackTitle), quality, status, tracks)
    }

    private fun parseTrack(value: JSONObject, position: Int, expectedQuality: String): StoredDownloadTrack {
        val index = value.optInt("index", -1)
        if (index != position) throw OfflineDownloadException("invalid_manifest", "Download manifest order is invalid")
        val status = value.optString("status")
        if (status !in TRACK_STATES) throw OfflineDownloadException("invalid_manifest", "Download track status is invalid")
        val ready = status == "ready"
        val quality = value.optString("quality", expectedQuality)
        if (quality != expectedQuality) throw OfflineDownloadException("invalid_manifest", "Download track quality changed")
        val byteSize = value.optLong("byte_size", 0)
        val sha = value.optString("sha256")
        val mediaPath = safePath(value.optString("media_path"))
        val sourceVersion = safeText(value.optString("source_version"))
        val mime = safeText(value.optString("mime")).lowercase(Locale.US)
        val codec = safeText(value.optString("codec")).lowercase(Locale.US)
        if (ready) {
            if (
                byteSize <= 0 ||
                byteSize > MAX_TRACK_BYTES ||
                sourceVersion.isBlank() ||
                !MEDIA_MIME.matches(mime) ||
                codec.isBlank()
            ) {
                throw OfflineDownloadException("invalid_manifest", "Ready download metadata is incomplete")
            }
            OfflineTransferPolicy.requireSha256(sha)
        }
        val error = value.optJSONObject("error")
        return StoredDownloadTrack(
            index = index,
            status = status,
            title = safeTitle(value.optString("title"), "Track ${index + 1}"),
            artist = safeText(value.optString("artist")),
            album = safeText(value.optString("album")),
            albumArtist = safeText(value.optString("album_artist")),
            disc = value.optInt("disc", 0).coerceAtLeast(0),
            track = value.optInt("track", 0).coerceAtLeast(0),
            durationMs = value.optLong("duration_ms", 0).coerceAtLeast(0),
            sourceVersion = sourceVersion,
            quality = quality,
            mime = mime,
            codec = codec,
            byteSize = byteSize.coerceAtLeast(0),
            sha256 = if (ready) OfflineTransferPolicy.requireSha256(sha) else "",
            mediaPath = if (ready) mediaPath else "",
            artworkPath = safePath(value.optString("artwork_path")),
            errorCode = error?.optString("code")?.takeIf(String::isNotBlank)?.take(64),
            errorMessage = error?.optString("message")?.takeIf(String::isNotBlank)?.take(MAX_TEXT),
        )
    }

    private fun pageCount(value: JSONObject): Int {
        val candidates = listOf("total", "total_count", "count")
        candidates.forEach { key ->
            if (value.has(key)) return value.optInt(key, 0).coerceIn(0, MAX_TRACKS)
        }
        return value.optJSONArray("items")?.length()?.coerceIn(0, MAX_TRACKS) ?: 0
    }

    private suspend fun json(
        method: String,
        url: HttpUrl,
        body: JSONObject? = null,
        authenticated: Boolean = true,
        authorization: String? = null,
    ): JSONObject {
        val bytes = body?.toString()?.toByteArray(Charsets.UTF_8)
        return execute(request(method, url, bytes, authenticated, authorization)) { response ->
            if (!response.isSuccessful) throw httpFailure(response)
            val raw = readBounded(response, MAX_JSON_BYTES)
            try {
                JSONObject(raw.toString(Charsets.UTF_8))
            } catch (failure: JSONException) {
                throw OfflineDownloadException("invalid_response", "Server returned invalid JSON", failure)
            }
        }
    }

    private suspend fun request(
        method: String,
        url: HttpUrl,
        body: ByteArray?,
        authenticated: Boolean = true,
        authorization: String? = null,
    ): Request {
        val builder = requestBuilder(url, authenticated, authorization)
            .header("Accept", "application/json")
            .header("Cache-Control", "no-store")
        if (method != "GET" && method != "HEAD") {
            builder.header("Origin", origin).header("X-Jastreamer-Request", "web")
        }
        return when (method) {
            "GET" -> builder.get().build()
            "POST" -> builder.post((body ?: ByteArray(0)).toRequestBody(JSON_MEDIA_TYPE)).build()
            "DELETE" -> if (body == null) builder.delete().build() else builder.delete(body.toRequestBody(JSON_MEDIA_TYPE)).build()
            else -> throw IllegalArgumentException("Unsupported method")
        }
    }

    private suspend fun requestBuilder(
        url: HttpUrl,
        authenticated: Boolean,
        authorization: String? = null,
    ): Request.Builder {
        requireSameOrigin(url)
        val builder = Request.Builder().url(url)
        val header = authorization ?: if (authenticated) cookie(url) else ""
        if (header.isNotBlank()) builder.header("Cookie", header)
        return builder
    }

    private suspend fun cookie(url: HttpUrl): String = withContext(Dispatchers.Main.immediate) {
        if (!WebViewFeature.isFeatureSupported(WebViewFeature.MULTI_PROFILE)) {
            throw OfflineDownloadException("profile_unavailable", "The isolated server profile is unavailable")
        }
        try {
            ProfileStore.getInstance()
                .getOrCreateProfile(EndpointPolicy.profileName(server))
                .cookieManager
                .getCookie(url.toString())
                .orEmpty()
        } catch (failure: RuntimeException) {
            throw OfflineDownloadException("profile_unavailable", "The isolated server profile is unavailable", failure)
        }
    }

    private suspend fun <T> execute(request: Request, consume: (Response) -> T): T = execute(client, request, consume)

    private suspend fun <T> execute(httpClient: OkHttpClient, request: Request, consume: (Response) -> T): T =
        suspendCancellableCoroutine { continuation ->
            val call = httpClient.newCall(request)
            continuation.invokeOnCancellation { call.cancel() }
            call.enqueue(object : Callback {
                override fun onFailure(call: Call, error: IOException) {
                    if (continuation.isActive) continuation.resumeWith(Result.failure(error))
                }

                override fun onResponse(call: Call, response: Response) {
                    if (!continuation.isActive) {
                        response.close()
                        return
                    }
                    val result = runCatching { response.use(consume) }
                    if (continuation.isActive) continuation.resumeWith(result)
                }
            })
        }

    private fun httpFailure(response: Response): OfflineDownloadException {
        if (response.code in 300..399) return OfflineDownloadException("redirect", "Server redirects are not allowed")
        val code = try {
            val raw = readBounded(response, MAX_ERROR_BYTES)
            JSONObject(raw.toString(Charsets.UTF_8)).optJSONObject("error")?.optString("code").orEmpty()
        } catch (_: Exception) {
            ""
        }
        val safeCode = code.takeIf { SAFE_CODE.matches(it) } ?: when (response.code) {
            401, 403 -> "auth_required"
            404, 410 -> "expired"
            409 -> "not_ready"
            else -> "http_${response.code}"
        }
        return OfflineDownloadException(
            safeCode,
            "Server download request failed (HTTP ${response.code})",
            retryable = response.code == 408 ||
                response.code == 425 ||
                response.code == 429 ||
                response.code in 500..599,
        )
    }

    private fun readBounded(response: Response, maximum: Int): ByteArray {
        val body = response.body ?: return ByteArray(0)
        if (body.contentLength() > maximum) throw OfflineDownloadException("response_too_large", "Server response is too large")
        val source = body.source()
        source.request(maximum.toLong() + 1)
        if (source.buffer.size > maximum) throw OfflineDownloadException("response_too_large", "Server response is too large")
        return source.readByteArray()
    }

    private fun path(vararg segments: String): HttpUrl {
        val builder = originUrl.newBuilder()
        segments.forEach(builder::addPathSegment)
        return builder.build()
    }

    private fun requireSameOrigin(url: HttpUrl) {
        if (url.scheme != originUrl.scheme || url.host != originUrl.host || url.port != originUrl.port) {
            throw OfflineDownloadException("invalid_path", "Cross-origin downloads are not allowed")
        }
    }

    private fun safeText(value: String): String = value.trim().take(MAX_TEXT).filterNot(Char::isISOControl)
    private fun safePath(value: String): String =
        value.takeIf { it.length <= MAX_PATH && it.none(Char::isISOControl) }.orEmpty()
    private fun safeTitle(value: String, fallback: String): String = safeText(value).ifBlank { fallback }

    companion object {
        private const val MAX_JSON_BYTES = 32 * 1024 * 1024
        private const val MAX_ERROR_BYTES = 32 * 1024
        private const val MAX_ARTWORK_BYTES = 5 * 1024 * 1024
        private const val MAX_TRACKS = 10_000
        private const val MAX_TRACK_BYTES = 512L * 1024 * 1024 * 1024
        private const val MAX_TEXT = 500
        private const val MAX_PATH = 2_048
        private val JSON_MEDIA_TYPE = "application/json; charset=utf-8".toMediaType()
        private val SAFE_CODE = Regex("[a-z0-9_.-]{1,64}")
        private val MEDIA_MIME =
            Regex("[a-z0-9][a-z0-9!#\$&^_.+-]{0,99}/[a-z0-9][a-z0-9!#\$&^_.+-]{0,99}")
        private val MANIFEST_STATES = setOf("preparing", "ready", "partial", "failed", "cancelled")
        private val TRACK_STATES = setOf("pending", "preparing", "ready", "failed")
    }
}
