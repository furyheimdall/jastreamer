package io.jastreamer.android

import android.os.SystemClock
import android.view.ViewGroup
import android.webkit.WebView
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
    )

    private fun evaluate(script: String): String? {
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
        val result = encoded.get() ?: return null
        if (result == "null") return null
        return JSONTokener(result).nextValue() as? String
    }

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
        val server = MockWebServer()
        val origin: String

        init {
            server.dispatcher = object : Dispatcher() {
                override fun dispatch(request: RecordedRequest): MockResponse {
                    requests.add(SeenRequest(request.method ?: "", request.path ?: ""))
                    return if (request.path == "/") {
                        MockResponse()
                            .setResponseCode(200)
                            .setHeader("Content-Type", "text/html; charset=utf-8")
                            .setBody(
                                """
                                <!doctype html>
                                <html><head><meta name="viewport" content="width=device-width"></head>
                                <body>ready</body></html>
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

        override fun close() {
            server.shutdown()
        }
    }
}
