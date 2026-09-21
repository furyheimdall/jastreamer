package io.jastreamer.android

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class OfflinePlaybackPolicyTest {
    @Test
    fun `queue preserves duplicate tracks as distinct ordered entries`() {
        val ids = ArrayDeque(listOf("entry-a", "entry-b", "entry-c"))
        val queue = OfflinePlaybackPolicy.newQueue(
            trackIds = listOf("track-1", "track-1", "track-2"),
            startIndex = 1,
            entryId = { ids.removeFirst() },
        )

        assertEquals(listOf("track-1", "track-1", "track-2"), queue.entries.map { it.trackId })
        assertEquals(listOf("entry-a", "entry-b", "entry-c"), queue.entries.map { it.id })
        assertEquals("entry-b", queue.currentEntryId)
    }

    @Test
    fun `moving entries retains current identity and playback position`() {
        val original = OfflineQueue(
            entries = listOf(
                OfflineQueueEntry("a", "track-1"),
                OfflineQueueEntry("b", "track-2"),
                OfflineQueueEntry("c", "track-3"),
            ),
            currentEntryId = "b",
            positionMs = 42_000,
            shuffle = true,
            repeatMode = 2,
        )

        val moved = OfflinePlaybackPolicy.move(original, "b", 1)

        assertEquals(listOf("a", "c", "b"), moved.entries.map { it.id })
        assertEquals("b", moved.currentEntryId)
        assertEquals(42_000L, moved.positionMs)
        assertTrue(moved.shuffle)
        assertEquals(2, moved.repeatMode)
    }

    @Test
    fun `removing current chooses next then previous without losing duplicates`() {
        val queue = OfflineQueue(
            entries = listOf(
                OfflineQueueEntry("a", "same-track"),
                OfflineQueueEntry("b", "same-track"),
                OfflineQueueEntry("c", "other-track"),
            ),
            currentEntryId = "b",
            positionMs = 9_000,
        )

        val middleRemoved = OfflinePlaybackPolicy.remove(queue, "b")
        assertEquals(listOf("a", "c"), middleRemoved.entries.map { it.id })
        assertEquals("c", middleRemoved.currentEntryId)
        assertEquals(0L, middleRemoved.positionMs)

        val lastRemoved = OfflinePlaybackPolicy.remove(middleRemoved.copy(currentEntryId = "c"), "c")
        assertEquals("a", lastRemoved.currentEntryId)
    }

    @Test
    fun `decode failover is one bounded pass even with repeat`() {
        assertEquals(listOf(2, 3), OfflinePlaybackPolicy.failoverIndices(4, 1, setOf(1), repeatMode = 0))
        assertEquals(listOf(2, 3, 0), OfflinePlaybackPolicy.failoverIndices(4, 1, setOf(1), repeatMode = 2))
        assertEquals(emptyList<Int>(), OfflinePlaybackPolicy.failoverIndices(3, 1, setOf(0, 1, 2), repeatMode = 2))
    }

    @Test
    fun `only confirmed decode boundaries can skip local music`() {
        assertTrue(OfflinePlaybackPolicy.isConfirmedLocalDecodeError(3001, false))
        assertTrue(OfflinePlaybackPolicy.isConfirmedLocalDecodeError(4005, false))
        assertFalse(OfflinePlaybackPolicy.isConfirmedLocalDecodeError(2000, false))
        assertFalse(OfflinePlaybackPolicy.isConfirmedLocalDecodeError(4003, true))
    }

    @Test
    fun `ownership changes require an explicit cross-owner handoff`() {
        assertFalse(OfflinePlaybackPolicy.requiresHandoff("none", "local"))
        assertFalse(OfflinePlaybackPolicy.requiresHandoff("local", "local"))
        assertTrue(OfflinePlaybackPolicy.requiresHandoff("server", "local"))
        assertTrue(OfflinePlaybackPolicy.requiresHandoff("local", "server"))
    }

    @Test
    fun `ownership and stop generations fence suspended playback acquisition`() {
        val token = OfflinePlaybackPolicy.OperationToken(
            ownershipGeneration = 7,
            commandGeneration = 11,
        )

        assertTrue(OfflinePlaybackPolicy.operationIsCurrent(token, 7, 11))
        assertFalse(OfflinePlaybackPolicy.operationIsCurrent(token, 8, 11))
        assertFalse(OfflinePlaybackPolicy.operationIsCurrent(token, 7, 12))
    }

    @Test
    fun `new facade or system request invalidates suspended work immediately`() {
        val suspendedRequest = OfflinePlaybackRequestFence.beginRequest()
        assertTrue(OfflinePlaybackRequestFence.isCurrent(suspendedRequest))

        val newerRequest = OfflinePlaybackRequestFence.beginRequest()
        assertFalse(OfflinePlaybackRequestFence.isCurrent(suspendedRequest))
        assertTrue(OfflinePlaybackRequestFence.isCurrent(newerRequest))
    }

    @Test
    fun `confirmed disconnect accepts its poll removal but rejects a new local owner`() {
        assertTrue(
            OfflinePlaybackPolicy.confirmedDisconnectCanComplete(
                removalObserved = true,
                currentOwner = OfflinePlaybackPolicy.OWNER_NONE,
                registrationIsMissingOrExpected = true,
            ),
        )
        assertFalse(
            OfflinePlaybackPolicy.confirmedDisconnectCanComplete(
                removalObserved = true,
                currentOwner = OfflinePlaybackPolicy.OWNER_LOCAL,
                registrationIsMissingOrExpected = true,
            ),
        )
        assertFalse(
            OfflinePlaybackPolicy.confirmedDisconnectCanComplete(
                removalObserved = false,
                currentOwner = OfflinePlaybackPolicy.OWNER_NONE,
                registrationIsMissingOrExpected = true,
            ),
        )
        assertFalse(
            OfflinePlaybackPolicy.confirmedDisconnectCanComplete(
                removalObserved = true,
                currentOwner = OfflinePlaybackPolicy.OWNER_NONE,
                registrationIsMissingOrExpected = false,
            ),
        )
    }
}
