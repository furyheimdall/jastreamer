package io.jastreamer.android

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

class OfflineTransferPolicyTest {
    private val origin = "https://music-box.local:8443"

    @Test
    fun `download file paths stay on the exact immutable job route`() {
        assertEquals(
            "$origin/api/v1/downloads/job_1/files/3",
            OfflineTransferPolicy.fileUrl(
                origin,
                "job_1",
                3,
                "/api/v1/downloads/job_1/files/3",
            ).toString(),
        )
        listOf(
            "https://other.local/api/v1/downloads/job_1/files/3",
            "//other.local/api/v1/downloads/job_1/files/3",
            "/api/v1/downloads/job_2/files/3",
            "/api/v1/downloads/job_1/files/4",
            "/api/v1/downloads/job_1/files/3?token=secret",
            "/api/v1/downloads/job_1/files/../4",
        ).forEach { path ->
            assertThrows(path, OfflineDownloadException::class.java) {
                OfflineTransferPolicy.fileUrl(origin, "job_1", 3, path)
            }
        }
    }

    @Test
    fun `resumed response must cover exactly the expected remaining bytes`() {
        OfflineTransferPolicy.validateContentRange("bytes 100-199/200", 100, 200, 100)
        listOf(
            "bytes 0-99/200",
            "bytes 100-198/200",
            "bytes 100-199/201",
            "bytes 100-200/200",
            "items 100-199/200",
        ).forEach { value ->
            assertThrows(value, OfflineDownloadException::class.java) {
                OfflineTransferPolicy.validateContentRange(value, 100, 200, 100)
            }
        }
    }

    @Test
    fun `only strong checksum validator resumes a partial`() {
        val digest = "a".repeat(64)
        assertTrue(OfflineTransferPolicy.strongEtagMatches("\"$digest\"", digest))
        assertEquals(false, OfflineTransferPolicy.strongEtagMatches(digest, digest))
        assertEquals(false, OfflineTransferPolicy.strongEtagMatches("W/\"$digest\"", digest))
        assertEquals(false, OfflineTransferPolicy.strongEtagMatches("\"${"b".repeat(64)}\"", digest))
    }

    @Test
    fun `target parser rejects extra native authority`() {
        assertEquals(
            "playlist" to "list-1",
            OfflineTransferPolicy.requireTarget(JSONObject().put("kind", "playlist").put("id", "list-1")),
        )
        assertThrows(OfflineDownloadException::class.java) {
            OfflineTransferPolicy.requireTarget(
                JSONObject().put("kind", "track").put("id", "one").put("url", "file:///sdcard/music"),
            )
        }
    }
}
