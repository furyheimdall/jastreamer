import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { expect, test } from "bun:test";
import { validateRelease } from "./validator";

test("valid fixtures produce independent manifests", () => {
  const result = validateRelease("tooling/release/fixtures/valid");
  expect(result.ok).toBe(true);
  if (result.ok) expect(result.manifests.map((manifest) => manifest.component).sort()).toEqual(["control", "renderer", "server"]);
});

test("invalid fixture classes have deterministic codes", () => {
  const result = validateRelease("tooling/release/fixtures/invalid");
  expect(result).toEqual(expect.objectContaining({ ok: false }));
  if (!result.ok) expect(result.errors).toEqual(expect.arrayContaining(["TAG_VERSION_MISMATCH", "ANDROID_SIGNING_LINEAGE_INVALID", "CA_CAPABLE_CERTIFICATE"]));
});

test("all rejection classes are machine-coded", () => {
  const result = validateRelease("tooling/release/fixtures/invalid/all-rejection-classes.json");
  if (result.ok) throw new Error("expected invalid fixture");
  expect(result.errors).toEqual(expect.arrayContaining(["INVALID_TAG", "TAG_VERSION_MISMATCH", "DUPLICATE_ARTIFACT", "AAB_PUBLIC_ASSET", "MISSING_CERTIFICATE_FINGERPRINT", "CA_CAPABLE_CERTIFICATE", "WRONG_CODE_SIGNING_EKU", "PUBLISHER_MISMATCH", "PRIVATE_KEY_PRESENT"]));
});

test("changelog starts at the matching component tag", () => {
  const result = validateRelease("tooling/release/fixtures/valid");
  expect(result.ok).toBe(true);
  if (result.ok) expect(result.manifests.every((manifest) => manifest.changelog_start === manifest.previous_component_tag)).toBe(true);
});


type MutableSigning = { readonly [key: string]: string | boolean | undefined };
type MutableManifest = { readonly artifacts: string[]; readonly signing: MutableSigning; readonly windows_signing: MutableSigning; readonly [key: string]: unknown };

function rejects(component: string, changes: (manifest: MutableManifest) => void): readonly string[] {
  const dir = mkdtempSync("/tmp/release-contract-");
  const path = `tooling/release/fixtures/valid/${component}.json`;
  const manifest = JSON.parse(readFileSync(path, "utf8")) as MutableManifest;
  changes(manifest);
  writeFileSync(`${dir}/manifest.json`, JSON.stringify(manifest));
  const result = validateRelease(`${dir}/manifest.json`);
  rmSync(dir, { recursive: true, force: true });
  if (result.ok) throw new Error("expected invalid fixture");
  return result.errors;
}

test("exact assets reject missing platform packages and public AAB", () => {
  expect(rejects("server", (m) => { m.artifacts = m.artifacts.filter((a: string) => !a.includes("arm64.rpm")); })).toContain("REQUIRED_ARTIFACT_MISSING");
  const errors = rejects("control", (m) => { m.artifacts = ["jstreamer-control_0.1.0_android_universal.aab"]; });
  expect(errors).toEqual(expect.arrayContaining(["REQUIRED_ARTIFACT_MISSING", "AAB_PUBLIC_ASSET"]));
  expect(rejects("renderer", (m) => { m.artifacts = ["jstreamer-renderer_0.1.0_diagnostic.zip"]; })).toContain("REQUIRED_ARTIFACT_MISSING");
});

test("each Windows signing field is enforced for Control and Renderer", () => {
  for (const [field, value, code] of [["publisher", "Other", "PUBLISHER_MISMATCH"], ["fingerprint", "", "MISSING_CERTIFICATE_FINGERPRINT"], ["code_signing_eku", false, "WRONG_CODE_SIGNING_EKU"], ["ca", true, "CA_CAPABLE_CERTIFICATE"], ["private_key", true, "PRIVATE_KEY_PRESENT"]] as const) {
    expect(rejects("control", (m) => { m.windows_signing[field] = value; })).toContain(code);
    expect(rejects("renderer", (m) => { m.windows_signing[field] = value; })).toContain(code);
  }
});

test("Android identity, lineage, and key state are enforced", () => {
  for (const [field, value] of [["application_id", "wrong"], ["keystore_lineage", ""], ["private_key", true]] as const) expect(rejects("control", (m) => { m.signing[field] = value; })).toContain("ANDROID_SIGNING_LINEAGE_INVALID");
});

test("changelog and mutable tag rejection remain explicit", () => {
  expect(rejects("server", (m) => { m.changelog_start = "renderer-v0.0.9"; })).toContain("CHANGELOG_START_MISMATCH");
  expect(rejects("control", (m) => { m.mutable_full_tag = true; })).toContain("MUTABLE_FULL_TAG");
});
