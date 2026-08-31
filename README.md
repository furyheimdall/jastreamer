# jastreamer

jastreamer is a locally controlled music streaming system with independently versioned Server and Control products plus a CI-only Renderer test harness:

| Component | Distribution | Directory | Prefix | Version Source |
| --- | --- | --- | --- | --- |
| Server | product candidate; not yet publicly released | `apps/server` | `jastreamer-server` | `apps/server/VERSION` |
| Control | product candidate; not yet publicly released | `apps/control` | `jastreamer-control` | `apps/control/VERSION` |
| Renderer | foreground Windows CI/test harness; never a public product | `apps/renderer` | `jastreamer-renderer` | `apps/renderer/VERSION` |

Each product owns its dependencies, lockfile, version, changelog, and build entry point. The root tooling only orchestrates those entry points; it is not a package workspace. Contracts and tooling are shared machine-readable inputs, not implementation packages.

## License

Licensed under Apache-2.0. See [LICENSE](LICENSE).

## Component Ownership

- **Server** (`apps/server`): Go binary, catalog, playback state machine, acoustic analysis, pairing portal, HTTPS API.
- **Control** (`apps/control`): Flutter Web PWA, Windows MSIX, Android APK. Shared behavior model across platforms.
- **Renderer test harness** (`apps/renderer`): foreground Rust/WASAPI Windows test peer. It is CI-only, has no public release, service, tray, or autostart behavior.

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

## Installation and Usage

- [한국어 통합 사용자 매뉴얼](docs/user-manual.ko.md)
- [Synology DSM Server](docs/synology.md)
- [Server bootstrap and pairing](docs/server-pairing.md)
- [Windows Control](docs/control-windows.md)
- [Web Control](docs/control-web.md)
- [Android Control](docs/control-android.md)
- [Windows Renderer test harness](docs/renderer-windows.md)
- [Release targets and operations](docs/releasing.md)

## Compatibility

See `tooling/fixtures/compatibility/released-peers.yaml` for the supported matrix.

## Release Dry-Runs

```sh
./tooling/componentctl release dry-run --component server --tag server-v1.2.3 --no-publish --output out/
./tooling/componentctl release dry-run --component control --tag control-v1.2.3 --no-publish --scenario android-in-place-upgrade --output out/
# Renderer candidate generation is CI/test-only and must never publish.
./tooling/componentctl release dry-run --component renderer --tag renderer-v1.2.3 --no-publish --scenario clean-windows-vm --output out/
```

Documentation claims are checked against the versioned capability registry and executable receipt mappings:

```sh
bun tooling/docs/verify.mjs --claims docs/claims.json --receipt-schema tooling/qa/product-receipt.schema.json
```

## Server Operations

- DB backup/restore: Use SQLite online backup before migrations.
- First-admin bootstrap: `JASTREAMER_SETUP_SECRET` environment variable.
- Discovery and pairing: HTTPS portal at advertised address.
- Device revocation: Admin token required; pairing codes are single-use and expire.

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

## Renderer Test-Harness Limitations

- Windows amd64 foreground test process only; no public Renderer product or installer claim.
- Native WASAPI loopback qualification remains pending on the authorized runner.
- Protocol compatibility is explicit (major + capabilities). No generic UPnP, gapless, synchronized, or multi-room behavior is claimed.

Server and Control publication also remains blocked until the physical FiiO K17 V261+ and native Windows audio gates produce exact candidate-bound receipts. Emulator and workflow existence are not publication readiness.
