# jastreamer

[![CI](https://github.com/furyheimdall/jastreamer/actions/workflows/ci.yml/badge.svg?branch=main&event=push)](https://github.com/furyheimdall/jastreamer/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![TypeScript](https://img.shields.io/badge/TypeScript-3178C6?logo=typescript&logoColor=white)](https://www.typescriptlang.org/)
[![Python](https://img.shields.io/badge/Python-3776AB?logo=python&logoColor=white)](https://www.python.org/)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

<img src="assets/jastreamer.svg" width="80" height="80" alt="jastreamer logo" />

jastreamer 0.2.0 is a self-hosted music server for a trusted private LAN. A Server on Linux or Windows indexes administrator-approved local music, serves the Web interface, keeps likes, the queue and playlists in SQLite, and sends audio to one selected network output or connected browser. UPnP/DLNA is built in, Google Cast can be enabled on either platform, and AirPlay sending is available in the supported Linux container only.

English is the default interface language; Korean is also supported.

- [Agent-assisted install or update](AGENTS.md)
- [Install or update](INSTALL.md)
- [English user guide](INSTRUCTION.md)
- [한국어 README](README.ko.md)
- [한국어 설치 안내](INSTALL.ko.md)
- [한국어 사용자 안내서](INSTRUCTION.ko.md)

## What it does

- Scans FLAC, MP3, WAV/WAVE, Ogg/Vorbis, Opus, and M4A without modifying the source library
- Browses and searches tracks, albums, artists, genres, and folders, and displays embedded artwork
- Shows stored tags and verified audio/file information when needed
- Maintains playlists and a Server-wide queue that preserves duplicate tracks
- Saves track likes and appends all available liked tracks to Queue in shuffled order
- Plays through **This device** using browser audio and the same Server queue
- Controls playback and seeking on compatible network outputs
- Discovers UPnP/DLNA, optional Google Cast, and Linux AirPlay outputs on the LAN
- Supports AirPlay PIN/password authentication when required by the receiver
- Provides first-account setup, password login/change/recovery, and durable sessions
- Provides private-LAN HTTP and optional built-in HTTPS using operator-provided PEM certificates and keys
- Provides a four-tab iPhone/Android phone layout, collapsible playback controls, and an optional installable PWA
- Offers a native Android client with verified Server discovery, isolated WebView sessions, and Server-controlled Media3 local playback with system media controls
- Includes native SwiftUI/WKWebView iOS source and simulator CI around the same Server-hosted Web UI

The phone layout is selected automatically for iPhone and Android phone browsers, not iPad or other tablet layouts. The PWA remains a network client of the Server; it does not cache the library or support offline playback.

## Deployment model

- **Linux Server:** one `amd64` or `arm64` container with the embedded Web interface, Python 3.12, audio-only FFmpeg 8.1.2, and the pyatv 0.18.0 AirPlay sender. Synology Container Manager uses the supplied Compose definition.
- **Native Windows Server:** the unsigned x64 portable ZIP with the embedded Web interface, UPnP/DLNA, and optional Google Cast, but no bundled AirPlay sender, native audio engine, or FFmpeg transcoder. It is not a Windows service.
- **Optional desktop:** a Windows 10/11 x64 portable ZIP or Linux amd64 DEB that discovers a Server or accepts its HTTP(S) address and displays its hosted Web interface. It is not a Server and adds no standalone native audio engine; local output uses the shared Web UI. There is no ARM64 desktop package; Linux arm64 clients can use a browser.
- **Android client:** Kotlin/WebView for Android 10 or newer, requiring a current WebView provider with independent-profile support. It reuses the Server-hosted phone/tablet interface and adds native local playback through a Media3 foreground service. [Android CI](https://github.com/furyheimdall/jastreamer/actions/workflows/android.yml) supplies development/test APKs and unsigned release builds, not a production-signed or Play Store release; see [Android installation](INSTALL.md#android).
- **iOS source and CI:** SwiftUI/WKWebView for iOS/iPadOS 18.4 or newer. [iOS CI](https://github.com/furyheimdall/jastreamer/actions/workflows/ios.yml) verifies the actual Web UI in an iPhone simulator and produces an unsigned device development bundle plus a simulator-only bundle. No installable iPhone package, TestFlight or App Store release is provided; see [iOS development scope](INSTALL.md#ios).

Google Cast needs no Chrome or Python helper on either Server platform. Select the newest compatible published Server release from the complete [GitHub Releases listing](https://github.com/furyheimdall/jastreamer/releases), including correctly labelled previews; verify its provenance and pin its exact image digest or verify the package checksum. Never use mutable `latest` or assume `/releases/latest` includes previews.

- **config:** writable `server.json` and optional HTTPS PEM files
- **data:** writable SQLite database, artwork cache, and AirPlay state
- **music:** a read-only existing absolute music root, or a new root explicitly approved by the user for creation and use

Releases whose manifest includes bundled samples can seed three MP3s under `jastreamer-samples` without overwriting existing files. When replacing either Server target, preserve existing deployment paths, config, data, and source music; never recursively change music ownership/permissions or expose Server directly to the public Internet.

For manual setup, package verification, Linux/Synology/Windows installation, samples, PWA setup, upgrade, and rollback, use the [installation guide](INSTALL.md). Agent instructions are in [AGENTS.md](AGENTS.md); everyday operation and troubleshooting are in the [user guide](INSTRUCTION.md).

## Optional Google Cast

Google Cast is disabled by default, including when an older `server.json` has no `cast.enabled` field. Enable **Google Cast output** in **Settings → Playback & outputs**, save, and restart Server. Do not enable it merely because a receiver is discovered. Discovery needs mDNS UDP 5353 on the selected `network.interfaces`. Server must be able to reach the receiver's advertised Cast TCP port, and the receiver must be able to reach the **Server URL used by playback devices to fetch audio**. Normally leave this URL blank for automatic selection; only set an actual Server address reachable by the receiver when routing requires it.

Cast direct streaming conservatively checks verified codec, sample rate, channel metadata, and bit depth where applicable. Every directly streamed source must have a confirmed matching codec, a positive sample rate, and mono or stereo channels. FLAC supports up to 96 kHz and 1–24-bit; MP3, Ogg/Vorbis, Ogg/Opus, and M4A/AAC support up to 48 kHz; LPCM WAV supports up to 48 kHz and 1–16-bit. Other formats or unverified sources require media conversion and configured FFmpeg. Converted output is a non-seekable 44.1 kHz stereo 16-bit WAV stream. Source files are not modified.

Cast uses the same single Server queue as the other outputs. Cast LOAD does not use autoplay; Play is sent explicitly. Receiver groups and gapless playback are not supported, and neither direct streaming nor conversion guarantees bit-perfect playback. Pause, seeking, and format support depend on the receiver. Cast control uses a persistent TLS connection and advances the queue only after an explicit `FINISHED` state from the application/media session owned by jastreamer.

## Install with an AI agent

Send the following request to your AI agent. Before installation, review the proposed paths and service changes yourself. Do not include passwords, private keys, or tokens in the prompt.

> Help me install or update jastreamer from https://github.com/furyheimdall/jastreamer.  
> Read and follow the repository's [AGENTS.md](AGENTS.md) first.

## Updating

Update Linux Server by replacing its container image while preserving the config/data mounts. For native Windows Server, verify and extract the new ZIP into a separate directory, then replace only package-owned files in the existing directory while preserving `server.json`, `data`, and `music`. Neither path reinstalls the application from scratch or resets its data. The Server-hosted Web interface and built-in optional Google Cast support are updated together on both targets; FFmpeg and AirPlay are included only in the supported Linux container. Optional desktop executables are updated separately through their ZIP or DEB.
The Android wrapper is updated separately with a compatible APK signed by the same certificate; Server-hosted Web changes do not require replacing the wrapper. Its current CI APKs are development artifacts, not an established production update channel.

The iOS wrapper currently has source and CI only. Device signing, installation, distribution and a production update channel remain separate from Server-hosted Web updates.

There is currently no in-app version check or automatic update. Download and verify the exact artifacts and checksums, stop playback and Server, and replace only the package-owned files or image while preserving existing configuration, data, music paths and mounts. After verification, reconnect to the existing Server address and refresh the Web interface. Playback does not resume automatically.

For Linux container procedures and post-update checks, see [upgrade](INSTALL.md#upgrade). For native Windows first installation and updates, see [Windows Server](INSTALL.md#windows-server). Registry distribution applies to Linux images; offline packages are also available.

## Compatibility scope

Automatic discovery depends on multicast and suitable private-LAN routes between Server, client, and output. UPnP uses SSDP UDP 1900; Google Cast and Linux AirPlay use mDNS UDP 5353. Output capabilities vary: receivers may lack pause or seeking, reject certain formats, or require authentication. Google Cast is available on Linux and native Windows but disabled by default; AirPlay remains Linux-only. Verify discovery, control, queue progression, and audible playback on the actual equipment you will use. Discovery or a healthy Server alone does not prove audible playback.

Private-LAN HTTP does not encrypt credentials or audio. Use the Server's built-in HTTPS listener with an operator-provided trusted PEM certificate and key where needed. A phone PWA requires trusted HTTPS except on a localhost development origin; do not bypass certificate warnings or expose a NAS publicly.

## License

jastreamer is licensed under the [Apache License 2.0](LICENSE). Packaged third-party components retain their own licenses; see `THIRD-PARTY-NOTICES.txt` in the supplied Server artifact set or `/usr/share/jastreamer/THIRD-PARTY-NOTICES.txt` in the container.
