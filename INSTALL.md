# Install and update jastreamer

[README](README.md) · [User guide](INSTRUCTION.md) · [한국어 설치 안내](INSTALL.ko.md)

Choose the branch below for the package and platform you actually use. The Server hosts the Web interface; the optional desktop and phone PWA are clients of an existing Server, not Servers or local audio renderers.

Public previews are unsigned and not production-qualified. Read the selected release's limitations, verify every downloaded artifact, and confirm operation on your own network and receivers.

| What you want to do | Follow this branch |
| --- | --- |
| Run a Server on Linux or Synology | [Linux Server](#linux-server) |
| Run a Server on Windows | [Windows Server](#windows-server) |
| Add a desktop client to an existing Server | [Windows ZIP](#desktop-windows) or [Linux DEB](#desktop-linux) |
| Use a phone or install its home-screen app | [Phone PWA](#pwa) |
| Update or recover an existing installation | [Backup and upgrade](#backup-and-upgrade) or [rollback](#rollback) |

<a id="requirements"></a>
## Requirements and safety

- **Linux Server:** Linux `amd64` or `arm64` with Docker Engine and Compose v2, or Synology DSM with Container Manager. `arm/v7` is not supported; DS918+ is `amd64`.
- **Native Windows Server:** Windows x64 and a writable local installation folder. The portable Server is not installed as a Windows service.
- **Windows desktop:** Windows 10/11 x64. There is no Windows ARM64 desktop package.
- **Linux desktop:** a graphical Linux `amd64` system. Ubuntu 24.04 amd64 is the native installation and sandbox qualification target; there is no Linux ARM64 desktop package.
- An exact image digest or package from a published jastreamer 0.2 preview, or a verified separately supplied offline artifact. Preview status and physical-device verification limits still apply.
- A trusted private LAN between the Server, browser/client, and outputs. Automatic discovery needs multicast.
- On Linux, separate config and data directories writable by container UID/GID `10001:10001`, plus an existing music directory readable by UID 10001 and mounted read-only.
- On Windows, music paths readable by the account running the Server. The default adjacent `server.json`, `data`, and `music` must remain writable by that account.

Keep Server config, data, and music separate. Do not use `chmod 777` or recursively change ownership of a music library. HTTP does not encrypt credentials or audio; use the Server's built-in HTTPS listener with your own PEM certificate and key when the LAN path is not fully trusted. Do not bypass certificate warnings or expose the Server directly to the public Internet.

<a id="releases"></a>
## Obtain and verify a release

### Public registry image

Choose a published preview from [GitHub Releases](https://github.com/furyheimdall/jastreamer/releases), read its limitations, and obtain the exact Server image reference from its release notes or manifest. Do not use `/releases/latest` to select previews automatically: a prerelease is not marked as the latest production release.

The registry is `ghcr.io/furyheimdall/jastreamer-server`. Public images can be pulled without a GitHub token. Replace the example digest below with the complete value from the selected release, not a guessed or floating tag:

```sh
export JASTREAMER_SERVER_IMAGE='ghcr.io/furyheimdall/jastreamer-server@sha256:<digest-from-release>'
docker pull "$JASTREAMER_SERVER_IMAGE"
docker image inspect --format '{{.Os}}/{{.Architecture}} {{.Id}}' "$JASTREAMER_SERVER_IMAGE"
```

The multi-platform Linux image selects `amd64` or `arm64` for the host. Keep the exact digest in the deployment's persistent environment settings; do not install FFmpeg or Python separately on the host. Google Cast itself needs no Chrome or Python helper. Download Windows Server or desktop files and their checksums only from the same release.

### Supplied offline artifacts

If you were separately given an offline Server bundle, verify its supplied checksum list before import:

```sh
cd /path/to/supplied/release
sha256sum -c SHA256SUMS
```

An explicitly approved private registry remains an alternative; use its exact digest and private credential handling. Do not put registry tokens in Compose files, source control, or support logs.

A supplied multi-platform `.oci` cannot be passed directly to `docker load`. When an offline Docker TAR is needed, convert only the target architecture on a Linux machine with Skopeo:

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

<a id="linux-server"></a>
## Linux or Synology Server

The Linux `amd64`/`arm64` image contains the Server, embedded Web interface, FFmpeg, and the AirPlay helper and runtime. Use the repository's `deploy/docker/server/compose.synology.yaml` on either Linux or Synology. It applies host networking, a read-only container filesystem, a temporary `/tmp`, dropped capabilities, `no-new-privileges`, and UID/GID `10001:10001`.

Choose host paths:

| Host | Config | Data | Music |
|---|---|---|---|
| Linux example | `/srv/jastreamer/config` | `/srv/jastreamer/data` | `/srv/music` |
| Synology example | `/volume1/docker/jastreamer/config` | `/volume1/docker/jastreamer/data` | `/volume1/music` |

The commands below are for a **new installation with empty, separate config/data locations only**. If either location already contains application state, follow [backup and upgrade](#backup-and-upgrade); do not copy a new template over existing configuration. Set the three paths for your host, then install the supplied config:

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

For Synology, substitute the `/volume1/...` paths from the table. Give UID 10001 read and traverse permission on the music share, but do not change the whole share's owner.

Review the copied `server.json`:

- keep `data_dir` as `/var/lib/jastreamer`;
- keep the usual library root path as `/music`—the Compose mount maps the host music path there;
- keep packaged paths `/usr/local/bin/ffmpeg` and `/usr/local/bin/jastreamer-airplay`;
- optionally set `server_name`, disable AirPlay if unused, or configure built-in HTTPS with PEM files placed in the config directory;
- leave `media.base_url` empty unless the output must use a specific Server HTTP(S) origin;
- keep `cast.enabled` false unless Google Cast is wanted; an absent value in an older config also defaults to false, and Cast needs no helper path.

The Web Settings page atomically replaces `server.json`, so both the file and config directory must remain writable by UID 10001.

Start with an exact verified registry digest or imported local image ID:

```sh
export JASTREAMER_SERVER_IMAGE='sha256:<verified-local-image-id>'
docker compose -f deploy/docker/server/compose.synology.yaml config
docker compose -f deploy/docker/server/compose.synology.yaml up -d
docker compose -f deploy/docker/server/compose.synology.yaml ps
docker compose -f deploy/docker/server/compose.synology.yaml logs --tail 100 jastreamer-server
curl --fail http://127.0.0.1:8080/healthz
```

Use the complete public registry digest instead of the local image ID when installing from GHCR. Save all four environment values in the existing Compose project's persistent environment settings; temporary `export` commands alone are not an upgradeable deployment record.

In Synology Container Manager, the same Compose file and four environment variables can be entered as a **Project**. The health URL is `http://<NAS-LAN-IP>:8080/healthz`. Use port 8443 instead when built-in HTTPS is enabled. Verify the Web page from a client on the private LAN, then continue with [first setup](INSTRUCTION.md#1-first-setup-and-everyday-use). A healthy container does not prove audible playback.

<a id="windows-server"></a>
## Native Windows x64 portable Server

Download `jastreamer-server_0.2.0_windows-x64.zip` and its `.sha256`, manifest, and verification receipt from the selected preview. The ZIP is unsigned and not production-qualified. Compare its bytes with the sidecar before extracting:

```powershell
$file = '.\jastreamer-server_0.2.0_windows-x64.zip'
$expected = (Get-Content "$file.sha256" -Raw).Split()[0].ToLowerInvariant()
$actual = (Get-FileHash $file -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actual -ne $expected) { throw 'Windows Server ZIP checksum mismatch' }
```

Extract the complete ZIP to a new writable local folder, then run `start-server.cmd` as the ordinary account that will operate the Server. Do not run inside the ZIP, copy only the EXE, run as Administrator, or weaken SmartScreen or Defender globally. On first launch only, the launcher atomically creates an absent `server.json` and adjacent `data` and `music` directories, records their absolute paths, validates the configuration, and starts the Server. It never replaces an existing `server.json`. Keep the console open and use Ctrl+C to stop.

Open `http://127.0.0.1:18080` on that computer, or use the Windows computer's private LAN address and port 18080 from another client. If Windows Firewall prompts, allow the executable on private networks only; do not disable the firewall. TCP 18080 is needed for Web and media access, SSDP UDP 1900 for UPnP discovery, and mDNS UDP 5353 on the selected interfaces for optional Google Cast discovery. Cast also requires Server TCP access to the port each receiver advertises and receiver access to the Server media HTTP(S) URL. Put test music in the adjacent `music` folder, or stop the Server and set an existing absolute Windows folder in `library_roots`; JSON paths may use `C:/Music` or escaped backslashes.

This package is the native Server, not the optional Windows desktop. It provides UPnP/DLNA and optional Google Cast network output and does not install a Renderer or play through local PC speakers. Google Cast needs no Chrome or Python helper, but the package does not bundle FFmpeg or the Linux-only AirPlay helper. Transcoding and AirPlay therefore default to disabled; Cast can stream supported original formats directly, while its fallback requires an operator-configured FFmpeg and enabled media transcoding. Native CI verifies package bytes, first install, the HTTP UI, account persistence, restart, and preservation of an existing configuration; that does not make the unsigned preview production-qualified or certify every Windows system or receiver.

To update an existing portable installation, stop it with Ctrl+C and back up its complete folder, including `server.json`, `data`, and `music`, while stopped. Verify and extract the new ZIP to a separate temporary folder, then replace the package-owned payload in the existing installation, including its program, launcher, template, notices, licenses, and build information. Do not replace `server.json` or delete or move `data` or `music`: their absolute paths and existing account, library, artwork, queue, playlist, and session state must remain intact. Run `start-server.cmd`, confirm it validates the unchanged configuration, and check the existing URL and state after restart. Keep the previous verified package and matching backup for [rollback](#rollback).

<a id="desktop-windows"></a>
## Optional Windows x64 desktop

Use the unsigned `jastreamer-desktop_0.2.0_windows-x64.zip` from the selected preview on Windows 10/11 x64. Compare its hash with the release checksum:

```powershell
Get-FileHash .\jastreamer-desktop_0.2.0_windows-x64.zip -Algorithm SHA256
```

Extract the complete ZIP to a new writable local folder and run `jastreamer-desktop.exe`; do not run inside the ZIP or copy only the EXE. Select a discovered Server or enter its complete HTTP(S) root URL. The app verifies the Server before connecting, and connection or discovery does not start playback.

Server discovery searches each active IPv4 network adapter every five seconds, including when adapters change. Firewall and multicast restrictions can still require entering the Server URL manually.

Recent Servers, language, cookies, and sessions are stored beside the EXE in `user-data`. Closing the app does not stop Server playback. To upgrade, exit completely, back up the old folder, extract the new ZIP to a new folder, and preserve the old `user-data` beside the new EXE. A different Windows account or PC may require login again.

<a id="desktop-linux"></a>
## Optional Linux amd64 desktop

Use `jastreamer-desktop_0.2.0_linux-amd64.deb` on a graphical Linux amd64 system. The native installation and sandbox qualification target is Ubuntu 24.04 amd64; this is not an arm64 desktop package or a Linux Server package.

Verify the downloaded DEB against the selected release checksum, then install through APT so dependencies are resolved:

```sh
sha256sum -c jastreamer-desktop_0.2.0_linux-amd64.deb.sha256
sudo apt install ./jastreamer-desktop_0.2.0_linux-amd64.deb
```

Launch **JASTREAMER** from the application menu as your ordinary user, or run `/usr/lib/jastreamer-desktop/jastreamer-desktop`. Do not launch it with `sudo` or add `--no-sandbox`. Select a Server or enter its complete HTTP(S) URL; no separate FFmpeg or audio player is needed on this client.

The installer keeps application files root-owned and installs `chrome-sandbox` as `root:root`, mode `4755`. On compatible AppArmor systems it installs an executable-specific user-namespace profile for `/usr/lib/jastreamer-desktop/jastreamer-desktop`. It does not disable AppArmor or the system-wide user-namespace restriction. Unmanaged policy is preserved; local additions belong in `/etc/apparmor.d/local/jastreamer-desktop`. If launch fails, report the error and installed permissions rather than weakening sandbox settings.

Recent Servers, language, cookies, and sessions use `$XDG_CONFIG_HOME/jastreamer-desktop`, normally `~/.config/jastreamer-desktop`, not the root-owned installation directory. Exit completely and back up this profile before installing an updated DEB. If replacing a preview with the same package version, use `sudo apt install --reinstall ./jastreamer-desktop_0.2.0_linux-amd64.deb`. Keep the previous verified DEB for rollback; never remove the profile just to upgrade.

<a id="pwa"></a>
## Optional phone PWA

The installable phone Web app is another view of the Server-hosted interface, requires network access to that Server, and provides no service worker, cached library, or offline playback. The Windows and Linux desktop apps do not need this PWA.

1. Open the Server's complete URL in the phone browser and sign in, then open **Settings** and find the optional **Install jastreamer** card.
2. In a browser that supplies a real `beforeinstallprompt` event, choose the card's **Install app** button and approve the browser's native prompt. If the browser does not offer that event, follow its displayed browser-specific guidance instead.
3. On iPhone Safari, use **Share → Add to Home Screen**. iPad uses the existing tablet/desktop layout rather than the phone layout, but Safari can still show the Add to Home Screen guidance.
4. Launch the saved app. When the browser reports standalone display mode, the Settings card reports that jastreamer is installed.

PWA installation requires an HTTPS origin trusted by that phone and browser. Plain HTTP is accepted only for local development on `localhost`, `127.0.0.1`, or `::1`; a phone opening a private-LAN HTTP address sees the HTTPS requirement rather than an install action. Configure the Server's built-in HTTPS certificate, private key, and listener in **Settings**, save, restart when requested, and reopen the trusted HTTPS URL. Do not bypass certificate warnings, register an untrusted certificate as a workaround, expose the NAS publicly, or place a naive TLS-terminating proxy in front of the HTTP listener: the Server checks mutation request origins against its own listener scheme.

The phone layout and normal browser controls do not require PWA installation. On a trusted private-LAN HTTP deployment, refresh the Server page and use it in the browser; installation remains unavailable until trusted HTTPS is configured. Moving to a different scheme, hostname, or port creates a different browser origin and may require signing in again.

See [Phone controls](INSTRUCTION.md#phone-controls) for the automatic phone layout and playback controls.

<a id="backup-and-upgrade"></a>
## Backup and upgrade

For the Linux container target, updating means replacing the Server container with a verified image, not running first-account setup again. The image includes the Web interface, FFmpeg, and the AirPlay runtime for its supported architecture. Do not mount the Docker socket into the Server or grant it host-management privileges to make it update itself. For native Windows, follow the [portable update procedure](#windows-server); do not apply the Compose steps below.

Use the **existing** Compose project name, project directory, Compose files, and environment file for every operation. Do not create a second installation with new default storage paths.

1. **Review the target version.** Read its changes, configuration/database compatibility notes, and any required intermediate versions. Record the current image identity, architecture, service URL, mounts, and persistent settings. Confirm backup space and a rollback plan before modifying anything.
2. **Download before downtime.** Pull the exact target digest from the selected public release; no registry login is required. Use existing Docker credentials or private interactive authentication only for an explicitly chosen private registry. Do not paste tokens into chat or configuration files. The multi-platform image selects the host architecture automatically; confirm `amd64` or `arm64`. For an offline artifact, follow [Obtain and verify a release](#releases) to verify and import the correct platform. Do not use a floating `latest` tag, invent a registry address, or delete the old image.
3. **Agree on the interruption.** Stop playback and confirm it is stopped. Stop only `jastreamer-server` in the existing Compose project before backing up its state. Do not stop unrelated services, remove volumes, or use `down -v`.
4. **Back up consistently.** With the Server stopped, back up the complete config and data directories, the Compose files, and their environment file; record the corresponding old image identity. Confirm the backup can be read and contains the expected files. Protect it as private data because it includes account, session, AirPlay, Google Cast enablement, and possibly TLS configuration or credentials. The read-only source music is not application state and must not be overwritten or modified.
5. **Change the saved image reference.** Set `JASTREAMER_SERVER_IMAGE` in the deployment's persistent settings to the verified target digest or imported local image ID. Preserve all other settings and mounts unless the release explicitly requires a reviewed migration. Do not replace `server.json` with a new-install template. Keep UID/GID `10001:10001`, host networking, read-only root filesystem and music, writable config and data, and the other Compose security restrictions. Render the proposed configuration with `docker compose config` and validate the existing configuration with the target image's `--check-config` command before starting it.
6. **Recreate only the Server.** Use the existing project with `up -d --no-deps jastreamer-server`. Confirm the running image matches the intended digest and platform, inspect container state and logs, and verify `/healthz` and the Web page from the client LAN. Do not declare success from container creation alone.
7. **Open and test.** Use the actual, previously used HTTP(S) URL including its port. Refresh the browser or desktop's hosted Web view. Check login, library and artwork, queue order, playlists, settings, and output discovery against the pre-update state. Playback must remain stopped until you explicitly start it. Play a chosen track, confirm audible sound, try pause and seek where supported, and stop playback. If a check fails, retain the failed step and exact error text without secrets.

The desktop does not need a package replacement just to display an updated Server-hosted Web interface, including the phone/PWA UI. If a release also updates the desktop executable, follow its separate [Windows](#desktop-windows) or [Linux](#desktop-linux) procedure and preserve the Windows adjacent `user-data` or Linux per-user profile.

For agent-assisted installation or updates, start at [AGENTS.md](AGENTS.md); its inspection, approval, data-security, verification, and reporting workflow applies before these manual platform steps.

<a id="rollback"></a>
## Rollback

If startup or verification fails, stop the new Server and preserve its logs and state. Restore the previous image **together with its matching config/data and deployment-settings backup**; changing only the image tag may not work after a database migration. Restoring a backup can discard changes made since that backup, so confirm this impact before restoring it. Recheck the original URL and preserved state without automatically restarting playback. Never delete or change the source music during an update or rollback.

For a native Windows Server, restore the matching stopped-folder backup and previous verified package together. For a desktop-only rollback, restore the previous verified package while preserving its matching Windows `user-data` or Linux per-user profile. Return to the [user guide](INSTRUCTION.md) for operation and troubleshooting after the original state is restored.
