# Android independent offline player implementation and qualification plan

Status: **implemented in the current Android, Server, and Web source; release qualification pending**. This describes source behavior, not a published production release or authorization to merge, sign, install, deploy, or publish it. CI APKs are development/test artifacts; physical-phone installation, networking, audible original/AAC playback, Bluetooth/headset behavior, and same-signer update preservation are not yet physically qualified. Current user behavior is summarized in the [user guide](INSTRUCTION.md#android-controls). [한국어](ANDROID_OFFLINE_PLAN.ko.md).

## 1. Latest decisions and superseded direction

The user's final correction on 2026-09-21 takes precedence over earlier choices. The product is **an independent player that imports music from servers, not an offline cache of a server account**.

| Decision | Current confirmed direction |
|---|---|
| D1 | Default entry is the bundled server-selection screen, with a direct **Listen to saved music** entry. Server discovery/connection and offline playback are separate paths. |
| D2 | Stored track/album/playlist screens and the playback engine are included in the app. Display, search, and playback require neither Server HTML, login, nor server responses. |
| D3 | Download tracks/albums/playlists from a server, with original and space-saving quality choices. |
| D4 | After verification and committed local import, music belongs to the device independently of server/account lifetimes. Server logout, account deletion, profile removal, or connection failure cannot lock or delete it. |
| D5 | Downloading a server playlist is a one-time snapshot import. Completed music/lists are not automatically synchronized, deleted, or refreshed. Users manage them in the offline player. |
| D6 | The device owns offline queue/playlists/position. Stored music from any source server can play together without updating shared Server queues, outputs, or playback. |
| D7 | Android is implemented first; Windows and iOS offline-player implementations/package changes are outside this scope. NAS originals remain unchanged and existing Server control remains available. |

**Superseded:** the default unified online/offline streaming surface, server-scoped offline access profiles, locking completed music on logout, automatic downloaded-playlist synchronization, and automatic upload of local errors on reconnect. Original and Server-advertised AAC-LC 256 kbps choices, default unmetered transfer policy, and the native screens below are implemented source behavior, not claims about a published or physically qualified release.

## 2. Entry screen and server discovery

```text
Launch → bundled server-selection screen
         ├─ Listen to saved music [always accessible]
         │  └─ Independent offline player
         │     ├─ Library: saved tracks / albums / artists / folders / search
         │     ├─ Playlists: locally stored lists
         │     ├─ Queue: device queue
         │     └─ Settings: device storage, language, diagnostics
         ├─ Download manager: user-requested transfers
         ├─ Saved servers → verify / login if needed → existing Server UI
         ├─ Nearby servers → verify / login → existing Server UI
         └─ Add by address → verify / login → existing Server UI
```

### Bundled server-selection screen

- This is the cold-start default, reusing Android discovery, recents, and manual addressing. Returning to a live Activity or rotating preserves its current screen.
- A prominent **Listen to saved music** action with local track/album counts appears near the top. Account identity and the last successful Server connection are not enabling conditions.
- The action works with an empty library; the local empty state offers **Connect to a server to download music**. Showing the local player never requires first signing in.
- Below it, show saved servers, current-LAN discovery, refresh/failure guidance, and complete HTTP(S) address input. Show server name/address and checking/reachable/unreachable status.
- Online access still verifies Server UUID/origin and authenticates to that server. Matching names alone do not justify credential reuse. Airplane mode or discovery failure affects only the server path, not saved music.
- Do not search the entire Internet or automatically change NAS exposure, VPN, or firewall configuration. Suspend screen-related discovery when entering the offline player; explicitly requested transfers remain separate jobs.

### Offline player and returning

- The header says **Saved music**, not a selected Server/account that appears to own playback, with **Servers** to return and an entry to **Downloads**.
- The player has **Library / Playlists / Queue / Settings**. Its library contains all locally stored music without a Server filter or login prerequisite.
- Visiting transfer status or a Server screen does not stop local playback. Navigation does not send Stop to other network outputs.
- The native layout covers phone/tablet/folding, landscape, large-font, TalkBack, and keyboard paths, with an 80dp mini-player, 48dp minimum hit targets, and system insets applied once. These device/accessibility paths remain release-qualification targets rather than physical-test claims.
- Account/logout controls belong to the server-connection context. Listening to independently stored music does not require entering an account menu.

## 3. Committed download completion is the ownership boundary

The implementation separates these lifecycles:

| Data | Owner and lifetime |
|---|---|
| Server profiles/sessions | Discovery and online access, isolated by origin/Server UUID/account |
| Incomplete transfer jobs | Bound to their originating server/account, selected quality and source version while authentication, transfer, and verification are needed |
| Completed local music/lists | Managed by local device IDs; never cascade-deleted with a server/account profile |

```text
Request from an authenticated server
 → receive temporary file
 → verify length/checksum/selected artifact
 → atomically commit local file, metadata, and local identity
 → expose in Saved music
 → remove transfer URL/credential associations and active server binding
```

- Completed tracks/albums/playlists use local IDs, file integrity information, copied display metadata, actual codec/quality, and local references. Server track/album/playlist/account IDs are not access, refresh, or deletion keys.
- Title, artist, album, disc/track numbers, and artwork are **local copies**, not remote artwork URLs fetched for each display. Missing images use placeholders without blocking audio.
- Completion history focuses on local filenames, completion time, and results. Do not retain remote endpoints, expiring media URLs, or account credentials in the completed library for playback, synchronization, or automatic redownload. Do not rewrite original audio merely to remove tags already embedded in it.
- Recover across filesystem/database interruption without losing committed music. Receiving the final byte is not completion; verification and committed local registration are the boundary.
- Resolve completion/logout races atomically: committed files survive and canceled jobs cannot reuse discarded credentials.

## 4. Importing and listening workflows

### Import tracks and albums from a server

1. Select/verify a server and log in.
2. Choose Download from a track row, album card, or details in the existing server library. Downloading does not start playback or alter the shared queue.
3. Review original/space-saving quality, track count, destination, and whether the current network policy will wait for an unmetered connection. Actual transfer bytes appear in Downloads as the job runs.
4. Follow progress in the server surface and bundled download manager. Permitted transfers can continue while listening to already saved music elsewhere in the app.
5. On completion, use **View in saved music**. Logging out of the server afterward leaves the music accessible and playable.

### Import a server playlist

1. Capture a consistent snapshot of the confirmed server list, order, and duplicates when the user requests the import.
2. Download the required audio and create a **local playlist**. Retain names/reasons for failed entries and show **7 of 10 tracks saved** for a partial import.
3. Failed/incomplete entries are not playable. Retry through Downloads only while a valid job remains. Once a job is canceled/closed, downloading again requires a new explicit server request.
4. A completed local list has no live relationship to its source playlist. Server additions/removals/reordering/deletion do not update it.
5. Downloading the Server playlist later is a **new import** and creates another local playlist, even when the name matches. It never automatically replaces or adds to an existing local list. Identical stored files may be reused only after checksum/quality verification.

### Listen in airplane mode

1. Launch → **Listen to saved music**, without waiting for server login, profile selection, or network probing.
2. Browse tracks/albums/artists or search local metadata. Music imported from different servers appears in the same local library.
3. Select a track or album and preserve order/duplicates in the device queue. Partial imports explicitly offer **Play 7 saved tracks**.
4. Expand the mini-player for full controls, seeking, previous/next, shuffle/repeat, and queue. Lock-screen/headset/Bluetooth actions execute locally.
5. Restore library/queue/position after app/device restart, but do not make sound before a user request.

### Manage locally

- Create local playlists and change their names/order/membership. Offline edits are never sent to servers.
- Distinguish **Remove from playlist**, **Delete local playlist**, and **Delete audio from device**. Editing a list does not silently erase shared audio files.
- Explicit audio deletion explains affected local lists and requests confirmation. Update all references consistently without changing NAS originals or server playlists.
- By default, defer deleting an in-use file until playback releases it. **Stop now and delete** is a separate explicit choice.

### Selection, deletion, and folder organization

- Long-press tracks, albums, or folders to enter selection mode. Multi-select and select-all in the current view show selected track count and deduplicated file size. Folder views include organization and move actions rather than being read-only.
- **Delete from device** is explicit permanent local deletion. Confirm track count, space reclaimed, affected playlists/queue. Do not promise trash, undo, or automatic server recovery. For playing music offer **Delete when playback ends / Stop now and delete / Cancel**.
- Keep a pending-deletion playing file until current use ends, then do not restart it through repeat mode. Actual deletion consistently removes references from local lists/queue. Report item-level failures rather than claiming an entire batch succeeded.
- **Folders** represent actual placement inside app-owned persistent music storage. Do not merely change virtual tags while claiming files moved. This is neither a whole-phone file manager nor NAS folder browsing.
- Support **New folder**, **Rename folder**, **Move selected tracks**, **Move folder**, and **Delete folder**, including subfolders, breadcrumbs, parent navigation, and destination selection. Default imports target the app's **Imported music** folder; users may choose another local folder when requesting a download.
- Move workflow: select → **Move** → choose or create a destination folder → complete. Keep local track IDs while updating actual paths and indexes, preserving playlists/queue/position without duplicating tracks. Moving an in-use file preserves its active handle and queued references rather than silently interrupting playback.
- Never overwrite a different same-named file. Offer **Move with another name / Skip / Cancel remaining**. Treat moves to the existing location and overlapping selections as no duplicate operation. Matching names never justify merging content.
- Folder moves/renames change paths, not title/artist/album tags or audio bytes. Album and folder views are different classifications; renaming a folder does not retag an album.
- Recover from process termination/disk failure during moves without losing originals or the index. If copying is necessary, validate the destination before removing the source. Show actual progress/success/failure.
- Restrict management to the app music root. Reject root escape, symlink traversal, and moving a folder into itself/its descendants. Do not expose internal partial downloads, databases, cookies, or other apps' files in the folder view.
- Track an active transfer's destination through a stable local folder ID. Follow moves/renames; if deleted, wait for a new destination. Never silently recreate a deleted folder or lose completed files.

## 5. Download controls and status

| State | Indicator/action |
|---|---|
| Request available | Download action; explain server connectivity/authentication/format capability requirements |
| Waiting | Network policy/connection/conversion/job reason; cancel |
| Active | Progress ring, percentage or received size; pause/cancel |
| Paused/failed | Distinguish user pause, storage, authentication, changed source, and transfer failure; permit appropriate retries |
| Imported | Locally saved and **View in saved music**; no account-lock state |
| Partial | Collection success/failure counts and access to saved tracks |

- Track/album/playlist list and detail download buttons have independent hit targets from playback/navigation and English/Korean labels without announcing every byte.
- The Server page reports only jobs requested by that live document; it does not infer a permanent global **Already saved** badge from a Server track ID or matching name.
- During a requested import, manifest checksum and actual quality may identify an existing verified local file and avoid duplicate storage. Same names or Server track IDs never merge different files. Repeated playlist/queue entries remain distinct even when they reference one verified local file.
- Server-hosted download controls invoke only a compatible Android's narrow import capability. This does not enable arbitrary file storage/native privileges for ordinary browsers or other shells.

## 6. Quality, transfer, and device storage

### Quality

- **Original:** preserve exact source bytes. This does not promise Android decoder compatibility or bit-perfect output. Explain unsupported formats rather than silently substituting another quality.
- **Space saving:** the Server-generated artifact is seekable AAC-LC at 256 kbps in M4A. It preserves mono/stereo, explicitly downmixes higher channel counts, supports 44.1/48kHz without unnecessary upsampling, and records the actual codec/quality/size.
- A low-bitrate original may be smaller than a conversion; distinguish estimates from actual sizes. The Server advertises AAC only when its configured media runtime supports it, so incompatible Servers offer Original only.
- Changing default quality does not modify stored music. A different quality requires a new explicit server import; users decide whether to keep/delete the previous local copy.

### Incomplete transfers

- Default to an unmetered network. The user must explicitly enable **Allow metered or mobile data**; a transfer waits when its current network does not meet that policy.
- Bound concurrent transfers/conversion jobs and prioritize existing playback resources. Downloading does not trigger another whole-library analysis.
- Only active jobs validate source version, Range conditions, and authentication for resumption. Never append changed source bytes to an old partial file. Do not endlessly retry authentication/TLS/Server-identity errors.
- Check temporary-file length/checksum before local registration. Transfer integrity does not prove every decoder can play the entire file. A known verification failure for the same source version blocks a new import with a reason, never retroactively changing an existing local copy.
- Use Android-version-appropriate durable work/notifications, not a media-playback foreground-service type as a substitute for transfer permissions. Do not guarantee restart after force-stop; reconcile incomplete jobs on next launch.

### Completed storage

- Put explicit imports in app-private **persistent storage**, not evictable temporary cache. Separate this from Server Web profiles, cookies, and recents.
- Manage files/local tracks/albums/playlists through local IDs/checksums, with no account/Server-profile cascade-deletion relationship.
- Do not LRU-evict downloads, replace them because the server changed, refresh expired URLs, or check online entitlements. The app never arbitrarily removes completed music instead of a user deletion action.
- Settings shows usage/free space, an optional download limit, individual/all-music deletion. Pause new transfers on shortage and let users choose cleanup. Even an unset limit requires free-space checks and a safety reserve.
- Separate cleanup of partial/app-owned temporary artifacts from completed music. Removing a local playlist reference does not automatically garbage-collect an audio file.
- This is not backup against app uninstall, Android data reset, or storage failure. Same-application-ID, same-signer in-place updates preserve saved audio, artwork, local playlists, queue/position, folders, and preferences. Uninstall or **Clear storage/data** erases the app-owned private library and state; never use either to force a signer-mismatched update. Export, backup, and arbitrary local-file import are separate scope.

## 7. Effects of authentication and server changes

| Event | Incomplete transfer | Completed offline music |
|---|---|---|
| Disconnection / NAS unavailable | Wait/resume under policy | Unaffected |
| Session expiration | Reauthenticate before new remote requests | Unaffected |
| Explicit server logout | Stop that session's transfers and end credential use | No lock, deletion, or playback stop |
| Server account deletion / permission revocation | Subsequent authenticated requests denied | No retroactive lock/deletion |
| Remove recent server shortcut | No effect; the isolated profile/session and affected jobs remain | Unaffected |
| Isolated profile unavailable or removed | Pending remote work cannot continue or reuse its credentials; reconcile any local commit already in progress | Unaffected |
| Server track/album/playlist edit or deletion | Handle referenced-version changes/failure | No automatic synchronization/deletion |
| Connect to another server/account | Isolate new jobs to its authorization | Existing local music remains available together |

- Downloaded access is separate from server account access. Anyone using this device/app can listen to music downloaded under a previous account. Logout does not hide/protect local music; use device-level access controls. Do not promise remote retrieval/revocation of completed copies.
- Distinguish an existing **Server-owned Android renderer**, which may need to stop on authentication loss, from the account-independent **offline player**. Server logout does not stop local-owned music and must not send arbitrary Stop to another network output.
- Offline server logout may end local credential use without claiming that the unreachable server session was revoked. Completed music is unaffected.
- Delete music through the offline player's explicit deletion actions. **Logout**, **Remove server**, and **Clear cache** must not become indirect delete-all-music operations.

## 8. Single Media3 engine and queues

- Reuse Media3 service/MediaSession with explicit **Server-renderer** versus **offline-player** ownership. Never run two engines or apply late Server commands to local playback.
- Existing Server-surface playback retains the shared queue/command/lease policy. This direction does not require converting online Server playback into independent device streaming.
- The offline player uses local files only. Missing/corrupt files do not trigger automatic NAS access/redownload. Position, next track, shuffle/repeat, and system controls execute on-device.
- Music originally downloaded from different servers can share one local queue. Persist local track/entry IDs, order, duplicates, current entry, and position. Provide move up/down, remove, shuffle, and repeat off/one/all. Volume remains an Android system control.
- Browsing other servers or controlling network outputs does not interrupt local music. Explicitly confirm ownership handoff only when assigning this Android as a Server output or starting local music while this Android is Server-owned.
- Confirm the previous owner's local engine stopped, preserve its state, then hand off. Do not fabricate a successful Server Stop without its response; old registrations/commands cannot control the new owner. Do not automatically clone Server playback into a local queue.
- Follow Android audio-focus/call/headphone/Bluetooth policies. Activity recreation must not restart the track. Process restart restores state without autoplay.

## 9. Implemented Server/app contracts

The current source defines the platform-neutral versioned v1 contract in [HTTP API contract](contracts/http-api) and uses it for Android imports. It is suitable for a future native iOS client, but this work adds **no iOS offline implementation**. Published older Servers may not provide it.

- **Authenticated downloads:** fetch original or converted media without changing shared playback queues or registering an output. Existing play-lease-bound `/media/` URLs are not permanent music identities.
- **Manifest/Range:** carry a consistent source version, selected quality, MIME, length, checksum, Range validator, and copied metadata/artwork. Remote identifiers needed during a job do not become lifecycle bindings in the completed library.
- **Collection snapshot:** atomically capture playlist name, order, revision, and duplicates at explicit import time without subscribing to later changes.
- **Optional conversion:** bound jobs, cancellation/status/failure, immutable artifacts, finite Server retention, and authorization. Expiration of a transfer artifact cannot affect a committed local file.
- **Narrow Android import interface:** only the verified current Server top-level document can request explicit downloads and necessary job state; it cannot request arbitrary URLs/paths/file listings/credentials or enumerate/delete the completed local library.
- **Capability negotiation:** a new import requires compatible v1 Server/Web support. An older Server retains its supported Server controls and cannot block Saved music or alter completed imports.
- **Local storage contract:** authenticated pending jobs and completed local files/database state are separate. The bundled Kotlin music UI starts without Server HTML or service-worker cache.

Do not generally disable remote Web arbitrary-download/external-navigation restrictions or the sandbox. Deny cross-origin redirects, invalid TLS, and changed Server identity. Plain HTTP remains limited to a trusted LAN.

## 10. Errors and diagnostics

- Distinguish transfer/authentication/storage/source-change/checksum/unsupported-format/local-decoding failures. Never show partial files as completed.
- Show missing/corrupt local files in the offline player and let users remove them or explicitly import again from a server. Do not retain a server binding for automatic repair.
- Only confirmed local-media decoding failure may mark the device queue entry failed and advance once to the next playable entry. Do not blindly skip permission/unknown errors. Repeat modes cannot loop indefinitely over failures; stop after a pass in which all candidates fail.
- Keep offline-player errors/file state **on-device**. Do not automatically upload on reconnection or reinterpret them as Server-renderer/NAS-source failures. Diagnostic sharing is a separate explicit user export.
- Record actual local track identity, codec/quality, and engine error without cookies/tokens/passwords. Existing Server-renderer history continues to apply to Server-owned playback.

## 11. Acceptance criteria

These remain release-qualification scenarios for the implemented source, not a claim that a production release or every physical-device scenario has passed. In particular, emulator/source coverage does not prove physical installation, audible output, reboot/update preservation, Bluetooth/headset behavior, folding/accessibility behavior, or live ownership-race handling.

| ID | Scenario and observable result |
|---|---|
| AC01 | First launch, Server outage, and airplane mode still open bundled server selection and Saved music. Neither login nor Server HTML is a prerequisite; empty-library state is clear. |
| AC02 | Preserve LAN discovery, recents, manual addresses, Server verification, login, and cancel. Failures in this path cannot block the local library. |
| AC03 | Track/album/playlist download request/progress/completion/partial-failure controls do not trigger playback/navigation/shared-queue changes. |
| AC04 | Original checksums match; actual displayed AAC quality supports seeking/full playback. NAS originals unchanged. |
| AC05 | After completion, airplane-mode app termination/relaunch and device reboot preserve search/select/seek/next without server requests or autoplay. |
| AC06 | Explicit server logout, session expiry, account deletion, and permission revocation preserve access to completed metadata/artwork/audio without login locks. |
| AC07 | Removing recent servers/profiles and clearing Web cookies/cache never deletes completed music. Another account can still use the same local library. |
| AC08 | Source track/album/playlist editing/deletion/reordering and later reconnection leave local files/lists/queue unchanged; no automatic sync/redownload. |
| AC09 | Colliding server track IDs do not overwrite local files. Music from multiple servers plays in one local queue/playlist with preserved order/duplicates. |
| AC10 | Local list creation/renaming/reordering/removal never reaches a server. List and file deletion differ; explicit file deletion consistently updates local references. |
| AC11 | Recover or clearly fail interrupted transfer/process exit/source changes/Range mismatch/checksum failure without exposing incomplete files as complete. |
| AC12 | Completion/logout/cancel races preserve committed files, prevent canceled credential reuse, and cannot leave partial registrations or lost files. |
| AC13 | Unmetered→metered transitions, shortage, limits, and cancellation obey policy without automatic completed-file deletion or infinite retries. |
| AC14 | Partial imports explicitly play saved tracks. Repeated requests reuse only verified checksum/quality matches, not guessed name/server-ID matches. |
| AC15 | Transfers stay bound to their originating server/account when another is browsed/logged into; completion enables local use without remote credential associations. |
| AC16 | Local playback survives browsing/logout/control of another network output. Explicit confirmation is required for this Android's engine handoff; late old commands cannot cross ownership. |
| AC17 | Real Android lock screen/background/Bluetooth/audio-focus/headphone-disconnect/rotation/recreation routes system actions only to the active owner. |
| AC18 | Fold7 folding, phone/tablet, landscape, large fonts, and TalkBack retain server selection, Saved music entry, downloads, playback, and deletion controls without duplicate insets. |
| AC19 | Invalid TLS/Server ID, external redirects, arbitrary paths/URLs, and untrusted-document requests cannot bypass import boundaries. Remote Web cannot enumerate/delete the entire local library. |
| AC20 | Same-signer updates preserve local music/lists/queue/position. Old-server capabilities do not affect completed music; explain separate uninstall/data-reset loss boundaries. |
| AC21 | Local decode errors/missing files remain local. No infinite repeat-failure loop, automatic diagnostic upload, automatic NAS reconnection/repair, or source-corruption declaration. |
| AC22 | Distinguish actual Android installation, transferred bytes, playback state, and audible original/AAC evidence; emulator/UI state alone is not physical playback proof. |
| AC23 | Multi-select/bulk-delete tracks/albums/folders with accurate counts/bytes/affected lists. Distinguish list/file removal, deferred/immediate current-track deletion, and partial failure; leave servers/other apps unchanged. |
| AC24 | Actual folder creation/rename/track and folder moves match UI placement while preserving local IDs/audio checksum/tags/playlists/queue/position. No silent playback interruption when moving in-use music. |
| AC25 | Name collisions/overlapping selections/process termination/disk failure during moves cause no overwrites, lost files, or false completion. Recover interrupted moves with filesystem/database consistency. |
| AC26 | Reject root escape/symlink traversal/descendant cycles. Track moved/deleted download destination folders correctly or wait, without recreating deleted folders or losing completed music. |

## 12. Implemented scope and remaining qualification

The current source contains the independent local library and ownership boundary; original/AAC v1 Server downloads and manifests; native entry, search, local playlists/duplicate-preserving queue, real private folders and crash-reconciled moves; download management and metered policy; verified local commit; deletion/deferred current-track deletion; and explicit single-Media3 ownership handoff without autoplay.

Release work must qualify AC01–AC26 without converting source or emulator results into physical-device claims, keep English/Korean instructions and the HTTP contract aligned, and publish only through a separately approved signing/distribution process. No public production release or physical-device qualification is asserted here.

Windows and iOS offline implementations, Web/PWA offline playback, cloud sync, DRM, server-side recall of completed copies, arbitrary local-file import/export/backup, and a phone casting media server remain outside scope. Data migration, merge, signing, physical installation, NAS replacement, and publication require separate authorization.
