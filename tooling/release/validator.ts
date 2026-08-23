import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
export type ValidationResult = { readonly ok: true; readonly manifests: readonly ArtifactManifest[] } | { readonly ok: false; readonly errors: readonly string[] };
type Signing = { readonly publisher?: string; readonly fingerprint?: string; readonly ca: boolean; readonly code_signing_eku?: boolean; readonly application_id?: string; readonly keystore_lineage?: string; readonly private_key?: boolean };
export type ArtifactManifest = { readonly component: "server" | "control" | "renderer"; readonly version: string; readonly tag: string; readonly artifacts: readonly string[]; readonly license: "Apache-2.0"; readonly third_party_notices: string; readonly source_revision: string; readonly previous_component_tag?: string; readonly previous_repository_tag?: string; readonly changelog_start?: string; readonly mutable_full_tag?: boolean; readonly signing: Signing; readonly windows_signing?: Signing };
const semver = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/;
const prefixes = { server: "server-v", control: "control-v", renderer: "renderer-v" } as const;
const required = { server: ["jstreamer-server_{v}_windows_amd64.msi", "jstreamer-server_{v}_linux_amd64.deb", "jstreamer-server_{v}_linux_amd64.rpm", "jstreamer-server_{v}_linux_arm64.deb", "jstreamer-server_{v}_linux_arm64.rpm"], control: ["jstreamer-control_{v}_web.zip", "jstreamer-control_{v}_windows.msix", "jstreamer-control_{v}_android_universal.apk"], renderer: ["jstreamer-renderer_{v}_windows_amd64.msi", "jstreamer-renderer_{v}_diagnostic.zip"] } as const;
export function validateRelease(path: string): ValidationResult {
  const files = path.endsWith(".json") ? [path] : readdirSync(path).filter((f) => f.endsWith(".json")).map((f) => join(path, f)); const errors: string[] = []; const manifests: ArtifactManifest[] = []; const names = new Set<string>();
  for (const file of files) { let item: ArtifactManifest; try { item = JSON.parse(readFileSync(file, "utf8")) as ArtifactManifest; } catch { errors.push("MALFORMED_MANIFEST"); continue; } manifests.push(item); const expected = prefixes[item.component];
    if (!expected || !item.tag?.startsWith(expected) || item.mutable_full_tag || /^(latest|floating|v?\d+\.\d+)$/.test(item.tag)) errors.push(item.mutable_full_tag ? "MUTABLE_FULL_TAG" : "INVALID_TAG");
    if (!semver.test(item.version) || item.tag !== `${expected}${item.version}`) errors.push("TAG_VERSION_MISMATCH");
    if (item.license !== "Apache-2.0" || item.third_party_notices !== "THIRD_PARTY_NOTICES" || !item.source_revision) errors.push("METADATA_MISSING");
    if (!item.previous_component_tag?.startsWith(`${expected ?? ""}`) || !item.previous_repository_tag || item.previous_repository_tag.startsWith(`${expected ?? ""}`)) errors.push("PREVIOUS_TAG_RELATION_INVALID");
    if (item.changelog_start !== item.previous_component_tag) errors.push("CHANGELOG_START_MISMATCH");
    const expectedArtifacts = required[item.component]?.map((a) => a.replace("{v}", item.version)) ?? []; if (item.artifacts.length !== expectedArtifacts.length || expectedArtifacts.some((a) => !item.artifacts.includes(a))) errors.push("REQUIRED_ARTIFACT_MISSING");
    for (const artifact of item.artifacts) { if (names.has(artifact)) errors.push("DUPLICATE_ARTIFACT"); names.add(artifact); if (!expectedArtifacts.includes(artifact)) errors.push(artifact.endsWith(".aab") ? "AAB_PUBLIC_ASSET" : "CROSS_COMPONENT_ASSET"); }
    const win = item.windows_signing ?? item.signing; if (item.component === "server" || item.component === "control" || item.component === "renderer") { if (win.ca) errors.push("CA_CAPABLE_CERTIFICATE"); if (!win.fingerprint) errors.push("MISSING_CERTIFICATE_FINGERPRINT"); if (win.code_signing_eku !== true) errors.push("WRONG_CODE_SIGNING_EKU"); if (win.publisher !== "CN=Jake Streamer") errors.push("PUBLISHER_MISMATCH"); if (win.private_key) errors.push("PRIVATE_KEY_PRESENT"); }
    if (item.component === "control" && (item.signing.application_id !== "io.jakestreamer.control" || !item.signing.keystore_lineage || item.signing.private_key !== false)) errors.push("ANDROID_SIGNING_LINEAGE_INVALID");
  }
  for (const manifest of manifests) { if (manifest.previous_repository_tag && !manifests.some((sibling) => sibling.component !== manifest.component && manifest.previous_repository_tag.startsWith(prefixes[sibling.component]))) errors.push("PREVIOUS_REPOSITORY_TAG_INVALID"); }
  return errors.length ? { ok: false, errors: [...new Set(errors)] } : { ok: true, manifests };
}
