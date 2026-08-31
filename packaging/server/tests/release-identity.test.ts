import { describe, expect, test } from "bun:test";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { releaseIdentity, verifyReleaseIdentity } from "../tooling/finalize";

type ManifestIdentity = Readonly<{ sourceRevision: string; sourceInputSha256: string }>;
type ProvenanceIdentity = Readonly<{ gitRevision: string; sourceInputSha256: string; sourceInputUri: string }>;

const stableManifest = { sourceRevision: "a".repeat(40), sourceInputSha256: "b".repeat(64) };
const stableProvenance = {
  gitRevision: "a".repeat(40),
  sourceInputSha256: "b".repeat(64),
  sourceInputUri: "https://github.com/furyheimdall/jastreamer/server-source-input@v1",
};

function identityFixture(manifest: ManifestIdentity, provenance: ProvenanceIdentity) {
  const directory = mkdtempSync(join(tmpdir(), "server-release-identity-"));
  const identity = releaseIdentity("a".repeat(40), `sha256:${"b".repeat(64)}`);
  writeFileSync(join(directory, "manifest.json"), JSON.stringify(manifest));
  writeFileSync(join(directory, "PROVENANCE.intoto.json"), JSON.stringify({
    predicate: { buildDefinition: { resolvedDependencies: [
      { uri: "git+https://github.com/furyheimdall/jastreamer", digest: { gitCommit: provenance.gitRevision } },
      { uri: provenance.sourceInputUri, digest: { sha256: provenance.sourceInputSha256 } },
    ] } },
  }));
  return { directory, identity };
}

describe("Server release source identity", () => {
  test("binds Git revision independently from source-input identity", () => {
    expect(releaseIdentity("a".repeat(40), `sha256:${"b".repeat(64)}`)).toEqual({ gitRevision: "a".repeat(40), sourceInputSha256: "b".repeat(64) });
    expect(() => releaseIdentity("b".repeat(64), `sha256:${"b".repeat(64)}`)).toThrow("GIT_REVISION_INVALID");
    expect(() => releaseIdentity("a".repeat(40), "b".repeat(64))).toThrow("SOURCE_INPUT_IDENTITY_INVALID");
  });

  test.each([
    ["manifest Git revision", { ...stableManifest, sourceRevision: "c".repeat(40) }, stableProvenance, "MANIFEST_GIT_REVISION_MISMATCH"],
    ["manifest source-input identity", { ...stableManifest, sourceInputSha256: "c".repeat(64) }, stableProvenance, "MANIFEST_SOURCE_INPUT_MISMATCH"],
    ["provenance Git revision", stableManifest, { ...stableProvenance, gitRevision: "c".repeat(40) }, "PROVENANCE_GIT_REVISION_MISMATCH"],
    ["provenance source-input identity", stableManifest, { ...stableProvenance, sourceInputSha256: "c".repeat(64) }, "PROVENANCE_SOURCE_INPUT_MISMATCH"],
    ["canonical provenance source-input material", stableManifest, { ...stableProvenance, sourceInputUri: "https://example.invalid/source" }, "PROVENANCE_SOURCE_INPUT_MATERIAL_MISSING"],
  ])("rejects %s drift while every other binding remains stable", (_name, manifest, provenance, code) => {
    // Given: one independently drifted identity across the candidate documents.
    const fixture = identityFixture(manifest, provenance);
    try {
      // When: the candidate identity is checked against its immutable source inputs.
      const verify = () => verifyReleaseIdentity(fixture.directory, fixture.identity);
      // Then: the verifier reports the exact drifted binding.
      expect(verify).toThrow(code);
    } finally { rmSync(fixture.directory, { recursive: true, force: true }); }
  });

  test("rejects ambiguous duplicate canonical source-input materials", () => {
    const fixture = identityFixture(stableManifest, stableProvenance);
    try {
      const path = join(fixture.directory, "PROVENANCE.intoto.json");
      const provenance = JSON.parse(readFileSync(path, "utf8"));
      provenance.predicate.buildDefinition.resolvedDependencies.push({
        uri: stableProvenance.sourceInputUri,
        digest: { sha256: "c".repeat(64) },
      });
      writeFileSync(path, JSON.stringify(provenance));
      expect(() => verifyReleaseIdentity(fixture.directory, fixture.identity)).toThrow("PROVENANCE_SOURCE_INPUT_MATERIAL_INVALID");
    } finally { rmSync(fixture.directory, { recursive: true, force: true }); }
  });

  test("accepts cross-document Git and source-input identity consistency", () => {
    const fixture = identityFixture(stableManifest, stableProvenance);
    try { expect(() => verifyReleaseIdentity(fixture.directory, fixture.identity)).not.toThrow(); }
    finally { rmSync(fixture.directory, { recursive: true, force: true }); }
  });
});
