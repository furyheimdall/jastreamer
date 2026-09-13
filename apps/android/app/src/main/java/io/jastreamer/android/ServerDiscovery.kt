package io.jastreamer.android

import android.content.Context
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import android.net.wifi.WifiManager
import android.os.Build
import android.os.Handler
import android.os.Looper
import java.net.Inet4Address
import java.net.Inet6Address
import java.net.InetAddress
import java.nio.ByteBuffer
import java.nio.charset.CodingErrorAction
import java.nio.charset.StandardCharsets
import java.util.Locale
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executor
import java.util.concurrent.atomic.AtomicBoolean
import kotlinx.coroutines.CancellableContinuation
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.channels.awaitClose
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.buffer
import kotlinx.coroutines.flow.callbackFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.sync.withPermit
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withContext

class ServerDiscovery(
    context: Context,
    private val probe: ServerProbe,
) {
    private val applicationContext = context.applicationContext

    fun observe(): Flow<DiscoveryState> = callbackFlow {
        val session = DiscoverySession(applicationContext, probe) { state ->
            trySend(state)
        }
        withContext(Dispatchers.Main.immediate) {
            session.start()
        }
        awaitClose { session.close() }
    }.buffer(Channel.CONFLATED)

    private class DiscoverySession(
        context: Context,
        private val probe: ServerProbe,
        private val emit: (DiscoveryState) -> Unit,
    ) {
        private val nsdManager = context.getSystemService(NsdManager::class.java)
        private val wifiManager = context.applicationContext.getSystemService(WifiManager::class.java)
        private val mainHandler = Handler(Looper.getMainLooper())
        private val mainExecutor = Executor { command -> mainHandler.post(command) }
        private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
        private val active = AtomicBoolean(true)
        private val resolveMutex = Mutex()
        private val probeSemaphore = Semaphore(MAX_PARALLEL_PROBES)
        private val candidates = LinkedHashMap<String, Candidate>()
        private val candidateErrors = LinkedHashMap<String, ClientException>()
        private var discoveryError: ClientException? = null
        private var generation = 0L
        private var discoveryRequested = false
        private var multicastLock: WifiManager.MulticastLock? = null

        private val discoveryListener = object : NsdManager.DiscoveryListener {
            override fun onDiscoveryStarted(serviceType: String) = onMain {
                discoveryRequested = true
                discoveryError = null
                emitState()
            }

            override fun onServiceFound(serviceInfo: NsdServiceInfo) = onMain {
                acceptService(serviceInfo)
            }

            override fun onServiceLost(serviceInfo: NsdServiceInfo) = onMain {
                removeService(serviceKey(serviceInfo))
            }

            override fun onDiscoveryStopped(serviceType: String) = onMain {
                discoveryRequested = false
                releaseMulticastLock()
                discoveryError = ClientException(ClientErrorCode.DISCOVERY, "LAN discovery stopped")
                emitState()
            }

            override fun onStartDiscoveryFailed(serviceType: String, errorCode: Int) = onMain {
                discoveryRequested = false
                releaseMulticastLock()
                discoveryError = discoveryFailure("NSD start failed", errorCode)
                emitState()
            }

            override fun onStopDiscoveryFailed(serviceType: String, errorCode: Int) = onMain {
                discoveryRequested = false
                if (active.get()) {
                    discoveryError = discoveryFailure("NSD stop failed", errorCode)
                    emitState()
                }
            }
        }

        fun start() {
            if (!active.get()) return
            acquireMulticastLock()
            discoveryRequested = true
            try {
                nsdManager.discoverServices(
                    SERVICE_TYPE,
                    NsdManager.PROTOCOL_DNS_SD,
                    discoveryListener,
                )
                emitState()
            } catch (error: RuntimeException) {
                discoveryRequested = false
                releaseMulticastLock()
                discoveryError = ClientException(
                    ClientErrorCode.DISCOVERY,
                    "Unable to start LAN discovery",
                    error,
                )
                emitState()
            }
        }

        fun close() {
            if (!active.compareAndSet(true, false)) return
            scope.cancel()
            if (Looper.myLooper() == Looper.getMainLooper()) {
                cleanup()
                return
            }

            val finished = CountDownLatch(1)
            val posted = mainHandler.post {
                try {
                    cleanup()
                } finally {
                    finished.countDown()
                }
            }
            if (!posted) {
                cleanup()
                return
            }
            try {
                finished.await()
            } catch (_: InterruptedException) {
                Thread.currentThread().interrupt()
            }
        }

        private fun cleanup() {
            candidates.values.forEach { it.job?.cancel() }
            candidates.clear()
            candidateErrors.clear()
            if (discoveryRequested) {
                try {
                    nsdManager.stopServiceDiscovery(discoveryListener)
                } catch (_: IllegalArgumentException) {
                    // The platform reports an already-stopped discovery this way on older releases.
                } catch (_: RuntimeException) {
                    // Collection is already closed; cleanup must still release multicast reception.
                }
                discoveryRequested = false
            }
            releaseMulticastLock()
        }

        private fun acceptService(serviceInfo: NsdServiceInfo) {
            if (!matchesServiceType(serviceInfo.serviceType)) return
            val key = serviceKey(serviceInfo)
            if (key.isEmpty()) return
            if (key !in candidates && candidates.size >= MAX_DISCOVERED_SERVICES) return

            candidates.remove(key)?.job?.cancel()
            candidateErrors.remove(key)
            val candidate = Candidate(++generation)
            candidates[key] = candidate
            emitState()
            candidate.job = scope.launch {
                resolveAndProbe(key, candidate.generation, serviceInfo)
            }
        }

        private fun removeService(key: String) {
            val candidate = candidates.remove(key) ?: return
            candidate.job?.cancel()
            candidateErrors.remove(key)
            emitState()
        }

        private suspend fun resolveAndProbe(key: String, expectedGeneration: Long, serviceInfo: NsdServiceInfo) {
            try {
                val resolved = resolveMutex.withLock { resolve(serviceInfo) }
                if (!isCurrent(key, expectedGeneration)) return
                val advertised = parseResolvedService(resolved)
                if (advertised == null) {
                    recordFailure(
                        key,
                        expectedGeneration,
                        ClientException(ClientErrorCode.DISCOVERY, "Invalid jastreamer service advertisement"),
                    )
                    return
                }

                var lastFailure: ClientException? = null
                probeSemaphore.withPermit {
                    for (origin in advertised.origins) {
                        if (!isCurrent(key, expectedGeneration)) return@withPermit
                        try {
                            val verified = probe.probe(origin, advertised.id)
                            if (isCurrent(key, expectedGeneration)) {
                                candidates[key]?.verified = verified
                                candidateErrors.remove(key)
                                discoveryError = null
                                emitState()
                            }
                            return@withPermit
                        } catch (cancelled: CancellationException) {
                            throw cancelled
                        } catch (error: ClientException) {
                            lastFailure = error
                        }
                    }
                }
                if (isCurrent(key, expectedGeneration) && candidates[key]?.verified == null) {
                    recordFailure(
                        key,
                        expectedGeneration,
                        lastFailure ?: ClientException(ClientErrorCode.UNREACHABLE, "Discovered server is unreachable"),
                    )
                }
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (error: ResolutionFailure) {
                recordFailure(key, expectedGeneration, discoveryFailure("NSD resolution failed", error.errorCode, error))
            } catch (error: RuntimeException) {
                recordFailure(
                    key,
                    expectedGeneration,
                    ClientException(ClientErrorCode.DISCOVERY, "Unable to resolve discovered server", error),
                )
            }
        }

        private suspend fun resolve(serviceInfo: NsdServiceInfo): NsdServiceInfo =
            suspendCancellableCoroutine { continuation ->
                val listener = object : NsdManager.ResolveListener {
                    override fun onServiceResolved(resolved: NsdServiceInfo) = onMain {
                        continuation.succeed(resolved)
                    }

                    override fun onResolveFailed(failed: NsdServiceInfo, errorCode: Int) = onMain {
                        continuation.fail(ResolutionFailure(errorCode))
                    }
                }

                continuation.invokeOnCancellation {
                    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
                        mainHandler.post {
                            try {
                                nsdManager.stopServiceResolution(listener)
                            } catch (_: IllegalArgumentException) {
                            } catch (_: RuntimeException) {
                            }
                        }
                    }
                }

                try {
                    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                        nsdManager.resolveService(serviceInfo, mainExecutor, listener)
                    } else {
                        @Suppress("DEPRECATION")
                        nsdManager.resolveService(serviceInfo, listener)
                    }
                } catch (error: RuntimeException) {
                    continuation.fail(error)
                }
            }

        private fun parseResolvedService(service: NsdServiceInfo): AdvertisedService? {
            val attributes = try {
                service.attributes
            } catch (_: RuntimeException) {
                return null
            }
            val product = attributes.text("product")
            if (product != null && product != "jastreamer") return null
            if (attributes.text("protocol") != "1" || attributes.text("path") != "/") return null

            val id = try {
                EndpointPolicy.normalizeServerId(attributes.text("id") ?: return null)
            } catch (_: ClientException) {
                return null
            }
            val scheme = attributes.text("scheme")?.lowercase(Locale.US)
            if (scheme != "http" && scheme != "https") return null
            if (cleanDisplayText(attributes.text("name"), MAX_NAME_LENGTH) == null) return null
            if (cleanDisplayText(attributes.text("version"), MAX_VERSION_LENGTH) == null) return null
            val port = service.port
            if (port !in 1..65_535) return null

            val addresses = resolvedAddresses(service).filter(::isEligibleLanAddress)
            if (addresses.isEmpty()) return null
            val localHosts = addresses.mapNotNull { address ->
                val hostname = address.hostName
                hostname.takeIf(::isLocalHostname)
            }.distinct()
            val addressHosts = addresses.mapNotNull { address ->
                val host = address.hostAddress ?: return@mapNotNull null
                host.takeUnless { '%' in it }
            }.distinct()
            val hosts = if (scheme == "https") localHosts + addressHosts else addressHosts + localHosts
            val origins = hosts.asSequence()
                .mapNotNull { host -> endpointForHost(scheme, host, port) }
                .distinct()
                .take(MAX_ORIGINS_PER_SERVICE)
                .toList()
            return origins.takeIf { it.isNotEmpty() }?.let { AdvertisedService(id, it) }
        }

        @Suppress("DEPRECATION")
        private fun resolvedAddresses(service: NsdServiceInfo): List<InetAddress> =
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
                service.hostAddresses
            } else {
                listOfNotNull(service.host)
            }

        private fun isEligibleLanAddress(address: InetAddress): Boolean {
            if (address.isAnyLocalAddress || address.isLoopbackAddress || address.isMulticastAddress) return false
            return when (address) {
                is Inet4Address -> address.isSiteLocalAddress || address.isLinkLocalAddress
                is Inet6Address -> {
                    val first = address.address.first().toInt() and 0xff
                    address.isLinkLocalAddress || address.isSiteLocalAddress || first and 0xfe == 0xfc
                }
                else -> false
            }
        }

        private fun endpointForHost(scheme: String, host: String, port: Int): String? {
            val bareHost = host.removeSuffix(".")
            val formattedHost = if (':' in bareHost) "[$bareHost]" else bareHost
            return try {
                EndpointPolicy.normalize("$scheme://$formattedHost:$port")
            } catch (_: ClientException) {
                null
            }
        }

        private fun isLocalHostname(host: String): Boolean {
            val normalized = host.lowercase(Locale.US).removeSuffix(".")
            return normalized.endsWith(".local") && normalized.length <= 253 &&
                normalized.none { it.isWhitespace() || it.isISOControl() }
        }

        private fun Map<String, ByteArray>.text(key: String): String? {
            val bytes = this[key] ?: return null
            if (bytes.isEmpty() || bytes.size > MAX_TXT_VALUE_BYTES) return null
            return try {
                StandardCharsets.UTF_8.newDecoder()
                    .onMalformedInput(CodingErrorAction.REPORT)
                    .onUnmappableCharacter(CodingErrorAction.REPORT)
                    .decode(ByteBuffer.wrap(bytes))
                    .toString()
            } catch (_: Exception) {
                null
            }
        }

        private fun cleanDisplayText(value: String?, maximum: Int): String? {
            val text = value?.trim().orEmpty()
            return text.takeIf {
                it.isNotEmpty() && it.length <= maximum && it.none(Char::isISOControl)
            }
        }

        private fun recordFailure(key: String, expectedGeneration: Long, error: ClientException) {
            if (!isCurrent(key, expectedGeneration)) return
            candidates[key]?.verified = null
            candidateErrors[key] = error
            emitState()
        }

        private fun isCurrent(key: String, expectedGeneration: Long): Boolean =
            active.get() && candidates[key]?.generation == expectedGeneration

        private fun emitState() {
            if (!active.get()) return
            val servers = candidates.values.asSequence()
                .mapNotNull(Candidate::verified)
                .distinctBy { server -> "${server.id}\u0000${server.origin}" }
                .sortedWith(compareBy(String.CASE_INSENSITIVE_ORDER) { it.name })
                .toList()
            emit(DiscoveryState(servers, discoveryError ?: candidateErrors.values.firstOrNull()))
        }

        private fun onMain(action: () -> Unit) {
            if (!active.get()) return
            if (Looper.myLooper() == Looper.getMainLooper()) {
                action()
            } else {
                mainHandler.post {
                    if (active.get()) action()
                }
            }
        }

        private fun acquireMulticastLock() {
            try {
                multicastLock = wifiManager.createMulticastLock(MULTICAST_LOCK_TAG).apply {
                    setReferenceCounted(false)
                    acquire()
                }
            } catch (error: RuntimeException) {
                multicastLock = null
                discoveryError = ClientException(
                    ClientErrorCode.DISCOVERY,
                    "Unable to enable multicast discovery",
                    error,
                )
            }
        }

        private fun releaseMulticastLock() {
            val lock = multicastLock ?: return
            multicastLock = null
            try {
                if (lock.isHeld) lock.release()
            } catch (_: RuntimeException) {
            }
        }

        private fun serviceKey(service: NsdServiceInfo): String {
            val name = service.serviceName?.trim().orEmpty()
            if (name.isEmpty() || name.length > MAX_NAME_LENGTH || name.any(Char::isISOControl)) return ""
            return "$name\u0000${service.serviceType.orEmpty()}"
        }

        private fun matchesServiceType(type: String?): Boolean {
            val normalized = type?.lowercase(Locale.US)?.removeSuffix(".") ?: return false
            return normalized == SERVICE_TYPE.removeSuffix(".") ||
                normalized == "${SERVICE_TYPE.removeSuffix(".")}.local"
        }

        private fun discoveryFailure(
            detail: String,
            errorCode: Int,
            cause: Throwable? = null,
        ): ClientException = ClientException(ClientErrorCode.DISCOVERY, "$detail ($errorCode)", cause)

        private fun CancellableContinuation<NsdServiceInfo>.succeed(value: NsdServiceInfo) {
            if (isActive) resumeWith(Result.success(value))
        }

        private fun CancellableContinuation<NsdServiceInfo>.fail(error: Throwable) {
            if (isActive) resumeWith(Result.failure(error))
        }
    }

    private data class Candidate(
        val generation: Long,
        var job: Job? = null,
        var verified: ServerEndpoint? = null,
    )

    private data class AdvertisedService(
        val id: String,
        val origins: List<String>,
    )

    private class ResolutionFailure(val errorCode: Int) : Exception("NSD resolution failed: $errorCode")

    companion object {
        private const val SERVICE_TYPE = "_jastreamer._tcp."
        private const val MULTICAST_LOCK_TAG = "jastreamer-discovery"
        private const val MAX_DISCOVERED_SERVICES = 64
        private const val MAX_PARALLEL_PROBES = 4
        private const val MAX_ORIGINS_PER_SERVICE = 6
        private const val MAX_TXT_VALUE_BYTES = 256
        private const val MAX_NAME_LENGTH = 128
        private const val MAX_VERSION_LENGTH = 64
    }
}
