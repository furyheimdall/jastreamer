# jastreamer 0.2 user guide

[Installation and upgrades](INSTALL.md) · [한국어 사용자 안내서](INSTRUCTION.ko.md) · [Project overview](README.md)

jastreamer runs a Server on Linux or native Windows. Both host the embedded Web interface, send music to UPnP/DLNA outputs, and can optionally enable Google Cast. AirPlay sending is available only in the Linux container package. Optional desktop, native mobile and PWA clients connect to a Server. **This device** adds local audio to the same Server queue: browser audio in browsers, Desktop and iOS, or a native Media3 service in the Android app.

## Installation and updates

Use the [installation guide](INSTALL.md) for requirements, release verification, Linux/Synology or native Windows Server installation, optional clients, upgrades, and rollback. For agent-assisted installation or updates, begin at [AGENTS.md](AGENTS.md); do not copy credentials into an agent prompt.

## 1. First setup and everyday use

1. Open the installed Server's complete private-LAN URL (`http://<server-LAN-IP>:8080/` for the default Linux listener or port 18080 for the default native Windows listener). Create the first administrator account only on a new installation; the password must have at least 10 characters. After an update, use the existing account and session rather than repeating setup or clearing data.
2. English is the default. Open **Settings**, then choose **English** or **한국어** under **Language / 언어**. The menu name remains **Settings** in both languages. The change is immediate and remembered; it does not save Server configuration or send playback commands.
3. In **Settings**, confirm the music root (`/music` for the standard Linux container), save, then choose **Scan now**. A new sample-enabled installation places three bundled MP3s under `jastreamer-samples`; they appear only after this explicit scan. Scanning supports FLAC, MP3, WAV/WAVE, Ogg/Vorbis, Opus, and M4A without modifying source files. Samples are never queued or played automatically.
   If that host music folder is empty, there is nothing to play: put your audio files in the exact host path confirmed during installation, then scan again. Bundled samples, when present, are only test tracks and do not represent your personal library.
   **Library scan**, including its start button, progress and history, is immediately below **Music folders** in Settings. Saving folder edits and starting a scan remain separate actions.
4. To use Google Cast, enable **Google Cast output** in **Settings**, save, and restart the Server. It remains disabled when `cast.enabled` is false or absent from an older configuration. Do not enable it merely because a receiver is present.
5. Browse Library or Playlists, add tracks or grouped views to Queue, and select an output while playback is stopped. Refresh outputs if a newly powered receiver is missing.
6. Use the player controls for Play/Pause, Stop, Previous, Next, and Seek when supported by the receiver. AirPlay may require a PIN or password; pair only while stopped.

### Playback and file-check history

Open **Settings → Playback and file-check history** to view the Server's shared, persistent history. Filter by result type or renderer. Renderer reports include the renderer name and ID, track, error code, playback position when supplied, and expandable structured diagnostics. Older imported records may identify a renderer only by ID. The newest 5,000 records are retained; viewing them never changes playback.

**Scan now** first indexes metadata for browsing. After a successful scan, **Background audio verification** separately checks every indexed audio file, including unchanged files. It decodes one file at a time with the Server's configured FFmpeg and pauses during playback or another scan. FLAC checks also compare the decoded sample count and the STREAMINFO checksum when present. A completed index is not a passed integrity check.

Failed checks and files that could not be verified appear in the same history with the music-folder name and relative path. Missing engines, unsupported formats, timeouts, and changed or unreadable files are not reported as healthy. Progress resumes after a Server restart. These checks never repair, rewrite, delete, or automatically remove source files or queue entries.

The queue is Server-wide, preserves order and duplicates, and survives restarts. A Server restart does not automatically resume playback. Cast uses that same single queue, loads media with Cast autoplay disabled, and sends Play explicitly. Receiver groups and gapless playback are not supported.

<a id="likes"></a>
### Likes and shuffled saved playlists

- Use a track's heart button in Library, Playlists, Queue, or Track information to add or remove its like. All four views show a filled heart when liked and an outlined heart when unliked, and update together, including duplicate queue entries and an open information dialog. Changing a like does not change queue order or playback. Likes are shared Server state, not private per-account lists, and survive rescans and Server restarts.
- Select **Liked** in Library to browse liked tracks. Search and paging still apply to that view.
- In **Playlists**, enter a name under **Liked shuffle** and select **Shuffle**. This saves all currently available liked tracks in random order, not just the current page or search results. Unavailable tracks are excluded; an empty selection or more than the normal 10,000-track playlist limit produces an error rather than a partial playlist.
- The result is an ordinary saved snapshot: later like changes do not rewrite it. Creating it does not change Queue or start playback; use its normal playback or queue actions explicitly.

<a id="browser-output"></a>
### This device: local audio output

The browser instructions below apply to browsers, PWA, Desktop and iOS. For the native Android app, see [Android local playback](#android-controls). Both appear as **Local audio** and use the Server queue.

1. Stop playback, then choose the output marked **(This device)** under **Output device**, for example **Windows · Chrome (This device) [Local audio]**. On a phone, expand the compact player to reach the selector.
2. Choose a track or use the existing queue and press **Play**. If the browser blocks audio, select **Allow playback** on that page. If the pending request has already failed, dismiss the error and press Play again; commands are not silently replayed.
3. Use Pause, Stop, Previous, Next and supported Seek normally. The browser is an output of the existing Server queue, not a separate local queue.

The default name describes the OS/browser information available to the page, not the computer's hostname or a phone's user-assigned name. Use the pencil button **Name this local output** beside the output selector to save an alias such as **Office PC**. **Use default name**, or saving an empty alias, restores the automatic name. Names are limited to 80 UTF-8 bytes; Korean characters can use several bytes each.

The alias is saved in this browser profile for this Server UUID and exact origin (including port). It does not follow you to another browser/profile, private browsing session or replacement Server. Storage failures are reported rather than claimed as saved. Clearing browser storage removes the alias.

Only the page that registered and owns the output adds **(This device)**, for example **Office PC (This device)**. Other pages/devices see **Office PC** without that marker, even under the same account. A not-yet-registered page also offers its own local output with the marker; this is not a label on somebody else's renderer. Naming an unregistered browser does not register/select it. Renaming a registered output updates its name for other clients without changing its ID, queue, playback or output selection. The alias is a display name, not verified hardware identity.

Control uses same-origin authenticated HTTP JSON; audio uses HTTP(S) GET/Range through the browser's audio element. This is not UPnP, HLS, DASH or WebRTC. The browser and operating system choose the physical speaker/headphones; there is no hardware-output picker, native WASAPI/ASIO engine, exclusive mode or bit-perfect guarantee. Supported formats depend on the browser decoder. Conversion requires enabled transcoding and configured FFmpeg; converted WAV streams cannot seek.

Keep the owning browser page open. Closing or reloading it releases that browser output; loss of its live registration makes the output unavailable without discarding the queue. Stop if needed, reselect **This device**, then explicitly Play to resume. Closing another Control does not stop the owning browser, and network outputs remain independent of Control lifetime. Background/lock-screen playback in browsers, PWA, Desktop Web content and iOS depends on the browser/WebView and OS and is not guaranteed. These clients have no offline playback or native background-audio service; the native Android app uses the service described below.

<a id="phone-controls"></a>
### Phone controls

The focused phone layout is selected automatically only for iPhone browsers or Android browsers whose user agent reports both Android and Mobile. An iPad, Android tablet, or merely narrow desktop window retains the existing layout. The phone header retains jastreamer identity and logout, and the four bottom tabs are **Library**, **Playlists**, **Queue**, and **Settings**.

The compact player keeps Play/Pause and Stop immediately available. Use its expand arrow to show Seek, Previous, Next, output selection and refresh, and AirPlay pairing when required; collapse it to return to the compact player. Primary playback, navigation and track-action buttons have at least 44-by-44-pixel targets, and the header, player, and bottom navigation account for device safe areas.

Selecting another bottom tab collapses the expanded player without stopping playback. Escape also collapses it when keyboard focus is inside the player. The expanded panel scrolls on short or landscape screens; the covered page is not interactive until the panel is closed.

The optional **Install jastreamer** card is in **Settings**. It does not change playback or Server configuration. See the [phone PWA installation branch](INSTALL.md#pwa) for browser, trusted-HTTPS, and network-only limitations.

<a id="android-controls"></a>
### Native Android controls

The native Kotlin app adds Server selection and native local playback around the shared Web interface; [compatible Server/Web UI versions, installation and APK update rules](INSTALL.md#android) are separate from PWA installation.

- Choose a discovered or recent Server, or enter its HTTP(S) root address and select **Verify and connect**. The app does not connect automatically on a fresh launch. Recent entries are rechecked, and discovery names are accepted only after an HTTP identity check.
- The native header identifies the selected Server and its origin, including port. **Servers** returns to selection without stopping network-output playback. A changed Server UUID is rejected for a saved entry rather than silently reusing its session.
- Cookies and local Web storage are isolated by Server UUID plus complete origin. Different ports are different profiles, even on the same host. Changing addresses can therefore require login again.
- Back first dismisses the keyboard when Android handles it, then navigates Web history when available, then returns to Server selection. Back from selection leaves the app. Rotation retains the live Web page and unsaved form state; returning from the background checks Server identity again before exposing the page.
- Native language controls and **Settings → Language / 언어** support English and Korean. A Web-language change is reflected in the native shell when the page finishes loading or you leave/pause it. The existing phone/tablet layout rules, four tabs, player controls and touch targets remain unchanged.
- The native shell sends no Play or Stop just because its screen closes, backgrounds, changes Servers or reopens. The Server owns the queue, current track and next-track decision. Browsing another Server does not stop an existing native output; stop that output before registering local playback with a different Server.
- To play on the phone, stop playback and explicitly select **Android · jastreamer (This device) [Local audio]**, or its saved alias, then press **Play**. The Media3 foreground service plays authenticated same-origin HTTP(S) GET/Range through the phone's OS-selected speaker, headphones or Bluetooth route. Supported formats depend on Media3/Android decoders; there is no exclusive-mode or bit-perfect guarantee.
- Android's system media card and lock-screen controls send Play, Pause, Stop, Previous, Next and supported Seek to the Server; they do not create an Android queue. Native playback continues independently of WebView backgrounding or Activity/WebView recreation. The same app reattaches its exact live registration after recreation; another browser or device does not inherit its **(This device)** marker.
- Transient media failures retry at most three times within a 12-second recovery window, retaining the current track, position and latest Play/Pause intent. Stop, track replacement, authentication loss and registration-lease expiry cancel recovery. Permanent failures are shown rather than retried indefinitely. Host connection revalidation also uses bounded retries; TLS and Server-identity failures remain terminal.
- Process termination or an expired/lost registration stops local playback. There is no automatic re-registration or Play after a process restart or terminal loss: resolve the error, sign in if needed, stop Server playback if necessary, then explicitly select the local output and Play. Android/OEM process and battery restrictions still apply; there is no offline player or service-worker command queue.
- Only the verified Server's current main-frame document receives a restricted native interface for local-output connection, status and naming. It exposes no cookies, registration credentials, arbitrary native calls or filesystem access. Other clients retain browser audio; iOS does not gain this interface.
- External navigation, new windows, downloads and native permission requests are blocked. The Server's same-origin Content Security Policy also protects its Web network requests; Android request interception alone is not a universal sandbox for arbitrary hostile HTML. Use only a Server you trust, especially over unencrypted HTTP.

The PWA installation card in the shared Web UI is for browser use; the native Android client needs no additional PWA installation.

<a id="ios-controls"></a>
### Native iOS controls

The SwiftUI app wraps the same Server-hosted interface. Its current availability is [source and CI only](INSTALL.md#ios), not an installable phone release.

- Choose a verified nearby or recent Server, or enter its HTTP(S) root address and select **Verify and connect**. A fresh launch stays on selection rather than connecting automatically.
- The header shows the selected Server and complete origin, including port. **Change Server** returns to selection without stopping network-output playback. Foreground return rechecks the Server UUID before exposing the page; identity or network failure keeps the old page and its keyboard inaccessible.
- Cookies and local Web storage use named profiles keyed by verified Server UUID and canonical scheme/host/port. Different ports are separate sessions. Removing a recent entry only changes the list; sign out inside the Server UI to end its session.
- Back navigates available Web history; Reload refreshes the page. Use the keyboard's **Next** and **Done** for form entry. Rotation retains the live page and unsaved input. In compact-height keyboard layouts the back/reload bar hides, while Server switching and language remain available.
- Native language controls and **Settings → Language / 언어** support English and Korean. Web language is read back at page load, foreground return and native screen transitions, without a JavaScript bridge. Changing the native language reloads the Web page, so finish unsaved edits first.
- The native shell sends no Play or Stop when closing, backgrounding or switching Servers. If the Web page owns **This device**, losing that page or its live registration releases the browser output as described above. There is no standalone native audio engine, offline player or cached command queue. External navigation, new windows, downloads, file pickers and native media/device permission requests are blocked; use the browser for file-upload workflows.

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

For **local audio** (browser or native Android), an unrecoverable track/media-engine failure during Play marks the entry **Playback failed** and lets the Server start the next entry in queue order. This is not natural completion: failed entries, duplicates and ordering are preserved, and you can explicitly retry a failed entry. Continuation does not open a blocking error dialog; if no next entry remains, playback stops with an error. Paused/stopped sessions do not resume, and network, authentication, permission, lease-loss or unclassified renderer failures do not skip tracks. Recoverable damaged frames are left to the decoder; JaStreamer does not blindly seek ahead. Android continuation requires a matching Server and APK: update the Server first using the [update procedures](INSTALL.md#upgrade).

UPnP, Google Cast, and AirPlay capabilities vary by receiver. Confirm audible playback and the controls you need on your equipment.

## 2. Troubleshooting

| Problem | Check |
|---|---|
| Web page unavailable | Windows Server console and TCP 18080, or Linux `/healthz`, Compose logs, configured listener, and TCP 8080/8443 firewall access |
| Phone layout does not appear | Confirm the device is an iPhone or an Android browser reporting both Android and Mobile; iPads, Android tablets, and narrow desktop windows intentionally retain the existing layout |
| PWA install action does not appear | Open **Settings** and follow [PWA installation](INSTALL.md#pwa); a private-LAN HTTP origin shows the trusted-HTTPS requirement |
| Windows cannot discover Server | Allow mDNS UDP 5353 or enter the full Server URL manually |
| Android reports unsupported WebView | Update the device's Android System WebView/Chrome provider and reopen the app; independent profiles are mandatory and shared cookies are never used as a fallback |
| Android cannot discover or reconnect | Check Wi-Fi, mDNS UDP 5353, VPN/client isolation and app network access; enter the complete URL manually, and retain identity/TLS errors rather than bypassing them |
| No output appears | Allow SSDP UDP 1900 for UPnP; Google Cast needs mDNS UDP 5353 on the selected interfaces plus Server TCP access to the receiver's advertised Cast port; Linux AirPlay also needs mDNS UDP 5353 and host networking; disable client isolation |
| Output cannot play | Permit Server-to-receiver control/stream traffic and receiver-to-Server media HTTP(S) traffic; normally leave **Server URL used by playback devices to fetch audio** blank, or set it only to a specific receiver-reachable Server origin; for unsupported Cast originals, enable conversion only with a configured FFmpeg |
| This device is silent or disconnected | Select **Allow playback** if shown, check browser/OS mute and audio routing, and keep the owning page open; after reload or lease loss, Stop if needed, reselect **This device** and explicitly Play; preserve decoder and transport errors |
| Cast FLAC fails after seeking near EOF | Preserve the reported `BUFFERING`/`ERROR`; this receiver-dependent failure was independently reproduced and is not completion, so do not skip the queue entry or weaken the owned `FINISHED` requirement |
| Library is empty | Confirm the Windows folder or Linux `/music` mount is the configured library root, the Server account/UID 10001 can read it, and a scan completed; bundled `jastreamer-samples` also require an explicit scan |
| Settings cannot save | Confirm Windows adjacent files or Linux config directory and `server.json` are writable by the Server account/UID 10001 |
| AirPlay authorization fails | Supported Linux Server only: stop playback, repeat the displayed PIN/password flow, and keep the packaged `/usr/local/bin/jastreamer-airplay` and FFmpeg paths; native Windows cannot enable AirPlay with another path |
| Password lost | Stop the Server, then run `jastreamer-server --reset-password USER --config /etc/jastreamer/server.json` in a maintenance container with the same config/data mounts; enter the new password only at the prompt |
| Android playback stops with the screen off, or a local output goes offline | Preserve the failure time and `<data_dir>/logs/server.log*` before reconnecting if possible. The Server records registration/lease expiry, last poll/renewal/report times, command outcomes, and media grant/access failures. These identify what the Server observed, not why Android suspended or terminated the app |
| Android reports a playback error but the output is still available | Check `native_playback_error` in `<data_dir>/logs/server.log*`. Matching Android/Server builds automatically retain the reported playback exception with the output, play, and command IDs; a playback failure does not itself mean registration was lost |

When reporting a problem, include Server version, exact image digest or Windows ZIP SHA-256, Server platform/architecture, relevant logs, and receiver model. Remove passwords, cookies, certificates, and private keys; preserve raw diagnostic wording.

Server diagnostics use UTC and are written to both the console and `<data_dir>/logs/server.log`. The default Linux container path is `/var/lib/jastreamer/logs/server.log`, retained in the existing data mount across container replacement. Rotation keeps the current file and `server.log.1`–`server.log.3`, each at most 5 MiB. Normal local-output activity is summarized at most once every 30 seconds per registration; repeated request failures are rate-limited. Diagnostic events correlate generated output/play/command IDs without recording credentials, cookies, media URLs, filenames, or output display names. On POSIX systems, the log directory is created with mode 0700 and files with mode 0600; on Windows, restrict the data directory's access permissions to the Server account. If persistent logging cannot start, inspect the Server console or Compose logs. Collect all retained log files with the exact incident time; do not expose the data directory over HTTP.

Android playback-error diagnostics require matching Server and APK versions; update the compatible Server before installing the corresponding APK using the [update procedures](INSTALL.md#upgrade). Failed native commands and terminal playback-error reports include the Media3 error code/name, occurrence time, playback position, cause classes, and bounded code-only stack frames. HTTP status and codec/audio-output numeric error codes are included when available. Exception messages, media URLs, file paths, and credentials are excluded. The Server retains accepted reports automatically in its existing rotating log, without an ADB collection step. This is not an offline upload queue: a process exit or loss of Server connectivity before delivery can still prevent collection, and older missing exceptions cannot be reconstructed.
