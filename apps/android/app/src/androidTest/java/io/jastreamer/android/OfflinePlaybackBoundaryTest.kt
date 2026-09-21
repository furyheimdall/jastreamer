package io.jastreamer.android

import android.content.ComponentName
import android.content.Intent
import android.os.Looper
import android.os.SystemClock
import android.widget.EditText
import androidx.media3.common.Player
import androidx.media3.session.MediaController
import androidx.media3.session.SessionToken
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import java.io.File
import java.nio.ByteBuffer
import java.nio.ByteOrder
import java.security.MessageDigest
import java.util.UUID
import java.util.concurrent.ConcurrentLinkedQueue
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference
import kotlin.math.PI
import kotlin.math.sin
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class OfflinePlaybackBoundaryTest {
    private val instrumentation = InstrumentationRegistry.getInstrumentation()
    private val context = instrumentation.targetContext
    private val library = OfflineLibrary.get(context)
    private val createdTrackIds = mutableListOf<String>()
    private val addedServers = mutableListOf<ServerEndpoint>()
    private var originalQueue: OfflineQueue? = null

    @Before
    fun setUp() {
        forceStopService()
        originalQueue = library.loadQueue()
        runBlocking { OfflinePlayback.restore(context) }
    }

    @After
    fun tearDown() {
        forceStopService()
        originalQueue?.let(library::saveQueue)
        if (createdTrackIds.isNotEmpty()) {
            val result = library.deleteTracks(createdTrackIds)
            check(result.failures.isEmpty()) { "Could not remove playback fixtures: ${result.failures}" }
        }
        val store = RecentServerStore(context)
        addedServers.forEach { server -> runCatching { store.remove(server) } }
        runBlocking { OfflinePlayback.restore(context) }
    }

    @Test
    fun localEngineSurvivesProfileBrowsingAndSystemCommandsStayLocal() {
        BrowserFixture().use { firstServer ->
            BrowserFixture().use { secondServer ->
                val first = importWav("Local first", 349.23)
                val second = importWav("Local second", 523.25)
                ActivityScenario.launch(MainActivity::class.java).use { scenario ->
                    runBlocking { OfflinePlayback.play(context, listOf(first.id, second.id)) }
                    await("local Media3 playback starts") {
                        OfflinePlayback.state.value.owner == "local" && OfflinePlayback.state.value.playing
                    }
                    val (controller, releaseController) = connectController()
                    try {
                        await("system controller sees local metadata") {
                            onMain {
                                controller.isConnected && controller.isPlaying &&
                                    controller.mediaMetadata.title?.toString() == first.title
                            }
                        }
                        assertTrue(onMain { controller.availableCommands.contains(Player.COMMAND_PLAY_PAUSE) })
                        assertTrue(onMain { controller.availableCommands.contains(Player.COMMAND_STOP) })
                        assertTrue(onMain { controller.availableCommands.contains(Player.COMMAND_SEEK_TO_NEXT_MEDIA_ITEM) })
                        assertTrue(onMain { controller.availableCommands.contains(Player.COMMAND_SET_REPEAT_MODE) })
                        assertTrue(onMain { controller.availableCommands.contains(Player.COMMAND_SET_SHUFFLE_MODE) })

                        val beforeBrowse = onMain { controller.currentPosition }
                        browse(scenario, firstServer)
                        await("local media advances while the first isolated profile is visible") {
                            onMain { controller.isPlaying && controller.currentPosition >= beforeBrowse + 400L }
                        }

                        scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }
                        instrumentation.waitForIdleSync()
                        val beforeProfileChange = onMain { controller.currentPosition }
                        browse(scenario, secondServer)
                        await("local media survives a change to another isolated profile") {
                            OfflinePlayback.state.value.owner == "local" && onMain {
                                controller.isPlaying && controller.currentPosition >= beforeProfileChange + 400L
                            }
                        }

                        onMain { controller.pause() }
                        await("system pause routes to the local owner") {
                            !OfflinePlayback.state.value.playing && onMain { !controller.isPlaying }
                        }
                        onMain { controller.seekTo(2_000L) }
                        await("system seek updates the local queue position") {
                            OfflinePlayback.state.value.queue.positionMs in 1_800L..2_300L
                        }
                        onMain { controller.seekToNextMediaItem() }
                        await("system next selects the second local track") {
                            currentTrackId() == second.id && onMain {
                                controller.mediaMetadata.title?.toString() == second.title
                            }
                        }
                        onMain {
                            controller.repeatMode = Player.REPEAT_MODE_ALL
                            controller.shuffleModeEnabled = true
                        }
                        await("system queue modes update local state") {
                            val queue = OfflinePlayback.state.value.queue
                            queue.repeatMode == Player.REPEAT_MODE_ALL && queue.shuffle
                        }
                        onMain { controller.play() }
                        await("system play resumes the local engine") {
                            OfflinePlayback.state.value.playing && onMain { controller.isPlaying }
                        }
                        onMain { controller.stop() }
                        await("system stop releases the local decoder") {
                            !OfflinePlayback.state.value.playing && onMain {
                                !controller.isPlaying && controller.playbackState == Player.STATE_IDLE
                            }
                        }

                        val unexpectedServerCalls =
                            firstServer.serverControlRequests() + secondServer.serverControlRequests()
                        assertTrue(
                            "Local MediaController commands reached a browsed Server: $unexpectedServerCalls",
                            unexpectedServerCalls.isEmpty(),
                        )
                        assertEquals("local", OfflinePlayback.state.value.owner)
                        assertEquals(listOf(first.id, second.id), OfflinePlayback.state.value.queue.entries.map { it.trackId })
                    } finally {
                        releaseController()
                    }
                }
            }
        }
    }

    @Test
    fun missingOwnedAudioStopsWithABoundedLocalFailure() {
        val track = importWav("Missing local", 440.0)
        val managed = managedAudio(track)
        assertTrue(managed.delete())

        ActivityScenario.launch(MainActivity::class.java).use {
            runBlocking { OfflinePlayback.play(context, listOf(track.id)) }
            val (controller, releaseController) = connectController()
            try {
                await("missing local audio fails without a retry loop", 8_000L) {
                    val state = OfflinePlayback.state.value
                    state.owner == "local" && !state.playing && state.errorCode == "local_playback"
                }
                assertTrue(onMain { !controller.isPlaying && controller.playbackState == Player.STATE_IDLE })
                assertEquals(track.id, currentTrackId())
            } finally {
                releaseController()
            }
        }
    }

    @Test
    fun corruptOwnedAudioStopsAfterOneBoundedDecodeFailover() {
        val track = importWav("Corrupt local", 659.25)
        val managed = managedAudio(track)
        managed.writeBytes(ByteArray(track.byteSize.toInt()) { 0x55 })

        ActivityScenario.launch(MainActivity::class.java).use {
            runBlocking { OfflinePlayback.play(context, listOf(track.id)) }
            val (controller, releaseController) = connectController()
            try {
                await("corrupt local audio exhausts its finite queue", 8_000L) {
                    val state = OfflinePlayback.state.value
                    state.owner == "local" && !state.playing && state.errorCode == "local_decode"
                }
                assertTrue(onMain { !controller.isPlaying && controller.playbackState == Player.STATE_IDLE })
                assertEquals(track.id, currentTrackId())
            } finally {
                releaseController()
            }
        }
    }

    @Test
    fun refusingLocalHandoffPreservesTheRegisteredServerOwner() {
        RegistrationFixture().use { fixture ->
            val track = importWav("Refused local handoff", 783.99)
            addedServers += fixture.endpoint
            ActivityScenario.launch(MainActivity::class.java).use {
                try {
                    val device = onMainSuspend {
                        NativePlayback.connect(context, fixture.endpoint, "Boundary phone")
                    }
                    assertEquals(fixture.deviceId, device.getString("id"))
                    await("Server registration owns playback") {
                        OfflinePlayback.state.value.owner == "server"
                    }

                    val failure = try {
                        runBlocking { OfflinePlayback.play(context, listOf(track.id), confirmHandoff = false) }
                        null
                    } catch (error: NativePlaybackException) {
                        error
                    }
                    assertEquals("handoff_required", failure?.code)
                    assertEquals("server", OfflinePlayback.state.value.owner)
                    assertFalse(OfflinePlayback.state.value.playing)
                    assertEquals(
                        fixture.deviceId,
                        onMain { NativePlayback.state(fixture.endpoint).getJSONObject("device").getString("id") },
                    )
                    assertEquals(1, fixture.registrationPosts())
                    assertEquals(0, fixture.registrationDeletes())
                    assertFalse(OfflinePlayback.state.value.queue.entries.any { entry -> entry.trackId == track.id })
                } finally {
                    forceStopService()
                }
            }
        }
    }

    private fun importWav(title: String, frequency: Double): OfflineTrack {
        val bytes = wavBytes(frequency)
        val source = File.createTempFile("offline-playback-", ".wav", context.cacheDir).apply { writeBytes(bytes) }
        val metadata = JSONObject()
            .put("status", "ready")
            .put("title", "$title ${UUID.randomUUID()}")
            .put("artist", "Offline boundary")
            .put("album", "Local engine")
            .put("album_artist", "Offline boundary")
            .put("disc", 1)
            .put("track", 1)
            .put("duration_ms", WAV_SECONDS * 1_000L)
            .put("mime", "audio/wav")
            .put("codec", "pcm_s16le")
            .put("quality", "original")
            .put("byte_size", bytes.size)
            .put("sha256", sha256(bytes))
        val track = library.importTrack(source, metadata)
        createdTrackIds += track.id
        return track
    }

    private fun wavBytes(frequency: Double): ByteArray {
        val sampleCount = WAV_SAMPLE_RATE * WAV_SECONDS
        return ByteBuffer.allocate(44 + sampleCount * 2).order(ByteOrder.LITTLE_ENDIAN).apply {
            put("RIFF".toByteArray())
            putInt(36 + sampleCount * 2)
            put("WAVEfmt ".toByteArray())
            putInt(16)
            putShort(1)
            putShort(1)
            putInt(WAV_SAMPLE_RATE)
            putInt(WAV_SAMPLE_RATE * 2)
            putShort(2)
            putShort(16)
            put("data".toByteArray())
            putInt(sampleCount * 2)
            repeat(sampleCount) { sample ->
                putShort((sin(2.0 * PI * frequency * sample / WAV_SAMPLE_RATE) * 4_096).toInt().toShort())
            }
        }.array()
    }

    private fun managedAudio(track: OfflineTrack): File =
        File(File(context.filesDir, "offline/music"), track.relativePath)

    private fun currentTrackId(): String? {
        val state = OfflinePlayback.state.value
        return state.queue.entries.firstOrNull { it.id == state.queue.currentEntryId }?.trackId
    }

    private fun browse(scenario: ActivityScenario<MainActivity>, fixture: BrowserFixture) {
        addedServers += fixture.endpoint
        scenario.onActivity { activity ->
            activity.findViewById<EditText>(R.id.server_address).setText(fixture.origin)
            activity.findViewById<android.view.View>(R.id.connect_button).performClick()
        }
        assertTrue("The isolated Server profile did not load", fixture.rootRequested.await(10, TimeUnit.SECONDS))
    }

    private fun connectController(): Pair<MediaController, () -> Unit> {
        val token = SessionToken(context, ComponentName(context, NativePlaybackService::class.java))
        val future = onMain {
            MediaController.Builder(context, token)
                .setApplicationLooper(Looper.getMainLooper())
                .buildAsync()
        }
        val controller = future.get(10, TimeUnit.SECONDS)
        return controller to {
            onMain { MediaController.releaseFuture(future) }
        }
    }

    private fun forceStopService() {
        instrumentation.runOnMainSync { OfflinePlayback.stop() }
        context.stopService(Intent(context, NativePlaybackService::class.java))
        await("playback service detaches", 5_000L) {
            onMain { NativePlaybackRegistry.service == null }
        }
    }

    private fun await(description: String, timeoutMillis: Long = 10_000L, condition: () -> Boolean) {
        val deadline = SystemClock.elapsedRealtime() + timeoutMillis
        while (SystemClock.elapsedRealtime() < deadline) {
            if (condition()) return
            SystemClock.sleep(50L)
        }
        throw AssertionError("Timed out waiting for $description; state=${OfflinePlayback.state.value}")
    }

    private fun <T> onMain(block: () -> T): T {
        val result = AtomicReference<Result<T>>()
        instrumentation.runOnMainSync { result.set(runCatching(block)) }
        return requireNotNull(result.get()).getOrThrow()
    }

    private fun <T> onMainSuspend(block: suspend () -> T): T = runBlocking {
        withContext(Dispatchers.Main.immediate) { block() }
    }

    private fun sha256(bytes: ByteArray): String = MessageDigest.getInstance("SHA-256")
        .digest(bytes)
        .joinToString("") { "%02x".format(it.toInt() and 0xff) }

    private data class SeenRequest(val method: String, val path: String)

    private class BrowserFixture : AutoCloseable {
        private val identity = UUID.randomUUID().toString()
        private val requests = ConcurrentLinkedQueue<SeenRequest>()
        val rootRequested = CountDownLatch(1)
        private val server = MockWebServer()
        val origin: String
        val endpoint: ServerEndpoint

        init {
            server.dispatcher = object : Dispatcher() {
                override fun dispatch(request: RecordedRequest): MockResponse {
                    val path = request.path.orEmpty()
                    requests += SeenRequest(request.method.orEmpty(), path)
                    return when (path) {
                        "/api/v1/discovery" -> json(
                            """{"product":"jastreamer","protocol":1,"id":"$identity","name":"Browse fixture","version":"test"}""",
                        )
                        "/" -> {
                            rootRequested.countDown()
                            MockResponse()
                                .setResponseCode(200)
                                .setHeader("Content-Type", "text/html; charset=utf-8")
                                .setBody("<!doctype html><html><body>profile $identity</body></html>")
                        }
                        else -> MockResponse().setResponseCode(404)
                    }
                }
            }
            server.start()
            server.url("/").let { url -> origin = "${url.scheme}://${url.host}:${url.port}" }
            endpoint = ServerEndpoint(identity, "Browse fixture", "test", origin)
        }

        fun serverControlRequests(): List<SeenRequest> = requests.filter { request ->
            request.path.startsWith("/api/v1/player") ||
                request.path.startsWith("/api/v1/browser-output")
        }

        override fun close() = server.shutdown()
    }

    private class RegistrationFixture : AutoCloseable {
        private val identity = UUID.randomUUID().toString()
        val deviceId = "device-${UUID.randomUUID()}"
        private val registrationId = "registration-${UUID.randomUUID()}"
        private val ownerToken = "owner-${UUID.randomUUID()}"
        private val requests = ConcurrentLinkedQueue<SeenRequest>()
        private val server = MockWebServer()
        val endpoint: ServerEndpoint

        init {
            server.dispatcher = object : Dispatcher() {
                override fun dispatch(request: RecordedRequest): MockResponse {
                    val path = request.path.orEmpty()
                    requests += SeenRequest(request.method.orEmpty(), path)
                    return when {
                        request.method == "GET" && path == "/api/v1/discovery" -> json(
                            """{"product":"jastreamer","protocol":1,"id":"$identity","name":"Registration fixture","version":"test"}""",
                        )
                        request.method == "POST" && path == "/api/v1/browser-output/registrations" -> json(
                            JSONObject()
                                .put("registration_id", registrationId)
                                .put("owner_token", ownerToken)
                                .put("lease_duration_ms", 15_000)
                                .put("poll_after_ms", 1_000)
                                .put("device", JSONObject()
                                    .put("id", deviceId)
                                    .put("name", "Boundary phone")
                                    .put("capabilities", JSONObject()
                                        .put("play", true)
                                        .put("pause", true)
                                        .put("stop", true)
                                        .put("seek", true)))
                                .toString(),
                        )
                        request.method == "GET" && path == "/api/v1/browser-output/registrations/$registrationId/commands" ->
                            json("""{"lease_duration_ms":15000,"poll_after_ms":1000}""")
                        request.method == "PUT" && path == "/api/v1/browser-output/registrations/$registrationId/lease" ->
                            json("""{"lease_duration_ms":15000}""")
                        request.method == "DELETE" && path == "/api/v1/browser-output/registrations/$registrationId" ->
                            MockResponse().setResponseCode(204)
                        else -> MockResponse().setResponseCode(404)
                    }
                }
            }
            server.start()
            val url = server.url("/")
            endpoint = ServerEndpoint(
                id = identity,
                name = "Registration fixture",
                version = "test",
                origin = "${url.scheme}://${url.host}:${url.port}",
            )
        }

        fun registrationPosts(): Int = requests.count {
            it.method == "POST" && it.path == "/api/v1/browser-output/registrations"
        }

        fun registrationDeletes(): Int = requests.count {
            it.method == "DELETE" && it.path == "/api/v1/browser-output/registrations/$registrationId"
        }

        override fun close() = server.shutdown()
    }

    companion object {
        private const val WAV_SAMPLE_RATE = 8_000
        private const val WAV_SECONDS = 120

        private fun json(body: String): MockResponse = MockResponse()
            .setResponseCode(200)
            .setHeader("Content-Type", "application/json")
            .setBody(body)
    }
}
