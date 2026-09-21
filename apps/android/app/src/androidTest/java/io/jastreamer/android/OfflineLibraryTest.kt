package io.jastreamer.android

import android.content.ContentValues
import android.database.sqlite.SQLiteDatabase
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import java.io.File
import java.nio.file.Files
import java.security.MessageDigest
import java.util.UUID
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import org.json.JSONObject
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class OfflineLibraryTest {
    private val context get() = InstrumentationRegistry.getInstrumentation().targetContext
    private val library get() = OfflineLibrary.get(context)

    @Test
    fun verifiedImportDeduplicatesByBytesAndQualityAndEnforcesQuota() {
        val bytes = "verified-${UUID.randomUUID()}".toByteArray()
        val artworkBytes = "art-${UUID.randomUUID()}".toByteArray()
        val firstSource = temporary("first.flac", bytes)
        val artwork = temporary("cover.png", artworkBytes)
        val usageBefore = library.usage().musicBytes
        val first = library.importTrack(firstSource, manifest(bytes, title = "First"), artwork = artwork)
        try {
            assertFalse(firstSource.exists())
            assertFalse(artwork.exists())
            assertTrue(File(first.artworkPath!!).isFile)
            assertEquals(usageBefore + bytes.size + artworkBytes.size, library.usage().musicBytes)
            library.openAudio(first.id).use { handle -> assertArrayEquals(bytes, handle.input.readBytes()) }
            assertEquals(first, library.trackByDigest(first.sha256, first.quality))

            val duplicateSource = temporary("duplicate.flac", bytes)
            val duplicate = library.importTrack(duplicateSource, manifest(bytes, title = "Different copied metadata"))
            assertEquals(first.id, duplicate.id)
            assertFalse(duplicateSource.exists())
            assertEquals(1, library.tracks().count { it.id == first.id })

            val badBytes = "wrong-${UUID.randomUUID()}".toByteArray()
            val corruptSource = temporary("corrupt.flac", badBytes)
            expectCode("checksum") {
                library.importTrack(corruptSource, manifest(bytes, title = "Must not appear"))
            }
            assertTrue(corruptSource.exists())
            corruptSource.delete()

            val limitedBytes = "limited-${UUID.randomUUID()}".toByteArray()
            val limitedSource = temporary("limited.flac", limitedBytes)
            val currentUsage = library.usage().musicBytes
            library.setLimitBytes(currentUsage + limitedBytes.size - 1)
            try {
                expectCode("storage_full") {
                    library.importTrack(limitedSource, manifest(limitedBytes, title = "Over limit"))
                }
                assertTrue(limitedSource.exists())
                assertTrue(library.tracks().none { it.title == "Over limit" })
            } finally {
                library.setLimitBytes(0)
                limitedSource.delete()
            }
        } finally {
            library.deleteTracks(listOf(first.id))
        }
    }

    @Test
    fun cancelledCommitPreservesInputsAndCanBeRetriedWithoutPartialRegistration() {
        val marker = UUID.randomUUID().toString()
        val bytes = "cancel-$marker".toByteArray()
        val artworkBytes = "cancel-art-$marker".toByteArray()
        val source = temporary("cancel.flac", bytes)
        val artwork = temporary("cancel.png", artworkBytes)
        var claims = 0
        expectCode("cancelled") {
            library.importTrack(
                source,
                manifest(bytes, "Cancelled"),
                artwork = artwork,
                commitGate = { _ ->
                    claims += 1
                    false
                },
            )
        }
        assertEquals(1, claims)
        assertTrue(source.exists())
        assertTrue(artwork.exists())
        assertTrue(library.tracks().none { it.title == "Cancelled" })
        expectCode("cancelled") {
            library.importTrack(
                source,
                manifest(bytes, "Unauthorized success"),
                artwork = artwork,
                commitGate = { true },
            )
        }
        assertTrue(source.exists())
        assertTrue(artwork.exists())
        assertTrue(library.tracks().none { it.title == "Unauthorized success" })

        val committed = library.importTrack(source, manifest(bytes, "Committed retry"), artwork = artwork)
        try {
            assertFalse(source.exists())
            assertFalse(artwork.exists())
            val duplicate = temporary("cancel-dedupe.flac", bytes)
            var duplicateClaims = 0
            try {
                expectCode("cancelled") {
                    library.importTrack(duplicate, manifest(bytes, "Cancelled dedupe"), commitGate = { _ ->
                        duplicateClaims += 1
                        false
                    })
                }
                assertEquals(1, duplicateClaims)
                assertTrue(duplicate.exists())
                assertEquals(committed.id, library.track(committed.id)?.id)
            } finally {
                duplicate.delete()
            }
        } finally {
            if (library.track(committed.id) != null) library.deleteTracks(listOf(committed.id))
            source.delete()
            artwork.delete()
        }
    }

    @Test
    fun playlistSnapshotReceiptIsIdempotentAndNeverRecreatesDeletedOrRewritesEditedPlaylist() {
        val marker = UUID.randomUUID().toString()
        val bytes = "playlist-receipt-$marker".toByteArray()
        val item = library.importTrack(temporary("playlist.flac", bytes), manifest(bytes, "Playlist receipt"))
        val receiptId = UUID.randomUUID().toString()
        var currentPlaylistId: String? = null
        try {
            expectCode("invalid") {
                library.savePlaylistSnapshotOnce("not-a-uuid", "Invalid", listOf(item.id))
            }
            expectCode("missing") {
                library.savePlaylist(UUID.randomUUID().toString(), "Ordinary update must not create", listOf(item.id))
            }
            val created = library.savePlaylistSnapshotOnce(
                receiptId,
                "Initial snapshot",
                listOf(item.id, item.id),
                listOf("Missing one"),
            )!!
            currentPlaylistId = created.id
            assertTrue(created.id != receiptId)
            assertEquals(listOf(item.id, item.id), created.trackIds)

            val edited = library.savePlaylist(created.id, "User edit", listOf(item.id), listOf("User missing"))
            val replayed = library.savePlaylistSnapshotOnce(
                receiptId,
                "Retry must not replace",
                listOf(item.id, item.id),
                listOf("Retry missing"),
            )
            assertEquals(edited, replayed)

            library.deletePlaylist(created.id)
            currentPlaylistId = null
            assertNull(library.savePlaylistSnapshotOnce(receiptId, "Must stay deleted", listOf(item.id)))

            library.acknowledgePlaylistSnapshot(receiptId)
            library.acknowledgePlaylistSnapshot(receiptId)
            val recreated = library.savePlaylistSnapshotOnce(receiptId, "New receipt lifetime", listOf(item.id))!!
            currentPlaylistId = recreated.id
            assertTrue(recreated.id != created.id)
        } finally {
            library.acknowledgePlaylistSnapshot(receiptId)
            currentPlaylistId?.let { id -> if (library.playlist(id) != null) library.deletePlaylist(id) }
            if (library.track(item.id) != null) library.deleteTracks(listOf(item.id))
        }
    }

    @Test
    fun movesPreserveIdentityChecksumPlaylistQueueAndActualPlacement() {
        val marker = UUID.randomUUID().toString()
        val parent = library.createFolder(OfflineLibrary.ROOT_FOLDER_ID, "parent-$marker")
        val destination = library.createFolder(OfflineLibrary.ROOT_FOLDER_ID, "destination-$marker")
        val bytes = "move-$marker".toByteArray()
        val item = library.importTrack(temporary("move.flac", bytes), manifest(bytes, "Moving"), parent.id)
        val playlist = library.savePlaylist(null, "moves-$marker", listOf(item.id, item.id))
        val queue = OfflineQueue(
            entries = listOf(OfflineQueueEntry("q1-$marker", item.id), OfflineQueueEntry("q2-$marker", item.id)),
            currentEntryId = "q2-$marker",
            positionMs = 1234,
        )
        library.saveQueue(queue)
        val originalId = item.id
        val originalChecksum = item.sha256
        val originalFile = musicFile(item.relativePath)
        try {
            library.moveTrack(item.id, destination.id, "renamed-$marker.flac")
            val movedTrack = library.track(item.id)!!
            assertEquals(originalId, movedTrack.id)
            assertEquals(originalChecksum, movedTrack.sha256)
            assertFalse(originalFile.exists())
            assertArrayEquals(bytes, musicFile(movedTrack.relativePath).readBytes())
            assertEquals(listOf(item.id, item.id), library.playlist(playlist.id)!!.trackIds)
            assertEquals(queue.entries, library.loadQueue().entries)

            library.renameFolder(destination.id, "renamed-folder-$marker")
            assertEquals(destination.id, library.folder(destination.id)!!.id)
            library.moveFolder(destination.id, parent.id, "nested-$marker")
            val afterFolderMove = library.track(item.id)!!
            assertTrue(afterFolderMove.relativePath.contains("parent-$marker/nested-$marker/"))
            assertArrayEquals(bytes, musicFile(afterFolderMove.relativePath).readBytes())
            assertEquals(listOf(item.id, item.id), library.playlist(playlist.id)!!.trackIds)
            assertEquals(queue.entries, library.loadQueue().entries)

            expectCode("invalid_path") { library.moveFolder(parent.id, destination.id) }
            expectCode("conflict") { library.createFolder(parent.id, "nested-$marker") }
        } finally {
            library.deletePlaylist(playlist.id)
            library.saveQueue(OfflineQueue())
            if (library.folder(parent.id) != null) library.deleteFolder(parent.id)
            if (library.folder(destination.id) != null) library.deleteFolder(destination.id)
            if (library.track(item.id) != null) library.deleteTracks(listOf(item.id))
        }
    }

    @Test
    fun playbackPinsDeferDeletionAndDeletionRemovesEveryReference() {
        val marker = UUID.randomUUID().toString()
        val bytes = "pin-$marker".toByteArray()
        val item = library.importTrack(temporary("pin.m4a", bytes), manifest(bytes, "Pinned", quality = "aac_256", mime = "audio/mp4", codec = "aac"))
        val playlist = library.savePlaylist(null, "pin-$marker", listOf(item.id, item.id), listOf("Unavailable snapshot entry"))
        library.saveQueue(OfflineQueue(listOf(
            OfflineQueueEntry("pin-1-$marker", item.id),
            OfflineQueueEntry("pin-2-$marker", item.id),
        ), "pin-1-$marker", 42))
        val audioHandle = library.openAudio(item.id)
        val playbackPin = library.retainTrack(item.id)
        val actualFile = musicFile(item.relativePath)
        try {
            val result = library.deleteTracks(listOf(item.id))
            assertEquals(listOf(item.id), result.deferredIds)
            assertNull(library.trackByDigest(item.sha256, item.quality))
            assertTrue(result.failures.isEmpty())
            assertTrue(actualFile.exists())
            assertEquals(true, library.track(item.id)?.pendingDelete)
            assertTrue(library.playlist(playlist.id)!!.trackIds.isEmpty())
            assertTrue(library.loadQueue().entries.isEmpty())
            val replacement = temporary("replacement.m4a", bytes)
            try {
                expectCode("conflict") {
                    library.importTrack(
                        replacement,
                        manifest(bytes, "Replacement", quality = "aac_256", mime = "audio/mp4", codec = "aac"),
                    )
                }
                assertTrue(replacement.exists())
                assertEquals(item.id, library.track(item.id)?.id)
            } finally {
                replacement.delete()
            }
            library.openAudio(item.id).use { reopened ->
                assertArrayEquals(bytes, reopened.input.readBytes())
            }
            audioHandle.close()
            assertNotNull(library.track(item.id))
            assertTrue(actualFile.exists())
            playbackPin.close()
            assertNull(library.track(item.id))
            assertFalse(actualFile.exists())
            assertNotNull(library.playlist(playlist.id))
            assertEquals(listOf("Unavailable snapshot entry"), library.playlist(playlist.id)!!.missingTitles)
        } finally {
            audioHandle.close()
            playbackPin.close()
            if (library.playlist(playlist.id) != null) library.deletePlaylist(playlist.id)
            library.saveQueue(OfflineQueue())
            if (library.track(item.id) != null) library.deleteTracks(listOf(item.id))
        }
    }

    @Test
    fun queuePositionPersistenceDoesNotPublishLibraryMutation() {
        val marker = UUID.randomUUID().toString()
        val bytes = "queue-$marker".toByteArray()
        val item = library.importTrack(temporary("queue.flac", bytes), manifest(bytes, "Queue"))
        val entry = OfflineQueueEntry("entry-$marker", item.id)
        try {
            library.saveQueue(OfflineQueue(listOf(entry), entry.id, 10))
            val mutation = library.changes.value
            library.saveQueue(OfflineQueue(listOf(entry), entry.id, 9_876))
            assertEquals(9_876L, library.loadQueue().positionMs)
            assertEquals(mutation, library.changes.value)
        } finally {
            library.saveQueue(OfflineQueue())
            library.deleteTracks(listOf(item.id))
        }
    }

    @Test
    fun interruptedPreclaimImportDiscardsStagingWithoutRegisteringOrConsumingSource() {
        val marker = UUID.randomUUID().toString()
        val bytes = "preclaim-$marker".toByteArray()
        val source = temporary("preclaim.flac", bytes)
        val trackId = UUID.randomUUID().toString()
        val operationId = UUID.randomUUID().toString()
        val relativePath = "${folderRelativePath(OfflineLibrary.IMPORT_FOLDER_ID)}/$trackId.flac"
        val finalAudio = musicFile(relativePath)
        val stagedAudio = File(finalAudio.parentFile, ".incoming-$operationId").apply { writeBytes(bytes) }
        val details = JSONObject()
            .put("track", JSONObject()
                .put("id", trackId)
                .put("title", "Unclaimed")
                .put("artist", "Artist")
                .put("album", "Album")
                .put("album_artist", "Album Artist")
                .put("disc", 1)
                .put("track", 1)
                .put("duration_ms", 1_000)
                .put("mime", "audio/flac")
                .put("codec", "flac")
                .put("quality", "original")
                .put("byte_size", bytes.size)
                .put("sha256", sha256(bytes))
                .put("folder_id", OfflineLibrary.IMPORT_FOLDER_ID)
                .put("relative_path", relativePath)
                .put("artwork_path", JSONObject.NULL))
            .put("staged_audio", stagedAudio.absolutePath)
            .put("final_audio", finalAudio.absolutePath)
            .put("staged_artwork", "")
            .put("final_artwork", "")
            .put("commit_authorized", false)
        try {
            database().insertOrThrow("operations", null, ContentValues().apply {
                put("id", operationId)
                put("kind", "import")
                put("details", details.toString())
                put("created_at", System.currentTimeMillis())
            })

            recoverNow()

            assertNull(library.track(trackId))
            assertFalse(stagedAudio.exists())
            assertFalse(finalAudio.exists())
            assertTrue(source.exists())
            assertArrayEquals(bytes, source.readBytes())
            assertEquals(0, database().query(
                "operations", arrayOf("id"), "id=?", arrayOf(operationId), null, null, null,
            ).use { it.count })
        } finally {
            database().delete("operations", "id=?", arrayOf(operationId))
            stagedAudio.delete()
            finalAudio.delete()
            source.delete()
            if (library.track(trackId) != null) library.deleteTracks(listOf(trackId))
        }
    }

    @Test
    fun interruptedMoveJournalReconcilesFileAndIndexWithoutChangingIdentity() {
        val marker = UUID.randomUUID().toString()
        val bytes = "recover-$marker".toByteArray()
        val item = library.importTrack(temporary("recover.flac", bytes), manifest(bytes, "Recover"))
        val source = musicFile(item.relativePath)
        val destinationRelative = "Imported music/recovered-$marker.flac"
        val destination = musicFile(destinationRelative)
        val operationId = UUID.randomUUID().toString()
        try {
            database().insertOrThrow("operations", null, ContentValues().apply {
                put("id", operationId)
                put("kind", "move_track")
                put("details", JSONObject()
                    .put("track_id", item.id)
                    .put("source", item.relativePath)
                    .put("destination", destinationRelative)
                    .put("folder_id", OfflineLibrary.IMPORT_FOLDER_ID)
                    .toString())
                put("created_at", System.currentTimeMillis())
            })
            Files.move(source.toPath(), destination.toPath())
            recoverNow()

            val recovered = library.track(item.id)!!
            assertEquals(item.id, recovered.id)
            assertEquals(item.sha256, recovered.sha256)
            assertEquals(destinationRelative, recovered.relativePath)
            assertFalse(source.exists())
            assertArrayEquals(bytes, destination.readBytes())
            assertEquals(0, database().query("operations", arrayOf("id"), "id=?", arrayOf(operationId), null, null, null).use { it.count })
        } finally {
            database().delete("operations", "id=?", arrayOf(operationId))
            if (library.track(item.id) != null) library.deleteTracks(listOf(item.id))
            destination.delete()
        }
    }

    @Test
    fun pendingMoveIsReconciledBeforeAnotherFolderMutationCanObscureItsDestination() {
        val marker = UUID.randomUUID().toString()
        val sourceFolder = library.createFolder(OfflineLibrary.ROOT_FOLDER_ID, "source-$marker")
        val destinationFolder = library.createFolder(OfflineLibrary.ROOT_FOLDER_ID, "destination-$marker")
        val outerFolder = library.createFolder(OfflineLibrary.ROOT_FOLDER_ID, "outer-$marker")
        val bytes = "move-chain-$marker".toByteArray()
        val item = library.importTrack(temporary("chain.flac", bytes), manifest(bytes, "Move chain"), sourceFolder.id)
        val source = musicFile(item.relativePath)
        val destinationRelative = "destination-$marker/recovered-$marker.flac"
        val destination = musicFile(destinationRelative)
        val operationId = UUID.randomUUID().toString()
        try {
            database().insertOrThrow("operations", null, ContentValues().apply {
                put("id", operationId)
                put("kind", "move_track")
                put("details", JSONObject()
                    .put("track_id", item.id)
                    .put("source", item.relativePath)
                    .put("destination", destinationRelative)
                    .put("folder_id", destinationFolder.id)
                    .toString())
                put("created_at", System.currentTimeMillis())
            })
            Files.move(source.toPath(), destination.toPath())

            library.moveFolder(destinationFolder.id, outerFolder.id, "nested-$marker")

            val recovered = library.track(item.id)!!
            assertEquals("outer-$marker/nested-$marker/recovered-$marker.flac", recovered.relativePath)
            assertArrayEquals(bytes, musicFile(recovered.relativePath).readBytes())
            assertFalse(source.exists())
            assertFalse(destination.exists())
        } finally {
            database().delete("operations", "id=?", arrayOf(operationId))
            if (library.folder(outerFolder.id) != null) library.deleteFolder(outerFolder.id)
            if (library.folder(destinationFolder.id) != null) library.deleteFolder(destinationFolder.id)
            if (library.folder(sourceFolder.id) != null) library.deleteFolder(sourceFolder.id)
            if (library.track(item.id) != null) library.deleteTracks(listOf(item.id))
        }
    }

    @Test
    fun coldStartRecoversImportedFolderRenameBeforeReservedDirectoryCreation() {
        val marker = UUID.randomUUID().toString()
        val original = library.folder(OfflineLibrary.IMPORT_FOLDER_ID)!!
        val originalRelative = folderRelativePath(OfflineLibrary.IMPORT_FOLDER_ID)
        val recoveredName = "cold-start-$marker"
        val recoveredRelative = recoveredName
        val source = musicFile(originalRelative)
        val destination = musicFile(recoveredRelative)
        val operationId = UUID.randomUUID().toString()
        var importedTrackId: String? = null
        try {
            database().insertOrThrow("operations", null, ContentValues().apply {
                put("id", operationId)
                put("kind", "move_folder")
                put("details", JSONObject()
                    .put("folder_id", OfflineLibrary.IMPORT_FOLDER_ID)
                    .put("source", originalRelative)
                    .put("destination", recoveredRelative)
                    .put("parent_id", OfflineLibrary.ROOT_FOLDER_ID)
                    .put("name", recoveredName)
                    .toString())
                put("created_at", System.currentTimeMillis())
            })
            Files.move(source.toPath(), destination.toPath())

            val restarted = coldStartLibrary()
            assertFalse(source.exists())
            assertTrue(destination.isDirectory)
            assertEquals(recoveredName, restarted.folder(OfflineLibrary.IMPORT_FOLDER_ID)?.name)
            val bytes = "cold-import-$marker".toByteArray()
            val imported = restarted.importTrack(temporary("cold.flac", bytes), manifest(bytes, "Cold import"))
            importedTrackId = imported.id
            assertTrue(imported.relativePath.startsWith("$recoveredName/"))
            assertArrayEquals(bytes, musicFile(imported.relativePath).readBytes())
        } finally {
            val active = OfflineLibrary.get(context)
            database().delete("operations", "id=?", arrayOf(operationId))
            importedTrackId?.let { id -> if (active.track(id) != null) active.deleteTracks(listOf(id)) }
            val current = active.folder(OfflineLibrary.IMPORT_FOLDER_ID)
            if (current != null && (current.parentId != original.parentId || current.name != original.name)) {
                active.moveFolder(
                    OfflineLibrary.IMPORT_FOLDER_ID,
                    original.parentId ?: OfflineLibrary.ROOT_FOLDER_ID,
                    original.name,
                )
            }
        }
    }

    @Test
    fun interruptedDeletionJournalFinishesReferencesAndLeavesNoFalseCompletedRow() {
        val marker = UUID.randomUUID().toString()
        val bytes = "delete-recover-$marker".toByteArray()
        val item = library.importTrack(temporary("delete-recover.flac", bytes), manifest(bytes, "Delete recovery"))
        val playlist = library.savePlaylist(null, "delete-recovery-$marker", listOf(item.id, item.id))
        library.saveQueue(OfflineQueue(listOf(OfflineQueueEntry("delete-$marker", item.id)), "delete-$marker", 99))
        val source = musicFile(item.relativePath)
        val operationId = UUID.randomUUID().toString()
        val trash = File(File(context.filesDir, "offline/.trash"), "$operationId-audio")
        try {
            database().insertOrThrow("operations", null, ContentValues().apply {
                put("id", operationId)
                put("kind", "delete_track")
                put("details", JSONObject()
                    .put("track_id", item.id)
                    .put("source", item.relativePath)
                    .put("trash", trash.absolutePath)
                    .put("artwork", "")
                    .toString())
                put("created_at", System.currentTimeMillis())
            })
            Files.move(source.toPath(), trash.toPath())
            recoverNow()

            assertNull(library.track(item.id))
            assertTrue(library.playlist(playlist.id)!!.trackIds.isEmpty())
            assertTrue(library.loadQueue().entries.isEmpty())
            assertFalse(source.exists())
            assertFalse(trash.exists())
        } finally {
            database().delete("operations", "id=?", arrayOf(operationId))
            if (library.playlist(playlist.id) != null) library.deletePlaylist(playlist.id)
            library.saveQueue(OfflineQueue())
            if (library.track(item.id) != null) library.deleteTracks(listOf(item.id))
            trash.delete()
        }
    }


    @Test
    fun folderOperationsRejectEscapesSymlinksAndDescendantCyclesWithoutTouchingOutsideFiles() {
        val marker = UUID.randomUUID().toString()
        expectCode("invalid_path") { library.createFolder(OfflineLibrary.ROOT_FOLDER_ID, "../escape-$marker") }
        expectCode("invalid_path") { library.deleteFolder(OfflineLibrary.ROOT_FOLDER_ID) }
        expectCode("invalid_path") { library.deleteFolder(OfflineLibrary.IMPORT_FOLDER_ID) }
        val parent = library.createFolder(OfflineLibrary.ROOT_FOLDER_ID, "safe-$marker")
        val child = library.createFolder(parent.id, "child-$marker")
        expectCode("invalid_path") { library.moveFolder(parent.id, child.id) }

        val outside = temporary("outside.txt", "outside-$marker".toByteArray())
        val link = musicFile("safe-$marker/link-$marker")
        try {
            Files.createSymbolicLink(link.toPath(), outside.toPath())
            expectCode("invalid_path") { library.deleteFolder(parent.id) }
            assertTrue(outside.exists())
            assertNotNull(library.folder(parent.id))
        } finally {
            Files.deleteIfExists(link.toPath())
            outside.delete()
            if (library.folder(parent.id) != null) library.deleteFolder(parent.id)
        }
    }

    @Test
    fun importedFolderCannotBeDeletedThroughAUserFolderAncestor() {
        val marker = UUID.randomUUID().toString()
        val original = library.folder(OfflineLibrary.IMPORT_FOLDER_ID)!!
        val wrapper = library.createFolder(OfflineLibrary.ROOT_FOLDER_ID, "wrapper-$marker")
        try {
            library.moveFolder(OfflineLibrary.IMPORT_FOLDER_ID, wrapper.id)
            expectCode("invalid_path") { library.deleteFolder(wrapper.id) }
            assertEquals(wrapper.id, library.folder(OfflineLibrary.IMPORT_FOLDER_ID)?.parentId)
            assertNotNull(library.folder(wrapper.id))
        } finally {
            val imported = library.folder(OfflineLibrary.IMPORT_FOLDER_ID)
            if (imported != null && (imported.parentId != original.parentId || imported.name != original.name)) {
                library.moveFolder(
                    OfflineLibrary.IMPORT_FOLDER_ID,
                    original.parentId ?: OfflineLibrary.ROOT_FOLDER_ID,
                    original.name,
                )
            }
            if (library.folder(wrapper.id) != null) library.deleteFolder(wrapper.id)
        }
    }

    @Test
    fun danglingSymlinkAtMoveDestinationNeverReplacesTheLinkOrMovesTheSource() {
        val marker = UUID.randomUUID().toString()
        val destinationFolder = library.createFolder(OfflineLibrary.ROOT_FOLDER_ID, "links-$marker")
        val bytes = "dangling-$marker".toByteArray()
        val item = library.importTrack(temporary("dangling.flac", bytes), manifest(bytes, "Dangling"))
        val source = musicFile(item.relativePath)
        val link = musicFile("links-$marker/collision.flac")
        val outsideTarget = File(context.cacheDir, "missing-$marker")
        outsideTarget.delete()
        try {
            Files.createSymbolicLink(link.toPath(), outsideTarget.toPath())
            expectCode("invalid_path") { library.moveTrack(item.id, destinationFolder.id, "collision.flac") }
            assertTrue(source.exists())
            assertTrue(Files.isSymbolicLink(link.toPath()))
            assertFalse(outsideTarget.exists())
            assertArrayEquals(bytes, source.readBytes())
        } finally {
            Files.deleteIfExists(link.toPath())
            outsideTarget.delete()
            if (library.track(item.id) != null) library.deleteTracks(listOf(item.id))
            if (library.folder(destinationFolder.id) != null) library.deleteFolder(destinationFolder.id)
        }
    }

    @Test
    fun concurrentIdenticalImportsProduceOneLocalIdentityAndConsumeBothTemporaries() {
        val marker = UUID.randomUUID().toString()
        val bytes = "concurrent-$marker".toByteArray()
        val sources = listOf(temporary("one.flac", bytes), temporary("two.flac", bytes))
        val start = CountDownLatch(1)
        val pool = Executors.newFixedThreadPool(2)
        try {
            val futures = sources.map { source ->
                pool.submit<OfflineTrack> {
                    start.await()
                    library.importTrack(source, manifest(bytes, "Concurrent"))
                }
            }
            start.countDown()
            val results = futures.map { it.get(20, TimeUnit.SECONDS) }
            assertEquals(results[0].id, results[1].id)
            assertTrue(sources.none(File::exists))
            assertEquals(1, library.tracks().count { it.id == results[0].id })
            library.deleteTracks(listOf(results[0].id))
        } finally {
            pool.shutdownNow()
            sources.forEach(File::delete)
        }
    }

    private fun temporary(name: String, bytes: ByteArray): File {
        val directory = File(context.cacheDir, "offline-library-tests").apply { mkdirs() }
        return File(directory, "${UUID.randomUUID()}-$name").apply { writeBytes(bytes) }
    }

    private fun musicFile(relativePath: String): File = File(File(context.filesDir, "offline/music"), relativePath)

    private fun manifest(
        bytes: ByteArray,
        title: String,
        quality: String = "original",
        mime: String = "audio/flac",
        codec: String = "flac",
    ): JSONObject = JSONObject()
        .put("status", "ready")
        .put("title", title)
        .put("artist", "Artist")
        .put("album", "Album")
        .put("album_artist", "Album Artist")
        .put("disc", 1)
        .put("track", 1)
        .put("duration_ms", 1_000)
        .put("quality", quality)
        .put("mime", mime)
        .put("codec", codec)
        .put("byte_size", bytes.size)
        .put("sha256", sha256(bytes))

    private fun sha256(bytes: ByteArray): String = MessageDigest.getInstance("SHA-256")
        .digest(bytes)
        .joinToString("") { "%02x".format(it) }

    private fun database(value: OfflineLibrary = library): SQLiteDatabase {
        val field = OfflineLibrary::class.java.getDeclaredField("database")
        field.isAccessible = true
        return field.get(value) as SQLiteDatabase
    }

    private fun folderRelativePath(id: String): String = database().query(
        "folders", arrayOf("relative_path"), "id=?", arrayOf(id), null, null, null,
    ).use { cursor ->
        assertTrue(cursor.moveToFirst())
        cursor.getString(0)
    }

    private fun coldStartLibrary(): OfflineLibrary {
        val current = library
        database(current).close()
        val instance = OfflineLibrary::class.java.getDeclaredField("instance")
        instance.isAccessible = true
        instance.set(null, null)
        return OfflineLibrary.get(context)
    }

    private fun recoverNow() {
        val method = OfflineLibrary::class.java.getDeclaredMethod("recoverLocked")
        method.isAccessible = true
        method.invoke(library)
    }

    private fun expectCode(code: String, action: () -> Unit) {
        try {
            action()
            fail("Expected OfflineLibraryException($code)")
        } catch (error: OfflineLibraryException) {
            assertEquals(code, error.code)
        }
    }
}
