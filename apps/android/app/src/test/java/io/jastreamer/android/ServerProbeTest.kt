package io.jastreamer.android

import java.util.concurrent.TimeUnit
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.SocketPolicy
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
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
    }

    @Test
    fun `caller cancellation cancels an in-flight network call`() = runBlocking {
        server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.NO_RESPONSE))
        val job = launch {
            ServerProbe().probe(server.url("/").toString())
        }

        assertNotNull(server.takeRequest(1, TimeUnit.SECONDS))
        job.cancelAndJoin()

        assertTrue(job.isCancelled)
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
