package io.jastreamer.android

import android.accessibilityservice.AccessibilityServiceInfo
import android.content.Intent
import android.os.SystemClock
import android.graphics.Rect
import android.os.Bundle
import android.view.View
import android.view.ViewGroup
import android.view.accessibility.AccessibilityNodeInfo
import android.widget.Button
import android.widget.CheckBox
import android.widget.HorizontalScrollView
import android.widget.SeekBar
import android.widget.TextView
import androidx.test.core.app.ActivityScenario
import androidx.webkit.ProfileStore
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
            val firstJobId = JSONObject(requireNotNull(evaluate("window.offlineSmokeAccepted")))
                .getJSONObject("result").getString("job_id")
            val previousMeteredConsent = OfflineDownloads.allowMetered(context)
            try {
                OfflineDownloads.setAllowMetered(context, false)
                for (accept in listOf(false, true)) {
                    bridge("configure_network", JSONObject().put("job_id", firstJobId)) {
                        confirmNativeDialog(accept) {
                            assertFalse("Opening network confirmation must not grant consent", OfflineDownloads.allowMetered(context))
                        }
                    }
                    assertEquals("Only explicit native approval grants metered access", accept, OfflineDownloads.allowMetered(context))
                }
            } finally {
                OfflineDownloads.setAllowMetered(context, previousMeteredConsent)
            }
            val compact = download(source, "aac_256")
            val shortAAC = download(shortSource, "aac_256")
            val playlistsBeforeFolder = library.playlists().map { it.id }
            val rootID = source.getString("root_id")
            val folderPath = source.getString("path").substringBeforeLast('/')
            evaluate("document.querySelector('button[data-library-kind=folders]').click()")
            val rootSelector = "button[data-download-kind=folder][data-download-root-id='$rootID'][data-download-path='']"
            await("source root folder") { evaluate("!!document.querySelector(${JSONObject.quote(rootSelector)})") == "true" }
            evaluate("document.querySelector(${JSONObject.quote(rootSelector)}).closest('.library-folder-item').querySelector('.library-folder-row').click()")
            val folderSelector = "button[data-download-kind=folder][data-download-root-id='$rootID'][data-download-path='$folderPath']:not(:disabled)"
            val folderTrack = download(source, "original", folderSelector)
            assertEquals("Folder reimport retains verified local audio", original.id, folderTrack.id)
            assertEquals("A folder import must not create a playlist", playlistsBeforeFolder, library.playlists().map { it.id })
            screenshot("offline-folder-import-complete")
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
            val origin = JSONObject(requireNotNull(evaluate("({origin:location.origin})"))).getString("origin")
            val endpoint = runBlocking {
                ServerProbe().probe(origin)
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
            val playlist = library.savePlaylist(
                null,
                "Offline smoke duplicate snapshot",
                listOf(shortAAC.id, original.id, compact.id, original.id),
            )
            playlistIds.add(playlist.id)
            val destination = library.createFolder(OfflineLibrary.ROOT_FOLDER_ID, "Offline smoke moved")
            folders.add(destination.id)
            library.moveTrack(original.id, destination.id)
            assertEquals("Folder move preserves the local identity", original.id, library.track(original.id)?.id)
            assertEquals(destination.id, library.track(original.id)?.folderId)
            assertEquals("Folder move preserves audio bytes", original.sha256, digest(original.id))
            assertEquals("Folder move preserves playlist duplicates", playlist.trackIds, library.playlist(playlist.id)?.trackIds)

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
            assertEquals("Folder move preserves the playing queue", localQueue, OfflinePlayback.state.value.queue.entries.map { it.trackId })

            scenario.onActivity { it.findViewById<View>(R.id.change_server_button).performClick() }
            await("independent music launcher entry") {
                var visible = false
                scenario.onActivity { visible = it.findViewById<View>(R.id.saved_music_button)?.isShown == true }
                visible
            }
            scenario.onActivity { it.findViewById<View>(R.id.saved_music_button).performClick() }
            await("saved-music library chrome after logout") {
                var ready = false
                scenario.onActivity { activity ->
                    ready = activity.findViewById<View>(R.id.offline_music_root)?.isShown == true &&
                        activity.findViewById<View>(R.id.offline_mini_player)?.isShown == true &&
                        findButtonOrNull(activity.window.decorView, localized(R.string.offline_tracks)) != null
                }
                ready
            }
            scenario.onActivity { activity ->
                findButton(activity.window.decorView, localized(R.string.offline_tracks)).performClick()
            }
            await("populated saved tracks after logout") {
                var populated = false
                scenario.onActivity { activity ->
                    populated = findButtonOrNull(activity.window.decorView, original.title) != null
                }
                populated
            }
            scenario.onActivity { activity ->
                val row = findButton(activity.window.decorView, original.title)
                assertTrue("Track rows remain primary Buttons", row.performLongClick())
            }
            await("real track selection actions") {
                var selected = false
                scenario.onActivity { activity ->
                    selected = activity.findViewById<View>(R.id.offline_selection_bar)?.isShown == true &&
                        activity.findViewById<View>(R.id.offline_move_tracks_button)?.isShown == true &&
                        findButtonOrNull(activity.window.decorView, original.title)?.isSelected == true
                }
                selected
            }
            scenario.onActivity { activity ->
                findButton(activity.window.decorView, localized(R.string.offline_select_all)).performClick()
            }
            await("selection context action selects the populated page") {
                var selected = false
                scenario.onActivity { activity ->
                    selected = findButtonOrNull(activity.window.decorView, original.title)?.isSelected == true &&
                        findButtonOrNull(activity.window.decorView, shortAAC.title)?.isSelected == true
                }
                selected
            }
            scenario.onActivity { activity ->
                findButton(activity.window.decorView, localized(R.string.offline_clear_selection)).performClick()
            }
            await("track selection clears") {
                var cleared = false
                scenario.onActivity {
                    cleared = it.findViewById<View>(R.id.offline_selection_bar)?.visibility == View.GONE
                }
                cleared
            }
            assertNativeSurface(expectMiniPlayer = true)
            screenshot("offline-native-tracks-phone")

            scenario.onActivity { activity ->
                findButton(activity.window.decorView, localized(R.string.offline_albums)).performClick()
            }
            val albumTitle = original.album.ifBlank { localized(R.string.offline_unknown_album) }
            await("populated saved albums") {
                var populated = false
                scenario.onActivity {
                    populated = findButtonOrNull(it.window.decorView, albumTitle) != null
                }
                populated
            }
            assertNativeSurface(expectMiniPlayer = true)
            screenshot("offline-native-albums-phone")

            scenario.onActivity { activity ->
                findButton(activity.window.decorView, localized(R.string.offline_folders)).performClick()
            }
            await("populated saved folders") {
                var populated = false
                scenario.onActivity {
                    populated = findButtonOrNull(it.window.decorView, destination.name) != null
                }
                populated
            }
            assertNativeSurface(expectMiniPlayer = true)
            screenshot("offline-native-folders-phone")

            scenario.onActivity { it.findViewById<View>(R.id.offline_playlists_button).performClick() }
            await("populated saved playlists") {
                var populated = false
                scenario.onActivity {
                    populated = findButtonOrNull(it.window.decorView, playlist.name) != null
                }
                populated
            }
            assertNativeSurface(expectMiniPlayer = true)
            screenshot("offline-native-playlists-phone")

            scenario.onActivity { it.findViewById<View>(R.id.offline_queue_button).performClick() }
            await("populated local queue") {
                var populated = false
                scenario.onActivity {
                    populated = it.findViewById<View>(R.id.offline_queue_button)?.isSelected == true &&
                        findTextOrNull(it.findViewById(R.id.offline_content), original.title) != null
                }
                populated
            }
            assertNativeSurface(expectMiniPlayer = true)
            screenshot("offline-native-queue-phone")

            scenario.onActivity { it.findViewById<View>(R.id.offline_downloads_button).performClick() }
            await("populated native downloads") {
                var populated = false
                scenario.onActivity { activity ->
                    val list = activity.findViewById<ViewGroup>(R.id.offline_download_list)
                    populated = list?.isShown == true && list.childCount > 0 &&
                        findTextOrNull(list, original.title) != null
                }
                populated
            }
            assertNativeSurface(expectMiniPlayer = true)
            screenshot("offline-native-downloads-phone")

            scenario.onActivity { it.findViewById<View>(R.id.offline_settings_button).performClick() }
            await("populated native settings") {
                var populated = false
                scenario.onActivity { activity ->
                    populated = activity.findViewById<View>(R.id.offline_storage_usage)?.isShown == true &&
                        activity.findViewById<View>(R.id.offline_diagnostics)?.isShown == true
                }
                populated
            }
            assertNativeSurface(expectMiniPlayer = true)
            screenshot("offline-native-settings-phone")

            scenario.onActivity { it.findViewById<View>(R.id.offline_library_button).performClick() }
            scenario.onActivity { activity ->
                findButton(activity.window.decorView, localized(R.string.offline_tracks)).performClick()
            }
            await("saved tracks return with mini player") {
                var populated = false
                scenario.onActivity {
                    populated = it.findViewById<View>(R.id.offline_mini_player)?.isShown == true &&
                        findButtonOrNull(it.window.decorView, original.title) != null
                }
                populated
            }
            assertNativeSurface(expectMiniPlayer = true)
            screenshot("offline-native-mini-player-phone")

            main { OfflinePlayback.next() }
            await("converted AAC plays on the local engine") {
                val state = OfflinePlayback.state.value
                check(state.errorCode == null) { "Local AAC playback failed: ${state.errorCode}: ${state.errorMessage}" }
                val current = state.queue.entries.find { it.id == state.queue.currentEntryId }
                current?.trackId == compact.id && state.playing && state.queue.positionMs > 0
            }
            scenario.onActivity {
                it.findViewById<View>(R.id.offline_mini_expand).performClick()
            }
            await("bundled full-player seeking") {
                var visible = false
                scenario.onActivity { visible = it.findViewById<View>(R.id.offline_player_seek)?.isShown == true }
                visible
            }
            assertFullPlayerSurface()
            screenshot("offline-native-full-player-phone")
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
            val pausedPosition = OfflinePlayback.state.value.queue.positionMs
            instrumentation.waitForIdleSync()
            scenario.onActivity {
                it.findViewById<View>(R.id.offline_mini_play_pause).performClick()
            }
            await("full-player resume advances the local engine") {
                val state = OfflinePlayback.state.value
                state.playing && state.queue.positionMs > pausedPosition
            }
            main { OfflinePlayback.pause() }
            await("local pause before deferred deletion") { !OfflinePlayback.state.value.playing }
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

    private fun assertNativeSurface(expectMiniPlayer: Boolean) {
        scenario.onActivity { activity ->
            val root = activity.findViewById<ViewGroup>(R.id.offline_music_root)
            val content = activity.findViewById<ViewGroup>(R.id.offline_content)
            val navigation = activity.findViewById<ViewGroup>(R.id.offline_navigation)
            assertTrue("Native saved-music root must be visible", root.isShown && root.isLaidOut)
            assertNoHorizontalOverflow(content)
            assertNoOverlappingVisibleButtons(root)
            assertTouchTargets(root)
            val tabs = activity.findViewById<HorizontalScrollView>(R.id.offline_library_tabs)
            if (tabs != null && tabs.getGlobalVisibleRect(Rect())) {
                val selected = requireNotNull(findView(tabs) { it is Button && it.isSelected })
                val selectedBounds = visibleRect(selected, "active library category")
                assertEquals("The active category must stay horizontally visible", selected.width, selectedBounds.width())
            }

            val rootRect = visibleRect(root, "saved-music root")
            val navigationRect = visibleRect(navigation, "saved-music navigation")
            assertTrue("Navigation stays inside the saved-music width", navigationRect.left >= rootRect.left && navigationRect.right <= rootRect.right)
            val contentRect = visibleRect(content, "saved-music content")
            val navigationIsLeft = navigationRect.right <= contentRect.left + 1
            if (navigationIsLeft) {
                assertTrue("Wide navigation does not overlap content", !Rect.intersects(navigationRect, contentRect))
            } else if (expectMiniPlayer) {
                val mini = activity.findViewById<View>(R.id.offline_mini_player)
                val miniRect = visibleRect(mini, "offline mini player")
                assertTrue("Narrow navigation stays below the mini player", navigationRect.top >= miniRect.bottom - 1)
                assertTrue("Narrow content stays above the mini player", contentRect.bottom <= miniRect.top + 1)
                assertTrue("Narrow navigation and mini player do not overlap", !Rect.intersects(navigationRect, miniRect))
            }
        }
    }

    private fun assertFullPlayerSurface() {
        scenario.onActivity { activity ->
            val root = activity.findViewById<ViewGroup>(R.id.offline_music_root)
            val content = activity.findViewById<ViewGroup>(R.id.offline_content)
            assertTrue("Expanded controls retain the compact player", activity.findViewById<View>(R.id.offline_mini_player).isShown)
            assertNoOverlappingVisibleButtons(root)
            assertNoHorizontalOverflow(content)
            assertTouchTargets(root)
            val navigationRect = visibleRect(
                activity.findViewById(R.id.offline_navigation),
                "expanded-player navigation",
            )
            val contentRect = visibleRect(content, "expanded-player controls")
            val miniRect = visibleRect(
                activity.findViewById(R.id.offline_mini_player),
                "expanded compact player",
            )
            if (navigationRect.right <= contentRect.left + 1) {
                assertTrue("Wide navigation stays left of expanded controls", !Rect.intersects(navigationRect, contentRect))
            } else {
                assertTrue("Narrow navigation stays below the expanded compact player", navigationRect.top >= miniRect.bottom - 1)
                assertTrue("Expanded controls stay above the compact player", contentRect.bottom <= miniRect.top + 1)
                assertTrue("Navigation does not occlude the expanded compact player", !Rect.intersects(navigationRect, miniRect))
            }
            val seek = activity.findViewById<View>(R.id.offline_player_seek)
            assertTrue("Expanded seek control remains at least 48dp tall", seek.height >= (48 * activity.resources.displayMetrics.density).toInt())
            listOf(
                R.id.offline_mini_play_pause,
                R.id.offline_mini_stop,
                R.id.offline_mini_expand,
                R.id.offline_player_previous,
                R.id.offline_player_next,
                R.id.offline_player_shuffle,
                R.id.offline_player_repeat,
                R.id.offline_player_queue,
                R.id.offline_player_information,
            ).forEach { id ->
                val control = activity.findViewById<View>(id)
                val bounds = visibleRect(control, activity.resources.getResourceEntryName(id))
                val rootBounds = visibleRect(root, "saved-music root")
                assertTrue(
                    "${activity.resources.getResourceEntryName(id)} stays inside the visible player",
                    bounds.left >= rootBounds.left && bounds.top >= rootBounds.top &&
                        bounds.right <= rootBounds.right && bounds.bottom <= rootBounds.bottom,
                )
            }
        }
    }

    private fun assertTouchTargets(root: View) {
        val minimum = (48 * root.resources.displayMetrics.density).toInt()
        visit(root) { view ->
            if (view is Button && view.isShown && view.width > 0) {
                assertTrue("${viewDescription(view)} width is at least 48dp", view.width >= minimum)
                assertTrue("${viewDescription(view)} height is at least 48dp", view.height >= minimum)
            }
            if (view is CheckBox && view.isShown && view.width > 0) {
                assertTrue("${viewDescription(view)} height is at least 48dp", view.height >= minimum)
            }
        }
    }

    private fun assertNoOverlappingVisibleButtons(root: View) {
        val visibleButtons = mutableListOf<Pair<String, Rect>>()
        visit(root) { view ->
            if (view is Button && view.isShown) {
                val bounds = Rect()
                if (view.getGlobalVisibleRect(bounds) && bounds.width() > 0 && bounds.height() > 0) {
                    visibleButtons += viewDescription(view) to bounds
                }
            }
        }
        for (first in visibleButtons.indices) {
            for (second in first + 1 until visibleButtons.size) {
                assertTrue(
                    "${visibleButtons[first].first} overlaps ${visibleButtons[second].first}",
                    !Rect.intersects(visibleButtons[first].second, visibleButtons[second].second),
                )
            }
        }
    }

    private fun assertNoHorizontalOverflow(root: ViewGroup) {
        val rootLocation = IntArray(2)
        root.getLocationOnScreen(rootLocation)
        val left = rootLocation[0]
        val right = left + root.width
        visit(root) { view ->
            if (!view.isShown || view.width == 0) return@visit
            var ancestor = view.parent
            while (ancestor is View && ancestor !== root && ancestor !is HorizontalScrollView) ancestor = ancestor.parent
            val bounds = Rect()
            if (ancestor is HorizontalScrollView) {
                if (!view.getGlobalVisibleRect(bounds)) return@visit
            } else {
                val location = IntArray(2)
                view.getLocationOnScreen(location)
                bounds.set(location[0], location[1], location[0] + view.width, location[1] + view.height)
            }
            assertTrue("${viewDescription(view)} $bounds starts inside native content [$left,$right]", bounds.left >= left - 1)
            assertTrue("${viewDescription(view)} $bounds ends inside native content [$left,$right]", bounds.right <= right + 1)
        }
    }

    private fun visibleRect(view: View, description: String): Rect {
        val bounds = Rect()
        assertTrue("$description must be visible", view.isShown && view.getGlobalVisibleRect(bounds))
        assertTrue("$description must have meaningful bounds", bounds.width() > 0 && bounds.height() > 0)
        return bounds
    }

    private fun viewDescription(view: View): String =
        if (view.id == View.NO_ID) view.javaClass.simpleName else view.resources.getResourceEntryName(view.id)

    private fun findButton(root: View, text: String): Button =
        requireNotNull(findButtonOrNull(root, text)) { "Button '$text' was not found" }

    private fun findButtonOrNull(root: View, text: String): Button? =
        findView(root) {
            it is Button && it.text.toString().lineSequence().firstOrNull()?.removePrefix("✓ ") == text
        } as? Button

    private fun findTextOrNull(root: View, text: String): TextView? =
        findView(root) { it is TextView && it.text.toString().contains(text) } as? TextView

    private fun findView(root: View, predicate: (View) -> Boolean): View? {
        if (predicate(root)) return root
        if (root !is ViewGroup) return null
        for (index in 0 until root.childCount) {
            findView(root.getChildAt(index), predicate)?.let { return it }
        }
        return null
    }

    private fun visit(root: View, action: (View) -> Unit) {
        action(root)
        if (root !is ViewGroup) return
        for (index in 0 until root.childCount) visit(root.getChildAt(index), action)
    }

    private fun localized(id: Int): String {
        val language = RecentServerStore(context).language()
        val configuration = android.content.res.Configuration(context.resources.configuration).apply {
            setLocale(java.util.Locale.forLanguageTag(language))
        }
        return context.createConfigurationContext(configuration).getString(id)
    }

    private fun download(
        source: JSONObject,
        quality: String,
        selector: String = "button[data-download-kind=track][data-download-id='${source.getString("id")}']:not(:disabled):not([data-download-confirm])",
    ): OfflineTrack {
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
            job?.status == "completed" && library.tracks().any { it.title == source.getString("title") && it.quality == quality }
        }
        await("Server shows the completed native download result") {
            evaluate("!!document.querySelector('.native-download-job-list > .native-download-job-completed:first-child')") == "true"
        }
        evaluate("document.querySelector('.native-download-status-dialog .library-dialog-heading button').click()")
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

    private fun bridge(action: String, body: JSONObject, confirm: (() -> Unit)? = null) {
        val id = "offline-smoke-$action"
        val message = JSONObject(body.toString()).put("id", id).put("action", action)
        evaluate("""
            (() => {
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
            })();
        """.trimIndent())
        confirm?.invoke()
        await("native $action response") { evaluate("window.offlineSmokeBridgeResult !== null") == "true" }
        val result = JSONObject(requireNotNull(evaluate("window.offlineSmokeBridgeResult")))
        check(!result.has("error")) { result.optJSONObject("error")?.toString() ?: "Native bridge error" }
    }

    private fun confirmNativeDialog(accept: Boolean = true, beforeClick: (() -> Unit)? = null) {
        val automation = instrumentation.uiAutomation
        automation.serviceInfo = automation.serviceInfo.apply { flags = flags or AccessibilityServiceInfo.FLAG_REPORT_VIEW_IDS }
        await("native download confirmation") {
            val root = automation.rootInActiveWindow ?: return@await false
            try {
                val button = root.findAccessibilityNodeInfosByViewId(if (accept) "android:id/button1" else "android:id/button2")
                    .firstOrNull { it.isVisibleToUser && it.isEnabled }
                if (button == null) false else {
                    beforeClick?.invoke()
                    button.performAction(AccessibilityNodeInfo.ACTION_CLICK)
                }
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
