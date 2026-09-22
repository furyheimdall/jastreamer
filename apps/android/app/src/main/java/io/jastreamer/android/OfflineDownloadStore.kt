package io.jastreamer.android

import java.io.File
import java.io.FileOutputStream
import java.nio.charset.StandardCharsets
import java.nio.file.Files
import java.nio.file.StandardCopyOption
import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject

internal data class StoredDownloadTrack(
    val index: Int,
    var status: String,
    var title: String,
    var artist: String,
    var album: String,
    var albumArtist: String,
    var disc: Int,
    var track: Int,
    var durationMs: Long,
    var sourceVersion: String,
    var quality: String,
    var mime: String,
    var codec: String,
    var byteSize: Long,
    var sha256: String,
    var mediaPath: String,
    var artworkPath: String,
    var importedTrackId: String? = null,
    var errorCode: String? = null,
    var errorMessage: String? = null,
)

internal data class StoredDownloadJob(
    val id: String,
    var title: String,
    var server: ServerEndpoint?,
    var principalId: String?,
    var remoteId: String?,
    var kind: String,
    var targetId: String?,
    var status: String,
    val quality: String,
    var folderId: String,
    var tracks: MutableList<StoredDownloadTrack>,
    var playlistId: String? = null,
    var playlistReceiptToken: String? = null,
    var receivedBytes: Long = 0,
    var errorCode: String? = null,
    var errorMessage: String? = null,
) {
    fun publicValue(): OfflineDownloadJob {
        val completed = tracks.count { it.importedTrackId != null }
        val failed = tracks.count { it.status == "failed" && it.importedTrackId == null }
        val totalBytes = tracks.filter { it.byteSize > 0 }.sumOf { it.byteSize }
        return OfflineDownloadJob(
            id = id,
            title = title,
            serverName = server?.name.orEmpty(),
            status = status,
            quality = quality,
            folderId = folderId,
            totalTracks = tracks.size,
            completedTracks = completed,
            failedTracks = failed,
            receivedBytes = receivedBytes.coerceAtMost(totalBytes.takeIf { it > 0 } ?: Long.MAX_VALUE),
            totalBytes = totalBytes,
            errorCode = errorCode,
            errorMessage = errorMessage,
        )
    }

    fun terminal(): Boolean = status in setOf("completed", "partial", "failed", "cancelled")

    fun scrubRemoteAssociation() {
        server = null
        principalId = null
        remoteId = null
        targetId = null
        tracks.forEach { item ->
            item.sourceVersion = ""
            item.mediaPath = ""
            item.artworkPath = ""
        }
    }
}

internal class OfflineDownloadStore(private val root: File) {
    private val directory = File(root, "offline-downloads")
    private val file = File(directory, "jobs.json")
    private val lock = Any()

    fun load(): MutableList<StoredDownloadJob> = synchronized(lock) {
        if (!file.isFile) return@synchronized mutableListOf()
        val raw = try {
            file.readText(StandardCharsets.UTF_8)
        } catch (failure: Exception) {
            throw OfflineDownloadException("storage", "Download state could not be read", failure)
        }
        try {
            val rootValue = JSONObject(raw)
            if (rootValue.optInt("schema", 0) != SCHEMA) return@synchronized mutableListOf()
            val values = rootValue.optJSONArray("jobs") ?: JSONArray()
            MutableList(values.length()) { index -> decodeJob(values.getJSONObject(index)) }
        } catch (failure: JSONException) {
            throw OfflineDownloadException("storage", "Download state is invalid", failure)
        }
    }

    fun save(jobs: List<StoredDownloadJob>) = synchronized(lock) {
        directory.mkdirs()
        if (!directory.isDirectory) throw OfflineDownloadException("storage", "Download storage is unavailable")
        val rootValue = JSONObject().put("schema", SCHEMA).put("jobs", JSONArray().apply {
            jobs.forEach { put(encodeJob(it)) }
        })
        val temporary = File(directory, "jobs.json.tmp")
        try {
            FileOutputStream(temporary).use { output ->
                output.write(rootValue.toString().toByteArray(StandardCharsets.UTF_8))
                output.fd.sync()
            }
            try {
                Files.move(
                    temporary.toPath(),
                    file.toPath(),
                    StandardCopyOption.ATOMIC_MOVE,
                    StandardCopyOption.REPLACE_EXISTING,
                )
            } catch (_: java.nio.file.AtomicMoveNotSupportedException) {
                Files.move(temporary.toPath(), file.toPath(), StandardCopyOption.REPLACE_EXISTING)
            }
        } catch (failure: Exception) {
            temporary.delete()
            throw OfflineDownloadException("storage", "Download state could not be saved", failure)
        }
    }

    fun partialFile(jobId: String, index: Int): File {
        val partials = File(directory, "partials")
        partials.mkdirs()
        return File(partials, "$jobId-$index.part")
    }

    fun artworkFile(jobId: String, index: Int): File {
        val partials = File(directory, "partials")
        partials.mkdirs()
        return File(partials, "$jobId-$index.art")
    }

    fun discardTemporary(job: StoredDownloadJob) {
        job.tracks.forEach {
            partialFile(job.id, it.index).delete()
            artworkFile(job.id, it.index).delete()
        }
    }

    private fun encodeJob(job: StoredDownloadJob): JSONObject = JSONObject().apply {
        put("id", job.id)
        put("title", job.title)
        job.server?.let { endpoint ->
            put("server", JSONObject()
                .put("id", endpoint.id)
                .put("name", endpoint.name)
                .put("version", endpoint.version)
                .put("origin", endpoint.origin))
        }
        putNullable("principal_id", job.principalId)
        putNullable("remote_id", job.remoteId)
        put("kind", job.kind)
        putNullable("target_id", job.targetId)
        put("status", job.status)
        put("quality", job.quality)
        put("folder_id", job.folderId)
        putNullable("playlist_id", job.playlistId)
        putNullable("playlist_receipt_token", job.playlistReceiptToken)
        put("received_bytes", job.receivedBytes)
        putNullable("error_code", job.errorCode)
        putNullable("error_message", job.errorMessage)
        put("tracks", JSONArray().apply { job.tracks.forEach { put(encodeTrack(it)) } })
    }

    private fun encodeTrack(value: StoredDownloadTrack): JSONObject = JSONObject().apply {
        put("index", value.index)
        put("status", value.status)
        put("title", value.title)
        put("artist", value.artist)
        put("album", value.album)
        put("album_artist", value.albumArtist)
        put("disc", value.disc)
        put("track", value.track)
        put("duration_ms", value.durationMs)
        put("source_version", value.sourceVersion)
        put("quality", value.quality)
        put("mime", value.mime)
        put("codec", value.codec)
        put("byte_size", value.byteSize)
        put("sha256", value.sha256)
        put("media_path", value.mediaPath)
        put("artwork_path", value.artworkPath)
        putNullable("imported_track_id", value.importedTrackId)
        putNullable("error_code", value.errorCode)
        putNullable("error_message", value.errorMessage)
    }

    private fun decodeJob(value: JSONObject): StoredDownloadJob {
        val endpoint = value.optJSONObject("server")?.let {
            ServerEndpoint(
                id = it.getString("id"),
                name = it.getString("name"),
                version = it.optString("version"),
                origin = EndpointPolicy.normalizeOrigin(it.getString("origin")),
            )
        }
        val tracks = value.optJSONArray("tracks") ?: JSONArray()
        val kind = value.optString("kind")
        val targetID = value.optionalString("target_id")
        val quality = value.optString("quality")
        if (kind !in setOf("track", "album", "playlist", "folder")) {
            throw JSONException("Download target kind is invalid")
        }
        try {
            OfflineTransferPolicy.requireQuality(quality)
            if (targetID != null) {
                if (kind == "folder") {
                    OfflineTransferClient.decodeFolderTarget(targetID)
                } else {
                    OfflineTransferPolicy.requireOpaqueTarget(targetID)
                }
            } else if (endpoint != null) {
                throw OfflineDownloadException("invalid_request", "Pending download target is missing")
            }
        } catch (_: OfflineDownloadException) {
            throw JSONException("Download target is invalid")
        }
        return StoredDownloadJob(
            id = value.getString("id"),
            title = value.optString("title"),
            server = endpoint,
            principalId = value.optionalString("principal_id"),
            remoteId = value.optionalString("remote_id"),
            kind = kind,
            targetId = targetID,
            status = value.optString("status", "failed"),
            quality = quality,
            folderId = value.optString("folder_id", OfflineLibrary.IMPORT_FOLDER_ID),
            tracks = MutableList(tracks.length()) { decodeTrack(tracks.getJSONObject(it)) },
            playlistId = value.optionalString("playlist_id"),
            playlistReceiptToken = value.optionalString("playlist_receipt_token"),
            receivedBytes = value.optLong("received_bytes", 0).coerceAtLeast(0),
            errorCode = value.optionalString("error_code"),
            errorMessage = value.optionalString("error_message"),
        )
    }

    private fun decodeTrack(value: JSONObject): StoredDownloadTrack = StoredDownloadTrack(
        index = value.getInt("index"),
        status = value.optString("status"),
        title = value.optString("title"),
        artist = value.optString("artist"),
        album = value.optString("album"),
        albumArtist = value.optString("album_artist"),
        disc = value.optInt("disc"),
        track = value.optInt("track"),
        durationMs = value.optLong("duration_ms"),
        sourceVersion = value.optString("source_version"),
        quality = value.optString("quality"),
        mime = value.optString("mime"),
        codec = value.optString("codec"),
        byteSize = value.optLong("byte_size"),
        sha256 = value.optString("sha256"),
        mediaPath = value.optString("media_path"),
        artworkPath = value.optString("artwork_path"),
        importedTrackId = value.optionalString("imported_track_id"),
        errorCode = value.optionalString("error_code"),
        errorMessage = value.optionalString("error_message"),
    )

    private fun JSONObject.putNullable(name: String, value: String?) {
        if (value == null) put(name, JSONObject.NULL) else put(name, value)
    }

    private fun JSONObject.optionalString(name: String): String? =
        opt(name).let { if (it is String && it.isNotBlank()) it else null }

    companion object {
        private const val SCHEMA = 1
    }
}
