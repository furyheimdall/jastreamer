package io.jastreamer.android

import android.content.ContentValues
import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.os.SystemClock
import android.provider.MediaStore
import android.view.View
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import java.io.File
import java.io.FileOutputStream
import java.nio.ByteBuffer
import java.nio.ByteOrder
import java.util.UUID
import java.security.MessageDigest
import kotlin.math.PI
import kotlin.math.sin
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

/** CI also runs seed/verify in separate processes, with the Server stopped and airplane mode enabled. */
@RunWith(AndroidJUnit4::class)
class OfflineColdStartSmokeTest {
    private val instrumentation = InstrumentationRegistry.getInstrumentation()
    private val context = instrumentation.targetContext
    private val library = OfflineLibrary.get(context)
    private val checkpoint = context.getSharedPreferences("offline-cold-start-smoke", Context.MODE_PRIVATE)

    @Test
    fun libraryAndPausedQueueRestoreWithoutAnAccount() {
        when (val phase = InstrumentationRegistry.getArguments().getString("offlinePhase", "roundtrip")) {
            "seed" -> seed()
            "verify" -> verify()
            "roundtrip" -> { seed(); verify() }
            else -> error("Unknown offlinePhase: $phase")
        }
    }

    private fun seed() {
        instrumentation.runOnMainSync {
            if (OfflinePlayback.state.value.owner == OfflinePlaybackPolicy.OWNER_LOCAL) {
                NativePlaybackRegistry.service?.prepareServerHandoff(
                    confirmHandoff = true,
                    requestGeneration = OfflinePlaybackRequestFence.beginRequest(),
                )
            }
        }
        context.stopService(Intent(context, NativePlaybackService::class.java))
        instrumentation.waitForIdleSync()
        await("previous playback ownership releases before cold-start seeding") {
            OfflinePlayback.state.value.owner == OfflinePlaybackPolicy.OWNER_NONE &&
                !OfflinePlayback.state.value.playing
        }
        val audio = File.createTempFile("offline-cold-", ".wav", context.cacheDir)
        val sampleRate = 44100
        val seconds = 12
        val length = sampleRate * seconds * 2
        FileOutputStream(audio).use { stream ->
            val header = ByteBuffer.allocate(44).order(ByteOrder.LITTLE_ENDIAN)
                .put("RIFF".toByteArray()).putInt(36 + length).put("WAVEfmt ".toByteArray())
                .putInt(16).putShort(1).putShort(1).putInt(sampleRate).putInt(sampleRate * 2)
                .putShort(2).putShort(16).put("data".toByteArray()).putInt(length).array()
            stream.write(header)
            val second = ByteBuffer.allocate(sampleRate * 2).order(ByteOrder.LITTLE_ENDIAN)
            repeat(sampleRate) { second.putShort((sin(2 * PI * 523.25 * it / sampleRate) * 2048).toInt().toShort()) }
            repeat(seconds) { stream.write(second.array()) }
        }
        val checksum = sha256(audio)
        val metadata = JSONObject().put("status", "ready").put("title", "Cold process local audio").put("artist", "Offline smoke")
            .put("album", "Local snapshot").put("album_artist", "Offline smoke").put("disc", 1).put("track", 1)
            .put("duration_ms", seconds * 1000).put("mime", "audio/wav").put("codec", "pcm_s16le")
            .put("quality", "original").put("byte_size", audio.length()).put("sha256", checksum)
        val track = library.importTrack(audio, metadata)
        val playlist = library.savePlaylist(null, "Cold process duplicate snapshot", listOf(track.id, track.id))
        val entries = listOf(OfflineQueueEntry(UUID.randomUUID().toString(), track.id), OfflineQueueEntry(UUID.randomUUID().toString(), track.id))
        library.saveQueue(OfflineQueue(entries, entries[1].id, 4500, false, 0))
        check(checkpoint.edit().putString("track", track.id).putString("sha256", checksum)
            .putString("playlist", playlist.id).putString("entry", entries[1].id).commit())
        RecentServerStore(context).list().forEach { RecentServerStore(context).remove(it) }
        assertEquals(listOf(track.id, track.id), library.loadQueue().entries.map { it.trackId })
        assertEquals(4500, library.loadQueue().positionMs)
        assertFalse(OfflinePlayback.state.value.playing)
    }

    private fun verify() {
        val id = requireNotNull(checkpoint.getString("track", null)) { "Cold-start seed must exist" }
        val checksum = requireNotNull(checkpoint.getString("sha256", null))
        val playlistId = requireNotNull(checkpoint.getString("playlist", null))
        try {
            val queue = library.loadQueue()
            assertEquals(listOf(id, id), queue.entries.map { it.trackId })
            assertEquals(checkpoint.getString("entry", null), queue.currentEntryId)
            assertEquals(4500, queue.positionMs)
            assertEquals(listOf(id, id), library.playlist(playlistId)?.trackIds)
            library.openAudio(id).use { handle ->
                val digest = MessageDigest.getInstance("SHA-256")
                val bytes = ByteArray(64 * 1024)
                while (true) {
                    val count = handle.input.read(bytes)
                    if (count < 0) break
                    digest.update(bytes, 0, count)
                }
                assertEquals(checksum, digest.digest().joinToString("") { "%02x".format(it.toInt() and 0xff) })
            }
            ActivityScenario.launch(MainActivity::class.java).use { scenario ->
                await("cold launcher") {
                    var visible = false
                    scenario.onActivity { visible = it.findViewById<View>(R.id.saved_music_button)?.isShown == true }
                    visible
                }
                assertFalse("Cold launch must never autoplay", OfflinePlayback.state.value.playing)
                scenario.onActivity { it.findViewById<View>(R.id.saved_music_button).performClick() }
                runBlocking { OfflinePlayback.restore(context) }
                assertFalse(OfflinePlayback.state.value.playing)
                assertEquals(4500, OfflinePlayback.state.value.queue.positionMs)
                val coldProcess = InstrumentationRegistry.getArguments().getString("offlinePhase") == "verify"
                if (coldProcess) assertTrue("Cold queue editing starts without a playback service", NativePlaybackRegistry.service == null)
                runBlocking { OfflinePlayback.enqueue(context, listOf(id), next = false) }
                val appended = library.loadQueue()
                assertEquals(listOf(id, id, id), appended.entries.map { it.trackId })
                assertEquals(queue.entries, appended.entries.take(queue.entries.size))
                assertEquals(queue.currentEntryId, appended.currentEntryId)
                assertEquals(4500, appended.positionMs)
                assertEquals(appended, OfflinePlayback.state.value.queue)
                assertFalse("Adding to a cold saved queue must not autoplay", OfflinePlayback.state.value.playing)
                if (coldProcess) assertTrue("Queue editing must not start a playback service", NativePlaybackRegistry.service == null)
                runBlocking {
                    withContext(Dispatchers.Main) { OfflinePlayback.resume() }
                }
                await("local playback without Server or account") { OfflinePlayback.state.value.playing }
                assertEquals("local", OfflinePlayback.state.value.owner)
                assertTrue("Resume must retain the persisted seek position", OfflinePlayback.state.value.queue.positionMs >= 4500)
                screenshot("offline-cold-process-airplane-playback")
                instrumentation.runOnMainSync { OfflinePlayback.stop() }
            }
        } finally {
            instrumentation.runOnMainSync { OfflinePlayback.stop() }
            context.stopService(Intent(context, NativePlaybackService::class.java))
            library.deletePlaylist(playlistId)
            library.deleteTracks(listOf(id))
            checkpoint.edit().clear().commit()
        }
    }

    private fun sha256(file: File): String {
        val digest = MessageDigest.getInstance("SHA-256")
        file.inputStream().use { input ->
            val buffer = ByteArray(64 * 1024)
            while (true) {
                val count = input.read(buffer)
                if (count < 0) break
                digest.update(buffer, 0, count)
            }
        }
        return digest.digest().joinToString("") { "%02x".format(it.toInt() and 0xff) }
    }

    private fun screenshot(name: String) {
        instrumentation.waitForIdleSync()
        val image = requireNotNull(instrumentation.uiAutomation.takeScreenshot())
        val resolver = context.contentResolver
        val values = ContentValues().apply {
            put(MediaStore.Images.Media.DISPLAY_NAME, "$name.png")
            put(MediaStore.Images.Media.MIME_TYPE, "image/png")
            put(MediaStore.Images.Media.RELATIVE_PATH, "Pictures/jastreamer-android-smoke")
            put(MediaStore.Images.Media.IS_PENDING, 1)
        }
        val uri = requireNotNull(resolver.insert(MediaStore.Images.Media.EXTERNAL_CONTENT_URI, values))
        requireNotNull(resolver.openOutputStream(uri)).use { check(image.compress(Bitmap.CompressFormat.PNG, 100, it)) }
        values.clear()
        values.put(MediaStore.Images.Media.IS_PENDING, 0)
        check(resolver.update(uri, values, null, null) == 1)
        image.recycle()
    }

    private fun await(description: String, condition: () -> Boolean) {
        val deadline = SystemClock.elapsedRealtime() + 30_000
        while (SystemClock.elapsedRealtime() < deadline) {
            if (condition()) return
            SystemClock.sleep(100)
        }
        throw AssertionError("Timed out waiting for $description: ${OfflinePlayback.state.value}")
    }
}
