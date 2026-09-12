# jastreamer

jastreamer 0.2.0 is a self-hosted music server for a private LAN. One Go process indexes administrator-approved local music, stores a durable queue and playlists in SQLite, serves an embedded React Web application, and plays through one selected network output. Generic UPnP/DLNA is always available; AirPlay sending is an optional Linux feature. A portable Windows desktop shell is also optional.

## Architecture and protocols

- **Server:** Go, SQLite, embedded React/TypeScript Web UI, same-origin JSON API and server-sent events.
- **UPnP/DLNA outputs:** SSDP discovery, SOAP AVTransport control, and renderer-bound HTTP media/artwork URLs. Original compatible audio is streamed; optional FFmpeg transcoding targets L16.
- **AirPlay outputs:** Linux amd64/arm64 only, using the packaged FFmpeg 8.1.2 decoder and pyatv 0.18.0 RAOP sender. It is independent of UPnP discovery.
- **Windows desktop:** an Electron x64 connection shell which discovers Servers with `_jastreamer._tcp.local`, verifies `/api/v1/discovery`, and then displays the Server-hosted Web UI. It is not a second player or server.

Server discovery and output discovery are separate. Discovering a Server or output never connects to it or starts playback automatically.

## Capabilities

- FLAC, MP3, WAV, Ogg/Vorbis, Opus, and M4A library scanning without modifying source music
- track, album, artist, genre, and folder browsing; search and embedded artwork
- on-demand track information from library info buttons and player artwork, including embedded text tags and verified audio properties
- saved playlists and one global, duplicate-preserving durable queue
- explicit Play, Pause, Stop, Previous, Next, and Seek when the selected output supports them
- stopped-only output selection and AirPlay PIN/password authorization
- deduplicated AirPlay discovery, advertised AirTunes RSA/AES with ALAC framing, and UTF-8-correct metadata
- dismissible full-text error dialogs, including delayed playback failures
- first-account setup, password login, HttpOnly sessions, password change and local recovery
- private-LAN HTTP by default, or built-in HTTPS with an operator-provided PEM certificate and key

## Prerequisites

Running jastreamer requires a private LAN between the Server, browser, and output; writable configuration and application-data directories; and read-only access to the music library. Multicast must be allowed for automatic discovery.

Building from source uses Go 1.25.0, Node.js 22.20.0, and npm. Container packaging additionally needs Docker Buildx. The optional desktop package targets Windows 10/11 x64.

## Source layout

- `apps/server` — Server, database, library, output protocols, API, and embedded-Web boundary
- `apps/control` — shared React Web application
- `apps/desktop` — optional portable Windows Electron shell
- `contracts/http-api` — HTTP API machine contract
- `deploy/docker/server` — Synology Compose definition
- `packaging/server` — container recipe inputs, notices, and private artifact scripts
- `tooling/qa` — browser integration checks

## Quick start from source

```sh
make build
apps/server/dist/jastreamer-server --init-config "$PWD/server.json"
```

Edit `server.json`: choose a writable absolute `data_dir` and add absolute `library_roots`. Then validate and start it:

```sh
apps/server/dist/jastreamer-server --check-config "$PWD/server.json"
apps/server/dist/jastreamer-server --config "$PWD/server.json"
```

Open `http://<server-LAN-address>:8080/` and create the initial account. HTTP does not encrypt credentials, cookies, metadata, commands, or media; use the built-in HTTPS listener unless every part of the LAN path is trusted. Never expose either listener directly to the public Internet.

Container and Synology deployment, the exact configuration schema, AirPlay setup, desktop packaging, upgrades, API routes, and operational checks are documented in [INSTRUCTION.md](INSTRUCTION.md).

## Development and verification

```sh
make verify
make browser-smoke
make desktop-verify
```

See [INSTRUCTION.md](INSTRUCTION.md) for prerequisites, exact command effects, private package commands, and the limits of automated verification.

## License

jastreamer is licensed under [Apache License 2.0](LICENSE). Packaged third-party components retain their own licenses; see `packaging/server/THIRD-PARTY-NOTICES.txt`. In particular, the AirPlay sender uses pyatv 0.18.0 under MIT, and the separate audio-only FFmpeg 8.1.2 executable is LGPL-2.1-or-later.
