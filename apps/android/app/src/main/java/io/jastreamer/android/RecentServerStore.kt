package io.jastreamer.android

import android.content.Context
import java.io.File
import java.io.FileOutputStream
import java.nio.file.Files
import java.nio.file.StandardCopyOption
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.put

class RecentServerStore internal constructor(private val directory: File) {
    constructor(context: Context) : this(context.applicationContext.filesDir)

    private val file = File(directory, "client.json")
    private data class Snapshot(val language: String = "en", val recents: List<ServerEndpoint> = emptyList())

    fun list(): List<ServerEndpoint> = synchronized(fileLock) { readSnapshot().recents }
    fun language(): String = synchronized(fileLock) { readSnapshot().language }

    fun remember(server: ServerEndpoint) = synchronized(fileLock) {
        val normalized = requireValidServer(server)
        val current = readSnapshot()
        val next = buildList {
            add(normalized)
            current.recents.asSequence()
                .filterNot { key(it) == key(normalized) }
                .take(MAX_RECENT_SERVERS - 1)
                .forEach(::add)
        }
        commit(current.copy(recents = next))
    }

    fun remove(server: ServerEndpoint) = synchronized(fileLock) {
        val normalized = requireValidServer(server)
        val current = readSnapshot()
        val next = current.recents.filterNot { key(it) == key(normalized) }
        if (next.size != current.recents.size) commit(current.copy(recents = next))
    }

    fun setLanguage(language: String) = synchronized(fileLock) {
        if (language != "en" && language != "ko") {
            throw storageFailure("Language must be en or ko")
        }
        val current = readSnapshot()
        if (current.language != language) commit(current.copy(language = language))
    }

    private fun readSnapshot(): Snapshot {
        try {
            if (!file.exists()) return Snapshot()
            if (file.length() > MAX_STORE_BYTES) throw storageFailure("Client storage exceeds its size limit")
            val root = Json.parseToJsonElement(file.readText(Charsets.UTF_8)) as? JsonObject
                ?: throw storageFailure("Client storage is invalid")
            val version = (root["version"] as? JsonPrimitive)?.takeUnless { it.isString }?.intOrNull
            val entries = root["recents"] as? JsonArray
            val language = root.string("language")
            if (version != STORE_VERSION || entries == null || language !in listOf("en", "ko")) {
                throw storageFailure("Client storage is invalid")
            }
            val unique = LinkedHashMap<String, ServerEndpoint>()
            for (entry in entries) {
                val server = normalizeStoredServer(entry as? JsonObject) ?: continue
                unique.putIfAbsent(key(server), server)
                if (unique.size == MAX_RECENT_SERVERS) break
            }
            return Snapshot(language!!, unique.values.toList())
        } catch (failure: ClientException) {
            throw failure
        } catch (failure: Exception) {
            throw storageFailure("Unable to read client preferences", failure)
        }
    }

    private fun commit(snapshot: Snapshot) {
        val payload = buildJsonObject {
            put("version", STORE_VERSION)
            put("language", snapshot.language)
            put("recents", buildJsonArray {
                snapshot.recents.forEach { server ->
                    add(buildJsonObject {
                        put("id", server.id)
                        put("name", server.name)
                        put("version", server.version)
                        put("origin", server.origin)
                    })
                }
            })
        }.toString().toByteArray(Charsets.UTF_8)
        var temporary: File? = null
        try {
            temporary = File.createTempFile("client-", ".tmp", directory)
            FileOutputStream(temporary).use { output ->
                output.write(payload)
                output.fd.sync()
            }
            // SharedPreferences.commit() changes its memory map even when disk persistence
            // fails. Read only this atomically replaced file, never an uncommitted map.
            Files.move(temporary.toPath(), file.toPath(), StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING)
        } catch (failure: Exception) {
            throw storageFailure("Unable to persist client preferences", failure)
        } finally {
            temporary?.delete()
        }
    }

    private fun normalizeStoredServer(value: JsonObject?): ServerEndpoint? {
        if (value == null) return null
        val id = value.string("id") ?: return null
        val name = cleanDisplayText(value.string("name"), MAX_NAME_LENGTH) ?: return null
        val version = cleanDisplayText(value.string("version"), MAX_VERSION_LENGTH) ?: return null
        val origin = value.string("origin") ?: return null
        return try {
            ServerEndpoint(
                id = EndpointPolicy.normalizeServerId(id),
                name = name,
                version = version,
                origin = EndpointPolicy.normalizeOrigin(origin),
            )
        } catch (_: ClientException) {
            null
        }
    }

    private fun requireValidServer(server: ServerEndpoint): ServerEndpoint = try {
        ServerEndpoint(
            id = EndpointPolicy.normalizeServerId(server.id),
            name = cleanDisplayText(server.name, MAX_NAME_LENGTH) ?: throw IllegalArgumentException("Invalid server name"),
            version = cleanDisplayText(server.version, MAX_VERSION_LENGTH) ?: throw IllegalArgumentException("Invalid server version"),
            origin = EndpointPolicy.normalizeOrigin(server.origin),
        )
    } catch (failure: Exception) {
        throw ClientException(ClientErrorCode.INVALID_METADATA, "Invalid recent server", failure)
    }

    private fun JsonObject.string(key: String): String? {
        val value = this[key] as? JsonPrimitive ?: return null
        return value.takeIf { it.isString }?.content
    }

    private fun cleanDisplayText(value: String?, maximum: Int): String? {
        val text = value?.trim().orEmpty()
        return text.takeIf { it.isNotEmpty() && it.length <= maximum && it.none(Char::isISOControl) }
    }

    private fun key(server: ServerEndpoint): String = "${server.id}\u0000${server.origin}"
    private fun storageFailure(detail: String, cause: Throwable? = null) = ClientException(ClientErrorCode.STORAGE, detail, cause)

    companion object {
        private val fileLock = Any()
        private const val STORE_VERSION = 1
        private const val MAX_STORE_BYTES = 65_536
        private const val MAX_RECENT_SERVERS = 12
        private const val MAX_NAME_LENGTH = 128
        private const val MAX_VERSION_LENGTH = 64
    }
}
