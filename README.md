# jastreamer

<img src="assets/jastreamer.svg" width="80" height="80" alt="jastreamer logo" />

jastreamer 0.2.0 is a self-hosted music server for a trusted private LAN. A Server on Linux or Windows indexes administrator-approved local music, serves the Web interface, keeps the queue and playlists in SQLite, and sends audio to one selected network output. UPnP/DLNA is built in. AirPlay sending is available only in the supported Linux container packages.

The interface supports English (the default) and Korean.

- [English user guide](INSTRUCTION.md)
- [한국어 README](README.ko.md)
- [한국어 사용자 안내서](INSTRUCTION.ko.md)
- [Copyable agent setup prompt](#agent-assisted-setup)

## What it does

- Scans FLAC, MP3, WAV/WAVE, Ogg/Vorbis, Opus, and M4A files without modifying the source library
- Browses tracks, albums, artists, genres, and folders, with search and embedded artwork
- Shows stored text tags and verified audio/file information on demand
- Maintains playlists and one server-wide, duplicate-preserving queue
- Controls playback and seeking on compatible network outputs
- Discovers UPnP/DLNA and AirPlay outputs on the LAN
- Supports AirPlay PIN/password authorization when required by the receiver
- Provides first-account setup, password login/change/recovery, and durable sessions
- Uses private-LAN HTTP by default, with optional built-in HTTPS using an operator-provided PEM certificate and key

The optional desktop is a connection shell: a Windows 10/11 x64 portable ZIP or Linux amd64 DEB. It finds a jastreamer Server or accepts its HTTP(S) address and displays the Server-hosted Web interface. Neither package is a server or local audio player; Linux arm64 clients can use a browser.

## Deployment model

The Server has two separate deployment targets. Linux `amd64` and `arm64` use a container that includes the embedded Web interface, an audio-only FFmpeg 8.1.2 executable, and the pyatv 0.18.0 AirPlay sender. Native Windows x64 uses the unsigned portable `jastreamer-server_0.2.0_windows-x64.zip`, with the embedded Web interface and UPnP/DLNA support but no bundled AirPlay sender, Renderer, or FFmpeg transcoder. Synology Container Manager uses the supplied Linux Compose definition. Select a published preview from [GitHub Releases](https://github.com/furyheimdall/jastreamer/releases), use its exact Linux image digest or verified Windows Server ZIP and checksum, and read the target-specific manifest and native verification receipt. Public previews do not require registry login. They are not production-qualified or signed releases; review the stated verification limits and verify downloaded files against the release checksums. Do not use a floating `latest` tag.

Keep three storage areas separate:

- **config:** writable `server.json` and optional HTTPS PEM files
- **data:** writable SQLite database, artwork cache, and AirPlay state
- **music:** the existing library, mounted read-only

Preserve config and data when replacing either Server target. Never recursively change ownership or permissions on the music library for jastreamer, and never expose the Server directly to the public Internet.

See the [English user guide](INSTRUCTION.md) for artifact import, Docker and Synology setup, optional desktop clients, first use, language selection, upgrades, and troubleshooting.

## Agent-assisted setup

Copy the entire prompt below into a coding agent that can read this repository and, with your permission, operate your Linux server or NAS. You do not need to fill in a template first: the agent should ask for missing information and explain unfamiliar choices. If it cannot access the server, it should give you commands to run and inspect their output, not claim to have installed anything.

Do not paste SSH passwords, private keys, registry tokens, or your jastreamer password into the prompt. Use an existing SSH profile/key agent or an interactive credential prompt. Review the proposed paths and service changes before authorizing installation.

<details>
<summary>Expand and copy the setup prompt</summary>

```text
Help me install jastreamer Server and its Web Control on my own server.
Use https://github.com/furyheimdall/jastreamer and read README.md,
INSTRUCTION.md, deploy/docker/server/compose.synology.yaml, and
packaging/server/server.json from the revision matching my supplied image.
The Web interface is embedded in the Server. For this Linux/NAS workflow,
deploy one Linux container, not a separate Web container or the native
Windows Server package. Use the complete Linux image, including FFmpeg,
pyatv, and its Python runtime for the selected architecture; do not deploy
only the Go executable or install these media dependencies on the host.
Complete the setup with me; do not stop at general advice or ask me to
write configuration files. Start with server access and the music path.
Inspect the host to fill in technical details, propose safe defaults for
the remaining choices, and ask me only for preferences or decisions that
affect access, data, or service availability. Explain one stage at a time.

1. Collect the information needed for this host before making changes.
   Ask only for details you cannot establish from my answers or authorized
   read-only inspection. Group related questions and explain safe defaults:
   - Server/NAS address, an existing SSH profile or SSH user and port,
     permitted access method, Linux/DSM version, CPU architecture, and
     whether Docker/Container Manager and Compose are available.
   - The LAN address browsers and audio outputs can reach; desired HTTP(S)
     port, port conflicts, and UPnP/DLNA or AirPlay outputs to use.
     Ask about HTTPS certificate/key file locations if HTTPS is needed;
     never ask me to paste private key contents.
   - The existing absolute music-folder path(s) ON THE SERVER, not my PC.
     Confirm it exists, including any network mount, and that UID/GID
     10001:10001 can read files and traverse directories.
   - Separate persistent project/config/data paths, available disk space,
     and whether jastreamer is already installed or playing. Identify any
     accounts, queue, playlists, credentials, and backups to preserve.
   - The target preview from https://github.com/furyheimdall/jastreamer/releases.
     Retrieve its exact ghcr.io/furyheimdall/jastreamer-server digest,
     checksums, and source revision from the release metadata.
     Public images need no registry password. Ask for an artifact location
     only if I choose a separately supplied offline/private package.
     If a published artifact or its verification evidence is unavailable,
     explain the missing prerequisite; do not invent an image URL or digest,
     use a floating tag, or silently substitute another build.
   Never collect secrets in chat, generated files, Git, or logs. Use
   existing secure credential handling or let me enter secrets privately.

2. Inspect first and show me a concrete installation plan for approval.
   State the exact image/architecture, project and backup locations,
   host-to-container mounts, listener/LAN URL, required permissions and
   network access, and any downtime. Support Linux amd64 or arm64 only.
   Propose all application settings together: server name, library roots,
   HTTP/HTTPS, AirPlay enablement, LAN interfaces/access restrictions, media
   base URL and transcoding, and my preferred UI language. Keep documented
   defaults unless my requirements or inspected network justify a change;
   explain any non-default value instead of asking me to understand JSON.
   Resolve real paths and reject overlapping config, data, and music
   locations, including nesting or symlinks that could expose application
   state as music. Never create a missing music path as an empty fallback.
   Ask separately before installing Docker, changing host permissions or
   firewall rules, or stopping/replacing an existing service.

3. After approval, prepare persistent Compose configuration using the
   repository template and actual values for JASTREAMER_SERVER_IMAGE,
   JASTREAMER_CONFIG_PATH, JASTREAMER_DATA_PATH, and JASTREAMER_MUSIC_PATH.
   Write the complete Compose file, persistent environment values, and
   server.json yourself, handling special characters in paths safely.
   Do not leave placeholders or make installation depend on exports in a
   temporary shell. If access is unavailable, provide ready-to-save files
   and exact commands for my confirmed paths, then verify the returned
   results before moving on.
   Verify the artifact and target architecture before import; follow the
   guide for OCI conversion rather than passing an OCI archive to docker
   load directly. Record the verified registry digest or local image ID.
   Keep UID/GID 10001:10001, read-only rootfs, dropped capabilities,
   no-new-privileges, tmpfs, and host networking. Do not add bridge port
   mappings; configure the listener in server.json and check port conflicts.
   Keep config writable at /etc/jastreamer, data writable at
   /var/lib/jastreamer, and existing music read-only at /music. Keep the
   configured data_dir and library_roots consistent with these container
   paths and retain the packaged FFmpeg/AirPlay helper paths.
   For additional approved music roots, add separate read-only mounts and
   matching library_roots entries rather than exposing a broader parent.
   Never use privileged mode, chmod 777, or recursive chown/chmod on music.
   Do not disable the firewall or expose the service to the public Internet.
   Never overwrite existing config/data or reset accounts. For an upgrade,
   obtain permission to stop playback and the service, back up complete
   config/data consistently with the service stopped, and retain the
   previous image and matching backup for rollback.
   Validate with docker compose config and the image's --check-config
   command using the prepared mounts before starting the service.

4. Start the approved deployment and verify actual results.
   Check container state/logs, /healthz, and the Web page from a client
   device, not just localhost. Verify UID, security settings, read-only
   music, writable config/data, and the configured library mount.
   Let me create the first administrator account in the browser privately;
   if an account already exists, use login rather than setup/reset.
   After private login, use the authorized browser session to apply the
   agreed UI language and finish settings, start a library scan, and wait
   for its result. If browser access is unavailable, guide me through the
   exact remaining steps and check the outcome. Select the output I choose
   only while stopped; do not pick or pair a receiver silently.
   Confirm library contents and output discovery without starting playback.
   Ask before pairing a receiver or sending playback commands; a healthy
   container or discovered receiver is not proof of audible playback.

5. Report the access URL, exact image identity, saved configuration and
   storage locations, commands used, verification results, and backup/
   rollback steps without secrets. Distinguish completed work from any
   client-network or physical-audio checks I still need to perform.
   End with a short "Open and try it" guide using the actual verified LAN
   URL, including scheme and port, as a clickable link. Do not leave an
   example address, localhost, or 0.0.0.0 as the client access URL.
   Tell me to open it from a browser on the same LAN and log in; the
   optional desktop client can use that same URL.
   Invite me to check that my music is listed, select my intended output
   while stopped, and explicitly play a track to confirm audible sound.
   Then suggest checking pause/seek if supported and stopping playback.
   These are user-run checks, not permission for automatic playback.
   Explain the expected result of each check and ask me to report the
   failed step and error text without passwords or other secrets.
   If a prerequisite or user-only action is still missing, identify it
   explicitly rather than calling the setup complete.
   Preserve existing music and application state if any step fails.
```

</details>

The prompt is an installation workflow, not an unattended installer or permission to modify an existing deployment without review. The [user guide](INSTRUCTION.md) remains the reference for package handling and configuration.

## Updating

Linux Server updates replace the container image while preserving its config and data mounts. Native Windows Server updates verify and extract a new portable ZIP separately, then replace only package-owned files in the existing folder while preserving `server.json`, `data`, and `music`. Neither update reinstalls the application or clears its data. The Server-hosted Web interface updates with either target; FFmpeg and AirPlay are bundled only with the supported Linux container. Optional desktop executables remain separate ZIP or DEB updates.

There is currently no in-app update checker or automatic updater. Review an available release, download the exact verified target and its checksum, stop playback and the Server, and back up persistent state before replacement. After verification, open the existing Server URL and refresh the Web interface; playback does not resume automatically.

See [backup and upgrade](INSTRUCTION.md#6-backup-and-upgrade) for the Linux container procedure and [native Windows x64 portable Server](INSTRUCTION.md#native-windows-x64-portable-server) for Windows first-install and update instructions. Registry delivery applies to the Linux images; offline artifacts remain an alternative.

## Compatibility scope

Automatic discovery depends on multicast and on the Server, client, and output having suitable LAN routes. Output capabilities vary by device: a receiver may omit Pause or Seek, reject a format, or require authorization. Linux AirPlay has bounded protocol and integration coverage; native Windows Server supports UPnP/DLNA and does not bundle AirPlay. Native CI verification and functional operation on a tested Windows Server system are separate from code signing, production qualification, long-term hardware qualification, and universal receiver certification. Confirm discovery, controls, queue advance, and audible playback on the equipment you intend to use.

## License

jastreamer is licensed under the [Apache License 2.0](LICENSE). Packaged third-party components retain their own licenses; see `THIRD-PARTY-NOTICES.txt` in the supplied Server artifact set or `/usr/share/jastreamer/THIRD-PARTY-NOTICES.txt` in the container.
