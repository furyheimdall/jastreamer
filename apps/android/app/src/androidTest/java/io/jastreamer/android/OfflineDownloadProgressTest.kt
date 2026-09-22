package io.jastreamer.android

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.webkit.ProfileStore
import androidx.webkit.WebViewFeature
import java.security.MessageDigest
import java.util.UUID
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
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
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class OfflineDownloadProgressTest {
    private val context get() = InstrumentationRegistry.getInstrumentation().targetContext
    private val library get() = OfflineLibrary.get(context)
    private val fixtures = mutableListOf<ProgressFixture>()
    private val endpoints = mutableListOf<ServerEndpoint>()
    private val jobIds = mutableListOf<String>()
    private val ownedTitles = mutableSetOf<String>()

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
        jobIds.forEach { OfflineDownloads.cancel(context, it) }
        endpoints.forEach { endpoint -> runCatching { clearCookies(endpoint) } }
        library.tracks().filter { it.title in ownedTitles }.forEach { track ->
            runCatching { library.deleteTracks(listOf(track.id)) }
        }
        fixtures.forEach { runCatching { it.close() } }
    }

    @Test
    fun singlePreparedTrackFinishesInOneWorkerRunWithoutExternalRescheduling(): Unit = runBlocking {
        val fixture = fixture(mixed = false)
        val jobId = enqueue(fixture)

        val retry = withTimeout(20_000) { OfflineDownloads.process(context, {}) }
        assertFalse("Preparation is normal progress, not a worker retry", retry)

        assertTrue("The worker did not continue polling preparation", fixture.pollCount.get() >= 3)
        assertEquals("completed", OfflineDownloads.jobs.value.single { it.id == jobId }.status)
        assertImported(fixture)
    }

    @Test
    fun readyTracksImportWhileTheirSiblingPreparesAndTheSameWorkerFinishesTheBatch(): Unit = runBlocking {
        val fixture = fixture(mixed = true)
        val jobId = enqueue(fixture)

        val retry = withTimeout(20_000) { OfflineDownloads.process(context, {}) }
        assertFalse("Mixed readiness must not enter WorkManager backoff", retry)

        assertTrue("The worker did not repoll the partially ready manifest", fixture.pollCount.get() >= 3)
        val completed = OfflineDownloads.jobs.value.single { it.id == jobId }
        assertEquals("completed", completed.status)
        assertEquals(2, completed.completedTracks)
        assertImported(fixture)
    }

    @Test
    fun explicitServerNotReadyResponseContinuesPreparationWithoutErrorBackoff(): Unit = runBlocking {
        val fixture = fixture(mixed = false, respondNotReady = true)
        val jobId = enqueue(fixture)

        val retry = withTimeout(20_000) { OfflineDownloads.process(context, {}) }

        assertFalse("Server not-ready is preparation, not a transport retry", retry)
        assertTrue(fixture.pollCount.get() >= 3)
        assertEquals("completed", OfflineDownloads.jobs.value.single { it.id == jobId }.status)
        assertImported(fixture)
    }

    @Test
    fun pausingDuringPreparationStopsTheContinuousPollLoop(): Unit = runBlocking {
        val fixture = fixture(mixed = false).apply { blockFirstPoll = true }
        val jobId = enqueue(fixture)
        val processing = async(Dispatchers.IO) { OfflineDownloads.process(context, {}) }
        assertTrue("Timed out waiting for the preparation poll", fixture.firstPollEntered.await(10, TimeUnit.SECONDS))

        OfflineDownloads.pause(context, jobId)
        fixture.firstPollRelease.countDown()
        val retry = withTimeout(10_000) { processing.await() }

        assertFalse(retry)
        assertEquals("paused", OfflineDownloads.jobs.value.single { it.id == jobId }.status)
        assertEquals(0, fixture.fileRequestCount.get())
    }

    @Test
    fun resumedDownloadFinishesWhileAnotherJobIsStillPreparing(): Unit = runBlocking {
        val sibling = fixture(mixed = false).apply { holdPreparation = true }
        val siblingId = enqueue(sibling)
        val resumed = fixture(mixed = false).apply { blockFirstPoll = true }
        val resumedId = enqueue(resumed)
        val processing = async(Dispatchers.IO) { OfflineDownloads.process(context, {}) }
        try {
            assertTrue("Timed out waiting for the preparation poll", resumed.firstPollEntered.await(10, TimeUnit.SECONDS))
            OfflineDownloads.pause(context, resumedId)
            val siblingPolls = sibling.pollCount.get()
            resumed.firstPollRelease.countDown()
            withTimeout(10_000) {
                while (sibling.pollCount.get() == siblingPolls) delay(10)
            }

            OfflineDownloads.resume(context, resumedId)
            withTimeout(10_000) {
                OfflineDownloads.jobs.first { jobs -> jobs.any { it.id == resumedId && it.status == "completed" } }
            }
            assertEquals("preparing", OfflineDownloads.jobs.value.single { it.id == siblingId }.status)
            assertImported(resumed)
        } finally {
            resumed.firstPollRelease.countDown()
            OfflineDownloads.cancel(context, resumedId)
            OfflineDownloads.cancel(context, siblingId)
            processing.cancelAndJoin()
        }
    }

    private suspend fun enqueue(fixture: ProgressFixture): String {
        val endpoint = ServerEndpoint(
            id = fixture.serverId,
            name = "Progress fixture",
            version = "test",
            origin = fixture.origin,
        ).also(endpoints::add)
        setCookie(endpoint, "progress_token=${fixture.marker}; Path=/")
        fixture.titles.forEach(ownedTitles::add)
        return OfflineDownloads.enqueue(
            context,
            endpoint,
            JSONObject().put("kind", "album").put("id", "target-${fixture.marker}"),
            "original",
        ).also(jobIds::add)
    }

    private fun assertImported(fixture: ProgressFixture) {
        fixture.titles.forEachIndexed { index, title ->
            val track = library.tracks().single { it.title == title }
            library.openAudio(track.id).use { handle ->
                assertArrayEquals(fixture.bytes[index], handle.input.readBytes())
            }
        }
    }

    private fun fixture(mixed: Boolean, respondNotReady: Boolean = false): ProgressFixture =
        ProgressFixture(UUID.randomUUID().toString(), mixed, respondNotReady).also(fixtures::add)

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

    private class ProgressFixture(
        val marker: String,
        private val mixed: Boolean,
        private val respondNotReady: Boolean,
    ) : AutoCloseable {
        val serverId: String = UUID.randomUUID().toString()
        val remoteId = "remote-$marker"
        val pollCount = AtomicInteger()
        val firstPollEntered = CountDownLatch(1)
        val firstPollRelease = CountDownLatch(1)
        val fileRequestCount = AtomicInteger()
        @Volatile var blockFirstPoll = false
        @Volatile var holdPreparation = false
        val bytes = List(if (mixed) 2 else 1) { index -> "prepared-$marker-$index".toByteArray() }
        val titles = bytes.indices.map { index -> "prepared-title-$marker-$index" }
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
                            .put("name", "Progress fixture")
                            .put("version", "test"),
                    )
                    request.path == "/api/v1/session" -> json(
                        JSONObject()
                            .put("authenticated", true)
                            .put("user", JSONObject().put("id", "account-$marker")),
                    )
                    request.method == "POST" && request.path == "/api/v1/downloads" -> json(manifest(0))
                    request.method == "GET" && request.path == "/api/v1/downloads/$remoteId" -> {
                        val poll = pollCount.incrementAndGet()
                        if (poll == 1) {
                            firstPollEntered.countDown()
                            if (blockFirstPoll) firstPollRelease.await(10, TimeUnit.SECONDS)
                        }
                        if (respondNotReady && poll < 3) {
                            MockResponse()
                                .setResponseCode(409)
                                .setHeader("Content-Type", "application/json")
                                .setBody(JSONObject().put("error", JSONObject().put("code", "not_ready")).toString())
                        } else {
                            json(manifest(poll))
                        }
                    }
                    request.method == "DELETE" && request.path == "/api/v1/downloads/$remoteId" ->
                        MockResponse().setResponseCode(204)
                    request.method == "GET" && request.path?.startsWith("/api/v1/downloads/$remoteId/files/") == true -> {
                        val index = request.path!!.substringAfterLast('/').toInt()
                        fileRequestCount.incrementAndGet()
                        MockResponse()
                            .setResponseCode(200)
                            .setHeader("Content-Type", "audio/flac")
                            .setHeader("ETag", "\"${sha256(bytes[index])}\"")
                            .setBody(okio.Buffer().write(bytes[index]))
                    }
                    else -> MockResponse().setResponseCode(404)
                }
            }
            server.start()
            val url = server.url("/")
            origin = "${url.scheme}://${url.host}:${url.port}"
        }

        override fun close() {
            firstPollRelease.countDown()
            server.shutdown()
        }

        private fun manifest(poll: Int): JSONObject {
            val tracks = JSONArray()
            bytes.indices.forEach { index ->
                val ready = !holdPreparation && (poll >= 3 || (mixed && index == 0))
                tracks.put(JSONObject()
                    .put("index", index)
                    .put("status", if (ready) "ready" else "preparing")
                    .put("title", titles[index])
                    .put("artist", "Progress artist")
                    .put("album", "Progress album")
                    .put("album_artist", "Progress artist")
                    .put("disc", 1)
                    .put("track", index + 1)
                    .put("duration_ms", 1_000)
                    .put("source_version", "version-$marker-$index")
                    .put("quality", "original")
                    .put("mime", "audio/flac")
                    .put("codec", "flac")
                    .put("byte_size", bytes[index].size)
                    .put("sha256", sha256(bytes[index]))
                    .put("media_path", "/api/v1/downloads/$remoteId/files/$index")
                    .put("artwork_path", ""))
            }
            val allReady = !holdPreparation && bytes.indices.all { poll >= 3 || (mixed && it == 0) }
            return JSONObject()
                .put("id", remoteId)
                .put("kind", "album")
                .put("title", "Progress album $marker")
                .put("quality", "original")
                .put("status", if (allReady) "ready" else "preparing")
                .put("tracks", tracks)
        }

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
