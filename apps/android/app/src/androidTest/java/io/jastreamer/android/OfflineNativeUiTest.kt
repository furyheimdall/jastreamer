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
import android.view.MotionEvent
import android.view.View
import android.view.ViewGroup
import android.view.accessibility.AccessibilityNodeInfo
import android.widget.Button
import android.widget.CheckBox
import android.widget.EditText
import android.widget.HorizontalScrollView
import android.widget.ScrollView
import android.widget.SeekBar
import android.widget.TextView
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import java.io.File
import java.io.FileInputStream
import java.security.MessageDigest
import java.util.Locale
import java.util.UUID
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import kotlinx.coroutines.runBlocking
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertArrayEquals
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

    private fun importUiTrack(title: String, folderId: String = OfflineLibrary.ROOT_FOLDER_ID, genre: String = ""): OfflineTrack {
        val bytes = UUID.randomUUID().toString().toByteArray()
        val source = File(instrumentation.targetContext.cacheDir, "ui-${UUID.randomUUID()}.wav").apply { writeBytes(bytes) }
        try {
            return library.importTrack(
                source,
                JSONObject()
                    .put("status", "ready")
                    .put("title", title)
                    .put("artist", "Native UX fixture")
                    .put("album", genre.ifBlank { "Native UX fixture" })
                    .put("album_artist", "Native UX fixture")
                    .put("disc", 1)
                    .put("track", 1)
                    .put("genre", genre)
                    .put("duration_ms", 60_000)
                    .put("quality", "original")
                    .put("mime", "audio/wav")
                    .put("codec", "pcm")
                    .put("byte_size", bytes.size)
                    .put("sha256", sha256(bytes)),
                folderId = folderId,
            ).also { createdTrackIds += it.id }
        } finally {
            source.delete()
        }
    }

    private fun assertSelectedCount(activity: MainActivity, count: Int) {
        val prefix = String.format(Locale.ENGLISH, activity.getStringForTest(R.string.offline_selected), count, "")
        val bar = activity.findViewById<View>(R.id.offline_selection_bar)
        assertTrue("Selection must contain $count matching tracks",
            findView(bar) { it is TextView && it.text.toString().startsWith(prefix) } != null)
    }

    @Test
    fun noServerLaunchKeepsSavedMusicAndDownloadsReachable() {
        scenario = ActivityScenario.launch(MainActivity::class.java)

        waitFor("native home entries") {
            var ready = false
            scenario.onActivity { activity ->
                ready = activity.findViewById<View>(R.id.server_playback_button) != null &&
                    activity.findViewById<View>(R.id.saved_music_button) != null &&
                    activity.findViewById<View>(R.id.downloads_button) != null
            }
            ready
        }
        scenario.onActivity { activity ->
            assertMinimumTouchTarget(activity.findViewById(R.id.server_playback_button), activity)
            assertMinimumTouchTarget(activity.findViewById(R.id.saved_music_button), activity)
            assertMinimumTouchTarget(activity.findViewById(R.id.downloads_button), activity)
            assertNotNull(activity.findViewById<View>(R.id.saved_music_summary))
            assertTrue(activity.findViewById<View>(R.id.server_address) == null)
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
    fun homeChooserAndManualAddressKeepDistinctBackAndCancelBoundaries() {
        scenario = ActivityScenario.launch(MainActivity::class.java)
        waitFor("playback home") {
            var visible = false
            scenario.onActivity {
                visible = it.findViewById<View>(R.id.server_playback_button)?.isShown == true
            }
            visible
        }
        screenshot("entry-home")
        scenario.onActivity { activity ->
            activity.findViewById<View>(R.id.server_playback_button).performClick()
            assertTrue(activity.findViewById<View>(R.id.saved_music_button) == null)
        }
        waitFor("server chooser layout") {
            var laidOut = false
            scenario.onActivity {
                laidOut = it.findViewById<View>(R.id.manual_address_button)?.isLaidOut == true
            }
            laidOut
        }
        screenshot("entry-server-chooser")
        scenario.onActivity { activity ->
            assertMinimumTouchTarget(activity.findViewById(R.id.manual_address_button), activity)
            assertTrue(activity.findViewById<View>(R.id.server_address) == null)
            activity.findViewById<View>(R.id.manual_address_button).performClick()
            activity.findViewById<EditText>(R.id.server_address).setText("draft.example:8080")
        }

        scenario.recreate()
        waitFor("restored manual address draft") {
            var restored = false
            scenario.onActivity {
                restored = it.findViewById<EditText>(R.id.server_address)?.let { field ->
                    field.isLaidOut && field.text.toString() == "draft.example:8080"
                } == true
            }
            restored
        }
        screenshot("entry-manual-address")
        scenario.onActivity { activity ->
            assertMinimumTouchTarget(activity.findViewById(R.id.connect_button), activity)
            assertMinimumTouchTarget(activity.findViewById(R.id.manual_cancel_button), activity)
            activity.findViewById<View>(R.id.manual_cancel_button).performClick()
        }
        waitFor("manual cancel returns to chooser") {
            var chooser = false
            scenario.onActivity {
                chooser = it.findViewById<View>(R.id.manual_address_button)?.isShown == true &&
                    it.findViewById<View>(R.id.server_address) == null
            }
            chooser
        }
        scenario.onActivity { activity ->
            activity.findViewById<View>(R.id.manual_address_button).performClick()
            activity.onBackPressedDispatcher.onBackPressed()
        }
        waitFor("back closes manual address first") {
            var chooser = false
            scenario.onActivity {
                chooser = it.findViewById<View>(R.id.manual_address_button)?.isShown == true &&
                    it.findViewById<View>(R.id.server_address) == null
            }
            chooser
        }
        scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }
        waitFor("chooser back returns home") {
            var home = false
            scenario.onActivity {
                home = it.findViewById<View>(R.id.server_playback_button)?.isShown == true &&
                    it.findViewById<View>(R.id.saved_music_button)?.isShown == true
            }
            home
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
        val originalLimit = library.usage().limitBytes
        var originalOrientation = ActivityInfo.SCREEN_ORIENTATION_UNSPECIFIED
        var expectedOrientation = Configuration.ORIENTATION_LANDSCAPE
        try {
            scenario.onActivity { activity ->
                originalOrientation = activity.requestedOrientation
                expectedOrientation = if (activity.resources.configuration.orientation == Configuration.ORIENTATION_LANDSCAPE) {
                    Configuration.ORIENTATION_PORTRAIT
                } else Configuration.ORIENTATION_LANDSCAPE
                (findView(activity.findViewById(R.id.offline_content)) { it is EditText } as EditText).apply {
                    setText("12345")
                    setSelection(3)
                }
                activity.requestedOrientation = if (expectedOrientation == Configuration.ORIENTATION_LANDSCAPE) {
                    ActivityInfo.SCREEN_ORIENTATION_LANDSCAPE
                } else ActivityInfo.SCREEN_ORIENTATION_PORTRAIT
            }
            waitFor("rotation keeps the unapplied storage limit") {
                var preserved = false
                scenario.onActivity { activity ->
                    val input = findView(activity.findViewById(R.id.offline_content)) { it is EditText } as? EditText
                    preserved = activity.resources.configuration.orientation == expectedOrientation &&
                        input?.text?.toString() == "12345" && input.selectionStart == 3
                }
                preserved
            }
            assertEquals(originalLimit, library.usage().limitBytes)
            scenario.recreate()
            waitFor("activity recreation keeps the unapplied storage limit") {
                var preserved = false
                scenario.onActivity { activity ->
                    preserved = (findView(activity.findViewById(R.id.offline_content)) { it is EditText } as? EditText)?.text?.toString() == "12345"
                }
                preserved
            }
            assertEquals(originalLimit, library.usage().limitBytes)
        } finally {
            scenario.onActivity { it.requestedOrientation = originalOrientation }
        }
    }

    @Test
    fun filteredFolderSelectAllExcludesHiddenTracksAndFolders() {
        val marker = UUID.randomUUID().toString()
        val parent = library.createFolder(OfflineLibrary.ROOT_FOLDER_ID, "Scope $marker").also { createdFolderIds += it.id }
        val visible = library.createFolder(parent.id, "match-$marker folder").also { createdFolderIds += it.id }
        val hidden = library.createFolder(parent.id, "Hidden folder").also { createdFolderIds += it.id }
        val matching = importUiTrack("match-$marker track", parent.id)
        importUiTrack("Hidden direct track", parent.id)
        importUiTrack("Visible folder contents", visible.id)
        importUiTrack("Hidden folder contents", hidden.id)
        scenario = ActivityScenario.launch(MainActivity::class.java)
        openSavedMusic()
        scenario.onActivity { activity ->
            findButton(activity.window.decorView, activity.getStringForTest(R.string.offline_folders)).performClick()
            findButton(activity.window.decorView, parent.name).performClick()
            activity.findViewById<EditText>(R.id.offline_search).setText("match-$marker")
            findButton(activity.window.decorView, matching.title).performLongClick()
            findButton(activity.window.decorView, activity.getStringForTest(R.string.offline_select_all)).performClick()
            assertSelectedCount(activity, 2)
        }
    }

    @Test
    fun genreSelectAllWorksAfterReturningFromASelectedTrack() {
        val genre = "Genre ${UUID.randomUUID()}"
        val first = importUiTrack("First genre track", genre = genre)
        importUiTrack("Second genre track", genre = genre)
        scenario = ActivityScenario.launch(MainActivity::class.java)
        openSavedMusic()
        scenario.onActivity { activity ->
            findButton(activity.window.decorView, activity.getStringForTest(R.string.offline_genres)).performClick()
            activity.findViewById<EditText>(R.id.offline_search).setText(genre)
            findButton(activity.window.decorView, genre).performClick()
            findButton(activity.window.decorView, first.title).performLongClick()
            findButton(activity.window.decorView, activity.getStringForTest(R.string.offline_back_to_list)).performClick()
            findButton(activity.window.decorView, activity.getStringForTest(R.string.offline_select_all)).performClick()
            assertSelectedCount(activity, 2)
        }
    }

    @Test
    fun likingATrackKeepsTheCurrentLibraryViewport() {
        val marker = "Viewport ${UUID.randomUUID()}"
        val values = (0 until 12).map { index -> importUiTrack("$marker ${index.toString().padStart(2, '0')}") }
        val last = values.last()
        scenario = ActivityScenario.launch(MainActivity::class.java)
        openSavedMusic()
        scenario.onActivity { activity ->
            findButton(activity.window.decorView, activity.getStringForTest(R.string.offline_tracks)).performClick()
            activity.findViewById<EditText>(R.id.offline_search).setText(marker)
        }
        instrumentation.waitForIdleSync()
        scenario.onActivity { activity ->
            (findView(activity.findViewById(R.id.offline_content)) { it is ScrollView } as ScrollView).fullScroll(View.FOCUS_DOWN)
        }
        var beforeScroll = 0
        waitFor("last filtered track is visible below the first screen") {
            var visible = false
            scenario.onActivity { activity ->
                val scroll = findView(activity.findViewById(R.id.offline_content)) { it is ScrollView } as ScrollView
                beforeScroll = scroll.scrollY
                val label = findButtonOrNull(activity.window.decorView, last.title)
                visible = beforeScroll > 0 && label?.getGlobalVisibleRect(Rect()) == true
            }
            visible
        }
        scenario.onActivity { activity ->
            val row = findButton(activity.window.decorView, last.title).parent as View
            findButton(row, activity.getStringForTest(R.string.offline_like)).performClick()
        }
        waitFor("the liked row finishes refreshing") {
            var stable = false
            scenario.onActivity { activity ->
                val scroll = findView(activity.findViewById(R.id.offline_content)) { it is ScrollView } as ScrollView
                val label = findButtonOrNull(activity.window.decorView, last.title)
                val row = label?.parent as? View
                stable = scroll.isLaidOut && !scroll.isLayoutRequested &&
                    row != null &&
                    findButtonOrNull(row, activity.getStringForTest(R.string.offline_unlike)) != null
            }
            stable
        }
        scenario.onActivity { activity ->
            val scroll = findView(activity.findViewById(R.id.offline_content)) { it is ScrollView } as ScrollView
            assertEquals("Liking a track must retain the library viewport", beforeScroll, scroll.scrollY)
        }
        assertEquals(true, library.track(last.id)?.liked)
        scenario.onActivity { it.findViewById<View>(R.id.offline_mini_expand).performClick() }
        waitFor("expanded controls retain the filtered library viewport") {
            var retained = false
            scenario.onActivity { activity ->
                val scroll = findView(activity.findViewById(R.id.offline_content)) { it is ScrollView } as? ScrollView
                retained = activity.findViewById<View>(R.id.offline_player_seek)?.isShown == true &&
                    scroll?.scrollY == beforeScroll &&
                    activity.findViewById<EditText>(R.id.offline_search)?.text?.toString() == marker
            }
            retained
        }
        screenshot("offline-native-preserved-browse-player")
        scenario.onActivity { it.findViewById<View>(R.id.offline_mini_expand).performClick() }
        waitFor("collapsing playback returns to the same liked row") {
            var retained = false
            scenario.onActivity { activity ->
                val scroll = findView(activity.findViewById(R.id.offline_content)) { it is ScrollView } as? ScrollView
                val row = findButtonOrNull(activity.window.decorView, last.title)?.parent as? View
                retained = activity.findViewById<View>(R.id.offline_player_seek) == null &&
                    scroll?.scrollY == beforeScroll && row != null &&
                    findButtonOrNull(row, activity.getStringForTest(R.string.offline_unlike))?.getGlobalVisibleRect(Rect()) == true
            }
            retained
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
    fun downloadHistoryConfirmationsKeepSavedMusicAndOnlyClearFinishedRecords() {
        val context = instrumentation.targetContext
        val marker = UUID.randomUUID().toString()
        val savedBytes = "history-saved-$marker".toByteArray()
        val completedFixture = DownloadHistoryFixture("completed-$marker", savedBytes, ready = true)
        val preparingFixture = DownloadHistoryFixture("preparing-$marker", ByteArray(0), ready = false)
        val jobIds = mutableListOf<String>()

        try {
            val completedJobId = runBlocking {
                OfflineDownloads.enqueue(
                    context,
                    completedFixture.endpoint,
                    JSONObject().put("kind", "track").put("id", "completed-$marker"),
                    "original",
                ).also(jobIds::add)
            }
            runBlocking { OfflineDownloads.process(context, {}) }
            waitFor("completed download history fixture") {
                OfflineDownloads.jobs.value.firstOrNull { it.id == completedJobId }?.status == "completed"
            }
            val savedTrack = requireNotNull(library.tracks().firstOrNull { it.title == completedFixture.title })
            createdTrackIds += savedTrack.id

            val activeJobId = runBlocking {
                OfflineDownloads.enqueue(
                    context,
                    preparingFixture.endpoint,
                    JSONObject().put("kind", "track").put("id", "active-$marker"),
                    "original",
                ).also(jobIds::add)
            }
            val cancelledJobId = runBlocking {
                OfflineDownloads.enqueue(
                    context,
                    preparingFixture.endpoint,
                    JSONObject().put("kind", "track").put("id", "cancelled-$marker"),
                    "original",
                ).also(jobIds::add)
            }
            OfflineDownloads.cancel(context, cancelledJobId)
            waitFor("cancelled download history fixture") {
                OfflineDownloads.jobs.value.firstOrNull { it.id == cancelledJobId }?.status == "cancelled"
            }

            scenario = ActivityScenario.launch(MainActivity::class.java)
            openDownloads()
            waitFor("terminal download history actions") {
                var ready = false
                scenario.onActivity { activity ->
                    val remove = findJobActionOrNull(
                        activity,
                        completedFixture.title,
                        activity.getStringForTest(R.string.offline_download_remove_history),
                    )
                    ready = remove?.contentDescription == activity.getStringForTest(
                        R.string.offline_download_remove_history_accessibility,
                        completedFixture.title,
                    ) && activity.findViewById<View>(R.id.offline_clear_download_history)?.isShown == true
                }
                ready
            }

            var removeMessage = ""
            var cancelLabel = ""
            scenario.onActivity { activity ->
                removeMessage = activity.getStringForTest(
                    R.string.offline_download_remove_history_message,
                    completedFixture.title,
                )
                cancelLabel = activity.getStringForTest(R.string.offline_cancel)
                findJobAction(
                    activity,
                    completedFixture.title,
                    activity.getStringForTest(R.string.offline_download_remove_history),
                ).performClick()
            }
            waitFor("remove-history confirmation explains saved music retention") {
                accessibilityTextVisible(removeMessage)
            }
            assertTrue("The remove-history confirmation must be cancellable", clickAccessibilityText(cancelLabel))
            waitFor("cancelled remove-history confirmation closes") {
                !accessibilityTextVisible(removeMessage)
            }
            assertTrue(
                "Cancelling record removal must leave the download record",
                OfflineDownloads.jobs.value.any { it.id == completedJobId },
            )
            assertNotNull("Cancelling record removal must keep saved music", library.track(savedTrack.id))

            scenario.onActivity { activity ->
                findJobAction(
                    activity,
                    completedFixture.title,
                    activity.getStringForTest(R.string.offline_download_remove_history),
                ).performClick()
            }
            waitFor("remove-history confirmation returns") { accessibilityTextVisible(removeMessage) }
            var removeLabel = ""
            scenario.onActivity {
                removeLabel = it.getStringForTest(R.string.offline_download_remove_history)
            }
            assertTrue("The download record confirmation must be actionable", clickAccessibilityText(removeLabel))
            waitFor("confirmed download record removal") {
                var removedFromUi = false
                scenario.onActivity { activity ->
                    removedFromUi = findView(activity.findViewById(R.id.offline_download_list)) {
                        it is TextView && it.text.toString() == completedFixture.title
                    } == null
                }
                OfflineDownloads.jobs.value.none { it.id == completedJobId } && removedFromUi
            }
            assertNotNull("Removing history must not remove the imported track", library.track(savedTrack.id))
            library.openAudio(savedTrack.id).use { audio ->
                assertArrayEquals(savedBytes, audio.input.readBytes())
            }

            var clearMessage = ""
            var clearLabel = ""
            scenario.onActivity { activity ->
                clearMessage = activity.getStringForTest(R.string.offline_download_clear_history_message)
                clearLabel = activity.getStringForTest(R.string.offline_download_clear_history)
                activity.findViewById<View>(R.id.offline_clear_download_history).performClick()
            }
            waitFor("bulk history confirmation explains active and saved content retention") {
                accessibilityTextVisible(clearMessage)
            }
            assertTrue("The clear-finished confirmation must be actionable", clickAccessibilityText(clearLabel))
            waitFor("bulk history removal keeps only the active fixture") {
                var finishedControlRemoved = false
                scenario.onActivity { activity ->
                    finishedControlRemoved = activity.findViewById<View>(R.id.offline_clear_download_history) == null
                }
                OfflineDownloads.jobs.value.none { it.id == cancelledJobId } &&
                    OfflineDownloads.jobs.value.any { it.id == activeJobId } &&
                    finishedControlRemoved
            }
            assertNotNull("Bulk history removal must keep saved music", library.track(savedTrack.id))
            screenshot("offline-download-history-active-retained")
        } finally {
            jobIds.forEach { jobId ->
                runCatching { OfflineDownloads.cancel(context, jobId) }
                runCatching { OfflineDownloads.removeHistory(context, jobId) }
            }
            library.tracks().filter { it.title == completedFixture.title }.forEach { track ->
                runCatching { library.deleteTracks(listOf(track.id)) }
                createdTrackIds.remove(track.id)
            }
            preparingFixture.close()
            completedFixture.close()
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
                assertAdaptiveNavigation(activity)
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
                assertTrue(activity.findViewById<View>(R.id.offline_mini_player).isShown)
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
                    assertIconControl(activity.findViewById(id), activity)
                }
                assertMinimumTouchTarget(activity.findViewById<SeekBar>(R.id.offline_player_seek), activity)
                assertNoHorizontalOverflow(activity.findViewById(R.id.offline_content))
                assertAdaptiveNavigation(activity)
            }
            val coveredSearch = Rect()
            scenario.onActivity { activity ->
                assertTrue(activity.findViewById<EditText>(R.id.offline_search).getGlobalVisibleRect(coveredSearch))
            }
            val touchStarted = SystemClock.uptimeMillis()
            for (action in intArrayOf(MotionEvent.ACTION_DOWN, MotionEvent.ACTION_UP)) {
                val event = MotionEvent.obtain(
                    touchStarted, SystemClock.uptimeMillis(), action,
                    coveredSearch.exactCenterX(), coveredSearch.exactCenterY(), 0,
                )
                try {
                    instrumentation.sendPointerSync(event)
                } finally {
                    event.recycle()
                }
            }
            instrumentation.waitForIdleSync()
            scenario.onActivity { activity ->
                assertTrue("Covered browsing must not receive text input", !activity.findViewById<EditText>(R.id.offline_search).hasFocus())
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
                        activity.findViewById<View>(R.id.offline_mini_player)?.isShown == true
                }
                ready
            }
            screenshot("offline-player-landscape")
            scenario.onActivity { activity ->
                val content = activity.findViewById<ViewGroup>(R.id.offline_content)
                assertNoHorizontalOverflow(content)
                val visible = Rect()
                listOf(
                    R.id.offline_player_seek,
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
                    val name = activity.resources.getResourceEntryName(id)
                    assertTrue("$name must be visible without scrolling in landscape", control.getGlobalVisibleRect(visible))
                    assertEquals("$name must not be clipped vertically", control.height, visible.height())
                    assertEquals("$name must not be clipped horizontally", control.width, visible.width())
                }
                assertAdaptiveNavigation(activity)
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
                assertAdaptiveNavigation(it)
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

    private fun openDownloads() {
        waitFor("download launcher") {
            var ready = false
            scenario.onActivity { ready = it.findViewById<View>(R.id.downloads_button) != null }
            ready
        }
        scenario.onActivity { it.findViewById<View>(R.id.downloads_button).performClick() }
        waitFor("native download manager") {
            var ready = false
            scenario.onActivity {
                ready = it.findViewById<View>(R.id.offline_download_list)?.isShown == true
            }
            ready
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

    private fun assertAdaptiveNavigation(activity: MainActivity) {
        val root = activity.findViewById<View>(R.id.offline_music_root)
        val navigation = activity.findViewById<ViewGroup>(R.id.offline_navigation)
        val content = activity.findViewById<View>(R.id.offline_content)
        val miniPlayer = activity.findViewById<View>(R.id.offline_mini_player)
        val rootBounds = Rect()
        val navigationBounds = Rect()
        val contentBounds = Rect()
        val miniBounds = Rect()
        assertTrue("saved-music root is visible", root.getGlobalVisibleRect(rootBounds))
        assertTrue("saved-music navigation is visible", navigation.getGlobalVisibleRect(navigationBounds))
        assertTrue("saved-music content is visible", content.getGlobalVisibleRect(contentBounds))
        assertTrue("mini player is visible", miniPlayer.getGlobalVisibleRect(miniBounds))
        assertTrue(
            "navigation remains inside the saved-music window",
            navigationBounds.left >= rootBounds.left && navigationBounds.right <= rootBounds.right &&
                navigationBounds.top >= rootBounds.top && navigationBounds.bottom <= rootBounds.bottom,
        )
        listOf(
            R.id.offline_library_button,
            R.id.offline_playlists_button,
            R.id.offline_queue_button,
            R.id.offline_settings_button,
        ).forEach { id ->
            val button = activity.findViewById<Button>(id)
            assertMinimumTouchTarget(button, activity)
            val bounds = Rect()
            assertTrue(
                "${activity.resources.getResourceEntryName(id)} is completely visible",
                button.getGlobalVisibleRect(bounds) &&
                    bounds.width() == button.width && bounds.height() == button.height,
            )
        }
        if (navigationBounds.right <= contentBounds.left + 1) {
            assertTrue("Wide navigation stays left of content", !Rect.intersects(navigationBounds, contentBounds))
        } else {
            assertTrue("Narrow navigation stays below the mini player", navigationBounds.top >= miniBounds.bottom - 1)
            assertTrue("Narrow content stays above the mini player", contentBounds.bottom <= miniBounds.top + 1)
            assertTrue("Narrow navigation and mini player do not overlap", !Rect.intersects(navigationBounds, miniBounds))
        }
    }

    private fun assertNoHorizontalOverflow(root: ViewGroup) {
        val rootLocation = IntArray(2)
        root.getLocationOnScreen(rootLocation)
        val left = rootLocation[0]
        val right = left + root.width
        fun visit(view: View) {
            if (view.visibility != View.VISIBLE || view.width == 0) return
            var ancestor = view.parent
            while (ancestor is View && ancestor !== root && ancestor !is HorizontalScrollView) ancestor = ancestor.parent
            val bounds = Rect()
            if (ancestor is HorizontalScrollView) {
                if (!view.getGlobalVisibleRect(bounds)) return
            } else {
                val location = IntArray(2)
                view.getLocationOnScreen(location)
                bounds.set(location[0], location[1], location[0] + view.width, location[1] + view.height)
            }
            assertTrue(
                "${view.javaClass.simpleName} $bounds starts outside ${root.resources.getResourceEntryName(root.id)}",
                bounds.left >= left - 1,
            )
            assertTrue(
                "${view.javaClass.simpleName} $bounds ends outside ${root.resources.getResourceEntryName(root.id)}",
                bounds.right <= right + 1,
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

    private class DownloadHistoryFixture(
        marker: String,
        private val bytes: ByteArray,
        private val ready: Boolean,
    ) : AutoCloseable {
        private val serverId = UUID.randomUUID().toString()
        private val remoteId = "remote-$marker"
        val title = "history-$marker"
        private val digest = MessageDigest.getInstance("SHA-256")
            .digest(bytes)
            .joinToString("") { "%02x".format(it.toInt() and 0xff) }
        private val server = MockWebServer()
        val endpoint: ServerEndpoint

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
                            .put("user", JSONObject().put("id", "history-user")),
                    )
                    request.method == "POST" && request.path == "/api/v1/downloads" -> json(manifest())
                    request.method == "GET" && request.path == "/api/v1/downloads/$remoteId" -> json(manifest())
                    request.method == "DELETE" && request.path == "/api/v1/downloads/$remoteId" ->
                        MockResponse().setResponseCode(204)
                    ready && request.method == "GET" &&
                        request.path == "/api/v1/downloads/$remoteId/files/0" ->
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
            endpoint = ServerEndpoint(
                id = serverId,
                name = "History fixture",
                version = "test",
                origin = "${url.scheme}://${url.host}:${url.port}",
            )
        }

        override fun close() {
            server.shutdown()
        }

        private fun manifest(): JSONObject = JSONObject()
            .put("id", remoteId)
            .put("kind", "track")
            .put("title", title)
            .put("quality", "original")
            .put("status", if (ready) "ready" else "preparing")
            .put("tracks", org.json.JSONArray().put(JSONObject()
                .put("index", 0)
                .put("status", if (ready) "ready" else "preparing")
                .put("title", title)
                .put("artist", "History fixture")
                .put("album", "History fixture")
                .put("album_artist", "History fixture")
                .put("disc", 1)
                .put("track", 1)
                .put("duration_ms", 1_000)
                .put("source_version", if (ready) "version-$remoteId" else "")
                .put("quality", "original")
                .put("mime", if (ready) "audio/flac" else "")
                .put("codec", if (ready) "flac" else "")
                .put("byte_size", if (ready) bytes.size else 0)
                .put("sha256", if (ready) digest else "")
                .put("media_path", if (ready) "/api/v1/downloads/$remoteId/files/0" else "")
                .put("artwork_path", "")))

        private fun json(body: JSONObject): MockResponse = MockResponse()
            .setResponseCode(200)
            .setHeader("Content-Type", "application/json")
            .setBody(body.toString())
    }

    private fun sha256(bytes: ByteArray): String = MessageDigest.getInstance("SHA-256")
        .digest(bytes)
        .joinToString("") { "%02x".format(it) }

    private fun shell(command: String): String =
        instrumentation.uiAutomation.executeShellCommand(command).use { descriptor ->
            FileInputStream(descriptor.fileDescriptor).bufferedReader().use { it.readText() }
        }


    private fun findJobAction(activity: MainActivity, title: String, action: String): Button =
        requireNotNull(findJobActionOrNull(activity, title, action)) {
            "Action '$action' for download '$title' was not found"
        }

    private fun findJobActionOrNull(activity: MainActivity, title: String, action: String): Button? {
        val list = activity.findViewById<View>(R.id.offline_download_list)
        val titleView = findView(list) { it is TextView && it.text.toString() == title } ?: return null
        return findButtonOrNull(titleView.parent as View, action)
    }

    private fun accessibilityTextVisible(value: String): Boolean {
        val root = instrumentation.uiAutomation.rootInActiveWindow ?: return false
        val nodes = root.findAccessibilityNodeInfosByText(value)
        return try {
            nodes.any {
                it.isVisibleToUser &&
                    (it.text?.toString() == value || it.contentDescription?.toString() == value)
            }
        } finally {
            nodes.forEach(AccessibilityNodeInfo::recycle)
            root.recycle()
        }
    }

    private fun clickAccessibilityText(value: String): Boolean {
        val root = instrumentation.uiAutomation.rootInActiveWindow ?: return false
        val nodes = root.findAccessibilityNodeInfosByText(value)
        return try {
            nodes.firstOrNull {
                it.isVisibleToUser && it.isEnabled &&
                    (it.text?.toString() == value || it.contentDescription?.toString() == value)
            }?.performAction(AccessibilityNodeInfo.ACTION_CLICK) == true
        } finally {
            nodes.forEach(AccessibilityNodeInfo::recycle)
            root.recycle()
        }
    }

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

    private fun MainActivity.getStringForTest(id: Int, vararg values: Any): String {
        val language = RecentServerStore(applicationContext).language()
        val config = android.content.res.Configuration(resources.configuration).apply {
            setLocale(java.util.Locale.forLanguageTag(language))
        }
        val localized = createConfigurationContext(config)
        return if (values.isEmpty()) localized.getString(id) else localized.getString(id, *values)
    }
}
