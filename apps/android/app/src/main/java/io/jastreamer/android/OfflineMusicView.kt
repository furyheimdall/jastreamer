package io.jastreamer.android

import android.app.AlertDialog
import android.content.Context
import android.graphics.Color
import android.os.Bundle
import android.text.Editable
import android.text.InputType
import android.text.TextWatcher
import android.view.Gravity
import android.view.View
import android.view.ViewGroup.LayoutParams.MATCH_PARENT
import android.view.ViewGroup.LayoutParams.WRAP_CONTENT
import android.view.accessibility.AccessibilityEvent
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
    private val workspace = LinearLayout(activity)
    private val navigation = LinearLayout(activity)
    private val page = FrameLayout(activity)
    private val miniPlayer = LinearLayout(activity)
    private val playerTitle = TextView(activity)
    private val playerPosition = TextView(activity)
    private val miniArtwork = ImageView(activity)
    private val playPause = Button(activity)
    private val miniPrevious = Button(activity)
    private val miniNext = Button(activity)

    private var screen = savedState?.getString(STATE_SCREEN) ?: initialScreen
    private var libraryTab = savedState?.getString(STATE_TAB) ?: TAB_TRACKS
    private var searchQuery = savedState?.getString(STATE_QUERY).orEmpty()
    private var folderId = savedState?.getString(STATE_FOLDER) ?: OfflineLibrary.ROOT_FOLDER_ID
    private var openPlaylistId = savedState?.getString(STATE_PLAYLIST)
    private var fullPlayerOpen = savedState?.getBoolean(STATE_FULL_PLAYER) == true
    private var collectionKind = savedState?.getString(STATE_COLLECTION_KIND)
    private var collectionFirst = savedState?.getString(STATE_COLLECTION_FIRST)
    private var collectionSecond = savedState?.getString(STATE_COLLECTION_SECOND)

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
    private var fullPlayerPlayPause: Button? = null
    private var updatingSeek = false
    private var wideLayout: Boolean? = null
    private var moveInProgress = false
    private var miniArtworkPath: String? = null

    private var lastDownloadSignature: List<OfflineDownloadJob> = emptyList()
    private val downloadProgressViews = mutableMapOf<String, Pair<ProgressBar, TextView>>()
    private val selectedTrackIds = linkedSetOf<String>()
    private val selectedAlbums = linkedSetOf<AlbumKey>()
    private val selectedFolderIds = linkedSetOf<String>()
    private val listPages = linkedMapOf<List<String?>, Int>()

    init {
        id = R.id.offline_music_root
        orientation = VERTICAL
        setBackgroundColor(BACKGROUND)
        isFocusable = true
        buildHeader()
        workspace.orientation = VERTICAL
        navigation.id = R.id.offline_navigation
        page.id = R.id.offline_content
        workspace.addView(navigation, LayoutParams(MATCH_PARENT, WRAP_CONTENT))
        workspace.addView(page, LayoutParams(MATCH_PARENT, 0, 1f))
        addView(workspace, LayoutParams(MATCH_PARENT, 0, 1f))
        buildMiniPlayer()
        renderNavigation()
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
                val playingChanged = next.playing != playback.playing
                playback = next
                updatePlayerChrome()
                if (queueChanged && screen == SCREEN_QUEUE) renderQueue()
                if (fullPlayerOpen) {
                    if (queueChanged || controlsChanged) renderFullPlayer()
                    else if (playingChanged) fullPlayerPlayPause?.text = text(when {
                        playback.owner == "server" -> R.string.offline_play
                        playback.playing -> R.string.offline_pause
                        else -> R.string.offline_resume
                    })
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
        out.putString(STATE_QUERY, searchField?.text?.toString() ?: searchQuery)
        out.putString(STATE_FOLDER, folderId)
        out.putString(STATE_PLAYLIST, openPlaylistId)
        out.putBoolean(STATE_FULL_PLAYER, fullPlayerOpen)
        out.putString(STATE_COLLECTION_KIND, collectionKind)
        out.putString(STATE_COLLECTION_FIRST, collectionFirst)
        out.putString(STATE_COLLECTION_SECOND, collectionSecond)
    }

    fun openDownloads() {
        fullPlayerOpen = false
        screen = SCREEN_DOWNLOADS
        renderNavigation()
        renderPage()
    }

    fun handleBack(): Boolean {
        if (fullPlayerOpen) {
            fullPlayerOpen = false
            renderPage()
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
        applyAdaptiveLayout(width.takeIf { it > 0 } ?: resources.displayMetrics.widthPixels)
    }

    override fun onSizeChanged(w: Int, h: Int, oldw: Int, oldh: Int) {
        super.onSizeChanged(w, h, oldw, oldh)
        applyAdaptiveLayout(w)
    }

    override fun onDetachedFromWindow() {
        attached = false
        scope.cancel()
        super.onDetachedFromWindow()
    }

    private fun buildHeader() {
        val header = LinearLayout(activity).apply {
            orientation = HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            setPadding(dp(8), dp(4), dp(8), dp(4))
            minimumHeight = dp(56)
        }
        header.addView(action(text(R.string.offline_servers), R.id.offline_servers_button, onServers))
        header.addView(label(text(R.string.offline_saved_music), 20f).apply {
            id = R.id.offline_header_title
            gravity = Gravity.CENTER
        }, LayoutParams(0, WRAP_CONTENT, 1f))
        header.addView(action(text(R.string.offline_downloads), R.id.offline_downloads_button) { openDownloads() })
        addView(header, LayoutParams(MATCH_PARENT, WRAP_CONTENT))
    }

    private fun buildMiniPlayer() {
        miniPlayer.id = R.id.offline_mini_player
        miniPlayer.orientation = HORIZONTAL
        miniPlayer.gravity = Gravity.CENTER_VERTICAL
        miniPlayer.minimumHeight = dp(80)
        miniArtwork.importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO
        miniPlayer.addView(miniArtwork, LayoutParams(dp(64), dp(64)).apply { marginEnd = dp(8) })
        miniPlayer.setPadding(dp(8), dp(4), dp(8), dp(4))
        miniPlayer.setBackgroundColor(PANEL)
        val titles = LinearLayout(activity).apply { orientation = VERTICAL }
        playerTitle.id = R.id.offline_player_title
        playerTitle.setTextColor(FOREGROUND)
        playerTitle.textSize = 15f
        playerTitle.maxLines = 2
        playerPosition.id = R.id.offline_mini_position
        playerPosition.setTextColor(MUTED)
        playerPosition.textSize = 12f
        titles.addView(playerTitle)
        titles.addView(playerPosition)
        titles.setOnClickListener {
            fullPlayerOpen = true
            renderPage()
        }
        titles.contentDescription = text(R.string.offline_expand_player)
        titles.isFocusable = true
        miniPlayer.addView(titles, LayoutParams(0, MATCH_PARENT, 1f))
        configureAction(miniPrevious, text(R.string.offline_previous)) { OfflinePlayback.previous() }
        miniPlayer.addView(miniPrevious)
        playPause.setTextColor(FOREGROUND)
        playPause.isAllCaps = false
        playPause.minWidth = dp(48)
        playPause.minHeight = dp(48)
        playPause.setOnClickListener {
            if (playback.owner == "server") resumeOrRequestHandoff()
            else if (playback.playing) OfflinePlayback.pause() else resumeOrRequestHandoff()
        }
        miniPlayer.addView(playPause)
        configureAction(miniNext, text(R.string.offline_next)) { OfflinePlayback.next() }
        miniPlayer.addView(miniNext)
        addView(miniPlayer, LayoutParams(MATCH_PARENT, dp(80)))
        updatePlayerChrome()
    }

    private fun renderNavigation() {
        navigation.removeAllViews()
        val items = listOf(
            Triple(SCREEN_LIBRARY, R.string.offline_library, R.id.offline_library_button),
            Triple(SCREEN_PLAYLISTS, R.string.offline_playlists, R.id.offline_playlists_button),
            Triple(SCREEN_QUEUE, R.string.offline_queue, R.id.offline_queue_button),
            Triple(SCREEN_SETTINGS, R.string.offline_settings, R.id.offline_settings_button),
        )
        items.forEach { (target, title, id) ->
            val button = action(text(title), id) {
                fullPlayerOpen = false
                screen = target
                if (target != SCREEN_PLAYLISTS) openPlaylistId = null
                clearSelection()
                renderNavigation()
                renderPage()
            }
            button.isSelected = screen == target
            button.alpha = if (screen == target) 1f else 0.76f
            navigation.addView(button, if (wideLayout == true) {
                LayoutParams(MATCH_PARENT, WRAP_CONTENT)
            } else {
                LayoutParams(0, WRAP_CONTENT, 1f)
            })
        }
    }

    private fun applyAdaptiveLayout(widthPixels: Int) {
        if (widthPixels <= 0) return
        val isWide = widthPixels / resources.displayMetrics.density >= 700f
        if (wideLayout == isWide) return
        wideLayout = isWide
        workspace.removeAllViews()
        if (isWide) {
            workspace.orientation = HORIZONTAL
            navigation.orientation = VERTICAL
            workspace.addView(navigation, LayoutParams(dp(184), MATCH_PARENT))
            workspace.addView(page, LayoutParams(0, MATCH_PARENT, 1f))
        } else {
            workspace.orientation = VERTICAL
            navigation.orientation = HORIZONTAL
            workspace.addView(navigation, LayoutParams(MATCH_PARENT, WRAP_CONTENT))
            workspace.addView(page, LayoutParams(MATCH_PARENT, 0, 1f))
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
                renderPage()
                updatePlayerChrome()
            } catch (failure: Throwable) {
                if (failure is CancellationException) throw failure
                loading = false
                showFailure(failure)
                renderPage()
            }
        }
    }

    private fun renderPage() {
        if (!fullPlayerOpen) {
            playerSeek = null
            fullPlayerPosition = null
        }
        when {
            fullPlayerOpen -> renderFullPlayer()
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
    }

    private fun renderLibrary() {
        val body = vertical(dp(12))
        val tabs = LinearLayout(activity).apply { orientation = HORIZONTAL }
        listOf(
            TAB_TRACKS to R.string.offline_tracks,
            TAB_ALBUMS to R.string.offline_albums,
            TAB_ARTISTS to R.string.offline_artists,
            TAB_FOLDERS to R.string.offline_folders,
        ).forEach { (tab, title) ->
            tabs.addView(action(text(title)) {
                libraryTab = tab
                folderId = if (tab == TAB_FOLDERS) folderId else OfflineLibrary.ROOT_FOLDER_ID
                clearCollection()
                clearSelection()
                renderLibrary()
            }.apply { alpha = if (libraryTab == tab) 1f else 0.7f }, LayoutParams(0, WRAP_CONTENT, 1f))
        }
        body.addView(tabs)
        if (libraryTab != TAB_FOLDERS) {
            searchField = EditText(activity).apply {
                id = R.id.offline_search
                hint = text(R.string.offline_search)
                setHintTextColor(MUTED)
                setTextColor(FOREGROUND)
                setSingleLine(true)
                minimumHeight = dp(48)
                setText(searchQuery)
                setSelection(text.length)
                addTextChangedListener(object : TextWatcher {
                    override fun beforeTextChanged(s: CharSequence?, start: Int, count: Int, after: Int) = Unit
                    override fun onTextChanged(s: CharSequence?, start: Int, before: Int, count: Int) {
                        searchQuery = s?.toString().orEmpty()
                        renderLibraryResults()
                    }
                    override fun afterTextChanged(s: Editable?) = Unit
                })
            }
            body.addView(searchField)
        } else {
            searchField = null
        }
        selectionBar = LinearLayout(activity).apply {
            id = R.id.offline_selection_bar
            orientation = VERTICAL
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
    }

    private fun renderLibraryResults() {
        val list = pageList ?: return
        list.removeAllViews()
        renderSelectionBar()
        val filtered = filteredTracks()
        val drilldown = collectionTracks(filtered)
        if (drilldown != null) {
            val title = collectionSecond ?: collectionFirst.orEmpty()
            list.addView(action(text(if (collectionKind == TAB_ARTISTS) R.string.offline_artists else R.string.offline_albums)) {
                clearCollection()
                renderLibraryResults()
            })
            list.addView(label(title, 22f))
            if (drilldown.isNotEmpty()) {
                list.addView(action(text(R.string.offline_play_saved, drilldown.size)) {
                    playTracks(drilldown, drilldown.first().id)
                })
            }
            renderTracks(list, drilldown, drilldown)
            return
        }
        when (libraryTab) {
            TAB_TRACKS -> renderTracks(list, filtered, filtered)
            TAB_ALBUMS -> renderAlbums(list, filtered)
            TAB_ARTISTS -> renderArtists(list, filtered)
            TAB_FOLDERS -> renderFolders(list)
        }
    }

    private fun filteredTracks(): List<OfflineTrack> {
        val query = searchQuery.trim()
        if (query.isEmpty()) return tracks
        return tracks.filter {
            it.title.contains(query, ignoreCase = true) ||
                it.artist.contains(query, ignoreCase = true) ||
                it.album.contains(query, ignoreCase = true) ||
                it.albumArtist.contains(query, ignoreCase = true)
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
        else -> null
    }

    private fun clearCollection() {
        collectionKind = null
        collectionFirst = null
        collectionSecond = null
    }

    private fun pageKey(section: String): List<String?> = listOf(
        section, screen, libraryTab, folderId, collectionKind, collectionFirst,
        collectionSecond, searchQuery, openPlaylistId,
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

    private fun renderTracks(parent: LinearLayout, values: List<OfflineTrack>, playOrder: List<OfflineTrack>) {
        if (values.isEmpty()) {
            renderEmptyLibrary(parent)
            return
        }
        val selectedIds = effectiveSelectedTrackIds()
        renderPaged(parent, pageKey("tracks"), values.size) { index ->
            val track = values[index]
            val row = row()
            val selected = track.id in selectedIds
            val title = buildString {
                if (selected) append("✓ ")
                append(track.title)
                append("\n")
                append(text(
                    R.string.offline_track_details,
                    displayArtist(track),
                    displayAlbum(track),
                    track.codec.uppercase(Locale.ROOT),
                    displayQuality(track.quality),
                    bytes(track.byteSize),
                ))
                if (track.pendingDelete) append("\n").append(text(R.string.offline_pending_delete))
            }
            row.addView(artwork(track, 56), LayoutParams(dp(56), dp(56)).apply { marginEnd = dp(6) })
            val main = action(title) {
                if (hasSelection()) toggleTrack(track.id) else playTracks(playOrder, track.id)
            }.apply {
                gravity = Gravity.START or Gravity.CENTER_VERTICAL
                isSelected = selected
                setOnLongClickListener { toggleTrack(track.id); true }
                contentDescription = "${track.title}, ${displayArtist(track)}, ${displayAlbum(track)}"
            }
            row.addView(main, LayoutParams(0, WRAP_CONTENT, 1f))
            parent.addView(row)
        }
    }

    private fun renderAlbums(parent: LinearLayout, values: List<OfflineTrack>) {
        val albums = values.groupBy { AlbumKey(it.albumArtist.ifBlank { it.artist }, it.album.ifBlank { text(R.string.offline_unknown_album) }) }
        if (albums.isEmpty()) {
            renderEmptyLibrary(parent)
            return
        }
        val orderedAlbums = albums.entries.sortedBy { it.key.album.lowercase(Locale.getDefault()) }
        renderPaged(parent, pageKey("albums"), orderedAlbums.size) { index ->
            val (key, albumTracks) = orderedAlbums[index]
            val ordered = albumTracks.sortedWith(compareBy<OfflineTrack> { it.disc }.thenBy { it.track }.thenBy { it.title })
            val selected = key in selectedAlbums
            val row = row()
            row.addView(artwork(ordered.first(), 64), LayoutParams(dp(64), dp(64)).apply { marginEnd = dp(6) })
            row.addView(action((if (selected) "✓ " else "") + key.album + "\n" + key.artist + " · " + text(R.string.offline_count_tracks, ordered.size)) {
                if (hasSelection()) toggleAlbum(key) else {
                    collectionKind = TAB_ALBUMS
                    collectionFirst = key.artist
                    collectionSecond = key.album
                    renderLibraryResults()
                }
            }.apply {
                gravity = Gravity.START or Gravity.CENTER_VERTICAL
                isSelected = selected
                setOnLongClickListener { toggleAlbum(key); true }
            }, LayoutParams(0, WRAP_CONTENT, 1f))
            row.addView(action(text(R.string.offline_play)) { playTracks(ordered, ordered.first().id) })
            parent.addView(row)
        }
    }

    private fun renderArtists(parent: LinearLayout, values: List<OfflineTrack>) {
        val groups = values.groupBy { it.artist.ifBlank { text(R.string.offline_unknown_artist) } }
        if (groups.isEmpty()) {
            renderEmptyLibrary(parent)
            return
        }
        val orderedArtists = groups.entries.sortedBy { it.key.lowercase(Locale.getDefault()) }
        renderPaged(parent, pageKey("artists"), orderedArtists.size) { index ->
            val (artist, artistTracks) = orderedArtists[index]
            val ordered = artistTracks.sortedWith(compareBy<OfflineTrack> { it.album }.thenBy { it.disc }.thenBy { it.track })
            val row = row()
            row.addView(action("$artist\n${text(R.string.offline_count_tracks, ordered.size)}") {
                collectionKind = TAB_ARTISTS
                collectionFirst = artist
                collectionSecond = null
                renderLibraryResults()
            }.apply { gravity = Gravity.START or Gravity.CENTER_VERTICAL }, LayoutParams(0, WRAP_CONTENT, 1f))
            row.addView(action(text(R.string.offline_play)) { playTracks(ordered, ordered.first().id) })
            parent.addView(row)
        }
    }

    private fun renderFolders(parent: LinearLayout) {
        val current = if (folderId == OfflineLibrary.ROOT_FOLDER_ID) null else folders.firstOrNull { it.id == folderId }
        val breadcrumb = folderBreadcrumb(folderId)
        parent.addView(label(breadcrumb, 18f).apply { setPadding(dp(4), dp(8), dp(4), dp(8)) })
        val actions = row()
        if (folderId != OfflineLibrary.ROOT_FOLDER_ID) {
            actions.addView(action(text(R.string.offline_parent_folder)) {
                folderId = current?.parentId ?: OfflineLibrary.ROOT_FOLDER_ID
                clearSelection()
                renderLibrary()
            })
        }
        actions.addView(action(text(R.string.offline_new_folder), R.id.offline_new_folder_button) { promptNewFolder(folderId) })
        if (current != null) {
            actions.addView(action(text(R.string.offline_rename), R.id.offline_rename_folder_button) { promptRenameFolder(current) })
            actions.addView(action(text(R.string.offline_move), R.id.offline_move_folder_button) { chooseDestination { moveFolder(current, it) } })
            actions.addView(action(text(R.string.offline_delete_device), R.id.offline_delete_folder_button) {
                selectedFolderIds.clear()
                selectedFolderIds += current.id
                confirmDeleteSelection()
            })
        }
        parent.addView(actionStrip(actions))
        val children = folders.filter { it.parentId == folderId }.sortedBy { it.name.lowercase(Locale.getDefault()) }
        renderPaged(parent, pageKey("folders"), children.size) { index ->
            val folder = children[index]
            val descendants = recursiveTrackIds(setOf(folder.id))
            val selected = folder.id in selectedFolderIds
            val button = action(
                (if (selected) "✓ " else "") + folder.name + "\n" +
                    text(R.string.offline_folder_summary, descendants.size, bytes(descendants.sumOf { id -> trackIndex[id]?.byteSize ?: 0 })),
            ) {
                if (hasSelection()) toggleFolder(folder.id) else {
                    folderId = folder.id
                    clearSelection()
                    renderLibrary()
                }
            }.apply {
                gravity = Gravity.START or Gravity.CENTER_VERTICAL
                isSelected = selected
                setOnLongClickListener { toggleFolder(folder.id); true }
            }
            parent.addView(button, LayoutParams(MATCH_PARENT, WRAP_CONTENT))
        }
        val directTracks = tracks.filter { it.folderId == folderId }
        if (directTracks.isNotEmpty()) renderTracks(parent, directTracks, directTracks)
        if (children.isEmpty() && directTracks.isEmpty()) renderEmptyLibrary(parent)
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
        val active = openPlaylistId?.let { id -> playlists.firstOrNull { it.id == id } }
        if (active == null) {
            openPlaylistId = null
            body.addView(action(text(R.string.offline_create_playlist)) { promptPlaylistName(null, emptyList()) })
            if (playlists.isEmpty()) body.addView(message(text(R.string.offline_no_playlists)))
            val orderedPlaylists = playlists.sortedBy { it.name.lowercase(Locale.getDefault()) }
            renderPaged(body, pageKey("playlists"), orderedPlaylists.size) { index ->
                val playlist = orderedPlaylists[index]
                val saved = playlist.trackIds.count(trackIndex::containsKey)
                val row = row()
                row.addView(action("${playlist.name}\n${text(R.string.offline_playlist_partial, saved, playlist.missingTitles.size + playlist.trackIds.size - saved)}") {
                    openPlaylistId = playlist.id
                    renderPlaylists()
                }.apply { gravity = Gravity.START or Gravity.CENTER_VERTICAL }, LayoutParams(0, WRAP_CONTENT, 1f))
                row.addView(action(text(R.string.offline_play_saved, saved)) { playPlaylist(playlist) }.apply { isEnabled = saved > 0 })
                body.addView(row)
            }
        } else {
            body.addView(action(text(R.string.offline_playlists)) { openPlaylistId = null; renderPlaylists() })
            body.addView(label(active.name, 24f))
            val headerActions = row()
            val playable = active.trackIds.filter(trackIndex::containsKey)
            headerActions.addView(action(text(R.string.offline_play_saved, playable.size)) { playPlaylist(active) }.apply { isEnabled = playable.isNotEmpty() })
            headerActions.addView(action(text(R.string.offline_rename_playlist)) { promptPlaylistName(active, active.trackIds) })
            headerActions.addView(action(text(R.string.offline_delete_playlist)) { confirmDeletePlaylist(active) })
            body.addView(actionStrip(headerActions))
            renderPaged(body, pageKey("playlist-entries"), active.trackIds.size) { index ->
                val trackId = active.trackIds[index]
                val track = trackIndex[trackId]
                val row = row()
                row.addView(label(track?.let { "${it.title}\n${displayArtist(it)}" } ?: text(R.string.offline_missing_track), 14f), LayoutParams(0, WRAP_CONTENT, 1f))
                row.addView(action(text(R.string.offline_move_up)) { reorderPlaylist(active, index, -1) }.apply { isEnabled = index > 0 })
                row.addView(action(text(R.string.offline_move_down)) { reorderPlaylist(active, index, 1) }.apply { isEnabled = index < active.trackIds.lastIndex })
                row.addView(action(text(R.string.offline_remove_playlist_entry)) { removePlaylistEntry(active, index) })
                body.addView(row)
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
        body.addView(playerModeControls())
        if (playback.queue.entries.isEmpty()) body.addView(message(text(R.string.offline_no_queue)))
        renderPaged(body, pageKey("queue"), playback.queue.entries.size) { index ->
            val entry = playback.queue.entries[index]
            val track = trackIndex[entry.trackId]
            val current = entry.id == playback.queue.currentEntryId
            val row = row()
            row.addView(label((if (current) "▶ " else "") + (track?.title ?: text(R.string.offline_missing_track)), 14f), LayoutParams(0, WRAP_CONTENT, 1f))
            row.addView(action(text(R.string.offline_move_up)) { OfflinePlayback.moveEntry(entry.id, -1) }.apply {
                isEnabled = index > 0
            })
            row.addView(action(text(R.string.offline_move_down)) { OfflinePlayback.moveEntry(entry.id, 1) }.apply {
                isEnabled = index < playback.queue.entries.lastIndex
            })
            row.addView(action(text(R.string.offline_remove_queue)) { OfflinePlayback.removeEntry(entry.id) })
            body.addView(row)
        }
        setPage(ScrollView(activity).apply { addView(body) })
    }

    private fun renderFullPlayer() {
        val body = vertical(dp(16))
        body.gravity = Gravity.CENTER_HORIZONTAL
        body.addView(action(text(R.string.offline_close_player)) { fullPlayerOpen = false; renderPage() })
        val track = currentTrack()
        track?.let {
            body.addView(artwork(it, 240), LayoutParams(dp(240), dp(240)).apply {
                gravity = Gravity.CENTER_HORIZONTAL
            })
        }
        body.addView(label(track?.title ?: text(R.string.offline_no_queue), 26f).apply { gravity = Gravity.CENTER })
        body.addView(label(track?.let(::displayArtist) ?: "", 16f).apply { gravity = Gravity.CENTER; setTextColor(MUTED) })
        val seek = SeekBar(activity).apply {
            id = R.id.offline_player_seek
            isEnabled = playback.owner != "server"
            max = (track?.durationMs ?: 0L).coerceIn(0L, Int.MAX_VALUE.toLong()).toInt()
            progress = playback.queue.positionMs.coerceIn(0L, max.toLong()).toInt()
            contentDescription = text(R.string.offline_seek)
            minimumHeight = dp(48)
            setOnSeekBarChangeListener(object : SeekBar.OnSeekBarChangeListener {
                private var dragging = false
                override fun onProgressChanged(seekBar: SeekBar?, value: Int, fromUser: Boolean) {
                    if (fromUser && !updatingSeek) {
                        fullPlayerPosition?.text = duration(value.toLong(), max.toLong())
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
        body.addView(seek, LayoutParams(MATCH_PARENT, WRAP_CONTENT))
        val positionLabel = label(duration(playback.queue.positionMs, track?.durationMs ?: 0L), 14f).apply {
            id = R.id.offline_player_position
            gravity = Gravity.CENTER
        }
        fullPlayerPosition = positionLabel
        body.addView(positionLabel)
        val controls = row().apply { gravity = Gravity.CENTER }
        controls.addView(action(text(R.string.offline_previous)) { OfflinePlayback.previous() }.apply {
            isEnabled = playback.owner != "server"
        })
        controls.addView(action(text(when {
            playback.owner == "server" -> R.string.offline_play
            playback.playing -> R.string.offline_pause
            else -> R.string.offline_resume
        })) {
            if (playback.owner == "server") resumeOrRequestHandoff()
            else if (playback.playing) OfflinePlayback.pause() else resumeOrRequestHandoff()
        }.also { fullPlayerPlayPause = it })
        controls.addView(action(text(R.string.offline_next)) { OfflinePlayback.next() }.apply {
            isEnabled = playback.owner != "server"
        })
        controls.addView(action(text(R.string.offline_stop)) { OfflinePlayback.stop() }.apply {
            isEnabled = playback.owner != "server"
        })
        body.addView(actionStrip(controls))
        body.addView(playerModeControls())
        playback.errorMessage?.takeIf { it.isNotBlank() }?.let { body.addView(label(it, 14f).apply { setTextColor(ERROR) }) }
        setPage(ScrollView(activity).apply { isFillViewport = true; addView(body) })
    }

    private fun playerModeControls(): LinearLayout = row().apply {
        addView(action(text(if (playback.queue.shuffle) R.string.offline_shuffle_on else R.string.offline_shuffle_off)) {
            OfflinePlayback.setShuffle(!playback.queue.shuffle)
        })
        val repeatText = when (playback.queue.repeatMode) {
            Player.REPEAT_MODE_ONE -> R.string.offline_repeat_one
            Player.REPEAT_MODE_ALL -> R.string.offline_repeat_all
            else -> R.string.offline_repeat_off
        }
        addView(action(text(repeatText)) {
            val next = when (playback.queue.repeatMode) {
                Player.REPEAT_MODE_OFF -> Player.REPEAT_MODE_ALL
                Player.REPEAT_MODE_ALL -> Player.REPEAT_MODE_ONE
                else -> Player.REPEAT_MODE_OFF
            }
            OfflinePlayback.setRepeat(next)
        })
    }

    private fun updatePlayerChrome() {
        val current = currentTrack()
        miniPlayer.visibility = if (playback.queue.entries.isEmpty()) GONE else VISIBLE
        playerTitle.text = current?.title ?: text(R.string.offline_missing_track)
        playerPosition.text = duration(playback.queue.positionMs, current?.durationMs ?: 0L)
        playPause.text = text(when {
            playback.owner == "server" -> R.string.offline_play
            playback.playing -> R.string.offline_pause
            else -> R.string.offline_resume
        })
        fullPlayerPosition?.text = duration(playback.queue.positionMs, current?.durationMs ?: 0L)
        if (miniArtworkPath != current?.artworkPath || miniArtwork.drawable == null) {
            miniArtworkPath = current?.artworkPath
            OfflineArtworkLoader.load(scope, miniArtwork, miniArtworkPath, dp(64))
        }
        miniPrevious.isEnabled = playback.owner != "server"
        miniNext.isEnabled = playback.owner != "server"
        val seek = playerSeek
        if (seek != null && fullPlayerOpen) {
            updatingSeek = true
            seek.max = (current?.durationMs ?: 0L).coerceIn(0L, Int.MAX_VALUE.toLong()).toInt()
            seek.progress = playback.queue.positionMs.coerceIn(0L, seek.max.toLong()).toInt()
            updatingSeek = false
        }
    }

    private fun renderDownloads() {
        if (screen != SCREEN_DOWNLOADS || fullPlayerOpen) return
        lastDownloadSignature = downloads.map { it.copy(receivedBytes = 0) }
        downloadProgressViews.clear()
        val body = vertical(dp(12))
        body.addView(label(text(R.string.offline_downloads), 24f))
        val policy = CheckBox(activity).apply {
            text = text(R.string.offline_allow_metered)
            setTextColor(FOREGROUND)
            minHeight = dp(48)
            isChecked = meteredAllowed
            setOnCheckedChangeListener { _, allowed ->
                meteredAllowed = allowed
                runStoreAction { OfflineDownloads.setAllowMetered(activity.applicationContext, allowed) }
            }
        }
        body.addView(label(text(R.string.offline_network_policy), 18f))
        body.addView(policy)
        val list = LinearLayout(activity).apply {
            id = R.id.offline_download_list
            orientation = VERTICAL
        }
        if (downloads.isEmpty()) list.addView(message(text(R.string.offline_download_empty)))
        downloads.forEach { job ->
            val card = vertical(dp(8)).apply { setBackgroundColor(PANEL) }
            card.addView(label(job.title, 18f))
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
        body.addView(label(text(R.string.offline_storage), 24f))
        body.addView(label(text(R.string.language), 20f).apply { setPadding(0, dp(20), 0, dp(8)) })
        body.addView(action(if (language == "ko") "한국어" else "English") {
            AlertDialog.Builder(activity)
                .setTitle(text(R.string.language))
                .setSingleChoiceItems(arrayOf("English", "한국어"), if (language == "ko") 1 else 0) { dialog, index ->
                    dialog.dismiss()
                    onLanguage(if (index == 1) "ko" else "en")
                }
                .show()
        })
        val limit = if (storageUsage.limitBytes == 0L) text(R.string.offline_no_limit) else bytes(storageUsage.limitBytes)
        body.addView(label(text(R.string.offline_storage_usage, bytes(storageUsage.musicBytes), bytes(storageUsage.availableBytes), limit), 16f).apply {
            id = R.id.offline_storage_usage
        })
        val limitInput = EditText(activity).apply {
            hint = text(R.string.offline_limit_mb)
            setHintTextColor(MUTED)
            setTextColor(FOREGROUND)
            inputType = InputType.TYPE_CLASS_NUMBER
            minimumHeight = dp(48)
            if (storageUsage.limitBytes > 0) setText((storageUsage.limitBytes / 1_000_000L).toString())
        }
        body.addView(limitInput)
        body.addView(action(text(R.string.offline_apply_limit)) {
            val mb = limitInput.text.toString().toLongOrNull()
            if (mb == null || mb > Long.MAX_VALUE / 1_000_000L) {
                Toast.makeText(activity, text(R.string.offline_invalid_number), Toast.LENGTH_LONG).show()
            } else runStoreAction { library.setLimitBytes(mb * 1_000_000L) }
        })
        body.addView(label(text(R.string.offline_storage_note), 13f).apply { setTextColor(MUTED) })
        body.addView(action(text(R.string.offline_delete_all)) { confirmDeleteTracks(tracks.map { it.id }) })
        body.addView(label(text(R.string.offline_diagnostics), 20f).apply { setPadding(0, dp(20), 0, dp(8)) })
        val errorList = LinearLayout(activity).apply {
            id = R.id.offline_diagnostics
            orientation = VERTICAL
        }
        if (diagnostics.isEmpty()) errorList.addView(message(text(R.string.offline_no_diagnostics)))
        diagnostics.asReversed().forEach { item ->
            val message = text(
                R.string.offline_error,
                item.optString("code", "error"),
                item.optString("message", ""),
            )
            val trackId = item.optString("track_id")
            val track = trackIndex[trackId]
            val detail = if (track == null) {
                if (trackId.isBlank()) message else "$trackId\n$message"
            } else {
                text(
                    R.string.offline_diagnostic_track,
                    track.title,
                    track.codec.uppercase(Locale.ROOT),
                    displayQuality(track.quality),
                    message,
                )
            }
            errorList.addView(label(detail, 13f).apply { setTextColor(ERROR) })
        }
        body.addView(errorList)
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
        val ids = playback.queue.entries.map { it.trackId }
        if (ids.isEmpty()) return
        val currentIndex = playback.queue.entries.indexOfFirst { it.id == playback.queue.currentEntryId }
            .coerceAtLeast(0)
        startPlayback(ids, currentIndex, false)
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
        val names = mutableListOf(text(R.string.offline_create_playlist))
        names += playlists.map { it.name }
        AlertDialog.Builder(activity)
            .setTitle(text(R.string.offline_add_playlist))
            .setItems(names.toTypedArray()) { _, which ->
                if (which == 0) promptPlaylistName(null, trackIds)
                else {
                    val playlist = playlists[which - 1]
                    runStoreAction { library.savePlaylist(playlist.id, playlist.name, playlist.trackIds + trackIds, playlist.missingTitles) }
                }
            }
            .setNegativeButton(text(R.string.offline_cancel), null)
            .show()
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
                TAB_ARTISTS -> selectedTrackIds += filteredTracks().map { it.id }
                TAB_FOLDERS -> {
                    selectedFolderIds += folders.filter { it.parentId == folderId }.map { it.id }
                    selectedTrackIds += tracks.filter { it.folderId == folderId }.map { it.id }
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
            "network_policy" -> R.string.offline_reason_network_policy
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
        page.removeAllViews()
        page.addView(view, FrameLayout.LayoutParams(MATCH_PARENT, MATCH_PARENT))
        view.sendAccessibilityEvent(AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED)
    }
    private fun artwork(track: OfflineTrack, sizeDp: Int): ImageView = ImageView(activity).apply {
        importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO
        scaleType = ImageView.ScaleType.CENTER_CROP
        OfflineArtworkLoader.load(scope, this, track.artworkPath, dp(sizeDp))
    }
    private fun configureAction(button: Button, value: String, click: () -> Unit) {
        button.text = value
        button.textSize = 13f
        button.isAllCaps = false
        button.setTextColor(FOREGROUND)
        button.minHeight = dp(48)
        button.minWidth = dp(48)
        button.setOnClickListener { click() }
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
        textSize = 13f
        isAllCaps = false
        setTextColor(FOREGROUND)
        minHeight = dp(48)
        minWidth = dp(48)
        setOnClickListener { click() }
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
    private fun duration(positionMs: Long, durationMs: Long): String {
        fun value(ms: Long): String {
            val total = ms.coerceAtLeast(0) / 1_000
            return text(R.string.offline_minutes_seconds, total / 60, total % 60)
        }
        return "${value(positionMs)} / ${value(durationMs)}"
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
        private const val TAB_FOLDERS = "folders"
        private const val STATE_SCREEN = "offline_screen"
        private const val STATE_TAB = "offline_tab"
        private const val STATE_QUERY = "offline_query"
        private const val STATE_FOLDER = "offline_folder"
        private const val STATE_PLAYLIST = "offline_playlist"
        private const val STATE_FULL_PLAYER = "offline_full_player"
        private const val STATE_COLLECTION_KIND = "offline_collection_kind"
        private const val STATE_COLLECTION_FIRST = "offline_collection_first"
        private const val STATE_COLLECTION_SECOND = "offline_collection_second"

        private val BACKGROUND = Color.rgb(12, 23, 19)
        private val PANEL = Color.rgb(24, 45, 37)
        private val FOREGROUND = Color.rgb(234, 245, 238)
        private val MUTED = Color.rgb(164, 186, 172)
        private val ERROR = Color.rgb(255, 180, 171)

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
