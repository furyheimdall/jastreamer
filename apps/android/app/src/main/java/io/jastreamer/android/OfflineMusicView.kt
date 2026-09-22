package io.jastreamer.android

import android.app.AlertDialog
import android.content.Context
import android.content.res.ColorStateList
import android.graphics.Typeface
import android.graphics.Rect
import android.os.Bundle
import android.os.Parcelable
import android.text.Editable
import android.text.InputType
import android.text.SpannableString
import android.text.Spanned
import android.text.style.AbsoluteSizeSpan
import android.text.style.ForegroundColorSpan
import android.text.style.StyleSpan
import android.text.TextUtils
import android.text.TextWatcher
import android.util.SparseArray
import android.view.Gravity
import android.view.MotionEvent
import android.view.View
import android.view.ViewGroup
import android.view.ViewGroup.LayoutParams.MATCH_PARENT
import android.view.ViewGroup.LayoutParams.WRAP_CONTENT
import android.view.accessibility.AccessibilityEvent
import android.view.inputmethod.InputMethodManager
import android.widget.ArrayAdapter
import android.widget.Button
import android.widget.CheckBox
import android.widget.EditText
import android.widget.FrameLayout
import android.widget.ImageView
import android.widget.HorizontalScrollView
import android.widget.LinearLayout
import android.widget.LinearLayout.LayoutParams
import android.widget.ProgressBar
import android.widget.ScrollView
import android.widget.Spinner
import android.widget.SeekBar
import android.widget.TextView
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.media3.common.Player
import java.util.Locale
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import androidx.core.view.doOnNextLayout

/** Entirely native, account-independent browser and manager for committed local music. */
class OfflineMusicView(
    private val activity: ComponentActivity,
    private val strings: Context,
    private val onServers: () -> Unit,
    private val language: String,
    private val onLanguage: (String) -> Unit,
    initialScreen: String = SCREEN_LIBRARY,
    savedState: Bundle? = null,
) : LinearLayout(activity) {
    private lateinit var library: OfflineLibrary
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private val header = LinearLayout(activity)
    private val workspace = LinearLayout(activity)
    private val navigation = LinearLayout(activity)
    private var browseBlocked = false
    private val page = object : FrameLayout(activity) {
        override fun onInterceptTouchEvent(event: MotionEvent): Boolean =
            browseBlocked || super.onInterceptTouchEvent(event)

        override fun onTouchEvent(event: MotionEvent): Boolean =
            browseBlocked || super.onTouchEvent(event)
    }
    private val content = LinearLayout(activity)
    private val expandedPlayer = FrameLayout(activity)
    private val miniPlayer = LinearLayout(activity)
    private val miniSummary = LinearLayout(activity)
    private val playerTitle = TextView(activity)
    private val playerArtist = TextView(activity)
    private val miniArtwork = ImageView(activity)
    private val playPause = Button(activity)
    private val miniStop = Button(activity)
    private val miniExpand = Button(activity)
    private var screen = savedState?.getString(STATE_SCREEN) ?: initialScreen
    private var libraryTab = savedState?.getString(STATE_TAB) ?: TAB_ALBUMS
    private var likedOnly = savedState?.getBoolean(STATE_LIKED) == true
    private var searchQuery = savedState?.getString(STATE_QUERY).orEmpty()
    private var folderId = savedState?.getString(STATE_FOLDER) ?: OfflineLibrary.ROOT_FOLDER_ID
    private var openPlaylistId = savedState?.getString(STATE_PLAYLIST)
    private var fullPlayerOpen = savedState?.getBoolean(STATE_FULL_PLAYER) == true
    private var collectionKind = savedState?.getString(STATE_COLLECTION_KIND)
    private var collectionFirst = savedState?.getString(STATE_COLLECTION_FIRST)
    private var collectionSecond = savedState?.getString(STATE_COLLECTION_SECOND)
    private var restoredPageState = savedState?.getSparseParcelableArray<Parcelable>(STATE_PAGE_STATE)

    private var tracks = emptyList<OfflineTrack>()
    private var trackIndex = emptyMap<String, OfflineTrack>()
    private var folders = emptyList<OfflineFolder>()
    private var folderIndex = emptyMap<String, OfflineFolder>()
    private var playlists = emptyList<OfflinePlaylist>()
    private var storageUsage = OfflineStorageUsage(0, 0, 0)
    private var diagnostics = emptyList<JSONObject>()
    private var playback = OfflinePlaybackState()
    private var downloads = emptyList<OfflineDownloadJob>()
    private var meteredAllowed = false
    private var loading = true
    private var attached = true
    private var libraryReload: Job? = null
    private var pageList: LinearLayout? = null
    private var selectionBar: LinearLayout? = null
    private var searchField: EditText? = null
    private var playerSeek: SeekBar? = null
    private var fullPlayerPosition: TextView? = null
    private var fullPlayerDuration: TextView? = null
    private var updatingSeek = false
    private var wideLayout: Boolean? = null
    private var moveInProgress = false
    private var miniArtworkPath: String? = null
    private var playlistSaveInProgress = false
    private var miniTrackInitialized = false
    private var miniTrackId: String? = null

    private var lastDownloadSignature: List<OfflineDownloadJob> = emptyList()
    private val downloadProgressViews = mutableMapOf<String, Pair<ProgressBar, TextView>>()
    private val selectedTrackIds = linkedSetOf<String>()
    private val selectedAlbums = linkedSetOf<AlbumKey>()
    private val selectedFolderIds = linkedSetOf<String>()
    private val listPages = linkedMapOf<List<String?>, Int>()
    private val reflow = Runnable {
        if (attached) {
            renderPage(preserveState = true)
        }
    }

    init {
        id = R.id.offline_music_root
        orientation = VERTICAL
        setBackgroundColor(BACKGROUND)
        isFocusable = true
        buildHeader()
        workspace.orientation = VERTICAL
        navigation.id = R.id.offline_navigation
        content.id = R.id.offline_content
        content.orientation = VERTICAL
        page.id = R.id.offline_browse_content
        expandedPlayer.visibility = GONE
        expandedPlayer.accessibilityPaneTitle = text(R.string.offline_expanded_player)
        content.addView(page, LayoutParams(MATCH_PARENT, 0, 1f))
        content.addView(expandedPlayer, LayoutParams(MATCH_PARENT, WRAP_CONTENT))
        workspace.addView(content, LayoutParams(MATCH_PARENT, 0, 1f))
        addView(workspace, LayoutParams(MATCH_PARENT, 0, 1f))
        buildMiniPlayer()
        addView(navigation, LayoutParams(MATCH_PARENT, WRAP_CONTENT))
        applyAdaptiveLayout(resources.displayMetrics.widthPixels)
        renderPage()
        scope.launch {
            try {
                OfflinePlayback.restore(activity.applicationContext)
            } catch (failure: Throwable) {
                if (failure is CancellationException) throw failure
                showFailure(failure)
            }
        }
        scope.launch {
            try {
                library = withContext(Dispatchers.IO) { OfflineLibrary.get(activity.applicationContext) }
                library.changes.collectLatest { reloadLibrary() }
            } catch (failure: Throwable) {
                if (failure is CancellationException) throw failure
                loading = false
                showFailure(failure)
                renderPage()
            }
        }
        scope.launch {
            OfflinePlayback.state.collect { next ->
                val queueChanged = next.queue.entries != playback.queue.entries ||
                    next.queue.currentEntryId != playback.queue.currentEntryId ||
                    next.queue.shuffle != playback.queue.shuffle ||
                    next.queue.repeatMode != playback.queue.repeatMode
                val controlsChanged = next.owner != playback.owner || next.errorMessage != playback.errorMessage
                playback = next
                updatePlayerChrome()
                if (queueChanged && screen == SCREEN_QUEUE) renderQueue()
                if (fullPlayerOpen) {
                    if (queueChanged || controlsChanged) renderFullPlayer()
                }
            }
        }
        scope.launch {
            try {
                meteredAllowed = withContext(Dispatchers.IO) {
                    OfflineDownloads.initialize(activity.applicationContext)
                    OfflineDownloads.allowMetered(activity.applicationContext)
                }
                OfflineDownloads.jobs.collect { next ->
                    val structureChanged = downloadStructureChanged(next)
                    downloads = next
                    lastDownloadSignature = next
                    if (screen == SCREEN_DOWNLOADS) {
                        if (structureChanged) renderDownloads() else updateDownloadProgress()
                    }
                }
            } catch (failure: Throwable) {
                if (failure is CancellationException) throw failure
                showFailure(failure)
            }
        }
    }

    fun saveState(out: Bundle) {
        out.putString(STATE_SCREEN, screen)
        out.putString(STATE_TAB, libraryTab)
        out.putBoolean(STATE_LIKED, likedOnly)
        out.putString(STATE_QUERY, searchField?.text?.toString() ?: searchQuery)
        out.putString(STATE_FOLDER, folderId)
        out.putString(STATE_PLAYLIST, openPlaylistId)
        out.putBoolean(STATE_FULL_PLAYER, fullPlayerOpen)
        out.putString(STATE_COLLECTION_KIND, collectionKind)
        out.putString(STATE_COLLECTION_FIRST, collectionFirst)
        out.putString(STATE_COLLECTION_SECOND, collectionSecond)
        out.putSparseParcelableArray(STATE_PAGE_STATE, SparseArray<Parcelable>().also(content::saveHierarchyState))
    }

    fun openDownloads() {
        fullPlayerOpen = false
        screen = SCREEN_DOWNLOADS
        renderNavigation()
        renderPage()
    }

    fun handleBack(): Boolean {
        if (fullPlayerOpen) {
            setFullPlayerOpen(false)
            return true
        }
        if (screen == SCREEN_DOWNLOADS) {
            screen = SCREEN_LIBRARY
            renderNavigation()
            renderPage()
            return true
        }
        if (screen == SCREEN_PLAYLISTS && openPlaylistId != null) {
            openPlaylistId = null
            renderPlaylists()
            return true
        }
        if (screen == SCREEN_LIBRARY && collectionKind != null) {
            clearCollection()
            renderLibraryResults()
            return true
        }
        if (screen == SCREEN_LIBRARY && libraryTab == TAB_FOLDERS && folderId != OfflineLibrary.ROOT_FOLDER_ID) {
            folderId = folders.firstOrNull { it.id == folderId }?.parentId ?: OfflineLibrary.ROOT_FOLDER_ID
            clearSelection()
            renderLibrary()
            return true
        }
        return false
    }

    fun updateForConfiguration() {
        wideLayout = null
        removeCallbacks(reflow)
        post(reflow)
    }

    override fun onSizeChanged(w: Int, h: Int, oldw: Int, oldh: Int) {
        super.onSizeChanged(w, h, oldw, oldh)
        if (fullPlayerOpen || w != oldw) {
            removeCallbacks(reflow)
            post(reflow)
        }
    }

    override fun onDetachedFromWindow() {
        attached = false
        scope.cancel()
        super.onDetachedFromWindow()
    }

    private fun buildHeader() {
        header.orientation = HORIZONTAL
        header.gravity = Gravity.CENTER_VERTICAL
        header.setPadding(dp(12), dp(6), dp(12), dp(6))
        header.minimumHeight = dp(60)
        header.setBackgroundColor(PANEL)
        header.addView(ImageView(activity).apply {
            setImageResource(R.mipmap.ic_launcher)
            importantForAccessibility = IMPORTANT_FOR_ACCESSIBILITY_NO
        }, LayoutParams(dp(36), dp(36)).apply { marginEnd = dp(10) })
        header.addView(label(text(R.string.offline_saved_music), 16f).apply {
            id = R.id.offline_header_title
            setTypeface(typeface, Typeface.BOLD)
            maxLines = 1
            ellipsize = TextUtils.TruncateAt.END
        }, LayoutParams(0, WRAP_CONTENT, 1f))
        header.addView(playerAction(
            R.id.offline_servers_button, R.drawable.ic_offline_servers, text(R.string.offline_servers), click = onServers,
        ), LayoutParams(dp(48), dp(48)))
        header.addView(playerAction(
            R.id.offline_downloads_button, R.drawable.ic_offline_download, text(R.string.offline_downloads),
        ) { openDownloads() }, LayoutParams(dp(48), dp(48)))
        addView(header, LayoutParams(MATCH_PARENT, WRAP_CONTENT))
    }

    private fun buildMiniPlayer() {
        miniPlayer.id = R.id.offline_mini_player
        miniPlayer.orientation = HORIZONTAL
        miniPlayer.gravity = Gravity.CENTER_VERTICAL
        miniPlayer.minimumHeight = dp(68)
        miniPlayer.setPadding(dp(8), dp(8), dp(8), dp(8))
        miniPlayer.setBackgroundColor(PANEL)

        miniArtwork.id = R.id.offline_mini_artwork
        miniArtwork.importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_YES
        miniArtwork.contentDescription = text(R.string.offline_open_queue)
        miniArtwork.isClickable = true
        miniArtwork.isFocusable = true
        miniArtwork.setOnClickListener { openQueueFromPlayer() }
        OfflineUi.styleArtwork(miniArtwork)
        miniPlayer.addView(miniArtwork, LayoutParams(dp(48), dp(48)).apply { marginEnd = dp(8) })

        miniSummary.apply {
            orientation = VERTICAL
            gravity = Gravity.CENTER_VERTICAL
            minimumWidth = 0
            isScreenReaderFocusable = true
            accessibilityLiveRegion = View.ACCESSIBILITY_LIVE_REGION_POLITE
        }
        playerTitle.id = R.id.offline_player_title
        playerTitle.setTextColor(FOREGROUND)
        playerTitle.textSize = 14f
        playerTitle.maxLines = 1
        playerTitle.ellipsize = TextUtils.TruncateAt.END
        playerArtist.id = R.id.offline_player_artist
        playerArtist.setTextColor(MUTED)
        playerArtist.textSize = 12f
        playerArtist.maxLines = 1
        playerArtist.ellipsize = TextUtils.TruncateAt.END
        miniSummary.addView(playerTitle, LayoutParams(MATCH_PARENT, WRAP_CONTENT))
        miniSummary.addView(playerArtist, LayoutParams(MATCH_PARENT, WRAP_CONTENT))
        miniPlayer.addView(miniSummary, LayoutParams(0, MATCH_PARENT, 1f))

        playPause.id = R.id.offline_mini_play_pause
        updatePlayPauseButton(playPause)
        playPause.setOnClickListener {
            if (playback.owner == "server") resumeOrRequestHandoff()
            else if (playback.playing) OfflinePlayback.pause() else resumeOrRequestHandoff()
        }
        miniPlayer.addView(playPause, miniControlParams())

        miniStop.id = R.id.offline_mini_stop
        configurePlayerAction(miniStop, R.drawable.ic_offline_stop, text(R.string.offline_stop)) {
            OfflinePlayback.stop()
        }
        miniPlayer.addView(miniStop, miniControlParams())

        miniExpand.id = R.id.offline_mini_expand
        configurePlayerAction(miniExpand, R.drawable.ic_offline_expand, text(R.string.offline_expand_player)) {
            setFullPlayerOpen(!fullPlayerOpen)
        }
        miniPlayer.addView(miniExpand, miniControlParams())

        addView(miniPlayer, LayoutParams(MATCH_PARENT, WRAP_CONTENT))
        updatePlayerChrome()
    }

    private fun renderNavigation() {
        navigation.removeAllViews()
        navigation.orientation = if (wideLayout == true) VERTICAL else HORIZONTAL
        navigation.setPadding(dp(6), dp(6), dp(6), dp(6))
        navigation.setBackgroundColor(PANEL)
        val items = listOf(
            Triple(SCREEN_LIBRARY, R.string.offline_library, R.id.offline_library_button),
            Triple(SCREEN_PLAYLISTS, R.string.offline_playlists, R.id.offline_playlists_button),
            Triple(SCREEN_QUEUE, R.string.offline_queue, R.id.offline_queue_button),
            Triple(SCREEN_SETTINGS, R.string.offline_settings, R.id.offline_settings_button),
        )
        items.forEach { (target, title, id) ->
            val icon = when (target) {
                SCREEN_LIBRARY -> R.drawable.ic_offline_library
                SCREEN_PLAYLISTS -> R.drawable.ic_offline_playlists
                SCREEN_QUEUE -> R.drawable.ic_offline_queue
                else -> R.drawable.ic_offline_settings
            }
            val button = action(text(title), id) {
                fullPlayerOpen = false
                screen = target
                if (target != SCREEN_PLAYLISTS) openPlaylistId = null
                clearSelection()
                renderNavigation()
                renderPage()
            }
            OfflineUi.configureNavigationButton(button, icon, text(title), screen == target, vertical = wideLayout != true)
            navigation.addView(button, if (wideLayout == true) {
                LayoutParams(MATCH_PARENT, WRAP_CONTENT).apply { bottomMargin = dp(4) }
            } else {
                LayoutParams(0, WRAP_CONTENT, 1f)
            })
        }
    }

    private fun applyAdaptiveLayout(widthPixels: Int) {
        if (widthPixels <= 0) return
        val isWide = widthPixels / resources.displayMetrics.density > 980f
        if (wideLayout == isWide) return
        wideLayout = isWide
        (navigation.parent as? ViewGroup)?.removeView(navigation)
        workspace.removeAllViews()
        if (isWide) {
            workspace.orientation = HORIZONTAL
            workspace.addView(navigation, LayoutParams(dp(224), MATCH_PARENT))
            workspace.addView(content, LayoutParams(0, MATCH_PARENT, 1f))
        } else {
            workspace.orientation = VERTICAL
            workspace.addView(content, LayoutParams(MATCH_PARENT, MATCH_PARENT))
            addView(navigation, LayoutParams(MATCH_PARENT, WRAP_CONTENT))
        }
        renderNavigation()
    }

    private fun reloadLibrary() {
        libraryReload?.cancel()
        libraryReload = scope.launch {
            try {
                val snapshot = withContext(Dispatchers.IO) {
                    LibrarySnapshot(
                        tracks = library.tracks(),
                        folders = allFolders(library),
                        playlists = library.playlists(),
                        usage = library.usage(),
                        errors = library.errors(),
                    )
                }
                if (!attached) return@launch
                tracks = snapshot.tracks
                folders = snapshot.folders
                folderIndex = folders.associateBy { it.id }
                if (folderId != OfflineLibrary.ROOT_FOLDER_ID && folders.none { it.id == folderId }) {
                    folderId = OfflineLibrary.ROOT_FOLDER_ID
                }
                trackIndex = tracks.associateBy { it.id }
                playlists = snapshot.playlists
                storageUsage = snapshot.usage
                diagnostics = snapshot.errors
                loading = false
                pruneSelection()
                if (screen == SCREEN_LIBRARY && pageList?.isAttachedToWindow == true) {
                    renderLibraryResults()
                    if (fullPlayerOpen) renderFullPlayer()
                } else {
                    renderPage(preserveState = true)
                }
                updatePlayerChrome()
            } catch (failure: Throwable) {
                if (failure is CancellationException) throw failure
                loading = false
                showFailure(failure)
                renderPage()
            }
        }
    }

    private fun renderPage(preserveState: Boolean = false) {
        val previousState = if (preserveState) SparseArray<Parcelable>().also(content::saveHierarchyState) else null
        val focusedId = if (preserveState) content.findFocus()?.id else null
        miniPlayer.visibility = VISIBLE
        expandedPlayer.removeAllViews()
        expandedPlayer.visibility = if (fullPlayerOpen) VISIBLE else GONE
        if (preserveState) applyAdaptiveLayout(width)
        if (!fullPlayerOpen) {
            playerSeek = null
            fullPlayerPosition = null
            fullPlayerDuration = null
        }
        updatePlayerChrome()
        when {
            loading -> setPage(scrollColumn().apply { addView(message(text(R.string.offline_loading))) })
            screen == SCREEN_LIBRARY -> renderLibrary()
            screen == SCREEN_PLAYLISTS -> renderPlaylists()
            screen == SCREEN_QUEUE -> renderQueue()
            screen == SCREEN_SETTINGS -> renderSettings()
            screen == SCREEN_DOWNLOADS -> renderDownloads()
            else -> {
                screen = SCREEN_LIBRARY
                renderLibrary()
            }
        }
        if (fullPlayerOpen) renderFullPlayer()
        if (!loading) {
            (restoredPageState ?: previousState)?.let(content::restoreHierarchyState)
            restoredPageState = null
            if (focusedId != null && focusedId != View.NO_ID) content.findViewById<View>(focusedId)?.requestFocus()
        }
        updateBrowseInteraction()
    }

    private fun renderLibrary() {
        val previousTabScroll = page.findViewById<HorizontalScrollView>(R.id.offline_library_tabs)?.scrollX
        val body = vertical(dp(12))
        body.addView(pageHeading(text(R.string.offline_library_heading)))
        searchField = EditText(activity).apply {
            id = R.id.offline_search
            hint = text(R.string.offline_search)
            OfflineUi.configureInput(this)
            setSingleLine(true)
            setCompoundDrawablesRelativeWithIntrinsicBounds(R.drawable.ic_offline_search, 0, 0, 0)
            compoundDrawableTintList = ColorStateList.valueOf(MUTED)
            compoundDrawablePadding = dp(8)
            setText(searchQuery)
            setSelection(text.length)
            addTextChangedListener(object : TextWatcher {
                override fun beforeTextChanged(s: CharSequence?, start: Int, count: Int, after: Int) = Unit
                override fun onTextChanged(s: CharSequence?, start: Int, before: Int, count: Int) {
                    val query = s?.toString().orEmpty()
                    if (query == searchQuery) return
                    searchQuery = query
                    renderLibraryResults()
                }
                override fun afterTextChanged(s: Editable?) = Unit
            })
        }
        body.addView(searchField, sectionParams())
        val tabs = row()
        var selectedTab: Button? = null
        listOf(
            TAB_ALBUMS to R.string.offline_albums,
            TAB_ARTISTS to R.string.offline_artists,
            TAB_GENRES to R.string.offline_genres,
            TAB_FOLDERS to R.string.offline_folders,
            TAB_TRACKS to R.string.offline_tracks,
            TAB_LIKED to R.string.offline_liked,
        ).forEach { (tab, title) ->
            tabs.addView(action(text(title)) {
                likedOnly = tab == TAB_LIKED
                libraryTab = if (likedOnly) TAB_TRACKS else tab
                folderId = if (tab == TAB_FOLDERS) folderId else OfflineLibrary.ROOT_FOLDER_ID
                clearCollection()
                clearSelection()
                renderLibrary()
            }.apply {
                OfflineUi.configureTab(this, if (tab == TAB_LIKED) likedOnly else !likedOnly && libraryTab == tab)
                if (isSelected) selectedTab = this
                if (tab == TAB_LIKED) {
                    setCompoundDrawablesRelativeWithIntrinsicBounds(R.drawable.ic_offline_heart, 0, 0, 0)
                    compoundDrawableTintList = textColors
                    compoundDrawablePadding = dp(8)
                }
            }, LayoutParams(WRAP_CONTENT, WRAP_CONTENT).apply { marginEnd = dp(4) })
        }
        val tabStrip = actionStrip(tabs).apply { id = R.id.offline_library_tabs }
        body.addView(tabStrip, sectionParams())
        selectionBar = LinearLayout(activity).apply {
            id = R.id.offline_selection_bar
            orientation = VERTICAL
            background = OfflineUi.cardBackground(activity)
        }
        body.addView(selectionBar)
        pageList = LinearLayout(activity).apply {
            id = R.id.offline_list
            orientation = VERTICAL
        }
        body.addView(pageList)
        setPage(ScrollView(activity).apply {
            isFillViewport = true
            addView(body)
        })
        renderLibraryResults()
        tabStrip.post {
            if (tabStrip.parent != null) {
                previousTabScroll?.let { tabStrip.scrollTo(it, 0) }
                selectedTab?.let { selected ->
                    val bounds = Rect()
                    selected.getHitRect(bounds)
                    tabStrip.requestChildRectangleOnScreen(tabs, bounds, true)
                }
            }
        }
    }

    private fun renderLibraryResults() {
        val list = pageList ?: return
        list.removeAllViews()
        renderSelectionBar()
        val filtered = filteredTracks()
        val drilldown = collectionTracks(filtered)
        if (drilldown != null) {
            list.addView(action(text(R.string.offline_back_to_list)) {
                clearCollection()
                renderLibraryResults()
            })
            val summary = row()
            if (collectionKind == TAB_ALBUMS && drilldown.isNotEmpty()) {
                summary.addView(artwork(drilldown.first(), 80), LayoutParams(dp(80), dp(80)).apply { marginEnd = dp(12) })
            }
            summary.addView(label(collectionSecond ?: collectionFirst.orEmpty(), 22f).apply {
                setTypeface(typeface, Typeface.BOLD)
                maxLines = 3
                ellipsize = TextUtils.TruncateAt.END
            }, LayoutParams(0, WRAP_CONTENT, 1f))
            list.addView(summary, sectionParams())
            list.addView(collectionActions(drilldown), sectionParams())
            renderTracks(list, drilldown)
            return
        }
        when (libraryTab) {
            TAB_TRACKS -> renderTracks(list, filtered)
            TAB_ALBUMS -> renderAlbums(list, filtered)
            TAB_ARTISTS, TAB_GENRES -> renderGroups(list, filtered, libraryTab)
            TAB_FOLDERS -> renderFolders(list)
        }
    }

    private fun filteredTracks(): List<OfflineTrack> {
        val query = searchQuery.trim()
        if (query.isEmpty() && !likedOnly) return tracks
        return tracks.filter {
            (!likedOnly || it.liked) && (query.isEmpty() ||
                it.title.contains(query, ignoreCase = true) ||
                it.artist.contains(query, ignoreCase = true) ||
                it.album.contains(query, ignoreCase = true) ||
                it.albumArtist.contains(query, ignoreCase = true) ||
                it.genre.contains(query, ignoreCase = true))
        }
    }

    private fun collectionTracks(values: List<OfflineTrack>): List<OfflineTrack>? = when (collectionKind) {
        TAB_ALBUMS -> values.filter {
            it.albumArtist.ifBlank { it.artist } == collectionFirst &&
                it.album.ifBlank { text(R.string.offline_unknown_album) } == collectionSecond
        }.sortedWith(compareBy<OfflineTrack> { it.disc }.thenBy { it.track }.thenBy { it.title })
        TAB_ARTISTS -> values.filter {
            it.artist.ifBlank { text(R.string.offline_unknown_artist) } == collectionFirst
        }.sortedWith(compareBy<OfflineTrack> { it.album }.thenBy { it.disc }.thenBy { it.track })
        TAB_GENRES -> values.filter {
            it.genre.ifBlank { text(R.string.offline_unknown_genre) } == collectionFirst
        }.sortedWith(compareBy<OfflineTrack> { it.album }.thenBy { it.disc }.thenBy { it.track })
        else -> null
    }

    private fun clearCollection() {
        collectionKind = null
        collectionFirst = null
        collectionSecond = null
    }

    private fun pageKey(section: String): List<String?> = listOf(
        section, screen, libraryTab, folderId, collectionKind, collectionFirst,
        collectionSecond, searchQuery, openPlaylistId, likedOnly.toString(),
    )

    private fun renderPaged(parent: LinearLayout, key: List<String?>, count: Int, bind: (Int) -> Unit) {
        val last = (count - 1).coerceAtLeast(0) / PAGE_SIZE
        val current = (listPages[key] ?: 0).coerceIn(0, last)
        if (key !in listPages && listPages.size >= 32) listPages.remove(listPages.keys.first())
        listPages[key] = current
        fun controls() {
            val navigation = row()
            navigation.addView(action(text(R.string.offline_previous_page), R.id.offline_previous_page) {
                listPages[key] = current - 1
                renderPage()
            }.apply { isEnabled = current > 0 }, LayoutParams(0, WRAP_CONTENT, 1f))
            navigation.addView(label(text(R.string.offline_page_count, current + 1, last + 1, count), 14f).apply {
                gravity = Gravity.CENTER
            }, LayoutParams(0, WRAP_CONTENT, 1f))
            navigation.addView(action(text(R.string.offline_next_page), R.id.offline_next_page) {
                listPages[key] = current + 1
                renderPage()
            }.apply { isEnabled = current < last }, LayoutParams(0, WRAP_CONTENT, 1f))
            parent.addView(navigation)
        }
        if (last > 0) controls()
        val start = current * PAGE_SIZE
        for (index in start until minOf(start + PAGE_SIZE, count)) bind(index)
        if (last > 0) controls()
    }

    private fun downloadStructureChanged(next: List<OfflineDownloadJob>): Boolean {
        if (next.size != lastDownloadSignature.size) return true
        for (index in next.indices) {
            val before = lastDownloadSignature[index]
            val after = next[index]
            if (before.id != after.id || before.title != after.title ||
                before.serverName != after.serverName || before.status != after.status ||
                before.quality != after.quality || before.folderId != after.folderId ||
                before.totalTracks != after.totalTracks || before.completedTracks != after.completedTracks ||
                before.failedTracks != after.failedTracks || before.errorCode != after.errorCode ||
                before.errorMessage != after.errorMessage) return true
        }
        return false
    }

    private fun renderTracks(parent: LinearLayout, values: List<OfflineTrack>) {
        if (values.isEmpty()) {
            renderEmptyLibrary(parent)
            return
        }
        val selectedIds = effectiveSelectedTrackIds()
        renderPaged(parent, pageKey("tracks"), values.size) { index ->
            val track = values[index]
            val selected = track.id in selectedIds
            val card = vertical(dp(8)).apply {
                background = OfflineUi.rowBackground(activity, selected)
            }
            val mainRow = row()
            mainRow.addView(artwork(track, 48), LayoutParams(dp(48), dp(48)))
            val detail = displayArtist(track) + if (track.pendingDelete) "\n" + text(R.string.offline_pending_delete) else ""
            mainRow.addView(listLabel(track.title, detail) {
                if (hasSelection()) toggleTrack(track.id) else showTrackInformation(track)
            }.apply {
                isSelected = selected
                setOnLongClickListener { toggleTrack(track.id); true }
                contentDescription = "${track.title}, ${displayArtist(track)}, ${displayAlbum(track)}"
            }, LayoutParams(0, WRAP_CONTENT, 1f))
            mainRow.addView(label(durationValue(track.durationMs), 12f).apply {
                setTextColor(MUTED)
                gravity = Gravity.END
            })
            addIconAction(mainRow, R.drawable.ic_offline_heart, text(if (track.liked) R.string.offline_unlike else R.string.offline_like), selected = track.liked, enabled = !track.pendingDelete) {
                runStoreAction { library.setLiked(track.id, !track.liked) }
            }
            card.addView(mainRow, LayoutParams(MATCH_PARENT, WRAP_CONTENT))
            val actions = row().apply { gravity = Gravity.END }
            addIconAction(actions, R.drawable.ic_offline_play, text(R.string.offline_play), enabled = !track.pendingDelete) {
                playTracks(listOf(track), track.id)
            }
            addIconAction(actions, R.drawable.ic_offline_play_next, text(R.string.offline_play_next), enabled = !track.pendingDelete) {
                enqueueTracks(listOf(track), true)
            }
            addIconAction(actions, R.drawable.ic_offline_add_end, text(R.string.offline_add_end), enabled = !track.pendingDelete) {
                enqueueTracks(listOf(track), false)
            }
            val remaining = if ((page.width.takeIf { it > 0 } ?: resources.displayMetrics.widthPixels) - dp(40) < dp(288)) {
                card.addView(actions, LayoutParams(MATCH_PARENT, dp(48)))
                row().apply { gravity = Gravity.END }
            } else actions
            addIconAction(remaining, R.drawable.ic_offline_playlists, text(R.string.offline_add_playlist)) { choosePlaylist(listOf(track.id)) }
            addIconAction(remaining, R.drawable.ic_offline_info, text(R.string.offline_track_information)) { showTrackInformation(track) }
            addIconAction(remaining, R.drawable.ic_offline_more, text(R.string.offline_more_actions)) { showTrackActions(track) }
            card.addView(remaining, LayoutParams(MATCH_PARENT, dp(48)))
            parent.addView(card, LayoutParams(MATCH_PARENT, WRAP_CONTENT).apply { bottomMargin = dp(8) })
        }
    }

    private fun renderAlbums(parent: LinearLayout, values: List<OfflineTrack>) {
        val albums = values.groupBy { AlbumKey(it.albumArtist.ifBlank { it.artist }, it.album.ifBlank { text(R.string.offline_unknown_album) }) }
        if (albums.isEmpty()) {
            renderEmptyLibrary(parent)
            return
        }
        val orderedAlbums = albums.entries.sortedBy { it.key.album.lowercase(Locale.getDefault()) }
        val viewportWidth = page.width.takeIf { it > 0 } ?: resources.displayMetrics.widthPixels
        val columns = ((viewportWidth - dp(24)) / dp(160)).coerceIn(2, 4)
        val artPixels = ((viewportWidth - dp(24) - dp(12) * (columns - 1)) / columns).coerceAtLeast(dp(48))
        var gridRow: LinearLayout? = null
        var cells = 0
        renderPaged(parent, pageKey("albums"), orderedAlbums.size) { index ->
            if (cells == 0) {
                gridRow = row().apply { gravity = Gravity.TOP; isBaselineAligned = false }
                parent.addView(gridRow, LayoutParams(MATCH_PARENT, WRAP_CONTENT).apply { bottomMargin = dp(12) })
            }
            val (key, albumTracks) = orderedAlbums[index]
            val ordered = albumTracks.sortedWith(compareBy<OfflineTrack> { it.disc }.thenBy { it.track }.thenBy { it.title })
            val selected = key in selectedAlbums
            val open = {
                if (hasSelection()) toggleAlbum(key) else {
                    collectionKind = TAB_ALBUMS
                    collectionFirst = key.artist
                    collectionSecond = key.album
                    renderLibraryResults()
                }
            }
            val card = vertical(0).apply {
                background = OfflineUi.rowBackground(activity, selected, plain = true)
            }
            val cover = ImageView(activity).apply {
                OfflineUi.styleArtwork(this, 10)
                OfflineArtworkLoader.load(scope, this, ordered.first().artworkPath, artPixels)
                setOnClickListener { open() }
                setOnLongClickListener { toggleAlbum(key); true }
                contentDescription = key.album
            }
            card.addView(cover, LayoutParams(MATCH_PARENT, artPixels))
            card.addView(listLabel(key.album, key.artist.ifBlank { text(R.string.offline_unknown_artist) } + " · " + text(R.string.offline_count_tracks, ordered.size), open).apply {
                isSelected = selected
                setOnLongClickListener { toggleAlbum(key); true }
                setPadding(0, dp(8), 0, 0)
            }, LayoutParams(MATCH_PARENT, WRAP_CONTENT))
            gridRow!!.addView(card, LayoutParams(0, WRAP_CONTENT, 1f).apply {
                if (cells < columns - 1) marginEnd = dp(12)
            })
            cells = (cells + 1) % columns
        }
        if (cells != 0) repeat(columns - cells) {
            gridRow?.addView(View(activity), LayoutParams(0, 1, 1f).apply {
                if (it < columns - cells - 1) marginEnd = dp(12)
            })
        }
    }

    private fun renderGroups(parent: LinearLayout, values: List<OfflineTrack>, kind: String) {
        val groups = values.groupBy {
            if (kind == TAB_GENRES) it.genre.ifBlank { text(R.string.offline_unknown_genre) }
            else it.artist.ifBlank { text(R.string.offline_unknown_artist) }
        }
        if (groups.isEmpty()) {
            renderEmptyLibrary(parent)
            return
        }
        val orderedGroups = groups.entries.sortedBy { it.key.lowercase(Locale.getDefault()) }
        renderPaged(parent, pageKey(kind), orderedGroups.size) { index ->
            val (name, groupTracks) = orderedGroups[index]
            val card = row().apply {
                setPadding(dp(10), dp(8), dp(10), dp(8))
                background = OfflineUi.cardBackground(activity)
            }
            card.addView(leadingIcon(if (kind == TAB_GENRES) R.drawable.ic_offline_genre else R.drawable.ic_offline_artist), LayoutParams(dp(48), dp(48)))
            card.addView(listLabel(name, text(R.string.offline_count_tracks, groupTracks.size)) {
                collectionKind = kind
                collectionFirst = name
                collectionSecond = null
                renderLibraryResults()
            }, LayoutParams(0, WRAP_CONTENT, 1f))
            parent.addView(card, LayoutParams(MATCH_PARENT, WRAP_CONTENT).apply { bottomMargin = dp(8) })
        }
    }

    private fun renderFolders(parent: LinearLayout) {
        val current = if (folderId == OfflineLibrary.ROOT_FOLDER_ID) null else folderIndex[folderId]
        parent.addView(label(folderBreadcrumb(folderId), 20f).apply {
            setTypeface(typeface, Typeface.BOLD)
            setPadding(dp(4), dp(8), dp(4), dp(12))
        })
        val actions = row()
        if (folderId != OfflineLibrary.ROOT_FOLDER_ID) {
            actions.addView(action(text(R.string.offline_parent_folder)) {
                folderId = current?.parentId ?: OfflineLibrary.ROOT_FOLDER_ID
                clearSelection()
                renderLibrary()
            })
        }
        actions.addView(action(text(R.string.offline_new_folder), R.id.offline_new_folder_button) { promptNewFolder(folderId) }.apply {
            OfflineUi.configureButton(this, primary = true)
        })
        if (current != null) {
            actions.addView(action(text(R.string.offline_rename), R.id.offline_rename_folder_button) { promptRenameFolder(current) })
            actions.addView(action(text(R.string.offline_move), R.id.offline_move_folder_button) { chooseDestination { moveFolder(current, it) } })
            actions.addView(action(text(R.string.offline_delete_device), R.id.offline_delete_folder_button) {
                selectedFolderIds.clear()
                selectedFolderIds += current.id
                confirmDeleteSelection()
            }.apply { OfflineUi.configureButton(this, danger = true) })
        }
        parent.addView(actionStrip(actions), sectionParams())
        val visibleTracks = filteredTracks()
        val children = visibleChildFolders(visibleTracks)
        renderPaged(parent, pageKey("folders"), children.size) { index ->
            val folder = children[index]
            val descendants = recursiveTrackIds(setOf(folder.id))
            val selected = folder.id in selectedFolderIds
            val card = row().apply {
                background = OfflineUi.rowBackground(activity, selected)
                setPadding(dp(8), dp(8), dp(8), dp(8))
            }
            card.addView(leadingIcon(R.drawable.ic_offline_folder), LayoutParams(dp(48), dp(48)))
            card.addView(listLabel(folder.name, text(R.string.offline_folder_summary, descendants.size, bytes(descendants.sumOf { id -> trackIndex[id]?.byteSize ?: 0 }))) {
                if (hasSelection()) toggleFolder(folder.id) else {
                    folderId = folder.id
                    clearSelection()
                    renderLibrary()
                }
            }.apply {
                isSelected = selected
                setOnLongClickListener { toggleFolder(folder.id); true }
            }, LayoutParams(0, WRAP_CONTENT, 1f))
            parent.addView(card, LayoutParams(MATCH_PARENT, WRAP_CONTENT).apply { bottomMargin = dp(8) })
        }
        val directTracks = visibleTracks.filter { it.folderId == folderId }
        if (directTracks.isNotEmpty()) {
            parent.addView(collectionActions(directTracks), sectionParams())
            renderTracks(parent, directTracks)
        }
        if (children.isEmpty() && directTracks.isEmpty()) renderEmptyLibrary(parent)
    }
    private fun collectionActions(values: List<OfflineTrack>): HorizontalScrollView {
        val actions = row()
        actions.addView(action(text(R.string.offline_play_saved, values.size)) {
            values.firstOrNull()?.let { playTracks(values, it.id) }
        }.apply { OfflineUi.configureButton(this, primary = true); isEnabled = values.isNotEmpty() })
        actions.addView(action(text(R.string.offline_play_next)) { enqueueTracks(values, true) }.apply { isEnabled = values.isNotEmpty() })
        actions.addView(action(text(R.string.offline_add_end)) { enqueueTracks(values, false) }.apply { isEnabled = values.isNotEmpty() })
        actions.addView(action(text(R.string.offline_add_playlist)) { choosePlaylist(values.map { it.id }) }.apply { isEnabled = values.isNotEmpty() })
        return actionStrip(actions)
    }

    private fun enqueueTracks(values: List<OfflineTrack>, next: Boolean) {
        scope.launch {
            try {
                OfflinePlayback.enqueue(activity.applicationContext, values.map { it.id }, next)
                Toast.makeText(activity, text(if (next) R.string.offline_play_next else R.string.offline_add_end), Toast.LENGTH_SHORT).show()
            } catch (failure: Throwable) {
                if (failure is CancellationException) throw failure
                showFailure(failure)
            }
        }
    }

    private fun showTrackActions(track: OfflineTrack) {
        AlertDialog.Builder(activity)
            .setTitle(track.title)
            .setItems(arrayOf(text(R.string.offline_select), text(R.string.offline_move), text(R.string.offline_delete_device))) { _, which ->
                when (which) {
                    0 -> toggleTrack(track.id)
                    1 -> chooseDestination { moveTracks(listOf(track.id), it) }
                    2 -> confirmDeleteTracks(listOf(track.id))
                }
            }
            .setNegativeButton(text(R.string.offline_cancel), null)
            .show()
    }

    private fun renderEmptyLibrary(parent: LinearLayout) {
        val emptyText = if (tracks.isEmpty()) R.string.offline_no_music else R.string.offline_no_results
        parent.addView(message(text(emptyText)).apply { id = R.id.offline_empty })
        if (tracks.isEmpty()) parent.addView(action(text(R.string.offline_connect_download)) { onServers() })
    }

    private fun renderSelectionBar() {
        val bar = selectionBar ?: return
        bar.removeAllViews()
        if (!hasSelection()) {
            bar.visibility = GONE
            return
        }
        bar.visibility = VISIBLE
        val directIds = effectiveSelectedTrackIds()
        val ids = directIds + recursiveTrackIds(selectedFolderIds)
        bar.addView(label(text(R.string.offline_selected, ids.size, bytes(selectedBytes(ids))), 15f))
        if (moveInProgress) {
            bar.addView(ProgressBar(activity).apply { contentDescription = text(R.string.offline_move) })
        }
        val actions = row()
        actions.addView(action(text(R.string.offline_select_all)) { selectAllCurrent() })
        if (directIds.isNotEmpty()) {
            actions.addView(action(text(R.string.offline_add_playlist)) { choosePlaylist(directIds.toList()) })
            actions.addView(action(text(R.string.offline_move), R.id.offline_move_tracks_button) { chooseDestination { moveTracks(directIds.toList(), it) } })
        }
        if (selectedFolderIds.isNotEmpty()) {
            actions.addView(action(text(R.string.offline_move_folder), R.id.offline_move_folders_button) {
                chooseDestination { moveSelectedFolders(selectedFolderIds.toList(), it) }
            })
        }
        actions.addView(action(text(R.string.offline_delete_device)) { confirmDeleteSelection() })
        actions.addView(action(text(R.string.offline_clear_selection)) { clearSelection(); renderLibraryResults() })
        bar.addView(actionStrip(actions))
    }

    private fun renderPlaylists() {
        val body = vertical(dp(12))
        body.addView(pageHeading(text(R.string.offline_playlists)))
        val active = openPlaylistId?.let { id -> playlists.firstOrNull { it.id == id } }
        if (active == null) {
            openPlaylistId = null
            body.addView(action(text(R.string.offline_create_playlist)) { promptPlaylistName(null, emptyList()) }.apply {
                OfflineUi.configureButton(this, primary = true)
            }, sectionParams())
            if (playlists.isEmpty()) body.addView(message(text(R.string.offline_no_playlists)))
            val orderedPlaylists = playlists.sortedBy { it.name.lowercase(Locale.getDefault()) }
            renderPaged(body, pageKey("playlists"), orderedPlaylists.size) { index ->
                val playlist = orderedPlaylists[index]
                val saved = playlist.trackIds.count(trackIndex::containsKey)
                val card = row().apply {
                    background = OfflineUi.cardBackground(activity)
                    setPadding(dp(10), dp(8), dp(10), dp(8))
                }
                val first = playlist.trackIds.firstNotNullOfOrNull(trackIndex::get)
                card.addView(first?.let { artwork(it, 48) } ?: leadingIcon(R.drawable.ic_offline_playlists), LayoutParams(dp(48), dp(48)))
                card.addView(listLabel(playlist.name, text(R.string.offline_playlist_partial, saved, playlist.missingTitles.size + playlist.trackIds.size - saved)) {
                    openPlaylistId = playlist.id
                    renderPlaylists()
                }, LayoutParams(0, WRAP_CONTENT, 1f))
                addIconAction(card, R.drawable.ic_offline_play, text(R.string.offline_play_saved, saved), enabled = saved > 0) { playPlaylist(playlist) }
                body.addView(card, LayoutParams(MATCH_PARENT, WRAP_CONTENT).apply { bottomMargin = dp(8) })
            }
        } else {
            body.addView(action(text(R.string.offline_back_to_list)) { openPlaylistId = null; renderPlaylists() })
            body.addView(label(active.name, 24f).apply { setTypeface(typeface, Typeface.BOLD) })
            val playable = active.trackIds.mapNotNull(trackIndex::get)
            body.addView(collectionActions(playable), sectionParams())
            val headerActions = row()
            headerActions.addView(action(text(R.string.offline_rename_playlist)) { promptPlaylistName(active, active.trackIds) })
            headerActions.addView(action(text(R.string.offline_delete_playlist)) { confirmDeletePlaylist(active) }.apply {
                OfflineUi.configureButton(this, danger = true)
            })
            body.addView(actionStrip(headerActions), sectionParams())
            renderPaged(body, pageKey("playlist-entries"), active.trackIds.size) { index ->
                val track = trackIndex[active.trackIds[index]]
                val card = vertical(dp(8)).apply { background = OfflineUi.rowBackground(activity) }
                val titleRow = row()
                titleRow.addView(track?.let { artwork(it, 48) } ?: leadingIcon(R.drawable.ic_offline_library), LayoutParams(dp(48), dp(48)))
                titleRow.addView(listLabel(track?.title ?: text(R.string.offline_missing_track), track?.let(::displayArtist).orEmpty()) {
                    track?.let(::showTrackInformation)
                }, LayoutParams(0, WRAP_CONTENT, 1f))
                if (track != null) addIconAction(titleRow, R.drawable.ic_offline_play, text(R.string.offline_play)) { playTracks(listOf(track), track.id) }
                card.addView(titleRow)
                val actions = row().apply { gravity = Gravity.END }
                addIconAction(actions, R.drawable.ic_offline_expand, text(R.string.offline_move_up), enabled = index > 0) { reorderPlaylist(active, index, -1) }
                addIconAction(actions, R.drawable.ic_offline_collapse, text(R.string.offline_move_down), enabled = index < active.trackIds.lastIndex) { reorderPlaylist(active, index, 1) }
                addIconAction(actions, R.drawable.ic_offline_remove, text(R.string.offline_remove_playlist_entry)) { removePlaylistEntry(active, index) }
                card.addView(actions)
                body.addView(card, LayoutParams(MATCH_PARENT, WRAP_CONTENT).apply { bottomMargin = dp(8) })
            }
            renderPaged(body, pageKey("playlist-missing"), active.missingTitles.size) { index ->
                body.addView(label("${text(R.string.offline_missing_track)}: ${active.missingTitles[index]}", 14f).apply { setTextColor(ERROR) })
            }
        }
        setPage(ScrollView(activity).apply { addView(body) })
    }

    private fun renderQueue() {
        if (screen != SCREEN_QUEUE || fullPlayerOpen) return
        val body = vertical(dp(12))
        body.addView(pageHeading(text(R.string.offline_queue)))
        body.addView(playerModeControls(), sectionParams())
        body.addView(action(text(R.string.offline_save_queue)) {
            promptPlaylistName(null, playback.queue.entries.map { it.trackId })
        }.apply { isEnabled = playback.queue.entries.isNotEmpty() }, sectionParams())
        if (playback.queue.entries.isEmpty()) body.addView(message(text(R.string.offline_no_queue)))
        renderPaged(body, pageKey("queue"), playback.queue.entries.size) { index ->
            val entry = playback.queue.entries[index]
            val track = trackIndex[entry.trackId]
            val current = entry.id == playback.queue.currentEntryId
            val card = vertical(dp(8)).apply { background = OfflineUi.rowBackground(activity, current) }
            val main = row()
            main.addView(track?.let { artwork(it, 48) } ?: leadingIcon(R.drawable.ic_offline_library), LayoutParams(dp(48), dp(48)))
            main.addView(listLabel(track?.title ?: text(R.string.offline_missing_track), track?.let(::displayArtist).orEmpty()) {
                track?.let(::showTrackInformation)
            }, LayoutParams(0, WRAP_CONTENT, 1f))
            addIconAction(main, R.drawable.ic_offline_play, text(R.string.offline_play), selected = current, enabled = track != null) { playQueueEntry(entry.id) }
            card.addView(main)
            val actions = row().apply { gravity = Gravity.END }
            addIconAction(actions, R.drawable.ic_offline_expand, text(R.string.offline_move_up), enabled = index > 0) { OfflinePlayback.moveEntry(entry.id, -1) }
            addIconAction(actions, R.drawable.ic_offline_collapse, text(R.string.offline_move_down), enabled = index < playback.queue.entries.lastIndex) { OfflinePlayback.moveEntry(entry.id, 1) }
            addIconAction(actions, R.drawable.ic_offline_remove, text(R.string.offline_remove_queue)) { OfflinePlayback.removeEntry(entry.id) }
            if (track != null) addIconAction(actions, R.drawable.ic_offline_info, text(R.string.offline_track_information)) { showTrackInformation(track) }
            card.addView(actions)
            body.addView(card, LayoutParams(MATCH_PARENT, WRAP_CONTENT).apply { bottomMargin = dp(8) })
        }
        setPage(ScrollView(activity).apply { addView(body) })
    }

    private fun playQueueEntry(entryId: String, confirmed: Boolean = false, startPositionMs: Long = 0L) {
        scope.launch {
            try {
                OfflinePlayback.playEntry(activity.applicationContext, entryId, confirmed, startPositionMs)
            } catch (failure: NativePlaybackException) {
                if (failure.code == "handoff_required" && !confirmed) {
                    AlertDialog.Builder(activity)
                        .setTitle(text(R.string.offline_handoff_title))
                        .setMessage(text(R.string.offline_handoff_message))
                        .setPositiveButton(text(R.string.offline_confirm_switch)) { _, _ -> playQueueEntry(entryId, true, startPositionMs) }
                        .setNegativeButton(text(R.string.offline_cancel), null)
                        .show()
                } else showFailure(failure)
            } catch (failure: Throwable) {
                if (failure is CancellationException) throw failure
                showFailure(failure)
            }
        }
    }

    private fun renderFullPlayer() {
        miniPlayer.visibility = VISIBLE
        val track = currentTrack()
        val body = vertical(dp(12)).apply { setBackgroundColor(PANEL) }
        val viewportWidth = page.width.takeIf { it > 0 } ?: resources.displayMetrics.widthPixels
        val sideBySide = viewportWidth >= dp(600)
        val information = vertical(0)
        val heading = row()
        heading.addView(vertical(0).apply {
            addView(label(text(R.string.offline_now_playing).uppercase(Locale.getDefault()), 11f).apply {
                setTextColor(OfflinePalette.accent)
                setTypeface(typeface, Typeface.BOLD)
                letterSpacing = 0.1f
                setPadding(0, 0, 0, dp(4))
            })
            addView(label(text(R.string.offline_expanded_player), 18f).apply {
                setTypeface(typeface, Typeface.BOLD)
                setPadding(0, 0, 0, 0)
            })
        }, LayoutParams(0, WRAP_CONTENT, 1f))
        heading.addView(playerAction(R.id.offline_player_close, R.drawable.ic_offline_collapse, text(R.string.offline_close_player)) {
            setFullPlayerOpen(false)
        }, LayoutParams(dp(48), dp(48)))
        information.addView(heading)
        information.addView(label(track?.title ?: text(R.string.offline_no_queue), 17f).apply {
            id = R.id.offline_full_player_title
            setTypeface(typeface, Typeface.BOLD)
            maxLines = 2
            ellipsize = TextUtils.TruncateAt.END
            setPadding(0, dp(10), 0, dp(4))
        }, LayoutParams(MATCH_PARENT, WRAP_CONTENT))
        information.addView(label(track?.let(::displayArtist).orEmpty(), 13f).apply {
            id = R.id.offline_full_player_artist
            maxLines = 1
            ellipsize = TextUtils.TruncateAt.END
            setTextColor(MUTED)
            setPadding(0, 0, 0, dp(8))
        }, LayoutParams(MATCH_PARENT, WRAP_CONTENT))

        val controls = vertical(0)
        val transport = row()
        transport.addView(playerAction(R.id.offline_player_previous, R.drawable.ic_offline_previous, text(R.string.offline_previous)) {
            OfflinePlayback.previous()
        }.apply { isEnabled = playback.owner == OfflinePlaybackPolicy.OWNER_LOCAL }, LayoutParams(dp(48), dp(48)))
        val seekColumn = vertical(0)
        val seek = SeekBar(activity).apply {
            id = R.id.offline_player_seek
            isSaveEnabled = false
            isEnabled = playback.owner == OfflinePlaybackPolicy.OWNER_LOCAL && track != null && track.durationMs > 0
            max = (track?.durationMs ?: 0L).coerceIn(1L, Int.MAX_VALUE.toLong()).toInt()
            progress = playback.queue.positionMs.coerceIn(0L, max.toLong()).toInt()
            progressTintList = ColorStateList.valueOf(OfflinePalette.accent)
            thumbTintList = ColorStateList.valueOf(OfflinePalette.accent)
            contentDescription = text(R.string.offline_seek)
            setOnSeekBarChangeListener(object : SeekBar.OnSeekBarChangeListener {
                private var dragging = false
                override fun onProgressChanged(seekBar: SeekBar?, value: Int, fromUser: Boolean) {
                    if (fromUser && !updatingSeek) {
                        fullPlayerPosition?.text = durationValue(value.toLong())
                        if (!dragging) OfflinePlayback.seekTo(value.toLong())
                    }
                }
                override fun onStartTrackingTouch(seekBar: SeekBar?) { dragging = true }
                override fun onStopTrackingTouch(seekBar: SeekBar?) {
                    dragging = false
                    seekBar?.let { OfflinePlayback.seekTo(it.progress.toLong()) }
                }
            })
        }
        playerSeek = seek
        seekColumn.addView(seek, LayoutParams(MATCH_PARENT, dp(48)))
        val times = row()
        fullPlayerPosition = label(durationValue(playback.queue.positionMs), 11f).apply {
            id = R.id.offline_player_position
            setTextColor(MUTED)
            setPadding(dp(4), 0, dp(4), 0)
        }
        fullPlayerDuration = label(durationValue(track?.durationMs ?: 0L), 11f).apply {
            id = R.id.offline_player_duration
            setTextColor(MUTED)
            gravity = Gravity.END
            setPadding(dp(4), 0, dp(4), 0)
        }
        times.addView(fullPlayerPosition, LayoutParams(0, WRAP_CONTENT, 1f))
        times.addView(fullPlayerDuration, LayoutParams(0, WRAP_CONTENT, 1f))
        seekColumn.addView(times)
        transport.addView(seekColumn, LayoutParams(0, WRAP_CONTENT, 1f))
        transport.addView(playerAction(R.id.offline_player_next, R.drawable.ic_offline_next, text(R.string.offline_next)) {
            OfflinePlayback.next()
        }.apply { isEnabled = playback.owner == OfflinePlaybackPolicy.OWNER_LOCAL }, LayoutParams(dp(48), dp(48)))
        controls.addView(transport, sectionParams())

        val options = row()
        addCenteredControl(options, playerAction(R.id.offline_player_shuffle, R.drawable.ic_offline_shuffle,
            text(if (playback.queue.shuffle) R.string.offline_shuffle_on else R.string.offline_shuffle_off)) {
            OfflinePlayback.setShuffle(!playback.queue.shuffle)
        }.apply { isSelected = playback.queue.shuffle })
        addCenteredControl(options, playerAction(R.id.offline_player_repeat,
            if (playback.queue.repeatMode == Player.REPEAT_MODE_ONE) R.drawable.ic_offline_repeat_one else R.drawable.ic_offline_repeat,
            text(repeatLabel())) { cycleRepeat() }.apply { isSelected = playback.queue.repeatMode != Player.REPEAT_MODE_OFF })
        addCenteredControl(options, playerAction(R.id.offline_player_queue, R.drawable.ic_offline_queue, text(R.string.offline_open_queue)) {
            openQueueFromPlayer()
        })
        addCenteredControl(options, playerAction(R.id.offline_player_information, R.drawable.ic_offline_info, text(R.string.offline_track_information)) {
            track?.let(::showTrackInformation)
        }.apply { isEnabled = track != null })
        controls.addView(options, LayoutParams(MATCH_PARENT, dp(56)))
        if (sideBySide) {
            body.addView(row().apply {
                gravity = Gravity.TOP
                addView(information, LayoutParams(0, WRAP_CONTENT, 0.4f).apply { marginEnd = dp(16) })
                addView(controls, LayoutParams(0, WRAP_CONTENT, 0.6f))
            })
        } else {
            body.addView(information, sectionParams())
            body.addView(controls)
        }
        playback.errorMessage?.takeIf { it.isNotBlank() }?.let {
            body.addView(label(it, 14f).apply { setTextColor(ERROR) })
        }
        val panel = ScrollView(activity).apply {
            id = R.id.offline_expanded_player
            isHorizontalScrollBarEnabled = false
            addView(body)
        }
        panel.setBackgroundColor(PANEL)
        expandedPlayer.removeAllViews()
        expandedPlayer.addView(panel, FrameLayout.LayoutParams(MATCH_PARENT, WRAP_CONTENT))
        expandedPlayer.visibility = VISIBLE
        updatePlayerChrome()
    }

    private fun playerModeControls(): LinearLayout = row().apply {
        addIconAction(this, R.drawable.ic_offline_shuffle,
            text(if (playback.queue.shuffle) R.string.offline_shuffle_on else R.string.offline_shuffle_off),
            selected = playback.queue.shuffle) { OfflinePlayback.setShuffle(!playback.queue.shuffle) }
        addIconAction(this,
            if (playback.queue.repeatMode == Player.REPEAT_MODE_ONE) R.drawable.ic_offline_repeat_one else R.drawable.ic_offline_repeat,
            text(repeatLabel()), selected = playback.queue.repeatMode != Player.REPEAT_MODE_OFF) { cycleRepeat() }
    }

    private fun updatePlayerChrome() {
        val current = currentTrack()
        miniPlayer.visibility = VISIBLE
        if (!miniTrackInitialized || miniTrackId != current?.id) {
            miniTrackInitialized = true
            miniTrackId = current?.id
            playerTitle.text = current?.title ?: text(R.string.offline_no_queue)
            playerArtist.text = current?.let(::displayArtist) ?: text(R.string.offline_saved_music)
            miniSummary.contentDescription = "${playerTitle.text}, ${playerArtist.text}"
        }
        updatePlayPauseButton(playPause)
        playPause.isEnabled = playback.queue.entries.isNotEmpty()
        val expansionIcon = if (fullPlayerOpen) R.drawable.ic_offline_collapse else R.drawable.ic_offline_expand
        if (miniExpand.tag != expansionIcon) {
            configurePlayerAction(miniExpand, expansionIcon,
                text(if (fullPlayerOpen) R.string.offline_close_player else R.string.offline_expand_player)) {
                setFullPlayerOpen(!fullPlayerOpen)
            }
        }
        miniStop.isEnabled = playback.owner == OfflinePlaybackPolicy.OWNER_LOCAL
        fullPlayerPosition?.text = durationValue(playback.queue.positionMs)
        fullPlayerDuration?.text = durationValue(current?.durationMs ?: 0L)
        if (miniArtworkPath != current?.artworkPath || miniArtwork.drawable == null) {
            miniArtworkPath = current?.artworkPath
            OfflineArtworkLoader.load(scope, miniArtwork, miniArtworkPath, dp(48))
        }
        val seek = playerSeek
        if (seek != null && fullPlayerOpen) {
            updatingSeek = true
            seek.max = (current?.durationMs ?: 0L).coerceIn(1L, Int.MAX_VALUE.toLong()).toInt()
            seek.progress = playback.queue.positionMs.coerceIn(0L, seek.max.toLong()).toInt()
            seek.isEnabled = playback.owner == OfflinePlaybackPolicy.OWNER_LOCAL && current != null && current.durationMs > 0
            updatingSeek = false
        }
    }

    private fun renderDownloads() {
        if (screen != SCREEN_DOWNLOADS || fullPlayerOpen) return
        lastDownloadSignature = downloads.map { it.copy(receivedBytes = 0) }
        downloadProgressViews.clear()
        val body = vertical(dp(12))
        body.addView(pageHeading(text(R.string.offline_downloads)))
        val policy = CheckBox(activity).apply {
            text = text(R.string.offline_allow_metered)
            OfflineUi.configureCheckBox(this)
            isChecked = meteredAllowed
            setOnCheckedChangeListener { _, allowed ->
                meteredAllowed = allowed
                runStoreAction { OfflineDownloads.setAllowMetered(activity.applicationContext, allowed) }
            }
        }
        body.addView(vertical(dp(14)).apply {
            background = OfflineUi.cardBackground(activity)
            addView(label(text(R.string.offline_network_policy), 16f).apply { setTypeface(typeface, Typeface.BOLD) })
            addView(policy, LayoutParams(MATCH_PARENT, WRAP_CONTENT))
        }, sectionParams())
        val list = LinearLayout(activity).apply {
            id = R.id.offline_download_list
            orientation = VERTICAL
        }
        if (downloads.isEmpty()) list.addView(message(text(R.string.offline_download_empty)))
        downloads.forEach { job ->
            val card = vertical(dp(14)).apply { background = OfflineUi.cardBackground(activity) }
            card.addView(label(job.title, 18f).apply { setTypeface(typeface, Typeface.BOLD) })
            val hardFailure = job.status == "failed" && job.serverName.isBlank()
            val statusLine = if (job.status == "completed" || job.serverName.isBlank()) {
                displayDownloadStatus(job)
            } else {
                text(R.string.offline_transfer_status, job.serverName, displayDownloadStatus(job))
            }
            card.addView(label(statusLine, 14f).apply { setTextColor(MUTED) })
            val destination = when {
                job.folderId == OfflineLibrary.ROOT_FOLDER_ID -> text(R.string.offline_saved_music)
                folders.any { it.id == job.folderId } -> folderBreadcrumb(job.folderId)
                else -> text(R.string.offline_destination_unavailable)
            }
            card.addView(label(text(R.string.offline_destination, destination), 13f).apply { setTextColor(MUTED) })
            card.addView(label(displayQuality(job.quality), 13f).apply { setTextColor(MUTED) })
            val progress = ProgressBar(activity, null, android.R.attr.progressBarStyleHorizontal).apply {
                max = 10_000
                progressTintList = ColorStateList.valueOf(OfflinePalette.accent)
                progressBackgroundTintList = ColorStateList.valueOf(OfflinePalette.panelRaised)
            }
            val progressText = label("", 13f)
            downloadProgressViews[job.id] = progress to progressText
            updateDownloadProgress(job, progress, progressText)
            card.addView(progress, LayoutParams(MATCH_PARENT, dp(32)))
            card.addView(progressText)
            if (job.failedTracks > 0) card.addView(label(text(R.string.offline_download_failed_count, job.failedTracks), 13f).apply { setTextColor(ERROR) })
            job.errorMessage?.takeIf { it.isNotBlank() }?.let { card.addView(label(it, 13f).apply { setTextColor(ERROR) }) }
            if (hardFailure) {
                card.addView(label(text(R.string.offline_new_request_required), 13f).apply { setTextColor(ERROR) })
            }
            val actions = row()
            when (job.status) {
                "downloading", "preparing", "waiting", "importing" -> actions.addView(action(text(R.string.offline_download_pause)) {
                    runStoreAction { OfflineDownloads.pause(activity.applicationContext, job.id) }
                })
                "paused" -> actions.addView(action(text(R.string.offline_download_resume)) {
                    runStoreAction { OfflineDownloads.resume(activity.applicationContext, job.id) }
                })
                "failed" -> if (!hardFailure) {
                    actions.addView(action(text(R.string.offline_download_resume)) {
                        runStoreAction { OfflineDownloads.resume(activity.applicationContext, job.id) }
                    })
                }
            }
            if (job.status == "completed" || job.status == "partial") {
                actions.addView(action(text(R.string.offline_view_saved_music)) {
                    screen = SCREEN_LIBRARY
                    libraryTab = TAB_TRACKS
                    likedOnly = false
                    searchQuery = ""
                    clearCollection()
                    renderNavigation()
                    renderPage()
                })
            }
            if (job.status !in setOf("completed", "cancelled")) {
                if (!hardFailure) {
                    actions.addView(action(text(R.string.offline_download_change_destination)) {
                        chooseDestination { folderId ->
                            runStoreAction { OfflineDownloads.changeDestination(activity.applicationContext, job.id, folderId) }
                        }
                    })
                }
                actions.addView(action(text(R.string.offline_download_cancel)) { confirmCancelDownload(job) })
            }
            if (actions.childCount > 0) card.addView(actionStrip(actions))
            list.addView(card, LayoutParams(MATCH_PARENT, WRAP_CONTENT).apply { bottomMargin = dp(8) })
        }
        body.addView(list)
        setPage(ScrollView(activity).apply { addView(body) })
    }

    private fun updateDownloadProgress() {
        downloads.forEach { job ->
            downloadProgressViews[job.id]?.let { (progress, label) ->
                updateDownloadProgress(job, progress, label)
            }
        }
    }

    private fun updateDownloadProgress(job: OfflineDownloadJob, progress: ProgressBar, label: TextView) {
        val total = job.totalBytes.coerceAtLeast(1L)
        progress.progress = ((job.receivedBytes.coerceIn(0, total).toDouble() / total) * 10_000).toInt()
        val description = text(
            R.string.offline_download_progress,
            job.completedTracks,
            job.totalTracks,
            bytes(job.receivedBytes),
            bytes(job.totalBytes),
        )
        progress.contentDescription = description
        label.text = description
    }

    private fun renderSettings() {
        val body = vertical(dp(12))
        body.addView(pageHeading(text(R.string.offline_settings)))
        val languageCard = vertical(dp(14)).apply { background = OfflineUi.cardBackground(activity) }
        languageCard.addView(label(text(R.string.language), 18f).apply { setTypeface(typeface, Typeface.BOLD) })
        languageCard.addView(action(if (language == "ko") "한국어" else "English") {
            AlertDialog.Builder(activity)
                .setTitle(text(R.string.language))
                .setSingleChoiceItems(arrayOf("English", "한국어"), if (language == "ko") 1 else 0) { dialog, index ->
                    dialog.dismiss()
                    onLanguage(if (index == 1) "ko" else "en")
                }
                .show()
        }, LayoutParams(MATCH_PARENT, WRAP_CONTENT))
        body.addView(languageCard, sectionParams())
        val storage = vertical(dp(14)).apply { background = OfflineUi.cardBackground(activity) }
        storage.addView(label(text(R.string.offline_storage), 18f).apply { setTypeface(typeface, Typeface.BOLD) })
        val limit = if (storageUsage.limitBytes == 0L) text(R.string.offline_no_limit) else bytes(storageUsage.limitBytes)
        storage.addView(label(text(R.string.offline_storage_usage, bytes(storageUsage.musicBytes), bytes(storageUsage.availableBytes), limit), 14f).apply {
            id = R.id.offline_storage_usage
            setTextColor(MUTED)
        }, sectionParams())
        storage.addView(label(text(R.string.offline_limit_mb), 13f))
        val limitInput = EditText(activity).apply {
            id = R.id.offline_storage_limit
            hint = text(R.string.offline_limit_mb)
            OfflineUi.configureInput(this)
            inputType = InputType.TYPE_CLASS_NUMBER
            if (storageUsage.limitBytes > 0) setText((storageUsage.limitBytes / 1_000_000L).toString())
        }
        storage.addView(limitInput, sectionParams())
        storage.addView(action(text(R.string.offline_apply_limit)) {
            val mb = limitInput.text.toString().toLongOrNull()
            if (mb == null || mb < 0 || mb > Long.MAX_VALUE / 1_000_000L) {
                limitInput.error = text(R.string.offline_invalid_number)
            } else runStoreAction { library.setLimitBytes(mb * 1_000_000L) }
        }.apply { OfflineUi.configureButton(this, primary = true) })
        storage.addView(label(text(R.string.offline_storage_note), 13f).apply { setTextColor(MUTED) })
        storage.addView(action(text(R.string.offline_delete_all)) { confirmDeleteTracks(tracks.map { it.id }) }.apply {
            OfflineUi.configureButton(this, danger = true)
            isEnabled = tracks.isNotEmpty()
        })
        body.addView(storage, sectionParams())
        val diagnosticsCard = vertical(dp(14)).apply { background = OfflineUi.cardBackground(activity) }
        diagnosticsCard.addView(label(text(R.string.offline_diagnostics), 18f).apply { setTypeface(typeface, Typeface.BOLD) })
        val errorList = LinearLayout(activity).apply {
            id = R.id.offline_diagnostics
            orientation = VERTICAL
        }
        if (diagnostics.isEmpty()) errorList.addView(message(text(R.string.offline_no_diagnostics)))
        diagnostics.asReversed().forEach { item ->
            val message = text(R.string.offline_error, item.optString("code", "error"), item.optString("message", ""))
            val trackId = item.optString("track_id")
            val track = trackIndex[trackId]
            val detail = if (track == null) {
                if (trackId.isBlank()) message else "$trackId\n$message"
            } else {
                text(R.string.offline_diagnostic_track, track.title, track.codec.uppercase(Locale.ROOT), displayQuality(track.quality), message)
            }
            errorList.addView(label(detail, 13f).apply { setTextColor(ERROR) })
        }
        diagnosticsCard.addView(errorList)
        body.addView(diagnosticsCard, sectionParams())
        setPage(ScrollView(activity).apply { addView(body) })
    }

    private fun playTracks(values: List<OfflineTrack>, selectedId: String) {
        val ids = values.map { it.id }
        val index = ids.indexOf(selectedId).coerceAtLeast(0)
        startPlayback(ids, index, false)
    }

    private fun playPlaylist(playlist: OfflinePlaylist) {
        val playable = playlist.trackIds.filter(trackIndex::containsKey)
        if (playable.isNotEmpty()) startPlayback(playable, 0, false)
    }

    private fun resumeOrRequestHandoff() {
        if (playback.owner != "server") {
            OfflinePlayback.resume()
            return
        }
        playback.queue.currentEntryId?.let { entryId ->
            playQueueEntry(entryId, startPositionMs = playback.queue.positionMs)
        }
    }

    private fun startPlayback(ids: List<String>, index: Int, confirmed: Boolean) {
        scope.launch {
            try {
                OfflinePlayback.play(activity.applicationContext, ids, index, confirmed)
            } catch (failure: NativePlaybackException) {
                if (failure.code == "handoff_required" && !confirmed) {
                    AlertDialog.Builder(activity)
                        .setTitle(text(R.string.offline_handoff_title))
                        .setMessage(text(R.string.offline_handoff_message))
                        .setPositiveButton(text(R.string.offline_confirm_switch)) { _, _ -> startPlayback(ids, index, true) }
                        .setNegativeButton(text(R.string.offline_cancel), null)
                        .show()
                } else showFailure(failure)
            } catch (failure: Throwable) {
                if (failure is CancellationException) throw failure
                showFailure(failure)
            }
        }
    }

    private fun promptPlaylistName(existing: OfflinePlaylist?, trackIds: List<String>) {
        promptText(
            title = text(if (existing == null) R.string.offline_create_playlist else R.string.offline_rename_playlist),
            hint = text(R.string.offline_playlist_name),
            initial = existing?.name.orEmpty(),
            positive = text(if (existing == null) R.string.offline_create else R.string.offline_rename),
        ) { name ->
            runStoreAction { library.savePlaylist(existing?.id, name, trackIds, existing?.missingTitles ?: emptyList()) }
        }
    }

    private fun choosePlaylist(trackIds: List<String>) {
        val body = vertical(dp(16))
        val dialog = AlertDialog.Builder(activity)
            .setTitle(text(R.string.offline_add_playlist))
            .setView(ScrollView(activity).apply { addView(body) })
            .setNegativeButton(text(R.string.offline_cancel), null)
            .create()
        val choices = playlists.toList()
        if (choices.isNotEmpty()) {
            body.addView(label(text(R.string.offline_playlists), 13f))
            val picker = Spinner(activity).apply {
                adapter = ArrayAdapter(activity, android.R.layout.simple_spinner_item, choices.map { it.name }).apply {
                    setDropDownViewResource(android.R.layout.simple_spinner_dropdown_item)
                }
                minimumHeight = dp(48)
                background = OfflineUi.cardBackground(activity, 10)
            }
            body.addView(picker, sectionParams())
            body.addView(action(text(R.string.offline_add_selected)) {
                val playlist = choices[picker.selectedItemPosition]
                savePlaylistFromDialog(dialog) {
                    library.savePlaylist(playlist.id, playlist.name, playlist.trackIds + trackIds, playlist.missingTitles)
                }
            }.apply { OfflineUi.configureButton(this, primary = true) }, sectionParams())
        }
        body.addView(label(text(R.string.offline_new_playlist_name), 13f))
        val input = EditText(activity).apply {
            hint = text(R.string.offline_playlist_name)
            OfflineUi.configureInput(this)
            setSingleLine(true)
        }
        body.addView(input, sectionParams())
        body.addView(action(text(R.string.offline_create_and_add)) {
            val name = input.text.toString().trim()
            if (name.isEmpty()) input.error = text(R.string.offline_playlist_name)
            else savePlaylistFromDialog(dialog) { library.savePlaylist(null, name, trackIds) }
        })
        dialog.show()
    }

    private fun savePlaylistFromDialog(dialog: AlertDialog, save: () -> Unit) {
        if (playlistSaveInProgress) return
        playlistSaveInProgress = true
        scope.launch {
            try {
                withContext(Dispatchers.IO) { save() }
                dialog.dismiss()
            } catch (failure: Throwable) {
                if (failure is CancellationException) throw failure
                showFailure(failure)
            } finally {
                playlistSaveInProgress = false
            }
        }
    }

    private fun reorderPlaylist(playlist: OfflinePlaylist, index: Int, offset: Int) {
        val target = index + offset
        if (target !in playlist.trackIds.indices) return
        val changed = playlist.trackIds.toMutableList()
        val value = changed.removeAt(index)
        changed.add(target, value)
        runStoreAction { library.savePlaylist(playlist.id, playlist.name, changed, playlist.missingTitles) }
    }

    private fun removePlaylistEntry(playlist: OfflinePlaylist, index: Int) {
        val changed = playlist.trackIds.toMutableList().apply { removeAt(index) }
        runStoreAction { library.savePlaylist(playlist.id, playlist.name, changed, playlist.missingTitles) }
    }

    private fun confirmDeletePlaylist(playlist: OfflinePlaylist) {
        AlertDialog.Builder(activity)
            .setTitle(text(R.string.offline_delete_playlist))
            .setMessage(text(R.string.offline_delete_playlist_message, playlist.name))
            .setPositiveButton(text(R.string.offline_delete_playlist)) { _, _ ->
                openPlaylistId = null
                runStoreAction { library.deletePlaylist(playlist.id) }
            }
            .setNegativeButton(text(R.string.offline_cancel), null)
            .show()
    }

    private fun promptNewFolder(parentId: String) {
        promptText(text(R.string.offline_new_folder), text(R.string.offline_folder_name), positive = text(R.string.offline_create)) { name ->
            runStoreAction { library.createFolder(parentId, name) }
        }
    }

    private fun promptRenameFolder(folder: OfflineFolder) {
        promptText(text(R.string.offline_rename_folder), text(R.string.offline_folder_name), folder.name, text(R.string.offline_rename)) { name ->
            runStoreAction { library.renameFolder(folder.id, name) }
        }
    }

    private fun chooseDestination(onChosen: (String) -> Unit) {
        val destinations = flattenFolders()
        val labels = mutableListOf(text(R.string.offline_new_folder))
        labels += destinations.map { it.second }
        val dialog = AlertDialog.Builder(activity)
            .setTitle(text(R.string.offline_choose_destination))
            .setItems(labels.toTypedArray()) { _, which ->
                if (which > 0) onChosen(destinations[which - 1].first)
                else chooseParentForNewDestination(destinations, onChosen)
            }
            .setNegativeButton(text(R.string.offline_cancel), null)
            .create()
        dialog.show()
        dialog.listView.id = R.id.offline_destination_list
    }

    private fun chooseParentForNewDestination(destinations: List<Pair<String, String>>, onChosen: (String) -> Unit) {
        val dialog = AlertDialog.Builder(activity)
            .setTitle(text(R.string.offline_choose_destination))
            .setItems(destinations.map { it.second }.toTypedArray()) { _, which ->
                val parent = destinations[which].first
                promptText(text(R.string.offline_new_folder), text(R.string.offline_folder_name), positive = text(R.string.offline_create)) { name ->
                    scope.launch {
                        try {
                            val created = withContext(Dispatchers.IO) { library.createFolder(parent, name) }
                            onChosen(created.id)
                        } catch (failure: Throwable) {
                            if (failure is CancellationException) throw failure
                            showFailure(failure)
                        }
                    }
                }
            }
            .setNegativeButton(text(R.string.offline_cancel), null)
            .create()
        dialog.show()
        dialog.listView.id = R.id.offline_destination_list
    }

    private fun moveTracks(ids: List<String>, destination: String) {
        moveInProgress = true
        renderLibraryResults()
        moveTrackAt(ids, 0, destination)
    }

    private fun moveTrackAt(ids: List<String>, index: Int, destination: String, newName: String? = null) {
        var position = index
        while (position < ids.size) {
            val candidate = trackIndex[ids[position]]
            if (candidate != null && (candidate.folderId != destination || position == index && newName != null)) break
            position++
        }
        if (position >= ids.size) {
            moveInProgress = false
            clearSelection()
            renderLibraryResults()
            return
        }
        val track = trackIndex.getValue(ids[position])
        val chosenName = if (position == index) newName else null
        scope.launch {
            try {
                withContext(Dispatchers.IO) { library.moveTrack(track.id, destination, chosenName) }
                moveTrackAt(ids, position + 1, destination)
            } catch (failure: OfflineLibraryException) {
                if (failure.code == "conflict") {
                    showTrackCollision(ids, position, destination, track)
                } else {
                    moveInProgress = false
                    renderLibraryResults()
                    showFailure(failure)
                }
            } catch (failure: Throwable) {
                if (failure is CancellationException) throw failure
                moveInProgress = false
                renderLibraryResults()
                showFailure(failure)
            }
        }
    }

    private fun showTrackCollision(ids: List<String>, index: Int, destination: String, track: OfflineTrack) {
        AlertDialog.Builder(activity)
            .setTitle(text(R.string.offline_collision_title))
            .setMessage(text(R.string.offline_collision_message, track.relativePath.substringAfterLast('/')))
            .setPositiveButton(text(R.string.offline_another_name)) { _, _ ->
                promptText(
                    text(R.string.offline_another_name),
                    text(R.string.offline_folder_name),
                    track.relativePath.substringAfterLast('/'),
                    text(R.string.offline_move),
                    onCancel = {
                        moveInProgress = false
                        renderLibraryResults()
                    },
                ) { moveTrackAt(ids, index, destination, it) }
            }
            .setNeutralButton(text(R.string.offline_skip)) { _, _ -> moveTrackAt(ids, index + 1, destination) }
            .setNegativeButton(text(R.string.offline_cancel_remaining)) { _, _ ->
                moveInProgress = false
                renderLibraryResults()
            }
            .show()
    }

    private fun moveFolder(folder: OfflineFolder, destination: String, newName: String? = null) {
        moveInProgress = true
        renderLibraryResults()
        moveFolderAt(listOf(folder.id), 0, destination, newName)
    }

    private fun moveSelectedFolders(ids: List<String>, destination: String) {
        moveInProgress = true
        renderLibraryResults()
        val selected = ids.toSet()
        val normalized = ids.filter { id ->
            var parent = folders.firstOrNull { it.id == id }?.parentId
            var nested = false
            val visited = hashSetOf<String>()
            while (parent != null && visited.add(parent)) {
                if (parent in selected) {
                    nested = true
                    break
                }
                parent = folders.firstOrNull { it.id == parent }?.parentId
            }
            !nested
        }
        moveFolderAt(normalized, 0, destination)
    }

    private fun moveFolderAt(
        ids: List<String>,
        index: Int,
        destination: String,
        newName: String? = null,
    ) {
        var position = index
        while (position < ids.size) {
            val candidate = folderIndex[ids[position]]
            if (candidate != null && (candidate.parentId != destination || position == index && newName != null)) break
            position++
        }
        if (position >= ids.size) {
            moveInProgress = false
            clearSelection()
            renderLibraryResults()
            return
        }
        val folder = folderIndex.getValue(ids[position])
        val chosenName = if (position == index) newName else null
        scope.launch {
            try {
                withContext(Dispatchers.IO) { library.moveFolder(folder.id, destination, chosenName) }
                moveFolderAt(ids, position + 1, destination)
            } catch (failure: OfflineLibraryException) {
                if (failure.code == "conflict") {
                    AlertDialog.Builder(activity)
                        .setTitle(text(R.string.offline_collision_title))
                        .setMessage(text(R.string.offline_collision_message, folder.name))
                        .setPositiveButton(text(R.string.offline_another_name)) { _, _ ->
                            promptText(
                                text(R.string.offline_another_name),
                                text(R.string.offline_folder_name),
                                folder.name,
                                text(R.string.offline_move),
                                onCancel = {
                                    moveInProgress = false
                                    renderLibraryResults()
                                },
                            ) { moveFolderAt(ids, position, destination, it) }
                        }
                        .setNeutralButton(text(R.string.offline_skip)) { _, _ ->
                            moveFolderAt(ids, position + 1, destination)
                        }
                        .setNegativeButton(text(R.string.offline_cancel_remaining)) { _, _ ->
                            moveInProgress = false
                            renderLibraryResults()
                        }
                        .show()
                } else {
                    moveInProgress = false
                    renderLibraryResults()
                    showFailure(failure)
                }
            } catch (failure: Throwable) {
                if (failure is CancellationException) throw failure
                moveInProgress = false
                renderLibraryResults()
                showFailure(failure)
            }
        }
    }

    private fun confirmDeleteSelection() {
        val folderTrackIds = recursiveTrackIds(selectedFolderIds)
        val explicitTrackIds = effectiveSelectedTrackIds()
        val ids = (explicitTrackIds + folderTrackIds).toList()
        if (selectedFolderIds.isNotEmpty()) {
            val idSet = ids.toHashSet()
            val playlistRefs = playlists.sumOf { playlist -> playlist.trackIds.count { it in idSet } }
            val queueRefs = playback.queue.entries.count { it.trackId in idSet }
            val size = ids.sumOf { id -> trackIndex[id]?.byteSize ?: 0 }
            confirmDeleteDialog(
                ids,
                text(
                    R.string.offline_delete_folder_message,
                    topLevelSelectedFolderIds().size,
                    ids.size,
                    bytes(size),
                    playlistRefs,
                    queueRefs,
                ),
            ) {
                deleteSelectedFolders((explicitTrackIds - folderTrackIds).toList())
            }
            return
        }
        confirmDeleteTracks(ids)
    }

    private fun confirmDeleteTracks(ids: List<String>) {
        if (ids.isEmpty()) return
        val unique = ids.distinct()
        val uniqueSet = unique.toHashSet()
        val playlistRefs = playlists.sumOf { playlist -> playlist.trackIds.count { it in uniqueSet } }
        val queueRefs = playback.queue.entries.count { it.trackId in uniqueSet }
        val size = unique.sumOf { id -> trackIndex[id]?.byteSize ?: 0 }
        confirmDeleteDialog(unique, text(R.string.offline_delete_message, unique.size, bytes(size), playlistRefs, queueRefs)) {
            deleteTracks(unique)
        }
    }

    private fun confirmDeleteDialog(ids: List<String>, detail: String, delete: () -> Unit) {
        val currentId = if (playback.owner == "local") currentTrack()?.id else null
        val builder = AlertDialog.Builder(activity)
            .setTitle(text(R.string.offline_delete_title))
            .setMessage(detail)
        if (currentId != null && currentId in ids) {
            builder.setPositiveButton(text(R.string.offline_delete_when_done)) { _, _ -> delete() }
            builder.setNeutralButton(text(R.string.offline_stop_delete)) { _, _ ->
                OfflinePlayback.stop()
                delete()
            }
        } else {
            builder.setPositiveButton(text(R.string.offline_delete_now)) { _, _ -> delete() }
        }
        builder.setNegativeButton(text(R.string.offline_cancel), null).show()
    }

    private fun deleteTracks(ids: List<String>) {
        scope.launch {
            try {
                val result = withContext(Dispatchers.IO) { library.deleteTracks(ids) }
                clearSelection()
                showDeleteResult(result)
            } catch (failure: Throwable) {
                if (failure is CancellationException) throw failure
                showFailure(failure)
            }
        }
    }

    private fun deleteSelectedFolders(additionalTrackIds: List<String>) {
        val ids = topLevelSelectedFolderIds()
        scope.launch {
            val deleted = mutableListOf<String>()
            val deferred = mutableListOf<String>()
            val failures = linkedMapOf<String, String>()
            ids.forEach { id ->
                try {
                    val result = withContext(Dispatchers.IO) { library.deleteFolder(id) }
                    deleted += result.deletedIds
                    deferred += result.deferredIds
                    failures += result.failures
                } catch (failure: Throwable) {
                    if (failure is CancellationException) throw failure
                    failures[id] = failure.message ?: failure.javaClass.simpleName
                }
            }
            if (additionalTrackIds.isNotEmpty()) {
                try {
                    val result = withContext(Dispatchers.IO) { library.deleteTracks(additionalTrackIds) }
                    deleted += result.deletedIds
                    deferred += result.deferredIds
                    failures += result.failures
                } catch (failure: Throwable) {
                    if (failure is CancellationException) throw failure
                    additionalTrackIds.forEach { id ->
                        failures[id] = failure.message ?: failure.javaClass.simpleName
                    }
                }
            }
            clearSelection()
            showDeleteResult(OfflineDeleteResult(deleted.distinct(), deferred.distinct(), failures))
        }
    }

    private fun showDeleteResult(result: OfflineDeleteResult) {
        Toast.makeText(activity, text(R.string.offline_delete_result, result.deletedIds.size, result.deferredIds.size, result.failures.size), Toast.LENGTH_LONG).show()
        if (result.failures.isNotEmpty()) {
            AlertDialog.Builder(activity)
                .setTitle(text(R.string.offline_delete_title))
                .setMessage(text(R.string.offline_delete_failure, result.failures.entries.joinToString("\n") { "${it.key}: ${it.value}" }))
                .setPositiveButton(text(R.string.offline_done), null)
                .show()
        }
    }

    private fun confirmCancelDownload(job: OfflineDownloadJob) {
        AlertDialog.Builder(activity)
            .setTitle(text(R.string.offline_download_cancel))
            .setMessage(text(R.string.offline_download_cancel_message, job.title))
            .setPositiveButton(text(R.string.offline_download_cancel)) { _, _ ->
                runStoreAction { OfflineDownloads.cancel(activity.applicationContext, job.id) }
            }
            .setNegativeButton(text(R.string.offline_cancel), null)
            .show()
    }

    private fun toggleTrack(id: String) {
        if (!selectedTrackIds.add(id)) selectedTrackIds.remove(id)
        renderLibraryResults()
    }

    private fun toggleAlbum(key: AlbumKey) {
        if (!selectedAlbums.add(key)) selectedAlbums.remove(key)
        renderLibraryResults()
    }

    private fun toggleFolder(id: String) {
        if (!selectedFolderIds.add(id)) selectedFolderIds.remove(id)
        renderLibraryResults()
    }

    private fun visibleChildFolders(matchingTracks: List<OfflineTrack>): List<OfflineFolder> {
        val query = searchQuery.trim()
        val matchingIds = if (query.isEmpty()) emptySet() else matchingTracks.mapTo(hashSetOf()) { it.id }
        return folders.filter { folder ->
            folder.parentId == folderId && (query.isEmpty() || folder.name.contains(query, true) ||
                recursiveTrackIds(setOf(folder.id)).any(matchingIds::contains))
        }.sortedBy { it.name.lowercase(Locale.getDefault()) }
    }

    private fun selectAllCurrent() {
        val visibleCollection = collectionTracks(filteredTracks())
        if (visibleCollection != null) {
            selectedTrackIds += visibleCollection.map { it.id }
        } else {
            when (libraryTab) {
                TAB_TRACKS -> selectedTrackIds += filteredTracks().map { it.id }
                TAB_ALBUMS -> selectedAlbums += filteredTracks().map {
                    AlbumKey(it.albumArtist.ifBlank { it.artist }, it.album.ifBlank { text(R.string.offline_unknown_album) })
                }
                TAB_ARTISTS, TAB_GENRES -> selectedTrackIds += filteredTracks().map { it.id }
                TAB_FOLDERS -> {
                    val visibleTracks = filteredTracks()
                    selectedFolderIds += visibleChildFolders(visibleTracks).map { it.id }
                    selectedTrackIds += visibleTracks.filter { it.folderId == folderId }.map { it.id }
                }
            }
        }
        renderLibraryResults()
    }

    private fun effectiveSelectedTrackIds(): Set<String> {
        val ids = linkedSetOf<String>()
        ids += selectedTrackIds
        selectedAlbums.forEach { key ->
            ids += tracks.filter {
                AlbumKey(it.albumArtist.ifBlank { it.artist }, it.album.ifBlank { text(R.string.offline_unknown_album) }) == key
            }.map { it.id }
        }
        return ids
    }

    private fun recursiveTrackIds(folderIds: Set<String>): Set<String> {
        if (folderIds.isEmpty()) return emptySet()
        val all = folderIds.toMutableSet()
        var changed: Boolean
        do {
            val before = all.size
            all += folders.filter { it.parentId in all }.map { it.id }
            changed = all.size != before
        } while (changed)
        return tracks.filter { it.folderId in all }.mapTo(linkedSetOf()) { it.id }
    }

    private fun topLevelSelectedFolderIds(): List<String> {
        val selected = selectedFolderIds.toSet()
        return selectedFolderIds.filter { id ->
            var parent = folders.firstOrNull { it.id == id }?.parentId
            val visited = hashSetOf<String>()
            while (parent != null && visited.add(parent)) {
                if (parent in selected) return@filter false
                parent = folders.firstOrNull { it.id == parent }?.parentId
            }
            true
        }
    }

    private fun selectedBytes(ids: Set<String>): Long = tracks.asSequence().filter { it.id in ids }.sumOf { it.byteSize }
    private fun hasSelection() = selectedTrackIds.isNotEmpty() || selectedAlbums.isNotEmpty() || selectedFolderIds.isNotEmpty()
    private fun clearSelection() {
        selectedTrackIds.clear()
        selectedAlbums.clear()
        selectedFolderIds.clear()
    }

    private fun pruneSelection() {
        val trackIds = tracks.mapTo(hashSetOf()) { it.id }
        val folderIds = folders.mapTo(hashSetOf()) { it.id }
        val albumKeys = tracks.mapTo(hashSetOf()) {
            AlbumKey(it.albumArtist.ifBlank { it.artist }, it.album.ifBlank { text(R.string.offline_unknown_album) })
        }
        selectedTrackIds.retainAll(trackIds)
        selectedAlbums.retainAll(albumKeys)
        selectedFolderIds.retainAll(folderIds)
    }

    private fun currentTrack(): OfflineTrack? {
        val currentEntry = playback.queue.entries.firstOrNull { it.id == playback.queue.currentEntryId }
        return trackIndex[currentEntry?.trackId]
    }

    private fun setFullPlayerOpen(open: Boolean) {
        val scroll = page.getChildAt(0) as? ScrollView
        val position = scroll?.scrollY ?: 0
        scroll?.doOnNextLayout { scroll.scrollTo(scroll.scrollX, position) }
        fullPlayerOpen = open
        if (open) {
            renderFullPlayer()
        } else {
            expandedPlayer.visibility = GONE
            expandedPlayer.removeAllViews()
            playerSeek = null
            fullPlayerPosition = null
            fullPlayerDuration = null
        }
        updateBrowseInteraction()
        updatePlayerChrome()
    }

    private fun updateBrowseInteraction() {
        val blocked = fullPlayerOpen && resources.configuration.smallestScreenWidthDp < 600
        if (blocked == browseBlocked) return
        browseBlocked = blocked
        page.importantForAccessibility = if (blocked) IMPORTANT_FOR_ACCESSIBILITY_NO_HIDE_DESCENDANTS else IMPORTANT_FOR_ACCESSIBILITY_AUTO
        page.descendantFocusability = if (blocked) ViewGroup.FOCUS_BLOCK_DESCENDANTS else ViewGroup.FOCUS_BEFORE_DESCENDANTS
        if (blocked) {
            page.clearFocus()
            (activity.getSystemService(Context.INPUT_METHOD_SERVICE) as InputMethodManager)
                .hideSoftInputFromWindow(windowToken, 0)
        }
    }

    private fun openQueueFromPlayer() {
        fullPlayerOpen = false
        screen = SCREEN_QUEUE
        clearSelection()
        renderNavigation()
        renderPage()
    }

    private fun repeatLabel(): Int = when (playback.queue.repeatMode) {
        Player.REPEAT_MODE_ONE -> R.string.offline_repeat_one
        Player.REPEAT_MODE_ALL -> R.string.offline_repeat_all
        else -> R.string.offline_repeat_off
    }

    private fun cycleRepeat() {
        val next = when (playback.queue.repeatMode) {
            Player.REPEAT_MODE_OFF -> Player.REPEAT_MODE_ALL
            Player.REPEAT_MODE_ALL -> Player.REPEAT_MODE_ONE
            else -> Player.REPEAT_MODE_OFF
        }
        OfflinePlayback.setRepeat(next)
    }

    private fun showTrackInformation(track: OfflineTrack) {
        val detail = buildString {
            append(displayArtist(track))
            append('\n')
            append(displayAlbum(track))
            if (track.genre.isNotBlank()) {
                append('\n')
                append(track.genre)
            }
            append('\n')
            append(track.codec.uppercase(Locale.ROOT))
            append(" · ")
            append(displayQuality(track.quality))
            append('\n')
            append(bytes(track.byteSize))
        }
        AlertDialog.Builder(activity)
            .setTitle(track.title)
            .setMessage(detail)
            .setPositiveButton(text(R.string.offline_done), null)
            .show()
    }


    private fun sectionParams() = LayoutParams(MATCH_PARENT, WRAP_CONTENT).apply {
        bottomMargin = dp(12)
    }

    private fun miniControlParams() = LayoutParams(dp(48), dp(48)).apply {
        marginStart = dp(4)
    }

    private fun addCenteredControl(parent: LinearLayout, button: Button) {
        val slot = FrameLayout(activity)
        slot.addView(button, FrameLayout.LayoutParams(dp(48), dp(48), Gravity.CENTER))
        parent.addView(slot, LayoutParams(0, dp(56), 1f))
    }

    private fun playerAction(
        id: Int,
        icon: Int,
        label: String,
        primary: Boolean = false,
        click: () -> Unit,
    ): Button = Button(activity).apply {
        this.id = id
        configurePlayerAction(this, icon, label, primary, click)
    }

    private fun configurePlayerAction(
        button: Button,
        icon: Int,
        label: String,
        primary: Boolean = false,
        click: () -> Unit,
    ) {
        OfflineUi.configureIconButton(button, icon, label, primary)
        button.setOnClickListener { click() }
    }

    private fun updatePlayPauseButton(button: Button) {
        val isPlaying = playback.playing && playback.owner != "server"
        val label = text(if (isPlaying) R.string.offline_pause else R.string.offline_play)
        val icon = if (isPlaying) R.drawable.ic_offline_pause else R.drawable.ic_offline_play
        if (button.tag == icon && button.text.toString() == label) return
        OfflineUi.configureIconButton(button, icon, label, primary = true)
    }

    private fun flattenFolders(): List<Pair<String, String>> {
        val result = mutableListOf(OfflineLibrary.ROOT_FOLDER_ID to text(R.string.offline_saved_music))
        fun visit(parent: String, depth: Int) {
            folders.filter { it.parentId == parent }.sortedBy { it.name.lowercase(Locale.getDefault()) }.forEach { folder ->
                result += folder.id to ("  ".repeat(depth) + folder.name)
                visit(folder.id, depth + 1)
            }
        }
        visit(OfflineLibrary.ROOT_FOLDER_ID, 1)
        return result
    }

    private fun displayQuality(quality: String): String = when (quality) {
        "original" -> text(R.string.offline_quality_original)
        "aac_256" -> text(R.string.offline_quality_aac)
        else -> quality
    }

    private fun folderBreadcrumb(id: String): String {
        if (id == OfflineLibrary.ROOT_FOLDER_ID) return text(R.string.offline_saved_music)
        val names = mutableListOf<String>()
        val seen = hashSetOf<String>()
        var current: String? = id
        while (current != null && current != OfflineLibrary.ROOT_FOLDER_ID && seen.add(current)) {
            val folder = folders.firstOrNull { it.id == current } ?: break
            names += folder.name
            current = folder.parentId
        }
        return (listOf(text(R.string.offline_saved_music)) + names.asReversed()).joinToString(" / ")
    }

    private fun displayArtist(track: OfflineTrack) = track.artist.ifBlank { text(R.string.offline_unknown_artist) }
    private fun displayAlbum(track: OfflineTrack) = track.album.ifBlank { text(R.string.offline_unknown_album) }

    private fun displayDownloadStatus(job: OfflineDownloadJob): String {
        val status = text(when (job.status) {
            "waiting" -> R.string.offline_status_waiting
            "preparing" -> R.string.offline_status_preparing
            "downloading" -> R.string.offline_status_downloading
            "paused" -> R.string.offline_status_paused
            "importing" -> R.string.offline_status_importing
            "completed" -> R.string.offline_status_completed
            "partial" -> R.string.offline_status_partial
            "failed" -> R.string.offline_status_failed
            "cancelled" -> R.string.offline_status_cancelled
            else -> R.string.offline_status_waiting
        })
        val reasonResource = when (job.errorCode) {
            "network_unmetered_required" -> R.string.offline_reason_network_unmetered
            "network_roaming" -> R.string.offline_reason_network_roaming
            "network_unavailable" -> R.string.offline_reason_network_unavailable
            "auth_required" -> R.string.offline_reason_auth
            "destination_missing" -> R.string.offline_reason_destination
            "storage_full" -> R.string.offline_reason_storage
            else -> null
        }
        return reasonResource?.let { "$status · ${text(it)}" } ?: status
    }

    private fun runStoreAction(action: () -> Unit) {
        scope.launch {
            try {
                withContext(Dispatchers.IO) { action() }
            } catch (failure: Throwable) {
                if (failure is CancellationException) throw failure
                showFailure(failure)
            }
        }
    }

    private fun promptText(
        title: String,
        hint: String,
        initial: String = "",
        positive: String,
        onCancel: () -> Unit = {},
        onValue: (String) -> Unit,
    ) {
        val input = EditText(activity).apply {
            this.hint = hint
            setText(initial)
            setSelection(text.length)
            setSingleLine(true)
            minimumHeight = dp(48)
        }
        val dialog = AlertDialog.Builder(activity)
            .setTitle(title)
            .setView(input)
            .setPositiveButton(positive, null)
            .setNegativeButton(text(R.string.offline_cancel)) { _, _ -> onCancel() }
            .create()
        dialog.setOnShowListener {
            dialog.getButton(AlertDialog.BUTTON_POSITIVE).setOnClickListener {
                val value = input.text.toString().trim()
                if (value.isNotEmpty()) {
                    dialog.dismiss()
                    onValue(value)
                }
            }
        }
        dialog.show()
    }
    private fun actionStrip(actions: LinearLayout): HorizontalScrollView = HorizontalScrollView(activity).apply {
        isHorizontalScrollBarEnabled = false
        isFillViewport = false
        addView(actions)
    }

    private fun showFailure(failure: Throwable) {
        if (!attached) return
        Toast.makeText(activity, text(R.string.offline_operation_failed, failure.message ?: failure.javaClass.simpleName), Toast.LENGTH_LONG).show()
    }

    private fun setPage(view: View) {
        if (view is ScrollView) view.id = R.id.offline_page_scroll
        page.removeAllViews()
        page.addView(view, FrameLayout.LayoutParams(MATCH_PARENT, MATCH_PARENT))
        view.sendAccessibilityEvent(AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED)
    }
    private fun artwork(track: OfflineTrack, sizeDp: Int): ImageView = ImageView(activity).apply {
        importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO
        OfflineUi.styleArtwork(this)
        OfflineArtworkLoader.load(scope, this, track.artworkPath, dp(sizeDp))
    }


    private fun scrollColumn(): LinearLayout = vertical(dp(12))
    private fun vertical(padding: Int): LinearLayout = LinearLayout(activity).apply {
        orientation = VERTICAL
        setPadding(padding, padding, padding, padding)
    }
    private fun row(): LinearLayout = LinearLayout(activity).apply {
        orientation = HORIZONTAL
        gravity = Gravity.CENTER_VERTICAL
    }
    private fun label(value: String, size: Float): TextView = TextView(activity).apply {
        text = value
        textSize = size
        setTextColor(FOREGROUND)
        setPadding(dp(6), dp(6), dp(6), dp(6))
    }
    private fun message(value: String): TextView = label(value, 15f).apply {
        setTextColor(MUTED)
        setPadding(dp(8), dp(20), dp(8), dp(20))
    }
    private fun action(value: String, id: Int = View.NO_ID, click: () -> Unit): Button = Button(activity).apply {
        if (id != View.NO_ID) this.id = id
        text = value
        OfflineUi.configureButton(this)
        setOnClickListener { click() }
    }
    private fun pageHeading(title: String): LinearLayout = vertical(0).apply {
        addView(label(text(R.string.offline_saved_music).uppercase(Locale.getDefault()), 11f).apply {
            setTextColor(OfflinePalette.accent)
            setTypeface(typeface, Typeface.BOLD)
            letterSpacing = 0.1f
            setPadding(0, dp(12), 0, dp(4))
        })
        addView(label(title, 28f).apply {
            setTypeface(typeface, Typeface.BOLD)
            setPadding(0, 0, 0, dp(20))
        })
    }

    private fun listLabel(title: String, detail: String, click: () -> Unit): Button {
        val value = if (detail.isBlank()) title else "$title\n$detail"
        val styled = SpannableString(value).apply {
            setSpan(StyleSpan(Typeface.BOLD), 0, title.length, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE)
            if (detail.isNotBlank()) {
                setSpan(ForegroundColorSpan(MUTED), title.length + 1, length, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE)
                setSpan(AbsoluteSizeSpan(12, true), title.length + 1, length, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE)
            }
        }
        return action(value, click = click).apply {
            text = styled
            textSize = 14f
            setTypeface(Typeface.DEFAULT, Typeface.NORMAL)
            gravity = Gravity.START or Gravity.CENTER_VERTICAL
            background = OfflineUi.rowBackground(activity, plain = true)
            maxLines = 3
            ellipsize = TextUtils.TruncateAt.END
            setPadding(dp(8), dp(8), dp(8), dp(8))
        }
    }

    private fun leadingIcon(icon: Int): ImageView = ImageView(activity).apply {
        setImageResource(icon)
        imageTintList = ColorStateList.valueOf(OfflinePalette.accent)
        setPadding(dp(10), dp(10), dp(10), dp(10))
        background = OfflineUi.cardBackground(activity, 10)
        importantForAccessibility = IMPORTANT_FOR_ACCESSIBILITY_NO
    }

    private fun addIconAction(parent: LinearLayout, icon: Int, label: String, selected: Boolean = false, enabled: Boolean = true, click: () -> Unit) {
        parent.addView(playerAction(View.NO_ID, icon, label, click = click).apply {
            isSelected = selected
            isEnabled = enabled
        }, LayoutParams(dp(48), dp(48)))
    }
    private fun text(id: Int, vararg values: Any): String = strings.getString(id, *values)
    private fun dp(value: Int) = (value * resources.displayMetrics.density).toInt()
    private fun bytes(value: Long): String {
        val safe = value.coerceAtLeast(0)
        if (safe < 1_000) return "$safe B"
        val units = arrayOf("KB", "MB", "GB", "TB")
        var amount = safe.toDouble()
        var index = -1
        while (amount >= 1_000 && index < units.lastIndex) {
            amount /= 1_000
            index++
        }
        return String.format(Locale.getDefault(), if (amount >= 10) "%.0f %s" else "%.1f %s", amount, units[index])
    }
    private fun durationValue(milliseconds: Long): String {
        val total = milliseconds.coerceAtLeast(0) / 1_000
        return text(R.string.offline_minutes_seconds, total / 60, total % 60)
    }

    private data class AlbumKey(val artist: String, val album: String)
    private data class LibrarySnapshot(
        val tracks: List<OfflineTrack>,
        val folders: List<OfflineFolder>,
        val playlists: List<OfflinePlaylist>,
        val usage: OfflineStorageUsage,
        val errors: List<JSONObject>,
    )

    companion object {
        private const val PAGE_SIZE = 100
        const val SCREEN_LIBRARY = "library"
        const val SCREEN_DOWNLOADS = "downloads"
        private const val SCREEN_PLAYLISTS = "playlists"
        private const val SCREEN_QUEUE = "queue"
        private const val SCREEN_SETTINGS = "settings"
        private const val TAB_TRACKS = "tracks"
        private const val TAB_ALBUMS = "albums"
        private const val TAB_ARTISTS = "artists"
        private const val TAB_GENRES = "genres"
        private const val TAB_LIKED = "liked"
        private const val TAB_FOLDERS = "folders"
        private const val STATE_SCREEN = "offline_screen"
        private const val STATE_TAB = "offline_tab"
        private const val STATE_QUERY = "offline_query"
        private const val STATE_LIKED = "offline_liked"
        private const val STATE_FOLDER = "offline_folder"
        private const val STATE_PLAYLIST = "offline_playlist"
        private const val STATE_FULL_PLAYER = "offline_full_player"
        private const val STATE_COLLECTION_KIND = "offline_collection_kind"
        private const val STATE_COLLECTION_FIRST = "offline_collection_first"
        private const val STATE_COLLECTION_SECOND = "offline_collection_second"
        private const val STATE_PAGE_STATE = "offline_page_state"

        private val BACKGROUND = OfflinePalette.background
        private val PANEL = OfflinePalette.panel
        private val FOREGROUND = OfflinePalette.foreground
        private val MUTED = OfflinePalette.muted
        private val ERROR = OfflinePalette.error

        private fun allFolders(library: OfflineLibrary): List<OfflineFolder> {
            val result = mutableListOf<OfflineFolder>()
            val pending = ArrayDeque<String>()
            pending += OfflineLibrary.ROOT_FOLDER_ID
            val visited = hashSetOf<String>()
            while (pending.isNotEmpty()) {
                val parent = pending.removeFirst()
                if (!visited.add(parent)) continue
                library.folders(parent).forEach { folder ->
                    result += folder
                    pending += folder.id
                }
            }
            return result
        }
    }
}
