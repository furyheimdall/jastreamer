package io.jastreamer.android

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class OfflineDownloadNetworkPolicyTest {
    @Test
    fun `available unmetered LAN is allowed without public Internet validation`() {
        val state = classifyOfflineNetwork(
            available = true,
            metered = false,
            mobile = false,
            roaming = false,
            meteredAllowed = false,
        )

        assertTrue(state.allowed)
        assertNull(state.errorCode)
    }

    @Test
    fun `metered network requires explicit permission`() {
        val blocked = classifyOfflineNetwork(
            available = true,
            metered = true,
            mobile = false,
            roaming = false,
            meteredAllowed = false,
        )
        val allowed = classifyOfflineNetwork(
            available = true,
            metered = true,
            mobile = false,
            roaming = false,
            meteredAllowed = true,
        )

        assertEquals("network_unmetered_required", blocked.errorCode)
        assertTrue(allowed.allowed)
        assertNull(allowed.errorCode)
    }

    @Test
    fun `unmetered mobile still requires explicit permission`() {
        val state = classifyOfflineNetwork(
            available = true,
            metered = false,
            mobile = true,
            roaming = false,
            meteredAllowed = false,
        )

        assertEquals("network_unmetered_required", state.errorCode)
    }

    @Test
    fun `roaming remains blocked after metered permission`() {
        val state = classifyOfflineNetwork(
            available = true,
            metered = true,
            mobile = false,
            roaming = true,
            meteredAllowed = true,
        )

        assertEquals("network_roaming", state.errorCode)
    }

    @Test
    fun `missing network reports unavailable`() {
        val state = classifyOfflineNetwork(
            available = false,
            metered = false,
            mobile = false,
            roaming = false,
            meteredAllowed = true,
        )

        assertEquals("network_unavailable", state.errorCode)
    }
}
