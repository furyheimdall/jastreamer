package io.jastreamer.android

import android.content.ContentValues
import android.content.Context
import android.database.Cursor
import android.database.sqlite.SQLiteDatabase
import android.database.sqlite.SQLiteOpenHelper
import android.os.StatFs
import java.io.Closeable
import java.io.File
import java.io.FileInputStream
import java.io.FileOutputStream
import java.io.IOException
import java.nio.file.AtomicMoveNotSupportedException
import java.nio.file.LinkOption
import java.nio.file.Path
import java.nio.file.Files
import java.nio.file.StandardCopyOption
import java.security.MessageDigest
import java.util.UUID
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import org.json.JSONObject

class OfflineLibraryException(
    val code: String,
    message: String,
    cause: Throwable? = null,
) : Exception(message, cause)

class OfflineAudioHandle internal constructor(
    val input: FileInputStream,
    val length: Long,
    private val release: () -> Unit,
) : Closeable {
    private var closed = false

    override fun close() {
        synchronized(this) {
            if (closed) return
            closed = true
        }
        try {
            input.close()
        } finally {
            release()
        }
    }
}

/**
 * Device-owned music catalog. Completed rows intentionally have no server, account, or remote URL fields.
 * All filesystem/database transitions are serialized and journaled before the filesystem changes.
 */
class OfflineLibrary private constructor(context: Context) {
    private val appContext = context.applicationContext
    private val trustedStorageRoots = listOf(appContext.filesDir, appContext.cacheDir).map { root ->
        TrustedStorageRoot(
            declared = root.toPath().toAbsolutePath().normalize(),
            resolved = canonicalPath(root),
        )
    }
    private val lock = Any()
    private val base = File(appContext.filesDir, "offline")
    private val musicRoot = File(base, "music")
    private val artworkRoot = File(base, "artwork")
    private val trashRoot = File(base, ".trash")
    private val database = LibraryDatabase(appContext).writableDatabase
    private val pins = HashMap<String, Int>()
    private val mutableChanges = MutableStateFlow(0L)

    val changes: StateFlow<Long> = mutableChanges.asStateFlow()

    init {
        synchronized(lock) {
            ensureDirectory(base)
            ensureDirectory(musicRoot)
            ensureDirectory(artworkRoot)
            ensureDirectory(trashRoot)
            bootstrapFolderRowsLocked()
            recoverLocked()
            ensureReservedFolderDirectoriesLocked()
        }
    }

    fun tracks(folderId: String? = null): List<OfflineTrack> = synchronized(lock) {
        val selection = if (folderId == null) null else "folder_id=?"
        val args = if (folderId == null) null else arrayOf(folderId)
        database.query(
            "tracks", TRACK_COLUMNS, selection, args, null, null,
            "album COLLATE NOCASE, disc_number, track_number, title COLLATE NOCASE, id",
        ).use { cursor -> buildList { while (cursor.moveToNext()) add(cursor.offlineTrack()) } }
    }

    fun track(id: String): OfflineTrack? = synchronized(lock) { trackLocked(id) }

    fun trackByDigest(sha256: String, quality: String): OfflineTrack? = synchronized(lock) {
        if (!SHA256.matches(sha256)) invalid("Checksum must be lowercase SHA-256.")
        if (quality != "original" && quality != "aac_256") invalid("Unsupported download quality '$quality'.")
        ensureFilesystemJournalsReconciledLocked()
        val item = trackByDigestLocked(sha256, quality) ?: return@synchronized null
        if (item.pendingDelete) return@synchronized null
        try {
            val file = safeMusicPath(item.relativePath, mustExist = true)
            requireRegularUnlinkedFile(file)
            if (file.length() != item.byteSize || this.sha256(file) != item.sha256) {
                recordErrorLocked(item.id, "integrity", "Stored audio no longer matches its checksum.")
                return@synchronized null
            }
        } catch (error: Exception) {
            recordErrorLocked(item.id, "integrity", "Stored audio could not be verified: ${safeMessage(error)}")
            return@synchronized null
        }
        item
    }

    fun folders(parentId: String = ROOT_FOLDER_ID): List<OfflineFolder> = synchronized(lock) {
        database.query(
            "folders", FOLDER_COLUMNS, "parent_id=? AND pending_delete=0", arrayOf(parentId),
            null, null, "name COLLATE NOCASE, id",
        ).use { cursor -> buildList { while (cursor.moveToNext()) add(cursor.offlineFolder()) } }
    }

    fun folder(id: String): OfflineFolder? = synchronized(lock) { folderRecordLocked(id, includePending = false)?.folder }

    fun createFolder(parentId: String, name: String): OfflineFolder = synchronized(lock) {
        ensureFilesystemJournalsReconciledLocked()
        val parent = requireFolderLocked(parentId)
        val safeName = requireComponent(name, "folder name")
        val relative = joinRelative(parent.relativePath, safeName)
        val target = safeMusicPath(relative, mustExist = false)
        ensureNoFolderCollisionLocked(parentId, safeName, null)
        if (pathOccupied(target)) conflict("A file or folder named '$safeName' already exists.")
        val result = OfflineFolder(UUID.randomUUID().toString(), parentId, safeName)
        val operationId = UUID.randomUUID().toString()
        putOperationLocked(operationId, OP_CREATE_FOLDER, JSONObject()
            .put("folder_id", result.id)
            .put("parent_id", parentId)
            .put("name", safeName)
            .put("relative_path", relative))
        try {
            if (!target.mkdir()) {
                deleteOperationLocked(operationId)
                throw storageFailure("Could not create folder '$safeName'.")
            }
            transaction(database) {
                insertFolderLocked(result, relative)
                deleteOperationLocked(operationId)
            }
        } catch (error: OfflineLibraryException) {
            throw error
        } catch (error: Exception) {
            throw storageFailure("Could not register folder '$safeName'.", error)
        }
        changedLocked()
        result
    }

    fun renameFolder(id: String, name: String) = synchronized(lock) {
        ensureFilesystemJournalsReconciledLocked()
        val current = requireFolderLocked(id)
        if (id == ROOT_FOLDER_ID) invalidPath("The music root cannot be renamed.")
        val safeName = requireComponent(name, "folder name")
        if (safeName == current.folder.name) return@synchronized
        moveFolderLocked(current, requireFolderLocked(current.folder.parentId ?: ROOT_FOLDER_ID), safeName)
    }

    fun moveFolder(id: String, parentId: String, newName: String? = null) = synchronized(lock) {
        ensureFilesystemJournalsReconciledLocked()
        if (id == ROOT_FOLDER_ID) invalidPath("The music root cannot be moved.")
        val current = requireFolderLocked(id)
        val parent = requireFolderLocked(parentId)
        if (id == parentId || isDescendantLocked(parentId, id)) {
            invalidPath("A folder cannot be moved into itself or one of its descendants.")
        }
        moveFolderLocked(current, parent, newName?.let { requireComponent(it, "folder name") } ?: current.folder.name)
    }

    fun moveTrack(id: String, folderId: String, newName: String? = null) = synchronized(lock) {
        ensureFilesystemJournalsReconciledLocked()
        val current = trackLocked(id) ?: missing("Track '$id' does not exist.")
        if (current.pendingDelete) missing("Track '$id' is pending deletion.")
        val folder = requireFolderLocked(folderId)
        val source = safeMusicPath(current.relativePath, mustExist = true)
        requireRegularUnlinkedFile(source)
        val fileName = newName?.let { requireComponent(it, "file name") } ?: source.name
        val destinationRelative = joinRelative(folder.relativePath, fileName)
        if (current.relativePath == destinationRelative) return@synchronized
        val destination = safeMusicPath(destinationRelative, mustExist = false)
        if (pathOccupied(destination) || trackAtPathLocked(destinationRelative, id)) {
            conflict("A file named '$fileName' already exists in that folder.")
        }
        val operationId = UUID.randomUUID().toString()
        putOperationLocked(operationId, OP_MOVE_TRACK, JSONObject()
            .put("track_id", id)
            .put("source", current.relativePath)
            .put("destination", destinationRelative)
            .put("folder_id", folderId))
        try {
            atomicMove(source, destination)
            transaction(database) {
                database.update("tracks", ContentValues().apply {
                    put("folder_id", folderId)
                    put("relative_path", destinationRelative)
                }, "id=?", arrayOf(id))
                deleteOperationLocked(operationId)
            }
        } catch (error: Exception) {
            runCatching { ensureFilesystemJournalsReconciledLocked() }
            if (trackLocked(id)?.relativePath != destinationRelative) {
                throw storageFailure("Could not move '${source.name}'. The original was preserved when possible.", error)
            }
        }
        changedLocked()
    }

    fun deleteTracks(ids: List<String>): OfflineDeleteResult = synchronized(lock) {
        ensureFilesystemJournalsReconciledLocked()
        deleteTracksLocked(ids.distinct())
    }

    fun deleteFolder(id: String): OfflineDeleteResult = synchronized(lock) {
        ensureFilesystemJournalsReconciledLocked()
        if (id == ROOT_FOLDER_ID || id == IMPORT_FOLDER_ID || isDescendantLocked(IMPORT_FOLDER_ID, id)) {
            invalidPath("The root and Imported music folders cannot be deleted, directly or through a parent.")
        }
        val folder = requireFolderLocked(id)
        val directory = safeMusicPath(folder.relativePath, mustExist = true)
        rejectSymlinksRecursively(directory)
        val operationId = UUID.randomUUID().toString()
        putOperationLocked(operationId, OP_DELETE_FOLDER, JSONObject().put("folder_id", id))
        val result = continueFolderDeleteLocked(operationId, folder)
        if (result.deletedIds.isNotEmpty() || result.deferredIds.isNotEmpty() || result.failures.isEmpty()) changedLocked()
        result
    }

    fun importTrack(
        source: File,
        metadata: JSONObject,
        folderId: String = IMPORT_FOLDER_ID,
        artwork: File? = null,
        commitGate: (authorize: () -> Unit) -> Boolean = { authorize -> authorize(); true },
    ): OfflineTrack = synchronized(lock) {
        ensureFilesystemJournalsReconciledLocked()
        val parsed = parseImport(metadata)
        val folder = requireFolderLocked(folderId)
        requireRegularUnlinkedFile(source)
        rejectManagedImportSource(source)
        if (source.length() != parsed.byteSize) integrity("The downloaded file length does not match its manifest.")
        val actualHash = sha256(source)
        if (actualHash != parsed.sha256) integrity("The downloaded file checksum does not match its manifest.")
        if (artwork != null) {
            requireRegularUnlinkedFile(artwork)
            rejectManagedImportSource(artwork)
        }

        trackByDigestLocked(parsed.sha256, parsed.quality)?.let { existing ->
            if (existing.pendingDelete) {
                conflict("An identical saved track is pending deletion; wait for deletion to finish before importing it again.")
            }
            val existingFile = safeMusicPath(existing.relativePath, mustExist = true)
            if (existing.byteSize != existingFile.length() || sha256(existingFile) != existing.sha256) {
                recordErrorLocked(existing.id, "integrity", "Stored audio no longer matches its checksum.")
                integrity("An existing matching track is corrupt; delete it before importing again.")
            }
            if (!runImportCommitGate(commitGate) {}) {
                throw OfflineLibraryException("cancelled", "The import was cancelled before local commit.")
            }
            consumeImportedSource(source)
            artwork?.let(::consumeImportedSource)
            return@synchronized existing
        }

        val artworkBytes = artwork?.length() ?: 0L
        if (parsed.byteSize > Long.MAX_VALUE - artworkBytes) {
            throw OfflineLibraryException("storage_full", "The import size exceeds supported device storage.")
        }
        enforceCapacityLocked(parsed.byteSize + artworkBytes)
        val localId = UUID.randomUUID().toString()
        val extension = mediaExtension(parsed.mime, source.extension)
        val fileName = "$localId.$extension"
        val destinationRelative = joinRelative(folder.relativePath, fileName)
        val finalAudio = safeMusicPath(destinationRelative, mustExist = false)
        val operationId = UUID.randomUUID().toString()
        val stagedAudio = File(finalAudio.parentFile, ".incoming-$operationId")
        val artworkExtension = artwork?.let { safeArtworkExtension(it.extension) }
        val artworkRelative = artworkExtension?.let { "$localId.$it" }
        val finalArtwork = artworkRelative?.let { File(artworkRoot, it) }
        val stagedArtwork = finalArtwork?.let { File(artworkRoot, ".incoming-$operationId-art") }
        if (pathOccupied(finalAudio) || pathOccupied(stagedAudio) ||
            finalArtwork?.let(::pathOccupied) == true || stagedArtwork?.let(::pathOccupied) == true
        ) {
            conflict("A generated local file name unexpectedly collided.")
        }
        val track = parsed.toTrack(localId, folderId, destinationRelative, finalArtwork?.absolutePath)
        val details = JSONObject()
            .put("track", trackToJson(track))
            .put("staged_audio", stagedAudio.absolutePath)
            .put("final_audio", finalAudio.absolutePath)
            .put("staged_artwork", stagedArtwork?.absolutePath ?: "")
            .put("final_artwork", finalArtwork?.absolutePath ?: "")
            .put("commit_authorized", false)
        putOperationLocked(operationId, OP_IMPORT, details)
        try {
            copyAndSync(source, stagedAudio)
            if (stagedAudio.length() != parsed.byteSize || sha256(stagedAudio) != parsed.sha256) {
                integrity("The durable copy failed checksum verification.")
            }
            val allowed = try {
                runImportCommitGate(commitGate) {
                    authorizeImportCommitLocked(operationId, details)
                }
            } catch (error: OfflineLibraryException) {
                discardCancelledImportLocked(operationId, stagedAudio, stagedArtwork)
                throw error
            }
            if (!allowed) {
                discardCancelledImportLocked(operationId, stagedAudio, stagedArtwork)
                throw OfflineLibraryException("cancelled", "The import was cancelled before local commit.")
            }
            authorizeImportCommitLocked(operationId, details)
            atomicMove(stagedAudio, finalAudio)
            if (stagedArtwork != null && finalArtwork != null) atomicMove(stagedArtwork, finalArtwork)
            transaction(database) {
                insertTrackLocked(track)
                deleteOperationLocked(operationId)
            }
        } catch (error: OfflineLibraryException) {
            throw error
        } catch (error: Exception) {
            throw storageFailure("Could not commit the verified download.", error)
        }
        consumeImportedSource(source)
        artwork?.let(::consumeImportedSource)
        changedLocked()
        track
    }

    fun playlists(): List<OfflinePlaylist> = synchronized(lock) {
        database.query("playlists", arrayOf("id"), null, null, null, null, "name COLLATE NOCASE, id").use { cursor ->
            buildList { while (cursor.moveToNext()) playlistLocked(cursor.getString(0))?.let(::add) }
        }
    }

    fun playlist(id: String): OfflinePlaylist? = synchronized(lock) { playlistLocked(id) }

    fun savePlaylist(
        id: String?,
        name: String,
        trackIds: List<String>,
        missingTitles: List<String> = emptyList(),
    ): OfflinePlaylist = synchronized(lock) {
        val safeName = requireDisplayText(name, "playlist name", 200)
        trackIds.forEach { trackId ->
            val item = trackLocked(trackId) ?: missing("Track '$trackId' does not exist.")
            if (item.pendingDelete) missing("Track '$trackId' is pending deletion.")
        }
        val safeMissing = missingTitles.map { requireDisplayText(it, "missing track title", 500) }
        val playlistId = id ?: UUID.randomUUID().toString()
        if (id != null && playlistLocked(id) == null) missing("Playlist '$id' does not exist.")
        transaction(database) {
            if (id == null) {
                database.insertOrThrow("playlists", null, ContentValues().apply {
                    put("id", playlistId)
                    put("name", safeName)
                })
            } else {
                database.update("playlists", ContentValues().apply { put("name", safeName) }, "id=?", arrayOf(playlistId))
                database.delete("playlist_entries", "playlist_id=?", arrayOf(playlistId))
                database.delete("playlist_missing", "playlist_id=?", arrayOf(playlistId))
            }
            trackIds.forEachIndexed { position, trackId ->
                database.insertOrThrow("playlist_entries", null, ContentValues().apply {
                    put("playlist_id", playlistId)
                    put("position", position)
                    put("track_id", trackId)
                })
            }
            safeMissing.forEachIndexed { position, title ->
                database.insertOrThrow("playlist_missing", null, ContentValues().apply {
                    put("playlist_id", playlistId)
                    put("position", position)
                    put("title", title)
                })
            }
        }
        changedLocked()
        OfflinePlaylist(playlistId, safeName, trackIds.toList(), safeMissing)
    }

    fun savePlaylistSnapshotOnce(
        id: String,
        name: String,
        trackIds: List<String>,
        missingTitles: List<String> = emptyList(),
    ): OfflinePlaylist? = synchronized(lock) {
        val receiptId = requireUuid(id, "playlist snapshot receipt")
        snapshotPlaylistIdLocked(receiptId)?.let { playlistId ->
            return@synchronized playlistLocked(playlistId)
        }
        val safeName = requireDisplayText(name, "playlist name", 200)
        trackIds.forEach { trackId ->
            val item = trackLocked(trackId) ?: missing("Track '$trackId' does not exist.")
            if (item.pendingDelete) missing("Track '$trackId' is pending deletion.")
        }
        val safeMissing = missingTitles.map { requireDisplayText(it, "missing track title", 500) }
        val playlistId = UUID.randomUUID().toString()
        transaction(database) {
            database.insertOrThrow("playlists", null, ContentValues().apply {
                put("id", playlistId)
                put("name", safeName)
            })
            trackIds.forEachIndexed { position, trackId ->
                database.insertOrThrow("playlist_entries", null, ContentValues().apply {
                    put("playlist_id", playlistId)
                    put("position", position)
                    put("track_id", trackId)
                })
            }
            safeMissing.forEachIndexed { position, title ->
                database.insertOrThrow("playlist_missing", null, ContentValues().apply {
                    put("playlist_id", playlistId)
                    put("position", position)
                    put("title", title)
                })
            }
            database.insertOrThrow("playlist_snapshot_receipts", null, ContentValues().apply {
                put("id", receiptId)
                put("playlist_id", playlistId)
            })
        }
        changedLocked()
        OfflinePlaylist(playlistId, safeName, trackIds.toList(), safeMissing)
    }

    fun acknowledgePlaylistSnapshot(id: String) = synchronized(lock) {
        database.delete("playlist_snapshot_receipts", "id=?", arrayOf(requireUuid(id, "playlist snapshot receipt")))
    }

    fun deletePlaylist(id: String) = synchronized(lock) {
        if (database.delete("playlists", "id=?", arrayOf(id)) == 0) missing("Playlist '$id' does not exist.")
        changedLocked()
    }

    fun loadQueue(): OfflineQueue = synchronized(lock) { loadQueueLocked() }

    fun saveQueue(queue: OfflineQueue) = synchronized(lock) {
        val before = loadQueueLocked()
        val entriesChanged = before.entries != queue.entries
        val entryIds = HashSet<String>()
        queue.entries.forEach { entry ->
            if (entry.id.isBlank() || !entryIds.add(entry.id)) invalid("Queue entry IDs must be non-empty and unique.")
            if (entriesChanged) {
                val item = trackLocked(entry.trackId) ?: missing("Track '${entry.trackId}' does not exist.")
                if (item.pendingDelete) missing("Track '${entry.trackId}' is pending deletion.")
            }
        }
        if (queue.currentEntryId != null && queue.currentEntryId !in entryIds) {
            invalid("The current queue entry is not in the queue.")
        }
        if (queue.positionMs < 0) invalid("Queue position cannot be negative.")
        if (queue.repeatMode !in 0..2) invalid("Queue repeat mode is invalid.")
        transaction(database) {
            if (entriesChanged) {
                database.delete("queue_entries", null, null)
                queue.entries.forEachIndexed { position, entry ->
                    database.insertOrThrow("queue_entries", null, ContentValues().apply {
                        put("entry_id", entry.id)
                        put("position", position)
                        put("track_id", entry.trackId)
                    })
                }
            }
            database.update("queue_state", ContentValues().apply {
                if (queue.currentEntryId == null) putNull("current_entry_id") else put("current_entry_id", queue.currentEntryId)
                put("position_ms", queue.positionMs)
                put("shuffle", if (queue.shuffle) 1 else 0)
                put("repeat_mode", queue.repeatMode)
            }, "singleton=1", null)
        }
        if (entriesChanged || before.currentEntryId != queue.currentEntryId ||
            before.shuffle != queue.shuffle || before.repeatMode != queue.repeatMode
        ) changedLocked()
    }

    fun usage(): OfflineStorageUsage = synchronized(lock) {
        val bytes = database.rawQuery("SELECT COALESCE(SUM(byte_size + artwork_size),0) FROM tracks", null).use { cursor ->
            cursor.moveToFirst()
            cursor.getLong(0)
        }
        val limit = settingLongLocked(SETTING_LIMIT)
        OfflineStorageUsage(bytes, StatFs(base.absolutePath).availableBytes, limit)
    }

    fun setLimitBytes(bytes: Long) = synchronized(lock) {
        if (bytes < 0) invalid("Storage limit cannot be negative.")
        database.insertWithOnConflict("settings", null, ContentValues().apply {
            put("key", SETTING_LIMIT)
            put("long_value", bytes)
        }, SQLiteDatabase.CONFLICT_REPLACE)
        changedLocked()
    }

    fun openAudio(id: String): OfflineAudioHandle = synchronized(lock) {
        ensureFilesystemJournalsReconciledLocked()
        val item = trackLocked(id) ?: missing("Track '$id' does not exist.")
        if (item.pendingDelete && (pins[id] ?: 0) == 0) missing("Track '$id' is pending deletion.")
        val file = safeMusicPath(item.relativePath, mustExist = true)
        requireRegularUnlinkedFile(file)
        if (file.length() != item.byteSize) {
            recordErrorLocked(id, "integrity", "Stored audio length changed.")
            integrity("Stored audio length does not match the library.")
        }
        val stream = try {
            FileInputStream(file)
        } catch (error: IOException) {
            throw storageFailure("Could not open stored audio.", error)
        }
        pins[id] = (pins[id] ?: 0) + 1
        OfflineAudioHandle(stream, item.byteSize) { releaseAudio(id) }
    }

    fun retainTrack(id: String): Closeable = synchronized(lock) {
        ensureFilesystemJournalsReconciledLocked()
        val item = trackLocked(id) ?: missing("Track '$id' does not exist.")
        if (item.pendingDelete) missing("Track '$id' is pending deletion.")
        pins[id] = (pins[id] ?: 0) + 1
        val closeLock = Any()
        var closed = false
        Closeable {
            val shouldRelease = synchronized(closeLock) {
                if (closed) false else {
                    closed = true
                    true
                }
            }
            if (shouldRelease) releaseAudio(id)
        }
    }
    fun recordError(trackId: String?, code: String, message: String) = synchronized(lock) {
        recordErrorLocked(trackId, requireDisplayText(code, "error code", 100), requireDisplayText(message, "error message", 2000))
    }

    fun errors(): List<JSONObject> = synchronized(lock) {
        database.query(
            "local_errors", arrayOf("id", "track_id", "code", "message", "created_at"),
            null, null, null, null, "id DESC", "200",
        ).use { cursor ->
            buildList {
                while (cursor.moveToNext()) {
                    add(JSONObject()
                        .put("id", cursor.getLong(0))
                        .put("track_id", cursor.nullableString(1) ?: JSONObject.NULL)
                        .put("code", cursor.getString(2))
                        .put("message", cursor.getString(3))
                        .put("created_at", cursor.getLong(4)))
                }
            }
        }
    }

    private fun continueFolderDeleteLocked(operationId: String, folder: FolderRecord): OfflineDeleteResult {
        val directory = safeMusicPath(folder.relativePath, mustExist = false)
        if (directory.exists()) rejectSymlinksRecursively(directory)
        val descendants = descendantFolderRecordsLocked(folder.relativePath)
        if (descendants.any { it.folder.id == IMPORT_FOLDER_ID }) {
            deleteOperationLocked(operationId)
            invalidPath("The Imported music folder cannot be deleted through a parent.")
        }
        val descendantIds = descendants.map { it.folder.id }.toSet()
        val trackIds = if (descendantIds.isEmpty()) emptyList() else database.query(
            "tracks", arrayOf("id"),
            descendantIds.joinToString(prefix = "folder_id IN (", postfix = ")") { "?" },
            descendantIds.toTypedArray(), null, null, "id",
        ).use { cursor -> buildList { while (cursor.moveToNext()) add(cursor.getString(0)) } }
        val result = deleteTracksLocked(trackIds)
        if (result.failures.isNotEmpty()) {
            transaction(database) {
                descendants.forEach { record ->
                    database.update("folders", ContentValues().apply { put("pending_delete", 0) }, "id=?", arrayOf(record.folder.id))
                }
                deleteOperationLocked(operationId)
            }
            return result
        }
        if (result.deferredIds.isNotEmpty()) {
            transaction(database) {
                descendants.forEach { record ->
                    database.update("folders", ContentValues().apply { put("pending_delete", 1) }, "id=?", arrayOf(record.folder.id))
                }
            }
            return result
        }
        return try {
            if (directory.exists()) deleteTreeWithinRoot(directory)
            transaction(database) {
                descendants.sortedByDescending { it.relativePath.length }.forEach { record ->
                    database.delete("folders", "id=?", arrayOf(record.folder.id))
                }
                deleteOperationLocked(operationId)
            }
            result
        } catch (error: Exception) {
            transaction(database) {
                descendants.forEach { record ->
                    database.update("folders", ContentValues().apply { put("pending_delete", 0) }, "id=?", arrayOf(record.folder.id))
                }
                deleteOperationLocked(operationId)
            }
            result.copy(failures = result.failures + (folder.folder.id to "Could not remove the folder: ${safeMessage(error)}"))
        }
    }

    private fun moveFolderLocked(current: FolderRecord, parent: FolderRecord, name: String) {
        ensureNoFolderCollisionLocked(parent.folder.id, name, current.folder.id)
        val destinationRelative = joinRelative(parent.relativePath, name)
        if (current.relativePath == destinationRelative && current.folder.parentId == parent.folder.id) return
        val source = safeMusicPath(current.relativePath, mustExist = true)
        val destination = safeMusicPath(destinationRelative, mustExist = false)
        rejectSymlinksRecursively(source)
        if (pathOccupied(destination)) conflict("A file or folder named '$name' already exists.")
        val operationId = UUID.randomUUID().toString()
        putOperationLocked(operationId, OP_MOVE_FOLDER, JSONObject()
            .put("folder_id", current.folder.id)
            .put("source", current.relativePath)
            .put("destination", destinationRelative)
            .put("parent_id", parent.folder.id)
            .put("name", name))
        try {
            atomicMove(source, destination)
            transaction(database) {
                applyFolderMoveLocked(current.folder.id, current.relativePath, destinationRelative, parent.folder.id, name)
                deleteOperationLocked(operationId)
            }
        } catch (error: Exception) {
            runCatching { ensureFilesystemJournalsReconciledLocked() }
            if (folderRecordLocked(current.folder.id, includePending = true)?.relativePath != destinationRelative) {
                throw storageFailure("Could not move folder '${current.folder.name}'. The original was preserved when possible.", error)
            }
        }
        changedLocked()
    }

    private fun deleteTracksLocked(ids: List<String>): OfflineDeleteResult {
        val deleted = ArrayList<String>()
        val deferred = ArrayList<String>()
        val failures = LinkedHashMap<String, String>()
        ids.forEach { id ->
            val item = trackLocked(id)
            if (item == null) {
                failures[id] = "Track does not exist."
                return@forEach
            }
            if ((pins[id] ?: 0) > 0) {
                markTrackPendingLocked(id)
                deferred += id
                return@forEach
            }
            try {
                completeTrackDeleteLocked(item)
                deleted += id
            } catch (error: Exception) {
                failures[id] = safeMessage(error)
                recordErrorLocked(id, "delete_failed", safeMessage(error))
            }
        }
        if (deleted.isNotEmpty() || deferred.isNotEmpty()) changedLocked()
        return OfflineDeleteResult(deleted, deferred, failures)
    }

    private fun markTrackPendingLocked(id: String) {
        transaction(database) {
            database.update("tracks", ContentValues().apply { put("pending_delete", 1) }, "id=?", arrayOf(id))
            removeReferencesLocked(id)
        }
    }

    private fun completeTrackDeleteLocked(item: OfflineTrack) {
        val source = safeMusicPath(item.relativePath, mustExist = false)
        if (source.exists()) requireRegularUnlinkedFile(source)
        val artwork = item.artworkPath?.let(::safeArtworkPath)
        if (artwork?.exists() == true) requireRegularUnlinkedFile(artwork)
        val operationId = UUID.randomUUID().toString()
        val trash = File(trashRoot, "$operationId-audio")
        val artworkTrash = File(trashRoot, "$operationId-artwork")
        putOperationLocked(operationId, OP_DELETE_TRACK, JSONObject()
            .put("track_id", item.id)
            .put("source", item.relativePath)
            .put("trash", trash.absolutePath)
            .put("artwork", artwork?.absolutePath ?: "")
            .put("artwork_trash", artworkTrash.absolutePath))
        if (source.exists()) atomicMove(source, trash)
        if (artwork?.exists() == true) atomicMove(artwork, artworkTrash)
        transaction(database) {
            removeReferencesLocked(item.id)
            database.delete("tracks", "id=?", arrayOf(item.id))
            deleteOperationLocked(operationId)
        }
        if (trash.exists() && !trash.delete()) {
            recordErrorLocked(null, "temporary_cleanup_failed", "Deleted audio trash could not be removed.")
        }
        if (artworkTrash.exists() && !artworkTrash.delete()) {
            recordErrorLocked(null, "temporary_cleanup_failed", "Deleted artwork trash could not be removed.")
        }
        cleanupPendingFoldersLocked()
    }

    private fun removeReferencesLocked(trackId: String) {
        val current = database.rawQuery(
            "SELECT qs.current_entry_id FROM queue_state qs JOIN queue_entries qe ON qe.entry_id=qs.current_entry_id WHERE qs.singleton=1 AND qe.track_id=?",
            arrayOf(trackId),
        ).use { cursor -> if (cursor.moveToFirst()) cursor.getString(0) else null }
        database.delete("playlist_entries", "track_id=?", arrayOf(trackId))
        database.delete("queue_entries", "track_id=?", arrayOf(trackId))
        if (current != null) {
            database.update("queue_state", ContentValues().apply {
                putNull("current_entry_id")
                put("position_ms", 0)
            }, "singleton=1", null)
        }
    }

    private fun releaseAudio(id: String) = synchronized(lock) {
        val count = pins[id] ?: return@synchronized
        if (count > 1) {
            pins[id] = count - 1
            return@synchronized
        }
        pins.remove(id)
        val item = trackLocked(id)
        if (item?.pendingDelete == true) {
            try {
                completeTrackDeleteLocked(item)
                changedLocked()
            } catch (error: Exception) {
                recordErrorLocked(id, "deferred_delete_failed", safeMessage(error))
            }
        }
    }

    private fun snapshotPlaylistIdLocked(id: String): String? = database.query(
        "playlist_snapshot_receipts", arrayOf("playlist_id"), "id=?", arrayOf(id), null, null, null,
    ).use { cursor -> if (cursor.moveToFirst()) cursor.getString(0) else null }

    private fun playlistLocked(id: String): OfflinePlaylist? {
        val name = database.query("playlists", arrayOf("name"), "id=?", arrayOf(id), null, null, null).use { cursor ->
            if (cursor.moveToFirst()) cursor.getString(0) else null
        } ?: return null
        val trackIds = database.query(
            "playlist_entries", arrayOf("track_id"), "playlist_id=?", arrayOf(id), null, null, "position, rowid",
        ).use { cursor -> buildList { while (cursor.moveToNext()) add(cursor.getString(0)) } }
        val missing = database.query(
            "playlist_missing", arrayOf("title"), "playlist_id=?", arrayOf(id), null, null, "position, rowid",
        ).use { cursor -> buildList { while (cursor.moveToNext()) add(cursor.getString(0)) } }
        return OfflinePlaylist(id, name, trackIds, missing)
    }

    private fun loadQueueLocked(): OfflineQueue {
        val entries = database.query(
            "queue_entries", arrayOf("entry_id", "track_id"), null, null, null, null, "position, rowid",
        ).use { cursor -> buildList { while (cursor.moveToNext()) add(OfflineQueueEntry(cursor.getString(0), cursor.getString(1))) } }
        return database.query(
            "queue_state", arrayOf("current_entry_id", "position_ms", "shuffle", "repeat_mode"),
            "singleton=1", null, null, null, null,
        ).use { cursor ->
            cursor.moveToFirst()
            OfflineQueue(entries, cursor.nullableString(0), cursor.getLong(1), cursor.getInt(2) != 0, cursor.getInt(3))
        }
    }

    private fun bootstrapFolderRowsLocked() {
        transaction(database) {
            database.insertWithOnConflict("folders", null, ContentValues().apply {
                put("id", ROOT_FOLDER_ID)
                putNull("parent_id")
                put("name", "Music")
                put("relative_path", "")
                put("pending_delete", 0)
            }, SQLiteDatabase.CONFLICT_IGNORE)
            database.insertWithOnConflict("folders", null, ContentValues().apply {
                put("id", IMPORT_FOLDER_ID)
                put("parent_id", ROOT_FOLDER_ID)
                put("name", "Imported music")
                put("relative_path", "Imported music")
                put("pending_delete", 0)
            }, SQLiteDatabase.CONFLICT_IGNORE)
        }
    }

    private fun ensureReservedFolderDirectoriesLocked() {
        val imported = folderRecordLocked(IMPORT_FOLDER_ID, includePending = true)
            ?: throw storageFailure("Imported music folder is missing.")
        val directory = safeMusicPath(imported.relativePath, mustExist = false)
        if (!pathOccupied(directory) && !directory.mkdir()) throw storageFailure("Could not create Imported music folder.")
        ensureDirectory(directory)
    }

    private fun recoverLocked() {
        val operations = database.query(
            "operations", arrayOf("id", "kind", "details"), null, null, null, null, "created_at, id",
        ).use { cursor -> buildList {
            while (cursor.moveToNext()) add(Triple(cursor.getString(0), cursor.getString(1), cursor.getString(2)))
        } }
        operations.forEach { (id, kind, raw) ->
            try {
                val details = JSONObject(raw)
                when (kind) {
                    OP_IMPORT -> recoverImportLocked(id, details)
                    OP_CREATE_FOLDER -> recoverFolderCreateLocked(id, details)
                    OP_MOVE_TRACK -> recoverTrackMoveLocked(id, details)
                    OP_MOVE_FOLDER -> recoverFolderMoveLocked(id, details)
                    OP_DELETE_TRACK -> recoverTrackDeleteLocked(id, details)
                    OP_DELETE_FOLDER -> recoverFolderDeleteLocked(id, details)
                    else -> deleteOperationLocked(id)
                }
            } catch (error: Exception) {
                recordErrorLocked(null, "recovery_failed", "Could not recover $kind operation: ${safeMessage(error)}")
            }
        }
        database.query("tracks", TRACK_COLUMNS, "pending_delete=1", null, null, null, null).use { cursor ->
            val pending = buildList { while (cursor.moveToNext()) add(cursor.offlineTrack()) }
            pending.forEach { item ->
                if ((pins[item.id] ?: 0) == 0) runCatching { completeTrackDeleteLocked(item) }
                    .onFailure { recordErrorLocked(item.id, "deferred_delete_failed", safeMessage(it)) }
            }
        }
        cleanupPendingFoldersLocked()
        cleanupTrashLocked()
    }

    private fun ensureFilesystemJournalsReconciledLocked() {
        val pendingBefore = database.rawQuery(
            "SELECT COUNT(*) FROM operations WHERE kind<>?",
            arrayOf(OP_DELETE_FOLDER),
        ).use { cursor ->
            cursor.moveToFirst()
            cursor.getLong(0)
        }
        if (pendingBefore == 0L) return
        recoverLocked()
        val pendingAfter = database.rawQuery(
            "SELECT COUNT(*) FROM operations WHERE kind<>?",
            arrayOf(OP_DELETE_FOLDER),
        ).use { cursor ->
            cursor.moveToFirst()
            cursor.getLong(0)
        }
        if (pendingAfter != 0L) {
            throw OfflineLibraryException(
                "recovery_required",
                "An interrupted storage operation must be resolved before changing saved music.",
            )
        }
        changedLocked()
    }

    private fun recoverFolderCreateLocked(operationId: String, details: JSONObject) {
        val folderId = details.getString("folder_id")
        if (folderRecordLocked(folderId, includePending = true) != null) {
            deleteOperationLocked(operationId)
            return
        }
        val parentId = details.getString("parent_id")
        if (folderRecordLocked(parentId, includePending = false) == null) {
            deleteOperationLocked(operationId)
            recordErrorLocked(null, "folder_create_recovery_failed", "The parent of an interrupted folder creation no longer exists.")
            return
        }
        val target = safeMusicPath(details.getString("relative_path"), mustExist = false)
        if (pathOccupied(target) && !target.isDirectory) {
            deleteOperationLocked(operationId)
            recordErrorLocked(null, "folder_create_recovery_conflict", "An interrupted folder creation found a conflicting path.")
            return
        }
        if (!pathOccupied(target) && !target.mkdir()) throw storageFailure("Could not recover folder creation.")
        val folder = OfflineFolder(folderId, parentId, details.getString("name"))
        transaction(database) {
            insertFolderLocked(folder, details.getString("relative_path"))
            deleteOperationLocked(operationId)
        }
    }

    private fun recoverFolderDeleteLocked(operationId: String, details: JSONObject) {
        val folder = folderRecordLocked(details.getString("folder_id"), includePending = true)
        if (folder == null) {
            deleteOperationLocked(operationId)
            return
        }
        continueFolderDeleteLocked(operationId, folder)
    }

    private fun recoverImportLocked(operationId: String, details: JSONObject) {
        val finalAudio = safeAbsoluteMusicPath(details.getString("final_audio"), mustExist = false)
        val stagedAudio = safeAbsoluteMusicPath(details.getString("staged_audio"), mustExist = false)
        if (!details.optBoolean("commit_authorized", false)) {
            discardInterruptedImportLocked(operationId, details, stagedAudio, finalAudio)
            return
        }
        val track = jsonToTrack(details.getJSONObject("track"))
        if (trackLocked(track.id) != null) {
            deleteOperationLocked(operationId)
            return
        }
        if (!pathOccupied(finalAudio) && pathOccupied(stagedAudio)) {
            if (stagedAudio.length() != track.byteSize || sha256(stagedAudio) != track.sha256) {
                discardInterruptedImportLocked(operationId, details, stagedAudio, finalAudio)
                recordErrorLocked(null, "import_recovery_failed", "An interrupted import failed integrity verification.")
                return
            }
            atomicMove(stagedAudio, finalAudio)
        }
        if (!pathOccupied(finalAudio)) {
            discardInterruptedImportLocked(operationId, details, stagedAudio, finalAudio)
            return
        }
        if (finalAudio.length() != track.byteSize || sha256(finalAudio) != track.sha256) {
            discardInterruptedImportLocked(operationId, details, stagedAudio, finalAudio)
            recordErrorLocked(null, "import_recovery_failed", "An interrupted import's committed file failed integrity verification.")
            return
        }
        var recovered = track
        val finalArtworkValue = details.optString("final_artwork")
        val stagedArtworkValue = details.optString("staged_artwork")
        if (finalArtworkValue.isNotEmpty()) {
            val finalArtwork = safeArtworkPath(finalArtworkValue)
            val stagedArtwork = safeArtworkPath(stagedArtworkValue)
            if (!finalArtwork.exists() && stagedArtwork.exists()) atomicMove(stagedArtwork, finalArtwork)
            if (!finalArtwork.exists()) recovered = recovered.copy(artworkPath = null)
        }
        trackByDigestLocked(track.sha256, track.quality)?.let {
            finalAudio.delete()
            recovered.artworkPath?.let { path -> runCatching { safeArtworkPath(path).delete() } }
            deleteOperationLocked(operationId)
            return
        }
        transaction(database) {
            insertTrackLocked(recovered)
            deleteOperationLocked(operationId)
        }
    }

    private fun authorizeImportCommitLocked(operationId: String, details: JSONObject) {
        details.put("commit_authorized", true)
        val updated = database.update(
            "operations",
            ContentValues().apply { put("details", details.toString()) },
            "id=?",
            arrayOf(operationId),
        )
        if (updated != 1) throw storageFailure("Could not durably authorize the verified download commit.")
    }

    private fun runImportCommitGate(
        commitGate: (authorize: () -> Unit) -> Boolean,
        authorize: () -> Unit,
    ): Boolean {
        var authorizationInvocations = 0
        var authorized = false
        var authorizationFailure: OfflineLibraryException? = null
        val allowed = try {
            commitGate {
                authorizationInvocations += 1
                if (authorizationInvocations != 1) {
                    throw OfflineLibraryException("cancelled", "The local commit authorization was invoked more than once.")
                }
                try {
                    authorize()
                    authorized = true
                } catch (error: OfflineLibraryException) {
                    authorizationFailure = error
                    throw error
                }
            }
        } catch (error: Exception) {
            authorizationFailure?.let { throw it }
            throw OfflineLibraryException("cancelled", "The import was cancelled before local commit.", error)
        }
        authorizationFailure?.let { throw it }
        if (allowed && (!authorized || authorizationInvocations != 1)) {
            throw OfflineLibraryException("cancelled", "The import commit gate did not authorize local commit.")
        }
        return allowed
    }

    private fun discardCancelledImportLocked(operationId: String, stagedAudio: File, stagedArtwork: File?) {
        try {
            Files.deleteIfExists(stagedAudio.toPath())
            stagedArtwork?.let { Files.deleteIfExists(it.toPath()) }
            deleteOperationLocked(operationId)
        } catch (error: Exception) {
            throw storageFailure("Could not cleanly cancel the staged import.", error)
        }
    }

    private fun discardInterruptedImportLocked(
        operationId: String,
        details: JSONObject,
        stagedAudio: File,
        finalAudio: File,
    ) {
        stagedAudio.delete()
        finalAudio.delete()
        listOf(details.optString("staged_artwork"), details.optString("final_artwork"))
            .filter { it.isNotEmpty() }
            .forEach { path -> runCatching { safeArtworkPath(path).delete() } }
        deleteOperationLocked(operationId)
    }

    private fun recoverTrackMoveLocked(operationId: String, details: JSONObject) {
        val trackId = details.getString("track_id")
        val source = safeMusicPath(details.getString("source"), mustExist = false)
        val destination = safeMusicPath(details.getString("destination"), mustExist = false)
        when {
            pathOccupied(destination) && !pathOccupied(source) -> {
                val track = trackLocked(trackId)
                if (track == null) {
                    deleteOperationLocked(operationId)
                    return
                }
                requireRegularUnlinkedFile(destination)
                if (destination.length() != track.byteSize || sha256(destination) != track.sha256) {
                    atomicMove(destination, source)
                    deleteOperationLocked(operationId)
                    recordErrorLocked(trackId, "move_recovery_integrity", "An interrupted move was rolled back because its destination failed integrity verification.")
                    return
                }
                transaction(database) {
                    database.update("tracks", ContentValues().apply {
                        put("folder_id", details.getString("folder_id"))
                        put("relative_path", details.getString("destination"))
                    }, "id=?", arrayOf(trackId))
                    deleteOperationLocked(operationId)
                }
            }
            pathOccupied(source) && !pathOccupied(destination) -> deleteOperationLocked(operationId)
            pathOccupied(source) && pathOccupied(destination) -> {
                deleteOperationLocked(operationId)
                recordErrorLocked(trackId, "move_recovery_conflict", "Both move paths existed; the indexed original was preserved.")
            }
            else -> {
                deleteOperationLocked(operationId)
                recordErrorLocked(trackId, "move_recovery_missing", "Both paths of an interrupted move are missing.")
            }
        }
    }

    private fun recoverFolderMoveLocked(operationId: String, details: JSONObject) {
        val sourceRelative = details.getString("source")
        val destinationRelative = details.getString("destination")
        val source = safeMusicPath(sourceRelative, mustExist = false)
        val destination = safeMusicPath(destinationRelative, mustExist = false)
        when {
            pathOccupied(destination) && !pathOccupied(source) -> {
                rejectSymlinksRecursively(destination)
                transaction(database) {
                    applyFolderMoveLocked(
                        details.getString("folder_id"), sourceRelative, destinationRelative,
                        details.getString("parent_id"), details.getString("name"),
                    )
                    deleteOperationLocked(operationId)
                }
            }
            pathOccupied(source) && !pathOccupied(destination) -> deleteOperationLocked(operationId)
            pathOccupied(source) && pathOccupied(destination) -> {
                deleteOperationLocked(operationId)
                recordErrorLocked(null, "move_recovery_conflict", "Both folder move paths existed; the indexed original was preserved.")
            }
            else -> {
                deleteOperationLocked(operationId)
                recordErrorLocked(null, "move_recovery_missing", "Both paths of an interrupted folder move are missing.")
            }
        }
    }

    private fun recoverTrackDeleteLocked(operationId: String, details: JSONObject) {
        val trackId = details.getString("track_id")
        val source = safeMusicPath(details.getString("source"), mustExist = false)
        val trash = safeTrashPath(details.getString("trash"))
        val artwork = details.optString("artwork").takeIf { it.isNotEmpty() }?.let(::safeArtworkPath)
        val artworkTrash = details.optString("artwork_trash").takeIf { it.isNotEmpty() }?.let(::safeTrashPath)
            ?: File(trashRoot, "$operationId-artwork")
        val conflict = (pathOccupied(source) && pathOccupied(trash)) ||
            (artwork?.let(::pathOccupied) == true && pathOccupied(artworkTrash))
        if (conflict) {
            if (!pathOccupied(source) && pathOccupied(trash)) atomicMove(trash, source)
            if (artwork != null && !pathOccupied(artwork) && pathOccupied(artworkTrash)) atomicMove(artworkTrash, artwork)
            deleteOperationLocked(operationId)
            recordErrorLocked(trackId, "delete_recovery_conflict", "Conflicting deletion paths existed; the indexed originals were preserved.")
            return
        }
        if (pathOccupied(source)) atomicMove(source, trash)
        if (artwork?.let(::pathOccupied) == true) atomicMove(artwork, artworkTrash)
        transaction(database) {
            removeReferencesLocked(trackId)
            database.delete("tracks", "id=?", arrayOf(trackId))
            deleteOperationLocked(operationId)
        }
        trash.delete()
        artworkTrash.delete()
    }

    private fun cleanupPendingFoldersLocked() {
        val pending = database.query(
            "folders", FOLDER_COLUMNS, "pending_delete=1", null, null, null, "LENGTH(relative_path) DESC",
        ).use { cursor -> buildList { while (cursor.moveToNext()) add(cursor.folderRecord()) } }
        pending.forEach { folder ->
            val folderIds = descendantFolderRecordsLocked(folder.relativePath).map { it.folder.id }
            val hasTracks = if (folderIds.isEmpty()) false else database.query(
                "tracks", arrayOf("id"),
                folderIds.joinToString(prefix = "folder_id IN (", postfix = ")") { "?" },
                folderIds.toTypedArray(), null, null, null, "1",
            ).use { it.moveToFirst() }
            if (!hasTracks) {
                val directory = safeMusicPath(folder.relativePath, mustExist = false)
                if (directory.exists()) runCatching { deleteTreeWithinRoot(directory) }
                    .onFailure { recordErrorLocked(null, "folder_delete_failed", safeMessage(it)) }
                if (!directory.exists()) database.delete("folders", "id=?", arrayOf(folder.folder.id))
            }
        }
        val completedFolderOperations = database.query(
            "operations", arrayOf("id", "details"), "kind=?", arrayOf(OP_DELETE_FOLDER), null, null, null,
        ).use { cursor ->
            buildList {
                while (cursor.moveToNext()) {
                    val folderId = runCatching { JSONObject(cursor.getString(1)).getString("folder_id") }.getOrNull()
                    if (folderId != null && folderRecordLocked(folderId, includePending = true) == null) add(cursor.getString(0))
                }
            }
        }
        completedFolderOperations.forEach(::deleteOperationLocked)
    }

    private fun cleanupTrashLocked() {
        val referenced = database.query(
            "operations", arrayOf("details"), "kind=?", arrayOf(OP_DELETE_TRACK), null, null, null,
        ).use { cursor ->
            buildSet<String> {
                while (cursor.moveToNext()) {
                    val details = runCatching { JSONObject(cursor.getString(0)) }.getOrNull() ?: continue
                    details.optString("trash").takeIf { it.isNotEmpty() }?.let(::add)
                    details.optString("artwork_trash").takeIf { it.isNotEmpty() }?.let(::add)
                }
            }
        }
        trashRoot.listFiles()?.forEach { file ->
            if (Files.isSymbolicLink(file.toPath())) return@forEach
            if (file.isFile && file.absolutePath !in referenced) file.delete()
        }
    }

    private fun applyFolderMoveLocked(folderId: String, oldPrefix: String, newPrefix: String, parentId: String, name: String) {
        val folders = descendantFolderRecordsLocked(oldPrefix)
        folders.forEach { record ->
            val suffix = record.relativePath.removePrefix(oldPrefix)
            database.update("folders", ContentValues().apply {
                put("relative_path", newPrefix + suffix)
                if (record.folder.id == folderId) {
                    put("parent_id", parentId)
                    put("name", name)
                }
            }, "id=?", arrayOf(record.folder.id))
        }
        val tracks = database.query("tracks", arrayOf("id", "relative_path"), null, null, null, null, null).use { cursor ->
            buildList {
                while (cursor.moveToNext()) {
                    val relative = cursor.getString(1)
                    if (relative.startsWith("$oldPrefix/")) add(cursor.getString(0) to relative)
                }
            }
        }
        tracks.forEach { (id, relative) ->
            database.update("tracks", ContentValues().apply {
                put("relative_path", newPrefix + relative.removePrefix(oldPrefix))
            }, "id=?", arrayOf(id))
        }
    }

    private fun descendantFolderRecordsLocked(prefix: String): List<FolderRecord> = database.query(
        "folders", FOLDER_COLUMNS, null, null, null, null, "LENGTH(relative_path), id",
    ).use { cursor ->
        buildList {
            while (cursor.moveToNext()) {
                val record = cursor.folderRecord()
                if (record.relativePath == prefix || record.relativePath.startsWith("$prefix/")) add(record)
            }
        }
    }

    private fun isDescendantLocked(candidateId: String, ancestorId: String): Boolean {
        var current: String? = candidateId
        val visited = HashSet<String>()
        while (current != null && visited.add(current)) {
            if (current == ancestorId) return true
            current = folderRecordLocked(current, includePending = true)?.folder?.parentId
        }
        return false
    }

    private fun ensureNoFolderCollisionLocked(parentId: String, name: String, excludingId: String?) {
        database.query(
            "folders", arrayOf("id"), "parent_id=? AND name=?", arrayOf(parentId, name), null, null, null,
        ).use { cursor ->
            if (cursor.moveToFirst() && cursor.getString(0) != excludingId) conflict("A folder named '$name' already exists.")
        }
    }

    private fun trackAtPathLocked(path: String, excludingId: String): Boolean = database.query(
        "tracks", arrayOf("id"), "relative_path=? AND id<>?", arrayOf(path, excludingId), null, null, null,
    ).use { it.moveToFirst() }

    private fun trackLocked(id: String): OfflineTrack? = database.query(
        "tracks", TRACK_COLUMNS, "id=?", arrayOf(id), null, null, null,
    ).use { cursor -> if (cursor.moveToFirst()) cursor.offlineTrack() else null }

    private fun trackByDigestLocked(digest: String, quality: String): OfflineTrack? = database.query(
        "tracks", TRACK_COLUMNS, "sha256=? AND quality=?", arrayOf(digest, quality), null, null, null,
    ).use { cursor -> if (cursor.moveToFirst()) cursor.offlineTrack() else null }

    private fun folderRecordLocked(id: String, includePending: Boolean): FolderRecord? {
        val selection = if (includePending) "id=?" else "id=? AND pending_delete=0"
        return database.query("folders", FOLDER_COLUMNS, selection, arrayOf(id), null, null, null).use { cursor ->
            if (cursor.moveToFirst()) cursor.folderRecord() else null
        }
    }

    private fun requireFolderLocked(id: String): FolderRecord = folderRecordLocked(id, includePending = false)
        ?: missing("Folder '$id' does not exist.")

    private fun insertFolderLocked(folder: OfflineFolder, relativePath: String) {
        database.insertOrThrow("folders", null, ContentValues().apply {
            put("id", folder.id)
            if (folder.parentId == null) putNull("parent_id") else put("parent_id", folder.parentId)
            put("name", folder.name)
            put("relative_path", relativePath)
            put("pending_delete", 0)
        })
    }

    private fun insertTrackLocked(track: OfflineTrack) {
        database.insertOrThrow("tracks", null, ContentValues().apply {
            put("id", track.id)
            put("title", track.title)
            put("artist", track.artist)
            put("album", track.album)
            put("album_artist", track.albumArtist)
            put("disc_number", track.disc)
            put("track_number", track.track)
            put("duration_ms", track.durationMs)
            put("mime", track.mime)
            put("codec", track.codec)
            put("quality", track.quality)
            put("byte_size", track.byteSize)
            put("sha256", track.sha256)
            put("folder_id", track.folderId)
            put("relative_path", track.relativePath)
            if (track.artworkPath == null) putNull("artwork_path") else put("artwork_path", track.artworkPath)
            put("artwork_size", track.artworkPath?.let { File(it).length() } ?: 0L)
            put("pending_delete", if (track.pendingDelete) 1 else 0)
        })
    }

    private fun putOperationLocked(id: String, kind: String, details: JSONObject) {
        database.insertOrThrow("operations", null, ContentValues().apply {
            put("id", id)
            put("kind", kind)
            put("details", details.toString())
            put("created_at", System.currentTimeMillis())
        })
    }

    private fun deleteOperationLocked(id: String) {
        database.delete("operations", "id=?", arrayOf(id))
    }

    private fun recordErrorLocked(trackId: String?, code: String, message: String) {
        database.insert("local_errors", null, ContentValues().apply {
            if (trackId == null) putNull("track_id") else put("track_id", trackId)
            put("code", code.take(100))
            put("message", message.take(2000))
            put("created_at", System.currentTimeMillis())
        })
        database.execSQL("DELETE FROM local_errors WHERE id NOT IN (SELECT id FROM local_errors ORDER BY id DESC LIMIT 200)")
    }

    private fun settingLongLocked(key: String): Long = database.query(
        "settings", arrayOf("long_value"), "key=?", arrayOf(key), null, null, null,
    ).use { cursor -> if (cursor.moveToFirst()) cursor.getLong(0) else 0L }

    private fun enforceCapacityLocked(additionalBytes: Long) {
        val current = database.rawQuery("SELECT COALESCE(SUM(byte_size + artwork_size),0) FROM tracks", null).use { cursor ->
            cursor.moveToFirst()
            cursor.getLong(0)
        }
        val limit = settingLongLocked(SETTING_LIMIT)
        if (limit > 0 && (additionalBytes > limit || current > limit - additionalBytes)) {
            throw OfflineLibraryException("storage_full", "The saved-music storage limit would be exceeded.")
        }
        val available = StatFs(base.absolutePath).availableBytes
        if (additionalBytes > available - minOf(available, SAFETY_RESERVE_BYTES)) {
            throw OfflineLibraryException("storage_full", "There is not enough free device storage to safely save this music.")
        }
    }

    private fun parseImport(metadata: JSONObject): ParsedImport {
        if (metadata.has("status") && metadata.optString("status") != "ready") invalid("Only ready manifest tracks can be imported.")
        val byteSize = requireLong(metadata, "byte_size", minimum = 1)
        val digest = requireDisplayText(metadata.optString("sha256"), "checksum", 64)
        if (!SHA256.matches(digest)) invalid("Manifest checksum must be lowercase SHA-256.")
        val quality = requireDisplayText(metadata.optString("quality"), "quality", 30)
        if (quality != "original" && quality != "aac_256") invalid("Unsupported download quality '$quality'.")
        return ParsedImport(
            title = requireDisplayText(metadata.optString("title"), "title", 500),
            artist = cleanOptionalText(metadata.optString("artist"), 500),
            album = cleanOptionalText(metadata.optString("album"), 500),
            albumArtist = cleanOptionalText(metadata.optString("album_artist"), 500),
            disc = requireInt(metadata, "disc", 0),
            track = requireInt(metadata, "track", 0),
            durationMs = requireLong(metadata, "duration_ms", 0),
            mime = requireDisplayText(metadata.optString("mime"), "MIME type", 200),
            codec = requireDisplayText(metadata.optString("codec"), "codec", 100),
            quality = quality,
            byteSize = byteSize,
            sha256 = digest,
        )
    }

    private fun requireInt(json: JSONObject, key: String, minimum: Int): Int {
        if (!json.has(key)) invalid("Manifest field '$key' is missing.")
        val value = try { json.getInt(key) } catch (error: Exception) { invalid("Manifest field '$key' is invalid.") }
        if (value < minimum) invalid("Manifest field '$key' is invalid.")
        return value
    }

    private fun requireLong(json: JSONObject, key: String, minimum: Long): Long {
        if (!json.has(key)) invalid("Manifest field '$key' is missing.")
        val value = try { json.getLong(key) } catch (error: Exception) { invalid("Manifest field '$key' is invalid.") }
        if (value < minimum) invalid("Manifest field '$key' is invalid.")
        return value
    }

    private fun requireUuid(value: String, label: String): String {
        val normalized = value.lowercase()
        val parsed = try {
            UUID.fromString(normalized)
        } catch (_: IllegalArgumentException) {
            invalid("The $label is not valid.")
        }
        if (parsed.toString() != normalized) invalid("The $label is not valid.")
        return normalized
    }

    private fun requireComponent(value: String, label: String): String {
        val result = value.trim()
        if (result.isEmpty() || result == "." || result == ".." || result.length > 200 ||
            result.any { it == '/' || it == '\\' || it == '\u0000' || it.code < 32 }
        ) invalidPath("The $label is not valid.")
        return result
    }

    private fun requireDisplayText(value: String, label: String, maximum: Int): String {
        val result = value.trim()
        if (result.isEmpty() || result.length > maximum || result.any { it == '\u0000' || (it.code < 32 && it != '\n' && it != '\t') }) {
            invalid("The $label is not valid.")
        }
        return result
    }

    private fun cleanOptionalText(value: String, maximum: Int): String {
        val result = value.trim()
        if (result.length > maximum || result.any { it == '\u0000' || (it.code < 32 && it != '\n' && it != '\t') }) {
            invalid("Manifest metadata is invalid.")
        }
        return result
    }

    private fun safeMusicPath(relative: String, mustExist: Boolean): File {
        if (relative.startsWith('/') || relative.split('/').any { it.isEmpty() || it == "." || it == ".." }) {
            if (relative.isNotEmpty()) invalidPath("Stored music path is invalid.")
        }
        val result = if (relative.isEmpty()) musicRoot else File(musicRoot, relative)
        ensureContainedAndUnlinked(musicRoot, result, mustExist)
        return result
    }

    private fun safeAbsoluteMusicPath(path: String, mustExist: Boolean): File {
        val result = File(path)
        ensureContainedAndUnlinked(musicRoot, result, mustExist)
        return result
    }

    private fun safeArtworkPath(path: String): File {
        val result = File(path)
        ensureContainedAndUnlinked(artworkRoot, result, mustExist = false)
        return result
    }

    private fun safeTrashPath(path: String): File {
        val result = File(path)
        ensureContainedAndUnlinked(trashRoot, result, mustExist = false)
        return result
    }

    private fun ensureContainedAndUnlinked(root: File, target: File, mustExist: Boolean) {
        val normalizedRoot = root.toPath().toAbsolutePath().normalize()
        val normalizedTarget = target.toPath().toAbsolutePath().normalize()
        if (normalizedTarget != normalizedRoot && !normalizedTarget.startsWith(normalizedRoot)) invalidPath("Path escapes app music storage.")
        var current = normalizedRoot
        val relative = normalizedRoot.relativize(normalizedTarget)
        relative.forEach { component ->
            current = current.resolve(component)
            if (Files.isSymbolicLink(current)) invalidPath("Symbolic links are not allowed in app music storage.")
        }
        if (mustExist && !Files.exists(normalizedTarget, LinkOption.NOFOLLOW_LINKS)) missing("Stored file or folder is missing.")
    }

    private fun pathOccupied(file: File): Boolean =
        Files.exists(file.toPath(), LinkOption.NOFOLLOW_LINKS)

    private fun requireRegularUnlinkedFile(file: File) {
        if (!pathOccupied(file)) missing("Required local file is missing.")
        val absolute = file.toPath().toAbsolutePath().normalize()
        var trustedRoot: TrustedStorageRoot? = null
        var relative: Path? = null
        var inspectedRoot: Path? = null
        for (root in trustedStorageRoots) {
            when {
                absolute == root.declared || absolute.startsWith(root.declared) -> {
                    trustedRoot = root
                    relative = root.declared.relativize(absolute)
                    inspectedRoot = root.declared
                }
                absolute == root.resolved || absolute.startsWith(root.resolved) -> {
                    trustedRoot = root
                    relative = root.resolved.relativize(absolute)
                    inspectedRoot = root.resolved
                }
            }
            if (trustedRoot != null) break
        }
        val root = trustedRoot ?: invalidPath("Local file path escapes private app storage.")
        val suffix = relative!!
        val expectedResolved = root.resolved.resolve(suffix).normalize()
        if (expectedResolved != root.resolved && !expectedResolved.startsWith(root.resolved)) {
            invalidPath("Local file path escapes private app storage.")
        }
        var current = inspectedRoot!!
        suffix.forEach { component ->
            current = current.resolve(component)
            if (Files.isSymbolicLink(current)) {
                invalidPath("Only regular files without symbolic-link traversal are allowed.")
            }
        }
        if (
            canonicalPath(file) != expectedResolved ||
            !Files.isRegularFile(absolute, LinkOption.NOFOLLOW_LINKS)
        ) {
            invalidPath("Only regular files without symbolic-link traversal are allowed.")
        }
    }

    private fun rejectSymlinksRecursively(directory: File) {
        ensureContainedAndUnlinked(musicRoot, directory, mustExist = true)
        try {
            Files.walk(directory.toPath()).use { stream ->
                stream.forEach { path -> if (Files.isSymbolicLink(path)) invalidPath("Symbolic links are not allowed in managed folders.") }
            }
        } catch (error: OfflineLibraryException) {
            throw error
        } catch (error: IOException) {
            throw storageFailure("Could not inspect a managed folder.", error)
        }
    }

    private fun deleteTreeWithinRoot(directory: File) {
        ensureContainedAndUnlinked(musicRoot, directory, mustExist = false)
        if (!directory.exists()) return
        rejectSymlinksRecursively(directory)
        Files.walk(directory.toPath()).use { stream ->
            stream.sorted(Comparator.reverseOrder()).forEach { Files.delete(it) }
        }
    }

    private fun ensureDirectory(directory: File) {
        if (Files.isSymbolicLink(directory.toPath())) invalidPath("Private offline storage cannot be a symbolic link.")
        if (!pathOccupied(directory) && !directory.mkdirs()) throw storageFailure("Could not create private offline storage.")
        if (!directory.isDirectory) invalidPath("Private offline storage is not a regular directory.")
    }

    private fun rejectManagedImportSource(file: File) {
        val source = canonicalPath(file)
        val completedMusic = canonicalPath(musicRoot)
        val completedArtwork = canonicalPath(artworkRoot)
        if (source.startsWith(completedMusic) || source.startsWith(completedArtwork)) {
            invalidPath("Completed library files cannot be re-imported as transfer temporaries.")
        }
    }

    private fun canonicalPath(file: File): Path = try {
        file.canonicalFile.toPath().toAbsolutePath().normalize()
    } catch (error: IOException) {
        throw storageFailure("Could not validate a local file path.", error)
    }

    private fun atomicMove(source: File, destination: File) {
        destination.parentFile?.let(::ensureDirectory)
        if (pathOccupied(destination)) conflict("Destination already exists; files are never overwritten.")
        try {
            Files.move(source.toPath(), destination.toPath(), StandardCopyOption.ATOMIC_MOVE)
        } catch (_: AtomicMoveNotSupportedException) {
            Files.move(source.toPath(), destination.toPath())
        }
    }

    private fun copyAndSync(source: File, destination: File) {
        destination.parentFile?.let(::ensureDirectory)
        FileInputStream(source).use { input ->
            FileOutputStream(destination).use { output ->
                input.copyTo(output)
                output.fd.sync()
            }
        }
    }

    private fun consumeImportedSource(file: File) {
        if (file.exists() && !file.delete()) {
            recordErrorLocked(null, "temporary_cleanup_failed", "A verified import temporary could not be removed after commit.")
        }
    }

    private fun sha256(file: File): String {
        return try {
            val digest = MessageDigest.getInstance("SHA-256")
            FileInputStream(file).use { input ->
                val buffer = ByteArray(DEFAULT_BUFFER_SIZE)
                while (true) {
                    val count = input.read(buffer)
                    if (count < 0) break
                    digest.update(buffer, 0, count)
                }
            }
            digest.digest().joinToString("") { "%02x".format(it) }
        } catch (error: IOException) {
            throw storageFailure("Could not read a local file for integrity verification.", error)
        }
    }

    private fun changedLocked() {
        mutableChanges.value = mutableChanges.value + 1
    }

    private fun transaction(database: SQLiteDatabase, block: () -> Unit) {
        database.beginTransaction()
        try {
            block()
            database.setTransactionSuccessful()
        } finally {
            database.endTransaction()
        }
    }

    private fun invalid(message: String): Nothing = throw OfflineLibraryException("invalid", message)
    private fun invalidPath(message: String): Nothing = throw OfflineLibraryException("invalid_path", message)
    private fun missing(message: String): Nothing = throw OfflineLibraryException("missing", message)
    private fun conflict(message: String): Nothing = throw OfflineLibraryException("conflict", message)
    private fun integrity(message: String): Nothing = throw OfflineLibraryException("checksum", message)
    private fun storageFailure(message: String, cause: Throwable? = null) = OfflineLibraryException("storage", message, cause)
    private fun safeMessage(error: Throwable): String = error.message?.take(500) ?: error.javaClass.simpleName

    private data class TrustedStorageRoot(val declared: Path, val resolved: Path)

    private data class FolderRecord(val folder: OfflineFolder, val relativePath: String, val pendingDelete: Boolean)

    private data class ParsedImport(
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
    ) {
        fun toTrack(id: String, folderId: String, relativePath: String, artworkPath: String?) = OfflineTrack(
            id, title, artist, album, albumArtist, disc, track, durationMs, mime, codec, quality,
            byteSize, sha256, folderId, relativePath, artworkPath,
        )
    }

    private fun Cursor.offlineTrack() = OfflineTrack(
        id = getString(getColumnIndexOrThrow("id")),
        title = getString(getColumnIndexOrThrow("title")),
        artist = getString(getColumnIndexOrThrow("artist")),
        album = getString(getColumnIndexOrThrow("album")),
        albumArtist = getString(getColumnIndexOrThrow("album_artist")),
        disc = getInt(getColumnIndexOrThrow("disc_number")),
        track = getInt(getColumnIndexOrThrow("track_number")),
        durationMs = getLong(getColumnIndexOrThrow("duration_ms")),
        mime = getString(getColumnIndexOrThrow("mime")),
        codec = getString(getColumnIndexOrThrow("codec")),
        quality = getString(getColumnIndexOrThrow("quality")),
        byteSize = getLong(getColumnIndexOrThrow("byte_size")),
        sha256 = getString(getColumnIndexOrThrow("sha256")),
        folderId = getString(getColumnIndexOrThrow("folder_id")),
        relativePath = getString(getColumnIndexOrThrow("relative_path")),
        artworkPath = nullableString(getColumnIndexOrThrow("artwork_path")),
        pendingDelete = getInt(getColumnIndexOrThrow("pending_delete")) != 0,
    )

    private fun Cursor.offlineFolder() = OfflineFolder(
        getString(getColumnIndexOrThrow("id")),
        nullableString(getColumnIndexOrThrow("parent_id")),
        getString(getColumnIndexOrThrow("name")),
    )

    private fun Cursor.folderRecord() = FolderRecord(
        offlineFolder(),
        getString(getColumnIndexOrThrow("relative_path")),
        getInt(getColumnIndexOrThrow("pending_delete")) != 0,
    )

    private fun Cursor.nullableString(index: Int): String? = if (isNull(index)) null else getString(index)

    private fun trackToJson(track: OfflineTrack) = JSONObject()
        .put("id", track.id).put("title", track.title).put("artist", track.artist)
        .put("album", track.album).put("album_artist", track.albumArtist)
        .put("disc", track.disc).put("track", track.track).put("duration_ms", track.durationMs)
        .put("mime", track.mime).put("codec", track.codec).put("quality", track.quality)
        .put("byte_size", track.byteSize).put("sha256", track.sha256)
        .put("folder_id", track.folderId).put("relative_path", track.relativePath)
        .put("artwork_path", track.artworkPath ?: JSONObject.NULL)

    private fun jsonToTrack(value: JSONObject) = OfflineTrack(
        value.getString("id"), value.getString("title"), value.getString("artist"), value.getString("album"),
        value.getString("album_artist"), value.getInt("disc"), value.getInt("track"), value.getLong("duration_ms"),
        value.getString("mime"), value.getString("codec"), value.getString("quality"), value.getLong("byte_size"),
        value.getString("sha256"), value.getString("folder_id"), value.getString("relative_path"),
        if (value.isNull("artwork_path")) null else value.getString("artwork_path"),
    )

    companion object {
        const val ROOT_FOLDER_ID = "root"
        const val IMPORT_FOLDER_ID = "imported"

        @Volatile private var instance: OfflineLibrary? = null

        fun get(context: Context): OfflineLibrary = instance ?: synchronized(this) {
            instance ?: OfflineLibrary(context).also { instance = it }
        }

        private val TRACK_COLUMNS = arrayOf(
            "id", "title", "artist", "album", "album_artist", "disc_number", "track_number", "duration_ms",
            "mime", "codec", "quality", "byte_size", "sha256", "folder_id", "relative_path", "artwork_path", "pending_delete",
        )
        private val FOLDER_COLUMNS = arrayOf("id", "parent_id", "name", "relative_path", "pending_delete")
        private val SHA256 = Regex("[0-9a-f]{64}")
        private const val OP_IMPORT = "import"
        private const val OP_CREATE_FOLDER = "create_folder"
        private const val OP_MOVE_TRACK = "move_track"
        private const val OP_MOVE_FOLDER = "move_folder"
        private const val OP_DELETE_TRACK = "delete_track"
        private const val OP_DELETE_FOLDER = "delete_folder"
        private const val SETTING_LIMIT = "limit_bytes"
        private const val SAFETY_RESERVE_BYTES = 32L * 1024L * 1024L

        private fun joinRelative(parent: String, name: String): String = if (parent.isEmpty()) name else "$parent/$name"

        private fun mediaExtension(mime: String, fallback: String): String = when (mime.lowercase()) {
            "audio/flac", "audio/x-flac" -> "flac"
            "audio/mp4", "audio/m4a", "audio/x-m4a" -> "m4a"
            "audio/mpeg" -> "mp3"
            "audio/ogg" -> "ogg"
            "audio/opus" -> "opus"
            "audio/wav", "audio/x-wav" -> "wav"
            else -> fallback.lowercase().takeIf { it.matches(Regex("[a-z0-9]{1,8}")) } ?: "audio"
        }
        private fun safeArtworkExtension(extension: String): String = extension.lowercase().takeIf {
            it in setOf("jpg", "jpeg", "png", "webp", "gif")
        } ?: "image"
    }
}

private class LibraryDatabase(context: Context) : SQLiteOpenHelper(context, "offline_library.db", null, 1) {
    override fun onConfigure(db: SQLiteDatabase) {
        super.onConfigure(db)
        db.setForeignKeyConstraintsEnabled(true)

    }

    override fun onCreate(db: SQLiteDatabase) {
        db.execSQL("CREATE TABLE folders (id TEXT PRIMARY KEY, parent_id TEXT REFERENCES folders(id), name TEXT NOT NULL, relative_path TEXT NOT NULL UNIQUE, pending_delete INTEGER NOT NULL DEFAULT 0 CHECK(pending_delete IN (0,1)), UNIQUE(parent_id,name))")
        db.execSQL("CREATE TABLE tracks (id TEXT PRIMARY KEY, title TEXT NOT NULL, artist TEXT NOT NULL, album TEXT NOT NULL, album_artist TEXT NOT NULL, disc_number INTEGER NOT NULL, track_number INTEGER NOT NULL, duration_ms INTEGER NOT NULL, mime TEXT NOT NULL, codec TEXT NOT NULL, quality TEXT NOT NULL, byte_size INTEGER NOT NULL, sha256 TEXT NOT NULL, folder_id TEXT NOT NULL REFERENCES folders(id), relative_path TEXT NOT NULL UNIQUE, artwork_path TEXT, artwork_size INTEGER NOT NULL DEFAULT 0, pending_delete INTEGER NOT NULL DEFAULT 0 CHECK(pending_delete IN (0,1)), UNIQUE(sha256,quality))")
        db.execSQL("CREATE TABLE playlists (id TEXT PRIMARY KEY, name TEXT NOT NULL)")
        db.execSQL("CREATE TABLE playlist_snapshot_receipts (id TEXT PRIMARY KEY, playlist_id TEXT NOT NULL)")
        db.execSQL("CREATE TABLE playlist_entries (playlist_id TEXT NOT NULL REFERENCES playlists(id) ON DELETE CASCADE, position INTEGER NOT NULL, track_id TEXT NOT NULL REFERENCES tracks(id) ON DELETE CASCADE, PRIMARY KEY(playlist_id,position))")
        db.execSQL("CREATE TABLE playlist_missing (playlist_id TEXT NOT NULL REFERENCES playlists(id) ON DELETE CASCADE, position INTEGER NOT NULL, title TEXT NOT NULL, PRIMARY KEY(playlist_id,position))")
        db.execSQL("CREATE TABLE queue_entries (entry_id TEXT PRIMARY KEY, position INTEGER NOT NULL UNIQUE, track_id TEXT NOT NULL REFERENCES tracks(id) ON DELETE CASCADE)")
        db.execSQL("CREATE TABLE queue_state (singleton INTEGER PRIMARY KEY CHECK(singleton=1), current_entry_id TEXT, position_ms INTEGER NOT NULL, shuffle INTEGER NOT NULL, repeat_mode INTEGER NOT NULL)")
        db.execSQL("INSERT INTO queue_state(singleton,current_entry_id,position_ms,shuffle,repeat_mode) VALUES(1,NULL,0,0,0)")
        db.execSQL("CREATE TABLE settings (key TEXT PRIMARY KEY, long_value INTEGER NOT NULL)")
        db.execSQL("CREATE TABLE local_errors (id INTEGER PRIMARY KEY AUTOINCREMENT, track_id TEXT, code TEXT NOT NULL, message TEXT NOT NULL, created_at INTEGER NOT NULL)")
        db.execSQL("CREATE TABLE operations (id TEXT PRIMARY KEY, kind TEXT NOT NULL, details TEXT NOT NULL, created_at INTEGER NOT NULL)")
        db.execSQL("CREATE INDEX tracks_folder_idx ON tracks(folder_id)")
        db.execSQL("CREATE INDEX playlist_entries_track_idx ON playlist_entries(track_id)")
        db.execSQL("CREATE INDEX queue_entries_track_idx ON queue_entries(track_id)")
    }

    override fun onUpgrade(db: SQLiteDatabase, oldVersion: Int, newVersion: Int) {
        throw IllegalStateException("Unsupported offline library schema upgrade from $oldVersion to $newVersion")
    }
}
