# jastreamer 0.2 user guide

[Installation and upgrades](INSTALL.md) · [한국어 사용자 안내서](INSTRUCTION.ko.md) · [Project overview](README.md)

The Server owns your music index, the shared queue, and every playback command. It runs on Linux (container) or native Windows, hosts the Web interface that all clients display, and plays through UPnP/DLNA renderers, optional Google Cast receivers, AirPlay receivers (Linux container only), or a local output on the device in front of you.

This guide covers everyday use and troubleshooting. Installation, upgrades, and rollback are in the [installation guide](INSTALL.md). For agent-assisted installation or updates, start at [AGENTS.md](AGENTS.md) and never paste passwords or certificates into an agent prompt.

## Contents

- [Glossary](#glossary)
- [1. First setup and everyday use](#1-first-setup-and-everyday-use)
- [2. Library: scans and file checks](#library)
- [3. Queue, likes, and playback order](#queue)
  - [Likes and one-time shuffled queues](#likes) · [Playback order and repeat](#playback-modes) · [Play counts and Most Played](#most-played)
- [4. Outputs](#outputs)
  - [This device: local audio](#browser-output) · [Windows native audio](#windows-audio) · [Phone controls](#phone-controls) · [Desktop app](#desktop) · [Native Android](#android-controls) · [Native iOS](#ios-controls) · [Google Cast](#cast)
- [5. Settings reference](#settings)
- [6. Diagnostics and logs](#diagnostics)
- [7. Troubleshooting](#troubleshooting)

<a id="glossary"></a>
## Glossary

| Term | What it means in jastreamer |
|---|---|
| Server | The single program that indexes music, keeps the queue, and issues playback commands. Everything else is a remote control or an output. |
| Control | The Web interface the Server hosts. A browser, the desktop app, the native Android app, and the native iOS app all show the same Control. |
| Output device (renderer) | Where sound comes out: a UPnP/DLNA renderer, a Google Cast receiver, an AirPlay receiver, or a local output. Selected under **Output device**. |
| **This device** | The local output offered by the page or app you are using, listed as `… (This device) [Local audio]`. It is an output of the Server queue, never a second queue. |
| Queue | The shared, Server-wide playback list. It keeps its order and duplicate entries and survives Server restarts. Maximum 10,000 entries. |
| Current track | The loaded track and its position. It is tracked separately from queue membership, so removing or clearing queue entries never interrupts it. |
| **Saved music** | The Android app's offline library, playlists, and device queue. It is local to the phone and needs no Server. |

<a id="1-first-setup-and-everyday-use"></a>
## 1. First setup and everyday use

1. **Open the Server.** Use its complete private-LAN URL: `http://<server-LAN-IP>:8080/` for the default Linux listener, or port 18080 for the default native Windows listener. Create the first administrator account only on a new installation; the password needs at least 10 characters. After an update, sign in with the existing account instead of repeating setup or clearing data.
2. **Choose a language.** English is the default. Open **Settings → General** and pick **English** or **한국어** under **Language / 언어**. The menu itself stays named **Settings** in both languages. The choice applies immediately to this device only; it saves no Server configuration and sends no playback command.
3. **Point the Server at your music.** In **Settings → Library**, confirm the music root (`/music` for the standard Linux container), use **Save settings**, then choose **Scan now**. Nothing appears until that explicit scan. A new sample-enabled installation places three bundled MP3s under `jastreamer-samples`; they are test files, never queued or played automatically. If the host folder is empty there is nothing to play — put your audio files in the exact host path confirmed during installation and scan again.
4. **Optional: enable Google Cast.** In **Settings → Playback & outputs**, turn on **Google Cast output**, save, and restart the Server. It stays off while `cast.enabled` is false or missing from an older configuration. Do not enable it just because a receiver exists on the network.
5. **Queue music.** Browse or search **Library** and **Playlists**, then use **Play all**, **Play next**, or **Add to queue end**.
6. **Pick an output and play.** Select an **Output device** while playback is stopped; use **Find output devices again** if a receiver you just powered on is missing. AirPlay may ask for a PIN or password — pair only while stopped. Then use Play/Pause, Stop, Previous, Next, and Seek where the receiver supports it.

Supported formats are FLAC, MP3, WAV/WAVE, Ogg/Vorbis, Opus, and M4A. jastreamer never modifies, moves, or rewrites your source files.

### Keyboard and media keys

- Escape closes the open dialog (track information, server path browser, AirPlay help, playlist chooser, download status, restart confirmation) and collapses the expanded phone player.
- The **Settings** tab strip accepts Left/Right, Home, and End.
- The Web interface has no global play/pause hotkey; use the on-screen player.
- Hardware media keys work where a native output owns the session: Windows system media controls with [Windows native audio](#windows-audio), and Android's system media controls and lock screen with the [native Android app](#android-controls). Browser-backend local audio registers no OS media controls.

<a id="library"></a>
## 2. Library: scans and file checks

**Settings → Library** holds **Music folders**, **Library scan**, and **Background audio verification**. Saving folder edits and starting a scan are separate actions.

| Action | What it does |
|---|---|
| **Scan now** | Incremental. The first scan reads everything; later scans enumerate folders for additions and missing files, then reuse stored metadata and completed verification results when the root/path, size, and modification time all match. New or changed files are analyzed and unfinished checks resume. |
| **Full rescan** | Forces metadata and audio verification for every file, including unchanged ones, after a confirmation. Use it when a file was replaced without changing its size or modification time, or to repeat a completed check. It does not reset track IDs, likes, play counts, playlists, or Queue. |
| **Background audio verification** | After successful indexing, decodes the files that need it one at a time with the Server's configured FFmpeg. It pauses during playback or another scan, and its totals include reused results. FLAC checks also compare the decoded sample count and the STREAMINFO checksum when present. |

**Library scan** lists the five most recent scans with local start and finish times; that display limit deletes no older records. Unchanged failures and inconclusive results stay visible instead of being reported as passed, and results survive Server restarts.

A completed index is not a passed integrity check. Failures and files that could not be verified appear in the history with their music-folder name and relative path; missing engines, unsupported formats, timeouts, and changed or unreadable files are never reported as healthy. These checks never repair, rewrite, delete, or automatically remove source files or queue entries, and progress resumes after a Server restart.

<a id="queue"></a>
## 3. Queue, likes, and playback order

The queue is Server-wide: every Control sees the same list, order and duplicate entries are preserved, and it survives restarts — a restart never resumes playback by itself. Google Cast uses that same single queue, loads media with Cast autoplay disabled, and sends Play explicitly. Receiver groups and gapless playback are not supported.

### Removing tracks and clearing the queue

- Any entry can be removed, including the loaded, playing, paused, or selected track. **Now playing** keeps the loaded track and position independently, so removal sends no Stop and does not restart the audio.
- **Clear queue**, beside the Queue heading, removes every entry — previous, current, and upcoming — after confirmation. It is separate from **Shuffle liked into queue**. Neither action deletes music files, likes, or saved playlists.
- A playing track continues until it finishes naturally or you send a playback command. Pause, resume, and supported seeking still work with an empty queue; use **Stop** to stop the audio.
- **Repeat one** replays the current track after natural completion even when its queue entry was removed or the queue is empty, without restoring removed entries. **Next** follows the remaining sequential or shuffled order regardless of repeat mode. With **Repeat off** or **Repeat all**, completion stops playback once no entries remain. Adding tracks or using **Play next** after clearing does not start playback.
- A Server restart keeps the loaded selection and saved position without restoring removed entries or autoplaying. Android **Saved music** keeps its own separate local queue.

### Folder navigation and list actions

Inside **Library → Folders**, **Parent folder** moves up one directory within the same music root; at the root, **Back to list** (and the **Folders** tab) returns to the music-root list. Navigation alone never changes Queue or starts playback.

A folder's actions include matching playable tracks from the current folder **and every subfolder**, in path order — descendants are included even when an intermediate folder has no direct tracks or the list spans several pages. Sibling folders and other music roots are never included; browsing itself still shows one level at a time.

| Action | Effect |
|---|---|
| **Play all** | Stops current playback, replaces Queue with the selection, and starts playing. |
| **Play next** | Inserts after the current track, or at the front of Queue when nothing is current. |
| **Add to queue end** | Appends after the existing entries. Neither insertion interrupts or starts playback. |
| **Add to saved playlist** | Appends to an existing saved list or creates a new one, leaving Queue unchanged. |

Navigation and list actions share one toolbar; on narrow screens scroll it horizontally. **Add to queue end** uses a queue/down-arrow icon and changes the playback queue, while **Add to saved playlist** uses a bookmark-plus icon and opens the playlist chooser.

<a id="likes"></a>
### Likes and one-time shuffled queues

- Use a track's heart button in Library, Playlists, Queue, or Track information to set or clear its like. All four views show a filled heart when liked and an outlined heart when not, and update together — including duplicate queue entries and an open information dialog. Likes never change queue order or playback.
- Likes are shared Server state, not private per-account lists, and survive rescans and Server restarts.
- Select **Liked** in Library to browse liked tracks; search and paging apply there too.
- In **Queue**, **Shuffle liked into queue** appends all currently available liked tracks in random order — not just the current page or search results. Existing order and duplicates stay intact, current playback continues, and stopped playback does not start. Unavailable tracks are excluded. No available likes, more than 10,000 selected tracks, or an append that would exceed the 10,000-entry queue limit produces an error instead of a partial append.
- This is a one-time selection for the shared queue, not a saved playlist or a live filter: later like changes do not rewrite the queued tracks, and no playlist is created or named. To keep the result, use Queue's **Save as playlist**. Existing playlists, including older liked-shuffle snapshots, are untouched.

<a id="playback-modes"></a>
### Playback order and repeat

- The player bar has independent **Sequential / Shuffle** and **Repeat off / Repeat all / Repeat one** controls. Desktop layouts show them beside the transport buttons; a phone's compact bar has a **Playback modes** chooser, and the expanded player shows both controls directly.
- **Sequential** follows the displayed queue. **Shuffle** uses a separate randomized traversal without rearranging the displayed queue or collapsing duplicate entries. Each entry occurs once per traversal; enabling shuffle or replacing the queue starts a new one. Explicit **Play next** additions stay next even when more tracks are appended afterwards, while ordinary appended batches join the end of the traversal in shuffled order.
- **Repeat off** stops at the end. **Repeat all** starts another traversal, choosing a fresh order in shuffle. **Repeat one** applies only to natural completion — **Next** still advances. Media failures are not repeated indefinitely.
- In shuffle, **Previous** follows the traversal's visited order instead of picking another random track. Playing a seekable track for more than five seconds still makes **Previous** restart it.
- Changing modes never starts stopped playback, restarts the current track, resets its position, or edits saved playlists. Server modes apply to the shared queue and all outputs, synchronize across Controls, and survive a restart without autoplay. Android Saved music keeps its own independent local modes.

<a id="most-played"></a>
### Play counts and Most Played

- **Library → Most Played** lists available tracks with at least one counted play, highest count first; each row shows its count, and search and pagination apply to the globally ranked results. A track's information dialog shows the same **Play count**, including zero.
- Counts are shared across the Server, not private per-account statistics. A playback session qualifies after 30 seconds of confirmed listening, or half the duration for a track shorter than 60 seconds; when the duration is unknown, the threshold is 30 seconds.
- The Server uses correlated renderer playback state and position progress. That is not a claim that a human heard the physical output.
- Pause and resume keep the accumulated listening time inside one session and never count it twice. Pause time, seek-skipped time, stalled progress, and gaps without reliable observations do not qualify. Replaying a track can add another count after meeting the threshold again.
- Counting started when this feature was installed; earlier listening is not reconstructed. Completed counts survive rescans and restarts, while partial listening below the threshold is not restored after a restart. Standalone Android **Saved music** playback never contributes.
- Browsing statistics changes nothing: counts live in the Server database, not in file tags, and no source audio is modified.

### Artwork and Queue row actions

- Player album artwork opens **Queue**; it does not show track information or start playback.
- Library `(i)` and Queue artwork open track information without starting playback. Queue artwork shows a large `(i)` on hover or keyboard focus, and continuously on touch screens.
- The triangular Play button is the first action on the right of each Queue row, and only that button starts the entry.

<a id="outputs"></a>
## 4. Outputs

Select an output under **Output device** while playback is stopped. Use **Find output devices again** after powering on or attaching hardware.

| Output | Available on | Notes |
|---|---|---|
| UPnP/DLNA | Linux and native Windows Server | Discovered automatically over SSDP (UDP 1900). |
| Google Cast | Linux and native Windows Server | Off by default; enable it in Settings and restart. Needs mDNS (UDP 5353) on the selected adapters. See [Google Cast](#cast). |
| AirPlay | Linux container Server only | Needs the packaged sender and FFmpeg; pair while stopped. Native Windows Server cannot enable AirPlay. |
| **This device** `[Local audio]` | Browsers, PWA, desktop app, native Android | An output of the Server queue on the machine you are using. See [This device](#browser-output). |

Capabilities vary by receiver: confirm audible playback and the controls you need on your own equipment.

<a id="browser-output"></a>
### This device: local audio

This section covers browsers, the PWA, Linux Desktop, and Windows Desktop on its default Browser backend. For native engines see [Windows native audio](#windows-audio) or [native Android](#android-controls); the native iOS client has no local playback. Every Server-mode local output appears as **Local audio** and plays the Server queue.

1. Stop playback, then choose the output marked **(This device)** under **Output device**, for example `Windows · Chrome (This device) [Local audio]`. On a phone, expand the compact player to reach the selector.
2. Choose a track or keep the existing queue and press **Play**. If the browser blocks audio, select **Allow playback** on that page. When the pending request already failed, dismiss the error and press Play again — commands are never silently replayed.
3. Use Pause, Stop, Previous, Next, and supported Seek normally. The browser is an output of the Server queue, not a separate local queue.

**Automatic selection on page entry.** If the previously selected output is offline while playback is stopped or unavailable, **This device** is selected once — queue, current track, and saved position stay intact, and you still press **Play** yourself. A concurrent output/state change, or recovery of the old output, cancels the switch. Android never takes ownership from Saved music or another Server, and a later disconnection on the same open page does not trigger repeated registration.

**Local volume.** When the selected **This device** belongs to this page or app, **Local volume** adjusts it from 0–100% (expand the player on phones). It changes browser audio, Windows native PCM gain, or Android's Server-mode Media3 volume only — never system volume, network renderers, or other devices. Native control requires the corresponding desktop or APK build.

**Naming a local output.** In a browser the default name describes the OS/browser information exposed to the page, not the computer's hostname or a phone's user-assigned name. The native apps use fixed defaults: `Windows · jastreamer` and `Android · jastreamer`. Use the pencil button **Name this local output** beside the output selector to save an alias such as `Office PC`; **Use default name**, or saving an empty alias, restores the automatic name. Names are limited to 80 UTF-8 bytes, and Korean characters use several bytes each.

The alias is stored in this browser profile for this Server UUID and exact origin, including port. It does not follow you to another browser or profile, a private-browsing session, or a replacement Server, and clearing browser storage removes it. Storage failures are reported rather than claimed as saved.

Only the page that registered and owns the output adds **(This device)**, for example `Office PC (This device)`. Other pages and devices see `Office PC` without the marker, even under the same account. A page that has not registered yet still offers its own local output with the marker — that is never a label on somebody else's renderer. Naming an unregistered browser neither registers nor selects it. Renaming a registered output updates the name for other clients without changing its ID, queue, playback, or output selection. The alias is a display name, not verified hardware identity.

**What the Browser backend is.** Control uses same-origin authenticated HTTP JSON, and audio uses the browser element's HTTP(S) GET/Range. This is not UPnP, HLS, DASH, or WebRTC. The browser and operating system choose the speaker or headphones: there is no hardware picker, WASAPI/ASIO engine, exclusive mode, or bit-perfect guarantee, and supported formats depend on the browser's decoder. Conversion requires enabled transcoding and a configured FFmpeg, and converted WAV streams cannot seek.

**Keep the owning page open.** Closing or reloading it releases that output; losing the live registration makes the output unavailable without discarding the queue. On re-entry the conditions above may reselect it; otherwise Stop if needed, reselect **This device**, and press Play. Closing another Control does not stop the owning browser, and network outputs are independent of Control lifetime. Windows Desktop's **X** hides the window instead of closing it (see [Desktop app](#desktop)). Background and lock-screen playback in ordinary browsers and the PWA depends on the browser and OS and is not guaranteed; desktop playback through sleep or logout is not promised either.

<a id="windows-audio"></a>
### Windows native audio

Opt-in WASAPI output for compatible Windows Desktop packages with a compatible Server-hosted Control. It is not a feature of older published packages, and it adds no offline library and no second queue.

1. Stop playback and wait for pending operations. Select **This device** as the output. **Windows audio settings** appears beside the output controls only while this device is the selected output. Change **Local audio backend** from **Browser (default)** to **Windows native (opt in)**.
2. Choose a named **Windows audio endpoint** to always use that device regardless of the Windows default, or **Follow Windows default device** — which also shows the current default's name — to follow whatever Windows defaults to. Refresh outputs after attaching new hardware. A missing fixed endpoint is an error, never permission to substitute another device.
3. Set **Exclusive** to **Off** for WASAPI Shared using the endpoint's mix format, or **On** for exact-format Exclusive. Busy devices, unsupported formats, and Windows exclusive-policy denial stay errors: the app never silently falls back to Shared.
4. Endpoint and mode changes keep the same **This device** output and apply from the next track. Switching between **Browser (default)** and **Windows native (opt in)** replaces the local output, and this device is reselected automatically when it was the selected output. Playback stays stopped until you press Play. Paused or loaded media still owns the endpoint and must be stopped before reconfiguration.

While **Browser (default)** is selected, the endpoint and **Exclusive** selectors are disabled: browser audio always plays through the Windows default device in Shared mode.

In Exclusive mode app-local volume is fixed at 100% and the **Local volume** control is not offered — adjust volume on the DAC or amplifier. Preferences persist beside the EXE in `user-data`.

If the Server restarts, the Windows playback connection ends: the app reports that the connection ended — your sign-in is still valid — and does not repeat that notice when you reopen the Server. Select **This device** again.

**Reading the panel.** It separates the **Requested path** from the **Actual active path**: endpoint, mode, sample rate, channels, container width, and valid-bit precision. **Local volume** changes only this app's PCM gain.

**Application path eligible for bit-transparent delivery** appears only when all of the following hold: a lossless source the Server explicitly reports as untransformed, unchanged rate/layout/precision, unity gain (app volume 100%), and actual Exclusive mode. Unknown source provenance, lossy decoding, Shared mode, or altered samples cannot qualify. This indication covers the application path only — it does **not** verify driver, DSP, or DAC behaviour, or physical bit-perfect output.

The bundled FFmpeg decoder supports FLAC, MP3, AAC/M4A, Vorbis, Opus, and WAV, but endpoint capabilities can still reject a decoded format, especially in Exclusive mode. The authenticated Desktop session fetches bounded same-origin media bytes; the audio helper receives neither credentials nor media URLs, and original files are never rewritten.

Windows system media controls show the current track, available artwork, and the actual timeline. Play, Pause, Stop, Previous, Next, and supported Seek go to the Server; they never keep a second queue, bypass Server playback commands, or control an unrelated selected network output. They withdraw when local playback ends or stops, or when ownership is lost.

Reloading the WebView on the same Server keeps native playback while the Desktop owner and registration remain valid. **X** still hides the window; tray **Exit** releases the local endpoint and registration without stopping network outputs. Terminal helper or registration loss never re-registers or autoplays: reconnect **This device** and press Play, or switch back to **Browser** while stopped. Windows compilation, real endpoint Shared/Exclusive behaviour, media-key integration, and audible output require verification on Windows hardware; decoder hashes and UI state are not physical-audio evidence.

<a id="phone-controls"></a>
### Phone controls

The focused phone layout is chosen automatically for iPhone/iPod browsers and Android browsers whose user agent reports both Android and Mobile. An iPad, an Android tablet, or a merely narrow desktop window keeps the standard layout.

- The phone header keeps the account name and sign-out; the four bottom tabs are **Library**, **Playlists**, **Queue**, and **Settings**. Ordinary browsers show the jastreamer logo; inside the Android, Desktop, and iOS apps only the duplicate Web header/sidebar logo is hidden, and account controls remain available.
- The compact player keeps Play/Pause, Stop, and a **Playback modes** chooser immediately available. Its expand arrow adds Seek, Previous, Next, shuffle/repeat, output selection and refresh, and AirPlay pairing when required; album artwork opens **Queue**.
- Primary playback, navigation, and track-action buttons have at least 44-by-44-pixel targets, and the header, player, and bottom navigation respect device safe areas.
- Selecting another bottom tab collapses the expanded player without stopping playback, and Escape collapses it when keyboard focus is inside the player. The expanded panel scrolls on short or landscape screens, and the page underneath is not interactive until it closes.

The optional installed Web app (PWA) opens the same Control from a home-screen shortcut and controls the same Server queue. It always needs network access to the Server and provides no service worker, cache, or offline playback. Settings has no in-app install card: use your browser's own "install" or "add to home screen" menu. See [PWA installation](INSTALL.md#pwa) for the trusted-HTTPS requirement and per-browser steps. The optional desktop app needs no PWA installation.

<a id="desktop"></a>
### Desktop app

The desktop app opens **Server playback**, with separate **Discovered now** and **Recent connections** card groups. Cards show the Server name, address, and checked availability — a saved entry is not proof that a Server is reachable now. Use a card's connection action, or **Connect by address** for a manual HTTP(S) root address; Cancel or Escape closes that dialog without connecting and keeps the draft. Discovery, navigation, and connecting never start playback.

The desktop app has no independent saved-music player and no Local playback card. After connecting, its shared Web **This device** output uses the Server library and queue.

On Windows, **X** hides the window in the notification tray while keeping the connection and local playback alive. Click or double-click the tray icon, choose **Open JASTREAMER** from its context menu, or launch the app again to restore the same window. To quit completely, right-click the tray icon and choose **Exit**; this releases the app's local output without sending Stop to network outputs. Quit this way before updating. If a tray icon cannot be created, **X** keeps its normal quit behaviour, and Linux window-closing behaviour is unchanged.

<a id="android-controls"></a>
### Native Android controls

The native Kotlin app adds Server selection, a Server-controlled phone output, and an independent **Saved music** player around the shared Control. [Compatible Server/Control versions, installation, and APK update rules](INSTALL.md#android) are separate from PWA installation.

**Getting connected**

- The native home offers **Server playback** and **Local playback**. **Local playback** opens **Saved music** without connecting or signing in, and its secondary **Downloads** action opens the transfer manager. **Server playback** shows separate **Discovered now** and **Recent connections** groups; for a manual URL choose **Connect by address**, enter the HTTP(S) root address, then **Verify and connect**. A fresh launch never connects automatically, and Server identity checks always apply.
- In Server mode the native header shows the selected Server and its complete origin. **Servers** returns to selection without stopping Server-controlled network outputs or saved-music playback. A changed Server UUID is rejected for a saved entry instead of silently reusing its session.
- Android applies system-bar and keyboard insets once in the native shell. Back dismisses the manual dialog or keyboard first, then follows the active Web or saved-music hierarchy: Web history returns to Server selection, Server selection and the root saved-music screen return to the home cards, and Back from home leaves the app. Rotation keeps the live screen and unsaved Web form state. Returning to the foreground rechecks a Server before exposing its page, but a failed check never blocks **Saved music**.
- Cookies and Web storage are isolated by Server UUID plus complete origin, including port. **Remove from recent servers** removes only the shortcut — it neither signs out nor deletes that isolated profile or saved music. Sign out inside Control to end a session; signing out stops that Server's incomplete imports while already committed music stays device-owned.
- Native language controls and **Settings → General → Language / 언어** support English and Korean. A Web language change reaches the native shell when the page finishes loading, or when you leave or pause it.

**Playback ownership**

- One Media3 service owns this phone at a time: none, Server, or Saved music. Starting saved music while the Server owns playback, or assigning the phone to a Server while local music owns it, requires an explicit handoff confirmation. The previous owner is stopped first; queues are never copied, and late commands from the old owner cannot cross the handoff.
- In Server mode, explicitly select `Android · jastreamer (This device) [Local audio]`, or its saved alias, then press **Play**. The Server owns that queue and its commands, and Android's system media controls report Play, Pause, Stop, Previous, Next, and supported Seek back to it. In Saved music mode the same controls act on local files and the device queue.
- The native shell sends no Play or Stop merely because its screen closes, backgrounds, changes Servers, or reopens, and playback survives Activity/WebView recreation while its service stays alive. Process termination or terminal registration loss stops Server-owned playback. The local library, queue, and position are restored after a restart without autoplay. Only a newly opened Server page may attempt the guarded offline-output selection described above; it can neither take Saved music ownership nor supersede an in-flight user request.
- Supported formats depend on Media3/Android decoders. Original imports promise no decoder compatibility or bit-perfect output; Server playback adds the opt-in USB path described below. Transient Server-mode media failures use bounded recovery; terminal failures are shown rather than retried indefinitely.
- Only the verified Server's current top-level document receives the restricted native interfaces for Server output and explicit imports. They expose no cookies, credentials, arbitrary native calls, filesystem browsing, or arbitrary URL downloads, and external navigation, new windows, file pickers, and unrelated Web downloads or native permission requests stay blocked. Ordinary browsers and Linux Desktop keep browser audio; compatible Windows Desktop has its separate opt-in audio bridge; native iOS gains neither the Android interfaces nor local playback.

**USB bit-perfect audio (Android 14+)**

Opt-in output that hands this app's PCM to a connected USB DAC without Android mixing, resampling, or volume changes. It is off by default, applies to Server playback on this phone, and leaves **Saved music** on the standard Android output.

1. Stop playback and wait for pending operations. Select the phone output (`Android · jastreamer (This device) [Local audio]`). **Phone audio settings** appears beside the output controls only while this device is the selected output.
2. Set **USB bit-perfect** to **On**. The setting belongs to the app on this phone rather than to one Server, and it applies from the next track: the same phone output registration is kept and playback stays stopped until you press **Play**. Paused or loaded audio must be stopped before changing it.
3. While it is on, app volume is fixed at 100% and the **Local volume** control is not offered — change the level on the DAC or amplifier.

The panel reports what is missing: Android 14 or newer, a connected USB audio device, and a device that offers a bit-perfect mixer. When any of these is missing, the option stays visible with the reason and normal playback is unaffected.

**Reading the panel.** It separates the **Requested path** from the **Actual active path**: USB device, mode, sample rate, channels, container width, valid-bit precision, and PCM representation. The actual values come from the audio track Android really opened and from the mixer attributes the framework reports, not from the request.

**No silent fallback.** If the USB device will not take the track's format as a bit-perfect stream, or the device disappears, that track fails with an error instead of quietly playing through the Android mixer. Stop playback and turn **USB bit-perfect** off to play such tracks normally.

**Application path eligible for bit-transparent delivery** appears only when all of the following hold: a lossless source the Server explicitly reports as untransformed, unchanged rate/layout/precision, unity gain (app volume 100%), and a mixer that is actually bit-perfect. This indication covers the application path only — it does **not** verify driver, DSP, or DAC behaviour, or physical bit-perfect output.

**Precision limit.** Media3 writes 16-bit PCM for the formats this client plays, so a 24-bit source is reduced to 16 bits before output. Such a track can still use the bit-perfect mixer, and the panel then reports 16-bit with the reason that the source precision was reduced — never a bit-transparent claim. 16-bit lossless sources, including CD-rate FLAC and WAV, can qualify.

The USB preference is released when the setting is turned off, when the USB device is detached, when Server playback ownership ends (disconnect, handoff to **Saved music**, or terminal registration loss), and when the playback service stops.

**USB direct test (experimental, debug builds only)**

Debug APKs add a **USB direct test** section at the bottom of **Phone audio settings**. It is a developer experiment, not a playback feature: it opens the USB DAC with `libusb`, takes it away from Android for as long as it runs, and never starts by itself. Release APKs do not contain the section, and plugging a DAC in never launches the app.

- **Find USB DAC** asks for USB access once and then lists what the dongle's own descriptors say: UAC version, bus speed, and one line per streaming format with its bit depth, subslot size, channel count, synchronisation type, feedback endpoint and sample rates. Report those lines when a format does not work.
- **Play test track via USB direct** decodes the Server track this phone loaded most recently, with Android's own extractor and decoder, and sends the decoded PCM straight to the DAC. It is refused while Server playback still holds a track, so stop playback first. 24-bit output is requested where Android supports it; the panel reports the encoding the decoder actually produced rather than assuming.
- The live status shows requested versus actual rate, bits and channels, the subslot size and synchronisation type in use, packets sent, underruns and the sample rate the DAC's feedback endpoint is asking for. Confirm the rate on the DAC's own display and by listening.
- **Stop**, a Server track starting, a detached device, an error, and the playback service stopping all release the interface and hand the DAC back to Android.

**Saved music**

- **Saved music** opens the app's own **Library / Playlists / Queue / Settings** without Server HTML, network access, or login. Library defaults to **Albums**, with **Artists / Genres / Folders / Tracks / Liked**, search, and collection drill-down. Genres use saved metadata, and likes belong to this device rather than the Server. A completed import keeps its metadata and artwork and stays available after logout, session expiry, account loss, Server/profile removal, or with an older or unreachable Server.
- Track and collection actions follow the familiar **Play / Play next / Add to end / Add to playlist / Track information** flow, and selection follows the current category and search results.
- Create, rename, reorder, and delete local playlists without touching Server playlists. Removing a playlist entry or deleting a local playlist leaves the audio file in place. The local queue preserves order and repeated occurrences: play a particular occurrence, move or remove entries, or save the queue as a local playlist. Adding entries starts no playback and does not reset the position. Seek, previous/next, shuffle, and repeat are independent of the Server queue.
- The local mini-player exposes **Sequential / Shuffle** and repeat directly, even while stopped or empty; the expanded player and queue show the same local settings. These controls never start playback, reorder the displayed queue, or change Server playback modes.
- Saved music reuses Control's navigation, dark/green styling, and labelled icon controls. The compact player stays visible while expanded controls open above it for seeking, previous/next, shuffle, repeat, queue, and track information; there is no second large artwork or duplicate playback bar. Wide layouts place metadata and controls side by side. Expanding or collapsing keeps the browsing screen, search, scroll position, queue, and playback ownership, and on phones the browsing content cannot receive input while the controls are expanded.
- **Folders** are real directories inside app-private persistent storage. Create, rename, and move folders, move selected tracks, choose a download destination, and resolve same-name collisions without overwriting. Local stable IDs, playlist entries, queue entries, and playback position survive a move, and interrupted storage operations are journaled and reconciled before another library change. These controls never browse or modify NAS folders or other apps' files.
- **Delete from device** permanently removes the selected local audio after confirmation and updates local playlists and queue references. Deleting a local playlist alone deletes no audio. If the current track is selected, choose **Delete when playback ends** or explicitly **Stop now and delete**; deferred deletion keeps the file until playback releases it.

**Downloads**

- A compatible Control and Android app show **Download** controls for tracks, albums, playlists, and folders. Confirm the destination and either **Original** (the verified source bytes) or **Space saving** (AAC-LC 256 kbps in M4A) when the Server advertises conversion. Downloading never starts playback or changes the shared Server queue. A playlist import is a one-time snapshot that preserves order and duplicates; it does not follow later Server edits.
- In Control's **Folders** view you can download a library root, a child folder, or the currently open folder. This snapshots up to 10,000 catalogued tracks recursively into the chosen local folder; it does not mirror the Server's directory hierarchy or create a playlist, and later Server changes need an explicit reimport.
- Accepted requests open progress in the Server screen immediately. Its **Downloads** entry shows this document's jobs, received bytes, track counts, waiting reasons, and completion or failure. An active Download control reopens status, while **Download again** starts a new confirmation. Open the native download manager for all jobs, including those restored after relaunch.
- The default policy waits for an unmetered, non-mobile connection. A waiting job can open **Download network settings** directly from the Server screen. Metered or mobile data requires explicit native confirmation or **Allow metered or mobile data** in the manager; cancelling grants nothing. Roaming and unavailable connections stay blocked, and a reachable local Server needs no Internet validation.
- The native **Downloads** manager covers preparation, transfer, verification, local import, partial failure, pause/resume, cancellation, and destination changes. Pending work stays bound to its originating Server and signed-in principal. A Server without the v1 download capability cannot start a new import but can still be controlled normally, and it cannot affect music already saved.
- **Remove download record** removes one completed, partially saved, cancelled, or failed record after confirmation; **Clear finished records** removes all of those while keeping active and paused downloads. Both leave saved music, artwork, folders, playlists, and the queue intact — cancel active work separately before removing its record. A compatible Control status panel also drops removed native records when refreshed.

The PWA installation guidance in Control is for browsers; the native Android client needs no PWA installation. Use only a Server you trust, especially over unencrypted HTTP.

<a id="ios-controls"></a>
### Native iOS controls

The SwiftUI app wraps the same Server-hosted Control. Its current availability is [source and CI only](INSTALL.md#ios), not an installable phone release.

- The Server-only chooser separates **Discovered now** from **Recent connections**. Nearby cards have passed discovery verification; recent cards are verified when connecting, never labelled online merely because they were saved. **Connect by address** opens a native sheet for the HTTP(S) root address, then **Verify and connect**. Cancel keeps the draft and, if verification is running, cancels that attempt first. A fresh launch stays on selection instead of connecting automatically.
- The header shows the selected Server and its complete origin, including port. **Change Server** returns to selection without stopping network-output playback. Returning to the foreground rechecks the Server UUID before exposing the page; an identity or network failure keeps the old page and its keyboard inaccessible.
- Cookies and local Web storage use named profiles keyed by verified Server UUID and canonical scheme/host/port, so different ports are separate sessions. Removing a recent entry only changes the list; sign out inside Control to end its session.
- **Back** navigates available Web history and **Reload** refreshes the page. Scrolling dismisses the keyboard interactively. Rotation keeps the live page and unsaved input. In compact-height keyboard layouts the back/reload bar hides while Server switching and language stay available.
- Native language controls and **Settings → General → Language / 언어** support English and Korean. The Web language is read back at page load, foreground return, and native screen transitions, without a JavaScript bridge. Changing the native language reloads the Web page, so finish unsaved edits first.
- The native shell sends no Play or Stop when closing, backgrounding, or switching Servers. It blocks Web media loads and has no local playback entry, native audio engine, offline player, or cached command queue. External navigation, new windows, downloads, file pickers, and native media/device permission requests stay blocked — use a normal browser for local audio or file uploads.

If discovery fails, check Local Network access, Wi-Fi/multicast, and VPN routing, or enter the complete address manually. Use only a Server you trust, especially over unencrypted HTTP.

<a id="cast"></a>
### Google Cast media and completion boundaries

Direct Cast streaming is chosen conservatively from inspected codec, sample-rate, channel, and — where applicable — bit-depth metadata. Every direct source needs a verified matching codec, a positive sample rate, and mono or stereo channels:

| Source | Accepted for direct streaming |
|---|---|
| FLAC | up to 96 kHz, 1–24-bit |
| MP3, Ogg/Vorbis, Ogg/Opus, M4A/AAC | up to 48 kHz |
| LPCM WAV | up to 48 kHz, 1–16-bit |

Missing or mismatched metadata and sources outside those limits are never assumed compatible. With media transcoding enabled and FFmpeg configured they use a nonseekable 44.1 kHz stereo 16-bit WAV stream instead; the source file is unchanged. Direct streaming avoids that conversion but promises no bit-perfect receiver output.

Cast control keeps a persistent TLS connection and owns the application and media session it launches. Queue advance requires an explicit `FINISHED` status for that owned media: EOF, an empty status, `BUFFERING`, or `ERROR` is never treated as completion. Pause, seek, and accepted media still depend on the receiver.

<a id="settings"></a>
## 5. Settings reference

| Tab | Controls |
|---|---|
| **General** | Language, Server name, data directory, and account password |
| **Network** | HTTP/HTTPS listeners, access rules, network adapters, discovery/polling intervals, and the Server audio URL |
| **Library** | Music folders, scanning, and background audio verification |
| **Playback & outputs** | Google Cast, AirPlay and its setup help, FFmpeg, and audio conversion |
| **Diagnostics** | Playback/file-check history and CSV report downloads |

Switching tabs keeps unsaved edits and saves nothing; it never restarts, scans, or controls playback. **Save settings** and **Discard changes** apply to all Server-setting tabs together, while language and account actions are separate. An invalid field in a hidden tab is revealed and focused before saving. Restart and configuration-conflict notices stay visible across tabs. On narrow screens scroll the tab strip horizontally.

### Server paths and network selection

- **Browse** beside music folders, the data directory, HTTPS certificate/key files, FFmpeg, and the AirPlay sender path opens an authenticated browser for the Server's filesystem, not this browser's computer. Windows lists accessible drives and accepts an absolute UNC share path; Linux/NAS starts at `/`; containers expose only their mounted filesystem. Listings omit symbolic links and Windows reparse points, and manual path entry stays available.
- Navigate with roots, **Parent folder**, or an absolute directory path. **Choose** changes only the draft field and **Cancel** leaves it unchanged. Save settings explicitly, then scan. Choosing a data directory does not move the existing database or artwork: migrate that data separately before changing storage and restarting.
- **Server network adapters** lists the Server's actual adapter names and IP/prefixes. **Automatic** clears `network.interfaces`; explicit selections keep manually entered names. Unavailable adapters and addresses stay visible but cannot be newly selected for UPnP or Google Cast discovery. Cast mDNS uses UDP 5353 on the selected interfaces.
- **Server URL used by playback devices to fetch audio** is `media.base_url`: it tells a renderer where to fetch media from the Server. The same address may also serve Control, but this setting changes no listener binding, port, or browser URL. Normally leave it blank so the Server selects automatically. **Choose a Server network address** combines a detected Server IP with a currently enabled HTTP(S) listener and only fills the draft — save Settings to apply it. That list is not a reachability test, so reject unsuitable VPN or container addresses. For a manual value use a Server address and enabled listener port reachable from the receiver's LAN; `localhost` and the client PC's address are wrong for a remote receiver. An explicit value or listener address keeps precedence, and Cast also needs Server access to the receiver's mDNS-advertised Cast port.
- Browsing and adapter/IP selection never save, restart, scan, or start playback on their own. Apply the draft explicitly; listener, storage, and network changes may require a restart.

### AirPlay setup help

**AirPlay setup help**, beside **Jastreamer AirPlay sender path**, explains the required sender. It installs no software, enables no AirPlay, validates no arbitrary path, and controls no receiver. The supported Linux `amd64`/`arm64` Server container includes `/usr/local/bin/jastreamer-airplay`, Python 3.12 with pinned pyatv 0.18.0, and FFmpeg as one matching runtime. Native Windows Server does not support AirPlay, even when a Linux or arbitrary helper path is entered.

A separately installed sender must use the adapter source and dependency file from the same release as the installed Server, not an arbitrary `main` revision, and must implement jastreamer's matching adapter/helper protocol with compatible dependencies. `atvremote`, a Python executable, pyatv alone, and receiver software such as Shairport Sync are not interchangeable sender paths. The dialog prints full reference URLs for the [adapter source](https://github.com/furyheimdall/jastreamer/blob/main/apps/server/internal/airplay/helper.py), the [pinned AirPlay requirements](https://github.com/furyheimdall/jastreamer/blob/main/packaging/server/requirements-airplay.txt), and the [pyatv 0.18.0 source](https://github.com/postlund/pyatv/tree/v0.18.0); those `main` links do not guarantee compatibility with an installed release. A normal browser can follow them, while the desktop app may block external windows — copy the displayed URL into a browser if it does not open. Save and restart the Server after changing a supported Linux sender path.

### Applying saved settings and restarting the Server

When saved settings need a restart, **Settings** shows a notice and a **Restart server** button, and the notice persists when you reopen the screen. Save or discard unsaved edits first. Confirming stops playback and restarts the Server with the saved settings; accounts, queue, and playlists are retained, and playback does not resume automatically.

For an unchanged address, Control confirms reconnection to a new Server runtime before reporting completion; if the address changes, open the new address shown. Failures and timeouts are never reported as completion, and the restart command is never retried automatically. A data-directory change cannot be applied with this button: migrate the existing data separately, then restart the Server manually. Older Servers without the restart API show manual-restart guidance.

<a id="diagnostics"></a>
## 6. Diagnostics and logs

**Settings → Diagnostics → Playback and file-check history** shows the Server's shared, persistent history. Filter by **Result type** (**Renderer reports** or **File checks**) or by renderer. Renderer reports include the renderer name and ID, track, error code, playback position when supplied, and expandable structured details; older imported records may identify a renderer only by ID. The newest 5,000 records are retained, and viewing them never changes playback.

In a PC or mobile web browser, choose **Download CSV** to save every retained record matching the current filters — not just the visible page. It contains UTC timestamps, outcomes, track/folder/relative-path information, error codes and messages, and structured details; renderer reports also keep their renderer and playback identifiers. UTF-8 with a BOM preserves Korean text in spreadsheets, and formula-like cells are quoted as text so they cannot execute. The export covers the retained failure and inconclusive history, not a pass certificate for every file, and it neither deletes history nor changes source files or playback. Android, iOS, and desktop embedded clients keep their download restrictions and point you to a normal browser; sign in there separately if needed.

**Where the logs are.** Server diagnostics use UTC and go to both the console and `<data_dir>/logs/server.log` — `/var/lib/jastreamer/logs/server.log` in the standard Linux container, inside the existing data mount so it survives container replacement. Rotation keeps `server.log` plus `server.log.1`–`server.log.3`, each at most 5 MiB. Normal local-output activity is summarized at most once every 30 seconds per registration, and repeated request failures are rate-limited. Events correlate generated output/play/command IDs and record no credentials, cookies, media URLs, filenames, or output display names. On POSIX systems the log directory is created with mode 0700 and files with mode 0600; on Windows, restrict the data directory to the Server account. If persistent logging cannot start, check the Server console or Compose logs. Never expose the data directory over HTTP.

**Android playback-error reports.** These require matching Server and APK versions: update the compatible Server first using the [update procedures](INSTALL.md#upgrade). Failed native commands and terminal playback-error reports include the Media3 error code/name, occurrence time, playback position, cause classes, and bounded code-only stack frames, plus HTTP status and codec/audio-output numeric error codes when available. Exception messages, media URLs, file paths, and credentials are excluded. The Server retains accepted reports in its rotating log automatically, with no ADB step. This is not an offline upload queue: a process exit or lost Server connectivity before delivery can still prevent collection, and previously lost exceptions are not reconstructed. Saved-music errors stay in on-device local diagnostics and are never uploaded automatically.

**When reporting a problem**, include the Server version, the exact image digest or Windows ZIP SHA-256, the Server platform/architecture, relevant logs, and the receiver model. Remove passwords, cookies, certificates, and private keys, and preserve raw diagnostic wording.

<a id="troubleshooting"></a>
## 7. Troubleshooting

### Reaching the Server and Control

| Symptom | Likely cause | What to do |
|---|---|---|
| The Web page does not open | The Server is not running or the port is blocked | Check the Windows Server console and TCP 18080, or Linux `/healthz`, Compose logs, the configured listener, and firewall access to TCP 8080/8443 |
| The desktop app cannot discover a Server | mDNS is blocked | Allow mDNS UDP 5353, or use **Connect by address** with the complete Server URL |
| The phone layout does not appear | The device is not detected as a phone | Confirm an iPhone/iPod browser or an Android browser reporting both Android and Mobile; iPads, Android tablets, and narrow desktop windows intentionally keep the standard layout |
| No install option for the phone Web app | The origin is not a trusted HTTPS origin, or the browser has no install support | Use the browser's own install menu and follow [PWA installation](INSTALL.md#pwa); a private-LAN HTTP origin cannot be installed |
| Android reports an unsupported WebView | The system WebView/Chrome provider is too old | Update Android System WebView or Chrome and reopen the app to restore isolated Server profiles, Server output, and new imports. Shared cookies are never used as a fallback, and already saved music stays available |
| Android cannot discover or reconnect to a Server | Network isolation or an identity/TLS error | Check Wi-Fi, mDNS UDP 5353, VPN/client isolation, and app network access; enter the complete URL manually. Keep identity/TLS errors rather than bypassing them — they never block already saved music |
| Settings cannot be saved | The configuration file is not writable | Confirm that the Windows files beside the EXE, or the Linux config directory and `server.json`, are writable by the Server account/UID 10001 |
| The password is lost | — | Stop the Server, then run `jastreamer-server --reset-password USER --config /etc/jastreamer/server.json` in a maintenance container with the same config/data mounts. Type the new password only at the prompt |

### Library

| Symptom | Likely cause | What to do |
|---|---|---|
| The library is empty | Wrong root, unreadable files, or no scan yet | Confirm the Windows folder or Linux `/music` mount is the configured library root, that the Server account/UID 10001 can read it, and that a scan completed. Bundled `jastreamer-samples` also need an explicit scan |
| A replaced file still shows old metadata | Size and modification time did not change, so the incremental scan reused stored results | Run **Full rescan** |
| A file check failed or could not be verified | Missing FFmpeg, unsupported format, timeout, or a changed/unreadable file | Open **Settings → Diagnostics** for the folder name and relative path; fix the file or the FFmpeg configuration. jastreamer never repairs or deletes source files |

### Outputs and playback

| Symptom | Likely cause | What to do |
|---|---|---|
| No outputs appear | Discovery traffic is blocked | Allow SSDP UDP 1900 for UPnP; Google Cast needs mDNS UDP 5353 on the selected interfaces plus Server TCP access to the receiver's advertised Cast port; Linux AirPlay needs mDNS UDP 5353 and host networking. Disable Wi-Fi client isolation |
| The output is selected but cannot play | The receiver cannot fetch media from the Server, or the format is unsupported | Permit Server-to-receiver control/stream traffic and receiver-to-Server media HTTP(S) traffic. Normally leave **Server URL used by playback devices to fetch audio** blank, or set only a receiver-reachable Server origin. For unsupported Cast originals, enable conversion with a configured FFmpeg |
| **This device** is silent or disconnected | Browser autoplay block, muted output, or a lost registration | Select **Allow playback** if shown, check browser/OS mute and audio routing, and keep the owning page open. After a reload or lease loss, Stop if needed, reselect **This device**, and press Play. Preserve decoder and transport errors |
| Windows native audio reports that the connection ended | The Server restarted or released the registration | Your sign-in is still valid: select **This device** again. See [Windows native audio](#windows-audio) |
| Exclusive mode fails or the **Local volume** slider is gone | The endpoint is busy, the format is unsupported, Windows policy denies exclusive access — or Exclusive mode is active | A denial stays an error and never falls back to Shared. In Exclusive mode volume is fixed at 100%; adjust it on the DAC |
| Cast FLAC fails after seeking near the end of a track | A receiver-dependent failure, independently reproduced | Preserve the reported `BUFFERING`/`ERROR`. It is not completion, so do not skip the queue entry or weaken the owned `FINISHED` requirement |
| AirPlay authorization fails | Wrong sender path, or playback is running | Supported Linux Server only: stop playback, repeat the displayed PIN/password flow, and keep the packaged `/usr/local/bin/jastreamer-airplay` and FFmpeg paths. Native Windows cannot enable AirPlay with another path |
| Android playback stops with the screen off, or a local output goes offline | The OS suspended or terminated the app, or the registration lease expired | Preserve the failure time and `<data_dir>/logs/server.log*` before reconnecting. The Server records registration/lease expiry, last poll/renewal/report times, command outcomes, and media grant/access failures — that shows what the Server observed, not why Android suspended the app |
| Android reports a playback error but the output is still available | A media/decoder failure, not a lost registration | Check `native_playback_error` in `<data_dir>/logs/server.log*`. Matching Android/Server builds retain the reported exception with the output, play, and command IDs |
| **USB bit-perfect** says the USB device offers no bit-perfect mixer, or none this player can open | The phone reported no `MIXER_BEHAVIOR_BIT_PERFECT` mixer attributes for that device, or only formats this player cannot open (for example channel index masks or an unrecognized encoding) | Open **Phone audio settings → Reported mixer formats** and read the counts and per-entry reasons: they show exactly what Android returned for each USB device and the API level. No reported bit-perfect entry means the phone or its HAL does not offer one for that dongle; entries marked not usable name the mismatch |

### How playback errors are reported

- An isolated renderer-status query failure neither restarts playback nor opens a popup. A status warning appears after three consecutive failed queries and closes automatically when a query succeeds; dismissing it suppresses repeats during the same failure streak. Playback-command failures and confirmed disconnections are still reported immediately.
- A retained playback error from an earlier connection does not reopen a request-failure popup on every entry; it stays available through the player's error details. Failures of newly issued requests still open an error notice.
- Playback-start errors identify the failed stage (`LoadTrack`, `PrepareMedia`, `SetURI`, or `Play`) with a safe error code when available. Keep the action name and numeric fault code for UPnP rejections, and the action, player state, idle reason, and error text for Cast. Do not reset the queue or disable the firewall to clear a generic failure; a timeout or transport failure still means the command outcome is unknown.
- For **Server-mode local audio** (browser, native Windows, or native Android), an unrecoverable track or decoder failure during Play marks the entry **Playback failed** and lets the Server start the next entry in queue order. This is not natural completion: failed entries, duplicates, and ordering are preserved, and you can retry a failed entry explicitly. Continuation opens no blocking dialog, and playback stops with an error when no next entry remains. Paused or stopped sessions do not resume, and network, authentication, permission, lease-loss, output-device, or unclassified failures never skip tracks. Recoverable damaged frames are left to the decoder — jastreamer does not blindly seek ahead. Native behaviour needs a matching Server and client: update the Server first using the [update procedures](INSTALL.md#upgrade). Saved music instead uses its bounded local-file failure behaviour and never reports a local-file failure as a Server-source failure.
