# jastreamer 0.2 user guide

[한국어 사용자 안내서](INSTRUCTION.ko.md) · [Project overview](README.md)

jastreamer runs a Server on Linux or native Windows. Both host the embedded Web interface, send music to UPnP/DLNA outputs, and can optionally enable Google Cast. AirPlay sending is available only in the Linux container package. Optional Windows and Linux desktop clients only connect to a Server.

## 1. Requirements and safety

- For the Linux container: Linux `amd64` or `arm64` with Docker Engine and Compose v2, or Synology DSM with Container Manager. `arm/v7` is not supported; DS918+ is `amd64`.
- For the native portable Server: Windows x64 and a writable local installation folder. It is not installed as a Windows service.
- An exact image digest or Windows Server ZIP from a published jastreamer 0.2 preview, or a verified separately supplied offline artifact. Preview status and physical-device verification limits still apply.
- A trusted private LAN between Server, browser/client, and outputs. Automatic discovery needs multicast.
- On Linux, separate config and data directories writable by container UID/GID `10001:10001`, plus an existing music directory readable by UID 10001 and mounted read-only.
- On Windows, music paths readable by the account running the Server. The default adjacent `server.json`, `data`, and `music` must remain writable by that account.

Do not use `chmod 777` or recursively change ownership of a music library. HTTP does not encrypt credentials or audio; use built-in HTTPS with your own PEM certificate/key when the LAN path is not fully trusted. Never expose the Server directly to the public Internet.

## 2. Obtain and verify a release

### Public registry image

Choose a published preview from [GitHub Releases](https://github.com/furyheimdall/jastreamer/releases), read its limitations, and obtain the exact Server image reference from its release notes/manifest. Do not use `/releases/latest` to select previews automatically: a prerelease is not marked as the latest production release.

The registry is `ghcr.io/furyheimdall/jastreamer-server`. Public images can be pulled without a GitHub token. Replace the example digest below with the complete value from the selected release, not a guessed tag:

```sh
export JASTREAMER_SERVER_IMAGE='ghcr.io/furyheimdall/jastreamer-server@sha256:<digest-from-release>'
docker pull "$JASTREAMER_SERVER_IMAGE"
docker image inspect --format '{{.Os}}/{{.Architecture}} {{.Id}}' "$JASTREAMER_SERVER_IMAGE"
```

The multi-platform Linux image selects `amd64` or `arm64` for the host. Keep the exact digest in the deployment's persistent environment settings; do not install FFmpeg or Python separately on the host. Google Cast itself needs no Chrome or Python helper. Download the Windows Server or desktop files and their checksums only from the same release.

### Supplied offline artifacts

If you were separately given an offline Server bundle, verify its supplied checksum list before import:

```sh
cd /path/to/supplied/release
sha256sum -c SHA256SUMS
```

An explicitly approved private registry remains an alternative; use its exact digest and private credential handling. Do not put registry tokens in Compose files, source control, or support logs.

If the supplied bundle contains the multi-platform `.oci`, it cannot be passed directly to `docker load`. When an offline Docker TAR is needed, convert only the target architecture on a Linux machine with Skopeo:

```sh
ARCH=amd64  # or arm64
skopeo copy --override-os linux --override-arch "$ARCH" \
  oci-archive:jastreamer-server_0.2.0_linux_amd64-arm64.oci \
  docker-archive:jastreamer-server_0.2.0_linux_${ARCH}.tar:jastreamer-server:0.2.0
sha256sum jastreamer-server_0.2.0_linux_${ARCH}.tar
docker load --input jastreamer-server_0.2.0_linux_${ARCH}.tar
docker image inspect --format '{{.Id}}' jastreamer-server:0.2.0
```

Compare the new TAR checksum after transfer. Use the printed `sha256:...` local image ID as `JASTREAMER_SERVER_IMAGE`.

## 3. Install a Server

### Native Windows x64 portable Server

Download `jastreamer-server_0.2.0_windows-x64.zip` and its `.sha256`, manifest, and verification receipt from the selected preview. The ZIP is unsigned and not production-qualified. Compare its bytes with the sidecar before extracting:

```powershell
$file = '.\jastreamer-server_0.2.0_windows-x64.zip'
$expected = (Get-Content "$file.sha256" -Raw).Split()[0].ToLowerInvariant()
$actual = (Get-FileHash $file -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actual -ne $expected) { throw 'Windows Server ZIP checksum mismatch' }
```

Extract the complete ZIP to a new writable local folder, then run `start-server.cmd` as the ordinary account that will operate the Server. Do not run inside the ZIP, copy only the EXE, run as Administrator, or weaken SmartScreen/Defender globally. On first launch only, the launcher atomically creates an absent `server.json` and adjacent `data` and `music` directories, records their absolute paths, validates the configuration, and starts the Server. It never replaces an existing `server.json`. Keep the console open and use Ctrl+C to stop.

Open `http://127.0.0.1:18080` on that computer, or use the Windows computer's private LAN address and port 18080 from another client. If Windows Firewall prompts, allow the executable on private networks only; do not disable the firewall. TCP 18080 is needed for Web/media access, SSDP UDP 1900 for UPnP discovery, and mDNS UDP 5353 on the selected interfaces for optional Google Cast discovery. Cast also requires Server TCP access to the port each receiver advertises and receiver access to the Server media HTTP(S) URL. Put test music in the adjacent `music` folder, or stop the Server and set an existing absolute Windows folder in `library_roots`; JSON paths may use `C:/Music` or escaped backslashes.

This package is the native Server, not the optional Windows desktop. It provides UPnP/DLNA and optional Google Cast network output and does not install a Renderer or play through local PC speakers. Google Cast needs no Chrome or Python helper, but the package does not bundle FFmpeg or the Linux-only AirPlay helper. Transcoding and AirPlay therefore default to disabled; Cast can stream supported original formats directly, while its fallback requires an operator-configured FFmpeg and enabled media transcoding. Native CI verifies package bytes, first install, the HTTP UI, account persistence, restart, and preservation of an existing configuration; that does not make the unsigned preview production-qualified or certify every Windows system or receiver.

To update an existing portable installation, stop it with Ctrl+C and back up its complete folder, including `server.json`, `data`, and `music`, while stopped. Verify and extract the new ZIP to a separate temporary folder, then replace the package-owned payload in the existing installation, including its program, launcher, template, notices, licenses, and build information. Do not replace `server.json` or delete/move `data` or `music`: their absolute paths and existing account, library, artwork, queue, playlist, and session state must remain intact. Run `start-server.cmd`, confirm it validates the unchanged configuration, and check the existing URL and state after restart. Keep the previous verified package and matching backup for rollback.

### Linux or Synology with Compose

Use the repository's `deploy/docker/server/compose.synology.yaml` on either Linux or Synology. It applies host networking, a read-only container filesystem, a temporary `/tmp`, and the required UID/GID.

Choose host paths:

| Host | Config | Data | Music |
|---|---|---|---|
| Linux example | `/srv/jastreamer/config` | `/srv/jastreamer/data` | `/srv/music` |
| Synology example | `/volume1/docker/jastreamer/config` | `/volume1/docker/jastreamer/data` | `/volume1/music` |

Set the three paths for your host, then install the supplied config:

```sh
export JASTREAMER_CONFIG_PATH='/srv/jastreamer/config'
export JASTREAMER_DATA_PATH='/srv/jastreamer/data'
export JASTREAMER_MUSIC_PATH='/srv/music'

sudo mkdir -p "$JASTREAMER_CONFIG_PATH" "$JASTREAMER_DATA_PATH"
sudo cp /path/to/supplied/server.json "$JASTREAMER_CONFIG_PATH/server.json"
sudo chown -R 10001:10001 "$JASTREAMER_CONFIG_PATH" "$JASTREAMER_DATA_PATH"
sudo chmod 700 "$JASTREAMER_CONFIG_PATH" "$JASTREAMER_DATA_PATH"
sudo chmod 600 "$JASTREAMER_CONFIG_PATH/server.json"
```

For Synology, substitute the `/volume1/...` paths from the table. Give UID 10001 read/traverse permission on the music share, but do not change the whole share's owner.

Review the copied `server.json`:

- keep `data_dir` as `/var/lib/jastreamer`;
- keep the usual library root path as `/music`—the Compose mount maps the host music path there;
- keep packaged paths `/usr/local/bin/ffmpeg` and `/usr/local/bin/jastreamer-airplay`;
- optionally set `server_name`, disable AirPlay if unused, or configure built-in HTTPS with PEM files placed in the config directory;
- leave `media.base_url` empty unless the output must use a specific Server HTTP(S) origin.
- keep `cast.enabled` false unless Google Cast is wanted; an absent value in an older config also defaults to false, and Cast needs no helper path;

The Web Settings page atomically replaces `server.json`, so both the file and config directory must remain writable by UID 10001.

Start with an exact verified registry digest or the imported local image ID:

```sh
export JASTREAMER_SERVER_IMAGE='sha256:<verified-local-image-id>'
docker compose -f deploy/docker/server/compose.synology.yaml config
docker compose -f deploy/docker/server/compose.synology.yaml up -d
docker compose -f deploy/docker/server/compose.synology.yaml ps
docker compose -f deploy/docker/server/compose.synology.yaml logs --tail 100 jastreamer-server
curl --fail http://127.0.0.1:8080/healthz
```

In Synology Container Manager, the same Compose file and four environment variables can be entered as a **Project**. The health URL is `http://<NAS-LAN-IP>:8080/healthz`. Use port 8443 instead when HTTPS is enabled.

## 4. First setup and everyday use

1. Open `http://<server-LAN-IP>:8080/` and create the first administrator account. The password must have at least 10 characters.
2. English is the default. Open **Settings**, then choose **English** or **한국어** under **Language / 언어**. The menu name remains **Settings** in both languages. The change is immediate and remembered; it does not save Server configuration or send playback commands.
3. In **Settings**, confirm that the music root is `/music`, save, then choose **Scan now**. Scanning supports FLAC, MP3, WAV/WAVE, Ogg/Vorbis, Opus, and M4A without modifying source files. Symlinks are skipped.
4. To use Google Cast, enable **Google Cast output** in **Settings**, save, and restart the Server. It remains disabled when `cast.enabled` is false or absent from an older configuration. Do not enable it merely because a receiver is present.
5. Browse or search Library, add tracks or grouped views to Queue, and select an output while playback is stopped. Refresh outputs if a newly powered receiver is missing.
6. Use the footer controls for Play/Pause, Stop, Previous, Next, and Seek when supported by the receiver. AirPlay may require a PIN/password; pair only while stopped.

The queue is Server-wide, preserves order and duplicates, and survives restarts. A Server restart does not automatically resume playback. Cast uses that same single queue, loads media with Cast autoplay disabled, and sends Play explicitly. Receiver groups and gapless playback are not supported.

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

- Footer album artwork navigates to **Queue**; it does not show track information or start playback.
- Library `(i)` and Queue artwork open track information without starting playback. Queue artwork shows a large `(i)` on hover or keyboard focus; touch screens show it continuously.
- The triangular Play button is the first action on the right of each Queue row. Only that button starts the entry.

An isolated renderer-status query failure does not restart playback or open a popup. A status warning appears after three consecutive failed queries and closes automatically when a query succeeds. Dismissing it suppresses repeat popups during the same failure streak. Playback-command failures and confirmed disconnections are still reported immediately.

Playback-start errors identify the failed stage (`LoadTrack`, `PrepareMedia`, `SetURI`, or `Play`) and include a safe error code when available. For UPnP rejections, retain the action name and numeric fault code when reporting the error; for Cast, retain the action, player state, idle reason, and error text. Do not reset the queue or disable the firewall to clear a generic failure; timeout/transport failures still mean the command outcome is unknown.

UPnP, Google Cast, and AirPlay capabilities vary by receiver. Confirm audible playback and the controls you need on your equipment.

## 5. Optional desktop clients

### Windows x64 portable ZIP

Use the unsigned `jastreamer-desktop_0.2.0_windows-x64.zip` from the selected preview on Windows 10/11 x64. Compare its hash with the release checksum:

```powershell
Get-FileHash .\jastreamer-desktop_0.2.0_windows-x64.zip -Algorithm SHA256
```

Extract the complete ZIP to a new writable local folder and run `jastreamer-desktop.exe`; do not run inside the ZIP or copy only the EXE. Select a discovered Server or enter its complete HTTP(S) root URL. The app verifies the Server before connecting, and connection/discovery does not start playback.

Server discovery searches each active IPv4 network adapter every five seconds, including when adapters change. Firewall and multicast restrictions can still require entering the Server URL manually.

Recent Servers, language, cookies, and sessions are stored beside the EXE in `user-data`. Closing the app does not stop Server playback. To upgrade, exit completely, back up the old folder, extract the new ZIP to a new folder, and preserve the old `user-data` beside the new EXE. A different Windows account or PC may require login again.
### Linux amd64 DEB

Use `jastreamer-desktop_0.2.0_linux-amd64.deb` on a graphical Linux amd64 system. The native installation/sandbox qualification target is Ubuntu 24.04 amd64; this is not an arm64 desktop package or a Linux Server package.

Verify the downloaded DEB against the selected release checksum, then install through APT so dependencies are resolved:

```sh
sha256sum -c jastreamer-desktop_0.2.0_linux-amd64.deb.sha256
sudo apt install ./jastreamer-desktop_0.2.0_linux-amd64.deb
```

Launch **JASTREAMER** from the application menu as your ordinary user, or run `/usr/lib/jastreamer-desktop/jastreamer-desktop`. Do not launch it with `sudo` or add `--no-sandbox`. Select a Server or enter its complete HTTP(S) URL; no separate FFmpeg or audio player is needed on this client.

The installer keeps application files root-owned and installs `chrome-sandbox` as `root:root`, mode `4755`. On compatible AppArmor systems it installs an executable-specific user-namespace profile for `/usr/lib/jastreamer-desktop/jastreamer-desktop`. It does not disable AppArmor or the system-wide user-namespace restriction. Unmanaged policy is preserved; local additions belong in `/etc/apparmor.d/local/jastreamer-desktop`. If launch fails, report the error and installed permissions rather than weakening sandbox settings.

Recent Servers, language, cookies, and sessions use `$XDG_CONFIG_HOME/jastreamer-desktop`, normally `~/.config/jastreamer-desktop`, not the root-owned installation directory. Exit completely and back up this profile before installing an updated DEB. If replacing a preview with the same package version, use `sudo apt install --reinstall ./jastreamer-desktop_0.2.0_linux-amd64.deb`. Keep the previous verified DEB for rollback; never remove the profile just to upgrade.


## 6. Backup and upgrade

For the Linux container target, updating means replacing the Server container with a verified image, not running first-account setup again. The image includes the Web interface, FFmpeg, and the AirPlay runtime for its supported architecture. Do not mount the Docker socket into the Server or grant it host-management privileges to make it update itself. For native Windows, follow the separate portable update procedure in section 3; do not apply the Compose steps below.

### Update procedure

Use the **existing** Compose project name, project directory, Compose files, and environment file for every operation. Do not create a second installation with new default storage paths.

1. **Review the target version.** Read its changes, configuration/database compatibility notes, and any required intermediate versions. Record the current image identity, architecture, service URL, mounts, and persistent settings. Confirm backup space and a rollback plan before modifying anything.
2. **Download before downtime.** Pull the exact target digest from the selected public release; no registry login is required. Use existing Docker credentials or private interactive authentication only for an explicitly chosen private registry. Do not paste tokens into chat or configuration files. The multi-platform image selects the host architecture automatically; confirm `amd64` or `arm64`. For an offline artifact, follow section 2 to verify and import the correct platform. Do not use a floating `latest` tag, invent a registry address, or delete the old image.
3. **Agree on the interruption.** Stop playback and confirm it is stopped. Stop only `jastreamer-server` in the existing Compose project before backing up its state. Do not stop unrelated services, remove volumes, or use `down -v`.
4. **Back up consistently.** With the Server stopped, back up the complete config and data directories, the Compose files, and their environment file; record the corresponding old image identity. Confirm the backup can be read and contains the expected files. Protect it as private data because it includes account, session, AirPlay, Google Cast enablement, and possibly TLS configuration or credentials. The read-only source music is not application state and must not be overwritten or modified.
5. **Change the saved image reference.** Set `JASTREAMER_SERVER_IMAGE` in the deployment's persistent settings to the verified target digest or imported local image ID. Preserve all other settings and mounts unless the release explicitly requires a reviewed migration. Do not replace `server.json` with a new-install template. Keep UID/GID `10001:10001`, host networking, read-only rootfs/music, writable config/data, and the other Compose security restrictions. Render the proposed configuration with `docker compose config` and validate the existing configuration with the target image's `--check-config` command before starting it.
6. **Recreate only the Server.** Use the existing project with `up -d --no-deps jastreamer-server`. Confirm the running image matches the intended digest/platform, inspect container state and logs, and verify `/healthz` and the Web page from the client LAN. Do not declare success from container creation alone.
7. **Open and test.** Give the user the actual, previously used HTTP(S) URL including its port. Refresh the browser or the desktop's hosted Web view. Check login, library/artwork, queue order, playlists, settings, and output discovery against the pre-update state. Playback must remain stopped until the user explicitly starts it. Invite the user to play a chosen track, confirm audible sound, try pause/seek where supported, and stop playback. If a check fails, report which step failed and its error text without secrets.

The desktop does not need a package replacement just to display an updated Server-hosted Web interface. If a release also updates the desktop executable, follow section 5 separately and preserve the Windows adjacent `user-data` or Linux per-user profile.

### Rollback

If startup or verification fails, stop the new Server, preserve its logs and state, and follow the approved rollback plan. Restore the previous image **together with its matching config/data and deployment settings backup**; changing only the image tag may not work after a database migration. Restoring a backup can discard changes made since that backup, so confirm this impact before restoring it. Recheck the original URL and preserved state without automatically restarting playback. Never delete or change the source music during an update or rollback.

### Agent-assisted update

Copy this prompt into an agent with authorized server access. Start with the current Server URL and how the agent may connect; let it inspect the existing deployment rather than asking you to rewrite configuration files. Passwords and keys must remain in private credential handling, not in the prompt.

<details>
<summary>Expand and copy the update prompt</summary>

```text
Help me update my existing jastreamer Server, preserving its data.
Read the matching repository README and INSTRUCTION.md, especially
the backup and upgrade procedure. This is an update, not a new install.

Ask for the current Server URL and authorized SSH connection method if
unknown. Inspect the actual deployment to identify its Compose project,
files, persistent environment, image/digest, architecture, mounts, config,
and playback state. Do not assume the installation uses example paths.
Collect only missing information and ask which released version to use;
explain the changes and recommend a compatible verified version.
Do not invent a published image, use latest, or substitute another build.

Show the current and target versions, expected interruption, state to
preserve, backup location, validation steps, and rollback plan for approval.
Keep the existing URL, settings, accounts, library, artwork, queue order,
playlists, AirPlay state, and Google Cast enablement unless a documented
migration is approved. Never enable Cast merely because a receiver appears.
Download and verify the full image for this architecture before downtime,
including its FFmpeg and AirPlay runtime. Google Cast needs no Chrome or
Python helper. Public GHCR images need no login; use private authentication
only for an explicitly chosen private registry, or follow the documented
offline import. Never collect secrets in chat or logs.

After approval, confirm playback is stopped and stop only this Server.
Back up complete config/data and deployment settings with the service
stopped, verify the backup, and retain the old image. Do not copy a running
SQLite database alone, remove volumes, reset accounts, modify music, change
its ownership recursively, or weaken container security.
Update only the required persistent deployment settings, validate Compose
and the target image's --check-config against the prepared configuration,
then recreate this service in the same project with the same mounts.
Write the actual configuration and commands yourself; do not leave
placeholders or depend on environment exports that disappear with a shell.

Verify the running image, logs, /healthz, client-LAN Web access, and preserved
state. Do not automatically pair outputs, start playback, or repeatedly
replace a failing container. If verification fails, preserve evidence and
use only the approved rollback plan; explain any loss of post-backup data.
Report blockers rather than claiming success.

Finish with the actual clickable Server URL and say: open this address,
refresh the Web interface, log in, and check the library, queue, playlists,
and outputs. Invite me to explicitly play a chosen track, confirm sound,
try supported pause/seek, and stop playback. Explain the expected results
and ask for the failed step and redacted error text if anything is wrong.
Keep agent-verified checks separate from user-only listening checks.
Provide the final version/digest, backup location, and rollback instructions.
Desktop packages are separate; preserve Windows user-data or the Linux
per-user profile if the client also needs an update. Do not replace the
desktop just to update the Server-hosted Web interface.
```

</details>

## 7. Troubleshooting

| Problem | Check |
|---|---|
| Web page unavailable | Windows Server console and TCP 18080, or Linux `/healthz`, Compose logs, configured listener, and TCP 8080/8443 firewall access |
| Windows cannot discover Server | Allow mDNS UDP 5353 or enter the full Server URL manually |
| No output appears | Allow SSDP UDP 1900 for UPnP; Google Cast needs mDNS UDP 5353 on the selected interfaces plus Server TCP access to the receiver's advertised Cast port; Linux AirPlay also needs mDNS UDP 5353 and host networking; disable client isolation |
| Output cannot play | Permit Server-to-receiver control/stream traffic and receiver-to-Server media HTTP(S) traffic; set `media.base_url` only when a specific reachable origin is required; for unsupported Cast originals, enable conversion only with a configured FFmpeg |
| Cast FLAC fails after seeking near EOF | Preserve the reported `BUFFERING`/`ERROR`; this receiver-dependent failure was independently reproduced and is not completion, so do not skip the queue entry or weaken the owned `FINISHED` requirement |
| Library is empty | Confirm the Windows folder or Linux `/music` mount is the configured library root, the Server account/UID 10001 can read it, and a scan completed |
| Settings cannot save | Confirm Windows adjacent files or Linux config directory and `server.json` are writable by the Server account/UID 10001 |
| AirPlay authorization fails | Linux Server only: stop playback, repeat the displayed PIN/password flow, and keep the packaged helper/FFmpeg paths; Windows Server does not bundle AirPlay |
| Password lost | Stop the Server, then run `jastreamer-server --reset-password USER --config /etc/jastreamer/server.json` in a maintenance container with the same config/data mounts; enter the new password only at the prompt |

When reporting a problem, include Server version, exact image digest or Windows ZIP SHA-256, Server platform/architecture, relevant logs, and receiver model. Remove passwords, cookies, certificates, and private keys; preserve raw diagnostic wording.
