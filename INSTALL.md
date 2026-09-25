# Install and update jastreamer

[README](README.md) · [User guide](INSTRUCTION.md) · [한국어 설치 안내](INSTALL.ko.md)

jastreamer is one Server plus optional clients. The Server owns the library, the shared queue, playback, and the Web interface; the desktop apps, the Android app, the iOS client, and the phone PWA are views of that Server on a trusted private LAN. Install the Server first, then add only the clients you need.

Releases are published on [GitHub Releases](https://github.com/furyheimdall/jastreamer/releases). A stable release is tagged `vX.Y.Z`, is marked as GitHub's **latest** release, and is the one to install; older `v0.2.0-preview.N` entries stay prereleases and are excluded from `/releases/latest`. The Windows Server ZIP and the Windows desktop ZIP are not Authenticode-signed, the Android APK is signed with the jastreamer Android release key, and iOS is source and CI only. Verify every download before you rely on it, and confirm operation on your own network and receivers.

| What you want to do | Follow this branch |
| --- | --- |
| Run a Server on Linux or Synology | [Linux Server](#linux-server) |
| Run a Server on Windows | [Windows Server](#windows-server) |
| Add a desktop client to an existing Server | [Windows ZIP](#desktop-windows) or [Linux DEB](#desktop-linux) |
| Add the native Android client | [Android APK](#android) |
| Develop or verify the native iOS client | [iOS source and CI](#ios), not device installation |
| Use a phone or install its home-screen app | [Phone PWA](#pwa) |
| Confirm an installation really works | [Verification checklist](#verify) |
| Update or recover an existing installation | [Upgrade](#upgrade) or [rollback](#rollback) |
| Find configuration, data, and logs | [File locations](#locations) |
| Remove a client or the Server | [Uninstall](#uninstall) |

For agent-assisted installation or updates, start at [AGENTS.md](AGENTS.md); its inspection, approval, data-security, verification, and reporting rules apply before any step below.

<a id="requirements"></a>
## Requirements and safety

| Component | Requirements | Package |
| --- | --- | --- |
| Linux or Synology Server | Linux `amd64`/`arm64` with Docker Engine and Compose v2, or Synology DSM with Container Manager. `arm/v7` is unsupported; DS918+ is `amd64` | `ghcr.io/furyheimdall/jastreamer-server` image |
| Windows Server | Windows x64 and a writable local folder, run by an ordinary account. Not installed as a Windows service | `jastreamer-server_0.2.0_windows-x64.zip` |
| Windows desktop | Windows 10/11 x64; no ARM64 package | `jastreamer-desktop_0.2.0_windows-x64.zip` |
| Linux desktop | Graphical Linux `amd64`; Ubuntu 24.04 amd64 is the qualification target; no ARM64 package | `jastreamer-desktop_0.2.0_linux-amd64.deb` |
| Android client | Android 10 (API 29) or newer, plus an Android System WebView that supports `MULTI_PROFILE` | `jastreamer-android_0.2.0_release.apk`, signed with the release key |
| iOS client | iOS/iPadOS 18.4 or newer; macOS with Xcode 16.4 and the iOS 18.5 simulator runtime for the pinned CI scenario | Source and CI only |
| Phone PWA | A phone browser and an HTTPS origin the phone trusts | None; served by the Server |

Server profiles, Server-controlled phone output, and new Android imports need the `MULTI_PROFILE` WebView feature: the OS version alone does not establish support, and the app refuses to fall back to a shared session. Android **Saved music** itself needs no Server profile.

Storage:

- **Linux:** separate config and data directories writable by container UID/GID `10001:10001`, plus a music root readable and traversable by UID 10001 that is mounted read-only.
- **Windows:** `server.json` and `data` beside the executable must be writable by the account running the Server, and its music root must be readable by that account.

Keep Server config, data, and music in separate directories, and never place the project or data inside the music root.

### Ports and network

| Purpose | Port | Notes |
| --- | --- | --- |
| Web UI and media, Linux image default | TCP 8080 | `http.address` in `server.json` |
| Web UI and media, Windows portable default | TCP 18080 | from the packaged `server.template.json` |
| Built-in HTTPS listener when enabled | TCP 8443 (Windows 18443) | `https.address`, with your own PEM certificate and key |
| UPnP/DLNA discovery and control | UDP 1900 to `239.255.255.250` | SSDP; multicast must be allowed |
| Client discovery (`_jastreamer._tcp`) and Google Cast discovery | UDP 5353 | mDNS on the selected interfaces |
| Google Cast control | TCP port advertised by each receiver | The receiver must also reach the Server URL used to fetch audio |

AirPlay is available only in the Linux image, through its packaged helper. Do not forward any of these ports from the Internet.

### Safety rules that apply everywhere

- Never create a missing music root as a fallback, use `chmod 777`, or recursively change ownership or permissions on a music library. Mount or configure music read-only where the platform allows it.
- HTTP encrypts neither credentials nor audio. When the LAN path is not fully trusted, enable the Server's built-in HTTPS listener with your own certificate and key. Never bypass certificate warnings, and never expose the Server directly to the public Internet.
- Keep passwords, private keys, registry tokens, and session cookies out of chat, Compose files, source control, and support logs. Use existing credential stores or private interactive prompts.
- Every update and rollback preserves the existing `server.json`, data directory, and music. Replace only package-owned files or the container image.
- Do not disable Defender, SmartScreen, the firewall, Play Protect, AppArmor, or the Chromium sandbox to make an installation succeed. Report the error and the observed permissions instead.
- A healthy container, a green CI run, or a connected client is not proof of audible playback. Confirm with a real track on a real output.

<a id="releases"></a>
## Obtain and verify a release

### Choose the release

Prefer the newest stable release: on the [GitHub Releases listing](https://github.com/furyheimdall/jastreamer/releases) it is the `vX.Y.Z` entry without a Preview label, and `/releases/latest` resolves to it. Choose a `vX.Y.Z-preview.N` prerelease only when you are deliberately testing that preview, and remember that previews are never marked latest. Whichever you pick, confirm the entry supports your host architecture and the package you need; if the newest one is incompatible, say why and use the newest compatible entry.

Verify the release provenance, source revision, package or image manifest, SHA-256 values, and architecture together, and download the Server image reference, Windows ZIPs, desktop packages, the Android APK, checksums, and manifests from that one release. `SHA256SUMS` covers every asset, and `release-provenance.json` records the release tag, channel, source revision, CI runs, and the Android signer certificate. Optional sample setup applies only when the selected release actually inventories `samples/manifest.json`, the three MP3 assets, and the seeding helper; repository files that were never published are not features of an older release.

### Public registry image

The registry is `ghcr.io/furyheimdall/jastreamer-server` and public images pull without a GitHub token. A stable release publishes the multi-architecture index as tag `0.2.0` together with per-architecture digests, and no mutable `latest` image tag exists. Pin the complete immutable digest the release lists for the `linux/amd64` or `linux/arm64` image rather than any tag:

```sh
export JASTREAMER_SERVER_IMAGE='ghcr.io/furyheimdall/jastreamer-server@sha256:<digest-from-release>'
docker pull "$JASTREAMER_SERVER_IMAGE"
docker image inspect --format '{{.Os}}/{{.Architecture}} {{.Id}}' "$JASTREAMER_SERVER_IMAGE"
```

Save the verified digest in the deployment's persistent environment file, not only in this shell variable. The Linux image already contains its architecture-matched media runtime, so do not install FFmpeg or Python on the host.

<a id="windows-unblock"></a>
### Windows downloads: SmartScreen and Mark-of-the-Web

Because the ZIPs are not Authenticode-signed, Windows marks them as downloaded from the Internet and SmartScreen may warn the first time you run the extracted program. Unblock the archive **before** extracting, so the mark is not copied onto every extracted file:

- In Explorer: right-click the ZIP → **Properties** → check **Unblock** → **OK**.
- Or in PowerShell: `Unblock-File -LiteralPath .\jastreamer-server_0.2.0_windows-x64.zip`

Only unblock a file whose SHA-256 you verified against the release. Do not disable SmartScreen, Defender, or the firewall, and do not run the packages as Administrator.

### Supplied offline artifacts

If you were separately given an offline Server bundle, verify its supplied checksum list before import:

```sh
cd /path/to/supplied/release
sha256sum -c SHA256SUMS
```

An explicitly approved private registry remains an alternative; use its exact digest and private credential handling.

A supplied multi-platform `.oci` cannot be passed to `docker load` directly. When an offline Docker TAR is needed, convert only the target architecture on a Linux machine with Skopeo, then compare the checksum after transfer and use the printed local image ID as `JASTREAMER_SERVER_IMAGE`:

```sh
ARCH=amd64  # or arm64
skopeo copy --override-os linux --override-arch "$ARCH" \
  oci-archive:jastreamer-server_0.2.0_linux_amd64-arm64.oci \
  docker-archive:jastreamer-server_0.2.0_linux_${ARCH}.tar:jastreamer-server:0.2.0
sha256sum jastreamer-server_0.2.0_linux_${ARCH}.tar
docker load --input jastreamer-server_0.2.0_linux_${ARCH}.tar
docker image inspect --format '{{.Id}}' jastreamer-server:0.2.0
```

<a id="linux-server"></a>
## Linux or Synology Server

The `amd64`/`arm64` image contains the Server, the embedded Web interface, Python 3.12 with pinned pyatv 0.18.0, FFmpeg, and `/usr/local/bin/jastreamer-airplay`. Releases that advertise onboarding samples also contain `/usr/share/jastreamer/samples/` and `/usr/share/jastreamer/seed-samples.py`.

Use `deploy/docker/server/compose.synology.yaml` from the source revision matching the release on both Linux and Synology. It applies host networking, a read-only container filesystem, a 64 MiB `/tmp`, dropped capabilities, `no-new-privileges`, UID/GID `10001:10001`, separate writable config and data mounts, and a read-only `/music` mount.

### 1. Decide the music root

A blank answer or “Skip” does not authorize an invented path, and an assisting agent must settle this before writing any file:

1. **Existing music root:** use an existing absolute host directory. Verify it with `test -d "$JASTREAMER_MUSIC_PATH"` instead of running `mkdir` to make a mistyped path pass, then create or reuse only its `jastreamer-samples` child.
2. **No music folder yet:** ask explicitly, “May I create and use `<your-home>/music` as your music folder?”, showing the resolved absolute path. Only after consent may that directory be created; if it already exists, inspect and confirm its reuse. Without consent, stop music setup rather than substituting `/data/music` or any other directory.

### 2. Prepare the project directories

On ordinary Linux the default project is the target user's `~/.config/jstreamer`, with separate `config` and `data` children; resolve the home on the target host and do not assume sudo or write access to `/srv`. On Synology, keep the approved persistent share paths, typically `/volume1/docker/jastreamer` with `config` and `data` children. Do not move an existing installation without separate migration approval.

```sh
JASTREAMER_PROJECT_PATH="$HOME/.config/jstreamer"
JASTREAMER_CONFIG_PATH="$JASTREAMER_PROJECT_PATH/config"
JASTREAMER_DATA_PATH="$JASTREAMER_PROJECT_PATH/data"
JASTREAMER_MUSIC_PATH="$HOME/music"   # or the verified existing root
mkdir -p -- "$JASTREAMER_PROJECT_PATH" "$JASTREAMER_CONFIG_PATH" "$JASTREAMER_DATA_PATH"
# Only after consent to this exact path, and only when it does not already exist:
mkdir -- "$JASTREAMER_MUSIC_PATH"
```

Home placement avoids system-directory writes but does not grant UID/GID `10001:10001` access automatically. Verify the real Docker UID mapping, config/data write access, and music read/traverse access. If access is missing, obtain approval for a permission or ACL change scoped to exactly the required directories; never assume sudo, silently change the container user, or recursively change home or music ownership. On Synology, preserve the share owner.

### 3. Seed the optional sample tracks

Skip this step if you already have music. The helper requires an absolute destination with an existing parent, creates only `jastreamer-samples` at mode `0755`, writes payload files at mode `0644`, verifies the manifest and asset hashes, copies the three CC0 1.0 Universal MP3s with `manifest.json` and `THIRD-PARTY-NOTICES.txt`, keeps an existing byte-identical copy, rejects destination symlinks, and preflights every entry so a conflict stops the run untouched. Keep the manifest and notice beside the tracks: they carry the titles, authors, source URLs, redistribution terms, and hashes.

Run it as the approved music owner, from the same pinned image, as an unprivileged one-off container:

```sh
docker run --rm --network none --read-only \
  --cap-drop ALL --security-opt no-new-privileges \
  --user "$(id -u):$(id -g)" \
  --mount "type=bind,source=$JASTREAMER_MUSIC_PATH,target=/seed-parent" \
  --entrypoint python3 "$JASTREAMER_SERVER_IMAGE" \
  /usr/share/jastreamer/seed-samples.py /seed-parent/jastreamer-samples
```

Seeding never scans, queues, selects an output, or starts playback. Inspect a reported conflict instead of deleting or overwriting the destination; an interrupted write can leave a newly created partial file, which you should report rather than retry blindly.

### 4. Save the project files

Copy `compose.synology.yaml` into the persistent project directory as `compose.yaml`, copy `packaging/server/server.json` from the source revision matching the release into the empty config directory, and write a project `.env` with resolved values for `JASTREAMER_SERVER_IMAGE`, `JASTREAMER_CONFIG_PATH`, `JASTREAMER_DATA_PATH`, and `JASTREAMER_MUSIC_PATH`. Save real values, not placeholders or temporary `export` commands, and never replace an existing `server.json`.

Review the saved `server.json`: keep `data_dir` at `/var/lib/jastreamer`, the library root at `/music`, and the packaged paths `/usr/local/bin/ffmpeg` and `/usr/local/bin/jastreamer-airplay`. Set `server_name`, HTTPS, and optional outputs only as approved. `media.base_url` is the Server URL playback devices use to fetch audio, not the Web UI address; leave it blank for automatic selection unless a receiver needs a specific reachable HTTP(S) origin. Keep `cast.enabled` false unless you want Google Cast. The Web **Settings** page rewrites `server.json` atomically, so both the file and its directory must stay writable by UID 10001.

### 5. Start and check

```sh
docker compose -f compose.yaml config
docker compose -f compose.yaml up -d
docker compose -f compose.yaml ps
docker compose -f compose.yaml logs --tail 100 jastreamer-server
curl --fail http://127.0.0.1:8080/healthz
```

In Synology Container Manager, import the same saved Compose file and `.env` values as a **Project**. The health URL is then `http://<NAS-LAN-IP>:8080/healthz`, or port 8443 with the built-in HTTPS listener enabled.

An empty music folder has nothing to play: put music in the approved root, then run **Settings → Library → Scan now**; added music always needs another scan. If the bundled samples were copied, name them separately from personal music, and report an empty library honestly instead of claiming playback is ready. Continue with the [verification checklist](#verify) and [first setup](INSTRUCTION.md#1-first-setup-and-everyday-use).

<a id="windows-server"></a>
## Native Windows x64 portable Server

Download the Windows Server ZIP, its `.sha256`, the manifest, and the verification receipt from the selected release, then [unblock the ZIP](#windows-unblock) and compare its bytes before extracting:

```powershell
$file = '.\jastreamer-server_0.2.0_windows-x64.zip'
$expected = (Get-Content "$file.sha256" -Raw).Split()[0].ToLowerInvariant()
$actual = (Get-FileHash $file -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actual -ne $expected) { throw 'Windows Server ZIP checksum mismatch' }
```

Extract the complete `jastreamer-server-windows-x64` folder to a new writable local location and run `start-server.cmd` as the ordinary account that will operate the Server. Do not run inside the ZIP, copy only the EXE, or run as Administrator. The launcher validates the configuration, starts the Server in its console window, and makes no permanent system or policy change; keep the window open and press Ctrl+C to stop.

When `server.json` is absent, the first launch creates adjacent `data` and `music\jastreamer-samples`, verifies the packaged samples against `samples\manifest.json`, copies only nonconflicting files, records absolute paths, and starts. It never replaces an existing `server.json` or an existing music file. A package that does not inventory those sample assets must not be described as containing them.

For assisted setup, choose **an existing absolute Windows music root** or **Skip** before the first launch. Skip deliberately uses the adjacent sample-only library. For an existing root, verify that root, create only its `jastreamer-samples` child, copy only nonconflicting verified samples, and save a real adjacent `server.json` using that root; never create a missing root or ask the user to edit JSON by hand.

Open `http://127.0.0.1:18080` on that computer, or the computer's private LAN address on port 18080 from another client, and create the first administrator account. If Windows Firewall prompts, allow the executable on private networks only. See [Ports and network](#requirements) for the ports UPnP/DLNA and Google Cast need.

This package is the Server, not the [Windows desktop client](#desktop-windows). It provides UPnP/DLNA and optional Google Cast output but does not open local PC speakers; a connected browser can select **This device** instead. Google Cast needs no Chrome or Python helper and is enabled in **Settings**, saved, and applied after a restart. The package bundles no FFmpeg and no AirPlay helper, so transcoding and AirPlay stay disabled; entering an arbitrary executable path does not add AirPlay on Windows. A separate Linux sender must use jastreamer's adapter source and dependencies from the same release and implement its matching protocol — `atvremote`, Python, pyatv alone, and receiver software such as Shairport Sync are not substitutes.

<a id="windows-server-update"></a>
### Update a portable Windows Server

1. Inspect the actual launcher and `server.json` first and resolve `data_dir` and every library root, including external drives, UNC shares, and junction or symlink targets. Do not assume they are the adjacent `data` and `music` folders. Record the real paths and permissions and stop if they disagree or cannot be read; storage migration needs separate approval.
2. Verify, unblock, and extract the new ZIP into a separate temporary folder, and check the release's configuration and database compatibility.
3. Stop the running Server with Ctrl+C and wait for its console to close.
4. In the existing installation folder, replace only package-owned files: the executable, launcher, first-install script, template, guidance texts, licenses, notices, and the packaged `samples` directory. Keep `server.json`, `data`, `music`, and any user files, and do not copy, archive, or relocate them. The packaged `samples` directory is not the music library.
5. Run `start-server.cmd` again. Because `server.json` exists, the launcher validates it and starts without writing it, and no sample seeding runs. Confirm the unchanged URL, account, library, artwork, queue, playlists, and sessions, and keep the previous verified package for [rollback](#rollback).

Adding samples to an existing installation is a separate opt-in step described under [Optional sample addition](#samples-after-upgrade).

<a id="desktop-windows"></a>
## Optional Windows x64 desktop

The desktop app is a connection shell for an existing Server on Windows 10/11 x64. [Unblock](#windows-unblock) `jastreamer-desktop_0.2.0_windows-x64.zip`, compare it with the release checksum, then extract the complete `jastreamer-desktop` folder to a new writable local location and run `jastreamer-desktop.exe`. Do not run inside the ZIP or copy only the EXE.

```powershell
Get-FileHash .\jastreamer-desktop_0.2.0_windows-x64.zip -Algorithm SHA256
```

The Server screen separates discovered and recent Servers; pick a card's connection action, or use **Connect by address** and enter a host name or complete HTTP(S) root URL. The app verifies the Server before connecting, and neither discovery nor connection starts playback. Discovery queries every active IPv4 adapter every five seconds and follows adapter changes, but firewall or multicast restrictions can still force manual address entry.

Optional Windows native (WASAPI) output needs both a desktop package that contains `resources\native-audio\jastreamer-audio.exe` with its bundled FFmpeg DLLs and a compatible Server-hosted Web UI; updating the Web page alone cannot add the helper, and older packages stay browser-only. Native output is opt-in, starts in Shared mode on the system default endpoint, and is configured in the Web UI — see [Windows audio](INSTRUCTION.md#windows-audio). Keep the packaged helper and DLLs as shipped rather than substituting codec DLLs, and do not claim that a build or CI result verifies audible output or DAC-level bit-perfect delivery.

Recent Servers, the language choice, cookies, sessions, and the saved native-audio preferences live in `user-data` beside the EXE, so the folder must stay writable. Closing the window with X hides the app in the notification tray while its connection and local playback continue; click the tray icon to reopen, or right-click it and choose **Exit** to quit completely. Exiting does not send Stop to other network outputs.

To upgrade, verify and extract the new ZIP to a separate temporary folder, quit through the tray, and replace only package-owned files in the existing folder. Leave `user-data` untouched; a different Windows account or PC may require signing in again.

<a id="desktop-linux"></a>
## Optional Linux amd64 desktop

Use `jastreamer-desktop_0.2.0_linux-amd64.deb` on a graphical Linux amd64 system. Verify it against the release checksum and install through APT so dependencies resolve:

```sh
sha256sum -c jastreamer-desktop_0.2.0_linux-amd64.deb.sha256
sudo apt install ./jastreamer-desktop_0.2.0_linux-amd64.deb
```

Launch **JASTREAMER** from the application menu as your ordinary user, or run `/usr/lib/jastreamer-desktop/jastreamer-desktop`. Never launch it with `sudo` or add `--no-sandbox`. Select a discovered Server or enter its complete HTTP(S) URL; this client needs no separate FFmpeg or audio player, and it has no Windows-style tray behaviour or native WASAPI output.

The package keeps application files root-owned, installs `chrome-sandbox` as `root:root` mode `4755`, and on compatible AppArmor systems installs an executable-specific user-namespace profile for `/usr/lib/jastreamer-desktop/jastreamer-desktop`. It disables neither AppArmor nor the system-wide user-namespace restriction and preserves unmanaged policy; put local additions in `/etc/apparmor.d/local/jastreamer-desktop`. If launching fails, report the error and the installed permissions instead of weakening the sandbox.

Recent Servers, language, cookies, and sessions live in `$XDG_CONFIG_HOME/jastreamer-desktop` (normally `~/.config/jastreamer-desktop`), not in the root-owned installation directory. Exit the app before installing an updated DEB and leave that profile in place. To reinstall the same package version, use `sudo apt install --reinstall ./jastreamer-desktop_0.2.0_linux-amd64.deb`. Keep the previous verified DEB for rollback.

<a id="android"></a>
## Optional native Android client

The Kotlin app discovers `_jastreamer._tcp` Servers, checks `/api/v1/discovery`, and opens the selected Server's Web UI. Its **Local playback** entry opens an independent **Saved music** library with its own downloads, playlists, folders, device queue, and Media3 playback of app-owned files, which needs neither Server access nor sign-in. In Server mode the same Media3 service provides **This device** output while the Server owns the queue, and the Android system media controls report their commands back to it. No PWA installation, broad local-music permission, or location permission is required.

The stable release attaches a signed APK built by CI from the same commit as the Server:

| Asset | What it is |
| --- | --- |
| `jastreamer-android_0.2.0_release.apk` | The installable app, signed with the jastreamer Android release key using the v2 and v3 signature schemes. Application ID `io.jastreamer.android`, version name 0.2.0, version code 20000 |
| `jastreamer-android_0.2.0_release.apk.sha256` | Checksum sidecar for the exact published bytes; `SHA256SUMS` repeats the same value for every asset |
| `jastreamer-android_0.2.0_release.manifest.json` | Receipt recording the source revision, application ID, SDK range, signature schemes, signer certificate SHA-256, and the unsigned CI APK that was signed |

### Verify and install the APK

1. Download the APK, its `.sha256` sidecar, and `SHA256SUMS` from the release.
2. Compare the downloaded bytes and the signer certificate with the two commands below. `apksigner` ships with the Android SDK build-tools; on Windows use `Get-FileHash .\jastreamer-android_0.2.0_release.apk -Algorithm SHA256` for the checksum.
3. Transfer the verified APK to the phone and open it. Grant **Install unknown apps** to the app you opened it with only when Android asks, and revoke that permission afterwards. With an already-authorized ADB connection you can run `adb install jastreamer-android_0.2.0_release.apk` instead.
4. Play Protect may warn that the app did not come from Google Play. That warning describes the distribution channel, not a detected problem: continue only if step 2 matched, and never disable Play Protect, certificate checks, or device security.

```sh
sha256sum -c jastreamer-android_0.2.0_release.apk.sha256
apksigner verify --print-certs jastreamer-android_0.2.0_release.apk
```

The reported `Signer #1 certificate SHA-256 digest` must be `53285c2c239aff2927ebe6f5c6aebb82fdbb50956ed84b1e9f0222b2d925943e`. Some tools print the same fingerprint in uppercase with colons (`53:28:5C:2C:…:25:94:3E`); compare it with the value shown on the release page. If it differs, stop — a different key means a different app, not an update.

A later release signed with the same key installs over the existing app and keeps its data, because an in-place update needs the same application ID and signer plus a nondecreasing version code. Never uninstall the app or clear its storage to force an update; both erase the app-owned music and state described below. Google has announced developer-verification requirements for apps installed outside Google Play, rolling out country by country, so check the release page for the current distribution status if that already applies in your region.

### Development CI builds

CI artifacts stay development and testing material. A successful [Android CI run](https://github.com/furyheimdall/jastreamer/actions/workflows/android.yml) produces `jastreamer-android-debug-test-signed-and-release-unsigned-<revision>` for its tested source revision, and a pull-request artifact is not a protected-main release.

| Artifact | Status |
| --- | --- |
| `*_debug-test-signed.apk` | Development testing only. Application ID `io.jastreamer.android.debug` makes it a separate app: it cannot update the released app and shares none of its saved music, profiles, or preferences. Its debug certificate can change between CI runs |
| `*_release-unsigned.apk` | Not installable as published. The release workflow signs exactly this file with the release key and records both digests in the release manifest |

Feature coupling between components:

- New imports require an APK **and** a Server/Web UI that advertise the platform-neutral v1 download capability; folder imports additionally require a Server build that implements folder targets. Updating only the APK or only the hosted Web page cannot add them.
- An older or unavailable Server must not block the app: already saved music stays usable, and a Server update is its own authorized procedure.

Networking: use HTTP only on a trusted private LAN, and let HTTPS validate against the device's normal trust store — if a certificate covers only a hostname, enter that hostname rather than bypassing the mismatch. The app targets API 36 and requests neither location nor the target-37 `ACCESS_LOCAL_NETWORK` permission; Android 17 grants legacy-target LAN access implicitly, while revoked or blocked network access fails visibly. Wi-Fi isolation, blocked multicast, and VPN routing can prevent discovery, so manual address entry remains available. Do not change device compatibility flags or network permissions to make a test pass.

A same-ID, same-signer in-place update preserves app-private saved audio, copied artwork, local playlists, saved genre metadata and device likes, the device queue and position, folders, download and language preferences, recent Servers, and isolated Server profiles. Its local metadata database uses schema version 2 for genres and likes; opening a version-1 library adds those columns without recreating tables, and downgrading that database is unsupported. **Remove from recent servers** removes only the shortcut — it neither signs out nor deletes the profile or saved music; sign out inside the Server UI to end that session and stop its unfinished imports, which still leaves completed device-owned music untouched. Uninstalling or using Android **Clear storage/data** erases the whole app container, which is excluded from cloud and device-transfer backup.

For source development, use the pinned Gradle Wrapper in `apps/android` with JDK 17, SDK platform 36, and Build Tools 35.0.0, then run `./gradlew :app:testDebugUnitTest :app:lint :app:assembleDebug :app:assembleRelease`. Full instrumentation uses an isolated API 36 emulator with the real Server/Web fixture from `.github/workflows/android.yml`, never an installed user's Server; emulator success establishes neither physical installation and networking nor Bluetooth/headset behaviour or audible playback.

See [Android controls](INSTRUCTION.md#android-controls) for Server mode, saved music, downloads, playback handoff, and lifecycle behaviour.

<a id="ios"></a>
## Native iOS source and CI

The SwiftUI/WKWebView client in `apps/ios` discovers `_jastreamer._tcp` Servers, verifies `/api/v1/discovery`, and reuses the selected Server's Web UI. It requires **iOS/iPadOS 18.4**, including the public WebKit API used to deny native file-picker requests, and is controller-only: its WebView blocks media loads, so there is no native audio engine, saved-music player, or Local playback entry.

**Current scope is source, unsigned device builds, and simulator CI only.** Successful [iOS CI runs](https://github.com/furyheimdall/jastreamer/actions/workflows/ios.yml) provide `jastreamer-ios-development-unsigned-and-simulator-<revision>`; check `SHA256SUMS`, `provenance.json`, the source revision, bundle identifier, and platform before using one. A PR artifact records its tested merge revision and is not a protected-main release.

- `*_device-development-unsigned-not-installable.zip` contains an unsigned `.app`, not an IPA or an installable iPhone/iPad package.
- `*_simulator-development-test-adhoc.zip` uses Xcode's ad-hoc test signature and a compatible simulator architecture; it cannot be installed on a phone or tablet.
- This CI establishes no Apple signing credentials, provisioning profiles, device installation, TestFlight, App Store publication, or production update channel.

On an approved macOS toolchain, open `apps/ios/Jastreamer.xcodeproj` and use the shared **Jastreamer** scheme. The reproducible isolated iPhone 16 scenario runs from the repository root:

```sh
export DEVELOPER_DIR=/Applications/Xcode_16.4.app/Contents/Developer
make build
bash tooling/qa/ios-simulator-ci.sh
```

The [workflow](.github/workflows/ios.yml) also builds for generic iPhone/iPad with signing disabled and packages only after unit and real-Web-UI tests pass, always against disposable fixtures rather than an installed user's Server. It establishes no physical installation, Wi-Fi/Bonjour behaviour, or audible output.

On a separately authorized device build, discovery needs **Local Network** access and suitable Wi-Fi/multicast routing. Private-LAN HTTP is unencrypted, and iOS local-network ATS exceptions cover local names and IP addresses, not arbitrary HTTP domain names; use a trusted HTTPS hostname where appropriate, since certificate validation cannot be bypassed. Preserve the app container and isolated profiles during an authorized update instead of uninstalling or clearing data.

See [iOS controls](INSTRUCTION.md#ios-controls) for selection, sessions, keyboard navigation, and lifecycle behaviour.

<a id="pwa"></a>
## Optional phone PWA

The installable phone app is another view of the Server-hosted interface. It needs network access to that Server and provides no service worker, cached library, or offline playback, and the desktop apps do not need it.

1. Open the Server's complete URL in the phone browser and sign in.
2. Use the browser's own **Install app** or **Add to Home Screen** menu and approve its prompt. On iPhone Safari, use **Share → Add to Home Screen**.
3. Launch the saved shortcut. iPad uses the tablet/desktop layout rather than the phone layout, but Safari can still offer Add to Home Screen.

Installation requires an HTTPS origin that the phone and browser trust; plain HTTP is accepted only for local development on `localhost`, `127.0.0.1`, or `::1`. Configure the certificate, private key, and HTTPS listener in **Settings**, save, restart when asked, and reopen the trusted HTTPS URL. Do not bypass certificate warnings, register an untrusted certificate as a workaround, expose the NAS publicly, or put a naive TLS-terminating proxy in front of the HTTP listener — the Server checks mutation request origins against its own listener scheme.

The phone layout and its controls work in the browser without installation, so on a trusted private-LAN HTTP deployment just refresh the Server page. Changing scheme, hostname, or port creates a different browser origin and may require signing in again. There is no installation status card in **Settings**; existing shortcuts keep working.

See [Phone controls](INSTRUCTION.md#phone-controls) for the phone layout and its playback controls.

<a id="verify"></a>
## Verification checklist

Run these after a new installation, an upgrade, or a rollback. Record the image digest or package SHA-256 you verified together with the result.

1. `curl --fail http://<server>:<port>/healthz` succeeds from the Server host and from another LAN client.
2. The Web UI opens at the real URL and port you will use every day, and sign-in works (first setup creates the administrator account).
3. **Settings → Library** shows the intended music root, and **Scan now** completes with the expected track count and artwork.
4. An output is available — a discovered renderer, or **This device** in the browser or desktop app.
5. One chosen track plays audibly on that output, and pause, seek, and stop behave.
6. After an upgrade, the queue, current track, playlists, likes, play counts, settings, and sessions match the pre-update state, and playback stays stopped until you start it.
7. Restarting the Server keeps the configuration, library, accounts, and stopped playback state.

If a step fails, keep the failed step and the exact error text (without secrets), check `logs/server.log` in the data directory, and look the symptom up in [Troubleshooting](INSTRUCTION.md#troubleshooting) before changing anything else.

<a id="locations"></a>
## File locations

| What | Linux container | Windows portable |
| --- | --- | --- |
| Configuration | `/etc/jastreamer/server.json` (host `${JASTREAMER_CONFIG_PATH}`) | `server.json` beside `jastreamer-server.exe` |
| Database: accounts, sessions, library, playlists, likes, queue, current track, play counts, download jobs, diagnostics | `/var/lib/jastreamer/server.sqlite` | `data\server.sqlite` |
| Artwork cache | `/var/lib/jastreamer/artwork` | `data\artwork` |
| Prepared downloads | `/var/lib/jastreamer/downloads` | `data\downloads` |
| AirPlay identity and credentials | `/var/lib/jastreamer/airplay` | Not applicable |
| Diagnostic logs | `/var/lib/jastreamer/logs/server.log` plus up to three rotated copies, 5 MiB each | `data\logs\server.log`, same rotation |
| Music | `/music`, mounted read-only | The configured library root |

Client-side state: the Windows desktop keeps recent Servers, language, sessions, and native-audio preferences in `user-data` beside its EXE; the Linux desktop uses `~/.config/jastreamer-desktop`; the Android app keeps saved music and profiles in app-private storage. Back up the Server data directory and `server.json` together, and read a log before attaching it to a report so that no address or account detail leaves your network unintentionally.

<a id="upgrade"></a>
## Upgrade

Updating the Linux Server means replacing the container with a verified image, not running first-account setup again. The image carries the Web interface, FFmpeg, and the AirPlay runtime for its architecture. Never mount the Docker socket into the Server or grant it host-management privileges so it can update itself. For the portable Windows Server, follow [Update a portable Windows Server](#windows-server-update) instead of the Compose steps here.

### Schema changes applied at startup

Review and approve these before deploying a newer Server. Each runs inside the existing database, preserves accounts, library, configuration, and music, and never autoplays or rescans.

| Change | Effect | Downgrade |
| --- | --- | --- |
| `download_jobs.kind` extended for folder jobs | Transactional migration preserving existing jobs, child rows, indexes, foreign keys, and artifact references | Older builds do not implement folder jobs; an image-only downgrade is not compatible |
| `player_current` added | Initialized transactionally from the selected queue entry; keeps the loaded track and traversal position even after that entry leaves the queue | Older Servers cannot interpret a loaded track outside their queue; an image-only downgrade is not compatible |
| `player_mode` and `player_shuffle` added | Preserve shuffle and repeat settings and entry-based traversal | Older Servers ignore them and reconcile queue changes when you return |
| `track_play_counts` added | Counts start at zero with no backfill of historical listening | Older builds leave the table unused and record nothing |
| `error_history` and library verification tables added | Retain bounded renderer-error and audio-integrity history | Older builds leave them unused |

Never reset data, delete jobs, or reinsert removed queue entries to force a downgrade. After a coordinated Server and Web update, refresh open Control pages: clearing the queue now removes every entry rather than only upcoming ones.

### Upgrade procedure

Use the **existing** Compose project name, project directory, Compose files, and environment file throughout, and never create a second installation with new default storage paths. Config, data, and music stay at their current paths and are reused through the same mounts; do not copy or archive them for an image update.

1. **Review the target and the actual storage.** Read the release's changes and compatibility notes, including any required intermediate version. Reconcile the running container's `Mounts` and config arguments with the rendered Compose/environment and the current config's `data_dir` and library roots. Record each host source or named volume, container destination, resolved symlink target, ownership, permissions, read/write mode, plus the current image, architecture, and service URL. Stop on mismatches, absent mounts, or unreadable paths; storage migration needs separate approval.
2. **Download before downtime.** Pull the exact target digest from the selected release; no login is needed for the public registry, and only an explicitly chosen private registry warrants interactive authentication. Confirm `amd64` or `arm64`, or import the correct platform from an [offline artifact](#releases). Do not use a floating tag, invent a registry address, or delete the old image.
3. **Agree on the interruption.** Stop playback and confirm it is stopped. Stop only `jastreamer-server` in the existing project; never stop unrelated services, remove volumes, or use `down -v`.
4. **Change the saved image reference.** Set `JASTREAMER_SERVER_IMAGE` to the verified digest or imported local image ID and preserve every other setting and mount, including UID/GID `10001:10001`, host networking, the read-only root filesystem and music mount, and the writable config and data mounts. Do not replace `server.json` with a new-install template. Render the result with `docker compose config` and validate the existing configuration with the target image's `--check-config` before starting it.
5. **Recreate only the Server.** Run `up -d --no-deps jastreamer-server` in the existing project, confirm the running image matches the intended digest and platform, and inspect container state and logs.
6. **Test.** Work through the [verification checklist](#verify) using the previously used URL and port, refreshing the browser or the desktop's hosted view. Container creation alone is not success.

<a id="samples-after-upgrade"></a>
### Optional sample addition after an upgrade

An upgrade preserves the current music, config, and data and adds no samples. Once the upgraded Server is verified, an operator may explicitly approve adding the release's three samples with playback stopped. On Linux or Synology, rerun the pinned image's seeding command against `<existing-music-root>/jastreamer-samples` without changing the `/music` mount or `server.json`. On Windows, verify `samples\manifest.json` and copy only nonconflicting sample files and notices into the approved root's `jastreamer-samples` child. In both cases retain identical files, stop on conflicts, preserve the root's owner and permissions, and remember that adding files neither scans nor queues nor plays them.

### Desktop and client updates

The desktop apps need no package replacement to show an updated Server-hosted Web interface, including the phone and PWA layouts. When a release also updates the desktop executable, follow the [Windows](#desktop-windows) or [Linux](#desktop-linux) procedure and preserve the Windows `user-data` folder or the Linux per-user profile. A newer Android APK signed with the same release key installs over the existing app and keeps its data; iOS updates follow its own section.

<a id="rollback"></a>
## Rollback

If startup or verification fails, stop the new Server and preserve its logs and state. Before returning to the previous verified image, check that it supports the current configuration and database — see the schema table above, because changing only the image may not work after a migration. If compatibility is unknown or the downgrade is unsupported, leave the Server stopped and report the required migration decision rather than resetting or rewriting data.

For an approved, compatible rollback, change only the saved image reference and recreate the Server with the same settings and mounts, then recheck the original URL and the preserved state without automatically restarting playback. For a portable Windows Server, apply the same compatibility check, stop the Server, and restore only package-owned files from the previous verified package while leaving `server.json`, `data`, and music in place. For a desktop-only rollback, replace only package-owned files and keep the Windows `user-data` folder or the Linux per-user profile.

Never delete or change the source music during an update or rollback. Once the original version runs, return to the [user guide](INSTRUCTION.md).

<a id="uninstall"></a>
## Uninstall

Removing software never requires deleting your music. Copy anything you want to keep before a step that erases app-owned storage.

| Target | Steps |
| --- | --- |
| Linux or Synology Server | Stop the project with `docker compose -f compose.yaml down` (never `-v`), then `docker image rm` the pinned digest. The host config, data, and music directories remain; delete `config` and `data` only if you accept losing accounts, library state, queue, playlists, likes, and play counts |
| Windows Server | Press Ctrl+C in the console. Move or back up `data`, and any music you keep inside the installation folder, before deleting the folder. Remove the Windows Firewall rule you allowed for `jastreamer-server.exe` |
| Windows desktop | Quit through the tray (**Exit**), then delete the extracted folder. This also deletes `user-data`, so saved sessions and native-audio preferences are lost |
| Linux desktop | `sudo apt remove jastreamer-desktop`, which also removes the AppArmor profile the package installed. Delete `~/.config/jastreamer-desktop` separately if you want its saved sessions gone |
| Android | Uninstall from Android. This erases the entire app container, including saved music, artwork, local playlists, the device queue, folders, preferences, and Server profiles, none of which are in cloud backup. The released app (`io.jastreamer.android`) and a development CI build (`io.jastreamer.android.debug`) are separate apps and must be removed separately |
| Phone PWA | Delete the home-screen shortcut; sign out in the Server UI first if you want the session ended |
