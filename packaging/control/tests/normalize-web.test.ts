import { createHash } from "node:crypto";
import { mkdtempSync, mkdirSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, test } from "bun:test";
import { normalizeWebBuild } from "../tooling/normalize-web";

const md5 = (value: string | Buffer): string => createHash("md5").update(value).digest("hex");
const fixture = (buildId: string, serviceVersion: string): string => {
  const root = mkdtempSync(join(tmpdir(), "control-web-normalize-"));
  mkdirSync(join(root, "assets", ".release-web-fonts"), { recursive: true });
  mkdirSync(join(root, "assets", "assets", "fonts", "noto_sans_kr"), { recursive: true });
  mkdirSync(join(root, "canvaskit", "chromium"), { recursive: true });
  const files = new Map([["index.html", "<html>Control</html>"], ["main.dart.js", "console.log('control')"], ["assets/AssetManifest.bin", "manifest"], ["assets/.release-web-fonts/Roboto-Regular.ttf", "local font"], ["assets/assets/fonts/noto_sans_kr/NotoSansKR-wght.ttf", "local Korean font"], ["assets/assets/fonts/noto_sans_kr/OFL.txt", "font license"], ["assets/assets/fonts/noto_sans_kr/source.json", "font source"], ["canvaskit/canvaskit.js", "export const local = true;"], ["canvaskit/canvaskit.wasm", "local wasm"], ["canvaskit/chromium/canvaskit.js", "export const local = true;"], ["canvaskit/chromium/canvaskit.wasm", "local chromium wasm"]]);
  const bootstrap = `window._flutter = {buildConfig: {"useLocalCanvasKit":true}};\nloader({\n  serviceWorkerSettings: {\n    serviceWorkerVersion: "${serviceVersion}"\n  },\n  config: { fontFallbackBaseUrl: "assets/font-fallback-disabled/" }\n});\n`;
  files.set("flutter_bootstrap.js", bootstrap);
  for (const [path, body] of files) writeFileSync(join(root, path), body);
  const resources = Object.fromEntries([...files].map(([path, body]) => [path, md5(body)]));
  const indexDigest = resources["index.html"]; if (indexDigest === undefined) throw new Error("fixture index digest missing"); resources["/"] = indexDigest;
  writeFileSync(join(root, "flutter_service_worker.js"), `'use strict';\nconst RESOURCES = ${JSON.stringify(resources)};\n// The application shell files that are downloaded before a service worker can\n// start.\nconst CORE = ["main.dart.js","index.html","flutter_bootstrap.js"];\n`);
  writeFileSync(join(root, ".last_build_id"), buildId);
  return root;
};
const tree = (root: string): Record<string, string> => Object.fromEntries(readdirSync(root, { recursive: true, encoding: "utf8" }).filter((path) => statSync(join(root, path)).isFile()).map((path) => [path, createHash("sha256").update(readFileSync(join(root, path))).digest("hex")]));
const options = { version: "0.1.0", sourceIdentity: "sha256:" + "a".repeat(64), toolchainIdentity: "sha256:" + "b".repeat(64) };

describe("Control Web build normalizer", () => {
  test("normalizes distinct Flutter build IDs to identical idempotent trees", () => {
    const left = fixture("1".repeat(32), "123"); const right = fixture("2".repeat(32), "456");
    try { const first = normalizeWebBuild(left, options); const second = normalizeWebBuild(right, options); expect(first).toEqual(second); expect(tree(left)).toEqual(tree(right)); const before = tree(left); expect(normalizeWebBuild(left, options)).toEqual(first); expect(tree(left)).toEqual(before); expect(readdirSync(left)).not.toContain(".last_build_id"); expect(statSync(join(left, "main.dart.js")).mtime.toISOString()).toBe("1980-01-01T00:00:00.000Z"); } finally { rmSync(left, { recursive: true, force: true }); rmSync(right, { recursive: true, force: true }); }
  });

  test.each([
    ["malformed bootstrap", (root: string) => writeFileSync(join(root, "flutter_bootstrap.js"), "loader({});")],
    ["multiple service versions", (root: string) => writeFileSync(join(root, "flutter_bootstrap.js"), 'serviceWorkerVersion: "1"\nserviceWorkerVersion: "2"')],
    ["malformed service worker", (root: string) => writeFileSync(join(root, "flutter_service_worker.js"), "const RESOURCES = nope;")],
    ["stale resource digest", (root: string) => writeFileSync(join(root, "main.dart.js"), "changed")],
    ["path traversal", (root: string) => { const path = join(root, "flutter_service_worker.js"); writeFileSync(path, readFileSync(path, "utf8").replace('const RESOURCES = {', 'const RESOURCES = {"../escape":"d41d8cd98f00b204e9800998ecf8427e",')); }],
    ["unexpected generated file", (root: string) => writeFileSync(join(root, "unexpected.js"), "unexpected")],
    ["external font fallback", (root: string) => { const path = join(root, "flutter_bootstrap.js"); writeFileSync(path, readFileSync(path, "utf8").replace("assets/font-fallback-disabled/", "https://fonts.gstatic.com/s/")); }],
  ])("rejects %s", (_name, corrupt) => { const root = fixture("3".repeat(32), "789"); try { corrupt(root); expect(() => normalizeWebBuild(root, options)).toThrow(); } finally { rmSync(root, { recursive: true, force: true }); } });

  test("rejects absent local CanvasKit before accepting the service-worker inventory", () => { const root = fixture("4".repeat(32), "790"); try { rmSync(join(root, "canvaskit"), { recursive: true }); expect(() => normalizeWebBuild(root, options)).toThrow("CONTROL_WEB_NORMALIZE_LOCAL_CANVASKIT_REQUIRED"); } finally { rmSync(root, { recursive: true, force: true }); } });
  test("rejects an absent local fallback font", () => { const root = fixture("5".repeat(32), "791"); try { rmSync(join(root, "assets", ".release-web-fonts"), { recursive: true }); expect(() => normalizeWebBuild(root, options)).toThrow("CONTROL_WEB_NORMALIZE_LOCAL_FONT_REQUIRED"); } finally { rmSync(root, { recursive: true, force: true }); } });
  test("rejects a CDN fallback even when local CanvasKit exists", () => { const root = fixture("5".repeat(32), "791"); try { const path = join(root, "flutter_bootstrap.js"); writeFileSync(path, readFileSync(path, "utf8").replace('"useLocalCanvasKit":true', '"useLocalCanvasKit":false')); expect(() => normalizeWebBuild(root, options)).toThrow("CONTROL_WEB_NORMALIZE_CDN_FALLBACK"); } finally { rmSync(root, { recursive: true, force: true }); } });
});
