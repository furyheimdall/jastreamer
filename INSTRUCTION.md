# jastreamer 0.2 user guide

[Installation and upgrades](INSTALL.md) · [한국어 사용자 안내서](INSTRUCTION.ko.md) · [Project overview](README.md)

jastreamer runs a Server on Linux or native Windows. Both host the embedded Web interface, send music to UPnP/DLNA outputs, and can optionally enable Google Cast. AirPlay sending is available only in the Linux container package. Optional Windows and Linux desktop clients and the phone PWA connect to a Server; they are not Servers or local audio renderers.

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

The queue is Server-wide, preserves order and duplicates, and survives restarts. A Server restart does not automatically resume playback. Cast uses that same single queue, loads media with Cast autoplay disabled, and sends Play explicitly. Receiver groups and gapless playback are not supported.

<a id="phone-controls"></a>
### Phone controls

The focused phone layout is selected automatically only for iPhone browsers or Android browsers whose user agent reports both Android and Mobile. An iPad, Android tablet, or merely narrow desktop window retains the existing layout. The phone header retains jastreamer identity and logout, and the four bottom tabs are **Library**, **Playlists**, **Queue**, and **Settings**.

The compact player keeps Play/Pause and Stop immediately available. Use its expand arrow to show Seek, Previous, Next, output selection and refresh, and AirPlay pairing when required; collapse it to return to the compact player. Primary playback, navigation and track-action buttons have at least 44-by-44-pixel targets, and the header, player, and bottom navigation account for device safe areas.

Selecting another bottom tab collapses the expanded player without stopping playback. Escape also collapses it when keyboard focus is inside the player. The expanded panel scrolls on short or landscape screens; the covered page is not interactive until the panel is closed.

The optional **Install jastreamer** card is in **Settings**. It does not change playback or Server configuration. See the [phone PWA installation branch](INSTALL.md#pwa) for browser, trusted-HTTPS, and network-only limitations.

<a id="android-controls"></a>
### Native Android controls

The native Kotlin app adds Server selection around the same Web interface; [installation and APK update rules](INSTALL.md#android) are separate from PWA installation.

- Choose a discovered or recent Server, or enter its HTTP(S) root address and select **Verify and connect**. The app does not connect automatically on a fresh launch. Recent entries are rechecked, and discovery names are accepted only after an HTTP identity check.
- The native header identifies the selected Server and its origin, including port. **Servers** returns to selection without stopping playback. A changed Server UUID is rejected for a saved entry rather than silently reusing its session.
- Cookies and local Web storage are isolated by Server UUID plus complete origin. Different ports are different profiles, even on the same host. Changing addresses can therefore require login again.
- Back first dismisses the keyboard when Android handles it, then navigates Web history when available, then returns to Server selection. Back from selection leaves the app. Rotation retains the live Web page and unsaved form state; returning from the background checks Server identity again before exposing the page.
- Native language controls and **Settings → Language / 언어** support English and Korean. A Web-language change is reflected in the native shell when the page finishes loading or you leave/pause it. The existing phone/tablet layout rules, four tabs, player controls and touch targets remain unchanged.
- Closing, backgrounding, changing Servers or reopening the app does not issue Play or Stop. Playback and queue state belong to the Server. There is no local renderer, offline player, service-worker command queue, or native JavaScript bridge.
- External navigation, new windows, downloads and native permission requests are blocked. The Server's same-origin Content Security Policy also protects its Web network requests; Android request interception alone is not a universal sandbox for arbitrary hostile HTML. Use only a Server you trust, especially over unencrypted HTTP.

The PWA installation card in the shared Web UI is for browser use; the native Android client needs no additional PWA installation.

<a id="ios-controls"></a>
### Native iOS controls

The SwiftUI app wraps the same Server-hosted interface. Its current availability is [source and CI only](INSTALL.md#ios), not an installable phone release.

- Choose a verified nearby or recent Server, or enter its HTTP(S) root address and select **Verify and connect**. A fresh launch stays on selection rather than connecting automatically.
- The header shows the selected Server and complete origin, including port. **Change Server** returns to selection without stopping playback. Foreground return rechecks the Server UUID before exposing the page; identity or network failure keeps the old page and its keyboard inaccessible.
- Cookies and local Web storage use named profiles keyed by verified Server UUID and canonical scheme/host/port. Different ports are separate sessions. Removing a recent entry only changes the list; sign out inside the Server UI to end its session.
- Back navigates available Web history; Reload refreshes the page. Use the keyboard's **Next** and **Done** for form entry. Rotation retains the live page and unsaved input. In compact-height keyboard layouts the back/reload bar hides, while Server switching and language remain available.
- Native language controls and **Settings → Language / 언어** support English and Korean. Web language is read back at page load, foreground return and native screen transitions, without a JavaScript bridge. Changing the native language reloads the Web page, so finish unsaved edits first.
- Closing, backgrounding or switching Servers sends no Play or Stop. There is no local renderer, offline player or cached command queue. External navigation, new windows, downloads, file pickers and native media/device permission requests are blocked; use the browser for file-upload workflows.

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
| Cast FLAC fails after seeking near EOF | Preserve the reported `BUFFERING`/`ERROR`; this receiver-dependent failure was independently reproduced and is not completion, so do not skip the queue entry or weaken the owned `FINISHED` requirement |
| Library is empty | Confirm the Windows folder or Linux `/music` mount is the configured library root, the Server account/UID 10001 can read it, and a scan completed; bundled `jastreamer-samples` also require an explicit scan |
| Settings cannot save | Confirm Windows adjacent files or Linux config directory and `server.json` are writable by the Server account/UID 10001 |
| AirPlay authorization fails | Supported Linux Server only: stop playback, repeat the displayed PIN/password flow, and keep the packaged `/usr/local/bin/jastreamer-airplay` and FFmpeg paths; native Windows cannot enable AirPlay with another path |
| Password lost | Stop the Server, then run `jastreamer-server --reset-password USER --config /etc/jastreamer/server.json` in a maintenance container with the same config/data mounts; enter the new password only at the prompt |

When reporting a problem, include Server version, exact image digest or Windows ZIP SHA-256, Server platform/architecture, relevant logs, and receiver model. Remove passwords, cookies, certificates, and private keys; preserve raw diagnostic wording.
