package io.jastreamer.android

import java.security.MessageDigest
import java.util.Locale
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import org.json.JSONArray
import org.json.JSONObject

internal object OfflineTransferPolicy {
    private val sha256 = Regex("[0-9a-f]{64}")
    private val opaqueId = Regex("[A-Za-z0-9._~-]{1,160}")
    private val contentRange = Regex("bytes ([0-9]+)-([0-9]+)/([0-9]+)")
    private const val MAX_FOLDER_PATH_BYTES = 4_096

    fun requireQuality(value: String): String {
        if (value != "original" && value != "aac_256") {
            throw OfflineDownloadException("invalid_request", "Unsupported download quality")
        }
        return value
    }

    fun requireTarget(value: JSONObject): Pair<String, String> {
        val kind = value.opt("kind") as? String
            ?: throw OfflineDownloadException("invalid_request", "Download target kind is required")
        if (kind == "folder") {
            if (value.keys().asSequence().toSet() != setOf("kind", "root_id", "path")) {
                throw OfflineDownloadException("invalid_request", "Download target contains unsupported fields")
            }
            val rootID = requireFolderRoot(value.opt("root_id"))
            val path = requireFolderPath(value.opt("path"))
            return kind to JSONArray().put(rootID).put(path).toString()
        }
        if (value.keys().asSequence().toSet() != setOf("kind", "id")) {
            throw OfflineDownloadException("invalid_request", "Download target contains unsupported fields")
        }
        if (kind !in setOf("track", "album", "playlist")) {
            throw OfflineDownloadException("invalid_request", "Download target kind is unsupported")
        }
        val id = value.opt("id") as? String
            ?: throw OfflineDownloadException("invalid_request", "Download target id is required")
        requireOpaqueTarget(id)
        return kind to id
    }

    internal fun requireOpaqueTarget(value: String): String {
        if (!opaqueId.matches(value)) {
            throw OfflineDownloadException("invalid_request", "Download target id is invalid")
        }
        return value
    }

    internal fun requireFolderRoot(value: Any?): String {
        val rootID = value as? String
            ?: throw OfflineDownloadException("invalid_request", "Download folder root is required")
        if (!opaqueId.matches(rootID)) {
            throw OfflineDownloadException("invalid_request", "Download folder root is invalid")
        }
        return rootID
    }

    internal fun requireFolderPath(value: Any?): String {
        val path = value as? String
            ?: throw OfflineDownloadException("invalid_request", "Download folder path is required")
        val segments = if (path.isEmpty()) emptyList() else path.split('/')
        if (
            path.toByteArray(Charsets.UTF_8).size > MAX_FOLDER_PATH_BYTES ||
            path.startsWith('/') ||
            path.endsWith('/') ||
            path.any(Char::isISOControl) ||
            segments.any { it.isEmpty() || it == "." || it == ".." }
        ) {
            throw OfflineDownloadException("invalid_request", "Download folder path is invalid")
        }
        return path
    }

    fun requireRemoteJobId(value: String): String {
        if (!opaqueId.matches(value)) {
            throw OfflineDownloadException("invalid_manifest", "Download job id is invalid")
        }
        return value
    }

    fun requireSha256(value: String): String {
        val normalized = value.lowercase(Locale.US)
        if (!sha256.matches(normalized)) {
            throw OfflineDownloadException("invalid_manifest", "Download checksum is invalid")
        }
        return normalized
    }

    fun fileUrl(origin: String, remoteJobId: String, index: Int, mediaPath: String): HttpUrl {
        if (index < 0 || !opaqueId.matches(remoteJobId)) invalidPath()
        val parsed = resolveSameOrigin(origin, mediaPath)
        val segments = parsed.pathSegments
        if (
            segments.size != 6 ||
            segments[0] != "api" ||
            segments[1] != "v1" ||
            segments[2] != "downloads" ||
            segments[3] != remoteJobId ||
            segments[4] != "files" ||
            segments[5] != index.toString()
        ) invalidPath()
        return parsed
    }

    fun artworkUrl(origin: String, artworkPath: String): HttpUrl? {
        if (artworkPath.isBlank()) return null
        val parsed = try {
            resolveSameOrigin(origin, artworkPath)
        } catch (_: OfflineDownloadException) {
            return null
        }
        val segments = parsed.pathSegments
        if (
            segments.size != 4 ||
            segments[0] != "api" ||
            segments[1] != "v1" ||
            segments[2] != "artwork" ||
            segments[3].isBlank() ||
            segments[3].length > 200
        ) return null
        return parsed
    }

    fun strongEtagMatches(value: String?, expectedSha256: String): Boolean =
        value == "\"$expectedSha256\""

    fun validateContentRange(value: String?, offset: Long, total: Long, bodyLength: Long) {
        val match = value?.let(contentRange::matchEntire)
            ?: throw OfflineDownloadException("range_mismatch", "Server returned an invalid Content-Range")
        val start = match.groupValues[1].toLongOrNull()
        val end = match.groupValues[2].toLongOrNull()
        val declaredTotal = match.groupValues[3].toLongOrNull()
        if (
            start != offset ||
            end == null ||
            declaredTotal != total ||
            end < offset ||
            end >= total ||
            end - offset + 1 != bodyLength
        ) {
            throw OfflineDownloadException("range_mismatch", "Server returned a mismatched byte range")
        }
    }

    fun sha256(file: java.io.File): String {
        val digest = MessageDigest.getInstance("SHA-256")
        file.inputStream().buffered().use { input ->
            val buffer = ByteArray(DEFAULT_BUFFER_SIZE)
            while (true) {
                val count = input.read(buffer)
                if (count < 0) break
                digest.update(buffer, 0, count)
            }
        }
        return digest.digest().joinToString("") { "%02x".format(Locale.US, it) }
    }

    private fun resolveSameOrigin(origin: String, path: String): HttpUrl {
        if (!path.startsWith('/') || path.startsWith("//") || '?' in path || '#' in path || '\\' in path) invalidPath()
        val base = EndpointPolicy.normalizeOrigin(origin).toHttpUrl()
        val parsed = base.resolve(path)?.toString()?.toHttpUrlOrNull() ?: invalidPath()
        if (
            parsed.scheme != base.scheme ||
            parsed.host != base.host ||
            parsed.port != base.port ||
            parsed.encodedUsername.isNotEmpty() ||
            parsed.encodedPassword.isNotEmpty()
        ) invalidPath()
        return parsed
    }

    private fun invalidPath(): Nothing =
        throw OfflineDownloadException("invalid_path", "Server returned an untrusted download path")
}

class OfflineDownloadException(
    val code: String,
    override val message: String,
    cause: Throwable? = null,
    val retryable: Boolean? = null,
) : Exception(message, cause)
