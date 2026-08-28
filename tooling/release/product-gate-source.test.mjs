import { describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { verifyCanonicalSource } from "./product-gate-source.mjs";

const sha256 = (value) => createHash("sha256").update(value).digest("hex");
const denied = (code, path) => ({ ok: false, code, path });
const stableRead = (root, path) => ({ bytes: readFileSync(join(root, path)) });
const paths = [
  "apps/renderer/src/audio.rs", "tooling/qa/windows-audio/receipt.mjs", "apps/server/internal/media/service.go",
  "apps/server/internal/upnp/client.go", "apps/server/internal/transcode/run.go", "apps/server/internal/playback/queue.go",
  "apps/control/lib/credential_vault.dart", "apps/control/lib/control_events.dart", "contracts/control-api/v3/schema.json",
  "contracts/renderer-protocol/v3/schema.json", "tooling/release/product-gate.mjs",
];

const fixture = () => {
  const root = mkdtempSync(join(tmpdir(), "source-policy-"));
  execFileSync("git", ["init", "-q", root]); execFileSync("git", ["-C", root, "config", "user.email", "qa@example.invalid"]); execFileSync("git", ["-C", root, "config", "user.name", "QA"]);
  for (const path of paths) { mkdirSync(dirname(join(root, path)), { recursive: true }); writeFileSync(join(root, path), `${path}\n`); }
  writeFileSync(join(root, "policy.json"), JSON.stringify({ schemaVersion: 1, policyId: "test", includeRoots: ["apps", "contracts", "tooling"], includeRootFiles: ["policy.json"], excludePrefixes: [] }));
  execFileSync("git", ["-C", root, "add", "."]); execFileSync("git", ["-C", root, "commit", "-qm", "fixture"]);
  const context = { root, policyRoot: root }; const canonical = { sourceRoot: ".", sourcePolicyPath: "policy.json" }; const tools = { stableRead, denied, sha256 };
  return { root, context, canonical, tools };
};

describe("canonical source inclusion policy", () => {
  test("covers the complete current repository inventory with zero unexplained omissions", () => {
    const repository = resolve(import.meta.dirname, "../.."); const result = verifyCanonicalSource({ root: repository, policyRoot: repository }, { sourceRoot: ".", sourcePolicyPath: "tooling/release/product-gate-source-policy-v1.json" }, { stableRead, denied, sha256 });
    const expected = execFileSync("git", ["-C", repository, "ls-files", "--cached", "--others", "--exclude-standard"]).toString().split("\n").filter(Boolean).filter((path) => !path.startsWith(".omo/") && !path.startsWith("tooling/qa/node_modules/"));
    expect(result.ok).not.toBe(false); expect(result.files).toHaveLength(expected.length);
  });
  test("enumerates every relevant cached and nonignored file and observes formerly omitted production mutations", () => {
    const item = fixture();
    try {
      const before = verifyCanonicalSource(item.context, item.canonical, item.tools); expect(before.files).toEqual([...paths, "policy.json"].sort());
      for (const path of paths) { writeFileSync(join(item.root, path), `changed ${path}\n`); const after = verifyCanonicalSource(item.context, item.canonical, item.tools); expect(after.digest).not.toBe(before.digest); writeFileSync(join(item.root, path), `${path}\n`); }
    } finally { rmSync(item.root, { recursive: true, force: true }); }
  });
  test("rejects a current relevant nonignored file outside policy", () => {
    const item = fixture(); try { writeFileSync(join(item.root, "unexplained.txt"), "outside"); expect(verifyCanonicalSource(item.context, item.canonical, item.tools)).toEqual(expect.objectContaining({ code: "SOURCE_POLICY_OMISSION" })); } finally { rmSync(item.root, { recursive: true, force: true }); }
  });
});
