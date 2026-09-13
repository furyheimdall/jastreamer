# jastreamer

<img src="assets/jastreamer.svg" width="80" height="80" alt="jastreamer logo" />

jastreamer 0.2.0 is a self-hosted music server for a trusted private LAN. A Server on Linux or Windows indexes administrator-approved local music, serves the Web interface, keeps the queue and playlists in SQLite, and sends audio to one selected network output. UPnP/DLNA is built in, Google Cast can be enabled on either platform, and AirPlay sending is available in the supported Linux container only.

The interface supports English and Korean.

- [Install or update](INSTALL.md)
- [English user guide](INSTRUCTION.md)
- [한국어 README](README.ko.md)
- [한국어 설치 안내](INSTALL.ko.md)
- [한국어 사용자 안내서](INSTRUCTION.ko.md)

## What it does

- Scans FLAC, MP3, WAV/WAVE, Ogg/Vorbis, Opus, and M4A without modifying the source library
- Browses and searches tracks, albums, artists, genres, folders, playlists, and a duplicate-preserving Server queue
- Shows embedded artwork, stored text tags, and verified audio/file information
- Controls compatible UPnP/DLNA, optional Google Cast, and Linux AirPlay outputs
- Provides first-account setup, password login/change/recovery, durable sessions, and optional built-in HTTPS
- Provides a focused phone layout and an optional installable PWA

The phone layout is selected automatically for iPhone and Android phone browsers, not iPad or other tablet layouts. The PWA remains a network client of the Server; it does not cache the library or support offline playback.

## Deployment model

- **Linux Server:** one `amd64` or `arm64` container with the embedded Web interface, audio-only FFmpeg 8.1.2, and the pyatv 0.18.0 AirPlay sender. Synology Container Manager uses the supplied Compose definition.
- **Native Windows Server:** the unsigned x64 portable ZIP with the embedded Web interface, UPnP/DLNA, and optional Google Cast, but no bundled AirPlay sender, Renderer, or FFmpeg transcoder. It is not a Windows service.
- **Optional desktop:** a Windows 10/11 x64 portable ZIP or Linux amd64 DEB that connects to a Server and displays its hosted Web interface. It is not a Server, Renderer, or local audio player. There is no ARM64 desktop package.

Google Cast needs no Chrome or Python helper on either Server platform. Public previews are unsigned and not production-qualified; select one from [GitHub Releases](https://github.com/furyheimdall/jastreamer/releases), use its exact image digest or verified package and checksum, and read its limitations.

Keep writable config and data separate from the existing read-only music library. Preserve config and data during replacement, never recursively change ownership or permissions on music for jastreamer, and never expose the Server directly to the public Internet.

## Install or update

> Help me install or update jastreamer from https://github.com/furyheimdall/jastreamer.
> Read [AGENTS.md](AGENTS.md) first, then follow my platform branch in [INSTALL.md](INSTALL.md) (or [INSTALL.ko.md](INSTALL.ko.md) in Korean).

For manual setup, package verification, platform-specific installation, PWA setup, backup, upgrade, and rollback, use the [installation guide](INSTALL.md). For everyday operation and troubleshooting, use the [user guide](INSTRUCTION.md).

## Compatibility scope

Automatic discovery depends on multicast and suitable private-LAN routes between Server, client, and output. UPnP uses SSDP UDP 1900; Google Cast and Linux AirPlay use mDNS UDP 5353. Google Cast is available but disabled by default; AirPlay remains Linux-only. Output formats and controls vary by receiver, and discovery or a healthy Server does not prove audible playback.

Private-LAN HTTP does not encrypt credentials or audio. Use the Server's built-in HTTPS listener with an operator-provided trusted PEM certificate and key where needed. A phone PWA requires trusted HTTPS except on a localhost development origin; do not bypass certificate warnings or expose a NAS publicly.

## License

jastreamer is licensed under the [Apache License 2.0](LICENSE). Packaged third-party components retain their own licenses; see `THIRD-PARTY-NOTICES.txt` in the supplied Server artifact set or `/usr/share/jastreamer/THIRD-PARTY-NOTICES.txt` in the container.
