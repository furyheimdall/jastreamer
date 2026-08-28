import { describe, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { loadTrustConfig } from "./product-gate-trust.mjs";

const hash = "a".repeat(64);
const valid = () => ({ schemaVersion: 1, profile: "production", trustPolicyVersion: "product-gate-trust-v1", rotationEpoch: 1, gate: { keyId: hash, publicKeyPath: "key.pem", algorithm: "Ed25519" }, artifactSigning: { keyIds: [hash], publicKeys: [{ keyId: hash, path: "artifact.pem" }] }, qualification: { todo19: {}, k17: {}, wasapi: {} }, builders: ["builder"], materialPolicy: { buildType: "build", sourceUri: "git+source" }, publication: { repository: "furyheimdall/jastreamer", environment: "product-promotion", receiptKeyId: hash }, canonical: { sourceRoot: ".", sourcePolicyPath: "policy.json", sourceRevision: "b".repeat(40), contracts: [], peers: [] } });
const denied = (code, path) => ({ ok: false, code, path });

describe("pinned production trust contract", () => {
  test.each([
    ["qualification", (value) => { value.qualification = {}; }], ["artifact keys", (value) => { value.artifactSigning.keyIds = []; value.artifactSigning.publicKeys = []; }],
    ["builders", (value) => { value.builders = []; }], ["material policy", (value) => { delete value.materialPolicy; }],
    ["gate", (value) => { delete value.gate.algorithm; }], ["canonical", (value) => { delete value.canonical.sourcePolicyPath; }],
  ])("returns typed production denial for missing or empty %s", (_name, mutate) => {
    const root = mkdtempSync(join(tmpdir(), "production-trust-")); const path = join(root, "tooling/release/product-gate-production-trust-v1.json");
    try { mkdirSync(dirname(path), { recursive: true }); const value = valid(); mutate(value); writeFileSync(path, JSON.stringify(value)); const result = loadTrustConfig({ profile: "production", trustConfigPath: path }, { bundle: root, repository: root }, { denied, readFile: (file) => readFileSync(file, "utf8") }); expect(result).toEqual(expect.objectContaining({ issue: expect.objectContaining({ code: "PRODUCTION_TRUST_INCOMPLETE" }) })); } finally { rmSync(root, { recursive: true, force: true }); }
  });
});
