package io.jastreamer.android

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.webkit.ProfileStore
import androidx.webkit.WebViewFeature
import java.security.MessageDigest
import java.util.UUID
import java.util.concurrent.ConcurrentLinkedQueue
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class OfflineDownloadsBoundaryTest {
    private val context get() = InstrumentationRegistry.getInstrumentation().targetContext
    private val library get() = OfflineLibrary.get(context)
    private val fixtures = mutableListOf<DownloadFixture>()
    private val endpoints = mutableListOf<ServerEndpoint>()
    private val jobIds = mutableListOf<String>()
    private val folderIds = mutableListOf<String>()
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
        fixtures.forEach { it.releaseAll() }
        jobIds.forEach { OfflineDownloads.cancel(context, it) }
        endpoints.forEach { endpoint -> runCatching { clearCookies(endpoint) } }
        library.tracks().filter { it.title in ownedTitles }.forEach { track ->
            runCatching { library.deleteTracks(listOf(track.id)) }
        }
        folderIds.asReversed().forEach { id ->
            if (library.folder(id) != null) runCatching { library.deleteFolder(id) }
        }
        fixtures.forEach { runCatching { it.close() } }
    }

    @Test
    fun logoutDuringDelayedCreationCannotRegisterTheReturnedAuthority(): Unit = runBlocking {
        val marker = UUID.randomUUID().toString()
        val fixture = fixture(marker, "logout-$marker".toByteArray()).apply { blockCreate = true }
        val endpoint = endpoint(fixture)
        setCookie(endpoint, "logout_token=$marker; Path=/")
        val jobsBefore = OfflineDownloads.jobs.value.map { it.id }.toSet()

        val enqueue = async(Dispatchers.IO) {
            runCatching {
                OfflineDownloads.enqueue(
                    context,
                    endpoint,
                    JSONObject().put("kind", "track").put("id", "target-$marker"),
                    "original",
                )
            }
        }
        assertTrue("Timed out waiting for the delayed create request", fixture.createEntered.await(10, TimeUnit.SECONDS))

        OfflineDownloads.logout(context, endpoint)
        fixture.createRelease.countDown()
        val failure = withTimeout(10_000) { enqueue.await() }.exceptionOrNull()

        assertTrue("The stale enqueue unexpectedly succeeded: $failure", failure is OfflineDownloadException)
        assertEquals("stopped", (failure as OfflineDownloadException).code)
        assertEquals(jobsBefore, OfflineDownloads.jobs.value.map { it.id }.toSet())
        assertTrue(library.tracks().none { it.title == fixture.title })
    }

    @Test
    fun cancelDuringDelayedMediaResponseCannotCommitOrResumeTheTransfer(): Unit = runBlocking {
        val marker = UUID.randomUUID().toString()
        val fixture = fixture(marker, "cancel-$marker".toByteArray()).apply { blockFile = true }
        val endpoint = endpoint(fixture)
        setCookie(endpoint, "cancel_token=$marker; Path=/")
        ownedTitles += fixture.title
        val jobId = enqueue(endpoint, marker)

        val processing = async(Dispatchers.IO) { OfflineDownloads.process(context, {}) }
        assertTrue("Timed out waiting for the delayed media request", fixture.fileEntered.await(10, TimeUnit.SECONDS))

        OfflineDownloads.cancel(context, jobId)
        fixture.fileRelease.countDown()
        withTimeout(10_000) { processing.await() }
        awaitJob(jobId) { it.status == "cancelled" }
        val fileRequests = fixture.fileRequestCount()

        OfflineDownloads.resume(context, jobId)
        withTimeout(10_000) { OfflineDownloads.process(context, {}) }

        assertEquals("cancelled", OfflineDownloads.jobs.value.single { it.id == jobId }.status)
        assertEquals(fileRequests, fixture.fileRequestCount())
        assertTrue(library.tracks().none { it.title == fixture.title })
    }

    @Test
    fun completedAudioSurvivesLogoutAndAnotherProfileCookieNeverCrossesOrigin(): Unit = runBlocking {
        val firstMarker = UUID.randomUUID().toString()
        val secondMarker = UUID.randomUUID().toString()
        val first = fixture(firstMarker, "unused-$firstMarker".toByteArray(), principal = "account-a")
        val secondBytes = "completed-$secondMarker".toByteArray()
        val second = fixture(secondMarker, secondBytes, principal = "account-b")
        val firstEndpoint = endpoint(first)
        val secondEndpoint = endpoint(second)
        setCookie(firstEndpoint, "alpha_token=$firstMarker; Path=/")
        setCookie(secondEndpoint, "beta_token=$secondMarker; Path=/")
        ownedTitles += second.title

        assertEquals("account-a", OfflineTransferClient(firstEndpoint).principal())
        val jobId = enqueue(secondEndpoint, secondMarker)
        withTimeout(15_000) { OfflineDownloads.process(context, {}) }
        val completed = awaitJob(jobId) { it.status == "completed" }
        val track = library.tracks().single { it.title == second.title }

        val firstCookies = first.authenticatedCookies()
        val secondCookies = second.authenticatedCookies()
        assertTrue("The first profile did not authenticate its request", firstCookies.any { "alpha_token=$firstMarker" in it })
        assertTrue(firstCookies.none { "beta_token=$secondMarker" in it })
        assertTrue("The download did not use its own profile cookie", secondCookies.isNotEmpty())
        assertTrue(secondCookies.all { "beta_token=$secondMarker" in it })
        assertTrue(secondCookies.none { "alpha_token=$firstMarker" in it })

        OfflineDownloads.logout(context, secondEndpoint)
        clearCookies(secondEndpoint)

        assertEquals("completed", completed.status)
        assertEquals("completed", OfflineDownloads.jobs.value.single { it.id == jobId }.status)
        library.openAudio(track.id).use { handle -> assertArrayEquals(secondBytes, handle.input.readBytes()) }
    }

    @Test
    fun activeTransfersResolveRenamedFoldersByIdAndDoNotRecreateDeletedDestinations(): Unit = runBlocking {
        val renameMarker = UUID.randomUUID().toString()
        val renameBytes = "renamed-$renameMarker".toByteArray()
        val renameFixture = fixture(renameMarker, renameBytes).apply { blockFile = true }
        val renameEndpoint = endpoint(renameFixture)
        setCookie(renameEndpoint, "rename_token=$renameMarker; Path=/")
        ownedTitles += renameFixture.title
        val originalName = "before-$renameMarker"
        val renamedName = "after-$renameMarker"
        val renamedFolder = library.createFolder(OfflineLibrary.ROOT_FOLDER_ID, originalName)
        folderIds += renamedFolder.id
        val renamedJob = enqueue(renameEndpoint, renameMarker, renamedFolder.id)
        val renameProcessing = async(Dispatchers.IO) { OfflineDownloads.process(context, {}) }
        assertTrue("Timed out waiting for the rename-boundary media request", renameFixture.fileEntered.await(10, TimeUnit.SECONDS))

        library.renameFolder(renamedFolder.id, renamedName)
        renameFixture.fileRelease.countDown()
        withTimeout(10_000) { renameProcessing.await() }
        awaitJob(renamedJob) { it.status == "completed" }
        val renamedTrack = library.tracks().single { it.title == renameFixture.title }
        assertEquals(renamedFolder.id, renamedTrack.folderId)
        assertTrue(renamedTrack.relativePath.startsWith("$renamedName/"))
        library.openAudio(renamedTrack.id).use { handle -> assertArrayEquals(renameBytes, handle.input.readBytes()) }

        val deleteMarker = UUID.randomUUID().toString()
        val deleteFixture = fixture(deleteMarker, "deleted-$deleteMarker".toByteArray()).apply { blockFile = true }
        val deleteEndpoint = endpoint(deleteFixture)
        setCookie(deleteEndpoint, "delete_token=$deleteMarker; Path=/")
        ownedTitles += deleteFixture.title
        val deletedName = "deleted-destination-$deleteMarker"
        val deletedFolder = library.createFolder(OfflineLibrary.ROOT_FOLDER_ID, deletedName)
        folderIds += deletedFolder.id
        val deletedJob = enqueue(deleteEndpoint, deleteMarker, deletedFolder.id)
        val deleteProcessing = async(Dispatchers.IO) { OfflineDownloads.process(context, {}) }
        assertTrue("Timed out waiting for the delete-boundary media request", deleteFixture.fileEntered.await(10, TimeUnit.SECONDS))

        val deletion = library.deleteFolder(deletedFolder.id)
        assertTrue(deletion.failures.isEmpty())
        assertNull(library.folder(deletedFolder.id))
        deleteFixture.fileRelease.countDown()
        withTimeout(10_000) { deleteProcessing.await() }
        val waiting = awaitJob(deletedJob) { it.errorCode == "destination_missing" }

        assertEquals("waiting", waiting.status)
        assertNull(library.folder(deletedFolder.id))
        assertFalse(library.folders().any { it.name == deletedName })
        assertTrue(library.tracks().none { it.title == deleteFixture.title })
    }

    private suspend fun enqueue(endpoint: ServerEndpoint, marker: String, folderId: String = OfflineLibrary.IMPORT_FOLDER_ID): String {
        val jobId = OfflineDownloads.enqueue(
            context,
            endpoint,
            JSONObject().put("kind", "track").put("id", "target-$marker"),
            "original",
            folderId,
        )
        jobIds += jobId
        return jobId
    }

    private suspend fun awaitJob(
        jobId: String,
        predicate: (OfflineDownloadJob) -> Boolean,
    ): OfflineDownloadJob = withTimeout(10_000) {
        while (true) {
            val job = OfflineDownloads.jobs.value.firstOrNull { it.id == jobId }
            if (job != null && predicate(job)) return@withTimeout job
            delay(25)
        }
        error("unreachable")
    }

    private fun fixture(marker: String, bytes: ByteArray, principal: String = "account-$marker"): DownloadFixture =
        DownloadFixture(marker, bytes, principal).also(fixtures::add)

    private fun endpoint(fixture: DownloadFixture): ServerEndpoint = ServerEndpoint(
        id = fixture.serverId,
        name = "Boundary fixture ${fixture.marker}",
        version = "test",
        origin = fixture.origin,
    ).also(endpoints::add)

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

    private class DownloadFixture(
        val marker: String,
        private val bytes: ByteArray,
        private val principal: String,
    ) : AutoCloseable {
        val serverId: String = UUID.randomUUID().toString()
        val remoteId = "remote-$marker"
        val title = "boundary-$marker"
        val createEntered = CountDownLatch(1)
        val createRelease = CountDownLatch(1)
        val fileEntered = CountDownLatch(1)
        val fileRelease = CountDownLatch(1)
        @Volatile var blockCreate = false
        @Volatile var blockFile = false
        private val requests = ConcurrentLinkedQueue<SeenRequest>()
        private val server = MockWebServer()
        val origin: String
        private val digest = sha256(bytes)

        init {
            server.dispatcher = object : Dispatcher() {
                override fun dispatch(request: RecordedRequest): MockResponse {
                    requests += SeenRequest(
                        method = request.method.orEmpty(),
                        path = request.path.orEmpty(),
                        cookie = request.getHeader("Cookie").orEmpty(),
                    )
                    return when {
                        request.path == "/api/v1/discovery" -> json(
                            JSONObject()
                                .put("product", "jastreamer")
                                .put("protocol", 1)
                                .put("id", serverId)
                                .put("name", "Boundary fixture")
                                .put("version", "test"),
                        )
                        request.path == "/api/v1/session" -> json(
                            JSONObject()
                                .put("authenticated", true)
                                .put("user", JSONObject().put("id", principal)),
                        )
                        request.method == "POST" && request.path == "/api/v1/downloads" -> {
                            createEntered.countDown()
                            if (blockCreate) createRelease.await(10, TimeUnit.SECONDS)
                            json(manifest())
                        }
                        request.method == "GET" && request.path == "/api/v1/downloads/$remoteId" -> json(manifest())
                        request.method == "DELETE" && request.path == "/api/v1/downloads/$remoteId" ->
                            MockResponse().setResponseCode(204)
                        request.method == "GET" && request.path == "/api/v1/downloads/$remoteId/files/0" -> {
                            fileEntered.countDown()
                            if (blockFile) fileRelease.await(10, TimeUnit.SECONDS)
                            MockResponse()
                                .setResponseCode(200)
                                .setHeader("Content-Type", "audio/flac")
                                .setHeader("ETag", "\"$digest\"")
                                .setBody(okio.Buffer().write(bytes))
                        }
                        else -> MockResponse().setResponseCode(404)
                    }
                }
            }
            server.start()
            val url = server.url("/")
            origin = "${url.scheme}://${url.host}:${url.port}"
        }

        fun authenticatedCookies(): List<String> = requests
            .filter { it.path == "/api/v1/session" || it.path.startsWith("/api/v1/downloads") }
            .map { it.cookie }
            .filter { it.isNotBlank() }

        fun fileRequestCount(): Int = requests.count { it.path == "/api/v1/downloads/$remoteId/files/0" }

        fun releaseAll() {
            createRelease.countDown()
            fileRelease.countDown()
        }

        override fun close() {
            releaseAll()
            server.shutdown()
        }

        private fun manifest(): JSONObject = JSONObject()
            .put("id", remoteId)
            .put("kind", "track")
            .put("title", title)
            .put("quality", "original")
            .put("status", "ready")
            .put("tracks", org.json.JSONArray().put(JSONObject()
                .put("index", 0)
                .put("status", "ready")
                .put("title", title)
                .put("artist", "Boundary artist")
                .put("album", "Boundary album")
                .put("album_artist", "Boundary artist")
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

        private data class SeenRequest(val method: String, val path: String, val cookie: String)

        companion object {
            private fun sha256(bytes: ByteArray): String = MessageDigest.getInstance("SHA-256")
                .digest(bytes)
                .joinToString("") { "%02x".format(it.toInt() and 0xff) }
        }
    }
}
