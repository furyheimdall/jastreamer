package io.jastreamer.android

import java.util.UUID
import java.util.concurrent.atomic.AtomicLong

internal object OfflinePlaybackRequestFence {
    private val generation = AtomicLong()

    fun beginRequest(): Long = generation.incrementAndGet()

    fun isCurrent(requestGeneration: Long): Boolean = generation.get() == requestGeneration
}

internal object OfflinePlaybackPolicy {
    const val OWNER_NONE = "none"
    const val OWNER_SERVER = "server"
    const val OWNER_LOCAL = "local"

    data class OperationToken(val ownershipGeneration: Long, val commandGeneration: Long)
    data class OwnershipRequestToken(val ownershipGeneration: Long, val requestGeneration: Long)

    fun operationIsCurrent(
        token: OperationToken,
        ownershipGeneration: Long,
        commandGeneration: Long,
    ): Boolean =
        token.ownershipGeneration == ownershipGeneration && token.commandGeneration == commandGeneration

    fun confirmedDisconnectCanComplete(
        removalObserved: Boolean,
        currentOwner: String,
        registrationIsMissingOrExpected: Boolean,
    ): Boolean =
        removalObserved && currentOwner != OWNER_LOCAL && registrationIsMissingOrExpected

    fun requiresHandoff(currentOwner: String, requestedOwner: String): Boolean =
        currentOwner != OWNER_NONE && currentOwner != requestedOwner

    fun newQueue(
        trackIds: List<String>,
        startIndex: Int,
        entryId: () -> String = { UUID.randomUUID().toString() },
    ): OfflineQueue {
        require(trackIds.isNotEmpty()) { "At least one track is required" }
        require(startIndex in trackIds.indices) { "Start index is outside the queue" }
        require(trackIds.none(String::isBlank)) { "Track IDs must not be blank" }
        val entries = trackIds.map { OfflineQueueEntry(entryId(), it) }
        require(entries.map(OfflineQueueEntry::id).toSet().size == entries.size) {
            "Queue entry IDs must be unique"
        }
        return OfflineQueue(entries = entries, currentEntryId = entries[startIndex].id)
    }

    fun enqueue(
        queue: OfflineQueue,
        trackIds: List<String>,
        next: Boolean,
        entryId: () -> String = { UUID.randomUUID().toString() },
    ): OfflineQueue {
        require(trackIds.isNotEmpty()) { "At least one track is required" }
        require(trackIds.none(String::isBlank)) { "Track IDs must not be blank" }
        val additions = trackIds.map { OfflineQueueEntry(entryId(), it) }
        val entryIds = queue.entries.mapTo(
            HashSet<String>(queue.entries.size + additions.size),
            OfflineQueueEntry::id,
        )
        require(additions.all { it.id.isNotBlank() && entryIds.add(it.id) }) {
            "Queue entry IDs must be non-empty and unique"
        }
        val insertionIndex = if (!next) {
            queue.entries.size
        } else if (queue.currentEntryId == null) {
            0
        } else {
            val currentIndex = queue.entries.indexOfFirst { it.id == queue.currentEntryId }
            require(currentIndex >= 0) { "The current queue entry is not in the queue" }
            currentIndex + 1
        }
        val entries = ArrayList<OfflineQueueEntry>(queue.entries.size + additions.size).apply {
            addAll(queue.entries.subList(0, insertionIndex))
            addAll(additions)
            addAll(queue.entries.subList(insertionIndex, queue.entries.size))
        }
        return normalized(queue.copy(entries = entries))
    }

    fun normalized(queue: OfflineQueue): OfflineQueue {
        val entries = queue.entries.filter { it.id.isNotBlank() && it.trackId.isNotBlank() }
        val unique = HashSet<String>(entries.size)
        val deduplicatedIds = entries.filter { unique.add(it.id) }
        val current = queue.currentEntryId?.takeIf { id -> deduplicatedIds.any { it.id == id } }
            ?: deduplicatedIds.firstOrNull()?.id
        return queue.copy(
            entries = deduplicatedIds,
            currentEntryId = current,
            positionMs = queue.positionMs.coerceAtLeast(0L),
            repeatMode = queue.repeatMode.takeIf { it in 0..2 } ?: 0,
        )
    }

    fun move(queue: OfflineQueue, entryId: String, offset: Int): OfflineQueue {
        if (offset == 0) return queue
        val from = queue.entries.indexOfFirst { it.id == entryId }
        if (from < 0) return queue
        val to = (from + offset).coerceIn(0, queue.entries.lastIndex)
        if (from == to) return queue
        val entries = queue.entries.toMutableList()
        val entry = entries.removeAt(from)
        entries.add(to, entry)
        return queue.copy(entries = entries)
    }

    fun remove(queue: OfflineQueue, entryId: String): OfflineQueue {
        val index = queue.entries.indexOfFirst { it.id == entryId }
        if (index < 0) return queue
        val entries = queue.entries.toMutableList().also { it.removeAt(index) }
        val current = if (queue.currentEntryId != entryId) {
            queue.currentEntryId
        } else {
            entries.getOrNull(index)?.id ?: entries.getOrNull(index - 1)?.id
        }
        return queue.copy(
            entries = entries,
            currentEntryId = current,
            positionMs = if (queue.currentEntryId == entryId) 0L else queue.positionMs,
        )
    }

    /** A bounded candidate pass. Failed entries are never returned twice. */
    fun failoverIndices(
        size: Int,
        currentIndex: Int,
        failedIndices: Set<Int>,
        repeatMode: Int,
    ): List<Int> {
        if (size <= 1 || currentIndex !in 0 until size) return emptyList()
        val result = ArrayList<Int>(size - 1)
        for (index in currentIndex + 1 until size) {
            if (index !in failedIndices) result += index
        }
        if (repeatMode != 0) {
            for (index in 0 until currentIndex) {
                if (index !in failedIndices) result += index
            }
        }
        return result
    }

    fun isConfirmedLocalDecodeError(errorCode: Int, hasSecurityOrTransportCause: Boolean): Boolean =
        !hasSecurityOrTransportCause && (errorCode in 3001..3004 || errorCode in 4003..4005)
}
