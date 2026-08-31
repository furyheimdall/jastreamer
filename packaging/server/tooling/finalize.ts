import { createHash } from "node:crypto";
import { readdirSync, readFileSync, writeFileSync } from "node:fs";
import { basename, join } from "node:path";

const metadata = ["Apache-2.0.txt", "THIRD_PARTY_NOTICES", "server.cer", "fingerprint.txt", "trust.md", "remove-trust.md", "oci-inspection.json", "windows-msi-inspection.json", "linux-amd64-inspection.json", "linux-arm64-inspection.json", "promotion-ledger.json"];
const gitSourceUri = "git+https://github.com/furyheimdall/jastreamer";
const sourceInputUri = "https://github.com/furyheimdall/jastreamer/server-source-input@v1";
export function distributables(version: string): string[] {
  return [
    `jastreamer-server_${version}_windows_amd64.exe`, `jastreamer-server_${version}_windows_amd64.msi`,
    ...["amd64", "arm64"].flatMap((arch) => ["deb", "rpm"].map((ext) => `jastreamer-server_${version}_linux_${arch}.${ext}`)),
    `jastreamer-server_${version}_linux_amd64-arm64.oci`,
  ];
}
export const sourceArtifact = (version: string): string =>
  `jastreamer-server_${version}_source.tar.gz`;
function digest(path: string): string { return createHash("sha256").update(readFileSync(path)).digest("hex"); }
export type ReleaseIdentity = Readonly<{ gitRevision: string; sourceInputSha256: string }>;
export type ReleaseCoordinates = Readonly<{ version: string; tag: string }>;
export function releaseIdentity(gitRevision: string, sourceInputIdentity: string): ReleaseIdentity {
  if (!/^[0-9a-f]{40}$/.test(gitRevision)) throw new Error("GIT_REVISION_INVALID");
  const sourceInput = /^sha256:([0-9a-f]{64})$/.exec(sourceInputIdentity);
  if (sourceInput?.[1] === undefined) throw new Error("SOURCE_INPUT_IDENTITY_INVALID");
  return { gitRevision, sourceInputSha256: sourceInput[1] };
}
export function verifyReleaseIdentity(directory: string, identity: ReleaseIdentity): void {
  const manifest = JSON.parse(readFileSync(join(directory, "manifest.json"), "utf8"));
  if (manifest.sourceRevision !== identity.gitRevision) throw new Error("MANIFEST_GIT_REVISION_MISMATCH");
  if (manifest.sourceInputSha256 !== identity.sourceInputSha256) throw new Error("MANIFEST_SOURCE_INPUT_MISMATCH");
  const provenance = JSON.parse(readFileSync(join(directory, "PROVENANCE.intoto.json"), "utf8"));
  const materials = provenance.predicate?.buildDefinition?.resolvedDependencies;
  const gitMaterials = materials?.filter((material: Readonly<{ uri?: string }>) => material.uri === gitSourceUri) ?? [];
  const sourceInputMaterials = materials?.filter((material: Readonly<{ uri?: string }>) => material.uri === sourceInputUri) ?? [];
  if (gitMaterials.length === 0) throw new Error("PROVENANCE_GIT_MATERIAL_MISSING");
  if (sourceInputMaterials.length === 0) throw new Error("PROVENANCE_SOURCE_INPUT_MATERIAL_MISSING");
  if (gitMaterials.length !== 1) throw new Error("PROVENANCE_GIT_MATERIAL_INVALID");
  if (sourceInputMaterials.length !== 1) throw new Error("PROVENANCE_SOURCE_INPUT_MATERIAL_INVALID");
  const gitMaterial = gitMaterials[0]; const sourceInputMaterial = sourceInputMaterials[0];
  if (gitMaterial?.digest?.gitCommit !== identity.gitRevision) throw new Error("PROVENANCE_GIT_REVISION_MISMATCH");
  if (sourceInputMaterial?.digest?.sha256 !== identity.sourceInputSha256) throw new Error("PROVENANCE_SOURCE_INPUT_MISMATCH");
}
export function finalize(directory: string, release: ReleaseCoordinates, identity: ReleaseIdentity): void {
  const assets = [...distributables(release.version), sourceArtifact(release.version)]; const sboms = assets.map((name) => `${name}.spdx.json`);
  const provenance = "PROVENANCE.intoto.json"; const expected = [...assets, ...sboms, ...metadata, provenance];
  const subjects = assets.map((name) => ({ name, digest: { sha256: digest(join(directory, name)) } }));
  writeFileSync(join(directory, provenance), `${JSON.stringify({ _type: "https://in-toto.io/Statement/v1", subject: subjects, predicateType: "https://slsa.dev/provenance/v1", predicate: { buildDefinition: { buildType: "https://github.com/furyheimdall/jastreamer/server-release@v1", externalParameters: { tag: release.tag, publish: false }, resolvedDependencies: [{ uri: gitSourceUri, digest: { gitCommit: identity.gitRevision } }, { uri: sourceInputUri, digest: { sha256: identity.sourceInputSha256 } }] }, runDetails: { builder: { id: "local:componentctl/server-release" }, metadata: { invocationId: identity.gitRevision } } } }, null, 2)}\n`);
  const actual = readdirSync(directory).filter((name) => !["manifest.json", "SHA256SUMS"].includes(name)).sort();
  if (actual.join("\0") !== expected.sort().join("\0")) throw new Error(`RELEASE_ALLOWLIST_MISMATCH\nexpected=${expected.sort()}\nactual=${actual}`);
  const all = [...expected].sort();
  const manifest = { schema: 1, component: "server", version: release.version, tag: release.tag, sourceRevision: identity.gitRevision, sourceInputSha256: identity.sourceInputSha256, license: "Apache-2.0", image: `ghcr.io/furyheimdall/jastreamer-server:${release.version}`, floatingTags: false, publishReachable: false, artifacts: all.map((name) => ({ name, sha256: digest(join(directory, name)) })), hostLimitations: ["local Windows EXE is cross-compiled and unsigned", "local MSI is built and inspected by pinned wixl in an amd64 QEMU container", "clean-Windows trust/install behavior requires the GitHub Windows runner", "amd64 runtime smoke is QEMU-emulated on this arm64 host; arm64 is native"] };
  writeFileSync(join(directory, "manifest.json"), `${JSON.stringify(manifest, null, 2)}\n`);
  verifyReleaseIdentity(directory, identity);
  const sums = [...expected, "manifest.json"].sort().map((name) => `${digest(join(directory, name))}  ${basename(name)}`).join("\n");
  writeFileSync(join(directory, "SHA256SUMS"), `${sums}\n`);
}
