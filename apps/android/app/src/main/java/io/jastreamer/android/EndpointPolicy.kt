package io.jastreamer.android

import java.nio.charset.StandardCharsets
import java.security.MessageDigest
import java.util.Locale
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

object EndpointPolicy {
    private const val MAX_ENDPOINT_LENGTH = 2_048
    private val explicitScheme = Regex("^[A-Za-z][A-Za-z0-9+.-]*://")
    private val uuid = Regex("^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")
    private val dnsLabel = Regex("^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$")

    fun normalize(input: String): String {
        val trimmed = input.trim()
        if (trimmed.isEmpty() || trimmed.length > MAX_ENDPOINT_LENGTH || trimmed.any(::isForbiddenCharacter)) {
            invalid("Invalid server address")
        }

        val candidate = if (explicitScheme.containsMatchIn(trimmed)) trimmed else "http://$trimmed"
        val address = candidate.substringAfter("://")
        val firstSlash = address.indexOf('/')
        val rawPath = if (firstSlash >= 0) address.substring(firstSlash) else ""
        if ((rawPath.isNotEmpty() && rawPath != "/") || '?' in candidate || '#' in candidate) {
            invalid("The server address must be an origin without a path, query, or fragment")
        }
        val parsed = candidate.toHttpUrlOrNull() ?: invalid("Invalid server address")
        if (parsed.scheme != "http" && parsed.scheme != "https") invalid("Only HTTP and HTTPS are supported")
        if (parsed.encodedPath != "/" || parsed.query != null || parsed.fragment != null) {
            invalid("The server address must be an origin without a path, query, or fragment")
        }

        val authority = if (firstSlash >= 0) address.substring(0, firstSlash) else address
        if ('@' in authority || parsed.encodedUsername.isNotEmpty() || parsed.encodedPassword.isNotEmpty()) {
            invalid("Credentials are not allowed in a server address")
        }

        val host = parsed.host.removeSuffix(".").lowercase(Locale.US)
        if (host.isEmpty() || !isSupportedHost(host)) invalid("Invalid server host")

        val formattedHost = if (':' in host) "[$host]" else host
        val port = when {
            parsed.scheme == "http" && parsed.port == 80 -> ""
            parsed.scheme == "https" && parsed.port == 443 -> ""
            else -> ":${parsed.port}"
        }
        return "${parsed.scheme}://$formattedHost$port"
    }
    internal fun normalizeOrigin(input: String): String {
        if (!explicitScheme.containsMatchIn(input)) invalid("The server origin must include its scheme")
        return normalize(input)
    }


    fun normalizeServerId(input: String): String {
        val normalized = input.lowercase(Locale.US)
        if (!uuid.matches(normalized)) invalid("Invalid server identity")
        return normalized
    }

    fun sameOrigin(url: String, origin: String): Boolean {
        if (url.isEmpty() || url.any(::isForbiddenCharacter)) return false
        val target = url.toHttpUrlOrNull() ?: return false
        if (target.scheme != "http" && target.scheme != "https") return false
        val targetAuthority = url.substringAfter("://")
            .substringBefore('/')
            .substringBefore('?')
            .substringBefore('#')
        if ('@' in targetAuthority) return false
        if (target.encodedUsername.isNotEmpty() || target.encodedPassword.isNotEmpty()) return false

        val normalizedOrigin = try {
            normalizeOrigin(origin)
        } catch (_: ClientException) {
            return false
        }
        val expected = normalizedOrigin.toHttpUrlOrNull() ?: return false
        return target.scheme == expected.scheme && target.host == expected.host && target.port == expected.port
    }

    fun profileName(server: ServerEndpoint): String {
        val id = normalizeServerId(server.id)
        val origin = normalizeOrigin(server.origin)
        val bytes = MessageDigest.getInstance("SHA-256")
            .digest("$id\u0000$origin".toByteArray(StandardCharsets.UTF_8))
        val hex = CharArray(bytes.size * 2)
        bytes.forEachIndexed { index, byte ->
            val value = byte.toInt() and 0xff
            hex[index * 2] = HEX[value ushr 4]
            hex[index * 2 + 1] = HEX[value and 0x0f]
        }
        return "jastreamer-${String(hex)}"
    }

    private fun isSupportedHost(host: String): Boolean {
        if (':' in host) return true // OkHttp has already parsed and canonicalized the IPv6 literal.
        if (host.all { it.isDigit() || it == '.' }) return isIpv4(host)
        if (host.length > 253) return false
        return host.split('.').all { label -> dnsLabel.matches(label) }
    }

    private fun isIpv4(host: String): Boolean {
        val parts = host.split('.')
        return parts.size == 4 && parts.all { part ->
            part.isNotEmpty() && part.length <= 3 && part.all(Char::isDigit) &&
                (part.length == 1 || part[0] != '0') && part.toInt() in 0..255
        }
    }

    private fun isForbiddenCharacter(character: Char): Boolean =
        character == '\\' || character.isWhitespace() || character.code <= 0x20 ||
            character.code == 0x7f || character.isISOControl()

    private fun invalid(detail: String): Nothing =
        throw ClientException(ClientErrorCode.INVALID_ENDPOINT, detail)
    private const val HEX = "0123456789abcdef"
}
