package io.jastreamer.android

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

class EndpointPolicyTest {
    @Test
    fun `normalizes only credential-free HTTP root origins`() {
        assertEquals("http://media-box.local:8080", EndpointPolicy.normalize("media-box.local:8080"))
        assertEquals("https://media-box.local", EndpointPolicy.normalize("HTTPS://MEDIA-BOX.LOCAL:443/"))
        assertEquals("http://[2001:db8::1]", EndpointPolicy.normalize("http://[2001:0DB8:0:0:0:0:0:1]:80"))

        listOf(
            " ftp://media-box.local",
            "ftp://media-box.local",
            "http://user:secret@media-box.local",
            "http://media-box.local/api",
            "http://media-box.local/a/../",
            "http://media-box.local?next=other",
            "http://media-box.local#section",
            "http://2001:db8::1",
            "http://bad_host.local",
        ).forEach { address ->
            assertThrows(ClientException::class.java) { EndpointPolicy.normalize(address) }
        }
    }

    @Test
    fun `same origin comparison includes scheme host and effective port`() {
        assertTrue(
            EndpointPolicy.sameOrigin(
                "https://MEDIA-BOX.local:443/library/42?view=grid",
                "https://media-box.local",
            ),
        )
        assertFalse(EndpointPolicy.sameOrigin("http://media-box.local/library", "https://media-box.local"))
        assertFalse(EndpointPolicy.sameOrigin("https://media-box.local:8443/library", "https://media-box.local"))
        assertFalse(EndpointPolicy.sameOrigin("https://media-box.local.evil/library", "https://media-box.local"))
        assertFalse(EndpointPolicy.sameOrigin("https://user@media-box.local/library", "https://media-box.local"))
    }

    @Test
    fun `profile identity binds strict UUID and normalized origin`() {
        val server = ServerEndpoint(
            id = "11111111-1111-4111-8111-111111111111",
            name = "Living room",
            version = "2.4.0",
            origin = "https://MEDIA-BOX.LOCAL:443/",
        )
        val equivalent = server.copy(origin = "https://media-box.local")
        val otherId = server.copy(id = "22222222-2222-4222-8222-222222222222")
        val otherOrigin = server.copy(origin = "https://media-box.local:8443")

        assertEquals(EndpointPolicy.profileName(server), EndpointPolicy.profileName(equivalent))
        assertNotEquals(EndpointPolicy.profileName(server), EndpointPolicy.profileName(otherId))
        assertNotEquals(EndpointPolicy.profileName(server), EndpointPolicy.profileName(otherOrigin))
        assertThrows(ClientException::class.java) {
            EndpointPolicy.normalizeServerId(" 11111111-1111-4111-8111-111111111111 ")
        }
    }
}
