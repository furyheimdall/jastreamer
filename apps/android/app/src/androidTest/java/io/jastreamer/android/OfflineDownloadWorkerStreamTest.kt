package io.jastreamer.android

import android.content.ContentValues
import android.graphics.Bitmap
import android.provider.MediaStore
import android.view.View
import android.view.ViewGroup
import android.widget.ProgressBar
import android.widget.TextView
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.webkit.ProfileStore
import androidx.webkit.WebViewFeature
import androidx.work.WorkInfo
import androidx.work.WorkManager
import java.security.MessageDigest
import java.util.UUID
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicLong
import java.util.concurrent.atomic.AtomicReference
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import okio.Buffer
import org.json.JSONArray
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class OfflineDownloadWorkerStreamTest {
    private val instrumentation = InstrumentationRegistry.getInstrumentation()
    private val context get() = instrumentation.targetContext
    private val library get() = OfflineLibrary.get(context)

    private var fixture: StreamingFixture? = null
    private var endpoint: ServerEndpoint? = null
    private var jobId: String? = null
    private var ownedTitle: String? = null
    private var scenario: ActivityScenario<MainActivity>? = null

    @Before
    fun requireProfileSupport() {
        assertTrue(
            "The installed WebView provider must support isolated profiles",
            WebViewFeature.isFeatureSupported(WebViewFeature.MULTI_PROFILE),
        )
        OfflineDownloads.initialize(context)
        RecentServerStore(context).setLanguage("en")
    }

    @After
    fun cleanUp(): Unit = runBlocking {
        runCatching { scenario?.close() }
        jobId?.let { id ->
            runCatching { OfflineDownloads.cancel(context, id) }
            runCatching { OfflineDownloads.removeHistory(context, id) }
        }
        runCatching {
            WorkManager.getInstance(context)
                .cancelUniqueWork(REMOTE_WORK_NAME)
                .result
                .get(CLEANUP_TIMEOUT_SECONDS, TimeUnit.SECONDS)
        }
        endpoint?.let { server -> runCatching { clearCookies(server) } }
        ownedTitle?.let { title ->
            library.tracks().filter { it.title == title }.forEach { track ->
                runCatching { library.deleteTracks(listOf(track.id)) }
            }
        }
        runCatching { fixture?.close() }
    }

    @Test
    fun productionWorkerStreamsAFullOriginalWithoutConsentOrSchedulerIntervention(): Unit = runBlocking {
        val marker = UUID.randomUUID().toString()
        val stream = StreamingFixture(marker).also { fixture = it }
        val server = ServerEndpoint(
            id = stream.serverId,
            name = "Worker stream fixture",
            version = "test",
            origin = stream.origin,
        ).also { endpoint = it }
        setCookie(server, "worker_session=$marker; Path=/")
        ownedTitle = stream.title

        val activity = ActivityScenario.launch(MainActivity::class.java).also { scenario = it }
        openDownloads(activity)

        val id = OfflineDownloads.enqueue(
            context,
            server,
            JSONObject().put("kind", "track").put("id", "target-$marker"),
            "original",
        ).also { jobId = it }

        val observation = awaitProductionWorker(id, stream, activity)
        val completed = requireNotNull(OfflineDownloads.jobs.value.firstOrNull { it.id == id })
        val diagnostic = diagnostics(id, stream)

        assertEquals("$diagnostic; terminal status", "completed", completed.status)
        assertEquals("$diagnostic; completed track count", 1, completed.completedTracks)
        assertEquals("$diagnostic; received byte count", StreamingFixture.FILE_SIZE, completed.receivedBytes)
        assertEquals("$diagnostic; declared byte count", StreamingFixture.FILE_SIZE, completed.totalBytes)
        assertTrue("$diagnostic; production WorkManager never reached RUNNING", observation.sawRunningWork)
        assertTrue(
            "$diagnostic; native progress did not continue advancing: ${observation.uiProgressValues}",
            observation.uiProgressValues.size >= 2,
        )
        assertTrue("$diagnostic; no associated production worker succeeded", observation.sawSuccessfulWork)

        val transferMillis = TimeUnit.NANOSECONDS.toMillis(observation.completedAtNanos - stream.mediaStartedNanos.get())
        assertTrue(
            "$diagnostic; the throttled worker path completed too quickly to cover a sustained stream ($transferMillis ms)",
            transferMillis >= MINIMUM_OBSERVED_STREAM_MILLIS,
        )
        assertTrue("$diagnostic; the original media endpoint was never requested", stream.mediaRequestCount.get() >= 1)

        val imported = library.tracks().single { it.title == stream.title }
        assertEquals("Imported metadata did not retain the full original byte count", StreamingFixture.FILE_SIZE, imported.byteSize)
        assertEquals("Imported metadata did not retain the original SHA-256", stream.digest, imported.sha256)
        assertImportedBytes(imported, stream)
    }

    @Test
    fun nativeDownloadsShowsProgressBeforeASubMiBBodyFinishes(): Unit = runBlocking {
        val marker = UUID.randomUUID().toString()
        val stream = StreamingFixture(
            marker = marker,
            fileSize = SUB_MIB_FILE_SIZE,
            throttleBytesPerPeriod = SUB_MIB_THROTTLE_BYTES,
            throttlePeriodMillis = SUB_MIB_THROTTLE_PERIOD_MILLIS,
        ).also { fixture = it }
        val server = ServerEndpoint(
            id = stream.serverId,
            name = "Worker stream fixture",
            version = "test",
            origin = stream.origin,
        ).also { endpoint = it }
        setCookie(server, "worker_session=$marker; Path=/")
        ownedTitle = stream.title

        val activity = ActivityScenario.launch(MainActivity::class.java).also { scenario = it }
        openDownloads(activity)
        val id = OfflineDownloads.enqueue(
            context,
            server,
            JSONObject().put("kind", "track").put("id", "target-$marker"),
            "original",
        ).also { jobId = it }

        val visibleAtBytes = awaitVisibleProgressBeforeCompletion(id, stream, activity)
        assertTrue(
            "Native progress became visible only after the response finished; ${diagnostics(id, stream)}",
            visibleAtBytes in 1 until stream.fileSize,
        )

        awaitProductionWorker(id, stream, activity)
        val completed = requireNotNull(OfflineDownloads.jobs.value.firstOrNull { it.id == id })
        assertEquals("${diagnostics(id, stream)}; terminal status", "completed", completed.status)
        screenshot(activity, "offline-download-sub-mib-complete")
        val imported = library.tracks().single { it.title == stream.title }
        assertImportedBytes(imported, stream)
    }

    @Test
    fun nativeRateClearsWhileNoResponseBytesArrive(): Unit = runBlocking {
        val marker = UUID.randomUUID().toString()
        val stream = StreamingFixture(
            marker = marker,
            fileSize = 256L * 1024,
            throttleBytesPerPeriod = 64L * 1024,
            throttlePeriodMillis = 3_000L,
        ).also { fixture = it }
        val server = ServerEndpoint(
            id = stream.serverId,
            name = "Worker stream fixture",
            version = "test",
            origin = stream.origin,
        ).also { endpoint = it }
        setCookie(server, "worker_session=$marker; Path=/")
        ownedTitle = stream.title
        val activity = ActivityScenario.launch(MainActivity::class.java).also { scenario = it }
        openDownloads(activity)
        val id = OfflineDownloads.enqueue(
            context,
            server,
            JSONObject().put("kind", "track").put("id", "target-$marker"),
            "original",
        ).also { jobId = it }

        awaitUi("measured download speed") {
            (displayedRate(activity, stream.title) ?: 0.0) > 0
        }
        val received = requireNotNull(OfflineDownloads.jobValues(context, setOf(id)).firstOrNull()).receivedBytes
        screenshot(activity, "offline-download-rate-active")
        awaitUi("zero speed during a response gap without changing consent") {
            val live = OfflineDownloads.jobValues(context, setOf(id)).firstOrNull()
            live?.status == "downloading" && live.receivedBytes == received &&
                displayedRate(activity, stream.title) == 0.0
        }
        screenshot(activity, "offline-download-rate-idle")
        awaitProductionWorker(id, stream, activity)
        val imported = library.tracks().single { it.title == stream.title }
        assertImportedBytes(imported, stream)
    }

    @Test
    fun interruptedWorkerResumesWithoutCountingCachedBytesAsSpeed(): Unit = runBlocking {
        val marker = UUID.randomUUID().toString()
        val stream = StreamingFixture(
            marker = marker,
            fileSize = 1024L * 1024,
            throttleBytesPerPeriod = 64L * 1024,
            throttlePeriodMillis = 500L,
        ).also { fixture = it }
        val server = ServerEndpoint(
            id = stream.serverId,
            name = "Worker stream fixture",
            version = "test",
            origin = stream.origin,
        ).also { endpoint = it }
        setCookie(server, "worker_session=$marker; Path=/")
        ownedTitle = stream.title
        val activity = ActivityScenario.launch(MainActivity::class.java).also { scenario = it }
        openDownloads(activity)
        val id = OfflineDownloads.enqueue(
            context,
            server,
            JSONObject().put("kind", "track").put("id", "target-$marker"),
            "original",
        ).also { jobId = it }

        awaitUi("a resumable partial transfer") {
            val live = OfflineDownloads.jobValues(context, setOf(id)).firstOrNull()
            live?.status == "downloading" && live.receivedBytes >= 512L * 1024
        }
        WorkManager.getInstance(context).cancelUniqueWork(REMOTE_WORK_NAME)
            .result.get(CLEANUP_TIMEOUT_SECONDS, TimeUnit.SECONDS)
        awaitUi("interrupted transfer reported as waiting rather than downloading") {
            val live = OfflineDownloads.jobValues(context, setOf(id)).firstOrNull()
            live?.status == "waiting" && live.errorCode == "interrupted" &&
                displayedRate(activity, stream.title) == null
        }
        val retainedBytes = requireNotNull(OfflineDownloads.jobValues(context, setOf(id)).firstOrNull()).receivedBytes
        screenshot(activity, "offline-download-interrupted-waiting")
        OfflineDownloads.resume(context, id)
        var resumedRate = 0.0
        awaitUi("measured speed from newly received resume bytes") {
            val live = OfflineDownloads.jobValues(context, setOf(id)).firstOrNull()
            resumedRate = displayedRate(activity, stream.title) ?: 0.0
            live?.status == "downloading" && live.receivedBytes > retainedBytes && resumedRate > 0
        }
        assertTrue(
            "Cached bytes inflated the resumed rate ($resumedRate B/s); ${diagnostics(id, stream)}",
            resumedRate <= 512L * 1024,
        )
        screenshot(activity, "offline-download-resumed-rate")
        awaitProductionWorker(id, stream, activity)
        assertTrue("The interrupted original did not resume with a range", stream.requestedRanges().any { it.startsWith("bytes=") })
        val imported = library.tracks().single { it.title == stream.title }
        assertImportedBytes(imported, stream)
    }

    private suspend fun awaitVisibleProgressBeforeCompletion(
        id: String,
        stream: StreamingFixture,
        activity: ActivityScenario<MainActivity>,
    ): Long {
        val deadline = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(SUB_MIB_UI_TIMEOUT_MILLIS)
        var lastIncomingBytes = 0L
        var capturedRawProgress = false
        while (System.nanoTime() < deadline) {
            val live = OfflineDownloads.jobValues(context, setOf(id)).firstOrNull()
            if (
                live != null &&
                live.status == "downloading" &&
                live.receivedBytes in 1 until live.totalBytes
            ) {
                lastIncomingBytes = live.receivedBytes
                if (!capturedRawProgress && live.receivedBytes >= SUB_MIB_SCREENSHOT_BYTES) {
                    screenshot(activity, "offline-download-sub-mib-raw-active")
                    capturedRawProgress = true
                }
                if (displayedIntermediateProgress(activity, stream.title) != null) {
                    screenshot(activity, "offline-download-sub-mib-progress")
                    return live.receivedBytes
                }
            }
            if (live?.status in setOf("failed", "partial", "cancelled", "paused", "completed")) {
                throw AssertionError(
                    "Response bytes advanced but native progress was not visible before completion " +
                        "(lastIncomingBytes=$lastIncomingBytes); ${diagnostics(id, stream)}",
                )
            }
            delay(SUB_MIB_POLL_INTERVAL_MILLIS)
        }
        throw AssertionError(
            "Timed out waiting for native progress while the slow response was unfinished " +
                "(lastIncomingBytes=$lastIncomingBytes); ${diagnostics(id, stream)}",
        )
    }


    private suspend fun awaitProductionWorker(
        id: String,
        stream: StreamingFixture,
        activity: ActivityScenario<MainActivity>,
    ): WorkerObservation {
        val observation = WorkerObservation()
        val deadline = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(WORKER_TIMEOUT_MILLIS)
        var nextUiObservation = 0L
        var completedAt = 0L

        while (System.nanoTime() < deadline) {
            val now = System.nanoTime()
            val job = OfflineDownloads.jobs.value.firstOrNull { it.id == id }

            val work = workInfos()
            work.forEach { info ->
                if (info.progress.getString("job_id") == id) {
                    observation.workIds += info.id
                }
                if (info.id in observation.workIds && info.state == WorkInfo.State.RUNNING) {
                    observation.sawRunningWork = true
                }
                if (info.id in observation.workIds && info.state == WorkInfo.State.SUCCEEDED) {
                    observation.sawSuccessfulWork = true
                }
            }

            if (now >= nextUiObservation) {
                displayedIntermediateProgress(activity, stream.title)?.let(observation.uiProgressValues::add)
                nextUiObservation = now + TimeUnit.MILLISECONDS.toNanos(UI_OBSERVATION_INTERVAL_MILLIS)
            }

            if (job?.status in setOf("failed", "partial", "cancelled", "paused")) {
                throw AssertionError("Worker reached a non-completed terminal state; ${diagnostics(id, stream, work)}")
            }
            if (job?.status == "completed") {
                if (completedAt == 0L) completedAt = now
                if (observation.sawSuccessfulWork) {
                    observation.completedAtNanos = completedAt
                    return observation
                }
            }
            delay(POLL_INTERVAL_MILLIS)
        }

        throw AssertionError("Timed out after $WORKER_TIMEOUT_MILLIS ms waiting for full worker completion; ${diagnostics(id, stream)}")
    }

    private fun workInfos(): List<WorkInfo> = WorkManager.getInstance(context)
        .getWorkInfosForUniqueWork(REMOTE_WORK_NAME)
        .get(WORK_QUERY_TIMEOUT_SECONDS, TimeUnit.SECONDS)

    private fun diagnostics(
        id: String,
        stream: StreamingFixture,
        knownWork: List<WorkInfo> = runCatching { workInfos() }.getOrDefault(emptyList()),
    ): String {
        val published = OfflineDownloads.jobs.value.firstOrNull { it.id == id }
        val live = runCatching { OfflineDownloads.jobValues(context, setOf(id)).firstOrNull() }.getOrNull()
        fun describe(job: OfflineDownloadJob?): String = if (job == null) {
            "missing"
        } else {
            "status=${job.status}, error=${job.errorCode}:${job.errorMessage}, bytes=${job.receivedBytes}/${job.totalBytes}, tracks=${job.completedTracks}/${job.totalTracks}"
        }
        val workValue = knownWork.joinToString(prefix = "[", postfix = "]") { info ->
            val progress = info.progress
            "${info.id}:${info.state}:stop=${info.stopReason}:job=${progress.getString("job_id")}:" +
                "bytes=${progress.getLong("received_bytes", 0L)}/${progress.getLong("total_bytes", 0L)}"
        }
        return "published={${describe(published)}}, live={${describe(live)}}, work=$workValue, " +
            "mediaRequests=${stream.mediaRequestCount.get()}, ranges=${stream.requestedRanges()}"
    }

    private suspend fun openDownloads(activity: ActivityScenario<MainActivity>) {
        awaitUi("download launcher") {
            var present = false
            activity.onActivity { present = it.findViewById<View>(R.id.downloads_button) != null }
            present
        }
        activity.onActivity { it.findViewById<View>(R.id.downloads_button).performClick() }
        awaitUi("native Downloads view") {
            var displayed = false
            activity.onActivity {
                displayed = it.findViewById<View>(R.id.offline_download_list)?.isShown == true
            }
            displayed
        }
    }

    private suspend fun awaitUi(label: String, condition: () -> Boolean) {
        val deadline = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(UI_TIMEOUT_MILLIS)
        while (System.nanoTime() < deadline) {
            if (condition()) return
            delay(POLL_INTERVAL_MILLIS)
        }
        throw AssertionError("Timed out waiting for $label")
    }

    private fun displayedIntermediateProgress(
        activity: ActivityScenario<MainActivity>,
        title: String,
    ): Int? {
        var displayed: Int? = null
        activity.onActivity { current ->
            val list = current.findViewById<ViewGroup>(R.id.offline_download_list) ?: return@onActivity
            val titleView = findText(list, title) ?: return@onActivity
            val card = titleView.parent as? ViewGroup ?: return@onActivity
            val progress = findView(card) { view ->
                view is ProgressBar && view.progress in 1 until view.max
            }
            if (titleView.isShown && progress?.isShown == true) {
                displayed = (progress as ProgressBar).progress
            }
        }
        return displayed
    }

    private fun displayedRate(activity: ActivityScenario<MainActivity>, title: String): Double? {
        var rate: Double? = null
        activity.onActivity { current ->
            val list = current.findViewById<ViewGroup>(R.id.offline_download_list) ?: return@onActivity
            val titleView = findText(list, title) ?: return@onActivity
            val card = titleView.parent as? ViewGroup ?: return@onActivity
            val label = findView(card) { it is TextView && RATE_VALUE.containsMatchIn(it.text) } as? TextView
                ?: return@onActivity
            if (label.isShown) {
                val match = RATE_VALUE.find(label.text) ?: return@onActivity
                val value = match.groupValues[1].replace(',', '.').toDoubleOrNull() ?: return@onActivity
                val unit = when (match.groupValues[2]) {
                    "KB" -> 1_000.0
                    "MB" -> 1_000_000.0
                    "GB" -> 1_000_000_000.0
                    "TB" -> 1_000_000_000_000.0
                    else -> 1.0
                }
                rate = value * unit
            }
        }
        return rate
    }

    private fun screenshot(activity: ActivityScenario<MainActivity>, name: String) {
        val rendered = CountDownLatch(1)
        activity.onActivity { current ->
            current.window.decorView.postOnAnimation {
                current.window.decorView.postOnAnimation { rendered.countDown() }
            }
        }
        assertTrue("Rendered Android frame did not commit", rendered.await(10, TimeUnit.SECONDS))
        instrumentation.waitForIdleSync()
        val bitmap = requireNotNull(instrumentation.uiAutomation.takeScreenshot()) {
            "Emulator screenshot unavailable"
        }
        val resolver = context.contentResolver
        val values = ContentValues().apply {
            put(MediaStore.Images.Media.DISPLAY_NAME, "$name.png")
            put(MediaStore.Images.Media.MIME_TYPE, "image/png")
            put(MediaStore.Images.Media.RELATIVE_PATH, "Pictures/jastreamer-android-smoke")
            put(MediaStore.Images.Media.IS_PENDING, 1)
        }
        val uri = requireNotNull(resolver.insert(MediaStore.Images.Media.EXTERNAL_CONTENT_URI, values))
        requireNotNull(resolver.openOutputStream(uri)).use { output ->
            check(bitmap.compress(Bitmap.CompressFormat.PNG, 100, output))
        }
        values.clear()
        values.put(MediaStore.Images.Media.IS_PENDING, 0)
        check(resolver.update(uri, values, null, null) == 1)
        bitmap.recycle()
    }

    private fun findText(root: View, value: String): TextView? =
        findView(root) { it is TextView && it.text.toString() == value } as? TextView

    private fun findView(root: View, predicate: (View) -> Boolean): View? {
        if (predicate(root)) return root
        if (root !is ViewGroup) return null
        for (index in 0 until root.childCount) {
            findView(root.getChildAt(index), predicate)?.let { return it }
        }
        return null
    }

    private fun assertImportedBytes(track: OfflineTrack, stream: StreamingFixture) {
        val digest = MessageDigest.getInstance("SHA-256")
        val buffer = ByteArray(64 * 1024)
        var offset = 0L
        library.openAudio(track.id).use { audio ->
            while (true) {
                val count = audio.input.read(buffer)
                if (count < 0) break
                for (index in 0 until count) {
                    val expected = StreamingFixture.byteAt(offset + index)
                    if (buffer[index] != expected) {
                        throw AssertionError(
                            "Imported original differs at byte ${offset + index}: expected=${expected.toInt() and 0xff}, actual=${buffer[index].toInt() and 0xff}",
                        )
                    }
                }
                digest.update(buffer, 0, count)
                offset += count
            }
        }
        assertEquals("Imported original was truncated", stream.fileSize, offset)
        assertEquals("Imported original hash differs", stream.digest, digest.digest().hex())
    }

    private suspend fun setCookie(server: ServerEndpoint, value: String) {
        val completed = CountDownLatch(1)
        val accepted = AtomicReference<Boolean>()
        withContext(Dispatchers.Main) {
            ProfileStore.getInstance()
                .getOrCreateProfile(EndpointPolicy.profileName(server))
                .cookieManager
                .setCookie(server.origin, value) { result ->
                    accepted.set(result)
                    completed.countDown()
                }
        }
        assertTrue("Timed out setting the isolated profile cookie", completed.await(10, TimeUnit.SECONDS))
        assertEquals(true, accepted.get())
    }

    private suspend fun clearCookies(server: ServerEndpoint) {
        val completed = CountDownLatch(1)
        withContext(Dispatchers.Main) {
            ProfileStore.getInstance()
                .getOrCreateProfile(EndpointPolicy.profileName(server))
                .cookieManager
                .removeAllCookies { completed.countDown() }
        }
        assertTrue("Timed out clearing the isolated profile cookie", completed.await(10, TimeUnit.SECONDS))
    }

    private data class WorkerObservation(
        val workIds: MutableSet<UUID> = linkedSetOf(),
        val uiProgressValues: MutableSet<Int> = linkedSetOf(),
        var sawRunningWork: Boolean = false,
        var sawSuccessfulWork: Boolean = false,
        var completedAtNanos: Long = 0L,
    )

    private class StreamingFixture(
        val marker: String,
        val fileSize: Long = FILE_SIZE,
        private val throttleBytesPerPeriod: Long = THROTTLE_BYTES_PER_PERIOD,
        private val throttlePeriodMillis: Long = THROTTLE_PERIOD_MILLIS,
    ) : AutoCloseable {
        val serverId: String = UUID.randomUUID().toString()
        val remoteId = "remote-$marker"
        val title = "worker-stream-$marker"
        val digest: String = patternDigest(fileSize)
        val mediaRequestCount = AtomicInteger()
        val mediaStartedNanos = AtomicLong()
        private val ranges = mutableListOf<String>()
        private val server = MockWebServer()
        val origin: String

        init {
            server.dispatcher = object : Dispatcher() {
                override fun dispatch(request: RecordedRequest): MockResponse {
                    val path = request.path
                    if (path == "/api/v1/discovery") return json(discovery())
                    if (!authorized(request)) return unauthorized()
                    return when {
                        path == "/api/v1/session" -> json(session())
                        request.method == "POST" && path == "/api/v1/downloads" -> json(manifest())
                        request.method == "GET" && path == "/api/v1/downloads/$remoteId" -> json(manifest())
                        request.method == "DELETE" && path == "/api/v1/downloads/$remoteId" ->
                            MockResponse().setResponseCode(204)
                        request.method == "GET" && path == "/api/v1/downloads/$remoteId/files/0" -> media(request)
                        else -> MockResponse().setResponseCode(404)
                    }
                }
            }
            server.start()
            val url = server.url("/")
            origin = "${url.scheme}://${url.host}:${url.port}"
        }

        override fun close() {
            server.shutdown()
        }

        fun requestedRanges(): List<String> = synchronized(ranges) { ranges.toList() }

        private fun authorized(request: RecordedRequest): Boolean =
            request.getHeader("Cookie")
                ?.split(';')
                ?.any { it.trim() == "worker_session=$marker" } == true

        private fun discovery(): JSONObject = JSONObject()
            .put("product", "jastreamer")
            .put("protocol", 1)
            .put("id", serverId)
            .put("name", "Worker stream fixture")
            .put("version", "test")

        private fun session(): JSONObject = JSONObject()
            .put("authenticated", true)
            .put("user", JSONObject().put("id", "account-$marker"))

        private fun manifest(): JSONObject = JSONObject()
            .put("id", remoteId)
            .put("kind", "track")
            .put("title", title)
            .put("quality", "original")
            .put("status", "ready")
            .put("tracks", JSONArray().put(JSONObject()
                .put("index", 0)
                .put("status", "ready")
                .put("title", title)
                .put("artist", "Worker stream artist")
                .put("album", "Worker stream album")
                .put("album_artist", "Worker stream artist")
                .put("disc", 1)
                .put("track", 1)
                .put("duration_ms", 180_000)
                .put("source_version", "version-$marker")
                .put("quality", "original")
                .put("mime", "audio/flac")
                .put("codec", "flac")
                .put("byte_size", fileSize)
                .put("sha256", digest)
                .put("media_path", "/api/v1/downloads/$remoteId/files/0")
                .put("artwork_path", "")))

        private fun media(request: RecordedRequest): MockResponse {
            if (request.getHeader("Accept-Encoding") != "identity") {
                return MockResponse().setResponseCode(400)
            }
            val range = request.getHeader("Range")
            val start = if (range == null) {
                0L
            } else {
                RANGE.matchEntire(range)?.groupValues?.get(1)?.toLongOrNull()
                    ?: return MockResponse().setResponseCode(416)
            }
            if (start !in 0 until fileSize) return MockResponse().setResponseCode(416)
            if (start > 0L && request.getHeader("If-Range") != "\"$digest\"") {
                return MockResponse().setResponseCode(412)
            }

            synchronized(ranges) { ranges += range ?: "full" }
            mediaRequestCount.incrementAndGet()
            mediaStartedNanos.compareAndSet(0L, System.nanoTime())
            val length = fileSize - start
            val body = Buffer()
            var offset = start
            while (offset < fileSize) {
                val count = minOf(GENERATION_CHUNK_SIZE.toLong(), fileSize - offset).toInt()
                val chunk = ByteArray(count) { index -> byteAt(offset + index) }
                body.write(chunk)
                offset += count
            }

            return MockResponse()
                .setResponseCode(if (start == 0L) 200 else 206)
                .setHeader("Content-Type", "audio/flac")
                .setHeader("Content-Length", length)
                .setHeader("ETag", "\"$digest\"")
                .setHeader("Accept-Ranges", "bytes")
                .apply {
                    if (start > 0L) setHeader("Content-Range", "bytes $start-${fileSize - 1}/$fileSize")
                }
                .setBody(body)
                .throttleBody(throttleBytesPerPeriod, throttlePeriodMillis, TimeUnit.MILLISECONDS)
        }

        private fun unauthorized(): MockResponse = MockResponse()
            .setResponseCode(401)
            .setHeader("Content-Type", "application/json")
            .setBody(JSONObject().put("error", JSONObject().put("code", "auth_required")).toString())

        private fun json(body: JSONObject): MockResponse = MockResponse()
            .setResponseCode(200)
            .setHeader("Content-Type", "application/json")
            .setBody(body.toString())

        companion object {
            const val FILE_SIZE = 32L * 1024 * 1024
            private const val GENERATION_CHUNK_SIZE = 64 * 1024
            private const val THROTTLE_BYTES_PER_PERIOD = 512L * 1024
            private const val THROTTLE_PERIOD_MILLIS = 125L
            private val RANGE = Regex("bytes=(\\d+)-")

            fun byteAt(offset: Long): Byte {
                val mixed = offset xor (offset ushr 7) xor (offset ushr 17) xor 0x5aL
                return (mixed and 0xff).toByte()
            }

            private fun patternDigest(fileSize: Long): String {
                val digest = MessageDigest.getInstance("SHA-256")
                val chunk = ByteArray(GENERATION_CHUNK_SIZE)
                var offset = 0L
                while (offset < fileSize) {
                    val count = minOf(chunk.size.toLong(), fileSize - offset).toInt()
                    for (index in 0 until count) chunk[index] = byteAt(offset + index)
                    digest.update(chunk, 0, count)
                    offset += count
                }
                return digest.digest().hex()
            }
        }
    }

    companion object {
        private const val REMOTE_WORK_NAME = "offline-music-imports"
        private const val POLL_INTERVAL_MILLIS = 200L
        private const val UI_OBSERVATION_INTERVAL_MILLIS = 500L
        private const val UI_TIMEOUT_MILLIS = 10_000L
        // The fixture transfers 32 MiB at 4 MiB/s (about 8 seconds); 60 seconds leaves
        // ordinary emulator scheduling, import fsync, and WorkManager persistence headroom.
        private const val WORKER_TIMEOUT_MILLIS = 60_000L
        private const val MINIMUM_OBSERVED_STREAM_MILLIS = 4_000L
        private const val WORK_QUERY_TIMEOUT_SECONDS = 3L
        private const val CLEANUP_TIMEOUT_SECONDS = 5L
        private const val SUB_MIB_FILE_SIZE = 512L * 1024
        private const val SUB_MIB_THROTTLE_BYTES = 64L * 1024
        private const val SUB_MIB_THROTTLE_PERIOD_MILLIS = 500L
        private const val SUB_MIB_POLL_INTERVAL_MILLIS = 100L
        // Eight 64 KiB periods take about four seconds; 15 seconds covers worker startup
        // while still failing promptly if only the final completion publication reaches UI.
        private const val SUB_MIB_UI_TIMEOUT_MILLIS = 15_000L
        private const val SUB_MIB_SCREENSHOT_BYTES = 128L * 1024
        private val RATE_VALUE = Regex("""(\d+(?:[.,]\d+)?)\s+([KMGT]?B)/s\b""")

        private fun ByteArray.hex(): String = joinToString("") { "%02x".format(it.toInt() and 0xff) }
    }
}
