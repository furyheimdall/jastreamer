import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { afterEach, describe, expect, test } from "bun:test";

const script = resolve(import.meta.dirname, "../tooling/offline_package_config.py");
const roots: string[] = [];
const createFixture = (): Readonly<{ control: string; cache: string; flutter: string }> => {
  const root = mkdtempSync(join(tmpdir(), "control-offline-packages-")); roots.push(root); const control = join(root, "control"); const cache = join(root, "cache"); const flutter = join(root, "flutter"); mkdirSync(control); mkdirSync(join(cache, "hosted/pub.dev/foo-1.2.3"), { recursive: true }); mkdirSync(join(cache, "hosted-hashes/pub.dev"), { recursive: true }); mkdirSync(join(flutter, "packages/flutter"), { recursive: true });
  writeFileSync(join(control, "pubspec.yaml"), "name: control\nversion: 1.0.0\nenvironment:\n  sdk: '>=3.5.0 <4.0.0'\ndependencies:\n  flutter:\n    sdk: flutter\n  foo: 1.2.3\n");
  writeFileSync(join(control, "pubspec.lock"), `packages:\n  flutter:\n    dependency: direct main\n    description: flutter\n    source: sdk\n    version: 0.0.0\n  foo:\n    dependency: direct main\n    description:\n      name: foo\n      sha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"\n      url: https://pub.dev\n    source: hosted\n    version: 1.2.3\n`);
  writeFileSync(join(cache, "hosted/pub.dev/foo-1.2.3/pubspec.yaml"), "name: foo\nversion: 1.2.3\nenvironment:\n  sdk: ^3.2.0\n"); writeFileSync(join(cache, "hosted-hashes/pub.dev/foo-1.2.3.sha256"), `${"a".repeat(64)}\n`); writeFileSync(join(flutter, "packages/flutter/pubspec.yaml"), "name: flutter\nversion: 0.0.0\nenvironment:\n  sdk: '>=3.4.0 <4.0.0'\n"); return { control, cache, flutter };
};
const run = async (fixture: Readonly<{ control: string; cache: string; flutter: string }>): Promise<Readonly<{ code: number; stderr: string }>> => { const child = Bun.spawn(["python3", script, fixture.control, fixture.cache, fixture.flutter], { stdout: "ignore", stderr: "pipe" }); return { code: await child.exited, stderr: await new Response(child.stderr).text() }; };
afterEach(() => { for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true }); });

describe("Control offline package preflight", () => {
  test("generates only build-local cache and SDK mappings from the lock", async () => { const fixture = createFixture(); expect((await run(fixture)).code).toBe(0); const config: unknown = JSON.parse(readFileSync(join(fixture.control, ".dart_tool/package_config.json"), "utf8")); expect(config).toEqual({ configVersion: 2, packages: [{ name: "flutter", rootUri: new URL(`file://${fixture.flutter}/packages/flutter`).href, packageUri: "lib/", languageVersion: "3.4" }, { name: "foo", rootUri: new URL(`file://${fixture.cache}/hosted/pub.dev/foo-1.2.3`).href, packageUri: "lib/", languageVersion: "3.2" }, { name: "control", rootUri: "../", packageUri: "lib/", languageVersion: "3.5" }], generator: "jastreamer-offline-lock-v1" }); });
  test("rejects a missing locked cache package", async () => { const fixture = createFixture(); rmSync(join(fixture.cache, "hosted/pub.dev/foo-1.2.3"), { recursive: true }); expect(await run(fixture)).toEqual({ code: 65, stderr: "CONTROL_PACKAGE_CACHE_MISSING\n" }); });
  test("rejects a cache checksum that differs from the lock", async () => { const fixture = createFixture(); writeFileSync(join(fixture.cache, "hosted-hashes/pub.dev/foo-1.2.3.sha256"), `${"b".repeat(64)}\n`); expect(await run(fixture)).toEqual({ code: 65, stderr: "CONTROL_PACKAGE_CACHE_CHECKSUM_MISMATCH\n" }); });
  test("rejects a stale lock missing a direct dependency", async () => { const fixture = createFixture(); writeFileSync(join(fixture.control, "pubspec.yaml"), readFileSync(join(fixture.control, "pubspec.yaml"), "utf8") + "  missing: 1.0.0\n"); expect(await run(fixture)).toEqual({ code: 65, stderr: "CONTROL_PACKAGE_LOCK_STALE\n" }); });
});
