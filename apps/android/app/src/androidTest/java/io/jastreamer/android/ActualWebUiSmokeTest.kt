package io.jastreamer.android

import android.accessibilityservice.AccessibilityService
import android.app.ActivityManager
import android.content.ComponentName
import android.content.ContentValues
import android.content.Context
import android.content.pm.ActivityInfo
import android.content.res.Configuration
import android.graphics.Bitmap
import android.os.Looper
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
import androidx.media3.common.Player
import androidx.media3.session.MediaController
import androidx.media3.session.SessionToken
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference
import org.json.JSONArray
import org.json.JSONObject
import org.json.JSONTokener
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
            waitFor("native chooser window focus") {
                var focused = false
                scenario.onActivity { focused = it.hasWindowFocus() }
                focused
            }
            awaitRenderedFrame()
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
            waitFor("native address keyboard dismissal") { !keyboardVisible() }
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
            assertEquals("Embedded chrome must hide only duplicate branding, not the signed-in account controls", "true", evaluate("""
                (() => {
                    const brand = document.querySelector('.mobile-header .brand');
                    const account = document.querySelector('.mobile-account');
                    const logout = account?.querySelector('button');
                    if (!account || !logout) return false;
                    const target = logout.getBoundingClientRect();
                    return (!brand || !brand.checkVisibility()) && account.checkVisibility() && logout.checkVisibility()
                        && account.textContent.includes('android-smoke')
                        && target.width >= 44 && target.height >= 44
                        && target.top >= 0 && target.bottom <= innerHeight;
                })()
            """.trimIndent()))
            screenshot("real-web-phone-library")
            assertStopped()

            evaluate("window.androidRecoveryMarker = 'view-' + Math.random(); 'marked'")
            armDiscoveryFault("disconnect")
            scenario.moveToState(Lifecycle.State.CREATED)
            scenario.moveToState(Lifecycle.State.RESUMED)
            waitFor("transient revalidation retry preserves the live WebView") {
                webViewVisible() &&
                    evaluate(
                        "!!document.querySelector('.phone-player-bar') && /^view-/.test(window.androidRecoveryMarker)",
                    ) == "true"
            }
            assertStopped()

            armDiscoveryFault("delay", delayMillis = 10_000)
            scenario.moveToState(Lifecycle.State.CREATED)
            scenario.moveToState(Lifecycle.State.RESUMED)
            SystemClock.sleep(300)
            scenario.moveToState(Lifecycle.State.CREATED)
            val cancelledAt = SystemClock.elapsedRealtime()
            scenario.moveToState(Lifecycle.State.RESUMED)
            waitFor("background cancellation does not await a stale delayed probe") {
                webViewVisible() &&
                    evaluate(
                        "!!document.querySelector('.phone-player-bar') && /^view-/.test(window.androidRecoveryMarker)",
                    ) == "true"
            }
            assertTrue(
                "A cancelled 10 second probe must not gate the next foreground revalidation",
                SystemClock.elapsedRealtime() - cancelledAt < 6_000,
            )
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
            exerciseNativePlayback()
        }
    }

    private fun connect(origin: String) {
        scenario.onActivity { activity ->
            activity.findViewById<EditText>(R.id.server_address).setText(origin)
            activity.findViewById<View>(R.id.connect_button).performClick()
        }
    }

    private fun armDiscoveryFault(mode: String, delayMillis: Int? = null) {
        armProxyFault("/api/v1/discovery", prefix = false, mode = mode, delayMillis = delayMillis)
    }

    private fun armMediaFault(mode: String, delayMillis: Int? = null, count: Int = 1) {
        armProxyFault("/media/", prefix = true, mode = mode, delayMillis = delayMillis, count = count)
    }

    private fun armProxyFault(
        path: String,
        prefix: Boolean,
        mode: String,
        delayMillis: Int?,
        count: Int = 1,
    ) {
        require(count in 1..8)
        val delay = delayMillis?.let { ",delay_ms:$it" }.orEmpty()
        val prefixField = if (prefix) ",prefix:true" else ""
        evaluate(
            """
            window.androidFaultArmed = false;
            fetch('/__android_smoke/fault', {
              method:'POST',
              headers:{'Content-Type':'application/json'},
              body:JSON.stringify({path:'$path',count:$count,mode:'$mode'$delay$prefixField})
            }).then(response => {
              if (!response.ok) throw new Error('fault control HTTP ' + response.status);
              window.androidFaultArmed = true;
            }).catch(error => { window.androidFaultArmed = String(error); });
            'arming';
            """.trimIndent(),
        )
        waitFor("$path fault arm") {
            evaluate("window.androidFaultArmed === true") == "true"
        }
    }

    private fun resetProxyFault() {
        evaluate(
            """
            window.androidFaultReset = false;
            fetch('/__android_smoke/fault/reset', {method:'POST'})
              .then(response => {
                if (!response.ok) throw new Error('fault reset HTTP ' + response.status);
                window.androidFaultReset = true;
              })
              .catch(error => { window.androidFaultReset = String(error); });
            'resetting';
            """.trimIndent(),
        )
        waitFor("proxy fault reset") {
            evaluate("window.androidFaultReset === true") == "true"
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
        evaluate("document.querySelector('$selector').scrollIntoView({block:'center', behavior:'instant'})")
        awaitRenderedFrame()
        val point = JSONArray(requireNotNull(evaluate("""
            (() => {
                const input = document.querySelector('$selector');
                const box = input.getBoundingClientRect();
                return [(box.left + box.width / 2) * devicePixelRatio, (box.top + box.height / 2) * devicePixelRatio];
            })()
        """.trimIndent())))
        val location = IntArray(2)
        scenario.onActivity { activity -> requireNotNull(webView(activity.window.decorView)).getLocationOnScreen(location) }
        val down = SystemClock.uptimeMillis()
        for (action in intArrayOf(MotionEvent.ACTION_DOWN, MotionEvent.ACTION_UP)) {
            val event = MotionEvent.obtain(
                down, down, action,
                location[0] + point.getDouble(0).toFloat(), location[1] + point.getDouble(1).toFloat(), 0,
            )
            event.source = InputDevice.SOURCE_TOUCHSCREEN
            try {
                // Queue the complete tap without stretching it into a long press while DOWN is handled.
                assertTrue(
                    "The account field must accept an actual Android touch",
                    instrumentation.uiAutomation.injectInputEvent(event, action == MotionEvent.ACTION_UP),
                )
            } finally {
                event.recycle()
            }
        }
    }


    private fun exerciseNativePlayback() {
        armMediaFault("disconnect")
        evaluate(
            """
            window.androidNativeSetup = undefined;
            (async () => {
              window.androidRequestNative = (action, name) => new Promise((resolve, reject) => {
                const id = 'android-smoke-' + action + '-' + Date.now() + '-' + Math.random();
                const timer = setTimeout(() => reject(new Error('native ' + action + ' timeout')), 10000);
                const listener = event => {
                  let message;
                  try { message = JSON.parse(event.data); } catch (_) { return; }
                  if (message.id !== id) return;
                  clearTimeout(timer);
                  JastreamerAndroidAudio.removeEventListener('message', listener);
                  if (message.error) reject(new Error(message.error.code + ': ' + message.error.message));
                  else resolve(message);
                };
                JastreamerAndroidAudio.addEventListener('message', listener);
                JastreamerAndroidAudio.postMessage(JSON.stringify({
                  id, action, ...(name === undefined ? {} : {name})
                }));
              });
              const api = async (path, method = 'GET', body) => {
                const response = await fetch('/api/v1' + path, {
                  method,
                  credentials: 'same-origin',
                  headers: {
                    'Accept': 'application/json',
                    'Content-Type': 'application/json',
                    'X-Jastreamer-Request': 'web'
                  },
                  ...(body === undefined ? {} : {body: JSON.stringify(body)})
                });
                if (!response.ok) throw new Error(method + ' ' + path + ': HTTP ' + response.status);
                return response.status === 204 ? null : response.json();
              };
              const native = await window.androidRequestNative('connect', 'Android smoke output');
              await api('/library/scans', 'POST', {});
              let tracks;
              for (let attempt = 0; attempt < 100; attempt++) {
                tracks = await api('/library/tracks?limit=200');
                if (tracks.items.some(track => track.root_id === 'android-smoke' && track.path === 'long.wav') &&
                    tracks.items.some(track => track.root_id === 'android-smoke' && track.path === 'short.wav') &&
                    tracks.items.some(track => track.root_id === 'android-smoke' && track.path === 'artwork/background.wav')) break;
                await new Promise(resolve => setTimeout(resolve, 100));
              }
              const long = tracks.items.find(track => track.root_id === 'android-smoke' && track.path === 'long.wav');
              const short = tracks.items.find(track => track.root_id === 'android-smoke' && track.path === 'short.wav');
              const background = tracks.items.find(track => track.root_id === 'android-smoke' && track.path === 'artwork/background.wav');
              if (!long || !short || !background) throw new Error('isolated audio fixtures were not scanned');
              const player = await api('/player');
              if (player.state !== 'stopped') {
                await api('/player', 'POST', {action:'stop'});
                for (let attempt = 0; attempt < 100; attempt++) {
                  if ((await api('/player')).state === 'stopped') break;
                  await new Promise(resolve => setTimeout(resolve, 100));
                }
              }
              const queue = await api('/queue');
              const replacement = await api('/queue', 'POST', {
                action:'replace', track_ids:[short.id, long.id, background.id], revision:queue.revision
              });
              await api('/player/output', 'PUT', {renderer_id:native.device.id});
              await api('/player', 'POST', {action:'play'});
              return {
                deviceId:native.device.id,
                longId:long.id,
                shortId:short.id,
                backgroundId:background.id,
                initialEntryId:replacement.entries[0].id
              };
            })().then(
              value => { window.androidNativeSetup = value; },
              error => { window.androidNativeSetup = {error:String(error && error.message || error)}; }
            );
            'started';
            """.trimIndent(),
        )
        waitFor("native playback fixture setup") {
            evaluate("window.androidNativeSetup !== undefined") == "true"
        }
        val setup = JSONObject(requireNotNull(evaluate("window.androidNativeSetup")))
        check(!setup.has("error")) { setup.optString("error") }
        val deviceId = setup.getString("deviceId")
        val longId = setup.getString("longId")
        val shortId = setup.getString("shortId")
        val backgroundId = setup.getString("backgroundId")
        val initialEntryId = setup.getString("initialEntryId")
        waitForPlayer("native media retry preserves the starting queue entry") { player ->
            player.optString("state") == "playing" &&
                player.optString("renderer_id") == deviceId &&
                player.optString("current_entry_id") == initialEntryId &&
                player.optJSONObject("track")?.optString("id") == shortId &&
                player.optString("error").isEmpty()
        }
        waitForPlayer("natural completion advances authoritatively to the long track") { player ->
            player.optString("state") == "playing" &&
                player.optString("renderer_id") == deviceId &&
                player.optJSONObject("track")?.optString("id") == longId &&
                player.optString("current_entry_id") != initialEntryId &&
                player.optString("error").isEmpty()
        }

        val token = SessionToken(
            instrumentation.targetContext,
            ComponentName(instrumentation.targetContext, NativePlaybackService::class.java),
        )
        val future = onMain {
            MediaController.Builder(instrumentation.targetContext, token)
                .setApplicationLooper(Looper.getMainLooper())
                .buildAsync()
        }
        val controller = future.get(10, TimeUnit.SECONDS)
        val activityManager = instrumentation.targetContext.getSystemService(ActivityManager::class.java)
        @Suppress("DEPRECATION")
        fun hasForegroundPlaybackService(): Boolean =
            activityManager.getRunningServices(32).any {
                it.service.className == NativePlaybackService::class.java.name && it.foreground
            }
        var primaryFailure: Throwable? = null
        try {
            waitFor("real MediaSession playback and metadata") {
                onMain {
                    controller.isConnected &&
                        controller.isPlaying &&
                        controller.duration >= 590_000L &&
                        controller.mediaMetadata.title?.toString()?.contains("long", ignoreCase = true) == true
                }
            }
            assertTrue(onMain { controller.availableCommands.contains(Player.COMMAND_PLAY_PAUSE) })
            assertTrue(onMain { controller.availableCommands.contains(Player.COMMAND_STOP) })
            assertTrue(onMain { controller.availableCommands.contains(Player.COMMAND_SEEK_IN_CURRENT_MEDIA_ITEM) })
            assertTrue(onMain { controller.availableCommands.contains(Player.COMMAND_SEEK_TO_NEXT_MEDIA_ITEM) })
            assertTrue(onMain { controller.availableCommands.contains(Player.COMMAND_SEEK_TO_PREVIOUS_MEDIA_ITEM) })
            screenshot("native-playing")

            scenario.moveToState(Lifecycle.State.CREATED)
            val backgroundPosition = onMain { controller.currentPosition }
            SystemClock.sleep(1_500)
            waitFor("native playback advances while the Activity stays backgrounded") {
                onMain {
                    controller.isConnected &&
                        controller.isPlaying &&
                        controller.currentPosition >= backgroundPosition + 500L
                }
            }
            waitFor("Android identifies the playing service as foreground") {
                hasForegroundPlaybackService()
            }
            armMediaFault("delay", delayMillis = 1_000)
            onMain { controller.seekToNextMediaItem() }
            waitFor("background track replacement retains foreground protection") {
                assertTrue(
                    "Background track replacement must not drop the Android foreground playback service",
                    hasForegroundPlaybackService(),
                )
                onMain {
                    controller.isPlaying &&
                        controller.mediaMetadata.title?.toString()?.contains("background", ignoreCase = true) == true
                }
            }
            assertTrue("Replacement artwork must have loaded", onMain { controller.mediaMetadata.artworkData != null })
            waitForPlayer("Server confirms the background track replacement") {
                it.optString("state") == "playing" &&
                    it.optJSONObject("track")?.optString("id") == backgroundId &&
                    it.optString("pending_command").isEmpty()
            }
            waitForPlayer("replacement advances before seeking back") {
                it.optString("state") == "playing" &&
                    it.optLong("position_ms") >= 1_000L &&
                    it.optString("pending_command").isEmpty()
            }
            onMain { controller.pause() }
            waitForPlayer("pause replacement before resetting previous navigation") {
                it.optString("state") == "paused" && it.optString("pending_command").isEmpty()
            }
            onMain { controller.seekTo(0) }
            waitForPlayer("replacement seek completes before previous command") {
                it.optString("state") == "paused" &&
                    it.optLong("position_ms") < 1_000L &&
                    it.optString("pending_command").isEmpty()
            }
            onMain { controller.seekToPreviousMediaItem() }
            waitForPlayer("background previous restores the long track") {
                it.optString("state") == "playing" &&
                    it.optJSONObject("track")?.optString("id") == longId &&
                    it.optString("pending_command").isEmpty()
            }
            onMain { controller.pause() }
            waitForPlayer("background MediaSession pause reaches Server") {
                it.optString("state") == "paused" && it.optString("pending_command").isEmpty()
            }
            waitFor("background MediaSession pause takes effect") {
                onMain { controller.isConnected && !controller.playWhenReady && !controller.isPlaying }
            }
            onMain { controller.play() }
            waitForPlayer("background MediaSession play reaches Server") {
                it.optString("state") == "playing" && it.optString("pending_command").isEmpty()
            }
            waitFor("background MediaSession play takes effect") {
                onMain { controller.isConnected && controller.playWhenReady && controller.isPlaying }
            }
            val mediaTitle = requireNotNull(onMain { controller.mediaMetadata.title?.toString() })
            assertTrue(
                "Android must expose expanded system media controls",
                instrumentation.uiAutomation.performGlobalAction(AccessibilityService.GLOBAL_ACTION_QUICK_SETTINGS),
            )
            waitFor("active transport session in Android system media controls") {
                systemUiShowsMediaTitle(mediaTitle)
            }
            screenshotSystemUi("native-background")
            assertTrue(
                "Android must dismiss its media panel without navigating the app",
                instrumentation.uiAutomation.performGlobalAction(
                    AccessibilityService.GLOBAL_ACTION_DISMISS_NOTIFICATION_SHADE,
                ),
            )

            scenario.moveToState(Lifecycle.State.RESUMED)
            scenario.recreate()
            waitFor("native playback survives Activity recreation") {
                onMain { controller.isConnected && controller.isPlaying } &&
                    evaluate("typeof window.JastreamerAndroidAudio === 'object'") == "true"
            }
            evaluate(
                """
                window.androidRecreatedNativeStatus = undefined;
                (() => {
                  const id = 'android-smoke-recreated-status';
                  const listener = event => {
                    let message;
                    try { message = JSON.parse(event.data); } catch (_) { return; }
                    if (message.id !== id) return;
                    JastreamerAndroidAudio.removeEventListener('message', listener);
                    window.androidRecreatedNativeStatus = message;
                  };
                  JastreamerAndroidAudio.addEventListener('message', listener);
                  JastreamerAndroidAudio.postMessage(JSON.stringify({id,action:'status'}));
                })();
                'sent';
                """.trimIndent(),
            )
            waitFor("recreated document reattaches the exact native owner") {
                evaluate("window.androidRecreatedNativeStatus !== undefined") == "true"
            }
            assertEquals(
                deviceId,
                decodeJavascriptString(
                    "window.androidRecreatedNativeStatus.device && window.androidRecreatedNativeStatus.device.id",
                ),
            )
            screenshot("native-recreated")

            onMain { controller.pause() }
            waitForPlayer("system MediaSession pause reaches Server") {
                it.optString("state") == "paused" && it.optString("pending_command").isEmpty()
            }
            onMain { controller.seekTo(1_000) }
            waitForPlayer("system MediaSession seek reaches Server") {
                it.optString("state") == "paused" &&
                    it.optLong("position_ms") in 700L..2_500L &&
                    it.optString("pending_command").isEmpty()
            }
            onMain { controller.play() }
            waitForPlayer("system MediaSession play reaches Server") {
                it.optString("state") == "playing" && it.optString("pending_command").isEmpty()
            }
            onMain { controller.seekToPreviousMediaItem() }
            waitForPlayer("system MediaSession previous reaches Server") {
                it.optString("state") == "playing" &&
                    it.optJSONObject("track")?.optString("id") == shortId &&
                    it.optString("pending_command").isEmpty()
            }
            onMain { controller.pause() }
            waitForPlayer("short track is paused before exercising Next") {
                it.optString("state") == "paused" &&
                    it.optJSONObject("track")?.optString("id") == shortId &&
                    it.optString("pending_command").isEmpty()
            }
            onMain { controller.seekToNextMediaItem() }
            waitForPlayer("system MediaSession next reaches Server") {
                it.optString("state") == "playing" &&
                    it.optJSONObject("track")?.optString("id") == longId &&
                    it.optString("pending_command").isEmpty()
            }
            waitFor("long media is actually playing before recovery") {
                onMain {
                    controller.isPlaying &&
                        controller.duration >= 590_000L &&
                        controller.currentPosition >= 0L
                }
            }

            armMediaFault("delay", delayMillis = 10_000, count = 8)
            onMain { controller.seekTo(570_000L) }
            waitForPlayer("the long-track seek command finishes before recovery controls") {
                it.optString("state") == "playing" &&
                    it.optJSONObject("track")?.optString("id") == longId &&
                    it.optLong("position_ms") in 568_000L..572_500L &&
                    it.optString("pending_command").isEmpty()
            }
            waitForNativeState("autonomous native recovery becomes observable") {
                it.optBoolean("recovering") &&
                    it.optJSONObject("device")?.optString("id") == deviceId
            }

            onMain { controller.pause() }
            waitForPlayer("Pause is accepted while autonomous recovery is active") {
                it.optString("state") == "paused" &&
                    it.optJSONObject("track")?.optString("id") == longId &&
                    it.optString("pending_command").isEmpty()
            }
            waitFor("MediaSession records paused intent while buffering", 2_000L) {
                onMain { !controller.playWhenReady }
            }
            SystemClock.sleep(4_500)
            waitForNativeState("recovery remains active across the next delayed retry", 2_000L) {
                it.optBoolean("recovering")
            }
            assertTrue(
                "Autonomous retry must preserve the later Pause intent",
                onMain { !controller.playWhenReady },
            )

            val stopDuringRecoveryAt = SystemClock.elapsedRealtime()
            onMain { controller.stop() }
            waitForPlayer("Stop cancels autonomous native media recovery", 6_000L) {
                it.optString("state") == "stopped" && it.optString("pending_command").isEmpty()
            }
            waitForNativeState("native recovery is cleared by Stop", 6_000L) {
                !it.optBoolean("recovering") && !it.has("error")
            }
            waitFor("real MediaSession stops and releases its decoder", 6_000L) {
                onMain { !controller.isPlaying && controller.playbackState == Player.STATE_IDLE }
            }
            assertTrue(
                "Stop must not wait for delayed media recovery",
                SystemClock.elapsedRealtime() - stopDuringRecoveryAt < 6_000L,
            )

            SystemClock.sleep(10_500)
            waitForPlayer("stale media recovery cannot restart after Stop") {
                it.optString("state") == "stopped" &&
                    it.optString("pending_command").isEmpty() &&
                    it.optString("error").isEmpty()
            }
            waitForNativeState("stale recovery remains canceled after the original delay") {
                !it.optBoolean("recovering") && !it.has("error")
            }
            assertTrue(onMain { !controller.isPlaying && controller.playbackState == Player.STATE_IDLE })
            screenshot("native-recovery-stopped")

            resetProxyFault()
            armMediaFault("not_found")
            onMain { controller.play() }
            waitForPlayer("a real Media3 preparation failure reaches Server") {
                it.optString("state") == "error" &&
                    it.optString("renderer_id") == deviceId &&
                    it.optString("pending_command").isEmpty()
            }
            waitForNativeState("failed preparation preserves the native registration") {
                it.optJSONObject("device")?.optString("id") == deviceId &&
                    it.optJSONObject("error")?.optString("code") == "playback_failed"
            }
            resetProxyFault()
            onMain { controller.play() }
            waitForPlayer("the same output replays after failed preparation") {
                it.optString("state") == "playing" &&
                    it.optString("renderer_id") == deviceId &&
                    it.optString("pending_command").isEmpty()
            }
            waitFor("Media3 is playing before a runtime read failure") {
                onMain { controller.isPlaying && controller.currentPosition >= 250L }
            }
            armMediaFault("not_found")
            onMain { controller.seekTo(570_000L) }
            waitForPlayer("a runtime Media3 error stops only the current playback") {
                it.optString("state") == "error" &&
                    it.optString("renderer_id") == deviceId &&
                    it.optString("pending_command").isEmpty()
            }
            waitForNativeState("runtime playback error retains a replayable registration") {
                it.optJSONObject("device")?.optString("id") == deviceId &&
                    it.optJSONObject("error")?.optString("code") == "media_error"
            }
            screenshot("native-playback-error")
            resetProxyFault()

            evaluate("document.querySelector('.error-dialog-close')?.click(); 'dismissed';")
            awaitRenderedFrame()
            armMediaFault("malformed_media")
            onMain { controller.play() }
            waitForPlayer("unreadable media advances to the next track without losing registration") {
                it.optString("state") == "playing" &&
                    it.optString("renderer_id") == deviceId &&
                    it.optJSONObject("track")?.optString("id") == backgroundId &&
                    it.optString("pending_command").isEmpty()
            }
            waitFor("the next track is actually playing through Media3") {
                onMain {
                    controller.isPlaying &&
                        controller.mediaMetadata.title?.toString()?.contains("background", ignoreCase = true) == true
                }
            }
            assertTrue(
                "Automatic continuation must not leave a blocking error dialog",
                evaluate("document.querySelector('[role=\"alertdialog\"]') === null") == "true",
            )
            screenshot("native-media-failure-continued")
            evaluate(
                """
                window.androidSkippedMedia = undefined;
                (async () => {
                  const queue = await (await fetch('/api/v1/queue', {credentials:'same-origin'})).json();
                  const failed = queue.entries.find(entry => entry.track_id === ${JSONObject.quote(longId)});
                  if (!failed || failed.status !== 'error') throw new Error('failed queue entry was not retained');
                  const response = await fetch('/api/v1/player', {
                    method:'POST', credentials:'same-origin',
                    headers:{'Content-Type':'application/json','X-Jastreamer-Request':'web'},
                    body:JSON.stringify({action:'play', entry_id:failed.id})
                  });
                  if (!response.ok) throw new Error('explicit failed-entry replay: HTTP ' + response.status);
                  return {failedEntryId:failed.id};
                })().then(
                  value => { window.androidSkippedMedia = value; },
                  error => { window.androidSkippedMedia = {error:String(error && error.message || error)}; }
                );
                'started';
                """.trimIndent(),
            )
            waitFor("failed entry remains selectable for explicit replay") {
                evaluate("window.androidSkippedMedia !== undefined") == "true"
            }
            val skippedMedia = JSONObject(requireNotNull(evaluate("window.androidSkippedMedia")))
            check(!skippedMedia.has("error")) { skippedMedia.optString("error") }

            waitForPlayer("long media restarts before the lease watchdog scenario") {
                it.optString("state") == "playing" &&
                    it.optJSONObject("track")?.optString("id") == longId &&
                    it.optString("pending_command").isEmpty()
            }
            waitFor("restarted long media is physically playing") {
                onMain { controller.isPlaying && controller.mediaItemCount == 1 }
            }

            armProxyFault(
                path = "/api/v1/browser-output/",
                prefix = true,
                mode = "delay",
                delayMillis = 15_000,
                count = 8,
            )
            val leaseFaultStartedAt = SystemClock.elapsedRealtime()
            waitForNativeState("monotonic lease watchdog expires the native registration", 18_000L) {
                it.optJSONObject("device") == null &&
                    !it.optBoolean("recovering") &&
                    it.optJSONObject("error")?.optString("code") == "lease_expired"
            }
            val leaseExpiryElapsed = SystemClock.elapsedRealtime() - leaseFaultStartedAt
            assertTrue(
                "The native registration must expire at its monotonic lease deadline, not after a later HTTP timeout",
                leaseExpiryElapsed in 10_000L..17_500L,
            )
            waitFor("lease expiry physically stops buffered native media", 3_000L) {
                onMain { !controller.isPlaying && controller.mediaItemCount == 0 }
            }
            waitFor("lease expiry releases foreground playback protection", 3_000L) {
                !hasForegroundPlaybackService()
            }
            waitForRendererAbsent("expired native output disappears from Server", deviceId, 5_000L)

            resetProxyFault()
            SystemClock.sleep(3_000)
            waitForNativeState("fault reset does not automatically register native output", 2_000L) {
                it.optJSONObject("device") == null &&
                    !it.optBoolean("recovering") &&
                    it.optJSONObject("error")?.optString("code") == "lease_expired"
            }
            waitForRendererAbsent("expired output stays absent after fault reset", deviceId, 2_000L)
            assertTrue(onMain { !controller.isPlaying && controller.mediaItemCount == 0 })

            stopServerPlaybackAfterLeaseLoss()
            waitForPlayer("lease scenario finishes in authoritative stopped state") {
                it.optString("state") == "stopped" && it.optString("pending_command").isEmpty()
            }
        } catch (error: Throwable) {
            primaryFailure = error
            throw error
        } finally {
            val cleanupFailure = runCatching {
                onMain {
                    runCatching { controller.stop() }
                    MediaController.releaseFuture(future)
                }
            }.exceptionOrNull()
            if (cleanupFailure != null) {
                primaryFailure?.addSuppressed(cleanupFailure) ?: throw cleanupFailure
            }
        }
    }

    private fun <T> onMain(block: () -> T): T {
        val result = AtomicReference<Result<T>>()
        instrumentation.runOnMainSync { result.set(runCatching(block)) }
        return requireNotNull(result.get()).getOrThrow()
    }

    private fun waitForPlayer(
        description: String,
        timeoutMillis: Long = 30_000L,
        condition: (JSONObject) -> Boolean,
    ) {
        var latest: JSONObject? = null
        waitFor(description, timeoutMillis) {
            evaluate(
                """
                window.androidSmokePlayerPoll = undefined;
                fetch('/api/v1/player', {credentials:'same-origin'})
                  .then(async response => {
                    if (!response.ok) throw new Error('HTTP ' + response.status);
                    window.androidSmokePlayerPoll = await response.json();
                  })
                  .catch(error => { window.androidSmokePlayerPoll = {poll_error:String(error)}; });
                'polling';
                """.trimIndent(),
            )
            val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(2)
            while (System.nanoTime() < deadline &&
                evaluate("window.androidSmokePlayerPoll !== undefined") != "true"
            ) {
                Thread.sleep(25)
            }
            val encoded = evaluate("window.androidSmokePlayerPoll") ?: return@waitFor false
            latest = JSONObject(encoded)
            condition(requireNotNull(latest))
        }
    }

    private fun waitForNativeState(
        description: String,
        timeoutMillis: Long = 30_000L,
        condition: (JSONObject) -> Boolean,
    ) {
        var latest: JSONObject? = null
        waitFor(description, timeoutMillis) {
            evaluate(
                """
                window.androidSmokeNativeState = undefined;
                (() => {
                  const id = 'android-smoke-status-' + Date.now() + '-' + Math.random();
                  const listener = event => {
                    let message;
                    try { message = JSON.parse(event.data); } catch (_) { return; }
                    if (message.id !== id) return;
                    JastreamerAndroidAudio.removeEventListener('message', listener);
                    window.androidSmokeNativeState = message;
                  };
                  JastreamerAndroidAudio.addEventListener('message', listener);
                  setTimeout(() => JastreamerAndroidAudio.removeEventListener('message', listener), 2000);
                  JastreamerAndroidAudio.postMessage(JSON.stringify({id,action:'status'}));
                })();
                'polling';
                """.trimIndent(),
            )
            val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(2)
            while (System.nanoTime() < deadline &&
                evaluate("window.androidSmokeNativeState !== undefined") != "true"
            ) {
                Thread.sleep(25)
            }
            val encoded = evaluate("window.androidSmokeNativeState") ?: return@waitFor false
            latest = JSONObject(encoded)
            condition(requireNotNull(latest))
        }
    }

    private fun waitForRendererAbsent(
        description: String,
        deviceId: String,
        timeoutMillis: Long,
    ) {
        waitFor(description, timeoutMillis) {
            evaluate(
                """
                window.androidSmokeRenderers = undefined;
                fetch('/api/v1/renderers', {credentials:'same-origin'})
                  .then(async response => {
                    if (!response.ok) throw new Error('HTTP ' + response.status);
                    window.androidSmokeRenderers = await response.json();
                  })
                  .catch(error => { window.androidSmokeRenderers = {poll_error:String(error)}; });
                'polling';
                """.trimIndent(),
            )
            val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(2)
            while (System.nanoTime() < deadline &&
                evaluate("window.androidSmokeRenderers !== undefined") != "true"
            ) {
                Thread.sleep(25)
            }
            evaluate(
                "Array.isArray(window.androidSmokeRenderers && window.androidSmokeRenderers.items) && " +
                    "!window.androidSmokeRenderers.items.some(device => device.id === '$deviceId')",
            ) == "true"
        }
    }

    private fun stopServerPlaybackAfterLeaseLoss() {
        evaluate(
            """
            window.androidLeaseStop = undefined;
            fetch('/api/v1/player', {
              method:'POST',
              credentials:'same-origin',
              headers:{'Content-Type':'application/json','X-Jastreamer-Request':'web'},
              body:JSON.stringify({action:'stop'})
            }).then(async response => {
              if (!response.ok) throw new Error('HTTP ' + response.status);
              window.androidLeaseStop = await response.json();
            }).catch(error => { window.androidLeaseStop = {request_error:String(error)}; });
            'stopping';
            """.trimIndent(),
        )
        waitFor("Server accepts the final offline Stop") {
            evaluate("window.androidLeaseStop !== undefined") == "true"
        }
        val result = JSONObject(requireNotNull(evaluate("window.androidLeaseStop")))
        check(!result.has("request_error")) { result.optString("request_error") }
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

    private fun decodeJavascriptString(script: String): String? {
        val encoded = evaluate(script) ?: return null
        if (encoded == "null") return null
        return JSONTokener(encoded).nextValue() as? String
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

    private fun webViewVisible(): Boolean {
        var visible = false
        scenario.onActivity { activity ->
            val browser = webView(activity.window.decorView)
            visible = browser?.visibility == View.VISIBLE && browser.isShown
        }
        return visible
    }

    private fun systemUiShowsMediaTitle(title: String): Boolean {
        val root = instrumentation.uiAutomation.rootInActiveWindow ?: return false
        return try {
            root.packageName?.toString() == SYSTEM_UI_PACKAGE &&
                root.findAccessibilityNodeInfosByText(title).any { it.isVisibleToUser }
        } finally {
            root.recycle()
        }
    }

    private fun orientation(): Int {
        var current = Configuration.ORIENTATION_UNDEFINED
        scenario.onActivity { current = it.resources.configuration.orientation }
        return current
    }

    private fun awaitRenderedFrame() {
        val committed = CountDownLatch(1)
        scenario.onActivity { activity ->
            val root = activity.window.decorView
            val commitFrame = {
                root.viewTreeObserver.registerFrameCommitCallback { committed.countDown() }
                root.invalidate()
            }
            val browser = webView(root)
            if (browser == null) {
                commitFrame()
            } else {
                browser.postVisualStateCallback(0, object : WebView.VisualStateCallback() {
                    override fun onComplete(requestId: Long) { commitFrame() }
                })
            }
        }
        assertTrue("Rendered Android frame did not commit", committed.await(10, TimeUnit.SECONDS))
        instrumentation.waitForIdleSync()
    }

    private fun screenshot(name: String) {
        awaitRenderedFrame()
        captureScreenshot(name)
    }

    private fun screenshotSystemUi(name: String) {
        instrumentation.waitForIdleSync()
        SystemClock.sleep(500)
        captureScreenshot(name)
    }

    private fun captureScreenshot(name: String) {
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

    private fun waitFor(
        description: String,
        timeoutMillis: Long = 30_000L,
        condition: () -> Boolean,
    ) {
        val deadline = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(timeoutMillis)
        while (System.nanoTime() < deadline) {
            if (condition()) return
            Thread.sleep(100)
        }
        captureScreenshot("failure-${description.replace(' ', '-')}")
        throw AssertionError("Timed out waiting for $description")
    }

    private companion object {
        const val SYSTEM_UI_PACKAGE = "com.android.systemui"
    }
}
