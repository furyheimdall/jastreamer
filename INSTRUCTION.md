# jastreamer 0.2 user guide

[Installation and upgrades](INSTALL.md) · [한국어 사용자 안내서](INSTRUCTION.ko.md) · [Project overview](README.md)

jastreamer runs a Server on Linux or native Windows. Both host the embedded Web interface, send music to UPnP/DLNA outputs, and can optionally enable Google Cast. AirPlay sending is available only in the Linux container package. Optional desktop, native mobile, and PWA clients can connect to a Server. In Server mode, **This device** adds local audio to the shared Server queue through browser audio in browsers and Desktop or the native Media3 service on Android. Android **Saved music** is a separate local library and device queue that does not require a Server connection. The native iOS client remains a Server controller and blocks media loads.

## Installation and updates

Use the [installation guide](INSTALL.md) for requirements, release verification, Linux/Synology or native Windows Server installation, optional clients, upgrades, and rollback. For agent-assisted installation or updates, begin at [AGENTS.md](AGENTS.md); do not copy credentials into an agent prompt.

## 1. First setup and everyday use

1. Open the installed Server's complete private-LAN URL (`http://<server-LAN-IP>:8080/` for the default Linux listener or port 18080 for the default native Windows listener). Create the first administrator account only on a new installation; the password must have at least 10 characters. After an update, use the existing account and session rather than repeating setup or clearing data.
2. English is the default. Open **Settings → General**, then choose **English** or **한국어** under **Language / 언어**. The menu name remains **Settings** in both languages. The change is immediate and remembered; it does not save Server configuration or send playback commands.
3. In **Settings → Library**, confirm the music root (`/music` for the standard Linux container), save, then choose **Scan now**. A new sample-enabled installation places three bundled MP3s under `jastreamer-samples`; they appear only after this explicit scan. Scanning supports FLAC, MP3, WAV/WAVE, Ogg/Vorbis, Opus, and M4A without modifying source files. Samples are never queued or played automatically.
   If that host music folder is empty, there is nothing to play: put your audio files in the exact host path confirmed during installation, then scan again. Bundled samples, when present, are only test tracks and do not represent your personal library.
   **Library scan**, immediately below **Music folders**, shows the latest five scans with local start and finish dates/times. Older records are not deleted by this display limit. Saving folder edits and starting a scan remain separate actions.
4. To use Google Cast, enable **Google Cast output** in **Settings → Playback & outputs**, save, and restart the Server. It remains disabled when `cast.enabled` is false or absent from an older configuration. Do not enable it merely because a receiver is present.
5. Browse Library or Playlists, add tracks or grouped views to Queue, and select an output while playback is stopped. Refresh outputs if a newly powered receiver is missing.
6. Use the player controls for Play/Pause, Stop, Previous, Next, and Seek when supported by the receiver. AirPlay may require a PIN or password; pair only while stopped.

### Settings categories

The Server's Settings screen groups existing controls into five tabs:

| Tab | Controls |
|---|---|
| General | Language, app installation, Android downloads, Server name, data directory, and account password |
| Network | HTTP/HTTPS, access rules, network adapters, discovery/polling intervals, and the Server audio URL |
| Library | Music folders, scanning, and background audio verification |
| Playback & outputs | Google Cast, AirPlay/help, FFmpeg, and audio conversion |
| Diagnostics | Playback/file-check history and CSV report downloads |

Switching tabs retains unsaved edits and does not save, restart, scan, or control playback. **Save settings** and **Discard changes** apply to all Server-setting tabs together; language and account actions remain separate. An invalid field in a hidden tab is revealed and focused before saving. Restart and configuration-conflict notices remain visible across tabs. On narrow screens, scroll the tab strip horizontally; keyboard users can use Left/Right, Home, and End.

### Playback and file-check history

Open **Settings → Diagnostics → Playback and file-check history** to view the Server's shared, persistent history. Filter by result type or renderer. Renderer reports include the renderer name and ID, track, error code, playback position when supplied, and expandable structured diagnostics. Older imported records may identify a renderer only by ID. The newest 5,000 records are retained; viewing them never changes playback.

**Scan now** is incremental. The first scan reads all music; later scans enumerate folders to find additions and missing files, then reuse stored metadata and completed verification results when the root/path, size, and modification time match. New or changed files are analyzed, unfinished checks resume, and unchanged failures or inconclusive results remain visible rather than being reported as passed. Results survive Server restarts.

Use the separate **Full rescan** button and confirmation to force metadata and audio verification for every file, including unchanged files. Use it when a file was replaced without changing its size or modification time, or when you explicitly want to repeat a completed check. It does not reset track IDs, likes, play counts, playlists, or Queue.

After successful indexing, **Background audio verification** decodes the required files one at a time with the Server's configured FFmpeg and pauses during playback or another scan. Its totals include reused results. FLAC checks also compare the decoded sample count and the STREAMINFO checksum when present. A completed index is not a passed integrity check.

Failed checks and files that could not be verified appear in the same history with the music-folder name and relative path. Missing engines, unsupported formats, timeouts, and changed or unreadable files are not reported as healthy. Progress resumes after a Server restart. These checks never repair, rewrite, delete, or automatically remove source files or queue entries.

In a PC or mobile web browser, select **File checks** to limit the report to audio verification, then choose **Download CSV**. The file contains all currently retained records matching the selected filters, not just the visible page. It includes UTC timestamps, outcomes, track/folder/relative-path information, error codes/messages, and structured details; renderer reports also retain their renderer and playback identifiers. UTF-8 with a BOM preserves Korean text in spreadsheet software, and formula-like cells are quoted as text to prevent execution.

This exports the existing failure/inconclusive history, not a complete pass certificate for every file. Exporting neither deletes history nor changes source files or playback. Android, iOS, and Desktop embedded clients keep their existing download restrictions and show guidance to open the same Server in a normal browser; sign in there separately if necessary.

The queue is Server-wide, preserves order and duplicates, and survives restarts. A Server restart does not automatically resume playback. Cast uses that same single queue, loads media with Cast autoplay disabled, and sends Play explicitly. Receiver groups and gapless playback are not supported.

### Folder navigation and music actions

Inside **Library → Folders**, **Parent folder** moves up one directory within the same music root. At that root, **Back to list** returns to the music-root list; the **Folders** tab also goes directly to that list. Navigation never changes Queue or starts playback.

A folder's **Play all**, **Play next**, **Add to queue end**, and **Add to saved playlist** actions include available matching tracks from the current folder and every subfolder, in path order. They include descendants even when the intermediate folder has no direct tracks or the selection spans multiple pages, without including sibling folders or other music roots. Folder browsing itself still shows one level at a time.

- **Play all** stops current playback, replaces Queue with the selection, and starts playback.
- **Play next** inserts after the current track, or at the front of Queue when there is no current track.
- **Add to queue end** appends after the existing queued tracks. Neither insertion action interrupts playback or starts it automatically.
- **Add to saved playlist** appends to an existing saved list or creates a new one without changing Queue.

Navigation and list actions share one toolbar. On narrow screens, scroll it horizontally to reach the remaining actions. **Add to queue end** uses a queue/down-arrow icon and changes the current playback queue. **Add to saved playlist** uses a bookmark-plus icon and opens the saved-playlist chooser; saving does not start playback.

<a id="likes"></a>
### Likes and one-time shuffled queues

- Use a track's heart button in Library, Playlists, Queue, or Track information to add or remove its like. All four views show a filled heart when liked and an outlined heart when unliked, and update together, including duplicate queue entries and an open information dialog. Changing a like does not change queue order or playback. Likes are shared Server state, not private per-account lists, and survive rescans and Server restarts.
- Select **Liked** in Library to browse liked tracks. Search and paging still apply to that view.
- In **Queue**, select **Shuffle liked into queue** to append all currently available liked tracks in random order, not just the current page or search results. Existing queue order and duplicate entries remain intact, current playback is not interrupted, and stopped playback does not start automatically. Unavailable tracks are excluded; no available likes, more than 10,000 selected tracks, or an append exceeding the total 10,000-entry queue limit produces an error rather than a partial append.
- This is a one-time selection for the shared queue, not a saved playlist or a live filter: later like changes do not rewrite the queued tracks. Queue still persists normally across Server restarts without autoplay.
- No playlist name is required and no saved playlist is created. To keep the result, explicitly use Queue's normal **Save as playlist** action. Previously saved playlists, including older liked-shuffle snapshots, remain unchanged.

<a id="playback-modes"></a>
### Playback order and repeat

- The player bar offers independent **Sequential / Shuffle** and **Repeat off / Repeat all / Repeat one** controls. Desktop shows them beside transport controls. A phone's compact bar has a **Playback modes** chooser, and the expanded player shows both controls directly.
- **Sequential** follows the displayed queue. **Shuffle** follows a separate randomized traversal without rearranging the displayed queue or collapsing duplicate track entries. Each queue entry occurs once per traversal; enabling shuffle or replacing the queue starts a new traversal. Explicit **Play next** additions remain next, including when more tracks are subsequently appended. Ordinary appended batches join the end of the traversal in shuffled order.
- **Repeat off** stops at the end. **Repeat all** starts another traversal; shuffle chooses a fresh order. **Repeat one** repeats only after the current track finishes naturally: **Next** still advances. Media failures are not repeated indefinitely.
- In shuffle, **Previous** follows the current traversal's visited order rather than picking another random track. The existing behavior of restarting the current seekable track after more than five seconds remains.
- Changing modes never starts stopped playback, restarts the current track, resets its position, or changes saved playlists. Server modes apply to the shared queue and all outputs, synchronize across Controls, and survive restart without autoplay. Android Saved music keeps its own independent local modes.

<a id="most-played"></a>
### Play counts and Most Played

- Open **Library → Most Played** to see available tracks with at least one counted play, ordered by count from highest to lowest. Each row shows its count; search and pagination apply to the globally ranked results. A track's information dialog shows the same count, including zero for an unplayed track.
- Counts are shared across the Server, not private per-account statistics. One playback session qualifies after 30 seconds of confirmed listening, or half the duration for a track shorter than one minute. If duration is unknown, the threshold is 30 seconds. The Server uses correlated renderer playback and position progress; this is not a claim that a human heard the physical output.
- Pause/resume keeps the accumulated listening time within that session and never counts the same session twice. Pause time, seek-skipped time, stalled progress, and gaps without reliable playback observations do not qualify. A new replay can add another count after meeting the threshold again.
- Counts start when this feature is installed; earlier listening is not reconstructed. Completed counts survive rescans and Server restarts. Partial listening below the threshold is not restored after a Server restart. Standalone Android **Saved music** playback does not contribute.
- Browsing statistics does not change the queue or start playback. Counts are stored in the Server database, not in music-file tags, and do not modify source audio.

<a id="browser-output"></a>
### This device: local audio output

The browser instructions below apply to browsers, PWA, and Desktop, not the native iOS client. For the native Android app, see [Android local playback](#android-controls). Browser and native Android outputs appear as **Local audio** and use the Server queue.

1. Stop playback, then choose the output marked **(This device)** under **Output device**, for example **Windows · Chrome (This device) [Local audio]**. On a phone, expand the compact player to reach the selector.
2. Choose a track or use the existing queue and press **Play**. If the browser blocks audio, select **Allow playback** on that page. If the pending request has already failed, dismiss the error and press Play again; commands are not silently replayed.
3. Use Pause, Stop, Previous, Next and supported Seek normally. The browser is an output of the existing Server queue, not a separate local queue.

The default name describes the OS/browser information available to the page, not the computer's hostname or a phone's user-assigned name. Use the pencil button **Name this local output** beside the output selector to save an alias such as **Office PC**. **Use default name**, or saving an empty alias, restores the automatic name. Names are limited to 80 UTF-8 bytes; Korean characters can use several bytes each.

The alias is saved in this browser profile for this Server UUID and exact origin (including port). It does not follow you to another browser/profile, private browsing session or replacement Server. Storage failures are reported rather than claimed as saved. Clearing browser storage removes the alias.

Only the page that registered and owns the output adds **(This device)**, for example **Office PC (This device)**. Other pages/devices see **Office PC** without that marker, even under the same account. A not-yet-registered page also offers its own local output with the marker; this is not a label on somebody else's renderer. Naming an unregistered browser does not register/select it. Renaming a registered output updates its name for other clients without changing its ID, queue, playback or output selection. The alias is a display name, not verified hardware identity.

Control uses same-origin authenticated HTTP JSON; audio uses HTTP(S) GET/Range through the browser's audio element. This is not UPnP, HLS, DASH or WebRTC. The browser and operating system choose the physical speaker/headphones; there is no hardware-output picker, native WASAPI/ASIO engine, exclusive mode or bit-perfect guarantee. Supported formats depend on the browser decoder. Conversion requires enabled transcoding and configured FFmpeg; converted WAV streams cannot seek.

Keep the owning browser page open. Closing or reloading it releases that browser output; loss of its live registration makes the output unavailable without discarding the queue. Stop if needed, reselect **This device**, then explicitly Play to resume. Closing another Control does not stop the owning browser, and network outputs remain independent of Control lifetime. Background/lock-screen playback in browsers, PWA, and Desktop Web content depends on the browser and OS and is not guaranteed. These clients have no offline playback or native background-audio service; the native Android app uses the service described below.

<a id="phone-controls"></a>
### Phone controls

The focused phone layout is selected automatically only for iPhone browsers or Android browsers whose user agent reports both Android and Mobile. An iPad, Android tablet, or merely narrow desktop window retains the existing layout. The phone header retains the account name and logout, and the four bottom tabs are **Library**, **Playlists**, **Queue**, and **Settings**. Ordinary browsers retain the jastreamer logo. Inside the Android, Desktop, and iOS apps, only the duplicate Web header/sidebar logo is hidden; account controls remain available.

The compact player keeps Play/Pause, Stop, and a **Playback modes** chooser immediately available. Use its expand arrow to show Seek, Previous, Next, shuffle/repeat, output selection and refresh, and AirPlay pairing when required; collapse it to return to the compact player. Primary playback, navigation and track-action buttons have at least 44-by-44-pixel targets, and the header, player, and bottom navigation account for device safe areas.

Selecting another bottom tab collapses the expanded player without stopping playback. Escape also collapses it when keyboard focus is inside the player. The expanded panel scrolls on short or landscape screens; the covered page is not interactive until the panel is closed.

The in-app shortcut installation card has been removed from **Settings**. Existing shortcuts still work, and a supporting browser may offer installation through its own menu. See the [phone PWA installation branch](INSTALL.md#pwa) for trusted-HTTPS and network-only limitations.

### Desktop entry

The Desktop app opens **Server playback**, with separate **Discovered now** and **Recent connections** card groups. Cards show the Server name, address, and checked availability; a saved entry is not proof that a Server is currently reachable. Select a card's connection action, or choose **Connect by address** to open the address dialog. Cancel or Escape closes it without connecting and retains the draft for reopening. Discovery, navigation, and connection do not start playback.

Desktop has no independent saved-music player or Local playback home card. After connecting, its shared Web **This device** output still uses the Server library and queue.

<a id="android-controls"></a>
### Native Android controls

The native Kotlin app adds Server selection, Server-controlled phone output, and an independent **Saved music** player around the shared Web interface. [Compatible Server/Web versions, installation, and APK update rules](INSTALL.md#android) are separate from PWA installation.

- The native home offers **Server playback** and **Local playback** cards. **Local playback** opens existing **Saved music** without connecting or signing in; its secondary **Downloads** action opens the device's transfer manager. **Server playback** opens separate **Discovered now** and **Recent connections** groups. For a manual URL, choose **Connect by address**, enter the HTTP(S) root address, then **Verify and connect**. A fresh launch does not connect automatically, and Server identity checks remain required.
- In Server mode, the native header identifies the selected Server and its complete origin. **Servers** returns to selection without stopping Server-controlled network outputs or saved-music playback. A changed Server UUID is rejected for a saved entry rather than silently reusing its session.
- Android applies system-bar and keyboard insets once in the native shell. Back dismisses the manual dialog or keyboard first, then follows the active Web or saved-music hierarchy. Web history returns to Server selection; Server selection and the root saved-music screen return to the home cards; Back from home leaves the app. Rotation retains the live screen and unsaved Web form state. Foreground return rechecks a Server before exposing its page, but a failed Server check does not block **Saved music**.
- Cookies and Web storage are isolated by Server UUID plus complete origin, including port. **Remove from recent servers** removes only the shortcut: it neither signs out nor deletes that isolated profile or saved music. Sign out inside the Server UI to end its session. Signing out stops that Server's incomplete imports; already committed music remains device-owned.
- Native language controls and **Settings → General → Language / 언어** support English and Korean. A Web-language change is reflected in the native shell when the page finishes loading or you leave or pause it.

**Saved music and downloads**

- **Saved music** opens the app's local **Library / Playlists / Queue / Settings** without Server HTML, network access, or login. Library defaults to **Albums**, with **Artists / Genres / Folders / Tracks / Liked** and search or collection drill-down. Genres use available saved metadata; likes belong to this device, not the Server. A completed import includes local metadata and artwork and remains available after logout, session expiry, account loss, Server/profile removal, or an older or unavailable Server.
- Track and collection actions follow the Server's **Play / Play next / Add to end / Add to playlist / Info** flow. Selection follows the current category and search results. Native move and delete actions manage only device-owned storage.
- A compatible Server/Web UI and Android app can show separate **Download** controls for tracks, albums, playlists, and folders. Confirm the destination and either **Original** (the verified source bytes) or **Space saving (AAC-LC 256 kbps in M4A)** when the Server advertises it. Downloading never starts playback or changes the shared Server queue. A playlist import is a one-time snapshot that preserves order and duplicate entries; it does not synchronize later Server edits.
- In the Server's **Folders** view, download a library root, a child folder, or the currently open folder. This snapshots up to 10,000 catalogued tracks recursively and imports them into the chosen local folder; it does not mirror the Server's directory hierarchy or create a playlist. Later Server changes require an explicit reimport.
- Accepted requests open progress in the Server screen immediately. Reopen it through **Settings → General → Downloads → View status**; there is no persistent Downloads entry above the Server page. The panel shows the current document's jobs, received bytes, track counts, waiting reasons, and completion/failure. An active Download control also reopens status; **Download again** starts a new confirmation. Open the native download manager for all jobs, including those restored after relaunch. The Settings entry appears only in compatible Android clients, not ordinary browsers, Desktop, or iOS.
- The default policy waits for an unmetered, non-mobile connection. A waiting job can open **Download network settings** directly from the Server screen. Using metered or mobile data requires explicit native confirmation or enabling **Allow metered or mobile data** in the manager; cancellation does not grant access. Roaming and unavailable connections remain blocked, and a reachable local Server does not require Internet validation.
- The native **Downloads** manager provides preparation, transfer, verification, local import, partial failure, pause/resume, cancellation, and destination controls. Pending work remains bound to its originating Server and signed-in principal. A Server without the v1 capability cannot start a new import, but it can still be controlled normally and cannot affect music already saved.
- **Remove download record** removes one completed, partially saved, cancelled, or failed record after confirmation. **Clear finished records** removes all such records while retaining active and paused downloads. Both leave saved music, artwork, folders, playlists, and the queue intact; cancel active work separately before removing its record. A compatible Server/Web status panel also drops removed native records when refreshed.
- Create, rename, reorder, and delete local playlists without changing Server playlists. Removing a playlist entry or deleting a local playlist leaves its audio file in place. The local queue preserves order and repeated occurrences: play a particular occurrence, move or remove entries, or save the queue as a local playlist. Adding entries does not start playback or reset its position. Seek, previous/next, shuffle, and repeat remain independent of the Server queue.
- The local mini-player directly exposes **Sequential / Shuffle** and repeat controls even while stopped or empty. Expanded-player and queue controls show the same local settings. These controls neither start playback nor reorder the displayed queue, and never change Server playback modes.
- Saved music uses the Server's navigation, dark/green styling, and labelled icon controls. The compact player stays visible when expanded controls open above it for seeking, previous/next, shuffle, repeat, queue, and track information; there is no second large artwork or duplicate playback bar. Wide layouts place metadata and controls side by side. Expanding or collapsing retains the browsing screen, search, scroll position, queue, and playback ownership. On phones, browsing content cannot receive input while playback controls are expanded.
- **Folders** are real directories inside app-private persistent music storage. Create, rename, and move folders; move selected tracks; choose a download destination; and resolve same-name collisions without overwrite. Local stable IDs, playlist entries, queue entries, and playback position survive a move. Interrupted storage operations are journaled and reconciled before another library change. These controls do not browse or modify NAS folders or other apps' files.
- **Delete from device** permanently removes selected local audio and updates local playlists and queue references after confirmation. Deleting a local playlist alone does not delete audio. If the current track is selected, choose **Delete when playback ends** or explicitly **Stop now and delete**; deferred deletion keeps the file until playback releases it.

**Playback ownership**

- One Media3 service owns this Android at a time: none, Server, or Saved music. Starting saved music while Server-owned, or assigning the phone to a Server while local music owns it, requires an explicit handoff confirmation. The previous owner is stopped before the next takes control; queues are not copied and late commands from the old owner cannot cross the handoff.
- In Server mode, explicitly select **Android · jastreamer (This device) [Local audio]**, or its saved alias, then press **Play**. The Server owns that queue and commands; Android's system media controls report Play, Pause, Stop, Previous, Next, and supported Seek back to it. In Saved music mode those controls execute against local files and the device queue.
- The native shell sends no Play or Stop merely because its screen closes, backgrounds, changes Servers, or reopens. Playback survives Activity/WebView recreation while its service remains alive. Process termination or terminal registration loss stops Server-owned playback. Local library, queue, and position are restored after restart, but neither owner registers, resumes, or autoplays without a new user action.
- Supported formats depend on Media3/Android decoders; original imports do not promise decoder compatibility, exclusive mode, or bit-perfect output. Transient Server-mode media failures use bounded recovery; terminal failures are shown rather than retried indefinitely.
- Only the verified Server's current top-level document receives restricted native interfaces for Server output and explicit imports. They expose no cookies, credentials, arbitrary native calls, filesystem browsing, or arbitrary URL downloads. External navigation, new windows, file pickers, and unrelated Web downloads/native permission requests remain blocked. Desktop and ordinary browsers retain browser audio; the native iOS client gains neither Android interface nor local playback.

The PWA installation card in the shared Web UI is for browser use; the native Android client needs no additional PWA installation. Use only a trusted Server, especially over unencrypted HTTP.

<a id="ios-controls"></a>
### Native iOS controls

The SwiftUI app wraps the same Server-hosted interface. Its current availability is [source and CI only](INSTALL.md#ios), not an installable phone release.

- The Server-only chooser separates **Discovered now** from **Recent connections**. Nearby cards have passed discovery verification; recent cards are verified when connecting, not labelled online merely because they were saved. Choose **Connect by address** to open a native sheet, enter the HTTP(S) root address, then select **Verify and connect**. Cancel preserves the draft; if verification is in progress, it cancels that attempt before closing. A fresh launch stays on selection rather than connecting automatically.
- The header shows the selected Server and complete origin, including port. **Change Server** returns to selection without stopping network-output playback. Foreground return rechecks the Server UUID before exposing the page; identity or network failure keeps the old page and its keyboard inaccessible.
- Cookies and local Web storage use named profiles keyed by verified Server UUID and canonical scheme/host/port. Different ports are separate sessions. Removing a recent entry only changes the list; sign out inside the Server UI to end its session.
- Back navigates available Web history; Reload refreshes the page. Use the keyboard's **Next** and **Done** for form entry. Rotation retains the live page and unsaved input. In compact-height keyboard layouts the back/reload bar hides, while Server switching and language remain available.
- Native language controls and **Settings → General → Language / 언어** support English and Korean. Web language is read back at page load, foreground return and native screen transitions, without a JavaScript bridge. Changing the native language reloads the Web page, so finish unsaved edits first.
- The native shell sends no Play or Stop when closing, backgrounding or switching Servers. It blocks Web media loads and has no Local playback entry, standalone native audio engine, offline player, or cached command queue. External navigation, new windows, downloads, file pickers and native media/device permission requests remain blocked; use a normal browser for browser audio or file-upload workflows.

The shared PWA card is for browser use, not an extra installation inside the native client. Use only a trusted Server, especially over unencrypted HTTP. If discovery fails, check Local Network access, Wi-Fi/multicast and VPN routing or enter the complete address manually.

### Google Cast media and completion boundaries

Direct Cast streaming is selected conservatively from inspected codec, sample-rate, channel, and, where applicable, bit-depth metadata. Every direct source must have a matching verified codec, a positive sample rate, and mono or stereo channels: FLAC is accepted through 96 kHz with 1–24-bit depth; MP3, Ogg/Vorbis, Ogg/Opus, and M4A/AAC through 48 kHz; and LPCM WAV through 48 kHz with 1–16-bit depth. Missing or mismatched metadata and sources outside those limits are not assumed compatible. With media transcoding enabled and FFmpeg configured, they instead use a nonseekable 44.1 kHz stereo 16-bit WAV stream. The source file is unchanged. Direct streaming avoids this conversion but does not promise bit-perfect receiver output.

Cast control keeps a persistent TLS connection and owns the application and media session it launches. Queue advance requires an explicit `FINISHED` status for that owned media; EOF, an empty status, `BUFFERING`, or `ERROR` is not treated as completion. Pause, seek, and accepted media still depend on the receiver.

### Server path and network selection

- **Browse** beside music folders, the data directory, HTTPS certificate/key files, FFmpeg and the Jastreamer AirPlay sender opens the authenticated **Server filesystem**, not this browser's computer. Windows lists accessible drives and accepts an absolute UNC share path; Linux/NAS starts at `/`. Containers expose only their mounted filesystem. Listings omit symbolic links and Windows reparse points; manual path inputs remain available.
- Navigate with roots, parent folder or an absolute directory path. **Choose** changes only the draft field; **Cancel** leaves it unchanged. Save settings explicitly, then scan music folders. Choosing a data directory does not move the existing database or artwork: preserve that data separately before changing storage and restarting.
- **Server network adapters** lists actual Server adapter names and IP/prefixes. Automatic leaves `network.interfaces` empty; explicit selections retain manually entered names. Unavailable adapters and addresses remain visible but cannot be newly selected for UPnP or Google Cast discovery. Cast mDNS uses UDP 5353 on these selected interfaces.
- **Server URL used by playback devices to fetch audio** is `media.base_url`: it tells a renderer where to fetch media from Server. The same address may also serve the Web UI, but this setting does not change listener bindings, ports, or the browser URL. Normally leave it blank so Server selects automatically. **Choose a Server network address** combines a detected Server IP with a currently enabled HTTP(S) listener and only fills the draft; save Settings to apply it. The list is not a receiver reachability test, so reject unsuitable VPN/container addresses. For a manual value, use a Server address and enabled listener port reachable from the receiver's LAN; `localhost` and the client PC's address are wrong for a remote receiver. An explicit value or listener address retains precedence. Cast also needs Server access to the receiver's mDNS-advertised Cast port.
- Browsing and adapter/IP selection never save, restart, scan or start playback by themselves. Apply the draft explicitly; listener, storage and network changes may require a restart.

### AirPlay setup help

**AirPlay setup help** beside **Jastreamer AirPlay sender path** explains the required sender; it does not install software, enable AirPlay, validate an arbitrary path, or control a receiver. The supported Linux `amd64`/`arm64` Server container includes `/usr/local/bin/jastreamer-airplay`, Python 3.12 with pinned pyatv 0.18.0, and FFmpeg as one matching runtime. Native Windows Server does not support AirPlay, even when a Linux or arbitrary helper path is entered.

A separately installed sender must use the adapter source and dependency file from the same release as the installed Server, not an arbitrary `main` revision. It must implement jastreamer's matching adapter/helper protocol and bring compatible dependencies. `atvremote`, a Python executable, pyatv alone, and receiver software such as Shairport Sync are not interchangeable sender paths. The popup prints full reference URLs for the [jastreamer AirPlay adapter source](https://github.com/furyheimdall/jastreamer/blob/main/apps/server/internal/airplay/helper.py), [pinned AirPlay requirements](https://github.com/furyheimdall/jastreamer/blob/main/packaging/server/requirements-airplay.txt), and [pyatv 0.18.0 source](https://github.com/postlund/pyatv/tree/v0.18.0); the `main` links do not guarantee compatibility with an installed release. A normal browser can follow them; the Desktop deliberately may block external windows, so copy a displayed URL into a normal browser if it does not open. Save and restart Server after changing a supported Linux sender path.

### Applying saved settings and restarting the Server

When saved settings require a restart, **Settings** shows a notice and **Restart server** button. The notice persists when you reopen the screen. Save or discard any unsaved edits before using the button. Confirming stops playback and restarts the Server with the saved settings. Existing accounts, queue and playlists are retained; playback does not resume automatically.

For an unchanged address, Control confirms reconnection to a new Server runtime before showing completion. If the address changes, open the new address shown. Failures and timeouts are not reported as completion, and the restart command is never retried automatically. A data-directory change cannot be applied with this button: migrate the existing data separately, then restart the Server manually. Older Servers without the restart API show manual-restart guidance.

### Artwork and Queue actions

- Player album artwork navigates to **Queue**; it does not show track information or start playback.
- Library `(i)` and Queue artwork open track information without starting playback. Queue artwork shows a large `(i)` on hover or keyboard focus; touch screens show it continuously.
- The triangular Play button is the first action on the right of each Queue row. Only that button starts the entry.

An isolated renderer-status query failure does not restart playback or open a popup. A status warning appears after three consecutive failed queries and closes automatically when a query succeeds. Dismissing it suppresses repeat popups during the same failure streak. Playback-command failures and confirmed disconnections are still reported immediately.

Playback-start errors identify the failed stage (`LoadTrack`, `PrepareMedia`, `SetURI`, or `Play`) and include a safe error code when available. For UPnP rejections, retain the action name and numeric fault code when reporting the error; for Cast, retain the action, player state, idle reason, and error text. Do not reset the queue or disable the firewall to clear a generic failure; timeout/transport failures still mean the command outcome is unknown.

For **Server-mode local audio** (browser or native Android), an unrecoverable track/media-engine failure during Play marks the entry **Playback failed** and lets the Server start the next entry in queue order. This is not natural completion: failed entries, duplicates, and ordering are preserved, and you can explicitly retry a failed entry. Continuation does not open a blocking error dialog; if no next entry remains, playback stops with an error. Paused/stopped sessions do not resume, and network, authentication, permission, lease-loss, or unclassified renderer failures do not skip tracks. Recoverable damaged frames are left to the decoder; jastreamer does not blindly seek ahead. Android continuation requires a matching Server and APK: update the Server first using the [update procedures](INSTALL.md#upgrade). Saved music instead uses the bounded local-file failure behavior described in its player and never reports a local-file failure as a NAS/Server source failure.

UPnP, Google Cast, and AirPlay capabilities vary by receiver. Confirm audible playback and the controls you need on your equipment.

## 2. Troubleshooting

| Problem | Check |
|---|---|
| Web page unavailable | Windows Server console and TCP 18080, or Linux `/healthz`, Compose logs, configured listener, and TCP 8080/8443 firewall access |
| Phone layout does not appear | Confirm the device is an iPhone or an Android browser reporting both Android and Mobile; iPads, Android tablets, and narrow desktop windows intentionally retain the existing layout |
| PWA install action does not appear | Open **Settings** and follow [PWA installation](INSTALL.md#pwa); a private-LAN HTTP origin shows the trusted-HTTPS requirement |
| Windows cannot discover Server | Allow mDNS UDP 5353 or enter the full Server URL manually |
| Android reports unsupported WebView | Update the device's Android System WebView/Chrome provider and reopen the app for isolated Server profiles, Server output, and new imports; shared cookies are never used as a fallback, while already saved music remains a separate local path |
| Android cannot discover or reconnect | Server mode: check Wi-Fi, mDNS UDP 5353, VPN/client isolation and app network access; enter the complete URL manually, and retain identity/TLS errors rather than bypassing them. These failures do not block already saved music |
| No output appears | Allow SSDP UDP 1900 for UPnP; Google Cast needs mDNS UDP 5353 on the selected interfaces plus Server TCP access to the receiver's advertised Cast port; Linux AirPlay also needs mDNS UDP 5353 and host networking; disable client isolation |
| Output cannot play | Permit Server-to-receiver control/stream traffic and receiver-to-Server media HTTP(S) traffic; normally leave **Server URL used by playback devices to fetch audio** blank, or set it only to a specific receiver-reachable Server origin; for unsupported Cast originals, enable conversion only with a configured FFmpeg |
| Server-mode **This device** is silent or disconnected | Select **Allow playback** if shown, check browser/OS mute and audio routing, and keep the owning page open; after reload or lease loss, Stop if needed, reselect **This device** and explicitly Play; preserve decoder and transport errors |
| Cast FLAC fails after seeking near EOF | Preserve the reported `BUFFERING`/`ERROR`; this receiver-dependent failure was independently reproduced and is not completion, so do not skip the queue entry or weaken the owned `FINISHED` requirement |
| Library is empty | Confirm the Windows folder or Linux `/music` mount is the configured library root, the Server account/UID 10001 can read it, and a scan completed; bundled `jastreamer-samples` also require an explicit scan |
| Settings cannot save | Confirm Windows adjacent files or Linux config directory and `server.json` are writable by the Server account/UID 10001 |
| AirPlay authorization fails | Supported Linux Server only: stop playback, repeat the displayed PIN/password flow, and keep the packaged `/usr/local/bin/jastreamer-airplay` and FFmpeg paths; native Windows cannot enable AirPlay with another path |
| Password lost | Stop the Server, then run `jastreamer-server --reset-password USER --config /etc/jastreamer/server.json` in a maintenance container with the same config/data mounts; enter the new password only at the prompt |
| Android playback stops with the screen off, or a local output goes offline | Preserve the failure time and `<data_dir>/logs/server.log*` before reconnecting if possible. The Server records registration/lease expiry, last poll/renewal/report times, command outcomes, and media grant/access failures. These identify what the Server observed, not why Android suspended or terminated the app |
| Android reports a playback error but the output is still available | Check `native_playback_error` in `<data_dir>/logs/server.log*`. Matching Android/Server builds automatically retain the reported playback exception with the output, play, and command IDs; a playback failure does not itself mean registration was lost |

When reporting a problem, include Server version, exact image digest or Windows ZIP SHA-256, Server platform/architecture, relevant logs, and receiver model. Remove passwords, cookies, certificates, and private keys; preserve raw diagnostic wording.

Server diagnostics use UTC and are written to both the console and `<data_dir>/logs/server.log`. The default Linux container path is `/var/lib/jastreamer/logs/server.log`, retained in the existing data mount across container replacement. Rotation keeps the current file and `server.log.1`–`server.log.3`, each at most 5 MiB. Normal local-output activity is summarized at most once every 30 seconds per registration; repeated request failures are rate-limited. Diagnostic events correlate generated output/play/command IDs without recording credentials, cookies, media URLs, filenames, or output display names. On POSIX systems, the log directory is created with mode 0700 and files with mode 0600; on Windows, restrict the data directory's access permissions to the Server account. If persistent logging cannot start, inspect the Server console or Compose logs. Collect all retained log files with the exact incident time; do not expose the data directory over HTTP.

Server-mode Android playback-error diagnostics require matching Server and APK versions; update the compatible Server before installing the corresponding APK using the [update procedures](INSTALL.md#upgrade). Failed native commands and terminal playback-error reports include the Media3 error code/name, occurrence time, playback position, cause classes, and bounded code-only stack frames. HTTP status and codec/audio-output numeric error codes are included when available. Exception messages, media URLs, file paths, and credentials are excluded. The Server retains accepted reports automatically in its existing rotating log, without an ADB collection step. This is not an offline upload queue: a process exit or loss of Server connectivity before delivery can still prevent collection, and previously lost exceptions are not reconstructed. Saved-music errors remain in on-device local diagnostics and are not uploaded automatically.
