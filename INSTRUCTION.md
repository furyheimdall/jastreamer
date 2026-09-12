# jastreamer 0.2 user guide

[한국어 사용자 안내서](INSTRUCTION.ko.md) · [Project overview](README.md)

jastreamer runs one Linux Server container that hosts the Web interface and sends music to UPnP/DLNA or AirPlay outputs. The optional Windows app only connects to that Server.

## 1. Requirements and safety

- Linux `amd64` or `arm64` with Docker Engine and Compose v2, or Synology DSM with Container Manager. `arm/v7` is not supported; DS918+ is `amd64`.
- A verified private jastreamer 0.2 image digest or supplied release artifact. There is no public image or download.
- A trusted private LAN between Server, browser/client, and outputs. Automatic discovery needs multicast.
- Separate config and data directories writable by container UID/GID `10001:10001`.
- An existing music directory readable by UID 10001 and mounted read-only.

Do not use `chmod 777` or recursively change ownership of the music library. HTTP port 8080 does not encrypt credentials or audio; use built-in HTTPS with your own PEM certificate/key when the LAN path is not fully trusted. Never expose the Server directly to the public Internet.

## 2. Verify the private artifact

For a supplied release directory:

```sh
cd /path/to/supplied/release
sha256sum -c SHA256SUMS
```

If you received an approved private-registry reference, use its exact digest as `JASTREAMER_SERVER_IMAGE`, for example `registry.example/name@sha256:<verified-digest>`.

The supplied `.oci` is multi-platform and cannot be passed directly to `docker load`. When an offline Docker TAR is needed, convert only the target architecture on a Linux machine with Skopeo:

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

## 3. Install with Compose

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
4. Browse or search Library, add tracks or grouped views to Queue, and select an output while playback is stopped. Refresh outputs if a newly powered receiver is missing.
5. Use the footer controls for Play/Pause, Stop, Previous, Next, and Seek when supported by the receiver. AirPlay may require a PIN/password; pair only while stopped.

The queue is Server-wide, preserves order and duplicates, and survives restarts. A Server restart does not automatically resume playback.

### Artwork and Queue actions

- Footer album artwork navigates to **Queue**; it does not show track information or start playback.
- Library `(i)` and Queue artwork open track information without starting playback. Queue artwork shows a large `(i)` on hover or keyboard focus; touch screens show it continuously.
- The triangular Play button is the first action on the right of each Queue row. Only that button starts the entry.

An isolated renderer-status query failure does not restart playback or open a popup. A status warning appears after three consecutive failed queries and closes automatically when a query succeeds. Dismissing it suppresses repeat popups during the same failure streak. Playback-command failures and confirmed disconnections are still reported immediately.

UPnP/AirPlay capabilities vary by receiver. Confirm audible playback and the controls you need on your equipment.

## 5. Optional Windows portable client

Use the supplied private, unsigned `jastreamer-desktop_0.2.0_windows-x64.zip` on Windows 10/11 x64. Compare its hash with the supplied `.sha256` value:

```powershell
Get-FileHash .\jastreamer-desktop_0.2.0_windows-x64.zip -Algorithm SHA256
```

Extract the complete ZIP to a new writable local folder and run `jastreamer-desktop.exe`; do not run inside the ZIP or copy only the EXE. Select a discovered Server or enter its complete HTTP(S) root URL. The app verifies the Server before connecting, and connection/discovery does not start playback.

Server discovery searches each active IPv4 network adapter every five seconds, including when adapters change. Firewall and multicast restrictions can still require entering the Server URL manually.

Recent Servers, language, cookies, and sessions are stored beside the EXE in `user-data`. Closing the app does not stop Server playback. To upgrade, exit completely, back up the old folder, extract the new ZIP to a new folder, and preserve the old `user-data` beside the new EXE. A different Windows account or PC may require login again.

## 6. Backup and upgrade

There is no automatic updater.

1. Stop playback and confirm the player is stopped.
2. Run `docker compose -f deploy/docker/server/compose.synology.yaml down`.
3. With the Server stopped, back up the complete config directory (including PEM files) and data directory.
4. Verify/import the new private image and update `JASTREAMER_SERVER_IMAGE` to its exact digest or local image ID.
5. Keep the same config, data, and read-only music paths; review `docker compose ... config`, then run `up -d`.
6. Check login, library, artwork, queue preservation, output discovery, and actual playback.

For rollback, restore the matching previous image **and** its config/data backup. Never delete `/music`, `/srv/music`, `/volume1/music`, or another source library during an update or reset.

## 7. Troubleshooting

| Problem | Check |
|---|---|
| Web page unavailable | `/healthz`, Compose logs, configured listener, and firewall access to TCP 8080/8443 |
| Windows cannot discover Server | Allow mDNS UDP 5353 or enter the full Server URL manually |
| No output appears | Keep host networking; allow SSDP UDP 1900 for UPnP and mDNS UDP 5353 for AirPlay; disable client isolation |
| Output cannot play | Permit Server-to-receiver control/stream traffic and receiver-to-Server media traffic; set `media.base_url` only for a required reachable origin |
| Library is empty | Confirm the host directory is mounted at `/music`, UID 10001 can read/traverse it, and a scan completed |
| Settings cannot save | Confirm config directory and `server.json` are writable by UID/GID 10001 |
| AirPlay authorization fails | Stop playback, repeat the displayed PIN/password flow, and keep the packaged helper/FFmpeg paths |
| Password lost | Stop the Server, then run `jastreamer-server --reset-password USER --config /etc/jastreamer/server.json` in a maintenance container with the same config/data mounts; enter the new password only at the prompt |

When reporting a problem, include Server version, exact image digest, host architecture, relevant logs, and receiver model. Remove passwords, cookies, certificates, and private keys; preserve raw diagnostic wording.
