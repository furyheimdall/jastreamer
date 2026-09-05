import { afterEach, describe, expect, test } from "bun:test";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { collectPackageReceipt } from "./artifacts.ts";
import { installInjection } from "./injection.ts";
import { atomicWriteJson, filteredEnvironment } from "./io.ts";
import { createNamespaces, verifyCanary } from "./namespaces.ts";
import { parseArguments, parseFixture, parseScopeManifest } from "./parse.ts";
import { normalizeTrace, normalizeTraceFile, outsideAllowlist } from "./trace.ts";
import { createWorktree, removeWorktree } from "./worktree.ts";

const temporaryDirectories: string[] = [];
const temporaryDirectory = (): string => {
  const path = mkdtempSync(join(tmpdir(), "isolation-test-"));
  temporaryDirectories.push(path);
  return path;
};
afterEach(() => {
  for (const path of temporaryDirectories.splice(0)) rmSync(path, { recursive: true, force: true });
});

const validArgs = ["--component", "server,control,renderer", "--sparse", "--trace-files"] as const;

describe("isolation boundaries", () => {
  test("rejects malformed, duplicate, traversal, missing, and unknown options", () => {
    expect(() => parseArguments(["--component", "server,server", "--sparse", "--trace-files"])).toThrow();
    expect(() => parseArguments(["--component", "../server", "--sparse", "--trace-files"])).toThrow();
    expect(() => parseArguments(["--component", "server"])).toThrow();
    expect(() => parseArguments([...validArgs, "--wat"])).toThrow();
    expect(() => parseArguments(["--component", "server", "--component", "control", "--sparse", "--trace-files"])).toThrow();
  });

  test("accepts only an injection identifier in a failure fixture", () => {
    const fixture = parseFixture("tooling/fixtures/isolation/server-imports-control.yaml");
    expect(fixture).toEqual({ components: ["server", "control", "renderer"], injection: "server-imports-control" });
    const path = join(temporaryDirectory(), "fixture.json");
    writeFileSync(path, '{"injection":"server-imports-control","path":"apps/control"}');
    expect(() => parseFixture(path)).toThrow();
  });

  test("parses the authoritative manifest with exact component paths", () => {
    const manifest = parseScopeManifest("tooling/scope-manifest.yaml");
    expect(manifest.components.server.paths).toEqual([
      "apps/server", "contracts/control-api", "contracts/renderer-protocol", "contracts/locks/server.json",
      "tooling/fixtures/e2e/local.yaml", "tooling/fixtures/music", "tooling/qa/task19/task19-media-fixture.py",
      "tooling/isolation", "tooling/fixtures/isolation", "tooling/scope-manifest.yaml",
    ]);
    expect(manifest.components.control.paths).toEqual([
      "apps/control", "contracts/control-api", "contracts/locks/control.json",
      "tooling/isolation", "tooling/fixtures/isolation", "tooling/scope-manifest.yaml",
    ]);
    expect(manifest.components.renderer.paths).toEqual([
      "apps/renderer", "contracts/renderer-protocol", "contracts/locks/renderer.json",
      "tooling/isolation", "tooling/fixtures/isolation", "tooling/scope-manifest.yaml",
    ]);
  });

  test("rejects duplicate, absolute, and traversing manifest paths", () => {
    const root = temporaryDirectory();
    for (const paths of [["apps/server", "apps/server"], ["/apps/server"], ["apps/../control"]]) {
      const path = join(root, `${paths.length}-${paths[0]?.replaceAll("/", "_")}.json`);
      writeFileSync(path, JSON.stringify({ schema: 1, components: {
        server: { paths }, control: { paths: ["apps/control"] }, renderer: { paths: ["apps/renderer"] },
      } }));
      expect(() => parseScopeManifest(path)).toThrow();
    }
  });
});

describe("trace policy", () => {
  test("normalizes absolute, relative, AT_FDCWD, dirfd, and ENOENT accesses", () => {
    const root = "/repo";
    const worktree = "/tmp/tree";
    const trace = [
      '100 openat(AT_FDCWD</tmp/tree/apps/server/internal/api>, "../../go.mod", O_RDONLY) = 3</tmp/tree/apps/server/go.mod>',
      '100 openat(3</tmp/tree/apps>, "control/lib/main.dart", O_RDONLY) = -1 ENOENT (No such file or directory)',
      '100 stat("/tmp/tree/contracts/control-api/schema.json", 0x1) = 0',
      '100 access("/repo/apps/renderer/Cargo.toml", F_OK) = -1 ENOENT (No such file or directory)',
      '100 openat(AT_FDCWD, "/repo/.git/worktrees/tree/index", O_RDONLY) = 4',
    ].join("\n");
    const result = normalizeTrace(trace, { repositoryRoot: root, worktree, initialDirectory: "/tmp/tree/apps/server", namespaceRoot: "/tmp/run/namespaces", aliases: [] });
    expect(result.accessedPaths).toEqual([
      "apps/control/lib/main.dart", "apps/renderer/Cargo.toml", "apps/server/go.mod", "contracts/control-api/schema.json",
    ]);
    expect(result.missingPaths).toEqual(["apps/control/lib/main.dart", "apps/renderer/Cargo.toml"]);
    const path = join(temporaryDirectory(), "trace.strace");
    writeFileSync(path, trace);
    expect(normalizeTraceFile(path, { repositoryRoot: root, worktree, initialDirectory: "/tmp/tree/apps/server", namespaceRoot: "/tmp/run/namespaces", aliases: [] })).toEqual(result);
  });

  test("suppresses missing ancestor metadata but rejects successful nested git access", () => {
    const accessed = [".cargo", "apps/.cargo", "apps/.git", "apps/server/.git", "apps/control/.git"];
    const missing = [".cargo", "apps/.cargo", "apps/.git", "apps/server/.git"];
    const violations = outsideAllowlist(accessed, ["apps/server"], missing, "server");
    expect(violations).toEqual(["apps/control/.git"]);
    expect(outsideAllowlist([".cargo"], ["apps/renderer"], [], "renderer")).toEqual([".cargo"]);
  });

  test("normalizes in-container workspace and namespace aliases", () => {
    const trace = [
      '7 openat(AT_FDCWD</workspace/apps/control>, "lib/main.dart", O_RDONLY) = 3',
      '7 openat(AT_FDCWD</workspace/apps/control>, "/pub-cache/pkg", O_RDONLY) = 3',
      '7 openat(AT_FDCWD</workspace/apps/control>, "/other-cache/canary", O_RDONLY) = 3',
    ].join("\n");
    const result = normalizeTrace(trace, {
      repositoryRoot: "/repo", worktree: "/tmp/tree", initialDirectory: "/workspace/apps/control", namespaceRoot: "/run/namespaces",
      aliases: [{ traceRoot: "/workspace", hostRoot: "/tmp/tree" }, { traceRoot: "/pub-cache", hostRoot: "/run/namespaces/control/cache" }, { traceRoot: "/other-cache", hostRoot: "/run/namespaces/server/cache" }],
    });
    expect(result.accessedPaths).toEqual(["@namespaces/control/cache/pkg", "@namespaces/server/cache/canary", "apps/control/lib/main.dart"]);
    expect(outsideAllowlist(result.accessedPaths, ["apps/control"], result.missingPaths, "control")).toEqual(["@namespaces/server/cache/canary"]);
  });
});

describe("injection and namespace receipts", () => {
  test("creates an executable Server injection that really fails on absent Control", async () => {
    const root = temporaryDirectory();
    mkdirSync(join(root, "apps/server"), { recursive: true });
    writeFileSync(join(root, "apps/server/go.mod"), "module example.invalid/injection\n\ngo 1.25\n");
    installInjection(root, "server-imports-control");
    const process = Bun.spawn(["go", "test", "./internal/isolation"], {
      cwd: join(root, "apps/server"),
      stdout: "pipe",
      stderr: "pipe",
    });
    const [exitCode, stdout, stderr] = await Promise.all([
      process.exited,
      new Response(process.stdout).text(),
      new Response(process.stderr).text(),
    ]);
    expect(exitCode).toBe(1);
    expect(`${stdout}${stderr}`).toContain("injected sibling access failed");
  }, 60_000);

  test("detects post-command canary corruption", () => {
    const namespaces = createNamespaces(temporaryDirectory());
    expect(verifyCanary("server", namespaces).collisionFree).toBe(true);
    writeFileSync(namespaces.server.evidence.cachePath, "corrupt\n");
    expect(verifyCanary("server", namespaces).collisionFree).toBe(false);
  });

  test("records a real artifact and platform deferral", () => {
    const root = temporaryDirectory();
    writeFileSync(join(root, "jastreamer-renderer"), "binary");
    const receipt = collectPackageReceipt("renderer", root);
    expect(receipt.artifacts).toEqual([join(root, "jastreamer-renderer")]);
    expect(receipt.platformDeferrals).toEqual(["windows-msi:todo20"]);
  });

  test("CI runs happy and injected output validation", () => {
    const workflow = readFileSync(".github/workflows/isolation.yml", "utf8");
    expect(workflow).toContain("server-imports-control.yaml");
    expect(workflow).toContain("failure.json");
    expect(workflow).toContain("exit 65");
  });
});

describe("environment and output", () => {
  test("filters credentials and assigns native caches with unique canaries", () => {
    const root = temporaryDirectory();
    const env = filteredEnvironment("server", root, { PATH: "/bin", HOME: "/ambient/home", RUSTUP_HOME: "/ambient/rustup", AWS_SECRET_ACCESS_KEY: "secret", GITHUB_TOKEN: "secret" });
    expect(env["AWS_SECRET_ACCESS_KEY"]).toBeUndefined();
    expect(env["GITHUB_TOKEN"]).toBeUndefined();
    expect(env["GOCACHE"]).toStartWith(root);
    expect(env["GOMODCACHE"]).toStartWith(root);
    expect(env["ISOLATION_CANARY"]).toContain("server");
    expect(env["RUSTUP_HOME"]).toBe("/ambient/rustup");
  });

  test("atomically replaces output without leaving a temporary file", () => {
    const root = temporaryDirectory();
    const output = join(root, "result.json");
    writeFileSync(output, "old");
    atomicWriteJson(output, { ok: true });
    expect(JSON.parse(readFileSync(output, "utf8"))).toEqual({ ok: true });
    expect(readdirSync(root)).toEqual(["result.json"]);
  });

  test("writes a CLI output roundtrip atomically", () => {
    const repository = temporaryDirectory();
    const app = join(repository, "apps/server");
    const tooling = join(repository, "tooling");
    const binaries = temporaryDirectory();
    mkdirSync(app, { recursive: true });
    mkdirSync(tooling, { recursive: true });
    writeFileSync(join(app, "go.mod"), "module example.invalid/isolation\n\ngo 1.25\n");
    writeFileSync(join(tooling, "scope-manifest.yaml"), JSON.stringify({ schema: 1, components: {
      server: { paths: ["apps/server", "tooling/scope-manifest.yaml"] },
      control: { paths: ["apps/control"] }, renderer: { paths: ["apps/renderer"] },
    } }));
    const fakeGo = join(binaries, "go");
    writeFileSync(fakeGo, "#!/bin/sh\nwhile [ $# -gt 0 ]; do if [ \"$1\" = -o ]; then shift; mkdir -p \"$(dirname \"$1\")\"; : > \"$1\"; fi; shift; done\nexit 0\n");
    chmodSync(fakeGo, 0o755);
    for (const args of [["init", "-q"], ["config", "user.email", "test@example.invalid"], ["config", "user.name", "Test"], ["add", "."], ["commit", "-qm", "fixture"]]) {
      expect(Bun.spawnSync(["git", ...args], { cwd: repository }).exitCode).toBe(0);
    }
    const output = join(repository, "result.json");
    const cli = join(import.meta.dir, "cli.ts");
    const result = Bun.spawnSync([process.execPath, cli, "--component", "server", "--sparse", "--trace-files", "--output", output], {
      cwd: repository, env: { PATH: `${binaries}:${process.env["PATH"] ?? "/usr/bin:/bin"}`, HOME: process.env["HOME"] ?? "/tmp" }, stdout: "pipe", stderr: "pipe",
    });
    expect(result.exitCode, readFileSync(output, "utf8")).toBe(0);
    expect(result.stdout.toString()).toBe("");
    const parsed: unknown = JSON.parse(readFileSync(output, "utf8"));
    expect(parsed).toMatchObject({ ok: true, runDirectoryCleanup: "removed" });
    expect(readdirSync(repository).filter((path) => path.includes(".tmp-"))).toEqual([]);
  });
});

describe("worktree lifecycle", () => {
  test("materializes exact non-cone paths with sibling apps absent and cleans administration", () => {
    const repository = temporaryDirectory();
    expect(Bun.spawnSync(["git", "init", "-q"], { cwd: repository }).exitCode).toBe(0);
    expect(Bun.spawnSync(["git", "config", "user.email", "test@example.invalid"], { cwd: repository }).exitCode).toBe(0);
    expect(Bun.spawnSync(["git", "config", "user.name", "Test"], { cwd: repository }).exitCode).toBe(0);
    for (const component of ["server", "control", "renderer"]) {
      const directory = join(repository, "apps", component);
      Bun.spawnSync(["mkdir", "-p", directory]);
      writeFileSync(join(directory, "component.txt"), component);
    }
    expect(Bun.spawnSync(["git", "add", "."], { cwd: repository }).exitCode).toBe(0);
    expect(Bun.spawnSync(["git", "commit", "-qm", "fixture"], { cwd: repository }).exitCode).toBe(0);
    const before = Bun.spawnSync(["git", "worktree", "list", "--porcelain"], { cwd: repository }).stdout.toString();
    const path = join(temporaryDirectory(), "tree");
    const proof = createWorktree(repository, path, ["apps/server"]);
    expect(proof.detached).toBe(true);
    expect(proof.coneMode).toBe(false);
    expect(existsSync(join(path, "apps/server/component.txt"))).toBe(true);
    expect(existsSync(join(path, "apps/control"))).toBe(false);
    expect(existsSync(join(path, "apps/renderer"))).toBe(false);
    const cleanup = removeWorktree(repository, path, before);
    expect(cleanup.directory).toBe("removed");
    expect(cleanup.administration).toBe("restored");
  });
});
