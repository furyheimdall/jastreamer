# Jake Streamer

Jake Streamer is a locally controlled music streaming system with three independently versioned products:

| Product | Directory | Prefix |
| --- | --- | --- |
| Server | `apps/server` | `jstreamer-server` |
| Control | `apps/control` | `jstreamer-control` |
| Renderer | `apps/renderer` | `jstreamer-renderer` |

Each product owns its dependencies, lockfile, version, changelog, and build entry point. The root Makefile only orchestrates those entry points; it is not a package workspace. Contracts and tooling are shared machine-readable inputs, not implementation packages.

Licensed under Apache-2.0. See [LICENSE](LICENSE).
