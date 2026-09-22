package io.jastreamer.android

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import androidx.work.Constraints
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.WorkManager
import java.io.IOException
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import javax.net.ssl.SSLException
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import org.json.JSONObject

object OfflineDownloads {
    private const val PREFERENCES = "offline_download_settings"
    private const val UNIQUE_LOCAL_WORK = "offline-local-finalization"
    private const val ALLOW_METERED = "allow_metered"
    private const val UNIQUE_WORK = "offline-music-imports"
    private const val SAFETY_RESERVE_BYTES = 32L * 1024 * 1024
    private const val PREPARATION_POLL_INTERVAL_MS = 1_500L

    private val lock = Any()
    private val commitGates = ConcurrentHashMap<String, CommitGate>()
    private val finalizationLocks = ConcurrentHashMap<String, Any>()
    private val remoteScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val serverGenerations = mutableMapOf<String, Long>()
    private val workerLock = Mutex()
    @Volatile private var remoteWorkerActive = false
    private var remoteScheduleRequested = false
    private var lastNetworkState: OfflineNetworkState? = null
    private var lastObservedNetworkState: OfflineNetworkState? = null
    private var networkCallback: ConnectivityManager.NetworkCallback? = null
    private const val NOTIFICATION_STEP_BYTES = 1024L * 1024
    private val mutableJobs = MutableStateFlow<List<OfflineDownloadJob>>(emptyList())
    private var appContext: Context? = null
    private var store: OfflineDownloadStore? = null
    private var records = mutableListOf<StoredDownloadJob>()

    val jobs: StateFlow<List<OfflineDownloadJob>> = mutableJobs.asStateFlow()

    fun initialize(context: Context) = initialize(context, scheduleRestored = true)

    private fun initialize(context: Context, scheduleRestored: Boolean) {
        val application = context.applicationContext
        var registerNetworkObserver = false
        var localFinalizationIds = emptyList<String>()
        var restoredPendingWork = false
        var completedReceipts = emptyList<Pair<String, String>>()
        synchronized(lock) {
            if (appContext == null) {
                appContext = application
                store = OfflineDownloadStore(application.filesDir)
                records = store!!.load()
                var changed = false
                records.forEach { job ->
                    val interruptedImport = job.status == "importing" &&
                        (job.tracks.any { it.status == "committing" } ||
                            (job.server == null && job.errorCode in setOf("cancelled", "logout")))
                    if (job.status in setOf("downloading", "importing") && !interruptedImport) {
                        job.status = "waiting"
                        job.errorCode = "interrupted"
                        job.errorMessage = "The interrupted download is ready to resume"
                        changed = true
                    }
                    if (job.status in setOf("completed", "partial", "cancelled")) {
                        store!!.discardTemporary(job)
                    }
                    if (job.status in setOf("completed", "partial", "cancelled") && job.server != null) {
                        job.scrubRemoteAssociation()
                        changed = true
                    }
                }
                if (changed) store!!.save(records)
                publishLocked()
                restoredPendingWork = records.any {
                    it.server != null &&
                        it.status in setOf("waiting", "preparing", "downloading", "importing") &&
                        !needsLocalRecovery(it)
                }
                localFinalizationIds = records.filter(::needsLocalRecovery).map(StoredDownloadJob::id)
                completedReceipts = records.mapNotNull { job ->
                    job.playlistReceiptToken
                        ?.takeIf { job.status in setOf("completed", "partial", "cancelled") }
                        ?.let { job.id to it }
                }
            } else if (appContext !== application && appContext?.filesDir != application.filesDir) {
                throw IllegalStateException("OfflineDownloads was initialized with another application")
            }
            if (networkCallback == null) {
                networkCallback = DownloadNetworkCallback(application)
                registerNetworkObserver = true
            }
        }
        if (registerNetworkObserver) {
            application.getSystemService(ConnectivityManager::class.java)
                .registerDefaultNetworkCallback(requireNotNull(networkCallback))
        }
        if (completedReceipts.isNotEmpty()) {
            val library = OfflineLibrary.get(application)
            completedReceipts.forEach { (jobId, receipt) ->
                library.acknowledgePlaylistSnapshot(receipt)
                synchronized(lock) {
                    val job = findLocked(jobId)
                    if (job?.playlistReceiptToken == receipt) {
                        job.playlistReceiptToken = null
                        persistLocked()
                    }
                }
            }
        }
        if (scheduleRestored && localFinalizationIds.isNotEmpty()) scheduleLocal(application)
        if (scheduleRestored) {
            localFinalizationIds.forEach { jobId -> finishCancelledAsync(application, jobId) }
        }
        if (scheduleRestored && restoredPendingWork) schedule(application, replace = true)
    }

    suspend fun enqueue(
        context: Context,
        server: ServerEndpoint,
        target: JSONObject,
        quality: String,
        folderId: String = OfflineLibrary.IMPORT_FOLDER_ID,
    ): String = withContext(Dispatchers.IO) {
        initialize(context)
        val safeQuality = OfflineTransferPolicy.requireQuality(quality)
        val (kind, targetId) = OfflineTransferPolicy.requireTarget(target)
        val library = OfflineLibrary.get(context)
        if (library.folder(folderId) == null) {
            throw OfflineDownloadException("destination_missing", "Choose an existing download folder")
        }
        val normalized = normalizedServer(server)
        val key = serverKey(normalized)
        val generation = synchronized(lock) { serverGenerations[key] ?: 0L }
        val stillAuthorized = { synchronized(lock) { serverGenerations[key] ?: 0L } == generation }
        val client = OfflineTransferClient(normalized)
        client.verifyServerIdentity()
        val principal = client.principal()
        val manifest = client.create(kind, targetId, safeQuality, principal, stillAuthorized)
        client.requirePrincipal(principal)
        val localId = UUID.randomUUID().toString()
        val record = StoredDownloadJob(
            id = localId,
            title = manifest.title,
            server = normalized,
            principalId = principal,
            remoteId = manifest.remoteId,
            kind = kind,
            targetId = targetId,
            status = if (manifest.status == "preparing") "preparing" else "waiting",
            quality = safeQuality,
            folderId = folderId,
            tracks = manifest.tracks,
        )
        synchronized(lock) {
            requireInitialized()
            if ((serverGenerations[key] ?: 0L) != generation) {
                throw OfflineDownloadException("stopped", "Download authorization was revoked")
            }
            records.add(0, record)
            persistLocked()
        }
        schedule(context.applicationContext, replace = true)
        localId
    }

    fun pause(context: Context, jobId: String) {
        initialize(context)
        synchronized(lock) {
            val job = findLocked(jobId) ?: return
            if (job.status in setOf("completed", "partial", "cancelled")) return
            cancelOpenImport(job.id)
            job.status = "paused"
            job.errorCode = "paused"
            job.errorMessage = "Download paused"
            persistLocked()
        }
    }

    fun resume(context: Context, jobId: String) {
        initialize(context)
        synchronized(lock) {
            val job = findLocked(jobId) ?: return
            if (job.server == null || job.status in setOf("completed", "partial", "cancelled")) return
            job.status = "waiting"
            job.errorCode = null
            job.errorMessage = null
            persistLocked()
        }
        schedule(context.applicationContext, replace = true)
    }

    fun cancel(context: Context, jobId: String) {
        initialize(context)
        var finishLocal = false
        var remote: Triple<ServerEndpoint, String, String>? = null
        var generation = 0L
        synchronized(lock) {
            val job = findLocked(jobId) ?: return
            if (job.status in setOf("completed", "partial", "cancelled")) return
            remote = job.server?.let { endpoint ->
                val principal = job.principalId ?: return@let null
                val remoteId = job.remoteId ?: return@let null
                generation = serverGenerations[serverKey(endpoint)] ?: 0L
                Triple(endpoint, principal, remoteId)
            }
            val commitWon = cancelOpenImport(job.id)
            finishLocal = commitWon || job.tracks.any { it.importedTrackId != null }
            if (finishLocal) {
                job.status = "importing"
                job.errorCode = "cancelled"
                job.errorMessage = "Download cancelled after a local import began"
            } else {
                job.status = "cancelled"
                job.errorCode = "cancelled"
                job.errorMessage = "Download cancelled"
                store!!.discardTemporary(job)
            }
            job.scrubRemoteAssociation()
            persistLocked()
        }
        remote?.let { (endpoint, principal, remoteId) ->
            val key = serverKey(endpoint)
            remoteScope.launch {
                runCatching {
                    OfflineTransferClient(endpoint).cancel(remoteId, principal) {
                        synchronized(lock) { serverGenerations[key] ?: 0L } == generation
                    }
                }
            }
        }
        if (finishLocal) scheduleLocal(context.applicationContext)
        if (finishLocal) finishCancelledAsync(context.applicationContext, jobId)
    }

    fun removeHistory(context: Context, jobId: String) {
        val application = context.applicationContext
        initialize(application)
        if (synchronized(lock) { findLocked(jobId)?.terminal() != true }) return
        val finalizationLock = finalizationLocks.computeIfAbsent(jobId) { Any() }
        synchronized(finalizationLock) {
            val receipt = synchronized(lock) {
                val job = findLocked(jobId) ?: return
                if (!job.terminal()) return
                job.playlistReceiptToken
            }
            if (receipt != null) {
                OfflineLibrary.get(application).acknowledgePlaylistSnapshot(receipt)
            }
            synchronized(lock) {
                val job = findLocked(jobId) ?: return
                if (!job.terminal()) return
                if (job.playlistReceiptToken != null && job.playlistReceiptToken != receipt) return
                store!!.discardTemporary(job)
                records.remove(job)
                persistLocked()
            }
        }
    }

    fun clearFinishedHistory(context: Context) {
        val application = context.applicationContext
        initialize(application)
        val finishedIds = synchronized(lock) {
            records.filter(StoredDownloadJob::terminal).map(StoredDownloadJob::id)
        }
        finishedIds.forEach { removeHistory(application, it) }
    }

    fun logout(context: Context, server: ServerEndpoint) {
        initialize(context)
        val key = serverKey(server)
        val localFinalizations = mutableListOf<String>()
        synchronized(lock) {
            serverGenerations[key] = (serverGenerations[key] ?: 0L) + 1L
            var changed = false
            records.forEach { job ->
                if (job.server?.let(::serverKey) == key && job.status !in setOf("completed", "partial", "cancelled")) {
                    val commitWon = cancelOpenImport(job.id)
                    val preserveLocal = commitWon || job.tracks.any { it.importedTrackId != null }
                    if (preserveLocal) {
                        job.status = "importing"
                        job.errorCode = "logout"
                        job.errorMessage = "Download stopped at logout after a local import began"
                        localFinalizations += job.id
                    } else {
                        job.status = "cancelled"
                        job.errorCode = "logout"
                        job.errorMessage = "Download stopped at logout"
                        store!!.discardTemporary(job)
                    }
                    job.scrubRemoteAssociation()
                    changed = true
                }
            }
            if (changed) persistLocked()
        }
        if (localFinalizations.isNotEmpty()) scheduleLocal(context.applicationContext)
        localFinalizations.forEach { finishCancelledAsync(context.applicationContext, it) }
    }

    fun allowMetered(context: Context): Boolean =
        context.applicationContext.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE)
            .getBoolean(ALLOW_METERED, false)

    internal fun networkState(context: Context): OfflineNetworkState =
        currentNetworkState(context.applicationContext, allowMetered(context))

    fun setAllowMetered(context: Context, allowed: Boolean) {
        val application = context.applicationContext
        application.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE)
            .edit().putBoolean(ALLOW_METERED, allowed).apply()
        initialize(application)
        schedule(application, replace = true)
    }

    fun changeDestination(context: Context, jobId: String, folderId: String) {
        initialize(context)
        val exists = OfflineLibrary.get(context).folder(folderId) != null
        synchronized(lock) {
            val job = findLocked(jobId) ?: return
            if (job.server == null || job.status in setOf("completed", "partial", "cancelled")) return
            cancelOpenImport(job.id)
            job.folderId = folderId
            if (exists) {
                if (job.status != "paused") job.status = "waiting"
                job.errorCode = null
                job.errorMessage = null
            } else {
                job.status = "waiting"
                job.errorCode = "destination_missing"
                job.errorMessage = "Choose another download folder"
            }
            persistLocked()
        }
        if (exists) schedule(context.applicationContext, replace = true)
    }

    internal suspend fun preview(context: Context, server: ServerEndpoint, target: JSONObject): DownloadPreview =
        withContext(Dispatchers.IO) {
            val (kind, targetId) = OfflineTransferPolicy.requireTarget(target)
            val client = OfflineTransferClient(normalizedServer(server))
            client.verifyServerIdentity()
            val principal = client.principal()
            client.preview(kind, targetId, principal)
        }

    internal fun jobValues(context: Context, ids: Set<String>): List<OfflineDownloadJob> {
        updateNetworkState(context.applicationContext)
        return synchronized(lock) {
            records.asSequence().filter { it.id in ids }.map(StoredDownloadJob::publicValue).toList()
        }
    }

    internal suspend fun process(
        context: Context,
        progress: (OfflineDownloadJob?) -> Unit,
        localOnly: Boolean = false,
    ): Boolean = workerLock.withLock {
        initialize(context, scheduleRestored = false)
        if (!localOnly) synchronized(lock) {
            remoteWorkerActive = true
            remoteScheduleRequested = false
        }
        try {
            var needsRetry = false
            var remoteHandoff = false
            var localHandoff = false
            var waitForPreparation = false
            // Preparation stays inside this foreground run. Jobs deferred after an
            // actual failure return to WorkManager so its error backoff still applies.
            val deferred = HashSet<String>()
            while (true) {
                if (waitForPreparation) delay(PREPARATION_POLL_INTERVAL_MS)
                val jobIds = synchronized(lock) {
                    records.asSequence()
                        .filter { job ->
                            job.id !in deferred &&
                                if (localOnly) {
                                    needsLocalRecovery(job)
                                } else {
                                    isRemotePending(job)
                                }
                        }
                        .map(StoredDownloadJob::id)
                        .toList()
                }
                if (jobIds.isEmpty()) break
                var preparationRemains = false
                jobIds.forEach { jobId ->
                    when (processOne(context.applicationContext, jobId, progress, localOnly)) {
                        ProcessOutcome.PREPARING -> preparationRemains = true
                        ProcessOutcome.RETRY -> {
                            needsRetry = true
                            deferred += jobId
                        }
                        ProcessOutcome.NO_PROGRESS -> deferred += jobId
                        ProcessOutcome.REMOTE_REQUIRED -> {
                            remoteHandoff = true
                            deferred += jobId
                        }
                        ProcessOutcome.DONE -> Unit
                    }
                    if (!localOnly && synchronized(lock) { findLocked(jobId)?.let(::needsLocalRecovery) == true }) {
                        localHandoff = true
                        deferred += jobId
                    }
                }
                if (localOnly || !preparationRemains) break
                waitForPreparation = true
            }
            if (remoteHandoff) schedule(context.applicationContext)
            if (localHandoff) scheduleLocal(context.applicationContext)
            progress(null)
            needsRetry
        } finally {
            if (!localOnly) {
                val reschedule = synchronized(lock) {
                    remoteWorkerActive = false
                    val requested = remoteScheduleRequested
                    remoteScheduleRequested = false
                    requested && records.any(::isRemotePending)
                }
                if (reschedule) schedule(context.applicationContext, replace = true)
            }
        }
    }

    private suspend fun processOne(
        context: Context,
        jobId: String,
        progress: (OfflineDownloadJob?) -> Unit,
        localOnly: Boolean,
    ): ProcessOutcome {
        var initial = synchronized(lock) { findLocked(jobId) } ?: return ProcessOutcome.DONE
        val library = OfflineLibrary.get(context)
        recoverCommitting(jobId, library)
        initial = synchronized(lock) { findLocked(jobId) } ?: return ProcessOutcome.DONE
        val locallyResolved = isLocallyResolved(initial)
        if (locallyResolved) {
            val cancelled = initial.server == null && initial.errorCode in setOf("cancelled", "logout")
            finishImport(context, jobId, cancelled)
            progress(publicJob(jobId))
            return ProcessOutcome.DONE
        }
        val endpoint = initial.server
        if (endpoint == null) {
            if (initial.status == "importing" && initial.errorCode in setOf("cancelled", "logout")) {
                finishImport(context, jobId, cancelled = true)
                progress(publicJob(jobId))
            }
            return ProcessOutcome.DONE
        }
        if (localOnly) return ProcessOutcome.REMOTE_REQUIRED
        val network = updateNetworkState(context)
        if (!network.allowed) {
            update(jobId, "waiting", network.errorCode, network.errorMessage)
            progress(publicJob(jobId))
            // A scheduler may match a secondary network; keep durable work until
            // the default route used by HTTP is eligible instead of completing it.
            return ProcessOutcome.RETRY
        }
        val principal = initial.principalId ?: return hardFailure(jobId, "auth_required", "Download authorization is unavailable")
        val remoteId = initial.remoteId ?: return hardFailure(jobId, "invalid_manifest", "Download job identity is unavailable")
        val client = OfflineTransferClient(endpoint)
        try {
            if (library.folder(initial.folderId) == null) {
                update(jobId, "waiting", "destination_missing", "Choose another download folder")
                return ProcessOutcome.NO_PROGRESS
            }
            client.verifyServerIdentity()
            val manifest = client.poll(
                remoteId,
                initial.kind,
                initial.quality,
                principal,
            ) { transferMayContinue(jobId) }
            val afterPoll = synchronized(lock) { findLocked(jobId) }
            if (afterPoll == null || afterPoll.status == "cancelled" || afterPoll.server == null) {
                return ProcessOutcome.DONE
            }
            if (afterPoll.status == "paused") return ProcessOutcome.NO_PROGRESS
            mergeManifest(jobId, manifest)
            if (manifest.status == "cancelled") {
                if (finishPartialIfAny(context, jobId)) return ProcessOutcome.DONE
                return hardFailure(jobId, "cancelled", "The server download was cancelled", cancelled = true)
            }

            val indexes = synchronized(lock) {
                findLocked(jobId)?.tracks?.filter { it.status == "ready" && it.importedTrackId == null }?.map { it.index }.orEmpty()
            }
            for (index in indexes) {
                val current = synchronized(lock) { findLocked(jobId) }
                if (current == null || current.status == "cancelled" || current.server == null) return ProcessOutcome.DONE
                if (current.status == "paused") return ProcessOutcome.NO_PROGRESS
                val item = current.tracks.firstOrNull { it.index == index } ?: continue
                if (library.folder(current.folderId) == null) {
                    update(jobId, "waiting", "destination_missing", "Choose another download folder")
                    return ProcessOutcome.NO_PROGRESS
                }
                enforceCapacity(library, item.byteSize)
                update(jobId, "downloading", null, null)
                progress(publicJob(jobId))
                val partial = store!!.partialFile(jobId, index)
                val previousHash = item.sha256
                var lastNotifiedBytes = partial.length()
                val completedBytes = current.tracks
                    .filter { it.importedTrackId != null && it.index != index }
                    .sumOf { it.byteSize }
                client.download(remoteId, item, partial, principal, { transferMayContinue(jobId) }) { received ->
                    var notification: OfflineDownloadJob? = null
                    synchronized(lock) {
                        val active = findLocked(jobId) ?: return@synchronized
                        if (active.status == "cancelled") return@synchronized
                        active.receivedBytes = maxOf(active.receivedBytes, received + completedBytes)
                        if (received == item.byteSize || received - lastNotifiedBytes >= NOTIFICATION_STEP_BYTES) {
                            lastNotifiedBytes = received
                            notification = active.publicValue()
                            publishLocked()
                        }
                    }
                    notification?.let(progress)
                }
                val afterDownload = synchronized(lock) { findLocked(jobId) }
                if (afterDownload == null || afterDownload.status == "cancelled" || afterDownload.server == null) {
                    partial.delete()
                    return ProcessOutcome.DONE
                }
                if (partial.length() != item.byteSize || OfflineTransferPolicy.sha256(partial) != previousHash) {
                    partial.delete()
                    failTrack(jobId, index, "checksum", "Downloaded audio failed checksum verification")
                    continue
                }
                if (afterDownload.status == "paused") return ProcessOutcome.NO_PROGRESS
                val artwork = client.artwork(item.artworkPath, store!!.artworkFile(jobId, index), principal) {
                    transferMayContinue(jobId)
                }
                val beforeImport = synchronized(lock) { findLocked(jobId)?.copy(tracks = findLocked(jobId)!!.tracks.toMutableList()) }
                if (beforeImport == null || beforeImport.status == "cancelled" || beforeImport.server == null) {
                    artwork?.delete()
                    partial.delete()
                    return ProcessOutcome.DONE
                }
                if (beforeImport.status == "paused") {
                    artwork?.delete()
                    return ProcessOutcome.NO_PROGRESS
                }
                if (library.folder(beforeImport.folderId) == null) {
                    artwork?.delete()
                    update(jobId, "waiting", "destination_missing", "Choose another download folder")
                    return ProcessOutcome.NO_PROGRESS
                }
                client.requirePrincipal(principal)
                val gateKey = commitGateKey(jobId, index)
                val commitGate = synchronized(lock) {
                    val active = findLocked(jobId)
                    if (
                        active == null ||
                        active.status in setOf("paused", "cancelled") ||
                        active.server == null ||
                        active.folderId != beforeImport.folderId
                    ) {
                        null
                    } else {
                        val gate = CommitGate()
                        check(commitGates.putIfAbsent(gateKey, gate) == null)
                        active.status = "importing"
                        active.tracks.firstOrNull { it.index == index }?.status = "committing"
                        active.errorCode = null
                        active.errorMessage = null
                        persistLocked()
                        gate
                    }
                }
                if (commitGate == null) {
                    artwork?.delete()
                    val active = synchronized(lock) { findLocked(jobId) }
                    if (active == null || active.status == "cancelled" || active.server == null) {
                        partial.delete()
                        if (active != null && active.server == null && active.status == "importing") {
                            finishImport(context, jobId, cancelled = true)
                        }
                        return ProcessOutcome.DONE
                    }
                    return ProcessOutcome.NO_PROGRESS
                }
                progress(publicJob(jobId))
                val metadata = trackMetadata(item)
                val imported = try {
                    library.importTrack(
                        source = partial,
                        metadata = metadata,
                        folderId = beforeImport.folderId,
                        artwork = artwork,
                        commitGate = { authorize -> commitGate.claim(authorize) },
                    )
                } catch (failure: OfflineLibraryException) {
                    commitGates.remove(gateKey, commitGate)
                    artwork?.delete()
                    if (failure.code == "cancelled") {
                        val active = synchronized(lock) {
                            findLocked(jobId)?.also { job ->
                                job.tracks.firstOrNull { it.index == index }?.let { track ->
                                    if (track.importedTrackId == null) track.status = item.status
                                }
                                persistLocked()
                            }
                        }
                        if (active == null || active.status == "cancelled" || active.server == null) {
                            partial.delete()
                            if (active != null && active.server == null && active.status == "importing") {
                                finishImport(context, jobId, cancelled = true)
                            }
                            return ProcessOutcome.DONE
                        }
                        return ProcessOutcome.NO_PROGRESS
                    }
                    if (failure.code == "missing") {
                        update(jobId, "waiting", "destination_missing", "Choose another download folder")
                        return ProcessOutcome.NO_PROGRESS
                    }
                    if (failure.code == "storage_full") {
                        update(jobId, "paused", failure.code, failure.message ?: "Storage is full")
                        return ProcessOutcome.NO_PROGRESS
                    }
                    if (failure.code == "checksum" || failure.code == "integrity") {
                        partial.delete()
                        failTrack(jobId, index, failure.code, failure.message ?: "Import verification failed")
                        continue
                    }
                    throw OfflineDownloadException(failure.code, failure.message ?: "Could not import download", failure)
                }
                synchronized(lock) {
                    val active = findLocked(jobId) ?: return@synchronized
                    val track = active.tracks.firstOrNull { it.index == index } ?: return@synchronized
                    track.importedTrackId = imported.id
                    track.status = "imported"
                    track.errorCode = null
                    track.errorMessage = null
                    active.receivedBytes = maxOf(active.receivedBytes, active.tracks.filter { it.importedTrackId != null }.sumOf { it.byteSize })
                    persistLocked()
                }
                commitGates.remove(gateKey, commitGate)
                val afterCommit = synchronized(lock) { findLocked(jobId) }
                if (afterCommit == null || afterCommit.server == null) {
                    finishImport(context, jobId, cancelled = true)
                    progress(publicJob(jobId))
                    return ProcessOutcome.DONE
                }
            }

            val after = synchronized(lock) { findLocked(jobId) } ?: return ProcessOutcome.DONE
            if (after.status == "cancelled" || after.server == null) return ProcessOutcome.DONE
            if (after.status == "paused") return ProcessOutcome.NO_PROGRESS
            val pending = after.tracks.any { it.importedTrackId == null && it.status in setOf("pending", "preparing", "ready") }
            if (manifest.status == "preparing" || (pending && manifest.status == "ready")) {
                update(jobId, "preparing", null, null)
                progress(publicJob(jobId))
                return ProcessOutcome.PREPARING
            }
            finishImport(context, jobId)
            progress(publicJob(jobId))
            return ProcessOutcome.DONE
        } catch (cancelled: CancellationException) {
            val network = currentNetworkState(context, allowMetered(context))
            if (!network.allowed) update(jobId, "waiting", network.errorCode, network.errorMessage)
            throw cancelled
        } catch (failure: OfflineDownloadException) {
            if (finishCancelledIfNeeded(context, jobId)) return ProcessOutcome.DONE
            val network = currentNetworkState(context, allowMetered(context))
            if (!network.allowed) {
                update(jobId, "waiting", network.errorCode, network.errorMessage)
                return ProcessOutcome.RETRY
            }
            if (isPermanentFailure(failure) && finishPartialIfAny(context, jobId)) {
                return ProcessOutcome.DONE
            }
            return handleFailure(jobId, failure)
        } catch (failure: SSLException) {
            if (finishCancelledIfNeeded(context, jobId)) return ProcessOutcome.DONE
            val network = currentNetworkState(context, allowMetered(context))
            if (!network.allowed) {
                update(jobId, "waiting", network.errorCode, network.errorMessage)
                return ProcessOutcome.RETRY
            }
            if (finishPartialIfAny(context, jobId)) return ProcessOutcome.DONE
            return hardFailure(jobId, "tls", "The server TLS connection could not be verified")
        } catch (failure: IOException) {
            if (finishCancelledIfNeeded(context, jobId)) return ProcessOutcome.DONE
            val network = currentNetworkState(context, allowMetered(context))
            if (!network.allowed) {
                update(jobId, "waiting", network.errorCode, network.errorMessage)
                return ProcessOutcome.RETRY
            }
            if (library.usage().availableBytes < SAFETY_RESERVE_BYTES) {
                update(jobId, "paused", "storage_full", "Not enough free space to continue the download")
                return ProcessOutcome.NO_PROGRESS
            }
            update(jobId, "waiting", "network", "The server connection was interrupted")
            return ProcessOutcome.RETRY
        } catch (failure: Exception) {
            if (finishCancelledIfNeeded(context, jobId)) return ProcessOutcome.DONE
            val network = currentNetworkState(context, allowMetered(context))
            if (!network.allowed) {
                update(jobId, "waiting", network.errorCode, network.errorMessage)
                return ProcessOutcome.RETRY
            }
            update(jobId, "waiting", "transfer", failure.message?.take(240) ?: "Download could not continue")
            return ProcessOutcome.RETRY
        }
    }

    private fun finishCancelledIfNeeded(context: Context, jobId: String): Boolean {
        val cancelledAfterCommit = synchronized(lock) {
            findLocked(jobId)?.let {
                it.server == null &&
                    it.status == "importing" &&
                    it.errorCode in setOf("cancelled", "logout")
            } == true
        }
        if (!cancelledAfterCommit) return false
        finishImport(context, jobId, cancelled = true)
        return true
    }

    private fun finishImport(
        context: Context,
        jobId: String,
        cancelled: Boolean = false,
    ) {
        val gate = finalizationLocks.computeIfAbsent(jobId) { Any() }
        synchronized(gate) { finishImportLocked(context, jobId, cancelled) }
    }

    private fun finishImportLocked(
        context: Context,
        jobId: String,
        cancelled: Boolean,
    ) {
        if (synchronized(lock) { findLocked(jobId)?.status in setOf("completed", "partial", "cancelled") }) return
        val library = OfflineLibrary.get(context)
        reconcileImportedTracks(jobId, library)
        val job = synchronized(lock) { findLocked(jobId) } ?: return
        if (job.status in setOf("completed", "partial", "cancelled")) return
        val importedIds = job.tracks.mapNotNull(StoredDownloadTrack::importedTrackId)
        val missing = job.tracks.filter { it.importedTrackId == null }.map(StoredDownloadTrack::title)
        var receiptToken: String? = null
        if (job.kind == "playlist") {
            receiptToken = synchronized(lock) {
                val active = findLocked(jobId) ?: return@synchronized null
                active.playlistReceiptToken ?: UUID.randomUUID().toString().also {
                    active.playlistReceiptToken = it
                    persistLocked()
                }
            } ?: return
            val playlist = try {
                library.savePlaylistSnapshotOnce(receiptToken, job.title, importedIds, missing)
            } catch (failure: OfflineLibraryException) {
                if (failure.code != "missing") throw failure
                reconcileImportedTracks(jobId, library)
                return finishImportLocked(context, jobId, cancelled)
            }
            synchronized(lock) {
                val active = findLocked(jobId) ?: return@synchronized
                if (active.playlistReceiptToken == receiptToken) {
                    active.playlistId = playlist?.id
                    persistLocked()
                }
            }
        }

        val status = when {
            job.kind == "playlist" && job.tracks.isEmpty() -> "completed"
            importedIds.isNotEmpty() && missing.isEmpty() -> "completed"
            importedIds.isNotEmpty() || job.kind == "playlist" -> "partial"
            else -> "failed"
        }
        if (status == "failed") {
            if (cancelled) {
                synchronized(lock) {
                    val active = findLocked(jobId) ?: return@synchronized
                    active.status = "cancelled"
                    active.scrubRemoteAssociation()
                    store!!.discardTemporary(active)
                    persistLocked()
                }
            } else {
                hardFailure(jobId, "no_tracks", "No tracks could be imported")
            }
            return
        }
        synchronized(lock) {
            val active = findLocked(jobId) ?: return@synchronized
            val cancellationRequested =
                cancelled ||
                    (active.server == null &&
                        active.status == "importing" &&
                        active.errorCode in setOf("cancelled", "logout"))
            if (cancellationRequested) {
                active.status = "cancelled"
                if (active.errorCode !in setOf("cancelled", "logout")) active.errorCode = "cancelled"
                if (active.errorMessage.isNullOrBlank()) active.errorMessage = "Download cancelled"
            } else {
                active.status = status
                active.errorCode = if (status == "partial") "partial" else null
            }
            active.scrubRemoteAssociation()
            store!!.discardTemporary(active)
            persistLocked()
        }
        if (receiptToken != null) {
            library.acknowledgePlaylistSnapshot(receiptToken)
            synchronized(lock) {
                val active = findLocked(jobId) ?: return@synchronized
                if (active.playlistReceiptToken == receiptToken) {
                    active.playlistReceiptToken = null
                    persistLocked()
                }
            }
        }
    }

    private fun reconcileImportedTracks(jobId: String, library: OfflineLibrary) {
        val receipts = synchronized(lock) {
            findLocked(jobId)?.tracks
                ?.mapNotNull { track -> track.importedTrackId?.let { track.index to it } }
                .orEmpty()
        }
        receipts.forEach { (index, localId) ->
            val available = library.track(localId)?.pendingDelete == false
            if (!available) {
                synchronized(lock) {
                    val job = findLocked(jobId) ?: return@synchronized
                    val track = job.tracks.firstOrNull { it.index == index } ?: return@synchronized
                    if (track.importedTrackId == localId) {
                        track.importedTrackId = null
                        track.status = "failed"
                        track.errorCode = "local_deleted"
                        track.errorMessage = "The saved local track was deleted"
                        persistLocked()
                    }
                }
            }
        }
    }

    private fun finishCancelledAsync(context: Context, jobId: String) {
        remoteScope.launch {
            val library = OfflineLibrary.get(context)
            recoverCommitting(jobId, library)
            val shouldFinish = synchronized(lock) {
                findLocked(jobId)?.let {
                    it.server == null &&
                        it.status == "importing" &&
                        it.errorCode in setOf("cancelled", "logout")
                } == true
            }
            if (shouldFinish) finishImport(context, jobId, cancelled = true)
        }
    }
    private fun recoverCommitting(jobId: String, library: OfflineLibrary) {
        val candidates = synchronized(lock) {
            findLocked(jobId)?.tracks?.filter { it.status == "committing" }?.map { it.copy() }.orEmpty()
        }
        candidates.forEach { candidate ->
            val cancelledGate = commitGates[commitGateKey(jobId, candidate.index)]?.isCancelled() == true
            val existing = if (cancelledGate) null else library.trackByDigest(candidate.sha256, candidate.quality)
            synchronized(lock) {
                val job = findLocked(jobId) ?: return@synchronized
                val track = job.tracks.firstOrNull { it.index == candidate.index } ?: return@synchronized
                if (track.status != "committing" || track.sha256 != candidate.sha256) return@synchronized
                if (existing != null) {
                    track.importedTrackId = existing.id
                    track.status = "imported"
                    track.errorCode = null
                    track.errorMessage = null
                    job.receivedBytes = maxOf(
                        job.receivedBytes,
                        job.tracks.filter { it.importedTrackId != null }.sumOf { it.byteSize },
                    )
                } else {
                    val partial = store!!.partialFile(job.id, track.index)
                    val reusable = partial.isFile &&
                        partial.length() == track.byteSize &&
                        OfflineTransferPolicy.sha256(partial) == track.sha256
                    if (reusable && job.server != null) {
                        track.status = "ready"
                        if (job.status == "importing") job.status = "waiting"
                    } else {
                        partial.delete()
                        track.status = "failed"
                        track.errorCode = "local_deleted"
                        track.errorMessage = "The committed local track is no longer available"
                    }
                }
                persistLocked()
            }
        }
    }

    private fun mergeManifest(jobId: String, manifest: DownloadManifest) {
        synchronized(lock) {
            val job = findLocked(jobId) ?: return
            if (job.status == "cancelled" || job.server == null) return
            if (job.remoteId != manifest.remoteId || job.kind != manifest.kind || job.quality != manifest.quality) {
                throw OfflineDownloadException("invalid_manifest", "Download manifest identity changed")
            }
            if (job.tracks.size != manifest.tracks.size) {
                throw OfflineDownloadException("source_changed", "Download collection changed while importing")
            }
            manifest.tracks.forEachIndexed { position, next ->
                val current = job.tracks[position]
                if (current.index != next.index) {
                    throw OfflineDownloadException("source_changed", "Download collection order changed while importing")
                }
                if (current.status == "failed") return@forEachIndexed
                if (current.importedTrackId != null) return@forEachIndexed
                val part = store!!.partialFile(job.id, current.index)
                if (
                    (current.sourceVersion.isNotBlank() && current.sourceVersion != next.sourceVersion) ||
                    (current.sha256.isNotBlank() &&
                        (current.sha256 != next.sha256 || current.byteSize != next.byteSize))
                ) {
                    part.delete()
                    throw OfflineDownloadException("source_changed", "Download source changed while resuming")
                }
                job.tracks[position] = next.copy(
                    importedTrackId = current.importedTrackId,
                    errorCode = next.errorCode,
                    errorMessage = next.errorMessage,
                )
            }
            job.title = manifest.title
            persistLocked()
        }
    }


    private fun enforceCapacity(library: OfflineLibrary, bytes: Long) {

        val usage = library.usage()
        if (usage.availableBytes - bytes < SAFETY_RESERVE_BYTES) {
            throw OfflineDownloadException("storage_full", "Not enough free space to safely finish the download")
        }
        if (usage.limitBytes > 0 && usage.musicBytes + bytes > usage.limitBytes) {
            throw OfflineDownloadException("storage_full", "The offline music storage limit would be exceeded")
        }
    }
    private fun finishPartialIfAny(context: Context, jobId: String): Boolean {
        val imported = synchronized(lock) {
            findLocked(jobId)?.tracks?.any { it.importedTrackId != null } == true
        }
        if (!imported) return false
        finishImport(context, jobId)
        return true
    }

    private fun isPermanentFailure(failure: OfflineDownloadException): Boolean =
        failure.code != "not_ready" &&
            (failure.retryable == false ||
                failure.code in setOf(
                    "principal_changed",
                    "identity_changed",
                    "invalid_server",
                    "invalid_manifest",
                    "expired",
                    "invalid_path",
                    "source_changed",
                    "range_mismatch",
                    "size_mismatch",
                    "redirect",
                ))

    private fun failTrack(jobId: String, index: Int, code: String, message: String) {
        synchronized(lock) {
            val job = findLocked(jobId) ?: return
            val item = job.tracks.firstOrNull { it.index == index } ?: return
            item.status = "failed"
            item.errorCode = code
            item.errorMessage = message.take(240)
            persistLocked()
        }
    }

    private fun handleFailure(jobId: String, failure: OfflineDownloadException): ProcessOutcome {
        return when (failure.code) {
            "stopped" -> synchronized(lock) {
                if (findLocked(jobId)?.status == "paused") ProcessOutcome.NO_PROGRESS else ProcessOutcome.DONE
            }
            "auth_required" -> {
                update(jobId, "paused", failure.code, failure.message)
                ProcessOutcome.NO_PROGRESS
            }
            "not_ready" -> {
                update(jobId, "preparing", failure.code, failure.message)
                ProcessOutcome.PREPARING
            }
            "storage_full" -> {
                update(jobId, "paused", failure.code, failure.message)
                ProcessOutcome.NO_PROGRESS
            }
            "destination_missing" -> {
                update(jobId, "waiting", failure.code, failure.message)
                ProcessOutcome.NO_PROGRESS
            }
            "principal_changed", "identity_changed", "invalid_server", "invalid_manifest", "expired",
            "invalid_path", "source_changed", "range_mismatch", "size_mismatch", "redirect" ->
                hardFailure(jobId, failure.code, failure.message)
            else -> {
                if (failure.retryable == false) {
                    hardFailure(jobId, failure.code, failure.message)
                } else {
                    update(jobId, "waiting", failure.code, failure.message)
                    ProcessOutcome.RETRY
                }
            }
        }
    }

    private fun hardFailure(jobId: String, code: String, message: String, cancelled: Boolean = false): ProcessOutcome {
        synchronized(lock) {
            val job = findLocked(jobId) ?: return ProcessOutcome.DONE
            if (
                job.server == null &&
                job.status == "importing" &&
                job.errorCode in setOf("cancelled", "logout")
            ) return ProcessOutcome.DONE
            if (job.status in setOf("paused", "cancelled") && !cancelled) return ProcessOutcome.DONE
            job.status = if (cancelled) "cancelled" else "failed"
            job.errorCode = code
            job.errorMessage = message.take(240)
            store!!.discardTemporary(job)
            job.scrubRemoteAssociation()
            persistLocked()
        }
        return ProcessOutcome.DONE
    }

    private fun scheduleLocal(context: Context) {
        val request = OneTimeWorkRequestBuilder<OfflineDownloadService>()
            .setInputData(androidx.work.workDataOf("local_only" to true))
            .build()
        WorkManager.getInstance(context).enqueueUniqueWork(
            UNIQUE_LOCAL_WORK,
            ExistingWorkPolicy.APPEND_OR_REPLACE,
            request,
        )
    }

    private fun update(jobId: String, status: String, code: String?, message: String?) {
        synchronized(lock) {
            val job = findLocked(jobId) ?: return
            if (
                job.server == null ||
                job.status in setOf("completed", "partial", "paused", "cancelled") ||
                (job.status == "importing" && job.errorCode in setOf("cancelled", "logout"))
            ) return
            job.status = status
            job.errorCode = code
            job.errorMessage = message?.take(240)
            persistLocked()
        }
    }

    private fun publicJob(jobId: String): OfflineDownloadJob? = synchronized(lock) { findLocked(jobId)?.publicValue() }

    private fun publishLocked() {
        mutableJobs.value = records.map(StoredDownloadJob::publicValue)
    }

    private fun persistLocked() {
        store!!.save(records)
        publishLocked()
    }

    private fun findLocked(id: String): StoredDownloadJob? = records.firstOrNull { it.id == id }
    private fun transferMayContinue(jobId: String): Boolean = synchronized(lock) {
        val job = findLocked(jobId)
        job != null &&
            job.server != null &&
            job.status !in setOf("paused", "cancelled") &&
            lastNetworkState?.allowed != false
    }

    private fun isLocallyResolved(job: StoredDownloadJob): Boolean =
        job.playlistReceiptToken != null ||
            (job.kind == "playlist" && job.tracks.isEmpty() && job.status != "preparing") ||
            (job.tracks.isNotEmpty() &&
                job.status != "preparing" &&
                job.tracks.all { it.importedTrackId != null || it.status == "failed" })

    private fun needsLocalRecovery(job: StoredDownloadJob): Boolean {
        if (job.status in setOf("completed", "partial", "cancelled", "failed")) return false
        return (job.server == null &&
            job.status == "importing" &&
            job.errorCode in setOf("cancelled", "logout")) ||
            job.tracks.any { it.status == "committing" } ||
            isLocallyResolved(job)
    }

    private fun commitGateKey(jobId: String, index: Int): String = "$jobId/$index"

    private fun cancelOpenImport(jobId: String): Boolean {
        var commitWon = false
        val prefix = "$jobId/"
        commitGates.forEach { (key, gate) ->
            if (key.startsWith(prefix) && gate.cancel()) commitWon = true
        }
        return commitWon
    }

    private fun isRemotePending(job: StoredDownloadJob): Boolean =
        job.server != null &&
            job.status in setOf("waiting", "preparing", "downloading", "importing") &&
            !needsLocalRecovery(job)

    private fun currentNetworkState(context: Context, meteredAllowed: Boolean): OfflineNetworkState {
        // OfflineTransferClient does not bind OkHttp to a Network, so only the process
        // default route may authorize bytes; a secondary network is never implicit consent.
        val connectivity = context.getSystemService(ConnectivityManager::class.java)
        val network = connectivity.activeNetwork
        val capabilities = network?.let(connectivity::getNetworkCapabilities)
        return classifyOfflineNetwork(
            available = capabilities?.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_SUSPENDED) == true,
            metered = capabilities?.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED) == false,
            mobile = capabilities?.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) == true,
            roaming = capabilities?.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_ROAMING) == false,
            meteredAllowed = meteredAllowed,
        )
    }

    private fun updateNetworkState(context: Context): OfflineNetworkState {
        val state = currentNetworkState(context, allowMetered(context))
        synchronized(lock) {
            lastNetworkState = state
            var changed = false
            records.forEach { job ->
                if (!isRemotePending(job) || job.status == "paused") return@forEach
                if (!state.allowed) {
                    if (
                        job.status != "waiting" ||
                        job.errorCode != state.errorCode ||
                        job.errorMessage != state.errorMessage
                    ) {
                        job.status = "waiting"
                        job.errorCode = state.errorCode
                        job.errorMessage = state.errorMessage
                        changed = true
                    }
                } else if (job.errorCode in NETWORK_POLICY_CODES) {
                    job.status = "waiting"
                    job.errorCode = null
                    job.errorMessage = null
                    changed = true
                }
            }
            if (changed) persistLocked()
        }
        return state
    }

    private fun onNetworkChanged(context: Context) {
        val state = updateNetworkState(context)
        val changed = synchronized(lock) {
            val previous = lastObservedNetworkState
            lastObservedNetworkState = state
            previous != state
        }
        if (changed && synchronized(lock) { records.any(::isRemotePending) }) {
            schedule(context, replace = true)
        }
    }

    private class DownloadNetworkCallback(private val context: Context) : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) = changed()
        override fun onLost(network: Network) = changed()
        override fun onCapabilitiesChanged(network: Network, capabilities: NetworkCapabilities) = changed()

        private fun changed() {
            OfflineDownloads.remoteScope.launch { OfflineDownloads.onNetworkChanged(context) }
        }
    }

    private fun requireInitialized() {
        check(appContext != null && store != null) { "OfflineDownloads is not initialized" }
    }

    private fun schedule(context: Context, replace: Boolean = false) {
        val application = context.applicationContext
        val meteredAllowed = allowMetered(application)
        val state = updateNetworkState(application)
        if (!state.allowed) WorkManager.getInstance(application).cancelUniqueWork(UNIQUE_WORK)
        synchronized(lock) {
            if (remoteWorkerActive) {
                remoteScheduleRequested = true
                return
            }
        }
        // This request keeps queued work durable and deliberately does not require public
        // Internet validation. processOne still authorizes the active/default route above.
        val networkRequest = NetworkRequest.Builder()
            .removeCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            .removeCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED)
            .removeCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)
            .addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_ROAMING)
            .addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_SUSPENDED)
            .apply {
                if (!meteredAllowed) {
                    addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED)
                    addTransportType(NetworkCapabilities.TRANSPORT_WIFI)
                    addTransportType(NetworkCapabilities.TRANSPORT_ETHERNET)
                    addTransportType(NetworkCapabilities.TRANSPORT_BLUETOOTH)
                    addTransportType(NetworkCapabilities.TRANSPORT_VPN)
                    addTransportType(NetworkCapabilities.TRANSPORT_WIFI_AWARE)
                    addTransportType(NetworkCapabilities.TRANSPORT_LOWPAN)
                }
            }
            .build()
        val constraints = Constraints.Builder()
            .setRequiredNetworkRequest(
                networkRequest,
                if (meteredAllowed) NetworkType.NOT_ROAMING else NetworkType.UNMETERED,
            )
            .build()
        val request = OneTimeWorkRequestBuilder<OfflineDownloadService>()
            .setConstraints(constraints)
            .build()
        WorkManager.getInstance(application).enqueueUniqueWork(
            UNIQUE_WORK,
            if (replace || !state.allowed) ExistingWorkPolicy.REPLACE else ExistingWorkPolicy.APPEND_OR_REPLACE,
            request,
        )
    }
    private fun normalizedServer(value: ServerEndpoint): ServerEndpoint = ServerEndpoint(
        id = EndpointPolicy.normalizeServerId(value.id),
        name = value.name.trim().take(200),
        version = value.version.trim().take(100),
        origin = EndpointPolicy.normalizeOrigin(value.origin),
    )

    private fun serverKey(value: ServerEndpoint): String =
        "${EndpointPolicy.normalizeServerId(value.id)}\u0000${EndpointPolicy.normalizeOrigin(value.origin)}"

    private fun trackMetadata(item: StoredDownloadTrack): JSONObject = JSONObject()
        .put("title", item.title)
        .put("artist", item.artist)
        .put("album", item.album)
        .put("album_artist", item.albumArtist)
        .put("disc", item.disc)
        .put("track", item.track)
        .put("duration_ms", item.durationMs)
        .put("quality", item.quality)
        .put("mime", item.mime)
        .put("codec", item.codec)
        .put("byte_size", item.byteSize)
        .put("sha256", item.sha256)

    private val NETWORK_POLICY_CODES = setOf(
        "network_unmetered_required",
        "network_roaming",
        "network_unavailable",
    )

    private class CommitGate {
        private var state = CommitGateState.OPEN

        fun claim(authorize: () -> Unit): Boolean = synchronized(this) {
            if (state != CommitGateState.OPEN) return@synchronized false
            authorize()
            state = CommitGateState.CLAIMED
            true
        }

        fun cancel(): Boolean = synchronized(this) {
            when (state) {
                CommitGateState.OPEN -> {
                    state = CommitGateState.CANCELLED
                    false
                }
                CommitGateState.CLAIMED -> true
                CommitGateState.CANCELLED -> false
            }
        }

        fun isCancelled(): Boolean = synchronized(this) { state == CommitGateState.CANCELLED }

        private enum class CommitGateState { OPEN, CLAIMED, CANCELLED }
    }

    private enum class ProcessOutcome { DONE, PREPARING, RETRY, NO_PROGRESS, REMOTE_REQUIRED }
}

internal data class OfflineNetworkState(
    val allowed: Boolean,
    val metered: Boolean,
    val roaming: Boolean,
    val errorCode: String?,
    val errorMessage: String?,
)

internal fun classifyOfflineNetwork(
    available: Boolean,
    metered: Boolean,
    mobile: Boolean,
    roaming: Boolean,
    meteredAllowed: Boolean,
): OfflineNetworkState = when {
    !available -> OfflineNetworkState(
        allowed = false,
        metered = false,
        roaming = false,
        errorCode = "network_unavailable",
        errorMessage = "Connect to the selected server's network to continue",
    )
    roaming -> OfflineNetworkState(
        allowed = false,
        metered = metered || mobile,
        roaming = true,
        errorCode = "network_roaming",
        errorMessage = "Downloads do not run while roaming",
    )
    (metered || mobile) && !meteredAllowed -> OfflineNetworkState(
        allowed = false,
        metered = true,
        roaming = false,
        errorCode = "network_unmetered_required",
        errorMessage = "Allow metered or mobile data to continue",
    )
    else -> OfflineNetworkState(
        allowed = true,
        metered = metered || mobile,
        roaming = false,
        errorCode = null,
        errorMessage = null,
    )
}
