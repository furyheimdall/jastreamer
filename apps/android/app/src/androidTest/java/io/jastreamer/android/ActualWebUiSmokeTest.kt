package io.jastreamer.android

import android.accessibilityservice.AccessibilityService
import android.content.Context
import android.content.ContentValues
import android.content.pm.ActivityInfo
import android.content.res.Configuration
import android.graphics.Bitmap
import android.os.SystemClock
import android.provider.MediaStore
import android.view.InputDevice
import android.view.MotionEvent
import android.view.View
import android.view.ViewGroup
import android.view.inputmethod.InputMethodManager
import android.webkit.WebView
import android.widget.EditText
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import androidx.lifecycle.Lifecycle
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference
import org.json.JSONArray
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class ActualWebUiSmokeTest {
    private val instrumentation = InstrumentationRegistry.getInstrumentation()
    private lateinit var scenario: ActivityScenario<MainActivity>

    @Test
    fun realAccountPhoneLayoutRotationAndSessionRestoration() {
        val origin = requireNotNull(InstrumentationRegistry.getArguments().getString("serverUrl")) {
            "serverUrl is mandatory: run through tooling/qa/android-server-smoke.py on an isolated emulator"
        }
        RecentServerStore(instrumentation.targetContext).setLanguage("en")
        scenario = ActivityScenario.launch(MainActivity::class.java)
        scenario.use {
            scenario.onActivity { activity ->
                val address = activity.findViewById<EditText>(R.id.server_address)
                address.requestFocus()
                (activity.getSystemService(Context.INPUT_METHOD_SERVICE) as InputMethodManager)
                    .showSoftInput(address, InputMethodManager.SHOW_IMPLICIT)
            }
            waitFor("native address keyboard") { keyboardVisible() }
            scenario.onActivity { activity ->
                val address = activity.findViewById<EditText>(R.id.server_address)
                val position = IntArray(2)
                address.getLocationOnScreen(position)
                val ime = ViewCompat.getRootWindowInsets(activity.window.decorView)!!
                    .getInsets(WindowInsetsCompat.Type.ime()).bottom
                assertTrue("Address must remain above the keyboard", position[1] + address.height <= activity.window.decorView.height - ime)
            }
            screenshot("native-keyboard")
            connect(origin)
            waitFor("real first-account form") { evaluate("document.querySelectorAll('.auth-form input').length === 3") == "true" }
            tapWebInput("input[autocomplete=username]")
            waitFor("WebView account keyboard") { keyboardVisible() }
            waitFor("focused account input above the keyboard") {
                evaluate("""
                    (() => {
                        const input = document.querySelector('input[autocomplete=username]');
                        const box = input.getBoundingClientRect();
                        return document.activeElement === input && box.top >= 0 && box.bottom <= visualViewport.height + 1;
                    })()
                """.trimIndent()) == "true"
            }
            screenshot("web-account-keyboard")
            assertTrue(instrumentation.uiAutomation.performGlobalAction(AccessibilityService.GLOBAL_ACTION_BACK))
            waitFor("Back dismisses the keyboard") { !keyboardVisible() }
            assertEquals("Keyboard Back must not leave the selected Server", "3", evaluate("document.querySelectorAll('.auth-form input').length"))
            evaluate(setInput("input[autocomplete=username]", "android-smoke"))
            scenario.onActivity { it.requestedOrientation = ActivityInfo.SCREEN_ORIENTATION_LANDSCAPE }
            waitFor("landscape layout") { orientation() == Configuration.ORIENTATION_LANDSCAPE }
            assertEquals("Rotation must preserve the unsaved username", "\"android-smoke\"", evaluate("document.querySelector('input[autocomplete=username]').value"))
            screenshot("web-login-landscape")
            scenario.onActivity { it.requestedOrientation = ActivityInfo.SCREEN_ORIENTATION_PORTRAIT }
            waitFor("portrait layout") { orientation() == Configuration.ORIENTATION_PORTRAIT }
            evaluate("""
                (() => {
                    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set;
                    for (const input of document.querySelectorAll('input[type=password]')) {
                        setter.call(input, 'android-smoke-fixture-password');
                        input.dispatchEvent(new Event('input', {bubbles:true}));
                    }
                    document.querySelector('.auth-form button[type=submit]').click();
                })()
            """.trimIndent())
            waitFor("authenticated phone library") { evaluate("!!document.querySelector('.phone-player-bar') && document.querySelectorAll('.mobile-nav button').length === 4") == "true" }
            assertEquals("Four phone tabs must fit the visible viewport", "true", evaluate("""
                Array.from(document.querySelectorAll('.mobile-nav button')).every(button => {
                    const box = button.getBoundingClientRect();
                    return box.width >= 44 && box.height >= 44 && box.top >= 0 && box.bottom <= innerHeight + 1;
                })
            """.trimIndent()))
            screenshot("real-web-phone-library")
            assertStopped()

            scenario.moveToState(Lifecycle.State.CREATED)
            scenario.moveToState(Lifecycle.State.RESUMED)
            waitFor("session after background return") { evaluate("!!document.querySelector('.phone-player-bar')") == "true" }
            assertStopped()
            scenario.recreate()
            waitFor("session after activity recreation") { evaluate("!!document.querySelector('.phone-player-bar')") == "true" }
            assertStopped()

            evaluate("document.querySelectorAll('.mobile-nav button')[3].click()")
            waitFor("language settings") { evaluate("!!document.getElementById('language-select')") == "true" }
            evaluate("""
                (() => {
                    const select = document.getElementById('language-select');
                    select.value = 'ko';
                    select.dispatchEvent(new Event('change', {bubbles:true}));
                })()
            """.trimIndent())
            waitFor("Korean Web interface") { evaluate("document.documentElement.lang === 'ko'") == "true" }
            scenario.onActivity { it.findViewById<View>(R.id.change_server_button).performClick() }
            val korean = Configuration(instrumentation.targetContext.resources.configuration).apply {
                setLocale(java.util.Locale.KOREAN)
            }
            val koreanConnectLabel = instrumentation.targetContext.createConfigurationContext(korean).getString(R.string.verify_connect)
            waitFor("native language synchronization") {
                var translated = false
                scenario.onActivity { activity ->
                    translated = activity.findViewById<android.widget.Button>(R.id.connect_button).text == koreanConnectLabel
                }
                translated
            }
            screenshot("native-recent-korean")
            connect(origin)
            waitFor("Korean session after selecting the server again") {
                evaluate("!!document.querySelector('.phone-player-bar') && document.documentElement.lang === 'ko'") == "true"
            }
            assertStopped()
            screenshot("real-web-phone-korean")
        }
    }

    private fun connect(origin: String) {
        scenario.onActivity { activity ->
            activity.findViewById<EditText>(R.id.server_address).setText(origin)
            activity.findViewById<View>(R.id.connect_button).performClick()
        }
    }
    private fun keyboardVisible(): Boolean {
        var visible = false
        scenario.onActivity { activity ->
            visible = ViewCompat.getRootWindowInsets(activity.window.decorView)
                ?.isVisible(WindowInsetsCompat.Type.ime()) == true
        }
        return visible
    }

    private fun tapWebInput(selector: String) {
        val point = JSONArray(requireNotNull(evaluate("""
            (() => {
                const input = document.querySelector('$selector');
                input.scrollIntoView({block:'center', behavior:'instant'});
                const box = input.getBoundingClientRect();
                return [(box.left + box.width / 2) * devicePixelRatio, (box.top + box.height / 2) * devicePixelRatio];
            })()
        """.trimIndent())))
        val location = IntArray(2)
        scenario.onActivity { activity -> requireNotNull(webView(activity.window.decorView)).getLocationOnScreen(location) }
        val down = SystemClock.uptimeMillis()
        for (action in intArrayOf(MotionEvent.ACTION_DOWN, MotionEvent.ACTION_UP)) {
            val event = MotionEvent.obtain(
                down, SystemClock.uptimeMillis(), action,
                location[0] + point.getDouble(0).toFloat(), location[1] + point.getDouble(1).toFloat(), 0,
            )
            event.source = InputDevice.SOURCE_TOUCHSCREEN
            try {
                assertTrue("The account field must accept an actual Android touch", instrumentation.uiAutomation.injectInputEvent(event, true))
            } finally {
                event.recycle()
            }
        }
    }


    private fun assertStopped() {
        evaluate("""
            window.androidSmokePlayer = null;
            fetch('/api/v1/player', {credentials:'same-origin'})
                .then(async response => {
                    if (!response.ok) throw new Error('HTTP ' + response.status);
                    window.androidSmokePlayer = await response.json();
                });
        """.trimIndent())
        waitFor("authoritative stopped player") { evaluate("window.androidSmokePlayer !== null && window.androidSmokePlayer !== undefined") == "true" }
        assertEquals("Lifecycle must not resume playback", "\"stopped\"", evaluate("window.androidSmokePlayer.state"))
        assertEquals("Lifecycle must not select an output", "\"\"", evaluate("window.androidSmokePlayer.renderer_id"))
    }

    private fun setInput(selector: String, value: String) = """
        (() => {
            const input = document.querySelector('$selector');
            Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(input, '$value');
            input.dispatchEvent(new Event('input', {bubbles:true}));
        })()
    """.trimIndent()

    private fun evaluate(script: String): String? {
        val result = AtomicReference<String?>()
        val latch = CountDownLatch(1)
        scenario.onActivity { activity ->
            val view = webView(activity.window.decorView)
            if (view == null) {
                latch.countDown()
            } else {
                view.evaluateJavascript(script) {
                    result.set(it)
                    latch.countDown()
                }
            }
        }
        assertTrue("WebView JavaScript callback timed out", latch.await(10, TimeUnit.SECONDS))
        return result.get()
    }

    private fun webView(view: View): WebView? {
        if (view is WebView) return view
        if (view is ViewGroup) {
            for (index in 0 until view.childCount) {
                webView(view.getChildAt(index))?.let { return it }
            }
        }
        return null
    }

    private fun orientation(): Int {
        var current = Configuration.ORIENTATION_UNDEFINED
        scenario.onActivity { current = it.resources.configuration.orientation }
        return current
    }

    private fun screenshot(name: String) {
        instrumentation.waitForIdleSync()
        val bitmap = requireNotNull(instrumentation.uiAutomation.takeScreenshot()) { "Emulator screenshot unavailable" }
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

    private fun waitFor(description: String, condition: () -> Boolean) {
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(30)
        while (System.nanoTime() < deadline) {
            if (condition()) return
            Thread.sleep(100)
        }
        screenshot("failure-${description.replace(' ', '-')}")
        throw AssertionError("Timed out waiting for $description")
    }
}
