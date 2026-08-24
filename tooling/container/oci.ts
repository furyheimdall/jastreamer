import { createHash } from "node:crypto";
import { existsSync, lstatSync, mkdirSync, readFileSync, readdirSync, rmSync, unlinkSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { run } from "./process";
import type { AttestationRecord, Descriptor, FilesystemFact, ImageRecord, LayoutInspection, Manifest, Platform } from "./types";

const digest = (data: Buffer): string => `sha256:${createHash("sha256").update(data).digest("hex")}`;
const blobPath = (layout: string, value: string): string => join(layout, "blobs", ...value.split(":"));
function verifiedBlob(layout: string, descriptor: Descriptor): Buffer {
  const data = readFileSync(blobPath(layout, descriptor.digest));
  if (data.length !== descriptor.size || digest(data) !== descriptor.digest) throw new Error(`OCI_BLOB_INVALID ${descriptor.digest}`);
  return data;
}

export function unpackArchive(archive: string, directory: string): void {
  rmSync(directory, { recursive: true, force: true }); mkdirSync(directory, { recursive: true });
  run("tar", ["-xf", archive, "-C", directory], { quiet: true });
  if (!existsSync(join(directory, "oci-layout")) || !existsSync(join(directory, "index.json"))) throw new Error("OCI_LAYOUT_INVALID");
}

export function classifyDescriptors(descriptors: readonly Descriptor[]): { runnable: readonly Descriptor[]; attestations: readonly Descriptor[] } {
  const attestations = descriptors.filter((item) => item.annotations?.["vnd.docker.reference.type"] === "attestation-manifest");
  const runnable = descriptors.filter((item) => item.annotations?.["vnd.docker.reference.type"] !== "attestation-manifest");
  const platforms = runnable.map((item) => `${item.platform?.os}/${item.platform?.architecture}`).sort();
  if (runnable.length !== 2 || platforms.join(",") !== "linux/amd64,linux/arm64") throw new Error("RUNNABLE_MANIFEST_SET_INVALID");
  return { runnable, attestations };
}

export function inspectLayout(layout: string): LayoutInspection {
  let indexData: Buffer<ArrayBufferLike> = readFileSync(join(layout, "index.json"));
  let index = JSON.parse(indexData.toString()) as { schemaVersion: number; manifests: Descriptor[] };
  if (index.schemaVersion !== 2) throw new Error("OCI_INDEX_SCHEMA_INVALID");
  if (index.manifests.length === 1 && index.manifests[0]?.mediaType === "application/vnd.oci.image.index.v1+json") {
    indexData = verifiedBlob(layout, index.manifests[0]); index = JSON.parse(indexData.toString()) as typeof index;
  }
  if (index.schemaVersion !== 2 || index.manifests.some((item) => item.mediaType === "application/vnd.oci.image.index.v1+json")) throw new Error("OCI_INDEX_NESTING_INVALID");
  classifyDescriptors(index.manifests);
  const images: ImageRecord[] = []; const attestations: AttestationRecord[] = [];
  for (const descriptor of index.manifests) {
    const manifest = JSON.parse(verifiedBlob(layout, descriptor).toString()) as Manifest;
    const isAttestation = descriptor.annotations?.["vnd.docker.reference.type"] === "attestation-manifest";
    if (!isAttestation) {
      const platform = `${descriptor.platform?.os}/${descriptor.platform?.architecture}` as Platform;
      if (platform !== "linux/amd64" && platform !== "linux/arm64") throw new Error(`RUNNABLE_PLATFORM_INVALID ${platform}`);
      const config = JSON.parse(verifiedBlob(layout, manifest.config).toString()) as Record<string, unknown>;
      validateConfig(config, platform); images.push({ platform, descriptor, manifest, config }); continue;
    }
    const subject = descriptor.annotations?.["vnd.docker.reference.digest"] ?? "";
    for (const layer of manifest.layers) {
      const statement = JSON.parse(verifiedBlob(layout, layer).toString()) as Record<string, unknown>;
      const predicateType = String(statement.predicateType ?? layer.annotations?.["in-toto.io/predicate-type"] ?? "");
      if (predicateType !== "https://spdx.dev/Document" && !predicateType.startsWith("https://slsa.dev/provenance/")) throw new Error(`ATTESTATION_TYPE_INVALID ${predicateType}`);
      attestations.push({ subject, predicateType, statement });
    }
  }
  images.sort((a, b) => a.platform.localeCompare(b.platform));
  if (images.length !== 2 || images[0]?.platform !== "linux/amd64" || images[1]?.platform !== "linux/arm64") throw new Error("RUNNABLE_MANIFEST_SET_INVALID");
  for (const image of images) for (const type of ["https://spdx.dev/Document", "https://slsa.dev/provenance/"]) {
    if (!attestations.some((item) => item.subject === image.descriptor.digest && (type.endsWith("/") ? item.predicateType.startsWith(type) : item.predicateType === type))) throw new Error(`ATTESTATION_MISSING ${image.platform} ${type}`);
  }
  if (attestations.length !== 4) throw new Error(`ATTESTATION_COUNT_INVALID ${attestations.length}`);
  const metadata = images.map((image) => { const labels = (image.config.config as Record<string, unknown>).Labels as Record<string, string>; return [labels["org.opencontainers.image.version"], labels["org.opencontainers.image.revision"], labels["org.opencontainers.image.created"], labels["org.opencontainers.image.base.name"]].join("\0"); });
  if (!metadata[0] || metadata[0] !== metadata[1]) throw new Error("CROSS_MANIFEST_METADATA_MISMATCH");
  return { digest: digest(indexData), images, attestations,
    sbom: { statements: attestations.filter((item) => item.predicateType === "https://spdx.dev/Document").map((item) => item.statement) },
    provenance: { statements: attestations.filter((item) => item.predicateType.startsWith("https://slsa.dev/provenance/")).map((item) => item.statement) } };
}

function validateConfig(config: Record<string, unknown>, platform: Platform): void {
  const expectedArch = platform.split("/")[1];
  if (config.os !== "linux" || config.architecture !== expectedArch) throw new Error(`IMAGE_CONFIG_PLATFORM_INVALID ${platform}`);
  const runtime = config.config as Record<string, unknown>;
  const labels = runtime.Labels as Record<string, string>;
  if (runtime.User !== "10001:10001" || !Array.isArray(runtime.Entrypoint) || !String(runtime.Entrypoint[0]).includes("jstreamer-server")) throw new Error(`IMAGE_RUNTIME_CONFIG_INVALID ${platform}`);
  const required = ["org.opencontainers.image.version", "org.opencontainers.image.revision", "org.opencontainers.image.created", "org.opencontainers.image.base.name"];
  if (required.some((name) => !labels?.[name]) || labels?.["org.opencontainers.image.base.name"] !== "alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce" || labels?.["org.opencontainers.image.source"] !== "https://github.com/furyheimdall/jake-streamer" || labels?.["org.opencontainers.image.licenses"] !== "Apache-2.0") throw new Error(`IMAGE_LABELS_INVALID ${platform}`);
}

export function flattenImage(
  layout: string,
  image: ImageRecord,
  destination: string,
  expected: Readonly<{ licenseSha256: string; noticesSha256: string }>,
): FilesystemFact {
  const rootfs = `${destination}.rootfs`; rmSync(rootfs, { recursive: true, force: true }); mkdirSync(rootfs, { recursive: true });
  for (const layer of image.manifest.layers) { const file = blobPath(layout, layer.digest); verifiedBlob(layout, layer); run("tar", ["-xf", file, "-C", rootfs], { quiet: true }); applyWhiteouts(rootfs); }
  const inspected = inspectFilesystem(rootfs, image.platform, expected); run("tar", ["--numeric-owner", "-C", rootfs, "-cf", destination, "."], { quiet: true });
  rmSync(rootfs, { recursive: true, force: true });
  return inspected;
}

function applyWhiteouts(root: string): void {
  const visit = (directory: string): void => { for (const entry of readdirSync(directory)) { const path = join(directory, entry); if (lstatSync(path).isDirectory()) visit(path); else if (entry.startsWith(".wh.")) { const target = join(dirname(path), entry.slice(4)); rmSync(target, { recursive: true, force: true }); unlinkSync(path); } } };
  visit(root);
}
function inspectFilesystem(
  root: string,
  platform: Platform,
  expected: Readonly<{ licenseSha256: string; noticesSha256: string }>,
): FilesystemFact {
  const requiredPaths = ["usr/share/licenses/jstreamer/LICENSE", "usr/share/licenses/jstreamer/THIRD_PARTY_NOTICES", "app/migrations/001_catalog.sql", "app/migrations/002_playback.sql", "app/migrations/003_todo12.sql", "usr/local/lib/jstreamer-server"];
  for (const required of requiredPaths) if (!existsSync(join(root, required))) throw new Error(`IMAGE_FILE_MISSING /${required}`);
  const forbidden = /(^|\/)(apps\/control|flutter_assets|AssetManifest\.json)|\.(apk|aab|msix)$/i;
  let scannedEntries = 0;
  const walk = (directory: string): void => { for (const entry of readdirSync(directory)) { const path = join(directory, entry); scannedEntries++; if (forbidden.test(relative(root, path))) throw new Error(`FORBIDDEN_CONTROL_ASSET /${relative(root, path)}`); if (lstatSync(path).isDirectory()) walk(path); } };
  walk(root);
  const licenseSha256 = digest(readFileSync(join(root, requiredPaths[0]!)));
  const noticesPath = join(root, requiredPaths[1]!);
  const noticesSha256 = digest(readFileSync(noticesPath));
  const notices = readFileSync(noticesPath, "utf8").trim().split("\n").map((line) => JSON.parse(line) as Record<string, unknown>);
  if (licenseSha256 !== expected.licenseSha256 || noticesSha256 !== expected.noticesSha256 ||
    notices.length === 0 || notices.some((notice) => notice.component !== "server" ||
      typeof notice.package !== "string" || typeof notice.license_text !== "string")) {
    throw new Error(`IMAGE_LICENSE_CONTENT_INVALID ${platform}`);
  }
  return { platform, requiredPaths: requiredPaths.map((path) => `/${path}`), scannedEntries, forbiddenControlAssets: [],
    licenseSha256, noticesSha256, noticeEntries: notices.length };
}
