package io.jastreamer.android

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

class NativePlaybackSecurityTest {
    private val origin = "https://media-box.local:8443"

    @Test
    fun `media grants stay on the verified origin and media path`() {
        assertEquals(
            "$origin/media/grant-id/track.mp3",
            NativePlaybackPolicy.mediaUrl(origin, "/media/grant-id/track.mp3")?.toString(),
        )

        listOf(
            "https://other.local:8443/media/grant-id/track.mp3",
            "http://media-box.local:8443/media/grant-id/track.mp3",
            "https://user@media-box.local:8443/media/grant-id/track.mp3",
            "/media/grant-id/track.mp3?token=leak",
            "/media/grant-id/track.mp3#fragment",
            "/media/../api/v1/player",
            "/api/v1/player",
            "//other.local/media/grant-id/track.mp3",
        ).forEach { value ->
            assertNull(value, NativePlaybackPolicy.mediaUrl(origin, value))
        }
    }

    @Test
    fun `profile identity includes both UUID and canonical origin`() {
        val first = ServerEndpoint(
            id = "11111111-1111-4111-8111-111111111111",
            name = "Server",
            version = "1",
            origin = "https://MEDIA-BOX.local:8443/",
        )
        val equivalent = first.copy(origin = origin)
        val otherProfile = first.copy(id = "22222222-2222-4222-8222-222222222222")

        assertEquals(NativePlaybackPolicy.serverKey(first), NativePlaybackPolicy.serverKey(equivalent))
        assertTrue(NativePlaybackPolicy.serverKey(first) != NativePlaybackPolicy.serverKey(otherProfile))
    }

    @Test
    fun `output names are bounded by UTF-8 bytes and reject controls`() {
        assertEquals("Phone", NativePlaybackPolicy.requireName("  Phone  "))
        assertEquals("가".repeat(26), NativePlaybackPolicy.requireName("가".repeat(26)))
        assertThrows(NativePlaybackException::class.java) { NativePlaybackPolicy.requireName("") }
        assertThrows(NativePlaybackException::class.java) { NativePlaybackPolicy.requireName("가".repeat(27)) }
        assertThrows(NativePlaybackException::class.java) { NativePlaybackPolicy.requireName("phone\noutput") }
    }
}
