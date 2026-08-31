import { createHash } from "node:crypto";
import { existsSync, lstatSync, mkdirSync, mkdtempSync, readFileSync, renameSync, rmSync, unlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, dirname, join } from "node:path";
import { inspectLayout, flattenImage, unpackArchive } from "./oci";
import { run } from "./process";
import { cleanup, importImage, runComposeReplacement, runPlatform } from "./runtime";
import type { Options, OwnedDockerResources } from "./types";

const hash = (path: string): string => createHash("sha256").update(readFileSync(path)).digest("hex");
type Publication = Readonly<{ staged: string; final: string }>;
export function publishAtomically(files: readonly Publication[], finalize: () => void = () => {}): void {
  const backups: { final: string; backup: string }[] = []; const promoted: string[] = [];
  try {
    for (const { final } of files) if (existsSync(final)) { const backup = `${final}.backup-${process.pid}`; renameSync(final, backup); backups.push({ final, backup }); }
    for (const { staged, final } of files) { renameSync(staged, final); promoted.push(final); }
    finalize();
    for (const item of backups) rmSync(item.backup, { recursive: true, force: true });
  } catch (error) {
    for (const final of promoted) rmSync(final, { recursive: true, force: true });
    for (const item of backups.reverse()) if (existsSync(item.backup)) renameSync(item.backup, item.final);
    throw error;
  }
}

type WorkspaceIdentity = Readonly<{ readonly device: number; readonly inode: number }>;
type CleanupSignal = "SIGINT" | "SIGTERM";
const cleanupSignals = ["SIGINT", "SIGTERM"] as const;

export function workspaceIdentity(work: string): WorkspaceIdentity {
  const entry = lstatSync(work);
  if (!entry.isDirectory() || entry.isSymbolicLink()) throw new Error("OWNED_WORKSPACE_IDENTITY_INVALID");
  return { device: entry.dev, inode: entry.ino };
}
export function removeWorkspace(work: string, expected?: WorkspaceIdentity): void {
  const entry = lstatSync(work, { throwIfNoEntry: false });
  if (entry === undefined) return;
  if (entry.isSymbolicLink()) { unlinkSync(work); return; }
  if (!entry.isDirectory()) throw new Error("OWNED_WORKSPACE_TYPE_INVALID");
  if (expected !== undefined && (entry.dev !== expected.device || entry.ino !== expected.inode)) throw new Error("OWNED_WORKSPACE_REPLACED");
  rmSync(work, { recursive: true, force: true });
}
export function installSignalCleanup(clean: () => void): () => void {
  let cleaning = false; let preservedSignal: CleanupSignal | undefined;
  const uninstall = (): void => { for (const signal of cleanupSignals) process.off(signal, handlers[signal]); };
  const handle = (signal: CleanupSignal): void => {
    preservedSignal ??= signal;
    if (cleaning) return;
    cleaning = true;
    try { clean(); } catch (error) {
      if (!(error instanceof Error)) throw error;
      process.stderr.write(`${error.message}\n`);
    } finally {
      uninstall();
      process.kill(process.pid, preservedSignal);
    }
  };
  const handlers = { SIGINT: (): void => handle("SIGINT"), SIGTERM: (): void => handle("SIGTERM") } as const;
  for (const signal of cleanupSignals) process.prependListener(signal, handlers[signal]);
  return uninstall;
}

function removeImages(tags: readonly string[]): Readonly<{ taskImagesRemoved: boolean; removedTags: readonly string[] }> {
  const failures: Error[] = [];
  const removedTags: string[] = [];
  for (const tag of tags) {
    if (!run("docker", ["image", "ls", "-q", "--filter", `reference=${tag}`], { quiet: true })) continue;
    try { run("docker", ["image", "rm", "-f", tag], { quiet: true }); removedTags.push(tag); } catch (error) {
      failures.push(error instanceof Error ? error : new Error(String(error)));
    }
  }
  const remainingTags = tags.filter((tag) => run("docker", ["image", "ls", "-q", "--filter", `reference=${tag}`], { quiet: true }));
  const taskImagesRemoved = remainingTags.length === 0;
  failures.push(...remainingTags.map((tag) => new Error(`TASK_IMAGE_REMAINS ${tag}`)));
  if (failures.length) throw new AggregateError(failures, "IMAGE_CLEANUP_FAILED");
  return { taskImagesRemoved, removedTags };
}

export async function execute(root: string, options: Options): Promise<Record<string, unknown>> {
  const token = `${process.pid}-${crypto.randomUUID()}`;
  const resources: OwnedDockerResources = { names: new Set<string>(), projects: new Set<string>(), volumes: new Set<string>() }; const tags: string[] = [];
  let work: string | undefined; let identity: WorkspaceIdentity | undefined;
  const cleanOwned = (): void => {
    const failures: Error[] = [];
    try { cleanup(resources, options.compose); } catch (error) { failures.push(error instanceof Error ? error : new Error(String(error))); }
    try { removeImages(tags); } catch (error) { failures.push(error instanceof Error ? error : new Error(String(error))); }
    if (work !== undefined) try { removeWorkspace(work, identity); } catch (error) { failures.push(error instanceof Error ? error : new Error(String(error))); }
    if (failures.length) throw new AggregateError(failures, "CONTAINER_QA_CLEANUP_FAILED");
  };
  const uninstallSignals = installSignalCleanup(cleanOwned);
  work = mkdtempSync(join(tmpdir(), `jastreamer-container-qa-${token}-`)); identity = workspaceIdentity(work);
  const stagedLayout = join(work, "server.oci"); const unpacked = join(work, "layout");
  try {
    run("docker", ["buildx", "build", "--platform", "linux/amd64,linux/arm64", "--sbom=true", "--provenance=mode=max",
      `--build-arg=VERSION=${options.version}`, `--build-arg=REVISION=${options.revision}`, `--build-arg=CREATED=${options.created}`,
      "-f", "apps/server/Dockerfile", "--output", `type=oci,dest=${stagedLayout}`, "."], { cwd: root });
    unpackArchive(stagedLayout, unpacked); const inspection = inspectLayout(unpacked);
    const runtimeFacts = []; const filesystemFacts = [];
    const expectedFiles = {
      licenseSha256: `sha256:${hash(join(root, "LICENSE"))}`,
      noticesSha256: `sha256:${hash(join(root, "packaging/container/THIRD_PARTY_NOTICES"))}`,
    };
    for (const image of [...inspection.images].sort((a, b) => b.platform.localeCompare(a.platform))) {
      const rootfs = join(work, `${image.platform.split("/")[1]}.tar`); const tag = `jastreamer-task17:${token}-${image.platform.split("/")[1]}`; tags.push(tag);
      filesystemFacts.push(flattenImage(unpacked, image, rootfs, expectedFiles));
      importImage(rootfs, image.platform, tag); runtimeFacts.push(await runPlatform({ platform: image.platform, tag }, work, resources));
    }
    const amd64Tag = tags.find((tag) => tag.endsWith("-amd64")); if (!amd64Tag) throw new Error("AMD64_IMAGE_NOT_IMPORTED");
    const replacement = await runComposeReplacement({ compose: options.compose, image: amd64Tag, workspace: work }, resources);
    const cleanupFacts = cleanup(resources, options.compose);
    const imageCleanup = removeImages(tags);
    const sbom = join(work, "server.sbom.json"); const provenance = join(work, "server.provenance.json"); const digestFile = join(work, "server.digest");
    writeFileSync(sbom, JSON.stringify(inspection.sbom, null, 2) + "\n"); writeFileSync(provenance, JSON.stringify(inspection.provenance, null, 2) + "\n"); writeFileSync(digestFile, `${inspection.digest}\n`);
    const results: Record<string, unknown> = { status: "passed", indexDigest: inspection.digest, version: options.version, revision: options.revision, created: options.created,
      manifestCount: inspection.images.length, manifests: inspection.images.map((item) => ({ platform: item.platform, digest: item.descriptor.digest, runnable: true })),
      referrers: inspection.attestations.map((item) => ({ subject: item.subject, predicateType: item.predicateType })), runtime: runtimeFacts, replacementPersistence: replacement,
      filesystem: filesystemFacts,
      cleanup: { ...cleanupFacts, ...imageCleanup, stagingRemoval: "atomic-publication-finalizer" },
      hostLimitation: "native amd64 unavailable on arm64 host; amd64 evidence is explicitly QEMU-emulated; arm64 evidence is native" };
    const resultFile = join(work, "results.json"); writeFileSync(resultFile, JSON.stringify(results, null, 2) + "\n");
    const sums = join(work, "SHA256SUMS"); const checksums: readonly (readonly [string, string])[] = [[stagedLayout, basename(options.layout)], [sbom, "server.sbom.json"], [provenance, "server.provenance.json"], [digestFile, "server.digest"]];
    writeFileSync(sums, checksums.map(([file, name]) => `${hash(file)}  ${name}`).join("\n") + "\n");
    const outputDir = dirname(options.output); mkdirSync(outputDir, { recursive: true }); mkdirSync(dirname(options.layout), { recursive: true });
    const publicationPairs: readonly (readonly [string, string])[] = [[stagedLayout, options.layout], [resultFile, options.output], [sbom, join(outputDir, "server.sbom.json")], [provenance, join(outputDir, "server.provenance.json")], [digestFile, join(outputDir, "server.digest")], [sums, join(outputDir, "SHA256SUMS")]];
    const publications = publicationPairs.map(([staged, final]) => ({ staged, final }));
    publishAtomically(publications, () => removeWorkspace(work, identity)); uninstallSignals(); return results;
  } catch (primary) {
    try { cleanOwned(); } catch (cleanupError) {
      throw new AggregateError([primary, cleanupError], "CONTAINER_QA_AND_CLEANUP_FAILED");
    } finally { uninstallSignals(); }
    throw primary;
  }
}
