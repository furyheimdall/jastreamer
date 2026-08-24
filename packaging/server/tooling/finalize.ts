import { createHash } from "node:crypto";
import { readdirSync, readFileSync, writeFileSync } from "node:fs";
import { basename, join } from "node:path";

const metadata = ["Apache-2.0.txt", "THIRD_PARTY_NOTICES", "server.cer", "fingerprint.txt", "trust.md", "remove-trust.md", "oci-inspection.json", "windows-msi-inspection.json", "linux-amd64-inspection.json", "linux-arm64-inspection.json", "promotion-ledger.json"];
export function distributables(version: string): string[] {
  return [
    `jstreamer-server_${version}_windows_amd64.exe`, `jstreamer-server_${version}_windows_amd64.msi`,
    ...["amd64", "arm64"].flatMap((arch) => ["deb", "rpm"].map((ext) => `jstreamer-server_${version}_linux_${arch}.${ext}`)),
    `jstreamer-server_${version}_linux_amd64-arm64.oci`,
  ];
}
function digest(path: string): string { return createHash("sha256").update(readFileSync(path)).digest("hex"); }
export function finalize(directory: string, version: string, tag: string, revision: string): void {
  const assets = distributables(version); const sboms = assets.map((name) => `${name}.spdx.json`);
  const provenance = "PROVENANCE.intoto.json"; const expected = [...assets, ...sboms, ...metadata, provenance];
  const subjects = assets.map((name) => ({ name, digest: { sha256: digest(join(directory, name)) } }));
  writeFileSync(join(directory, provenance), `${JSON.stringify({ _type: "https://in-toto.io/Statement/v1", subject: subjects, predicateType: "https://slsa.dev/provenance/v1", predicate: { buildDefinition: { buildType: "https://github.com/furyheimdall/jake-streamer/server-release@v1", externalParameters: { tag, publish: false } }, runDetails: { builder: { id: "local:componentctl/server-release" }, metadata: { invocationId: revision, sourceIdentity: revision } } } }, null, 2)}\n`);
  const actual = readdirSync(directory).filter((name) => !["manifest.json", "SHA256SUMS"].includes(name)).sort();
  if (actual.join("\0") !== expected.sort().join("\0")) throw new Error(`RELEASE_ALLOWLIST_MISMATCH\nexpected=${expected.sort()}\nactual=${actual}`);
  const all = [...expected].sort();
  const manifest = { schema: 1, component: "server", version, tag, sourceRevision: revision, license: "Apache-2.0", image: `ghcr.io/furyheimdall/jstreamer-server:${version}`, floatingTags: false, publishReachable: false, artifacts: all.map((name) => ({ name, sha256: digest(join(directory, name)) })), hostLimitations: ["local Windows EXE is cross-compiled and unsigned", "local MSI is built and inspected by pinned wixl in an amd64 QEMU container", "clean-Windows trust/install behavior requires the GitHub Windows runner", "amd64 runtime smoke is QEMU-emulated on this arm64 host; arm64 is native"] };
  writeFileSync(join(directory, "manifest.json"), `${JSON.stringify(manifest, null, 2)}\n`);
  const sums = [...expected, "manifest.json"].sort().map((name) => `${digest(join(directory, name))}  ${basename(name)}`).join("\n");
  writeFileSync(join(directory, "SHA256SUMS"), `${sums}\n`);
}
