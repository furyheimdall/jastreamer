package io.jastreamer.android

import android.accessibilityservice.AccessibilityServiceInfo
import android.content.Intent
import android.os.SystemClock
import android.os.Bundle
import android.view.View
import android.view.ViewGroup
import androidx.webkit.ProfileStore
import android.widget.Button
import android.widget.SeekBar
import android.view.accessibility.AccessibilityNodeInfo
import androidx.test.core.app.ActivityScenario
import androidx.test.platform.app.InstrumentationRegistry
import java.io.File
import java.security.MessageDigest
import java.util.concurrent.TimeUnit
import java.util.concurrent.CountDownLatch
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue

/** Exercises the real Server, trusted Web bridge, native import and account-independent player. */
internal class OfflineImportSmoke(
    private val scenario: ActivityScenario<MainActivity>,
    private val evaluate: (String) -> String?,
    private val screenshot: (String) -> Unit,
) {
    private val instrumentation = InstrumentationRegistry.getInstrumentation()
    private val context = instrumentation.targetContext
    private val library = OfflineLibrary.get(context)

    fun run() {
        val initialIds = library.tracks().map { it.id }.toSet()
        val folders = mutableListOf<String>()
        val playlistIds = mutableListOf<String>()
        try {
            val capabilities = request("/api/v1/downloads/capabilities")
            assertEquals(1, capabilities.getInt("version"))
            val qualities = capabilities.getJSONArray("qualities")
            assertTrue("Real fixture must exercise original downloads", (0 until qualities.length()).any { qualities.getString(it) == "original" })
            assertTrue("Real fixture must exercise AAC conversion", (0 until qualities.length()).any { qualities.getString(it) == "aac_256" })
            val items = request("/api/v1/library/tracks?limit=100").getJSONArray("items")
            val source = (0 until items.length()).map { items.getJSONObject(it) }
                .single { it.getString("path").endsWith("artwork/background.wav") }
            val shortSource = (0 until items.length()).map { items.getJSONObject(it) }
                .single { it.getString("path") == "short.wav" }
            val queueBefore = request("/api/v1/queue").toString()
            val playerBefore = request("/api/v1/player")
            evaluate("document.querySelectorAll('.mobile-nav button')[0].click()")
            await("all-track library selector") { evaluate("!!document.querySelector('button[data-library-kind=tracks]')") == "true" }
            evaluate("document.querySelector('button[data-library-kind=tracks]').click()")
            val original = download(source, "original")
            val compact = download(source, "aac_256")
            val shortAAC = download(shortSource, "aac_256")
            assertNotEquals("Different qualities remain distinct local artifacts", original.id, compact.id)
            assertEquals(source.getLong("size"), original.byteSize)
            assertEquals("audio/mp4", compact.mime)
            assertEquals("aac", compact.codec)
            assertEquals(original.sha256, digest(original.id))
            assertEquals(compact.sha256, digest(compact.id))
            val artwork = File(requireNotNull(original.artworkPath) { "Source artwork was not saved locally" })
            val artworkBytes = artwork.readBytes()
            assertEquals("Imports must not mutate the shared queue", queueBefore, request("/api/v1/queue").toString())
            val playerAfter = request("/api/v1/player")
            for (field in listOf("state", "renderer_id", "position_ms")) {
                assertEquals("Import changed Server player $field", playerBefore.opt(field), playerAfter.opt(field))
            }
            screenshot("offline-import-complete")

            // Invoke the same narrow logout notification as the account button before revoking the session.
            bridge("logout", JSONObject())
            request("/api/v1/logout", "POST", JSONObject(), expectedStatus = 204)
            val endpoint = runBlocking {
                ServerProbe().probe(requireNotNull(evaluate("location.origin")))
            }
            val cookiesCleared = CountDownLatch(1)
            main {
                ProfileStore.getInstance().getOrCreateProfile(EndpointPolicy.profileName(endpoint))
                    .cookieManager.removeAllCookies { cookiesCleared.countDown() }
            }
            assertTrue("Web cookies clear independently", cookiesCleared.await(10, TimeUnit.SECONDS))
            val idsBeforeLogout = library.tracks().map { it.id }.toSet()
            RecentServerStore(context).list().forEach { RecentServerStore(context).remove(it) }
            assertEquals("Server logout and recent-profile removal cannot delete music", idsBeforeLogout, library.tracks().map { it.id }.toSet())
            assertTrue("Logout and Web cookie cleanup preserve local artwork", artworkBytes.contentEquals(artwork.readBytes()))
            scenario.onActivity { it.findViewById<View>(R.id.change_server_button).performClick() }
            await("independent music launcher entry") {
                var visible = false
                scenario.onActivity { visible = it.findViewById<View>(R.id.saved_music_button)?.isShown == true }
                visible
            }
            scenario.onActivity { it.findViewById<View>(R.id.saved_music_button).performClick() }
            screenshot("offline-library-after-logout")

            val playlist = library.savePlaylist(null, "Offline smoke duplicate snapshot", listOf(shortAAC.id, original.id, compact.id, original.id))
            playlistIds.add(playlist.id)
            val destination = library.createFolder(OfflineLibrary.ROOT_FOLDER_ID, "Offline smoke moved")
            folders.add(destination.id)
            runBlocking {
                withContext(Dispatchers.Main) {
                    OfflinePlayback.play(context, playlist.trackIds, confirmHandoff = true)
                }
            }
            await("complete short AAC begins after logout") {
                val state = OfflinePlayback.state.value
                check(state.errorCode == null) { "Short AAC playback failed: ${state.errorCode}: ${state.errorMessage}" }
                state.queue.entries.find { it.id == state.queue.currentEntryId }?.trackId == shortAAC.id &&
                    state.playing && state.queue.positionMs > 0
            }
            await("local original audio advances after logout") {
                val state = OfflinePlayback.state.value
                check(state.errorCode == null) { "Local original playback failed: ${state.errorCode}: ${state.errorMessage}" }
                state.owner == "local" && state.playing && state.queue.positionMs > 0 &&
                    state.queue.entries.find { it.id == state.queue.currentEntryId }?.trackId == original.id
            }
            val localQueue = OfflinePlayback.state.value.queue.entries.map { it.trackId }
            assertEquals(playlist.trackIds, localQueue)
            library.moveTrack(original.id, destination.id)
            assertEquals("Folder move preserves the local identity", original.id, library.track(original.id)?.id)
            assertEquals(destination.id, library.track(original.id)?.folderId)
            assertEquals("Folder move preserves audio bytes", original.sha256, digest(original.id))
            assertEquals("Folder move preserves playlist duplicates", playlist.trackIds, library.playlist(playlist.id)?.trackIds)
            assertEquals("Folder move preserves the playing queue", localQueue, OfflinePlayback.state.value.queue.entries.map { it.trackId })
            screenshot("offline-playing-after-folder-move")

            main { OfflinePlayback.next() }
            await("converted AAC plays on the local engine") {
                val state = OfflinePlayback.state.value
                check(state.errorCode == null) { "Local AAC playback failed: ${state.errorCode}: ${state.errorMessage}" }
                val current = state.queue.entries.find { it.id == state.queue.currentEntryId }
                current?.trackId == compact.id && state.playing && state.queue.positionMs > 0
            }
            scenario.onActivity { activity ->
                (activity.findViewById<View>(R.id.offline_player_title).parent as View).performClick()
            }
            await("bundled full-player seeking") {
                var visible = false
                scenario.onActivity { visible = it.findViewById<View>(R.id.offline_player_seek)?.isShown == true }
                visible
            }
            scenario.onActivity { activity ->
                val seek = activity.findViewById<SeekBar>(R.id.offline_player_seek)
                assertTrue("Accessibility seeking is accepted", seek.performAccessibilityAction(
                    AccessibilityNodeInfo.AccessibilityAction.ACTION_SET_PROGRESS.id,
                    Bundle().apply { putFloat(AccessibilityNodeInfo.ACTION_ARGUMENT_PROGRESS_VALUE, 45_000f) },
                ))
            }
            await("accessibility seek reaches the AAC engine") {
                OfflinePlayback.state.value.queue.positionMs in 44_000L..55_000L
            }
            main { OfflinePlayback.pause() }
            await("local pause") { !OfflinePlayback.state.value.playing }
            await("full-player action updates after pause") {
                var updated = false
                scenario.onActivity { activity ->
                    val configuration = android.content.res.Configuration(activity.resources.configuration).apply {
                        setLocale(java.util.Locale.forLanguageTag(RecentServerStore(context).language()))
                    }
                    val resume = activity.createConfigurationContext(configuration).getString(R.string.offline_resume)
                    val body = activity.findViewById<SeekBar>(R.id.offline_player_seek).parent as ViewGroup
                    val matches = ArrayList<View>()
                    body.findViewsWithText(matches, resume, View.FIND_VIEWS_WITH_TEXT)
                    updated = matches.any { it is Button && it.text.toString() == resume && it.isShown }
                }
                updated
            }
            val deletion = library.deleteTracks(listOf(compact.id))
            assertTrue("Current-track ownership must defer deletion even when AAC is buffered", compact.id in deletion.deferredIds)
            assertTrue(library.track(compact.id)?.pendingDelete == true)
            screenshot("offline-current-delete-deferred")
            main { OfflinePlayback.stop() }
            await("deferred current-track file deletion") { library.track(compact.id) == null }
            assertEquals(listOf(shortAAC.id, original.id, original.id), library.playlist(playlist.id)?.trackIds)

            scenario.recreate()
            await("offline UI remains accessible after recreation") {
                var accessible = false
                scenario.onActivity {
                    accessible = it.findViewById<View>(R.id.offline_library_button)?.isShown == true ||
                        it.findViewById<View>(R.id.saved_music_button)?.isShown == true
                }
                accessible
            }
            assertFalse("Recreation must not autoplay", OfflinePlayback.state.value.playing)
            assertEquals("Logout cannot lock the original on relaunch", original.sha256, digest(original.id))
            screenshot("offline-restored-without-server-login")
            library.deletePlaylist(playlist.id)
        } finally {
            main { OfflinePlayback.stop() }
            context.stopService(Intent(context, NativePlaybackService::class.java))
            playlistIds.forEach { id -> if (library.playlist(id) != null) library.deletePlaylist(id) }
            library.tracks().filter { it.id !in initialIds }.forEach { library.deleteTracks(listOf(it.id)) }
            folders.asReversed().forEach { runCatching { library.deleteFolder(it) } }
        }
    }

    private fun download(source: JSONObject, quality: String): OfflineTrack {
        val selector = "button[data-download-kind=track][data-download-id='${source.getString("id")}'][aria-disabled=false]:not(:disabled):not([data-download-confirm])"
        await("track download control") { evaluate("!!document.querySelector(${JSONObject.quote(selector)})") == "true" }
        evaluate("document.querySelector(${JSONObject.quote(selector)}).click()")
        await("download quality confirmation") { evaluate("!!document.querySelector('button[data-download-confirm]')") == "true" }
        evaluate("document.querySelector('input[value=$quality]').click()")
        watchBridge()
        evaluate("document.querySelector('button[data-download-confirm]').click()")
        confirmNativeDialog()
        await("native download accepted") {
            val raw = evaluate("window.offlineSmokeAccepted")
            if (raw.isNullOrBlank() || raw == "null") false else {
                val value = JSONObject(raw)
                check(!value.has("error")) { "Native import rejected: ${value.optJSONObject("error")}" }
                value.optJSONObject("result")?.has("job_id") == true
            }
        }
        val jobId = JSONObject(requireNotNull(evaluate("window.offlineSmokeAccepted"))).getJSONObject("result").getString("job_id")
        await("verified $quality import", 90_000) {
            val job = OfflineDownloads.jobs.value.find { it.id == jobId }
            check(job?.errorCode == null) { "Import failed: ${job?.status} ${job?.errorCode}: ${job?.errorMessage}" }
            library.tracks().any { it.title == source.getString("title") && it.quality == quality }
        }
        return library.tracks().single { it.title == source.getString("title") && it.quality == quality }
    }

    private fun watchBridge() {
        evaluate("""
            window.offlineSmokeAccepted = null;
            if (window.offlineSmokeListener) JastreamerDownloads.removeEventListener('message', window.offlineSmokeListener);
            window.offlineSmokeListener = event => {
              const response = JSON.parse(event.data);
              if (response.result?.job_id || response.error) window.offlineSmokeAccepted = response;
            };
            JastreamerDownloads.addEventListener('message', window.offlineSmokeListener);
        """.trimIndent())
    }

    private fun bridge(action: String, body: JSONObject) {
        val id = "offline-smoke-$action"
        val message = JSONObject(body.toString()).put("id", id).put("action", action)
        evaluate("""
            window.offlineSmokeBridgeResult = null;
            const offlineSmokeReply = event => {
              const response = JSON.parse(event.data);
              if (response.id === ${JSONObject.quote(id)}) {
                window.offlineSmokeBridgeResult = response;
                JastreamerDownloads.removeEventListener('message', offlineSmokeReply);
              }
            };
            JastreamerDownloads.addEventListener('message', offlineSmokeReply);
            JastreamerDownloads.postMessage(${JSONObject.quote(message.toString())});
        """.trimIndent())
        await("native $action response") { evaluate("window.offlineSmokeBridgeResult !== null") == "true" }
        val result = JSONObject(requireNotNull(evaluate("window.offlineSmokeBridgeResult")))
        check(!result.has("error")) { result.optJSONObject("error")?.toString() ?: "Native bridge error" }
    }

    private fun confirmNativeDialog() {
        val automation = instrumentation.uiAutomation
        automation.serviceInfo = automation.serviceInfo.apply { flags = flags or AccessibilityServiceInfo.FLAG_REPORT_VIEW_IDS }
        await("native import confirmation") {
            val root = automation.rootInActiveWindow ?: return@await false
            try {
                val button = root.findAccessibilityNodeInfosByViewId("android:id/button1")
                    .firstOrNull { it.isVisibleToUser && it.isEnabled }
                button?.performAction(AccessibilityNodeInfo.ACTION_CLICK) == true
            } finally {
                root.recycle()
            }
        }
    }

    private fun request(path: String, method: String = "GET", body: JSONObject? = null, expectedStatus: Int = 200): JSONObject {
        val options = JSONObject().put("method", method).put("credentials", "same-origin")
        if (body != null) options.put("body", body.toString()).put("headers", JSONObject()
            .put("Content-Type", "application/json").put("X-Jastreamer-Request", "web"))
        evaluate("""
            window.offlineSmokeHTTP = null;
            fetch(${JSONObject.quote(path)}, $options).then(async response => {
              window.offlineSmokeHTTP = {status:response.status, body:response.status===204?{}:await response.json()};
            }).catch(error => { window.offlineSmokeHTTP = {error:String(error)}; });
        """.trimIndent())
        await("HTTP $method $path") { evaluate("window.offlineSmokeHTTP !== null") == "true" }
        val result = JSONObject(requireNotNull(evaluate("window.offlineSmokeHTTP")))
        check(!result.has("error")) { result.optString("error") }
        assertEquals("HTTP $method $path", expectedStatus, result.getInt("status"))
        return result.getJSONObject("body")
    }

    private fun digest(id: String): String {
        val hash = MessageDigest.getInstance("SHA-256")
        library.openAudio(id).use { handle ->
            val buffer = ByteArray(64 * 1024)
            while (true) {
                val count = handle.input.read(buffer)
                if (count < 0) break
                hash.update(buffer, 0, count)
            }
        }
        return hash.digest().joinToString("") { "%02x".format(it.toInt() and 0xff) }
    }

    private fun main(action: () -> Unit) = instrumentation.runOnMainSync(action)

    private fun await(description: String, timeout: Long = 30_000, condition: () -> Boolean) {
        val deadline = SystemClock.elapsedRealtime() + timeout
        while (SystemClock.elapsedRealtime() < deadline) {
            if (condition()) return
            TimeUnit.MILLISECONDS.sleep(100)
        }
        screenshot("failure-${description.replace(' ', '-')}")
        throw AssertionError("Timed out waiting for $description")
    }
}
