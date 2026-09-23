package io.jastreamer.android

import android.app.AlertDialog
import android.content.Context
import android.content.res.Configuration
import android.graphics.Color
import android.graphics.drawable.GradientDrawable
import android.os.Bundle
import android.os.SystemClock
import android.text.InputType
import android.text.TextUtils
import android.view.Gravity
import android.view.View
import android.view.inputmethod.EditorInfo
import android.view.inputmethod.InputMethodManager
import android.widget.Button
import android.widget.EditText
import android.widget.FrameLayout
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.ScrollView
import android.widget.TextView
import androidx.activity.ComponentActivity
import androidx.activity.OnBackPressedCallback
import androidx.core.view.ViewCompat
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import java.util.Locale
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.supervisorScope
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.sync.withPermit
import kotlinx.coroutines.withContext

class MainActivity : ComponentActivity() {
    private lateinit var store: RecentServerStore
    private val probe = ServerProbe()
    private lateinit var root: LinearLayout
    private lateinit var header: LinearLayout
    private lateinit var content: FrameLayout
    private lateinit var selector: ScrollView
    private var address: EditText? = null
    private lateinit var errorView: TextView
    private lateinit var discoveredRows: LinearLayout
    private lateinit var recentRows: LinearLayout
    private lateinit var progress: ProgressBar
    private lateinit var savedMusicSummary: TextView
    private var manualDialog: FrameLayout? = null
    private var manualDraft = ""
    private var entryScreen = ENTRY_HOME
    private var language = "en"
    private lateinit var stringsContext: Context
    private val languageWrites = Mutex()
    private var recentEpoch = 0L
    private var recent = emptyList<ServerEndpoint>()
    private var discovered = emptyList<ServerEndpoint>()
    private var discoveryError: ClientException? = null
    private var error: ClientException? = null
    private var availability = emptyMap<String, Boolean>()
    private var selected: ServerEndpoint? = null
    private var remote: RemoteServerView? = null
    private var offline: OfflineMusicView? = null
    private var connecting = false
    private var generation = 0L
    private var remoteEpoch = 0L
    private var connectionJob: Job? = null
    private var chooserJobs: Job? = null
    private var savedSummaryJob: Job? = null
    private var restoreTarget: Pair<String, String?>? = null
    private var retryTarget: Pair<String, String?>? = null
    private var foreground = false
    private var destroyed = false
    private var recoveryFailures = 0
    private var recoveryDeadlineMillis = 0L
    private var remoteRequiresReload = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        store = RecentServerStore(applicationContext)
        try {
            language = store.language()
        } catch (failure: ClientException) {
            error = failure
        }
        updateStringsContext()
        WindowCompat.setDecorFitsSystemWindows(window, false)
        root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setBackgroundColor(BACKGROUND)
        }
        header = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            setPadding(dp(8), dp(4), dp(8), dp(4))
            minimumHeight = dp(56)
        }
        content = FrameLayout(this).apply { id = R.id.remote_container }
        root.addView(header, LinearLayout.LayoutParams(-1, -2))
        root.addView(content, LinearLayout.LayoutParams(-1, 0, 1f))
        setContentView(root)
        ViewCompat.setOnApplyWindowInsetsListener(root) { view, insets ->
            val safe = insets.getInsets(
                WindowInsetsCompat.Type.systemBars() or
                    WindowInsetsCompat.Type.displayCutout() or WindowInsetsCompat.Type.ime(),
            )
            view.setPadding(safe.left, safe.top, safe.right, safe.bottom)
            // The root owns these insets; WebView must not apply the same safe area again.
            WindowInsetsCompat.CONSUMED
        }
        ViewCompat.requestApplyInsets(root)
        manualDraft = savedInstanceState?.getString("address").orEmpty()
        val restoredOrigin = savedInstanceState?.getString("selected_origin")
        val restoredId = savedInstanceState?.getString("selected_id")
        if (restoredOrigin != null && restoredId != null) {
            restoreTarget = restoredOrigin to restoredId
            entryScreen = ENTRY_CHOOSER
        } else {
            entryScreen = savedInstanceState?.getString("entry_screen") ?: ENTRY_HOME
        }
        renderEntry()
        renderHeader()
        if (
            entryScreen == ENTRY_CHOOSER &&
            savedInstanceState?.getBoolean("manual_open") == true
        ) {
            content.post { if (!destroyed) showManualAddress() }
        }
        onBackPressedDispatcher.addCallback(this, object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() {
                when {
                    manualDialog?.isShown == true -> dismissManualAddress()
                    offline?.handleBack() == true -> Unit
                    offline != null -> showHome()
                    remote?.canGoBack() == true -> remote?.goBack()
                    remote != null -> showChooser()
                    connecting -> showHome()
                    entryScreen == ENTRY_CHOOSER -> showHome()
                    else -> {
                        isEnabled = false
                        onBackPressedDispatcher.onBackPressed()
                        isEnabled = true
                    }
                }
            }
        })
        lifecycleScope.launch {
            try {
                val localLibrary = withContext(Dispatchers.IO) { OfflineLibrary.get(applicationContext) }
                localLibrary.changes.collectLatest { refreshSavedMusicSummary() }
            } catch (_: RuntimeException) {
                refreshSavedMusicSummary()
            }
        }
        if (savedInstanceState?.getBoolean("offline_visible") == true) {
            showOffline(
                savedInstanceState.getString("offline_screen") ?: OfflineMusicView.SCREEN_LIBRARY,
                savedInstanceState,
            )
        }
    }

    override fun onStart() {
        super.onStart()
        foreground = true
        val target = restoreTarget
        restoreTarget = null
        if (offline != null) {
            // The native library is process-local and needs no network revalidation.
        } else if (target != null) {
            connect(target.first, target.second)
        } else if (remote != null) {
            revalidateConnection()
        } else if (entryScreen == ENTRY_CHOOSER) {
            startChooserJobs()
        }
    }

    override fun onResume() {
        super.onResume()
        if (offline == null) remote?.resume()
    }

    override fun onPause() {
        remote?.pause()
        super.onPause()
    }

    override fun onStop() {
        foreground = false
        stopChooserJobs()
        generation++
        connectionJob?.cancel()
        connectionJob = null
        remote?.pause()
        remote?.visibility = View.INVISIBLE
        if (connecting) {
            connecting = false
            progress.visibility = View.GONE
            findViewById<Button>(R.id.manual_address_button)?.isEnabled = true
            renderHeader()
        }
        super.onStop()
    }

    override fun onSaveInstanceState(outState: Bundle) {
        manualDraft = address?.text?.toString() ?: manualDraft
        outState.putString("address", manualDraft)
        outState.putString("entry_screen", entryScreen)
        outState.putBoolean("manual_open", manualDialog?.isShown == true)
        selected?.let {
            outState.putString("selected_origin", it.origin)
            outState.putString("selected_id", it.id)
        }
        offline?.let {
            outState.putBoolean("offline_visible", true)
            it.saveState(outState)
        }
        super.onSaveInstanceState(outState)
    }

    override fun onConfigurationChanged(newConfig: Configuration) {
        super.onConfigurationChanged(newConfig)
        // Keep the live WebView and its unsaved form/tab state through rotation.
        renderHeader()
        offline?.updateForConfiguration()
        ViewCompat.requestApplyInsets(root)
    }

    override fun onDestroy() {
        destroyed = true
        generation++
        dismissManualAddress()
        disposeRemote()
        disposeOffline()
        super.onDestroy()
    }

    private fun connect(input: String, expectedId: String? = null) {
        if (expectedId == null) manualDraft = input
        dismissManualAddress()
        generation++
        val attempt = generation
        connectionJob?.cancel()
        disposeOffline()
        stopChooserJobs()
        resetRecovery()
        disposeRemote()
        selected = null
        retryTarget = input to expectedId
        connecting = true
        error = null
        entryScreen = ENTRY_CHOOSER
        selector.visibility = View.VISIBLE
        errorView.visibility = View.GONE
        progress.visibility = View.VISIBLE
        findViewById<Button>(R.id.manual_address_button)?.isEnabled = false
        renderHeader()
        connectionJob = lifecycleScope.launch {
            try {
                val server = probeForConnection(input, expectedId)
                withContext(Dispatchers.IO) { store.remember(server) }
                if (attempt != generation || !foreground) return@launch
                val epoch = ++remoteEpoch
                lateinit var view: RemoteServerView
                view = RemoteServerView(
                    this@MainActivity,
                    server,
                    language,
                    canStartNativePlayback = {
                        epoch == remoteEpoch &&
                            foreground &&
                            !destroyed &&
                            lifecycle.currentState.isAtLeast(Lifecycle.State.RESUMED)
                    },
                    onLoaded = {
                        if (epoch == remoteEpoch && !destroyed) {
                            remoteRequiresReload = false
                            resetRecovery()
                            progress.visibility = View.GONE
                        }
                    },
                    onError = { failure ->
                        if (epoch == remoteEpoch && !destroyed) {
                            if (failure.code == ClientErrorCode.NAVIGATION_BLOCKED) {
                                android.widget.Toast.makeText(
                                    this@MainActivity, errorText(failure), android.widget.Toast.LENGTH_LONG,
                                ).show()
                            } else {
                                remoteRequiresReload = true
                                recoverConnection(server, view, initialFailure = failure)
                            }
                        }
                    },
                    onLanguageChanged = { next ->
                        if (epoch == remoteEpoch && !destroyed) updateLanguage(next, false)
                    },
                    onOpenLibrary = {
                        if (epoch == remoteEpoch && foreground && !destroyed) {
                            showOffline(OfflineMusicView.SCREEN_LIBRARY)
                        }
                    },
                    onOpenDownloads = {
                        if (epoch == remoteEpoch && foreground && !destroyed) {
                            showOffline(OfflineMusicView.SCREEN_DOWNLOADS)
                        }
                    },
                )
                remote = view
                remoteRequiresReload = true
                selected = server
                retryTarget = server.origin to server.id
                connecting = false
                content.addView(view, FrameLayout.LayoutParams(-1, -1))
                selector.visibility = View.GONE
                progress.bringToFront()
                renderHeader()
                view.load()
                view.resume()
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (failure: ClientException) {
                if (attempt == generation && !destroyed) showChooser(failure)
            }
        }
    }

    private suspend fun probeForConnection(input: String, expectedId: String?): ServerEndpoint {
        while (true) {
            try {
                return probe.probe(input, expectedId).also { resetRecovery() }
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (failure: ClientException) {
                if (expectedId == null || !acceptRecoveryFailure(failure)) throw failure
                delay(recoveryDelayMillis())
            }
        }
    }

    private fun revalidateConnection() {
        val server = selected ?: return
        val view = remote ?: return
        recoverConnection(server, view)
    }

    private fun recoverConnection(
        server: ServerEndpoint,
        view: RemoteServerView,
        initialFailure: ClientException? = null,
    ) {
        if (initialFailure != null && !acceptRecoveryFailure(initialFailure)) {
            showChooser(initialFailure)
            return
        }
        val attempt = ++generation
        connectionJob?.cancel()
        progress.visibility = View.VISIBLE
        connectionJob = lifecycleScope.launch {
            var lastFailure = initialFailure
            while (attempt == generation && !destroyed && remote === view) {
                if (!foreground) return@launch
                if (lastFailure != null) {
                    if (SystemClock.elapsedRealtime() >= recoveryDeadlineMillis) {
                        showChooser(lastFailure)
                        return@launch
                    }
                    delay(recoveryDelayMillis())
                }
                try {
                    probe.probe(server.origin, server.id)
                    if (attempt != generation || !foreground || remote !== view) return@launch
                    view.visibility = View.VISIBLE
                    view.resume()
                    if (remoteRequiresReload) {
                        view.retryLoad()
                    } else {
                        resetRecovery()
                        progress.visibility = View.GONE
                    }
                    return@launch
                } catch (cancelled: CancellationException) {
                    throw cancelled
                } catch (failure: ClientException) {
                    if (attempt != generation || remote !== view) return@launch
                    if (!acceptRecoveryFailure(failure)) {
                        showChooser(failure)
                        return@launch
                    }
                    lastFailure = failure
                }
            }
        }
    }

    private fun acceptRecoveryFailure(failure: ClientException): Boolean {
        if (!failure.retryable) return false
        val now = SystemClock.elapsedRealtime()
        if (recoveryDeadlineMillis == 0L) recoveryDeadlineMillis = now + RECOVERY_WINDOW_MILLIS
        recoveryFailures++
        return recoveryFailures <= MAX_RECOVERY_FAILURES && now < recoveryDeadlineMillis
    }

    private fun recoveryDelayMillis(): Long = when (recoveryFailures) {
        0, 1 -> 250L
        2 -> 500L
        else -> 1_000L
    }

    private fun resetRecovery() {
        recoveryFailures = 0
        recoveryDeadlineMillis = 0L
    }

    private fun disposeRemote() {
        val previous = remote ?: return
        // Read the language cookie before invalidating this view's callback epoch.
        previous.pause()
        remoteEpoch++
        remote = null
        resetRecovery()
        content.removeView(previous)
        previous.dispose()
    }

    private fun disposeOffline() {
        val previous = offline ?: return
        offline = null
        content.removeView(previous)
    }

    private fun showOffline(screen: String, savedState: Bundle? = null) {
        generation++
        connectionJob?.cancel()
        connectionJob = null
        stopChooserJobs()
        dismissManualAddress()
        disposeRemote()
        disposeOffline()
        selected = null
        connecting = false
        entryScreen = ENTRY_HOME
        selector.visibility = View.GONE
        progress.visibility = View.GONE
        header.visibility = View.GONE
        val view = OfflineMusicView(
            activity = this,
            strings = stringsContext,
            onServers = { showChooser() },
            language = language,
            onLanguage = { next -> updateLanguage(next, false) },
            initialScreen = screen,
            savedState = savedState,
        )
        offline = view
        content.addView(view, FrameLayout.LayoutParams(-1, -1))
    }

    private fun startChooserJobs() {
        if (!foreground || chooserJobs?.isActive == true) return
        chooserJobs = lifecycleScope.launch {
            launch {
                ServerDiscovery(applicationContext, probe).observe().collect { state ->
                    discovered = state.servers
                    discoveryError = state.error
                    renderServerLists()
                }
            }
            launch {
                while (isActive) {
                    refreshRecents()
                    delay(60_000)
                }
            }
        }
    }

    private fun stopChooserJobs() {
        chooserJobs?.cancel()
        chooserJobs = null
    }

    private fun refreshSavedMusicSummary() {
        savedSummaryJob?.cancel()
        savedSummaryJob = lifecycleScope.launch {
            val counts = try {
                withContext(Dispatchers.IO) {
                    val local = OfflineLibrary.get(applicationContext).tracks()
                    local.size to local.map { it.albumArtist to it.album }.distinct().size
                }
            } catch (_: RuntimeException) {
                null
            }
            if (::savedMusicSummary.isInitialized) {
                savedMusicSummary.text = counts?.let {
                    text(R.string.offline_saved_counts, it.first, it.second)
                } ?: text(R.string.offline_saved_counts_unavailable)
            }
        }
    }

    private fun showHome() {
        generation++
        connectionJob?.cancel()
        connectionJob = null
        stopChooserJobs()
        dismissManualAddress()
        disposeRemote()
        disposeOffline()
        selected = null
        connecting = false
        error = null
        retryTarget = null
        entryScreen = ENTRY_HOME
        header.visibility = View.VISIBLE
        renderEntry()
        renderHeader()
    }

    private fun showChooser(failure: ClientException? = null) {
        generation++
        connectionJob?.cancel()
        connectionJob = null
        dismissManualAddress()
        disposeRemote()
        disposeOffline()
        selected = null
        connecting = false
        error = failure
        entryScreen = ENTRY_CHOOSER
        header.visibility = View.VISIBLE
        renderEntry()
        renderHeader()
        startChooserJobs()
    }

    private suspend fun refreshRecents() {
        val refresh = ++recentEpoch
        try {
            val saved = withContext(Dispatchers.IO) { store.list() }
            if (refresh != recentEpoch || destroyed) return
            recent = saved
            renderServerLists()
            val limit = Semaphore(3)
            val checked = supervisorScope {
                saved.map { server ->
                    async {
                        limit.withPermit {
                            val reachable = try {
                                probe.probe(server.origin, server.id)
                                true
                            } catch (cancelled: CancellationException) {
                                throw cancelled
                            } catch (_: ClientException) {
                                false
                            }
                            key(server) to reachable
                        }
                    }
                }.awaitAll().toMap()
            }
            if (refresh != recentEpoch || destroyed) return
            availability = checked
            renderServerLists()
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (failure: ClientException) {
            if (refresh != recentEpoch || destroyed) return
            error = failure
            if (remote == null) {
                errorView.text = errorText(failure)
                errorView.visibility = View.VISIBLE
            }
        }
    }

    private fun updateLanguage(next: String, updateRemote: Boolean) {
        if (next != "en" && next != "ko") return
        lifecycleScope.launch {
            try {
                languageWrites.withLock {
                    if (next != language) {
                        withContext(Dispatchers.IO) { store.setLanguage(next) }
                        if (destroyed) return@withLock
                        val offlineState = offline?.let { view ->
                            Bundle().also(view::saveState)
                        }
                        val reopenManual = manualDialog?.isShown == true
                        manualDraft = address?.text?.toString() ?: manualDraft
                        dismissManualAddress()
                        language = next
                        updateStringsContext()
                        renderEntry()
                        renderHeader()
                        if (reopenManual && entryScreen == ENTRY_CHOOSER) showManualAddress()
                        if (offlineState != null) {
                            showOffline(
                                offlineState.getString("offline_screen") ?: OfflineMusicView.SCREEN_LIBRARY,
                                offlineState,
                            )
                        }
                    }
                    if (updateRemote) remote?.setLanguage(next)
                }
            } catch (failure: ClientException) {
                android.widget.Toast.makeText(
                    this@MainActivity, errorText(failure), android.widget.Toast.LENGTH_LONG,
                ).show()
            }
        }
    }

    private fun renderHeader() {
        header.removeAllViews()
        header.addView(ImageView(this).apply {
            setImageResource(R.mipmap.ic_launcher)
            importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO
        }, LinearLayout.LayoutParams(dp(32), dp(32)).apply { marginEnd = dp(8) })
        header.addView(LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            addView(label(selected?.name ?: "jastreamer", 16f).apply {
                maxLines = 1
                ellipsize = TextUtils.TruncateAt.END
            })
            addView(label(selected?.origin ?: text(R.string.android_control), 11f).apply {
                maxLines = 1
                ellipsize = TextUtils.TruncateAt.MIDDLE
                setTextColor(MUTED)
            })
        }, LinearLayout.LayoutParams(0, -2, 1f))
        when {
            remote != null -> {
                header.addView(button(text(R.string.servers)) { showChooser() }.apply {
                    id = R.id.change_server_button
                    contentDescription = text(R.string.change_server)
                })
            }
            entryScreen == ENTRY_CHOOSER -> {
                header.addView(button(text(R.string.back_home)) { showHome() }.apply {
                    id = R.id.server_chooser_back_button
                })
            }
        }
        header.addView(button(if (language == "ko") "한국어" else "English") {
            AlertDialog.Builder(this)
                .setTitle(text(R.string.language))
                .setSingleChoiceItems(arrayOf("English", "한국어"), if (language == "ko") 1 else 0) { dialog, index ->
                    dialog.dismiss()
                    updateLanguage(if (index == 1) "ko" else "en", true)
                }.show()
        })
    }

    private fun renderEntry() {
        if (::selector.isInitialized) content.removeView(selector)
        if (::progress.isInitialized) content.removeView(progress)
        address = null
        val body = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(20), dp(20), dp(20), dp(32))
        }
        if (entryScreen == ENTRY_CHOOSER) renderChooser(body) else renderHome(body)
        selector = ScrollView(this).apply {
            isFillViewport = true
            addView(body)
            visibility = if (remote == null) View.VISIBLE else View.GONE
        }
        content.addView(selector, FrameLayout.LayoutParams(-1, -1))
        progress = ProgressBar(this).apply {
            contentDescription = text(R.string.verifying)
            visibility = if (connecting) View.VISIBLE else View.GONE
        }
        content.addView(progress, FrameLayout.LayoutParams(dp(48), dp(48), Gravity.CENTER))
        if (entryScreen == ENTRY_CHOOSER) renderServerLists()
    }

    private fun renderHome(body: LinearLayout) {
        body.addView(label(text(R.string.playback_home_title), 28f))
        body.addView(label(text(R.string.playback_home_detail), 14f).apply {
            setTextColor(MUTED)
            setPadding(0, dp(8), 0, dp(24))
        })
        body.addView(entryCard(
            id = R.id.server_playback_button,
            title = text(R.string.server_playback),
            detail = text(R.string.server_playback_detail),
            accent = true,
        ) { showChooser() }, cardLayoutParams())
        val localCard = entryCard(
            id = R.id.saved_music_button,
            title = text(R.string.local_playback),
            detail = text(R.string.local_playback_detail),
            accent = false,
        ) { showOffline(OfflineMusicView.SCREEN_LIBRARY) }
        savedMusicSummary = label(text(R.string.offline_saved_counts_unavailable), 15f).apply {
            id = R.id.saved_music_summary
            setTextColor(FOREGROUND)
            setPadding(0, dp(14), 0, dp(4))
        }
        localCard.addView(savedMusicSummary)
        localCard.addView(button(text(R.string.offline_downloads)) {
            showOffline(OfflineMusicView.SCREEN_DOWNLOADS)
        }.apply {
            id = R.id.downloads_button
        }, LinearLayout.LayoutParams(-1, -2).apply { topMargin = dp(12) })
        localCard.addView(label(text(R.string.offline_downloads_entry_detail), 12f).apply {
            setTextColor(MUTED)
            setPadding(dp(4), dp(2), dp(4), 0)
        })
        body.addView(localCard, cardLayoutParams())
        refreshSavedMusicSummary()
    }

    private fun renderChooser(body: LinearLayout) {
        body.addView(label(text(R.string.select_server), 28f))
        body.addView(label(text(R.string.selection_detail), 14f).apply {
            setTextColor(MUTED)
            setPadding(0, dp(8), 0, dp(20))
        })
        body.addView(button(text(R.string.connect_by_address)) { showManualAddress() }.apply {
            id = R.id.manual_address_button
        }, LinearLayout.LayoutParams(-1, -2))
        errorView = label(error?.let(::errorText).orEmpty(), 14f).apply {
            id = R.id.connection_error
            setTextColor(ERROR)
            setPadding(0, dp(12), 0, dp(4))
            accessibilityLiveRegion = View.ACCESSIBILITY_LIVE_REGION_POLITE
            visibility = if (error == null) View.GONE else View.VISIBLE
        }
        body.addView(errorView)
        body.addView(button(text(R.string.retry)) {
            retryTarget?.let { connect(it.first, it.second) }
        }.apply {
            visibility = if (error != null && retryTarget != null) View.VISIBLE else View.GONE
        }, LinearLayout.LayoutParams(-1, -2))
        body.addView(sectionTitle(text(R.string.discovered_servers)))
        discoveredRows = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        body.addView(discoveredRows)
        body.addView(sectionTitle(text(R.string.recent_servers)))
        recentRows = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        body.addView(recentRows)
    }

    private fun showManualAddress() {
        if (connecting || remote != null || entryScreen != ENTRY_CHOOSER || manualDialog?.isShown == true) return
        val field = EditText(this).apply {
            id = R.id.server_address
            setTextColor(FOREGROUND)
            setHintTextColor(MUTED)
            hint = "music-server.local:8080"
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_URI
            setSingleLine(true)
            imeOptions = EditorInfo.IME_ACTION_GO
            minimumHeight = dp(48)
            setText(manualDraft)
            setSelection(text.length)
        }
        address = field
        val panel = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            background = cardBackground(true)
            setPadding(dp(24), dp(20), dp(24), dp(20))
            isClickable = true
            addView(label(text(R.string.connect_by_address), 22f))
            addView(label(text(R.string.server_address), 14f).apply {
                labelFor = R.id.server_address
                setPadding(0, dp(16), 0, dp(4))
            })
            addView(field, LinearLayout.LayoutParams(-1, -2))
            addView(label(text(R.string.http_notice), 12f).apply {
                setTextColor(MUTED)
                setPadding(0, dp(8), 0, dp(12))
            })
        }
        val connectButton = button(text(R.string.verify_connect)) {
            connect(field.text.toString())
        }.apply { id = R.id.connect_button }
        panel.addView(connectButton, LinearLayout.LayoutParams(-1, -2))
        panel.addView(button(text(R.string.cancel)) { dismissManualAddress() }.apply {
            id = R.id.manual_cancel_button
        }, LinearLayout.LayoutParams(-1, -2))
        field.setOnEditorActionListener { _, action, _ ->
            if (action == EditorInfo.IME_ACTION_GO) {
                connectButton.performClick()
                true
            } else {
                false
            }
        }
        val scroll = ScrollView(this).apply {
            addView(panel, FrameLayout.LayoutParams(-1, -2))
        }
        val overlay = FrameLayout(this).apply {
            setBackgroundColor(Color.argb(196, 0, 0, 0))
            isClickable = true
            isFocusable = true
            addView(scroll, FrameLayout.LayoutParams(-1, -2, Gravity.CENTER).apply {
                leftMargin = dp(20)
                rightMargin = dp(20)
            })
        }
        ViewCompat.setAccessibilityPaneTitle(overlay, text(R.string.connect_by_address))
        selector.importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO_HIDE_DESCENDANTS
        header.importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO_HIDE_DESCENDANTS
        manualDialog = overlay
        content.addView(overlay, FrameLayout.LayoutParams(-1, -1))
        overlay.requestFocus()
    }

    private fun dismissManualAddress() {
        val overlay = manualDialog ?: return
        manualDraft = address?.text?.toString() ?: manualDraft
        address?.windowToken?.let { token ->
            (getSystemService(Context.INPUT_METHOD_SERVICE) as InputMethodManager)
                .hideSoftInputFromWindow(token, 0)
        }
        manualDialog = null
        address = null
        content.removeView(overlay)
        if (::selector.isInitialized) {
            selector.importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_AUTO
        }
        header.importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_AUTO
    }

    private fun entryCard(
        id: Int,
        title: String,
        detail: String,
        accent: Boolean,
        action: () -> Unit,
    ) = LinearLayout(this).apply {
        this.id = id
        orientation = LinearLayout.VERTICAL
        minimumHeight = dp(132)
        isClickable = true
        isFocusable = true
        background = cardBackground(accent)
        setPadding(dp(20), dp(20), dp(20), dp(20))
        setOnClickListener { action() }
        addView(label(title, 21f))
        addView(label(detail, 14f).apply {
            setTextColor(MUTED)
            setPadding(0, dp(8), 0, 0)
        })
    }

    private fun renderServerLists() {
        if (
            !::discoveredRows.isInitialized ||
            remote != null ||
            entryScreen != ENTRY_CHOOSER
        ) return
        discoveredRows.removeAllViews()
        recentRows.removeAllViews()
        if (discovered.isEmpty()) {
            discoveredRows.addView(label(
                discoveryError?.let(::errorText) ?: text(R.string.no_discovered), 14f,
            ).apply {
                setTextColor(MUTED)
                setPadding(dp(4), dp(4), dp(4), dp(8))
            })
        }
        discovered.forEach { addServerRow(discoveredRows, it, false) }
        if (recent.isEmpty()) {
            recentRows.addView(label(text(R.string.no_recent), 14f).apply {
                setTextColor(MUTED)
                setPadding(dp(4), dp(4), dp(4), dp(8))
            })
        }
        recent.forEach { addServerRow(recentRows, it, true) }
    }

    private fun addServerRow(parent: LinearLayout, server: ServerEndpoint, removable: Boolean) {
        val availableNow = !removable || availability[key(server)] == true
        val status = if (availableNow) text(R.string.available)
            else if (availability[key(server)] == false) text(R.string.unavailable) else text(R.string.checking)
        val card = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            minimumHeight = dp(96)
            isClickable = true
            isFocusable = true
            background = cardBackground(availableNow)
            setPadding(dp(16), dp(14), dp(16), dp(10))
            setOnClickListener { connect(server.origin, server.id) }
            contentDescription = "${server.name}, ${server.origin}, $status, ${server.version}"
            addView(label(server.name, 17f).apply {
                maxLines = 1
                ellipsize = TextUtils.TruncateAt.END
            })
            addView(label(server.origin, 13f).apply {
                setTextColor(MUTED)
                maxLines = 1
                ellipsize = TextUtils.TruncateAt.MIDDLE
                setPadding(0, dp(4), 0, 0)
            })
            addView(label("$status · ${server.version}", 13f).apply {
                setTextColor(if (availableNow) ACCENT else MUTED)
                setPadding(0, dp(4), 0, 0)
            })
        }
        if (removable) {
            card.addView(button(text(R.string.remove_recent)) {
                lifecycleScope.launch {
                    try {
                        withContext(Dispatchers.IO) { store.remove(server) }
                        refreshRecents()
                    } catch (failure: ClientException) {
                        showChooser(failure)
                    }
                }
            }.apply {
                contentDescription = "${text(R.string.remove_recent)}: ${server.name}"
            }, LinearLayout.LayoutParams(-2, -2).apply {
                gravity = Gravity.END
                topMargin = dp(6)
            })
        }
        parent.addView(card, cardLayoutParams())
    }

    private fun cardBackground(accent: Boolean) = GradientDrawable().apply {
        shape = GradientDrawable.RECTANGLE
        cornerRadius = dp(18).toFloat()
        setColor(CARD)
        setStroke(dp(1), if (accent) ACCENT_DARK else CARD_BORDER)
    }

    private fun cardLayoutParams() = LinearLayout.LayoutParams(-1, -2).apply {
        bottomMargin = dp(14)
    }

    private fun sectionTitle(value: String) = label(value, 19f).apply {
        setPadding(0, dp(24), 0, dp(10))
    }

    private fun label(value: String, size: Float) = TextView(this).apply {
        text = value
        textSize = size
        setTextColor(FOREGROUND)
    }

    private fun button(value: String, action: () -> Unit) = Button(this).apply {
        text = value
        textSize = 13f
        isAllCaps = false
        minHeight = dp(48)
        minWidth = dp(48)
        setOnClickListener { action() }
    }
    private fun text(id: Int, vararg values: Any): String = stringsContext.getString(id, *values)
    private fun updateStringsContext() {
        val config = Configuration(resources.configuration)
        config.setLocale(Locale.forLanguageTag(language))
        stringsContext = createConfigurationContext(config)
    }
    private fun errorText(failure: ClientException): String {
        val resource = when (failure.code) {
            ClientErrorCode.INVALID_ENDPOINT -> R.string.error_endpoint
            ClientErrorCode.UNREACHABLE -> R.string.error_unreachable
            ClientErrorCode.TIMEOUT -> R.string.error_timeout
            ClientErrorCode.TLS -> R.string.error_tls
            ClientErrorCode.REDIRECT -> R.string.error_redirect
            ClientErrorCode.HTTP_STATUS -> R.string.error_http
            ClientErrorCode.INVALID_METADATA -> R.string.error_metadata
            ClientErrorCode.INCOMPATIBLE_SERVER -> R.string.error_incompatible
            ClientErrorCode.IDENTITY_MISMATCH -> R.string.error_identity
            ClientErrorCode.STORAGE -> R.string.error_storage
            ClientErrorCode.DISCOVERY -> R.string.error_discovery
            ClientErrorCode.WEBVIEW_UNSUPPORTED -> R.string.error_webview
            ClientErrorCode.WEB_LOAD -> R.string.error_web_load
            ClientErrorCode.NAVIGATION_BLOCKED -> R.string.error_navigation
        }
        return "${text(resource)}\n${failure.code}" + (failure.detail?.let { "\n$it" } ?: "")
    }
    private fun key(server: ServerEndpoint) = "${server.id}\u0000${server.origin}"
    private fun dp(value: Int) = (value * resources.displayMetrics.density).toInt()

    companion object {
        private val BACKGROUND = Color.rgb(12, 23, 19)
        private val FOREGROUND = Color.rgb(234, 245, 238)
        private val MUTED = Color.rgb(164, 186, 172)
        private val ACCENT = Color.rgb(116, 205, 157)
        private val ACCENT_DARK = Color.rgb(57, 119, 84)
        private val CARD = Color.rgb(20, 37, 30)
        private val CARD_BORDER = Color.rgb(52, 72, 62)
        private val ERROR = Color.rgb(255, 180, 171)
        private const val ENTRY_HOME = "home"
        private const val ENTRY_CHOOSER = "chooser"
        private const val MAX_RECOVERY_FAILURES = 4
        private const val RECOVERY_WINDOW_MILLIS = 25_000L
    }
}
