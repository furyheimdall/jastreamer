package io.jastreamer.android

import android.content.ComponentName
import android.content.Intent
import android.os.Handler
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
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicReference
import kotlin.math.PI
import kotlin.math.sin
import kotlin.coroutines.CoroutineContext
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.async
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
import org.junit.Assert.assertNull
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
        releasePlayback()
        originalQueue = library.loadQueue()
        runBlocking { OfflinePlayback.restore(context) }
    }

    @After
    fun tearDown() {
        releasePlayback()
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
    fun inactiveEnqueuePersistsAfterDeletionWithoutAutoplay() {
        val track = importWav("Inactive enqueue", 369.99)
        val removed = importWav("Deleted inactive queue", 311.13)
        library.saveQueue(OfflinePlaybackPolicy.newQueue(listOf(removed.id), 0))
        runBlocking { OfflinePlayback.restore(context) }
        assertTrue(library.deleteTracks(listOf(removed.id)).failures.isEmpty())
        createdTrackIds.remove(removed.id)

        runBlocking { OfflinePlayback.enqueue(context, listOf(track.id, track.id), next = false) }

        val queued = OfflinePlayback.state.value.queue.entries
        assertEquals(listOf(track.id, track.id), queued.map { entry -> entry.trackId })
        assertEquals(queued, library.loadQueue().entries)
        assertEquals(OfflinePlaybackPolicy.OWNER_NONE, OfflinePlayback.state.value.owner)
        assertFalse(OfflinePlayback.state.value.playing)
    }

    @Test
    fun automaticServerConnectAndDisconnectLeaveSavedMusicOwnershipUntouched() {
        RegistrationFixture().use { fixture ->
            val track = importWav("Automatic connect local owner", 415.3)
            addedServers += fixture.endpoint
            ActivityScenario.launch(MainActivity::class.java).use {
                runBlocking { OfflinePlayback.play(context, listOf(track.id)) }
                await("saved music owns active playback") {
                    OfflinePlayback.state.value.owner == OfflinePlaybackPolicy.OWNER_LOCAL &&
                        OfflinePlayback.state.value.playing
                }
                val queue = OfflinePlayback.state.value.queue
                val (controller, releaseController) = connectController()
                try {
                    val originalVolume = onMain { controller.volume }
                    val volumeFailure = try {
                        onMain { NativePlayback.setVolume(fixture.endpoint, 0.25) }
                        null
                    } catch (failure: NativePlaybackException) {
                        failure
                    }
                    assertEquals("not_connected", volumeFailure?.code)
                    assertEquals(originalVolume, onMain { controller.volume }, 0f)

                    val pendingUserRequest = OfflinePlaybackRequestFence.beginRequest()
                    val device = onMainSuspend {
                        NativePlayback.connectIfAvailable(context, fixture.endpoint, "Boundary phone")
                    }
                    assertNull(device)
                    assertTrue("Automatic fallback must not supersede an explicit playback request",
                        OfflinePlaybackRequestFence.isCurrent(pendingUserRequest))
                    onMainSuspend { NativePlayback.disconnect(fixture.endpoint) }

                    assertEquals(OfflinePlaybackPolicy.OWNER_LOCAL, OfflinePlayback.state.value.owner)
                    assertTrue(OfflinePlayback.state.value.playing)
                    assertEquals(queue.entries, OfflinePlayback.state.value.queue.entries)
                    assertEquals(queue.currentEntryId, OfflinePlayback.state.value.queue.currentEntryId)
                    assertTrue(OfflinePlayback.state.value.queue.positionMs >= queue.positionMs)
                    assertEquals(0, fixture.registrationPosts())
                    assertEquals(0, fixture.registrationDeletes())
                } finally {
                    releaseController()
                }
            }
        }
    }

    @Test
    fun serverVolumeAutomaticConnectAndDisconnectStayWithTheRegisteredServer() {
        RegistrationFixture().use { first ->
            RegistrationFixture().use { second ->
                addedServers += first.endpoint
                addedServers += second.endpoint
                ActivityScenario.launch(MainActivity::class.java).use {
                    val connected = onMainSuspend {
                        NativePlayback.connect(context, first.endpoint, "Boundary phone")
                    }
                    assertEquals(first.deviceId, connected.getString("id"))
                    val (controller, releaseController) = connectController()
                    try {
                        val state = onMain {
                            NativePlayback.setVolume(first.endpoint, 0.25)
                        }
                        assertEquals(0.25, state.getDouble("volume"), 0.0001)
                        assertEquals(0.25f, onMain { controller.volume }, 0.0001f)
                        assertFalse(onMain { controller.isPlaying })

                        val existing = onMainSuspend {
                            NativePlayback.connectIfAvailable(context, first.endpoint, "Boundary phone")
                        }
                        assertEquals(first.deviceId, existing?.getString("id"))
                        assertEquals(1, first.registrationPosts())

                        val other = onMainSuspend {
                            NativePlayback.connectIfAvailable(context, second.endpoint, "Other boundary phone")
                        }
                        assertNull(other)
                        assertEquals(0, second.registrationPosts())
                        assertEquals(
                            first.deviceId,
                            onMain { NativePlayback.state(first.endpoint).getJSONObject("device").getString("id") },
                        )

                        onMainSuspend { NativePlayback.disconnect(second.endpoint) }
                        assertEquals(OfflinePlaybackPolicy.OWNER_SERVER, OfflinePlayback.state.value.owner)
                        assertEquals(0, first.registrationDeletes())

                        onMainSuspend { NativePlayback.disconnect(first.endpoint) }
                        assertEquals(OfflinePlaybackPolicy.OWNER_NONE, OfflinePlayback.state.value.owner)
                        assertEquals(1, first.registrationDeletes())
                    } finally {
                        releaseController()
                    }
                }
            }
        }
    }

    @Test
    fun enqueueUpdatesTheLiveMedia3QueueAndPlayEntryKeepsOccurrenceIdentity() {
        val first = importWav("Enqueue current", 392.0)
        val duplicate = importWav("Enqueue duplicate", 493.88)
        val tail = importWav("Enqueue tail", 587.33)

        ActivityScenario.launch(MainActivity::class.java).use {
            runBlocking { OfflinePlayback.play(context, listOf(first.id, tail.id)) }
            val (controller, releaseController) = connectController()
            try {
                onMain { controller.seekTo(5_000L) }
                await("local playback passes its seek landmark before live insertion") {
                    OfflinePlayback.state.value.playing && onMain { controller.currentPosition >= 5_500L }
                }
                val currentEntryId = OfflinePlayback.state.value.queue.currentEntryId

                runBlocking {
                    OfflinePlayback.enqueue(context, listOf(duplicate.id, duplicate.id), next = true)
                }
                await("duplicate next entries reach Media3 without replacing current playback") {
                    val state = OfflinePlayback.state.value
                    state.playing &&
                        state.queue.currentEntryId == currentEntryId &&
                        state.queue.entries.map { entry -> entry.trackId } ==
                        listOf(first.id, duplicate.id, duplicate.id, tail.id) &&
                        onMain {
                            controller.mediaItemCount == 4 &&
                                controller.currentMediaItem?.mediaId == currentEntryId
                        }
                }
                val inserted = OfflinePlayback.state.value.queue
                assertEquals(inserted.entries.size, inserted.entries.map { entry -> entry.id }.toSet().size)
                assertTrue("Live insertion must retain the seek landmark", onMain { controller.currentPosition >= 5_000L })
                val selectedEntryId = inserted.entries[2].id
                val stableIds = inserted.entries.map { entry -> entry.id }

                runBlocking { OfflinePlayback.playEntry(context, selectedEntryId) }
                await("selected duplicate occurrence plays by its stable queue ID") {
                    val state = OfflinePlayback.state.value
                    state.playing && state.queue.currentEntryId == selectedEntryId &&
                        onMain { controller.currentMediaItem?.mediaId == selectedEntryId }
                }
                assertEquals(stableIds, OfflinePlayback.state.value.queue.entries.map { entry -> entry.id })
                onMain {
                    controller.pause()
                    controller.seekTo(2_000L)
                }
                await("paused playback settles at an exact seek position") {
                    onMain { !controller.isPlaying && controller.currentPosition == 2_000L }
                }
                runBlocking { OfflinePlayback.enqueue(context, listOf(tail.id), next = false) }
                assertFalse(onMain { controller.isPlaying })
                assertEquals(2_000L, onMain { controller.currentPosition })
                assertEquals(stableIds, OfflinePlayback.state.value.queue.entries.take(stableIds.size).map { it.id })
            } finally {
                releaseController()
            }
        }
    }

    @Test
    fun serverOwnedEnqueuePersistsWithoutAutoplayAndPlayEntryRequiresHandoff() {
        RegistrationFixture().use { fixture ->
            val track = importWav("Server-owned enqueue", 698.46)
            val removed = importWav("Deleted Server-owned queue", 622.25)
            library.saveQueue(OfflinePlaybackPolicy.newQueue(listOf(removed.id), 0))
            runBlocking { OfflinePlayback.restore(context) }
            addedServers += fixture.endpoint
            ActivityScenario.launch(MainActivity::class.java).use {
                try {
                    onMainSuspend { NativePlayback.connect(context, fixture.endpoint, "Boundary phone") }
                    await("Server registration owns playback") {
                        OfflinePlayback.state.value.owner == OfflinePlaybackPolicy.OWNER_SERVER
                    }
                    assertTrue(library.deleteTracks(listOf(removed.id)).failures.isEmpty())
                    createdTrackIds.remove(removed.id)

                    runBlocking {
                        OfflinePlayback.enqueue(context, listOf(track.id, track.id), next = false)
                    }
                    val queued = OfflinePlayback.state.value.queue.entries
                    assertEquals(listOf(track.id, track.id), queued.map { entry -> entry.trackId })
                    assertEquals(2, queued.map { entry -> entry.id }.toSet().size)
                    assertEquals(queued, library.loadQueue().entries)
                    assertEquals(OfflinePlaybackPolicy.OWNER_SERVER, OfflinePlayback.state.value.owner)
                    assertFalse(OfflinePlayback.state.value.playing)
                    assertEquals(0, fixture.registrationDeletes())

                    val failure = try {
                        runBlocking { OfflinePlayback.playEntry(context, queued[1].id) }
                        null
                    } catch (error: NativePlaybackException) {
                        error
                    }
                    assertEquals("handoff_required", failure?.code)
                    assertEquals(OfflinePlaybackPolicy.OWNER_SERVER, OfflinePlayback.state.value.owner)
                    assertFalse(OfflinePlayback.state.value.playing)
                    assertEquals(0, fixture.registrationDeletes())
                    runBlocking {
                        OfflinePlayback.playEntry(context, queued[1].id, confirmHandoff = true, startPositionMs = 1_000L)
                    }
                    await("confirmed handoff resumes the selected saved occurrence at its offset") {
                        val state = OfflinePlayback.state.value
                        state.owner == OfflinePlaybackPolicy.OWNER_LOCAL && state.playing &&
                            state.queue.currentEntryId == queued[1].id && state.queue.positionMs >= 1_000L
                    }
                    assertEquals(queued, OfflinePlayback.state.value.queue.entries)
                    assertEquals(1, fixture.registrationDeletes())
                } finally {
                    fixture.removeRegistration()
                    releasePlayback()
                }
            }
        }
    }

    @Test
    fun enqueueDoesNotCancelPendingNextPlayback() {
        val first = importWav("Pending next current", 261.63)
        val next = importWav("Pending next target", 329.63)
        val appended = importWav("Pending next append", 392.0)
        ActivityScenario.launch(MainActivity::class.java).use {
            runBlocking { OfflinePlayback.play(context, listOf(first.id, next.id)) }
            val (controller, releaseController) = connectController()
            val service = onMain { requireNotNull(NativePlaybackRegistry.service) }
            val scopeField = NativePlaybackService::class.java.getDeclaredField("scope").apply { isAccessible = true }
            val originalScope = onMain { scopeField.get(service) as CoroutineScope }
            val gate = PausedMainDispatcher()
            try {
                await("first track starts before pending Next") { OfflinePlayback.state.value.playing }
                val nextEntry = OfflinePlayback.state.value.queue.entries[1].id
                onMain {
                    scopeField.set(service, CoroutineScope(originalScope.coroutineContext + gate))
                    OfflinePlayback.next()
                }
                runBlocking { OfflinePlayback.enqueue(context, listOf(appended.id), next = false) }
                onMain {
                    gate.release()
                    scopeField.set(service, originalScope)
                }
                await("Next still selects its existing occurrence after append") {
                    OfflinePlayback.state.value.playing &&
                        onMain { controller.currentMediaItem?.mediaId == nextEntry }
                }
                assertEquals(
                    listOf(first.id, next.id, appended.id),
                    OfflinePlayback.state.value.queue.entries.map { entry -> entry.trackId },
                )
            } finally {
                onMain {
                    gate.release()
                    scopeField.set(service, originalScope)
                }
                releaseController()
            }
        }
    }

    @Test
    fun enqueueSurvivesAnAlreadyConfirmedPendingPlaybackHandoff() {
        val deleteRequested = CountDownLatch(1)
        val allowDelete = CountDownLatch(1)
        RegistrationFixture(beforeDelete = {
            deleteRequested.countDown()
            check(allowDelete.await(10, TimeUnit.SECONDS)) { "Handoff fixture was not released" }
        }).use { fixture ->
            addedServers += fixture.endpoint
            val first = importWav("Pending handoff current", 440.0)
            val appended = importWav("Pending handoff append", 554.37)
            ActivityScenario.launch(MainActivity::class.java).use {
                onMainSuspend { NativePlayback.connect(context, fixture.endpoint, "Boundary phone") }
                try {
                    runBlocking {
                        val play = async(Dispatchers.Main.immediate) {
                            OfflinePlayback.play(context, listOf(first.id), confirmHandoff = true)
                        }
                        try {
                            withContext(Dispatchers.IO) {
                                assertTrue("Confirmed handoff reaches Server", deleteRequested.await(5, TimeUnit.SECONDS))
                            }
                            val append = async(Dispatchers.Main.immediate, start = CoroutineStart.UNDISPATCHED) {
                                OfflinePlayback.enqueue(context, listOf(appended.id), next = false)
                            }
                            allowDelete.countDown()
                            play.await()
                            append.await()
                        } finally {
                            allowDelete.countDown()
                        }
                    }
                    await("confirmed playback and additive request both survive") {
                        val state = OfflinePlayback.state.value
                        state.playing && state.queue.entries.map { entry -> entry.trackId } == listOf(first.id, appended.id)
                    }
                    val queue = OfflinePlayback.state.value.queue
                    assertEquals(first.id, queue.entries.first { it.id == queue.currentEntryId }.trackId)
                    assertEquals(queue.entries, library.loadQueue().entries)
                    assertEquals(1, fixture.registrationDeletes())
                } finally {
                    allowDelete.countDown()
                    fixture.removeRegistration()
                    releasePlayback()
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
                    state.owner == "local" && !state.playing && state.errorCode == "local_playback" &&
                        onMain { !controller.isPlaying && controller.playbackState == Player.STATE_IDLE }
                }
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
                    // MediaController receives session state asynchronously; observe its
                    // stopped state within the same bound, not just the in-process facade.
                    state.owner == "local" && !state.playing && state.errorCode == "local_decode" &&
                        onMain { !controller.isPlaying && controller.playbackState == Player.STATE_IDLE }
                }
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
                    fixture.removeRegistration()
                    await("fixture registration releases Server ownership", 5_000L) {
                        OfflinePlayback.state.value.owner == "none"
                    }
                    releasePlayback()
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
            activity.findViewById<android.view.View>(R.id.server_playback_button)?.performClick()
            activity.findViewById<android.view.View>(R.id.manual_address_button)?.performClick()
            requireNotNull(activity.findViewById<EditText>(R.id.server_address)).setText(fixture.origin)
            requireNotNull(activity.findViewById<android.view.View>(R.id.connect_button)).performClick()
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

    private fun releasePlayback() {
        instrumentation.runOnMainSync {
            if (OfflinePlayback.state.value.owner == "local") {
                val requestGeneration = OfflinePlaybackRequestFence.beginRequest()
                NativePlaybackRegistry.service?.prepareServerHandoff(
                    confirmHandoff = true,
                    requestGeneration = requestGeneration,
                )
            }
        }
        context.stopService(Intent(context, NativePlaybackService::class.java))
        await("playback ownership releases", 5_000L) {
            val state = OfflinePlayback.state.value
            state.owner == "none" && !state.playing
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

    private class PausedMainDispatcher : CoroutineDispatcher() {
        private val pending = ArrayDeque<Runnable>()
        private val handler = Handler(Looper.getMainLooper())
        private var paused = true

        override fun dispatch(context: CoroutineContext, block: Runnable) {
            synchronized(pending) {
                if (paused) pending.addLast(block) else handler.post(block)
            }
        }

        fun release() {
            val ready = synchronized(pending) {
                paused = false
                pending.toList().also { pending.clear() }
            }
            ready.forEach(Runnable::run)
        }
    }

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

    private class RegistrationFixture(private val beforeDelete: (() -> Unit)? = null) : AutoCloseable {
        private val identity = UUID.randomUUID().toString()
        val deviceId = "device-${UUID.randomUUID()}"
        private val registrationId = "registration-${UUID.randomUUID()}"
        private val ownerToken = "owner-${UUID.randomUUID()}"
        private val removed = AtomicBoolean()
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
                            if (removed.get()) {
                                MockResponse().setResponseCode(404)
                            } else {
                                json("""{"lease_duration_ms":15000,"poll_after_ms":1000}""")
                            }
                        request.method == "PUT" && path == "/api/v1/browser-output/registrations/$registrationId/lease" ->
                            if (removed.get()) {
                                MockResponse().setResponseCode(404)
                            } else {
                                json("""{"lease_duration_ms":15000}""")
                            }
                        request.method == "DELETE" && path == "/api/v1/browser-output/registrations/$registrationId" -> {
                            beforeDelete?.invoke()
                            MockResponse().setResponseCode(204)
                        }
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

        fun removeRegistration() {
            removed.set(true)
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
