package io.jastreamer.android

/** Local identities deliberately contain no server or account ownership. */
data class OfflineTrack(
    val id: String,
    val title: String,
    val artist: String,
    val album: String,
    val albumArtist: String,
    val disc: Int,
    val track: Int,
    val durationMs: Long,
    val mime: String,
    val codec: String,
    val quality: String,
    val byteSize: Long,
    val sha256: String,
    val folderId: String,
    val relativePath: String,
    val artworkPath: String?,
    val pendingDelete: Boolean = false,
)

data class OfflineFolder(val id: String, val parentId: String?, val name: String)

data class OfflinePlaylist(
    val id: String,
    val name: String,
    val trackIds: List<String>,
    val missingTitles: List<String> = emptyList(),
)

data class OfflineQueueEntry(val id: String, val trackId: String)

data class OfflineQueue(
    val entries: List<OfflineQueueEntry> = emptyList(),
    val currentEntryId: String? = null,
    val positionMs: Long = 0,
    val shuffle: Boolean = false,
    val repeatMode: Int = 0,
)

data class OfflinePlaybackState(
    val owner: String = "none",
    val playing: Boolean = false,
    val queue: OfflineQueue = OfflineQueue(),
    val errorCode: String? = null,
    val errorMessage: String? = null,
)

data class OfflineDeleteResult(
    val deletedIds: List<String>,
    val deferredIds: List<String>,
    val failures: Map<String, String>,
)

data class OfflineStorageUsage(val musicBytes: Long, val availableBytes: Long, val limitBytes: Long)

/** Server attribution belongs to a pending transfer, never a completed OfflineTrack. */
data class OfflineDownloadJob(
    val id: String,
    val title: String,
    val serverName: String,
    val status: String,
    val quality: String,
    val folderId: String,
    val totalTracks: Int,
    val completedTracks: Int,
    val failedTracks: Int,
    val receivedBytes: Long,
    val totalBytes: Long,
    val errorCode: String? = null,
    val errorMessage: String? = null,
)
