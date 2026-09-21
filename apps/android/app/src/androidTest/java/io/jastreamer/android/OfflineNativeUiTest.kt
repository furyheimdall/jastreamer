package io.jastreamer.android

import android.content.Context
import android.os.SystemClock
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.CheckBox
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import java.util.UUID
import org.junit.After
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

    private fun openSavedMusic() {
        waitFor("saved music launcher") {
            var ready = false
            scenario.onActivity { ready = it.findViewById<View>(R.id.saved_music_button) != null }
            ready
        }
        scenario.onActivity { it.findViewById<View>(R.id.saved_music_button).performClick() }
        waitFor("saved music root") {
            var ready = false
            scenario.onActivity { ready = it.findViewById<View>(R.id.offline_music_root)?.isShown == true }
            ready
        }
    }

    private fun assertMinimumTouchTarget(view: View, context: Context) {
        val minimum = (48 * context.resources.displayMetrics.density).toInt()
        assertTrue("${view.resources.getResourceEntryName(view.id)} width", view.width >= minimum || view.minimumWidth >= minimum)
        assertTrue("${view.resources.getResourceEntryName(view.id)} height", view.height >= minimum || view.minimumHeight >= minimum)
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

    private fun MainActivity.getStringForTest(id: Int): String {
        val language = RecentServerStore(applicationContext).language()
        val config = android.content.res.Configuration(resources.configuration).apply {
            setLocale(java.util.Locale.forLanguageTag(language))
        }
        return createConfigurationContext(config).getString(id)
    }
}
