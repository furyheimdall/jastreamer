# jastreamer 0.2 user guide

[Installation and upgrades](INSTALL.md) · [한국어 사용자 안내서](INSTRUCTION.ko.md) · [Project overview](README.md)

jastreamer runs a Server on Linux or native Windows. Both host the embedded Web interface, send music to UPnP/DLNA outputs, and can optionally enable Google Cast. AirPlay sending is available only in the Linux container package. Optional Windows and Linux desktop clients and the phone PWA connect to a Server; they are not Servers or local audio renderers.

## Installation and updates

Use the [installation guide](INSTALL.md) for requirements, release verification, Linux/Synology or native Windows Server installation, optional clients, backup, upgrades, and rollback. For agent-assisted installation or updates, begin at [AGENTS.md](AGENTS.md); do not copy credentials into an agent prompt.

## 1. First setup and everyday use

1. Open the installed Server's complete private-LAN URL (`http://<server-LAN-IP>:8080/` for the default Linux listener or port 18080 for the default native Windows listener). Create the first administrator account only on a new installation; the password must have at least 10 characters. After an update, use the existing account and session rather than repeating setup or clearing data.
2. English is the default. Open **Settings**, then choose **English** or **한국어** under **Language / 언어**. The menu name remains **Settings** in both languages. The change is immediate and remembered; it does not save Server configuration or send playback commands.
3. In **Settings**, confirm the music root (`/music` for the standard Linux container), save, then choose **Scan now**. Scanning supports FLAC, MP3, WAV/WAVE, Ogg/Vorbis, Opus, and M4A without modifying source files. Symlinks are skipped.
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

### Google Cast media and completion boundaries

Direct Cast streaming is selected conservatively from inspected codec, sample-rate, channel, and, where applicable, bit-depth metadata. Every direct source must have a matching verified codec, a positive sample rate, and mono or stereo channels: FLAC is accepted through 96 kHz with 1–24-bit depth; MP3, Ogg/Vorbis, Ogg/Opus, and M4A/AAC through 48 kHz; and LPCM WAV through 48 kHz with 1–16-bit depth. Missing or mismatched metadata and sources outside those limits are not assumed compatible. With media transcoding enabled and FFmpeg configured, they instead use a nonseekable 44.1 kHz stereo 16-bit WAV stream. The source file is unchanged. Direct streaming avoids this conversion but does not promise bit-perfect receiver output.

Cast control keeps a persistent TLS connection and owns the application and media session it launches. Queue advance requires an explicit `FINISHED` status for that owned media; EOF, an empty status, `BUFFERING`, or `ERROR` is not treated as completion. Pause, seek, and accepted media still depend on the receiver.

### Server path and network selection

- **Browse** beside music folders, the data directory, HTTPS certificate/key files, FFmpeg and the AirPlay helper opens the authenticated **Server filesystem**, not this browser's computer. Windows lists accessible drives and accepts an absolute UNC share path; Linux/NAS starts at `/`. Containers expose only their mounted filesystem. Listings omit symbolic links and Windows reparse points; manual path inputs remain available.
- Navigate with roots, parent folder or an absolute directory path. **Choose** changes only the draft field; **Cancel** leaves it unchanged. Save settings explicitly, then scan music folders. Choosing a data directory does not move the existing database or artwork: preserve that data separately before changing storage and restarting.
- **Server network adapters** lists actual Server adapter names and IP/prefixes. Automatic leaves `network.interfaces` empty; explicit selections retain manually entered names. Unavailable adapters and addresses remain visible but cannot be newly selected for UPnP or Google Cast discovery. Cast mDNS uses UDP 5353 on these selected interfaces.
- **Fill from a server LAN address** fills the editable `media.base_url` with an eligible IPv4 address and an enabled listener's scheme/port. It does not change listener binding. UPnP and Cast receivers must be able to reach that HTTP(S) media origin. Automatic clears the URL override and uses the interface on which the selected output was discovered, rather than an unrelated VPN/default route. An explicit media URL or explicit listener address retains precedence. For Cast, also permit Server TCP access to the receiver's mDNS-advertised Cast port.
- Browsing and adapter/IP selection never save, restart, scan or start playback by themselves. Apply the draft explicitly; listener, storage and network changes may require a restart.

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
| No output appears | Allow SSDP UDP 1900 for UPnP; Google Cast needs mDNS UDP 5353 on the selected interfaces plus Server TCP access to the receiver's advertised Cast port; Linux AirPlay also needs mDNS UDP 5353 and host networking; disable client isolation |
| Output cannot play | Permit Server-to-receiver control/stream traffic and receiver-to-Server media HTTP(S) traffic; set `media.base_url` only when a specific reachable origin is required; for unsupported Cast originals, enable conversion only with a configured FFmpeg |
| Cast FLAC fails after seeking near EOF | Preserve the reported `BUFFERING`/`ERROR`; this receiver-dependent failure was independently reproduced and is not completion, so do not skip the queue entry or weaken the owned `FINISHED` requirement |
| Library is empty | Confirm the Windows folder or Linux `/music` mount is the configured library root, the Server account/UID 10001 can read it, and a scan completed |
| Settings cannot save | Confirm Windows adjacent files or Linux config directory and `server.json` are writable by the Server account/UID 10001 |
| AirPlay authorization fails | Linux Server only: stop playback, repeat the displayed PIN/password flow, and keep the packaged helper/FFmpeg paths; Windows Server does not bundle AirPlay |
| Password lost | Stop the Server, then run `jastreamer-server --reset-password USER --config /etc/jastreamer/server.json` in a maintenance container with the same config/data mounts; enter the new password only at the prompt |

When reporting a problem, include Server version, exact image digest or Windows ZIP SHA-256, Server platform/architecture, relevant logs, and receiver model. Remove passwords, cookies, certificates, and private keys; preserve raw diagnostic wording.
