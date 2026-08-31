import { expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const sha256 = (path: string): string => createHash("sha256").update(readFileSync(path)).digest("hex");
const sha256Bytes = (bytes: string): string => createHash("sha256").update(bytes).digest("hex");

test("Server candidate evidence binds every claimed digest to exact staged bytes", () => {
  const candidate = mkdtempSync(join(tmpdir(), "jastreamer-server-evidence-"));
  const fixture = {
    "manifest.json": "{\"schema\":1}\n",
    "PROVENANCE.intoto.json": "{\"predicateType\":\"fixture\"}\n",
    "jastreamer-server_0.1.0_linux_amd64-arm64.oci": "fixture-oci\n",
    "oci-inspection.json": "{\"platforms\":[\"linux/amd64\",\"linux/arm64\"]}\n",
    "SHA256SUMS": "fixture checksums\n",
  } as const;
  try {
    for (const [name, bytes] of Object.entries(fixture)) writeFileSync(join(candidate, name), bytes);
    const evidence = {
      candidate: {
        manifestSha256: sha256Bytes(fixture["manifest.json"]),
        provenanceSha256: sha256Bytes(fixture["PROVENANCE.intoto.json"]),
        ociSha256: sha256Bytes(fixture["jastreamer-server_0.1.0_linux_amd64-arm64.oci"]),
        ociInspectionSha256: sha256Bytes(fixture["oci-inspection.json"]),
        sha256SumsSha256: sha256Bytes(fixture.SHA256SUMS),
      },
    };

    const observed = {
      manifestSha256: sha256(join(candidate, "manifest.json")),
      provenanceSha256: sha256(join(candidate, "PROVENANCE.intoto.json")),
      ociSha256: sha256(join(candidate, "jastreamer-server_0.1.0_linux_amd64-arm64.oci")),
      ociInspectionSha256: sha256(join(candidate, "oci-inspection.json")),
      sha256SumsSha256: sha256(join(candidate, "SHA256SUMS")),
    };

    expect(evidence.candidate).toEqual(observed);
    writeFileSync(join(candidate, "manifest.json"), "{\"schema\":2}\n");
    expect(sha256(join(candidate, "manifest.json"))).not.toBe(evidence.candidate.manifestSha256);
  } finally {
    rmSync(candidate, { recursive: true, force: true });
  }
});
