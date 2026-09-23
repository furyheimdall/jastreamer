package io.jastreamer.android

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.webkit.ProfileStore
import androidx.webkit.WebViewFeature
import java.security.MessageDigest
import java.util.UUID
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import org.json.JSONArray
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class OfflineDownloadHistoryTest {
    private val context get() = InstrumentationRegistry.getInstrumentation().targetContext
    private val library get() = OfflineLibrary.get(context)
    private val fixtures = mutableListOf<HistoryFixture>()
    private val endpoints = mutableListOf<ServerEndpoint>()
    private val jobIds = mutableListOf<String>()
    private val ownedTitles = mutableSetOf<String>()
    private val ownedPlaylistNames = mutableSetOf<String>()

    @Before
    fun requireProfileSupport() {
        assertTrue(
            "The installed WebView provider must support isolated profiles",
            WebViewFeature.isFeatureSupported(WebViewFeature.MULTI_PROFILE),
        )
        OfflineDownloads.initialize(context)
    }

    @After
    fun cleanUp(): Unit = runBlocking {
        jobIds.forEach { jobId ->
            OfflineDownloads.cancel(context, jobId)
            OfflineDownloads.removeHistory(context, jobId)
        }
        endpoints.forEach { endpoint -> runCatching { clearCookies(endpoint) } }
        library.playlists().filter { it.name in ownedPlaylistNames }.forEach { playlist ->
            runCatching { library.deletePlaylist(playlist.id) }
        }
        library.tracks().filter { it.title in ownedTitles }.forEach { track ->
            runCatching { library.deleteTracks(listOf(track.id)) }
        }
        fixtures.forEach { runCatching { it.close() } }
    }

    @Test
    fun deletingHistoryPreservesSavedLibraryStateAndNeverDropsPausedWork(): Unit = runBlocking {
        val originalQueue = library.loadQueue()
        val completedFixture = fixture(kind = "playlist", ready = true)
        val completedId = enqueue(completedFixture)
        withTimeout(15_000) { OfflineDownloads.process(context, {}) }
        assertEquals("completed", OfflineDownloads.jobs.value.single { it.id == completedId }.status)

        val savedTrack = library.tracks().single { it.title == completedFixture.title }
        val savedPlaylist = library.playlists().single { it.name == completedFixture.title }
        val duplicateQueue = OfflineQueue(
            entries = listOf(
                OfflineQueueEntry("history-first-${completedFixture.marker}", savedTrack.id),
                OfflineQueueEntry("history-second-${completedFixture.marker}", savedTrack.id),
            ),
            currentEntryId = "history-second-${completedFixture.marker}",
            positionMs = 321,
        )
        library.saveQueue(duplicateQueue)

        try {
            val activeFixture = fixture(kind = "track", ready = false)
            val activeId = enqueue(activeFixture)
            OfflineDownloads.pause(context, activeId)
            assertEquals("paused", OfflineDownloads.jobs.value.single { it.id == activeId }.status)

            OfflineDownloads.removeHistory(context, activeId)
            assertEquals("paused", OfflineDownloads.jobs.value.single { it.id == activeId }.status)
            assertTrue(OfflineDownloadStore(context.filesDir).load().any { it.id == activeId })

            OfflineDownloads.removeHistory(context, completedId)
            assertFalse(OfflineDownloads.jobs.value.any { it.id == completedId })
            assertFalse(OfflineDownloadStore(context.filesDir).load().any { it.id == completedId })
            assertSavedState(savedTrack.id, savedPlaylist.id, duplicateQueue, completedFixture)

            OfflineDownloads.clearFinishedHistory(context)
            assertEquals("paused", OfflineDownloads.jobs.value.single { it.id == activeId }.status)
            assertTrue(OfflineDownloadStore(context.filesDir).load().any { it.id == activeId })

            OfflineDownloads.cancel(context, activeId)
            assertEquals("cancelled", OfflineDownloads.jobs.value.single { it.id == activeId }.status)
            OfflineDownloads.clearFinishedHistory(context)
            assertFalse(OfflineDownloads.jobs.value.any { it.id == activeId })
            assertFalse(OfflineDownloadStore(context.filesDir).load().any { it.id == activeId })
            assertSavedState(savedTrack.id, savedPlaylist.id, duplicateQueue, completedFixture)
        } finally {
            library.saveQueue(originalQueue)
        }
    }

    private fun assertSavedState(
        trackId: String,
        playlistId: String,
        expectedQueue: OfflineQueue,
        fixture: HistoryFixture,
    ) {
        val track = library.track(trackId)
        assertNotNull(track)
        library.openAudio(trackId).use { handle -> assertArrayEquals(fixture.bytes, handle.input.readBytes()) }
        val playlist = library.playlist(playlistId)
        assertNotNull(playlist)
        assertEquals(listOf(trackId), playlist?.trackIds)
        assertEquals(expectedQueue, library.loadQueue())
    }

    private suspend fun enqueue(fixture: HistoryFixture): String {
        val endpoint = ServerEndpoint(
            id = fixture.serverId,
            name = "History fixture",
            version = "test",
            origin = fixture.origin,
        ).also(endpoints::add)
        setCookie(endpoint, "history_token=${fixture.marker}; Path=/")
        ownedTitles += fixture.title
        if (fixture.kind == "playlist") ownedPlaylistNames += fixture.title
        return OfflineDownloads.enqueue(
            context,
            endpoint,
            JSONObject().put("kind", fixture.kind).put("id", "target-${fixture.marker}"),
            "original",
        ).also(jobIds::add)
    }

    private fun fixture(kind: String, ready: Boolean): HistoryFixture =
        HistoryFixture(UUID.randomUUID().toString(), kind, ready).also(fixtures::add)

    private suspend fun setCookie(endpoint: ServerEndpoint, value: String) {
        val completed = CountDownLatch(1)
        val accepted = AtomicReference<Boolean>()
        withContext(Dispatchers.Main) {
            ProfileStore.getInstance()
                .getOrCreateProfile(EndpointPolicy.profileName(endpoint))
                .cookieManager
                .setCookie(endpoint.origin, value) { result ->
                    accepted.set(result)
                    completed.countDown()
                }
        }
        assertTrue("Timed out setting the isolated profile cookie", completed.await(10, TimeUnit.SECONDS))
        assertEquals(true, accepted.get())
    }

    private suspend fun clearCookies(endpoint: ServerEndpoint) {
        val completed = CountDownLatch(1)
        withContext(Dispatchers.Main) {
            ProfileStore.getInstance()
                .getOrCreateProfile(EndpointPolicy.profileName(endpoint))
                .cookieManager
                .removeAllCookies { completed.countDown() }
        }
        assertTrue("Timed out clearing the isolated profile cookie", completed.await(10, TimeUnit.SECONDS))
    }

    private class HistoryFixture(
        val marker: String,
        val kind: String,
        private val ready: Boolean,
    ) : AutoCloseable {
        val serverId: String = UUID.randomUUID().toString()
        val remoteId = "remote-$marker"
        val title = "history-title-$marker"
        val bytes = "history-audio-$marker".toByteArray()
        private val digest = sha256(bytes)
        private val server = MockWebServer()
        val origin: String

        init {
            server.dispatcher = object : Dispatcher() {
                override fun dispatch(request: RecordedRequest): MockResponse = when {
                    request.path == "/api/v1/discovery" -> json(
                        JSONObject()
                            .put("product", "jastreamer")
                            .put("protocol", 1)
                            .put("id", serverId)
                            .put("name", "History fixture")
                            .put("version", "test"),
                    )
                    request.path == "/api/v1/session" -> json(
                        JSONObject()
                            .put("authenticated", true)
                            .put("user", JSONObject().put("id", "account-$marker")),
                    )
                    request.method == "POST" && request.path == "/api/v1/downloads" -> json(manifest())
                    request.method == "GET" && request.path == "/api/v1/downloads/$remoteId" -> json(manifest())
                    request.method == "DELETE" && request.path == "/api/v1/downloads/$remoteId" ->
                        MockResponse().setResponseCode(204)
                    request.method == "GET" && request.path == "/api/v1/downloads/$remoteId/files/0" ->
                        MockResponse()
                            .setResponseCode(200)
                            .setHeader("Content-Type", "audio/flac")
                            .setHeader("ETag", "\"$digest\"")
                            .setBody(okio.Buffer().write(bytes))
                    else -> MockResponse().setResponseCode(404)
                }
            }
            server.start()
            val url = server.url("/")
            origin = "${url.scheme}://${url.host}:${url.port}"
        }

        override fun close() {
            server.shutdown()
        }

        private fun manifest(): JSONObject = JSONObject()
            .put("id", remoteId)
            .put("kind", kind)
            .put("title", title)
            .put("quality", "original")
            .put("status", if (ready) "ready" else "preparing")
            .put("tracks", JSONArray().put(JSONObject()
                .put("index", 0)
                .put("status", if (ready) "ready" else "preparing")
                .put("title", title)
                .put("artist", "History artist")
                .put("album", "History album")
                .put("album_artist", "History artist")
                .put("disc", 1)
                .put("track", 1)
                .put("duration_ms", 1_000)
                .put("source_version", "version-$marker")
                .put("quality", "original")
                .put("mime", "audio/flac")
                .put("codec", "flac")
                .put("byte_size", bytes.size)
                .put("sha256", digest)
                .put("media_path", "/api/v1/downloads/$remoteId/files/0")
                .put("artwork_path", "")))

        private fun json(body: JSONObject): MockResponse = MockResponse()
            .setResponseCode(200)
            .setHeader("Content-Type", "application/json")
            .setBody(body.toString())

        companion object {
            private fun sha256(value: ByteArray): String = MessageDigest.getInstance("SHA-256")
                .digest(value)
                .joinToString("") { "%02x".format(it.toInt() and 0xff) }
        }
    }
}
