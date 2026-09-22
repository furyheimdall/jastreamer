package io.jastreamer.android

import android.accessibilityservice.AccessibilityServiceInfo
import android.content.ContentValues
import android.content.Context
import android.content.Intent
import android.content.pm.ActivityInfo
import android.content.res.Configuration
import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Rect
import android.os.SystemClock
import android.provider.MediaStore
import android.view.View
import android.view.ViewGroup
import android.view.accessibility.AccessibilityNodeInfo
import android.widget.Button
import android.widget.CheckBox
import android.widget.SeekBar
import android.widget.TextView
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import java.io.File
import java.io.FileInputStream
import java.security.MessageDigest
import java.util.UUID
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.runBlocking
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class OfflineNativeUiTest {
    private val instrumentation = InstrumentationRegistry.getInstrumentation()
    private lateinit var scenario: ActivityScenario<MainActivity>
    private lateinit var library: OfflineLibrary
    private val createdFolderIds = mutableListOf<String>()
    private val createdTrackIds = mutableListOf<String>()

    @Before
    fun prepare() {
        RecentServerStore(instrumentation.targetContext).setLanguage("en")
        library = OfflineLibrary.get(instrumentation.targetContext)
    }

    @After
    fun cleanUp() {
        if (::scenario.isInitialized) scenario.close()
        createdFolderIds.asReversed().forEach { id ->
            runCatching { if (library.folder(id) != null) library.deleteFolder(id) }
        }
        createdTrackIds.asReversed().forEach { id ->
            runCatching { if (library.track(id) != null) library.deleteTracks(listOf(id)) }
        }
    }

    @Test
    fun noServerLaunchKeepsSavedMusicAndDownloadsReachable() {
        scenario = ActivityScenario.launch(MainActivity::class.java)

        waitFor("native chooser entries") {
            var ready = false
            scenario.onActivity { activity ->
                ready = activity.findViewById<View>(R.id.saved_music_button) != null &&
                    activity.findViewById<View>(R.id.downloads_button) != null
            }
            ready
        }
        scenario.onActivity { activity ->
            assertMinimumTouchTarget(activity.findViewById(R.id.saved_music_button), activity)
            assertMinimumTouchTarget(activity.findViewById(R.id.downloads_button), activity)
            assertNotNull(activity.findViewById<View>(R.id.saved_music_summary))
            activity.findViewById<View>(R.id.saved_music_button).performClick()
        }

        waitFor("saved music without a server") {
            var visible = false
            scenario.onActivity { visible = it.findViewById<View>(R.id.offline_music_root)?.isShown == true }
            visible
        }
        scenario.onActivity { activity ->
            assertNotNull(activity.findViewById<View>(R.id.offline_library_button))
            assertNotNull(activity.findViewById<View>(R.id.offline_playlists_button))
            assertNotNull(activity.findViewById<View>(R.id.offline_queue_button))
            assertNotNull(activity.findViewById<View>(R.id.offline_settings_button))
            assertMinimumTouchTarget(activity.findViewById(R.id.offline_servers_button), activity)
            assertMinimumTouchTarget(activity.findViewById(R.id.offline_downloads_button), activity)
        }

        scenario.recreate()
        waitFor("saved music survives activity recreation") {
            var visible = false
            scenario.onActivity { visible = it.findViewById<View>(R.id.offline_music_root)?.isShown == true }
            visible
        }
    }

    @Test
    fun folderAndSettingsManagementRemainNativeAndAccessible() {
        val folderName = "UI ${UUID.randomUUID()}"
        createdFolderIds += library.createFolder(OfflineLibrary.ROOT_FOLDER_ID, folderName).id
        scenario = ActivityScenario.launch(MainActivity::class.java)
        openSavedMusic()

        scenario.onActivity { activity ->
            findButton(activity.window.decorView, activity.getStringForTest(R.string.offline_folders)).performClick()
        }
        waitFor("folder browser") {
            var ready = false
            scenario.onActivity { activity ->
                ready = activity.findViewById<View>(R.id.offline_new_folder_button)?.isShown == true &&
                    findButtonOrNull(activity.window.decorView, folderName) != null
            }
            ready
        }
        scenario.onActivity { activity ->
            assertMinimumTouchTarget(activity.findViewById(R.id.offline_new_folder_button), activity)
            findButton(activity.window.decorView, folderName).performClick()
        }
        waitFor("real folder actions") {
            var ready = false
            scenario.onActivity { activity ->
                ready = activity.findViewById<View>(R.id.offline_rename_folder_button)?.isShown == true &&
                    activity.findViewById<View>(R.id.offline_move_folder_button)?.isShown == true &&
                    activity.findViewById<View>(R.id.offline_delete_folder_button)?.isShown == true
            }
            ready
        }
        scenario.onActivity { activity ->
            assertMinimumTouchTarget(activity.findViewById(R.id.offline_rename_folder_button), activity)
            assertMinimumTouchTarget(activity.findViewById(R.id.offline_move_folder_button), activity)
            assertMinimumTouchTarget(activity.findViewById(R.id.offline_delete_folder_button), activity)
            activity.findViewById<View>(R.id.offline_settings_button).performClick()
        }
        waitFor("persistent storage settings") {
            var ready = false
            scenario.onActivity { ready = it.findViewById<View>(R.id.offline_storage_usage)?.isShown == true }
            ready
        }
        scenario.onActivity { activity ->
            assertNotNull(activity.findViewById<View>(R.id.offline_diagnostics))
        }
    }

    @Test
    fun downloadManagerIsAvailableDirectlyAndShowsMeteredPolicy() {
        scenario = ActivityScenario.launch(MainActivity::class.java)
        waitFor("download launcher") {
            var ready = false
            scenario.onActivity { ready = it.findViewById<View>(R.id.downloads_button) != null }
            ready
        }
        scenario.onActivity { it.findViewById<View>(R.id.downloads_button).performClick() }
        waitFor("native download manager") {
            var ready = false
            scenario.onActivity { activity ->
                ready = activity.findViewById<View>(R.id.offline_download_list)?.isShown == true &&
                    findView(activity.window.decorView) { it is CheckBox } != null
            }
            ready
        }
        scenario.onActivity { activity ->
            assertNotNull(activity.findViewById<View>(R.id.offline_servers_button))
            assertNotNull(activity.findViewById<View>(R.id.offline_mini_player))
        }
    }

    @Test
    fun offlinePlayerUsesAccessibleIconControlsWithoutNarrowOrLandscapeOverflow() {
        val context = instrumentation.targetContext
        // Earlier playback tests may leave a system-bound service owning a stopped
        // local queue. Release that test-owned engine before installing this fixture.
        instrumentation.runOnMainSync {
            if (OfflinePlayback.state.value.owner == OfflinePlaybackPolicy.OWNER_LOCAL) {
                NativePlaybackRegistry.service?.prepareServerHandoff(
                    confirmHandoff = true,
                    requestGeneration = OfflinePlaybackRequestFence.beginRequest(),
                )
            }
        }
        context.stopService(Intent(context, NativePlaybackService::class.java))
        waitFor("previous test playback ownership releases") {
            OfflinePlayback.state.value.owner == OfflinePlaybackPolicy.OWNER_NONE &&
                !OfflinePlayback.state.value.playing
        }
        val originalQueue = library.loadQueue()
        val originalFontScale = shell("settings get system font_scale").trim().toFloatOrNull() ?: 1f
        val marker = UUID.randomUUID().toString()
        val bytes = "offline-player-ui-$marker".toByteArray()
        val source = File(context.cacheDir, "$marker.wav").apply { writeBytes(bytes) }
        val artwork = File(context.cacheDir, "$marker.png").also(::writeArtwork)
        val track = library.importTrack(
            source,
            JSONObject()
                .put("status", "ready")
                .put("title", "A deliberately long saved track title that must stay inside the player")
                .put("artist", "A long local artist name for narrow and large-font layouts")
                .put("album", "Offline player layout fixture")
                .put("album_artist", "Local fixture artist")
                .put("disc", 1)
                .put("track", 1)
                .put("duration_ms", 182_000)
                .put("quality", "original")
                .put("mime", "audio/wav")
                .put("codec", "pcm")
                .put("byte_size", bytes.size)
                .put("sha256", sha256(bytes)),
            artwork = artwork,
        )
        createdTrackIds += track.id
        val entry = OfflineQueueEntry("ui-$marker", track.id)
        val fixtureQueue = OfflineQueue(listOf(entry), entry.id, 31_000)
        var originalRequestedOrientation = ActivityInfo.SCREEN_ORIENTATION_UNSPECIFIED

        try {
            library.saveQueue(fixtureQueue)
            runBlocking { OfflinePlayback.restore(context) }
            assertTrue(
                "fixture queue must load without replacing active playback ownership",
                OfflinePlayback.state.value.owner == "none" &&
                    OfflinePlayback.state.value.queue.currentEntryId == entry.id,
            )
            shell("settings put system font_scale 1.3")
            scenario = ActivityScenario.launch(MainActivity::class.java)
            scenario.onActivity { originalRequestedOrientation = it.requestedOrientation }
            waitFor("large-font activity configuration") {
                var scaled = false
                scenario.onActivity { scaled = it.resources.configuration.fontScale >= 1.25f }
                scaled
            }
            openSavedMusic()
            waitFor("offline mini player fixture") {
                var shown = false
                scenario.onActivity {
                    val mini = it.findViewById<View>(R.id.offline_mini_player)
                    val title = it.findViewById<TextView>(R.id.offline_player_title)
                    shown = mini?.isShown == true && mini.isLaidOut && title?.text?.toString() == track.title
                }
                shown
            }
            scenario.onActivity { activity ->
                val mini = activity.findViewById<ViewGroup>(R.id.offline_mini_player)
                listOf(
                    activity.findViewById<Button>(R.id.offline_mini_play_pause),
                    activity.findViewById<Button>(R.id.offline_mini_stop),
                    activity.findViewById<Button>(R.id.offline_mini_expand),
                ).forEach { control ->
                    assertIconControl(control, activity)
                }
                assertNoHorizontalOverflow(mini)
                val title = activity.findViewById<TextView>(R.id.offline_player_title)
                assertTrue("mini title stays on one rendered line", title.layout.lineCount == 1)
                assertTrue((title.parent as View).contentDescription.toString().contains(title.text))
            }
            screenshot("offline-player-mini")

            scenario.onActivity { it.findViewById<View>(R.id.offline_mini_expand).performClick() }
            waitFor("expanded offline player") {
                var shown = false
                scenario.onActivity {
                    val seek = it.findViewById<View>(R.id.offline_player_seek)
                    shown = seek?.isShown == true && seek.isLaidOut
                }
                shown
            }
            scenario.onActivity { activity ->
                assertTrue(activity.findViewById<View>(R.id.offline_mini_player).visibility == View.GONE)
                listOf(
                    R.id.offline_player_close,
                    R.id.offline_player_previous,
                    R.id.offline_player_play_pause,
                    R.id.offline_player_next,
                    R.id.offline_player_stop,
                    R.id.offline_player_shuffle,
                    R.id.offline_player_repeat,
                    R.id.offline_player_queue,
                    R.id.offline_player_information,
                ).forEach { id ->
                    assertIconControl(activity.findViewById(id), activity)
                }
                assertMinimumTouchTarget(activity.findViewById<SeekBar>(R.id.offline_player_seek), activity)
                assertNoHorizontalOverflow(activity.findViewById(R.id.offline_content))
            }

            scenario.onActivity {
                it.findViewById<View>(R.id.offline_player_shuffle).performClick()
            }
            waitFor("shuffle icon updates the local queue") {
                OfflinePlayback.state.value.queue.shuffle && library.loadQueue().shuffle
            }
            scenario.onActivity {
                it.findViewById<View>(R.id.offline_player_repeat).performClick()
            }
            waitFor("repeat icon updates the local queue") {
                OfflinePlayback.state.value.queue.repeatMode == androidx.media3.common.Player.REPEAT_MODE_ALL &&
                    library.loadQueue().repeatMode == androidx.media3.common.Player.REPEAT_MODE_ALL
            }

            screenshot("offline-player-expanded")

            scenario.onActivity { it.requestedOrientation = ActivityInfo.SCREEN_ORIENTATION_LANDSCAPE }
            waitFor("landscape expanded offline player") {
                var ready = false
                scenario.onActivity { activity ->
                    ready = activity.resources.configuration.orientation == Configuration.ORIENTATION_LANDSCAPE &&
                        activity.findViewById<View>(R.id.offline_player_seek)?.isShown == true &&
                        activity.findViewById<View>(R.id.offline_player_artwork)?.isLaidOut == true
                }
                ready
            }
            screenshot("offline-player-landscape")
            scenario.onActivity { activity ->
                val content = activity.findViewById<ViewGroup>(R.id.offline_content)
                val playerArtwork = activity.findViewById<View>(R.id.offline_player_artwork)
                assertNoHorizontalOverflow(content)
                assertTrue("landscape artwork width", playerArtwork.width <= content.width - dp(activity, 24))
                val visible = Rect()
                listOf(
                    R.id.offline_player_seek,
                    R.id.offline_player_previous,
                    R.id.offline_player_play_pause,
                    R.id.offline_player_next,
                    R.id.offline_player_stop,
                    R.id.offline_player_shuffle,
                    R.id.offline_player_repeat,
                    R.id.offline_player_queue,
                    R.id.offline_player_information,
                ).forEach { id ->
                    val control = activity.findViewById<View>(id)
                    val name = activity.resources.getResourceEntryName(id)
                    assertTrue("$name must be visible without scrolling in landscape", control.getGlobalVisibleRect(visible))
                    assertEquals("$name must not be clipped vertically", control.height, visible.height())
                    assertEquals("$name must not be clipped horizontally", control.width, visible.width())
                }
            }
            scenario.onActivity {
                it.findViewById<View>(R.id.offline_player_information).performClick()
            }
            val automation = instrumentation.uiAutomation
            automation.serviceInfo = automation.serviceInfo.apply {
                flags = flags or AccessibilityServiceInfo.FLAG_REPORT_VIEW_IDS
            }
            waitFor("track information dialog") {
                val root = automation.rootInActiveWindow ?: return@waitFor false
                try {
                    val titleVisible = root.findAccessibilityNodeInfosByText(track.title)
                        .any { it.isVisibleToUser }
                    val done = root.findAccessibilityNodeInfosByViewId("android:id/button1")
                        .firstOrNull { it.isVisibleToUser && it.isEnabled }
                    titleVisible && done?.performAction(AccessibilityNodeInfo.ACTION_CLICK) == true
                } finally {
                    root.recycle()
                }
            }
            instrumentation.waitForIdleSync()
            scenario.onActivity { activity ->
                activity.findViewById<View>(R.id.offline_player_queue).performClick()
            }
            waitFor("queue affordance closes the full player") {
                var queueShown = false
                scenario.onActivity { activity ->
                    queueShown = activity.findViewById<View>(R.id.offline_queue_button).isSelected &&
                        activity.findViewById<View>(R.id.offline_player_seek) == null &&
                        activity.findViewById<View>(R.id.offline_mini_player).isShown
                }
                queueShown
            }
            scenario.onActivity {
                assertNoHorizontalOverflow(it.findViewById(R.id.offline_content))
            }
        } finally {
            if (::scenario.isInitialized) {
                runCatching {
                    scenario.onActivity {
                        it.requestedOrientation = ActivityInfo.SCREEN_ORIENTATION_PORTRAIT
                    }
                    waitFor("portrait cleanup", timeoutMillis = 5_000) {
                        var portrait = false
                        scenario.onActivity {
                            portrait = it.resources.configuration.orientation == Configuration.ORIENTATION_PORTRAIT
                        }
                        portrait
                    }
                    scenario.onActivity { it.requestedOrientation = originalRequestedOrientation }
                }
            }
            shell("settings put system font_scale $originalFontScale")
            if (::scenario.isInitialized) {
                runCatching {
                    waitFor("font scale cleanup", timeoutMillis = 5_000) {
                        var restored = false
                        scenario.onActivity {
                            restored = kotlin.math.abs(
                                it.resources.configuration.fontScale - originalFontScale,
                            ) < 0.01f
                        }
                        restored
                    }
                }
            }
            library.saveQueue(originalQueue)
            runBlocking { OfflinePlayback.restore(context) }
            if (library.track(track.id) != null) library.deleteTracks(listOf(track.id))
            createdTrackIds.remove(track.id)
        }
    }

    private fun openSavedMusic() {
        waitFor("saved music launcher") {
            var ready = false
            scenario.onActivity { ready = it.findViewById<View>(R.id.saved_music_button) != null }
            ready
        }
        scenario.onActivity { it.findViewById<View>(R.id.saved_music_button).performClick() }
        waitFor("saved music library is loaded") {
            var ready = false
            scenario.onActivity { activity ->
                ready = activity.findViewById<View>(R.id.offline_music_root)?.isShown == true &&
                    findButtonOrNull(activity.window.decorView, activity.getStringForTest(R.string.offline_folders)) != null
            }
            ready
        }
    }

    private fun assertMinimumTouchTarget(view: View, context: Context) {
        val minimum = (48 * context.resources.displayMetrics.density).toInt()
        assertTrue("${view.resources.getResourceEntryName(view.id)} width", view.width >= minimum)
        assertTrue("${view.resources.getResourceEntryName(view.id)} height", view.height >= minimum)
    }

    private fun assertIconControl(button: Button, context: Context) {
        assertMinimumTouchTarget(button, context)
        assertTrue(
            "${button.resources.getResourceEntryName(button.id)} description",
            !button.contentDescription.isNullOrBlank(),
        )
    }

    private fun assertNoHorizontalOverflow(root: ViewGroup) {
        val rootLocation = IntArray(2)
        root.getLocationOnScreen(rootLocation)
        val left = rootLocation[0]
        val right = left + root.width
        fun visit(view: View) {
            if (view.visibility != View.VISIBLE || view.width == 0) return
            val location = IntArray(2)
            view.getLocationOnScreen(location)
            assertTrue(
                "${view.javaClass.simpleName} starts outside ${root.resources.getResourceEntryName(root.id)}",
                location[0] >= left - 1,
            )
            assertTrue(
                "${view.javaClass.simpleName} ends outside ${root.resources.getResourceEntryName(root.id)}",
                location[0] + view.width <= right + 1,
            )
            if (view is ViewGroup) {
                for (index in 0 until view.childCount) visit(view.getChildAt(index))
            }
        }
        visit(root)
    }

    private fun screenshot(name: String) {
        val rendered = CountDownLatch(1)
        scenario.onActivity { activity ->
            activity.window.decorView.postOnAnimation {
                activity.window.decorView.postOnAnimation { rendered.countDown() }
            }
        }
        assertTrue("Rendered Android frame did not commit", rendered.await(10, TimeUnit.SECONDS))
        instrumentation.waitForIdleSync()
        val bitmap = requireNotNull(instrumentation.uiAutomation.takeScreenshot()) {
            "Emulator screenshot unavailable"
        }
        val resolver = instrumentation.targetContext.contentResolver
        val values = ContentValues().apply {
            put(MediaStore.Images.Media.DISPLAY_NAME, "$name.png")
            put(MediaStore.Images.Media.MIME_TYPE, "image/png")
            put(MediaStore.Images.Media.RELATIVE_PATH, "Pictures/jastreamer-android-smoke")
            put(MediaStore.Images.Media.IS_PENDING, 1)
        }
        val uri = requireNotNull(resolver.insert(MediaStore.Images.Media.EXTERNAL_CONTENT_URI, values))
        requireNotNull(resolver.openOutputStream(uri)).use { stream ->
            check(bitmap.compress(Bitmap.CompressFormat.PNG, 100, stream))
        }
        values.clear()
        values.put(MediaStore.Images.Media.IS_PENDING, 0)
        check(resolver.update(uri, values, null, null) == 1)
        bitmap.recycle()
    }

    private fun writeArtwork(file: File) {
        val bitmap = Bitmap.createBitmap(96, 96, Bitmap.Config.ARGB_8888)
        val canvas = Canvas(bitmap)
        canvas.drawColor(Color.rgb(37, 43, 47))
        val paint = android.graphics.Paint().apply {
            color = Color.rgb(200, 237, 178)
            style = android.graphics.Paint.Style.FILL
        }
        canvas.drawCircle(48f, 48f, 28f, paint)
        file.outputStream().use { stream ->
            check(bitmap.compress(Bitmap.CompressFormat.PNG, 100, stream))
        }
        bitmap.recycle()
    }

    private fun sha256(bytes: ByteArray): String = MessageDigest.getInstance("SHA-256")
        .digest(bytes)
        .joinToString("") { "%02x".format(it) }

    private fun shell(command: String): String =
        instrumentation.uiAutomation.executeShellCommand(command).use { descriptor ->
            FileInputStream(descriptor.fileDescriptor).bufferedReader().use { it.readText() }
        }

    private fun dp(context: Context, value: Int) =
        (value * context.resources.displayMetrics.density).toInt()

    private fun findButton(root: View, text: String): Button =
        requireNotNull(findButtonOrNull(root, text)) { "Button '$text' was not found" }

    private fun findButtonOrNull(root: View, text: String): Button? =
        findView(root) { it is Button && it.text.toString().lineSequence().firstOrNull() == text } as? Button

    private fun findView(root: View, predicate: (View) -> Boolean): View? {
        if (predicate(root)) return root
        if (root !is ViewGroup) return null
        for (index in 0 until root.childCount) {
            findView(root.getChildAt(index), predicate)?.let { return it }
        }
        return null
    }

    private fun waitFor(description: String, timeoutMillis: Long = 10_000, condition: () -> Boolean) {
        val deadline = SystemClock.elapsedRealtime() + timeoutMillis
        while (SystemClock.elapsedRealtime() < deadline) {
            instrumentation.waitForIdleSync()
            if (condition()) return
            SystemClock.sleep(40)
        }
        throw AssertionError("Timed out waiting for $description")
    }

    private fun MainActivity.getStringForTest(id: Int): String {
        val language = RecentServerStore(applicationContext).language()
        val config = android.content.res.Configuration(resources.configuration).apply {
            setLocale(java.util.Locale.forLanguageTag(language))
        }
        return createConfigurationContext(config).getString(id)
    }
}
