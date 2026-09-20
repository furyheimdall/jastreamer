package io.jastreamer.android

import java.net.InetAddress
import java.net.ServerSocket
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.fail
import org.junit.Test

class ServerProbeTest {
    private val server = MockWebServer()

    @After
    fun closeServer() {
        server.close()
    }

    @Test
    fun `rejects redirects without following them`() = runBlocking {
        server.enqueue(
            MockResponse()
                .setResponseCode(302)
                .addHeader("Location", server.url("/somewhere-else")),
        )

        val error = expectClientFailure { ServerProbe().probe(server.url("/").toString()) }

        assertEquals(ClientErrorCode.REDIRECT, error.code)
        assertEquals(false, error.retryable)
        assertEquals(1, server.requestCount)
        assertEquals("/api/v1/discovery", server.takeRequest().path)
    }

    @Test
    fun `rejects oversized and incompatible discovery metadata`() = runBlocking {
        server.enqueue(
            MockResponse()
                .addHeader("Content-Type", "application/json")
                .setBody("x".repeat(32 * 1_024 + 1)),
        )
        server.enqueue(
            MockResponse()
                .addHeader("Content-Type", "application/json")
                .setBody(discoveryJson(product = "other-product")),
        )

        val oversized = expectClientFailure { ServerProbe().probe(server.url("/").toString()) }
        val incompatible = expectClientFailure { ServerProbe().probe(server.url("/").toString()) }

        assertEquals(ClientErrorCode.INVALID_METADATA, oversized.code)
        assertEquals(ClientErrorCode.INCOMPATIBLE_SERVER, incompatible.code)
    }

    @Test
    fun `verified identity cannot change between discovery and connection`() = runBlocking {
        server.enqueue(
            MockResponse()
                .addHeader("Content-Type", "application/json; charset=utf-8")
                .setBody(discoveryJson(id = OTHER_SERVER_ID)),
        )

        val error = expectClientFailure {
            ServerProbe().probe(server.url("/").toString(), expectedId = SERVER_ID)
        }

        assertEquals(ClientErrorCode.IDENTITY_MISMATCH, error.code)
        assertEquals(false, error.retryable)
    }

    @Test
    fun `only transient HTTP discovery failures are retryable`() = runBlocking {
        server.enqueue(MockResponse().setResponseCode(503))
        server.enqueue(MockResponse().setResponseCode(401))

        val unavailable = expectClientFailure { ServerProbe().probe(server.url("/").toString()) }
        val unauthorized = expectClientFailure { ServerProbe().probe(server.url("/").toString()) }

        assertEquals(ClientErrorCode.HTTP_STATUS, unavailable.code)
        assertEquals(true, unavailable.retryable)
        assertEquals(ClientErrorCode.HTTP_STATUS, unauthorized.code)
        assertEquals(false, unauthorized.retryable)
    }

    @Test
    fun `cancelling a probe closes its server connection before the probe timeout`() = runBlocking {
        ServerSocket(0, 1, InetAddress.getByName("127.0.0.1")).use { listener ->
            listener.soTimeout = 2_000
            val job = launch(start = CoroutineStart.UNDISPATCHED) {
                ServerProbe().probe("http://127.0.0.1:${listener.localPort}")
            }
            try {
                listener.accept().use { connection ->
                    connection.soTimeout = 1_500
                    val input = connection.getInputStream().bufferedReader()
                    var line = input.readLine()
                    while (!line.isNullOrEmpty()) line = input.readLine()
                    assertEquals("The request must arrive before cancellation", "", line)
                    job.cancelAndJoin()
                    assertEquals("Cancellation must close the peer socket, not leave it until timeout", -1, input.read())
                }
            } finally {
                job.cancelAndJoin()
            }
        }
    }

    private suspend fun expectClientFailure(block: suspend () -> Unit): ClientException {
        try {
            block()
            fail("Expected ClientException")
        } catch (error: ClientException) {
            return error
        }
        throw AssertionError("unreachable")
    }

    private fun discoveryJson(
        product: String = "jastreamer",
        id: String = SERVER_ID,
    ): String = """
        {
          "product": "$product",
          "protocol": 1,
          "id": "$id",
          "name": "Living room",
          "version": "2.4.0"
        }
    """.trimIndent()

    companion object {
        private const val SERVER_ID = "11111111-1111-4111-8111-111111111111"
        private const val OTHER_SERVER_ID = "22222222-2222-4222-8222-222222222222"
    }
}
