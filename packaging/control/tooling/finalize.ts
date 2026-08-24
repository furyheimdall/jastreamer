import { createHash } from "node:crypto";
import { basename, join } from "node:path";
import { readFileSync, readdirSync, writeFileSync } from "node:fs";

const records = [
  "Android-CERT-SHA256.txt",
  "Apache-2.0.txt",
  "THIRD_PARTY_NOTICES",
  "android-upgrade-inspection.json",
  "control-windows.cer",
  "Windows-CERT-SHA256.txt",
  "trust.md",
  "remove-trust.md",
] as const;

const digest = (path: string): string => createHash("sha256").update(readFileSync(path)).digest("hex");

export function distributables(version: string): readonly string[] {
  return [
    `jastreamer-control_${version}_android_universal.apk`,
    `jastreamer-control_${version}_web.zip`,
    `jastreamer-control_${version}_windows.msix`,
  ];
}

export function finalize(directory: string, version: string, tag: string, revision: string): void {
  const assets = distributables(version);
  const subjects = assets.map((name) => ({ name, digest: { sha256: digest(join(directory, name)) } }));
  writeFileSync(join(directory, "SBOM.spdx.json"), `${JSON.stringify({
    spdxVersion: "SPDX-2.3",
    dataLicense: "CC0-1.0",
    SPDXID: "SPDXRef-DOCUMENT",
    name: "jastreamer Control release",
    packages: subjects.map((subject, index) => ({
      name: subject.name,
      SPDXID: `SPDXRef-Package-${index + 1}`,
      downloadLocation: "NOASSERTION",
      filesAnalyzed: false,
      licenseConcluded: "Apache-2.0",
      licenseDeclared: "Apache-2.0",
      checksums: [{ algorithm: "SHA256", checksumValue: subject.digest.sha256 }],
    })),
  }, null, 2)}\n`);
  writeFileSync(join(directory, "PROVENANCE.intoto.json"), `${JSON.stringify({
    _type: "https://in-toto.io/Statement/v1",
    subject: subjects,
    predicateType: "https://slsa.dev/provenance/v1",
    predicate: {
      buildDefinition: {
        buildType: "https://github.com/furyheimdall/jastreamer/control-release@v1",
        externalParameters: { tag, publish: false },
      },
      runDetails: {
        builder: { id: "local:componentctl/control-release" },
        metadata: { invocationId: revision, sourceIdentity: revision },
      },
    },
  }, null, 2)}\n`);
  const expected = [...assets, ...records, "SBOM.spdx.json", "PROVENANCE.intoto.json"].sort();
  const actual = readdirSync(directory).filter((name) => !["manifest.json", "SHA256SUMS"].includes(name)).sort();
  if (actual.some((name) => name.toLowerCase().endsWith(".aab"))) throw new Error("FORBIDDEN_AAB_ASSET");
  if (actual.join("\0") !== expected.join("\0")) {
    throw new Error(`RELEASE_ALLOWLIST_MISMATCH\nexpected=${expected}\nactual=${actual}`);
  }
  writeFileSync(join(directory, "manifest.json"), `${JSON.stringify({
    schema: 1,
    component: "control",
    version,
    tag,
    sourceRevision: revision,
    license: "Apache-2.0",
    publicArtifacts: assets,
    ciOnlyArtifacts: [`jastreamer-control_${version}_android.aab`],
    publishReachable: false,
    artifacts: expected.map((name) => ({ name, sha256: digest(join(directory, name)) })),
    hostLimitations: [
      "local Android update evidence verifies package/signing identity without an x86_64 emulator",
      "local Windows MSIX is cross-packaged and unsigned; protected native Windows signing and trust QA remain authoritative",
    ],
  }, null, 2)}\n`);
  const sums = [...expected, "manifest.json"].sort()
    .map((name) => `${digest(join(directory, name))}  ${basename(name)}`)
    .join("\n");
  writeFileSync(join(directory, "SHA256SUMS"), `${sums}\n`);
}
