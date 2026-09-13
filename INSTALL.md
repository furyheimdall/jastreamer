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
| Update or recover an existing installation | [Upgrade](#upgrade) or [rollback](#rollback) |

<a id="requirements"></a>
## Requirements and safety

- **Linux Server:** Linux `amd64` or `arm64` with Docker Engine and Compose v2, or Synology DSM with Container Manager. `arm/v7` is not supported; DS918+ is `amd64`.
- **Native Windows Server:** Windows x64 and a writable local installation folder. The portable Server is not installed as a Windows service.
- **Windows desktop:** Windows 10/11 x64. There is no Windows ARM64 desktop package.
- **Linux desktop:** a graphical Linux `amd64` system. Ubuntu 24.04 amd64 is the native installation and sandbox qualification target; there is no Linux ARM64 desktop package.
- The newest compatible, published, non-draft Server release selected from the complete [GitHub Releases listing](https://github.com/furyheimdall/jastreamer/releases), including any entry correctly labelled as a preview, or a verified separately supplied offline artifact. Preview status and physical-device verification limits still apply.
- A trusted private LAN between the Server, browser/client, and outputs. Automatic discovery needs multicast.
- On Linux, separate config and data directories writable by container UID/GID `10001:10001`. For music, choose either an existing absolute host root readable by UID 10001 or the deliberate sample-only root described below; mount that root read-only.
- On Windows, either an existing absolute music root readable by the Server account or the deliberate adjacent sample-only `music` directory. Adjacent `server.json` and `data` must remain writable by that account.

Keep Server config, data, and music separate. Do not create a missing requested music root as a fallback, use `chmod 777`, or recursively change ownership or permissions on a music library. HTTP does not encrypt credentials or audio; use the Server's built-in HTTPS listener with your own PEM certificate and key when the LAN path is not fully trusted. Do not bypass certificate warnings or expose the Server directly to the public Internet.

<a id="releases"></a>
## Obtain and verify a release

### Public registry image

Inspect the complete [GitHub Releases listing](https://github.com/furyheimdall/jastreamer/releases), not only `/releases/latest`. Among published, non-draft **Server** releases, include prereleases in publication-time order and retain their Preview label; choose the most recently published entry that supports the host architecture and required package. If the newest entry is incompatible, state why and use the newest compatible one.

Read that release's limitations and verify the release provenance, source revision, package/image manifest, SHA-256 values, and architecture together. Sample setup below applies only when the selected published release actually inventories `samples/manifest.json`, three MP3 assets and the seeding helper; repository files that have not been published are not features of an older release.

The registry is `ghcr.io/furyheimdall/jastreamer-server`. Public images can be pulled without a GitHub token. Resolve and pin the complete immutable digest published for the compatible `linux/amd64` or `linux/arm64` image; never use a floating `latest` tag or a guessed tag:

```sh
export JASTREAMER_SERVER_IMAGE='ghcr.io/furyheimdall/jastreamer-server@sha256:<digest-from-release>'
docker pull "$JASTREAMER_SERVER_IMAGE"
docker image inspect --format '{{.Os}}/{{.Architecture}} {{.Id}}' "$JASTREAMER_SERVER_IMAGE"
```

Save the verified digest, not this temporary shell variable, in the deployment's persistent environment file. The complete Linux image includes its architecture-matched media runtime; do not install FFmpeg or Python separately on the host. Download Windows Server or desktop files, checksums, manifests and provenance only from the same selected release.

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

The supported Linux `amd64`/`arm64` image contains the Server, embedded Web interface, Python 3.12, pinned pyatv 0.18.0, FFmpeg, and `/usr/local/bin/jastreamer-airplay`. Releases that advertise onboarding samples also contain `/usr/share/jastreamer/samples/` and `/usr/share/jastreamer/seed-samples.py`. Use the repository's `deploy/docker/server/compose.synology.yaml` on either Linux or Synology. It applies host networking, a read-only container filesystem, a temporary `/tmp`, dropped capabilities, `no-new-privileges`, UID/GID `10001:10001`, separate writable config/data mounts, and a read-only `/music` mount.

For a new agent-assisted installation, the agent must obtain a music-path decision before preparing the saved Compose, environment and config files. A blank answer or “Skip” does not authorize an invented path:

1. **Existing music root:** ask the user for an existing absolute host directory. Verify it rather than creating a missing requested path, then create or reuse only its `jastreamer-samples` child.
2. **No music folder yet:** ask explicitly, “May I create and use `<your-home>/music` as your music folder?” Show the target user's resolved absolute path. Only after consent may the agent create that directory; if it already exists, inspect and confirm its reuse. Without consent, stop music setup rather than silently substituting `/data/music` or another directory.

For ordinary Linux, use the target user's `~/.config/jstreamer` as the default project, with separate `~/.config/jstreamer/config` and `~/.config/jstreamer/data` directories. Resolve the home on the target host and do not assume sudo or write access to `/srv`. Synology retains its approved persistent project/share paths, typically `/volume1/docker/jastreamer` with separate `config` and `data` children. Keep project/config/data outside the approved music root. Do not move an existing installation without separate migration approval.

For an existing root, save the user's actual absolute path as `JASTREAMER_MUSIC_PATH` and verify it with `test -d "$JASTREAMER_MUSIC_PATH"`; do not run `mkdir` to make a mistyped path pass. The following is only the ordinary-Linux **explicitly approved home-music creation** branch, run as the target user:

```sh
JASTREAMER_PROJECT_PATH="$HOME/.config/jstreamer"
JASTREAMER_CONFIG_PATH="$JASTREAMER_PROJECT_PATH/config"
JASTREAMER_DATA_PATH="$JASTREAMER_PROJECT_PATH/data"
JASTREAMER_MUSIC_PATH="$HOME/music"
mkdir -p -- "$JASTREAMER_PROJECT_PATH" "$JASTREAMER_CONFIG_PATH" "$JASTREAMER_DATA_PATH"
# Only after consent to this exact path, and only when it does not already exist:
mkdir -- "$JASTREAMER_MUSIC_PATH"
```

Home placement avoids system-directory writes but does not grant container UID/GID `10001:10001` access automatically. Verify the real Docker UID mapping, config/data write access and music read/traverse access. For missing access, obtain approval for a permission/ACL change scoped to the required directories and files; never assume sudo, silently change the container user, or recursively change home/music ownership or permissions. On Synology, preserve the share owner. Run the seeder as the approved music owner; if that account cannot create the sample child, resolve that narrowly scoped permission requirement first.

Before starting the Server, seed the verified CC0 1.0 Universal bundle. The helper requires an absolute destination with an existing parent, creates only `jastreamer-samples` at mode `0755`, and writes new payload files at mode `0644`. It verifies the manifest and asset hashes, copies the three MP3s together with `manifest.json` and `THIRD-PARTY-NOTICES.txt`, retains an existing byte-identical copy, and preflights all entries so any conflict stops without touching them. Keep the manifest and notice beside the tracks as their titles, authors, original source URLs, redistribution terms and hashes. Run the helper from the same pinned image as an unprivileged one-off container; the normal Server mount remains read-only:

```sh
docker run --rm --network none --read-only \
  --cap-drop ALL --security-opt no-new-privileges \
  --user "$(id -u):$(id -g)" \
  --mount "type=bind,source=$JASTREAMER_MUSIC_PATH,target=/seed-parent" \
  --entrypoint python3 "$JASTREAMER_SERVER_IMAGE" \
  /usr/share/jastreamer/seed-samples.py /seed-parent/jastreamer-samples
```

Use that command only after confirming the selected published image contains the documented helper and manifest. Inspect its result rather than deleting a conflicting user file. Seeding does not scan, queue, select an output, or start playback.
An interrupted write may leave a newly created partial file. Inspect and report that failure before retrying; do not delete or overwrite a conflicting destination to force setup to succeed. Destination symlinks are rejected so samples do not escape the approved directory.

During installation, show the exact host music path and explain: **an empty music folder has no tracks to play; put music in that folder, then run Settings → Scan now.** If the bundled samples were copied, identify the three test tracks separately from personal music. Additional music goes in the same approved root and requires another scan. Report an empty library honestly rather than claiming playback is ready.

For a new installation, copy `deploy/docker/server/compose.synology.yaml` into the persistent project directory, copy `packaging/server/server.json` from the source revision matching the release into the empty config directory, and write a project `.env` containing resolved values for `JASTREAMER_SERVER_IMAGE`, `JASTREAMER_CONFIG_PATH`, `JASTREAMER_DATA_PATH`, and `JASTREAMER_MUSIC_PATH`. An assisting agent fills the real approved values and saves these files; do not leave placeholders or depend on temporary `export` commands. Never replace an existing `server.json`.

Review the saved `server.json`: keep `data_dir` at `/var/lib/jastreamer`, the library root at `/music`, and packaged paths `/usr/local/bin/ffmpeg` and `/usr/local/bin/jastreamer-airplay`. Set `server_name`, HTTPS and optional outputs only as approved. `media.base_url` is the Server URL used by playback devices to fetch audio, not the Web UI bind or browser URL; leave it blank for automatic selection unless receivers require a specific reachable HTTP(S) origin. Keep `cast.enabled` false unless Google Cast is wanted.

The Web Settings page atomically replaces `server.json`, so both the file and config directory must remain writable by UID 10001. Validate and start from the saved project:

```sh
docker compose -f compose.yaml config
docker compose -f compose.yaml up -d
docker compose -f compose.yaml ps
docker compose -f compose.yaml logs --tail 100 jastreamer-server
curl --fail http://127.0.0.1:8080/healthz
```

In Synology Container Manager, import the same saved Compose file and `.env` values as a **Project**. The health URL is `http://<NAS-LAN-IP>:8080/healthz`, or port 8443 when built-in HTTPS is enabled. Verify the Web page from a private-LAN client, then continue with [first setup](INSTRUCTION.md#1-first-setup-and-everyday-use). A healthy container does not prove audible playback.

<a id="windows-server"></a>
## Native Windows x64 portable Server

Download the Windows x64 Server ZIP, its `.sha256`, manifest, and provenance/verification receipt from the selected release. Confirm that release is the newest compatible published Server release and retains any Preview label. The ZIP is unsigned and not production-qualified. Compare its bytes with the sidecar before extracting:

```powershell
$file = '.\jastreamer-server_<version>_windows-x64.zip'
$expected = (Get-Content "$file.sha256" -Raw).Split()[0].ToLowerInvariant()
$actual = (Get-FileHash $file -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actual -ne $expected) { throw 'Windows Server ZIP checksum mismatch' }
```

Extract the complete ZIP to a new writable local folder. If the verified package manifest inventories `samples\manifest.json` and the three MP3s, a normal first launch with no `server.json` creates adjacent `data` and `music\jastreamer-samples`, verifies and copies the bundled samples without replacing an existing music file, records absolute paths, validates the configuration, and starts the Server. It never replaces an existing `server.json` or existing music. A package that does not inventory these assets must not be described as containing them.

For agent-assisted setup, choose **an existing absolute Windows music root** or **Skip** before launch. Skip deliberately uses the adjacent `music\jastreamer-samples` sample-only library. For an existing root, the agent must verify that root, create only its `jastreamer-samples` child, verify each bundled sample against `samples\manifest.json`, copy only nonconflicting files, and save an actual adjacent `server.json` using that root; it must not create a missing root or ask the user to edit JSON. In either branch, keep adjacent config/data separate and run `start-server.cmd` as the ordinary account that will operate the Server. Do not run inside the ZIP, copy only the EXE, run as Administrator, or weaken SmartScreen or Defender globally. Keep the console open and use Ctrl+C to stop.

Open `http://127.0.0.1:18080` on that computer, or use the Windows computer's private LAN address and port 18080 from another client. If Windows Firewall prompts, allow the executable on private networks only; do not disable the firewall. TCP 18080 is needed for Web and media access, SSDP UDP 1900 for UPnP discovery, and mDNS UDP 5353 on the selected interfaces for optional Google Cast discovery. Cast also requires Server TCP access to the port each receiver advertises and receiver access to the Server URL used to fetch audio.

This package is the native Server, not the optional Windows desktop. It provides UPnP/DLNA and optional Google Cast network output and does not install a Renderer or play through local PC speakers. Google Cast needs no Chrome or Python helper. Native Windows Server does not support AirPlay, even if an arbitrary executable path is entered; the package also does not bundle FFmpeg. Transcoding and AirPlay therefore default to disabled. A separate Linux sender must use jastreamer's adapter source and dependencies from the same release as the installed Server and implement its matching protocol; `atvremote`, Python, pyatv alone, and receiver software such as Shairport Sync are not substitutes.

Before a portable update, inspect the actual launcher/configuration and resolve its `data_dir` and every music root, including external drives, UNC shares and symlink/junction targets. Do not assume they are the adjacent `data` and `music` folders. Record and preserve the actual paths and permissions. Stop if paths disagree or cannot be read; do not relocate or copy these directories as part of the update.

To update an existing portable installation, verify and extract the new ZIP to a separate temporary folder, then stop the Server with Ctrl+C. Replace only the package-owned payload in the existing installation folder, including program, launcher, template, bundled `samples`, notices, licenses, and build information. Do not replace `server.json` or delete, move or copy `data` or `music`. Adding samples to an upgrade is a separate, explicit opt-in step described below; first-launch seeding must not run against existing state. Confirm the unchanged configuration, URL, account, library, artwork, queue, playlists and sessions after restart, and retain the previous verified package identity.

<a id="desktop-windows"></a>
## Optional Windows x64 desktop

Use the unsigned `jastreamer-desktop_0.2.0_windows-x64.zip` from the selected preview on Windows 10/11 x64. Compare its hash with the release checksum:

```powershell
Get-FileHash .\jastreamer-desktop_0.2.0_windows-x64.zip -Algorithm SHA256
```

Extract the complete ZIP to a new writable local folder and run `jastreamer-desktop.exe`; do not run inside the ZIP or copy only the EXE. Select a discovered Server or enter its complete HTTP(S) root URL. The app verifies the Server before connecting, and connection or discovery does not start playback.

Server discovery searches each active IPv4 network adapter every five seconds, including when adapters change. Firewall and multicast restrictions can still require entering the Server URL manually.

Recent Servers, language, cookies, and sessions are stored beside the EXE in `user-data`. Closing the app does not stop Server playback. To upgrade, verify and extract the new ZIP to a separate temporary folder, exit the app completely, and replace only package-owned files in the existing installation folder. Leave `user-data` in place without copying or overwriting it. A different Windows account or PC may require login again.

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

Recent Servers, language, cookies, and sessions use `$XDG_CONFIG_HOME/jastreamer-desktop`, normally `~/.config/jastreamer-desktop`, not the root-owned installation directory. Exit completely before installing an updated DEB and leave this profile in place. If replacing a preview with the same package version, use `sudo apt install --reinstall ./jastreamer-desktop_0.2.0_linux-amd64.deb`. Keep the previous verified DEB for rollback; never remove the profile just to upgrade.

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

<a id="upgrade"></a>
## Upgrade

For the Linux container target, updating means replacing the Server container with a verified image, not running first-account setup again. The image includes the Web interface, FFmpeg, and the AirPlay runtime for its supported architecture. Do not mount the Docker socket into the Server or grant it host-management privileges to make it update itself. For native Windows, follow the [portable update procedure](#windows-server); do not apply the Compose steps below.

Use the **existing** Compose project name, project directory, Compose files, and environment file for every operation. Do not create a second installation with new default storage paths.

The existing config/data and music directories stay at their current paths and are reused through the same mounts. Do not copy or archive these directories during an image update.

1. **Review the target and actual storage.** Read the release's changes, configuration/database compatibility notes and any required intermediate versions. Reconcile the actual running container's `Mounts` and config argument/environment with the existing rendered Compose/environment and current config's `data_dir` and every library root. Record each config/data/music host source or named volume, container destination, resolved symlink target, ownership, permissions and read/write mode, plus the current image, architecture and service URL. Do not use fresh-install defaults for a replacement. Stop on mismatches, absent mounts or unreadable paths. Any required storage migration needs separate review and approval.
2. **Download before downtime.** Pull the exact target digest from the selected public release; no registry login is required. Use existing Docker credentials or private interactive authentication only for an explicitly chosen private registry. Do not paste tokens into chat or configuration files. The multi-platform image selects the host architecture automatically; confirm `amd64` or `arm64`. For an offline artifact, follow [Obtain and verify a release](#releases) to verify and import the correct platform. Do not use a floating `latest` tag, invent a registry address, or delete the old image.
3. **Agree on the interruption.** Stop playback and confirm it is stopped. Stop only `jastreamer-server` in the existing Compose project for the image replacement. Do not stop unrelated services, remove volumes, or use `down -v`.
4. **Change the saved image reference.** Set `JASTREAMER_SERVER_IMAGE` in the deployment's persistent settings to the verified target digest or imported local image ID. Preserve all other settings and mounts unless the release explicitly requires a reviewed migration. Do not replace `server.json` with a new-install template. Keep UID/GID `10001:10001`, host networking, read-only root filesystem and music, writable config and data, and the other Compose security restrictions. Render the proposed configuration with `docker compose config` and validate the existing configuration with the target image's `--check-config` command before starting it.
5. **Recreate only the Server.** Use the existing project with `up -d --no-deps jastreamer-server`. Confirm the running image matches the intended digest and platform, inspect container state and logs, and verify `/healthz` and the Web page from the client LAN. Do not declare success from container creation alone.
6. **Open and test.** Use the actual, previously used HTTP(S) URL including its port. Refresh the browser or desktop's hosted Web view. Check login, library and artwork, queue order, playlists, settings, and output discovery against the pre-update state. Playback must remain stopped until you explicitly start it. Play a chosen track, confirm audible sound, try pause and seek where supported, and stop playback. If a check fails, retain the failed step and exact error text without secrets.

### Optional sample addition after an upgrade

An upgrade preserves the current music, config and data and does not add samples automatically. After the upgraded Server is verified, an operator may explicitly approve adding the selected release's three samples. Keep playback stopped for the operation. On Linux/Synology, rerun the verified pinned image's seeding command above against `<existing-music-root>/jastreamer-samples`; do not change the persistent `/music` mount or `server.json`. On native Windows, verify the package's `samples\manifest.json` and copy only nonconflicting bundled sample files and notices into the approved existing root's `jastreamer-samples` child. In either case, retain identical files, stop on conflicts, preserve the root's owner and permissions, and do not overwrite state or source music. Adding files does not scan, queue, select an output or play them; scanning and playback remain explicit user actions.

The desktop does not need a package replacement just to display an updated Server-hosted Web interface, including the phone/PWA UI. If a release also updates the desktop executable, follow its separate [Windows](#desktop-windows) or [Linux](#desktop-linux) procedure and preserve the Windows adjacent `user-data` or Linux per-user profile.

For agent-assisted installation or updates, start at [AGENTS.md](AGENTS.md); its inspection, approval, data-security, verification, and reporting workflow applies before these manual platform steps.

<a id="rollback"></a>
## Rollback

If startup or verification fails, stop the new Server and preserve its logs and state. Before returning to the previous verified image, check that it supports the current configuration and database; changing only the image may not work after a database migration. If compatibility is unknown or a downgrade is unsupported, leave the Server stopped and report the required migration decision rather than resetting or rewriting data. For a compatible, approved rollback, change only the saved image reference and recreate the Server with the same settings and mounts. Recheck the original URL and preserved state without automatically restarting playback. Never delete or change the source music during an update or rollback.

For a native Windows Server, apply the same compatibility check, stop the Server, and replace only package-owned files with the previous verified package while leaving `server.json`, data and music in place. For a desktop-only rollback, replace only package-owned files and preserve the existing Windows `user-data` or Linux per-user profile. Return to the [user guide](INSTRUCTION.md) for operation and troubleshooting after the original version is running.
