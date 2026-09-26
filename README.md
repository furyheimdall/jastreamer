# jastreamer

[![CI](https://github.com/furyheimdall/jastreamer/actions/workflows/ci.yml/badge.svg?branch=main&event=push)](https://github.com/furyheimdall/jastreamer/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![TypeScript](https://img.shields.io/badge/TypeScript-3178C6?logo=typescript&logoColor=white)](https://www.typescriptlang.org/)
[![Python](https://img.shields.io/badge/Python-3776AB?logo=python&logoColor=white)](https://www.python.org/)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

<img src="assets/jastreamer.svg" width="80" height="80" alt="jastreamer logo" />

jastreamer 0.2.0 is a self-hosted music server for a trusted private LAN. A Server on Linux or Windows indexes the local music you approve, hosts the Web interface, keeps accounts, likes, play counts, playlists and one shared queue in SQLite, and sends audio to a single selected output. UPnP/DLNA is built in, Google Cast is optional on both Server platforms, and AirPlay sending is available only in the Linux container. English is the default interface language; Korean is also supported.

## Documentation

| Topic | English | 한국어 |
| --- | --- | --- |
| Install, upgrade, rollback | [INSTALL.md](INSTALL.md) | [INSTALL.ko.md](INSTALL.ko.md) |
| Everyday use and troubleshooting | [INSTRUCTION.md](INSTRUCTION.md) | [INSTRUCTION.ko.md](INSTRUCTION.ko.md) |
| Product overview | README.md (this file) | [README.ko.md](README.ko.md) |
| Rules for AI agents and contributors | [AGENTS.md](AGENTS.md) | English only |

## What it does

- Scans FLAC, MP3, WAV/WAVE, Ogg/Vorbis, Opus and M4A without modifying the source library: **Scan now** reuses saved results for unchanged files, and a confirmed **Full rescan** rereads everything
- Browses and searches tracks, albums, artists, genres and folders with embedded artwork and stored or verified track details, and applies folder actions to every matching track in subfolders
- Verifies audio end to end in the background and keeps a filterable error and file-check history with CSV export
- Maintains playlists and one Server-wide queue that preserves order and duplicates, survives restarts, and can be cleared with a confirmation
- Offers shared **Shuffle** and **Repeat off / all / one** modes, and keeps the loaded track playing independently of queue membership
- Saves Server-wide likes and play counts, with a **Most Played** view and a one-time **Shuffle liked into queue** action
- Plays through **This device** using browser audio, with a per-device output name and local volume
- Discovers and controls UPnP/DLNA, optional Google Cast, and Linux AirPlay outputs, including AirPlay PIN/password authentication
- Provides first-account setup, password login/change/recovery, durable sessions, private-LAN HTTP, and optional built-in HTTPS
- Adapts to a four-tab iPhone/Android phone layout with collapsible controls and an optional installable PWA

## Supported platforms

| Role | Target | Package | Notes |
| --- | --- | --- | --- |
| Server | Linux `amd64`/`arm64`, incl. Synology | Container image | Web interface, Python 3.12, audio-only FFmpeg 8.1.2, pyatv 0.18.0 AirPlay sender |
| Server | Windows x64 | Portable ZIP | Web interface, UPnP/DLNA, optional Cast; no AirPlay and no FFmpeg; not a Windows service |
| Desktop client | Windows 10/11 x64 | Portable ZIP | Browser audio plus opt-in WASAPI Shared/Exclusive output and Windows media controls ([Windows audio](INSTRUCTION.md#windows-audio)) |
| Desktop client | Linux `amd64` | DEB | Browser audio only; no ARM64 desktop package |
| Mobile client | Android 10+ | Signed `jastreamer-android_0.2.0_release.apk` from [GitHub Releases](https://github.com/furyheimdall/jastreamer/releases) | Media3 local playback with system media controls, opt-in bit-perfect direct USB output to a connected USB Audio Class DAC, and an offline **Saved music** library ([Android](INSTALL.md#android)) |
| Mobile client | iOS/iPadOS 18.4+ | [iOS CI](https://github.com/furyheimdall/jastreamer/actions/workflows/ios.yml) and source only | Controller-only client; no installable package, TestFlight or App Store release ([iOS](INSTALL.md#ios)) |
| Browser or PWA | Any current LAN browser | Served by the Server | Phone layout for iPhone and Android phone browsers; PWA installation needs trusted HTTPS ([PWA](INSTALL.md#pwa)) |

Desktop and mobile clients all display the same Server-hosted Web interface. Install the Android APK attached to a release; CI APKs remain development artifacts, and jastreamer is not distributed through the Play Store.

## Quick start

1. Choose a release from [GitHub Releases](https://github.com/furyheimdall/jastreamer/releases) and verify its checksums or image digest — see [releases](INSTALL.md#releases).
2. Install the Server: [Linux or Synology](INSTALL.md#linux-server) or [native Windows](INSTALL.md#windows-server), with `config` and `data` writable and your music root read-only.
3. Open the Server's LAN URL, create the first account privately, set the music folder, and run **Settings → Library → Scan now**.
4. Select an output, play a track and confirm the sound — everyday controls are in the [user guide](INSTRUCTION.md).

## Install with an AI agent

Send the following request to your AI agent. Review the proposed paths and service changes yourself before installation, and never include passwords, private keys or tokens in the prompt.

> Help me install or update jastreamer from https://github.com/furyheimdall/jastreamer.  
> Read and follow the repository's [AGENTS.md](AGENTS.md) first.

## Updating

An update replaces only the container image or the package-owned files: configuration, database, accounts, sessions, playlists, queue and music paths are preserved, and playback does not resume automatically. There is no in-app version check or automatic updater, so download and verify the exact artifacts first. Desktop, Android and iOS clients are updated separately from the Server.

Procedures and post-update checks are in [upgrade](INSTALL.md#upgrade) and [rollback](INSTALL.md#rollback).

## What jastreamer is not

- Not an Internet service: it is designed for a trusted private LAN and must not be exposed publicly.
- Not a cloud or streaming subscription: it plays only local files you own and never modifies them.
- Not a UPnP/DLNA media server for other apps: it discovers and controls renderers and serves media to them over HTTP(S).
- Not an offline player in the browser: the PWA is a network client with no library cache. Offline listening exists only in the Android client's **Saved music**.
- Not a self-updating application, a Windows service, or an app-store product.

## Compatibility and security scope

Automatic discovery needs multicast and suitable private-LAN routes between Server, client and output: UPnP uses SSDP UDP 1900, Google Cast and Linux AirPlay use mDNS UDP 5353. Receivers differ — some lack pause or seeking, reject formats, or require authentication. Google Cast works on both Server platforms but is disabled by default; AirPlay sending remains Linux-only. Verify discovery, control, queue progression and audible playback on your own equipment: a healthy Server or a discovered receiver does not prove sound.

Private-LAN HTTP encrypts neither credentials nor audio; use the built-in HTTPS listener with an operator-provided trusted certificate and key where that matters. Do not bypass certificate warnings, publish a NAS to the Internet, use `chmod 777`, or change music ownership recursively: the container runs as UID/GID `10001:10001` with a read-only music mount.

<a id="code-signing-policy"></a>
## Code signing policy

**How artifacts are built.** Public artifacts are produced only by GitHub Actions from the protected `main` branch. The release workflow republishes the exact bytes of a successful protected-`main` CI run, verifies every artifact against that run first, and refuses to overwrite an existing tag or registry reference. The one exception is the Android APK: the workflow takes the unsigned APK from the matching Android CI run and signs it with the release key, then verifies the resulting signature. Each release records its source revision and ships machine-readable evidence: `SHA256SUMS` over all assets, `release-provenance.json` (repository, release tag, channel, source revision, CI runs, artifact records and the Android signer certificate), per-package `*.manifest.json` and `*.verification.json` receipts, and a Server publication manifest listing each image's platform, immutable digest and digest reference. Verify these before installing.

**Current signing status.**

| Artifact | Status |
| --- | --- |
| Linux Server container images | Not code-signed. Identified and pinned by immutable `sha256` digest, published to GHCR and verified by anonymous download during release. Pin the digest, never a mutable tag. |
| Windows Server portable ZIP | Not Authenticode-signed. Windows may show SmartScreen or Mark-of-the-Web warnings; verify the published SHA-256 and [unblock the ZIP](INSTALL.md#windows-unblock) before extracting — see [Windows Server](INSTALL.md#windows-server). |
| Windows desktop ZIP | Not Authenticode-signed; same SmartScreen, checksum and [unblock](INSTALL.md#windows-unblock) handling — see [Windows desktop](INSTALL.md#desktop-windows). |
| Android APK | Release APKs attached to GitHub Releases are signed with the jastreamer Android release key using APK Signature Scheme v2 and v3. Signer certificate SHA-256 `53285C2C239AFF2927EBE6F5C6AEBB82FDBB50956ED84B1E9F0222B2D925943E`. CI APKs stay development-only: debug/test-signed or unsigned. |
| iOS | Not distributed. Source and CI only, with unsigned development bundles that cannot be installed from this repository. |

Before installing an APK, confirm the signer yourself and compare it with the fingerprint above and in the release notes, which carry the same value:

```
apksigner verify --print-certs jastreamer-android_0.2.0_release.apk
```

The repository pins that fingerprint in `packaging/android/release-certificate-sha256.txt`, and the release workflow refuses to publish an APK signed by any other certificate. The digest is compared case-insensitively; `apksigner` prints it without separators. jastreamer is not published on Google Play.

Stable releases (`vX.Y.Z`) are marked latest; older `vX.Y.Z-preview.N` entries remain prereleases and are never marked latest.

**Who signs and approves.** jastreamer is maintained by [@furyheimdall](https://github.com/furyheimdall), who is the only committer, reviewer and approver. Contributions from anyone else are reviewed by the maintainer before merge, and every signing or release action is approved by the maintainer.

**Privacy.** This program will not transfer any information to other networked systems unless specifically requested by the user or the person installing or operating it. There is no telemetry, analytics, update check or externally hosted content: the Server and its clients communicate only with the Server you configure and with the LAN outputs you discover and select.

**Signing service.** SignPath Foundation code signing is not in use yet.

## Contributing and support

Questions, bug reports and feature requests belong in [GitHub Issues](https://github.com/furyheimdall/jastreamer/issues). Private vulnerability reporting is not enabled here, so report security problems through an issue without including credentials, private keys, tokens, secret-bearing logs or personal data. Contributor and agent rules — repository layout, build targets and safety boundaries — are in [AGENTS.md](AGENTS.md). Release notes and downloads are on the [Releases](https://github.com/furyheimdall/jastreamer/releases) page.

## License

jastreamer is licensed under the [Apache License 2.0](LICENSE). Packaged third-party components keep their own licenses; see `THIRD-PARTY-NOTICES.txt` in the supplied Server artifact set or `/usr/share/jastreamer/THIRD-PARTY-NOTICES.txt` in the container.
