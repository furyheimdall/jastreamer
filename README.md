# jastreamer

jastreamer is a locally controlled music streaming system with three independently versioned products:

| Product | Directory | Prefix | Version Source |
| --- | --- | --- | --- |
| Server | `apps/server` | `jastreamer-server` | `apps/server/VERSION` |
| Control | `apps/control` | `jastreamer-control` | `apps/control/VERSION` |
| Renderer | `apps/renderer` | `jastreamer-renderer` | `apps/renderer/VERSION` |

Each product owns its dependencies, lockfile, version, changelog, and build entry point. The root tooling only orchestrates those entry points; it is not a package workspace. Contracts and tooling are shared machine-readable inputs, not implementation packages.

## License

Licensed under Apache-2.0. See [LICENSE](LICENSE).

## Component Ownership

- **Server** (`apps/server`): Go binary, catalog, playback state machine, acoustic analysis, pairing portal, HTTPS API.
- **Control** (`apps/control`): Flutter Web PWA, Windows MSIX, Android APK. Shared behavior model across platforms.
- **Renderer** (`apps/renderer`): Rust/WASAPI Windows binary. Protocol compatibility adapter.

## Build / Test / Package Commands

See individual `component.yaml` files for entry points.

- **Server**: `cd apps/server && go test ./... -count=1`
- **Control**: `cd apps/control && flutter test && flutter build web --release`
- **Renderer**: `cd apps/renderer && cargo test --locked && cargo clippy --locked --all-targets --all-features -- -D warnings`

## Version / Tag / Changelog Rules

- Full SemVer tags only: `server-vX.Y.Z`, `control-vX.Y.Z`, `renderer-vX.Y.Z`.
- Tag must exactly match the component's `VERSION` file.
- Changelog starts at the previous matching component tag.

## Artifacts

See `packaging/*/manifest.json` and `packaging/*/config.json` for exact names and records.

## Compatibility

See `tooling/fixtures/compatibility/released-peers.yaml` for the supported matrix.

## Release Dry-Runs

```sh
./tooling/componentctl release dry-run --component server --tag server-v1.2.3 --no-publish --output out/
./tooling/componentctl release dry-run --component control --tag control-v1.2.3 --no-publish --scenario android-in-place-upgrade --output out/
./tooling/componentctl release dry-run --component renderer --tag renderer-v1.2.3 --no-publish --scenario clean-windows-vm --output out/
```

## Server Operations

- DB backup/restore: Use SQLite online backup before migrations.
- First-admin bootstrap: `JSTREAMER_SETUP_SECRET` environment variable.
- Discovery and pairing: HTTPS portal at advertised address.
- Device revocation: Admin token required; token is single-use and expires.

## DSM / Synology

- Compose contract: Host networking, explicit advertised address, non-root.
- DS918+ (DSM 7.2.2) is the initial x86_64 candidate. Runtime certification is `candidate-pending-runtime-authorization`.
- Synology arm64 hardware is `unverified`.
- ARMv7 is unsupported. No SPK is provided.

## Control Web Deployment

Independent of Server. Can be served from any static host.

## Analyzer Licensing

Default pipeline uses commercially safe local DSP. No AGPL, non-commercial, or cloud model weights are included.

## Indexing Recovery

Incremental scan resumes from the last successful generation. Tombs are durable.

## Autoplay Reasons

- `STOP_ALBUM_COMPLETE`: Finite album exhausted.
- `STOP_NO_ALBUM`: No album anchor.
- `STOP_NO_SIGNAL`: No explicit head and no continuation policy.
- `STOP_AUTO_FAILURE_LIMIT`: Three generated failures without explicit intervention.

## Renderer Limitations

- Windows-only native build.
- Protocol compatibility is explicit (major + capabilities). No generic UPnP gapless/synchronized behavior is claimed.

## No-Publish Rehearsals

See `.omo/evidence/implementation/task-18/`, `task-19/`, `task-20/` for exact dry-run outputs.