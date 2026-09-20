package io.jastreamer.android

import android.os.SystemClock
import android.view.View
import android.view.ViewGroup
import android.webkit.WebView
import android.widget.EditText
import android.widget.FrameLayout
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.webkit.WebViewFeature
import java.util.UUID
import java.util.concurrent.ConcurrentLinkedQueue
import java.util.concurrent.CountDownLatch
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicReference
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import org.json.JSONTokener
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class WebViewBoundaryTest {
    private lateinit var scenario: ActivityScenario<MainActivity>
    private val activeView = AtomicReference<RemoteServerView?>()
    private val fixtures = mutableListOf<HttpFixture>()

    @Before
    fun launchActivity() {
        assertTrue(
            "The installed WebView provider must support isolated profiles",
            WebViewFeature.isFeatureSupported(WebViewFeature.MULTI_PROFILE),
        )
        assertTrue(
            "The installed WebView provider must support origin-bound WebMessage listeners",
            WebViewFeature.isFeatureSupported(WebViewFeature.WEB_MESSAGE_LISTENER),
        )
        scenario = ActivityScenario.launch(MainActivity::class.java)
    }

    @After
    fun closeActivity() {
        if (::scenario.isInitialized) {
            scenario.onActivity {
                activeView.getAndSet(null)?.let(::detachAndDispose)
            }
            scenario.close()
        }
        fixtures.forEach(HttpFixture::close)
        fixtures.clear()
    }

    @Test
    fun sameHostDifferentPortsKeepCookiesIsolated() {
        val firstFixture = fixture()
        val secondFixture = fixture()
        val serverId = UUID.randomUUID().toString()

        mount(endpoint(firstFixture.origin, serverId))
        setBoundaryState("first-port")
        unmount()

        mount(endpoint(secondFixture.origin, serverId))
        assertEquals("|", boundaryState())
    }

    @Test
    fun sameOriginDifferentServerIdsKeepCookiesAndDomStorageIsolated() {
        val fixture = fixture()
        val first = endpoint(fixture.origin, UUID.randomUUID().toString())
        val second = endpoint(fixture.origin, UUID.randomUUID().toString())

        mount(first)
        setBoundaryState("first-id")
        unmount()

        mount(second)
        assertEquals("|", boundaryState())
    }

    @Test
    fun sameProfileRetainsCookiesAndDomStorageAcrossViewRecreation() {
        val fixture = fixture()
        val endpoint = endpoint(fixture.origin, UUID.randomUUID().toString())

        mount(endpoint)
        setBoundaryState("retained")
        unmount()

        mount(endpoint)
        assertEquals("retained|retained", boundaryState())
    }

    @Test
    fun latestOverlappingLanguageWriteRemainsAuthoritative() {
        val fixture = fixture()
        val mounted = mount(endpoint(fixture.origin, UUID.randomUUID().toString()))

        scenario.onActivity {
            activeView.get()?.apply {
                setLanguage("ko")
                setLanguage("en")
            }
        }

        assertEquals(true, mounted.loads.poll(10, TimeUnit.SECONDS))
        assertTrue("Unexpected language write failure: ${mounted.errors.peek()?.detail}", mounted.errors.isEmpty())
        assertEquals("en", evaluate(
            "document.cookie.split(';').map(function(v) { return v.trim(); })" +
                ".find(function(v) { return v.indexOf('jastreamer_language=') === 0; })" +
                ".substring('jastreamer_language='.length)",
        ))
    }

    @Test
    fun crossOriginSubresourcesAndNavigationNeverReachHostileServer() {
        val trusted = fixture()
        val hostile = fixture()
        val mounted = mount(endpoint(trusted.origin, UUID.randomUUID().toString()))

        evaluate(
            """
            (function() {
              fetch('${hostile.origin}/stolen').catch(function() {});
              return 'requested';
            })()
            """.trimIndent(),
        )
        SystemClock.sleep(500)
        assertEquals(0, hostile.server.requestCount)

        assertEquals(
            "scheduled",
            evaluate(
                "setTimeout(function() { location.href = '${hostile.origin}/escape'; }, 0); 'scheduled'",
            ),
        )
        val failure = mounted.errors.poll(5, TimeUnit.SECONDS)
        assertNotNull("Blocked navigation did not report an error", failure)
        assertEquals(ClientErrorCode.NAVIGATION_BLOCKED, failure?.code)
        assertEquals(trusted.origin, evaluate("location.origin"))
        assertEquals(0, hostile.server.requestCount)
    }

    @Test
    fun pauseResumeAndDisposeDoNotSendPlaybackMutations() {
        val fixture = fixture()
        mount(endpoint(fixture.origin, UUID.randomUUID().toString()))
        fixture.requests.clear()

        scenario.onActivity {
            activeView.get()?.apply {
                pause()
                resume()
                pause()
                resume()
            }
        }
        unmount()
        SystemClock.sleep(500)

        assertFalse(
            "Lifecycle operations sent an HTTP mutation: ${fixture.requests}",
            fixture.requests.any { request -> request.method != "GET" && request.method != "HEAD" },
        )
    }

    @Test
    fun nativePlaybackBridgeIsMainFrameOnlyTypedAndCredentialFree() {
        val fixture = fixture()
        mount(endpoint(fixture.origin, UUID.randomUUID().toString()))

        scenario.onActivity {
            val settings = (activeView.get()?.getChildAt(0) as WebView).settings
            assertFalse(settings.allowFileAccess)
            assertFalse(settings.allowContentAccess)
            assertFalse(settings.allowFileAccessFromFileURLs)
            assertFalse(settings.allowUniversalAccessFromFileURLs)
            assertFalse(settings.javaScriptCanOpenWindowsAutomatically)
        }

        assertEquals("object", evaluate("typeof window.JastreamerAndroidAudio"))
        assertEquals(
            true,
            evaluate(
                """
                (() => {
                  const bridge = window.JastreamerAndroidAudio;
                  const exposed = Object.keys(bridge).sort();
                  return typeof bridge.postMessage === 'function' &&
                    typeof bridge.addEventListener === 'function' &&
                    !('getCookie' in bridge) && !('fetch' in bridge) &&
                    !('evaluateJavascript' in bridge) && !('openFile' in bridge) &&
                    !('startActivity' in bridge);
                })()
                """.trimIndent(),
            ),
        )

        evaluate(
            """
            window.nativeBridgeReplies = [];
            JastreamerAndroidAudio.addEventListener('message', event => nativeBridgeReplies.push(event.data));
            JastreamerAndroidAudio.postMessage(JSON.stringify({id:'status-1', action:'status'}));
            'sent';
            """.trimIndent(),
        )
        waitFor("top-frame native status response") {
            evaluate("String(window.nativeBridgeReplies && window.nativeBridgeReplies.length)") == "1"
        }
        assertEquals(
            true,
            evaluate(
                """
                (() => {
                  const response = JSON.parse(nativeBridgeReplies[0]);
                  return response.id === 'status-1' && response.device === null &&
                    !JSON.stringify(response).includes('owner_token') &&
                    Object.keys(response).every(key => ['id','device','recovering','error'].includes(key));
                })()
                """.trimIndent(),
            ),
        )

        evaluate(
            """
            JastreamerAndroidAudio.postMessage(JSON.stringify({
              id:'invalid-1', action:'fetch', url:'/api/v1/session'
            }));
            const frame = document.createElement('iframe');
            frame.onload = () => {
              const bridge = frame.contentWindow.JastreamerAndroidAudio;
              window.frameBridgeAttempted = typeof bridge === 'object';
              window.frameBridgeReplies = 0;
              bridge.addEventListener('message', () => window.frameBridgeReplies++);
              bridge.postMessage(JSON.stringify({id:'frame-status', action:'status'}));
            };
            frame.src = '/';
            document.body.appendChild(frame);
            'sent';
            """.trimIndent(),
        )
        waitFor("typed rejection and frame bridge attempt") {
            evaluate(
                "String(nativeBridgeReplies.length >= 2 && window.frameBridgeAttempted === true)",
            ) == "true"
        }
        SystemClock.sleep(500)
        assertEquals("0", evaluate("String(window.frameBridgeReplies)"))
        assertEquals(
            true,
            evaluate(
                """
                (() => {
                  const response = JSON.parse(nativeBridgeReplies[1]);
                  return response.id === 'invalid-1' && response.device === null &&
                    response.error.code === 'invalid_request' &&
                    !JSON.stringify(response).includes('owner_token');
                })()
                """.trimIndent(),
            ),
        )
    }

    @Test
    fun navigationCancelsAConnectFromTheStaleDocument() {
        val fixture = fixture()
        val id = UUID.randomUUID().toString()
        fixture.discoveryId = id
        val mounted = mount(endpoint(fixture.origin, id))
        fixture.requests.clear()
        fixture.blockDiscovery = true

        assertEquals(
            "sent",
            evaluate(
                """
                JastreamerAndroidAudio.postMessage(JSON.stringify({
                  id:'stale-connect', action:'connect', name:'Stale document'
                }));
                'sent';
                """.trimIndent(),
            ),
        )
        assertTrue(
            "The stale-document Connect must reach its identity probe before navigation",
            fixture.discoveryEntered.await(5, TimeUnit.SECONDS),
        )
        scenario.onActivity {
            val remote = requireNotNull(activeView.get())
            (remote.getChildAt(0) as WebView).reload()
        }
        assertEquals(true, mounted.loads.poll(10, TimeUnit.SECONDS))
        fixture.discoveryRelease.countDown()
        assertTrue(
            "The canceled identity probe did not leave the fixture",
            fixture.discoveryFinished.await(5, TimeUnit.SECONDS),
        )
        assertFalse(
            "A Connect canceled by navigation must not reach registration",
            fixture.registrationReceived.await(1, TimeUnit.SECONDS),
        )
        assertFalse(
            "A Connect canceled by navigation produced a registration request",
            fixture.requests.any {
                it.method == "POST" && it.path.startsWith("/api/v1/browser-output/registrations")
            },
        )
    }

    @Test
    fun pauseCancelsAPendingConnectAndResumeDoesNotResurrectIt() {
        val fixture = fixture()
        val id = UUID.randomUUID().toString()
        fixture.discoveryId = id
        mount(endpoint(fixture.origin, id))
        fixture.requests.clear()
        fixture.blockDiscovery = true

        assertEquals(
            "sent",
            evaluate(
                """
                JastreamerAndroidAudio.postMessage(JSON.stringify({
                  id:'paused-connect', action:'connect', name:'Paused document'
                }));
                'sent';
                """.trimIndent(),
            ),
        )
        assertTrue(
            "Connect must be pending in its identity probe before the view pauses",
            fixture.discoveryEntered.await(5, TimeUnit.SECONDS),
        )
        scenario.onActivity {
            requireNotNull(activeView.get()).apply {
                pause()
                resume()
            }
        }
        fixture.discoveryRelease.countDown()
        assertTrue(
            "The pause-canceled identity probe did not leave the fixture",
            fixture.discoveryFinished.await(5, TimeUnit.SECONDS),
        )
        assertFalse(
            "Resuming a view must not resurrect the Connect canceled while pausing",
            fixture.registrationReceived.await(1, TimeUnit.SECONDS),
        )
        assertFalse(
            "A pause-canceled Connect produced a registration request",
            fixture.requests.any {
                it.method == "POST" && it.path.startsWith("/api/v1/browser-output/registrations")
            },
        )
    }

    @Test
    fun failedRootReloadsAfterBackgroundWhileHealthyRootOnlyRevalidates() {
        val fixture = fixture()
        fixture.blockRoot = true
        fixture.rootResponseCode = 503

        scenario.onActivity { activity ->
            activity.findViewById<EditText>(R.id.server_address).setText(fixture.origin)
            activity.findViewById<View>(R.id.connect_button).performClick()
        }
        assertTrue(
            "The failing root document was never requested",
            fixture.rootRequested.await(10, TimeUnit.SECONDS),
        )
        scenario.moveToState(androidx.lifecycle.Lifecycle.State.CREATED)
        fixture.rootRelease.countDown()
        assertTrue(
            "The failed root response did not leave the fixture after backgrounding",
            fixture.rootFinished.await(5, TimeUnit.SECONDS),
        )
        val failedRootRequests = fixture.rootRequestCount()
        fixture.rootResponseCode = 200

        scenario.moveToState(androidx.lifecycle.Lifecycle.State.RESUMED)
        waitFor("a successful root reload after the failed page resumes") {
            fixture.rootRequestCount() > failedRootRequests &&
                evaluateActivity("document.body.textContent.trim()") == "ready"
        }
        val healthyRootRequests = fixture.rootRequestCount()
        val discoveryRequests = fixture.discoveryRequestCount()

        scenario.moveToState(androidx.lifecycle.Lifecycle.State.CREATED)
        scenario.moveToState(androidx.lifecycle.Lifecycle.State.RESUMED)
        waitFor("healthy page identity revalidation after backgrounding") {
            fixture.discoveryRequestCount() > discoveryRequests
        }
        SystemClock.sleep(500)
        assertEquals(
            "A healthy loaded document must survive pause/resume without a root reload",
            healthyRootRequests,
            fixture.rootRequestCount(),
        )
        assertEquals("ready", evaluateActivity("document.body.textContent.trim()"))
    }

    private fun waitFor(description: String, condition: () -> Boolean) {
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(10)
        while (System.nanoTime() < deadline) {
            if (condition()) return
            SystemClock.sleep(50)
        }
        throw AssertionError("Timed out waiting for $description")
    }

    private fun fixture(): HttpFixture = HttpFixture().also(fixtures::add)

    private fun mount(server: ServerEndpoint): MountedView {
        val completed = CountDownLatch(1)
        val loaded = AtomicBoolean(false)
        val errors = LinkedBlockingQueue<ClientException>()
        val loads = LinkedBlockingQueue<Boolean>()

        scenario.onActivity { activity ->
            activeView.getAndSet(null)?.let(::detachAndDispose)
            val view = RemoteServerView(
                activity,
                server,
                "en",
                canStartNativePlayback = { true },
                onLoaded = {
                    loads.offer(true)
                    loaded.set(true)
                    completed.countDown()
                },
                onError = { failure ->
                    errors.offer(failure)
                    completed.countDown()
                },
                onLanguageChanged = {},
            )
            val container = activity.findViewById<FrameLayout>(R.id.remote_container)
            container.addView(view, FrameLayout.LayoutParams(-1, -1))
            activeView.set(view)
            view.load()
            view.resume()
        }

        assertTrue("Timed out waiting for fixture page", completed.await(15, TimeUnit.SECONDS))
        assertTrue("Fixture page failed to load: ${errors.peek()?.detail}", loaded.get())
        errors.clear()
        loads.clear()
        return MountedView(errors, loads)
    }

    private fun unmount() {
        scenario.onActivity {
            activeView.getAndSet(null)?.let(::detachAndDispose)
        }
    }

    private fun detachAndDispose(view: RemoteServerView) {
        (view.parent as? ViewGroup)?.removeView(view)
        view.dispose()
    }

    private fun setBoundaryState(value: String) {
        val result = evaluate(
            """
            (function() {
              document.cookie = 'boundary=$value; Path=/; Max-Age=31536000; SameSite=Strict';
              localStorage.setItem('boundary', '$value');
              return 'stored';
            })()
            """.trimIndent(),
        )
        assertEquals("stored", result)
    }

    private fun boundaryState(): String? = evaluate(
        """
        (function() {
          var cookie = document.cookie.split(';').map(function(value) { return value.trim(); })
            .find(function(value) { return value.indexOf('boundary=') === 0; });
          return (cookie ? cookie.substring('boundary='.length) : '') + '|' +
            (localStorage.getItem('boundary') || '');
        })()
        """.trimIndent(),
    ) as? String

    private fun evaluate(script: String): Any? {
        val completed = CountDownLatch(1)
        val encoded = AtomicReference<String?>()
        scenario.onActivity {
            val remote = activeView.get() ?: error("No RemoteServerView is mounted")
            val webView = remote.getChildAt(0) as WebView
            webView.evaluateJavascript(script) { result ->
                encoded.set(result)
                completed.countDown()
            }
        }
        assertTrue("Timed out evaluating fixture JavaScript", completed.await(5, TimeUnit.SECONDS))
        return decodeJavascriptResult(encoded.get())
    }

    private fun evaluateActivity(script: String): Any? {
        val completed = CountDownLatch(1)
        val encoded = AtomicReference<String?>()
        scenario.onActivity { activity ->
            val container = activity.findViewById<FrameLayout>(R.id.remote_container)
            val remote = (0 until container.childCount)
                .map(container::getChildAt)
                .filterIsInstance<RemoteServerView>()
                .singleOrNull()
            val webView = remote?.getChildAt(0) as? WebView
            if (webView == null) {
                completed.countDown()
            } else {
                webView.evaluateJavascript(script) { result ->
                    encoded.set(result)
                    completed.countDown()
                }
            }
        }
        assertTrue("Timed out evaluating Activity WebView JavaScript", completed.await(5, TimeUnit.SECONDS))
        return decodeJavascriptResult(encoded.get())
    }

    private fun decodeJavascriptResult(encoded: String?): Any? =
        encoded?.takeUnless { it == "null" }?.let { JSONTokener(it).nextValue() }

    private fun endpoint(origin: String, id: String) = ServerEndpoint(
        id = id,
        name = "WebView fixture",
        version = "test",
        origin = origin,
    )

    private data class MountedView(
        val errors: LinkedBlockingQueue<ClientException>,
        val loads: LinkedBlockingQueue<Boolean>,
    )

    private data class SeenRequest(val method: String, val path: String)

    private class HttpFixture : AutoCloseable {
        val requests = ConcurrentLinkedQueue<SeenRequest>()
        val discoveryEntered = CountDownLatch(1)
        val discoveryRelease = CountDownLatch(1)
        val discoveryFinished = CountDownLatch(1)
        val rootRequested = CountDownLatch(1)
        val rootRelease = CountDownLatch(1)
        val rootFinished = CountDownLatch(1)
        val registrationReceived = CountDownLatch(1)
        @Volatile var blockDiscovery = false
        @Volatile var blockRoot = false
        @Volatile var discoveryId = UUID.randomUUID().toString()
        @Volatile var rootResponseCode = 200
        val server = MockWebServer()
        val origin: String

        init {
            server.dispatcher = object : Dispatcher() {
                override fun dispatch(request: RecordedRequest): MockResponse {
                    requests.add(SeenRequest(request.method ?: "", request.path ?: ""))
                    if (
                        request.method == "POST" &&
                        request.path?.startsWith("/api/v1/browser-output/registrations") == true
                    ) {
                        registrationReceived.countDown()
                    }
                    if (request.path == "/api/v1/discovery") {
                        if (blockDiscovery) {
                            discoveryEntered.countDown()
                            try {
                                discoveryRelease.await(10, TimeUnit.SECONDS)
                            } finally {
                                discoveryFinished.countDown()
                            }
                        }
                        return MockResponse()
                            .setResponseCode(200)
                            .setHeader("Content-Type", "application/json")
                            .setBody(
                                """{"product":"jastreamer","protocol":1,"id":"$discoveryId","name":"Fixture","version":"test"}""",
                            )
                    }
                    return if (request.path == "/") {
                        val responseCode = rootResponseCode
                        rootRequested.countDown()
                        if (blockRoot) {
                            try {
                                rootRelease.await(10, TimeUnit.SECONDS)
                            } finally {
                                rootFinished.countDown()
                            }
                        }
                        MockResponse()
                            .setResponseCode(responseCode)
                            .setHeader("Content-Type", "text/html; charset=utf-8")
                            .setBody(
                                """
                                <!doctype html>
                                <html><head><meta name="viewport" content="width=device-width"></head>
                                <body>${if (responseCode == 200) "ready" else "unavailable"}</body></html>
                                """.trimIndent(),
                            )
                    } else {
                        MockResponse().setResponseCode(204)
                    }
                }
            }
            server.start()
            val url = server.url("/")
            origin = "${url.scheme}://${url.host}:${url.port}"
        }

        fun rootRequestCount(): Int = requests.count { it.method == "GET" && it.path == "/" }

        fun discoveryRequestCount(): Int =
            requests.count { it.method == "GET" && it.path == "/api/v1/discovery" }

        override fun close() {
            discoveryRelease.countDown()
            rootRelease.countDown()
            server.shutdown()
        }
    }
}
