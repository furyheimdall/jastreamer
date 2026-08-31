import { afterEach, describe, expect, test } from "bun:test";
import { mkdtemp, rm, unlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const script = join(import.meta.dirname, "task19-media-fixture.py"); const roots = [];
afterEach(async () => Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true }))));
const invoke = (args, env = process.env) => { const result = Bun.spawnSync(["python3", script, ...args], { env, stdout: "pipe", stderr: "pipe" }); return { exitCode: result.exitCode, stdout: result.stdout.toString(), stderr: result.stderr.toString() }; };
const workspace = async () => { const root = await mkdtemp(join(tmpdir(), "task19-media-generator-")); roots.push(root); return { root, catalog: join(root, "catalog") }; };

describe("Task19 deterministic valid-media fixture", () => {
  test("creates exact deterministic names and one valid disk-efficient WAV seed", async () => {
    const value = await workspace(); const created = invoke(["create", "--root", value.catalog, "--count", "101", "--strategy", "hardlink"]); expect(created.exitCode).toBe(0); const manifest = JSON.parse(created.stdout); expect(manifest).toMatchObject({ count: 101, first_path: "task19-000000.wav", middle_path: "task19-000050.wav", last_path: "task19-000100.wav", unique_inodes: 1, strategies: ["hardlink"] }); expect(manifest.seed_bytes).toBe(1748); expect(manifest.physical_bytes).toBeLessThan(manifest.logical_bytes); const verified = invoke(["verify", "--root", value.catalog, "--count", "101"]); expect(verified.exitCode).toBe(0); expect(JSON.parse(verified.stdout).seed_sha256).toBe(manifest.seed_sha256); expect(invoke(["cleanup", "--root", value.catalog]).exitCode).toBe(0); expect(await Bun.file(value.catalog).exists()).toBe(false);
  });

  test("falls back from unavailable hardlinks without changing path semantics", async () => {
    const value = await workspace(); const created = invoke(["create", "--root", value.catalog, "--count", "17", "--strategy", "auto"], { ...process.env, TASK19_FORCE_LINK_FAILURE: "1" }); expect(created.exitCode).toBe(0); const manifest = JSON.parse(created.stdout); expect(manifest.count).toBe(17); expect(manifest.unique_inodes).toBe(17); expect(manifest.strategies.every((strategy) => strategy === "reflink" || strategy === "copy")).toBe(true); expect(invoke(["verify", "--root", value.catalog, "--count", "17"]).exitCode).toBe(0);
  });

  test("rejects empty and header-only malformed seeds", async () => {
    const value = await workspace(); for (const [name, bytes] of [["empty.wav", new Uint8Array()], ["header.wav", new TextEncoder().encode("RIFF0000WAVE")]]) { const path = join(value.root, name); await writeFile(path, bytes); const result = invoke(["validate", "--path", path]); expect(result.exitCode, name).toBe(1); expect(result.stderr).toContain("TASK19_MEDIA_SEED_INVALID"); }
  });

  test("rejects a 99,999-path fixture at the qualification boundary", async () => {
    const value = await workspace(); const created = invoke(["create", "--root", value.catalog, "--count", "99999", "--strategy", "auto"]); expect(created.exitCode).toBe(0); expect(JSON.parse(created.stdout).count).toBe(99999); const result = invoke(["verify", "--root", value.catalog, "--count", "100000"]); expect(result.exitCode).toBe(1); expect(result.stderr).toContain("TASK19_MEDIA_PATH_SET_INVALID");
  });

  test("rejects missing duplicate or unexpected path sets", async () => {
    const value = await workspace(); expect(invoke(["create", "--root", value.catalog, "--count", "9"]).exitCode).toBe(0); await unlink(join(value.catalog, "task19-000008.wav")); await writeFile(join(value.catalog, "duplicate.wav"), "not media"); const result = invoke(["verify", "--root", value.catalog, "--count", "9"]); expect(result.exitCode).toBe(1); expect(result.stderr).toContain("TASK19_MEDIA_PATH_SET_INVALID");
  });
});
