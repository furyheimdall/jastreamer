package io.jastreamer.android

import java.nio.file.Files
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Test

class OfflineDownloadStoreTest {
    @Test
    fun `pending folder target survives store reload`() {
        val root = Files.createTempDirectory("offline-download-store").toFile()
        try {
            val (_, targetID) = OfflineTransferPolicy.requireTarget(
                JSONObject()
                    .put("kind", "folder")
                    .put("root_id", "music")
                    .put("path", "Albums/Live"),
            )
            val endpoint = ServerEndpoint(
                id = "11111111-1111-4111-8111-111111111111",
                name = "Server",
                version = "0.2.0",
                origin = "https://music-box.local:8443",
            )
            val job = StoredDownloadJob(
                id = "local-job",
                title = "Live",
                server = endpoint,
                principalId = "account",
                remoteId = "remote-job",
                kind = "folder",
                targetId = targetID,
                status = "preparing",
                quality = "original",
                folderId = OfflineLibrary.IMPORT_FOLDER_ID,
                tracks = mutableListOf(),
            )

            OfflineDownloadStore(root).save(listOf(job))
            val restored = OfflineDownloadStore(root).load().single()

            assertEquals("folder", restored.kind)
            assertEquals(targetID, restored.targetId)
            assertEquals("music" to "Albums/Live", OfflineTransferClient.decodeFolderTarget(restored.targetId!!))
            assertEquals("preparing", restored.status)
            assertNotNull(restored.server)
        } finally {
            root.deleteRecursively()
        }
    }
}
