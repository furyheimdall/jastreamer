# Agent guide

This is the entry point for agents helping with [jastreamer](https://github.com/furyheimdall/jastreamer). Read the relevant installation branch before proposing commands. Reading this file is not permission to install software, stop playback, replace a service, publish a release, or change host security.

## Route the request

1. Read [README.md](README.md) or [README.ko.md](README.ko.md) for the product and supported targets.
2. For installation, select the platform in [INSTALL.md](INSTALL.md) or [INSTALL.ko.md](INSTALL.ko.md). For an existing deployment, start with [backup and upgrade](INSTALL.md#backup-and-upgrade), not first-account setup.
3. For everyday controls, settings, music browsing, and troubleshooting, use [INSTRUCTION.md](INSTRUCTION.md) or [INSTRUCTION.ko.md](INSTRUCTION.ko.md).
4. Inspect the authorized host and the repository revision matching the chosen artifact. Ask only for information unavailable from that inspection and for decisions affecting access, data, or availability.

Do not require the user to fill out a technical template or write configuration files. Explain the selected branch and prepare the actual files after approval. If access is unavailable, provide ready-to-save files and commands for the user's confirmed paths, inspect their returned results, and distinguish guidance from work actually performed.

## Supported installation branches

| Request | Read first | Boundary |
| --- | --- | --- |
| Linux or Synology Server | [Linux Server](INSTALL.md#linux-server), `deploy/docker/server/compose.synology.yaml`, `packaging/server/server.json` | Linux `amd64` or `arm64`; DS918+ is `amd64`. The image includes Web Control, FFmpeg, and the Linux AirPlay runtime. |
| Native Windows Server | [Windows Server](INSTALL.md#windows-server) | Portable Windows x64 ZIP; embedded Web Control, UPnP/DLNA and opt-in Cast, without bundled AirPlay or FFmpeg. |
| Desktop Control | [Windows ZIP](INSTALL.md#desktop-windows) or [Linux DEB](INSTALL.md#desktop-linux) | Optional client of an existing Server, not a local playback renderer. No ARM64 Desktop package. |
| Phone or PWA | [PWA](INSTALL.md#pwa) | Use the Server-hosted UI. PWA installation needs a trusted HTTPS origin, except local development. It is not an offline player or a native Android/iOS package. |
| Upgrade or recovery | [Upgrade](INSTALL.md#backup-and-upgrade), [rollback](INSTALL.md#rollback) | Preserve the current deployment and state; never treat an upgrade as a clean installation. |

## Inspect and agree before changing the host

- Establish the authorized connection method, OS/architecture, available container tooling or native Windows target, LAN routing, listener ports and conflicts, and whether an existing Server is running or playing.
- Identify the existing absolute music locations on the **Server**, including network mounts. Check traversal/read access and available storage. Never create a missing music directory as an empty fallback.
- Identify separate project, config, data, and backup locations. Resolve real paths and reject overlapping or nested config/data/music paths that could expose application state as music.
- Select a published artifact from [Releases](https://github.com/furyheimdall/jastreamer/releases). Read its limitations and verify the exact architecture, source revision, SHA-256 and image digest. A public GHCR image needs no registry password. Do not invent an artifact, use `latest`, silently substitute a build, or promote an unsigned preview to production-qualified status.
- Propose the exact mounts, saved configuration, client LAN URL, permissions, network access, backup/rollback plan, and interruption. Cover Server name, music roots, HTTP/HTTPS, selected network interfaces/access restrictions, media origin/transcoding, optional outputs, and UI language together. Explain non-default settings. Keep Google Cast disabled unless explicitly selected; Cast needs no Chrome or Python helper.
- Obtain approval for deployment changes and separately for installing host tooling, changing permissions/firewall rules, stopping playback or a service, or pairing/controlling a receiver. Existing approval for another PR, release or deployment does not carry over.

Never request or record passwords, private keys, registry tokens, session cookies, or other credentials in chat, Git, generated instructions, or logs. Use existing secure credential handling or private interactive entry. For HTTPS, request certificate/key **paths**, not key contents. Do not bypass certificate validation or expose the NAS publicly to make PWA installation work.

## Install or upgrade without losing state

Follow the selected INSTALL branch rather than duplicating its commands here.

- Use the existing Compose project name, directory, files, environment and mounts during updates. Save actual deployment inputs persistently; do not depend on temporary shell exports or leave unresolved placeholders. Keep the complete architecture-matched Linux image rather than replacing it with a bare Go binary or installing its media dependencies on the host.
- Preserve UID/GID `10001:10001`, read-only container root and music, writable config/data only, dropped capabilities, `no-new-privileges`, temporary storage, host networking and the documented restart policy. Keep container `data_dir`, library roots and packaged helper paths consistent with their mounts. Additional music roots need separate approved read-only mounts.
- Never use privileged mode, mount the Docker socket, disable a firewall/sandbox, use `chmod 777`, or recursively change music ownership/permissions. Do not add bridge port mappings to the host-network deployment. Keep Chromium's packaged setuid sandbox for the Linux Desktop DEB.
- Preserve accounts, sessions, Server UUID, settings, library, artwork, queue/order/position, playlists and device credentials. Do not overwrite existing config/data, remove volumes or reset accounts. Let the user create the first account privately; an existing account requires login, not setup.
- Download and verify the target before downtime. After permission to stop playback and the Server, take and verify a consistent complete config/data/deployment backup with the service stopped. Retain the old image or package and its matching backup. A lone copy of a live SQLite database is not a consistent backup.
- Validate the prepared Compose configuration and the target Server's `--check-config` with its actual mounts before starting. On Windows, preserve the existing `server.json`, `data` and `music` while replacing only package-owned files; preserve Desktop `user-data` separately.
- If verification fails, retain diagnostics and use only the approved rollback plan. Restoring a backup may discard post-backup changes; explain that impact first. Do not repeatedly replace a failing service or weaken checks to claim success.

## Verify and hand over

1. Verify the actual running image/package identity, health and logs, mounts/security and persistent state. Check the Web UI through the real client LAN route, not only `localhost`.
2. After private login, apply the agreed language and settings, scan the actual music roots and inspect the completed result. Confirm output discovery without silently selecting, pairing or playing a receiver. If browser access is unavailable, guide these steps and check the user's result.
3. Keep playback Server-owned: one queue and selected output, explicit resume after reconnect/restart, and no Stop when closing Control or changing its Server selection. UI navigation, PWA installation and player-panel expansion must not send playback commands.
4. Distinguish a healthy Server, media retrieval, receiver-observed playback and actual audible output. CI, signatures, browser emulation and a discovered receiver do not prove physical audio qualification. Preserve real errors and unsupported-operation boundaries.
5. Report the verified clickable LAN URL, exact version/digest/hash, saved paths, backup/rollback location, checks performed and remaining prerequisites without secrets. Invite the user to explicitly select their intended output while stopped, play a track, confirm sound, try supported pause/seek, then Stop. These are user-run checks, not authorization for automatic playback.

## When the request is a code change

The Go Server is in `apps/server`, shared React Web Control in `apps/control`, and Electron Desktop wrapper in `apps/desktop`. Use the actual targets in [Makefile](Makefile) and the component package manifests. Verify the affected component and actual UI/API behavior; a successful build of one component does not qualify the others. Installation assistance is not permission to commit, push, merge, release or deploy unrelated changes. Keep installation procedures in INSTALL and detailed UI behavior in INSTRUCTION rather than growing the README prompt again.
