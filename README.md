# jastreamer

<img src="assets/jastreamer.svg" width="80" height="80" alt="jastreamer logo" />

jastreamer 0.2.0 is a self-hosted music server for a trusted private LAN. A single Linux Server indexes administrator-approved local music, serves the Web interface, keeps the queue and playlists in SQLite, and sends audio to one selected network output. UPnP/DLNA is built in; AirPlay sending is available on the supported Linux container platforms.

English is the default interface language, and Korean is also available. The navigation label is always **Settings** in both languages; choose **Language / 언어** inside that page.

- [English user guide](INSTRUCTION.md)
- [한국어 README](README.ko.md)
- [한국어 사용자 안내서](INSTRUCTION.ko.md)

## What it does

- Scans FLAC, MP3, WAV/WAVE, Ogg/Vorbis, Opus, and M4A files without modifying the source library
- Browses tracks, albums, artists, genres, and folders, with search and embedded artwork
- Shows stored text tags and verified audio/file information on demand
- Maintains playlists and one server-wide, duplicate-preserving queue
- Uses predictable artwork actions: footer artwork opens Queue; Library `(i)` and Queue artwork open track information; Queue artwork reveals a large `(i)` on hover, and only the explicit triangular Play button starts that entry
- Provides explicit Play, Pause, Stop, Previous, Next, and Seek controls when the selected output supports them
- Discovers UPnP/DLNA and AirPlay outputs on the LAN
- Supports AirPlay PIN/password authorization when required by the receiver
- Provides first-account setup, password login/change/recovery, and durable sessions
- Uses private-LAN HTTP by default, with optional built-in HTTPS using an operator-provided PEM certificate and key

The optional Windows 10/11 x64 portable desktop is a connection shell. It finds a jastreamer Server or accepts its HTTP(S) address and displays the Server-hosted Web interface. It is not a Windows server and does not play audio locally.

## Deployment model

The supported Server package is a Linux container for `amd64` or `arm64`, including the Web interface, an audio-only FFmpeg 8.1.2 executable, and the pyatv 0.18.0 AirPlay sender. Synology Container Manager is supported through the supplied Compose definition. Version 0.2 distribution is private: use only a verified artifact supplied to you or an exact digest from an approved private registry. There is no public image or public download promised by this repository.

Keep three storage areas separate:

- **config:** writable `server.json` and optional HTTPS PEM files
- **data:** writable SQLite database, artwork cache, and AirPlay state
- **music:** the existing library, mounted read-only

Preserve config and data across container replacement. Never recursively change ownership or permissions on the music library for jastreamer, and never expose the Server directly to the public Internet.

See the [English user guide](INSTRUCTION.md) for artifact import, Docker and Synology setup, the optional Windows client, first use, language selection, upgrades, and troubleshooting.

## Compatibility scope

Automatic discovery depends on multicast and on the Server, client, and output having suitable LAN routes. Output capabilities vary by device: a receiver may omit Pause or Seek, reject a format, or require authorization. The repaired AirPlay path has bounded protocol and integration coverage; that is not universal receiver certification, long-term hardware qualification, or a promise about listening quality on every device. Confirm discovery, authorization, controls, queue advance, and audible playback on the equipment you intend to use.

## License

jastreamer is licensed under the [Apache License 2.0](LICENSE). Packaged third-party components retain their own licenses; see `THIRD-PARTY-NOTICES.txt` in the supplied Server artifact set or `/usr/share/jastreamer/THIRD-PARTY-NOTICES.txt` in the container.
