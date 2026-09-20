package io.jastreamer.android

data class ServerEndpoint(
    val id: String,
    val name: String,
    val version: String,
    val origin: String,
)

enum class ClientErrorCode {
    INVALID_ENDPOINT,
    UNREACHABLE,
    TIMEOUT,
    TLS,
    REDIRECT,
    HTTP_STATUS,
    INVALID_METADATA,
    INCOMPATIBLE_SERVER,
    IDENTITY_MISMATCH,
    STORAGE,
    DISCOVERY,
    WEBVIEW_UNSUPPORTED,
    WEB_LOAD,
    NAVIGATION_BLOCKED,
}

class ClientException(
    val code: ClientErrorCode,
    val detail: String? = null,
    cause: Throwable? = null,
    val retryable: Boolean = code == ClientErrorCode.UNREACHABLE ||
        code == ClientErrorCode.TIMEOUT ||
        code == ClientErrorCode.WEB_LOAD,
) : Exception(detail ?: code.name, cause)

data class DiscoveryState(
    val servers: List<ServerEndpoint>,
    val error: ClientException? = null,
)
