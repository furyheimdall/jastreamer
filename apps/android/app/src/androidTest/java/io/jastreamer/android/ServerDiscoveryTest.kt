package io.jastreamer.android

import android.content.Context
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import java.net.InetAddress
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class ServerDiscoveryTest {
    @Test
    fun nativeNsdPublishesOnlyVerifiedServerAndRemovesLostService(): Unit = runBlocking {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val manager = context.getSystemService(Context.NSD_SERVICE) as NsdManager
        val identity = "c2b9fb5b-1c16-4e18-a237-f06e2f6e5918"
        val registered = CompletableDeferred<Unit>()
        val unregistered = CompletableDeferred<Unit>()
        val listener = object : NsdManager.RegistrationListener {
            override fun onServiceRegistered(info: NsdServiceInfo) { registered.complete(Unit) }
            override fun onRegistrationFailed(info: NsdServiceInfo, code: Int) {
                registered.completeExceptionally(AssertionError("NSD registration failed: $code"))
            }
            override fun onServiceUnregistered(info: NsdServiceInfo) { unregistered.complete(Unit) }
            override fun onUnregistrationFailed(info: NsdServiceInfo, code: Int) {
                unregistered.completeExceptionally(AssertionError("NSD unregister failed: $code"))
            }
        }
        MockWebServer().use { server ->
            server.dispatcher = object : Dispatcher() {
                override fun dispatch(request: RecordedRequest): MockResponse {
                    if (request.method != "GET" || request.path != "/api/v1/discovery") {
                        return MockResponse().setResponseCode(404)
                    }
                    return MockResponse().setHeader("Content-Type", "application/json").setBody(
                        """{"product":"jastreamer","protocol":1,"id":"$identity","name":"HTTP verified name","version":"0.2.0"}""",
                    )
                }
            }
            server.start(InetAddress.getByName("0.0.0.0"), 0)
            val service = NsdServiceInfo().apply {
                serviceName = "Jastreamer-Android-NSD-Smoke"
                serviceType = "_jastreamer._tcp."
                port = server.port
                setAttribute("id", identity)
                setAttribute("name", "Unverified multicast hint")
                setAttribute("version", "0.2.0")
                setAttribute("protocol", "1")
                setAttribute("scheme", "http")
                setAttribute("path", "/")
            }
            manager.registerService(service, NsdManager.PROTOCOL_DNS_SD, listener)
            var published = false
            try {
                withTimeout(20_000) { registered.await() }
                published = true
                val states = MutableStateFlow(DiscoveryState(emptyList()))
                val observer = launch {
                    ServerDiscovery(context, ServerProbe()).observe().collect { states.value = it }
                }
                try {
                    val found = withTimeout(45_000) { states.first { state -> state.servers.any { it.id == identity } } }
                    val endpoint = found.servers.first { it.id == identity }
                    assertEquals("Display name must come from the verified HTTP response", "HTTP verified name", endpoint.name)
                    assertEquals(server.port, java.net.URI(endpoint.origin).port)
                    manager.unregisterService(listener)
                    published = false
                    withTimeout(20_000) { unregistered.await() }
                    withTimeout(45_000) { states.first { state -> state.servers.none { it.id == identity } } }
                } finally {
                    observer.cancelAndJoin()
                }
            } finally {
                if (published) manager.unregisterService(listener)
            }
        }
    }
}
