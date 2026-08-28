import { createHash } from "node:crypto";
import { chmodSync, copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, unlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import { preparePublication } from "./publication-contract";
import { createPromotionFixture } from "./product-gate-fixture.mjs";
import { verifyProductGate } from "./product-gate.mjs";
import type {
  AuthorizedProviderCommand,
  PreparedPublication,
  ProviderResult,
  PublicationDriver,
  PublicationRequest,
  PublicComponent,
} from "./publication-types";

const NOW = "2026-08-26T12:00:00.000Z";
const serverImage = "ghcr.io/furyheimdall/jastreamer-server";
const sha256 = (value: NodeJS.ArrayBufferView | string): string => createHash("sha256").update(value).digest("hex");

export type PublicationFixture = {
  readonly root: string;
  readonly gateRoot: string;
  readonly stageRoot: string;
  readonly request: PublicationRequest;
  readonly key: Buffer;
  readonly prepared: PreparedPublication;
  readonly cleanup: () => void;
};

export const createPublicationFixture = async (component: PublicComponent): Promise<PublicationFixture> => {
  const root = mkdtempSync(join(tmpdir(), `publication-${component}-`));
  const gateRoot = join(root, "gate");
  const fixture = await createPromotionFixture(gateRoot, NOW);
  const result = verifyProductGate(fixture.receiptPath, { root: gateRoot, now: NOW, profile: "fixture", trustConfigPath: fixture.trustConfigPath, mutationLedgerPath: fixture.mutationLedgerPath });
  if (!result.ok) throw new Error(result.code);
  const verifiedPath = join(gateRoot, "verified.json");
  writeFileSync(verifiedPath, `${JSON.stringify({ schemaVersion: 1, kind: "product_gate_verification", status: "authorized", ...result }, null, 2)}\n`);
  const stageRoot = join(root, "stage"); mkdirSync(stageRoot);
  for (const artifact of result.selection.filter((item) => item.kind.startsWith(`${component}-`))) copyFileSync(join(gateRoot, artifact.path), join(stageRoot, basename(artifact.path)));
  const candidate = result.publication.candidates[component];
  const request: PublicationRequest = {
    schemaVersion: 1, mode: "observe", component, repository: "furyheimdall/jastreamer", environment: "product-promotion",
    event: { name: "push", ref: `refs/tags/${candidate.releaseTag}`, refType: "tag", refName: candidate.releaseTag, sha: candidate.staging.headSha },
    publisherRun: { id: "9001", attempt: 2, actor: "publication-test" },
    gate: { root: gateRoot, receiptPath: "product-gate.json", verifiedPath: "verified.json", expectedReceiptSha256: result.productGateSha256 },
    stageRoot, receiptPath: join(root, "publication-receipt.json"), ...(component === "server" ? { dockerConfigRoot: join(root, "docker-auth") } : {}),
  };
  return { root, gateRoot, stageRoot, request, key: fixture.publicationReceiptKey, prepared: preparePublication(request, fixture.publicationReceiptKey), cleanup: () => rmSync(root, { recursive: true, force: true }) };
};

export type FakeDriverOptions = {
  readonly failAt?: string;
  readonly failAlwaysAt?: string;
  readonly sideEffectThenErrorAt?: string;
  readonly sideEffectDigest?: string;
  readonly swapStageDuringUpload?: boolean;
  readonly approvedAssetChangeDuringUpload?: "mutate" | "replace";
  readonly uploadedAssetLimit?: number;
  readonly providerAssetDigest?: string;
  readonly sideEffectReleaseBody?: string;
  readonly runConclusion?: string;
  readonly artifactDigest?: string;
  readonly mutateAfter?: Readonly<{ readonly commandId: string; readonly mutate: () => void }>;
};

export type ObservedAsset = { readonly name: string; readonly sha256: string; readonly size: number };

export class FakePublicationDriver implements PublicationDriver {
  readonly commands: AuthorizedProviderCommand[] = [];
  readonly releases = new Map<string, { draft: boolean; assets: string[]; body?: string }>();
  readonly uploadedAssets: ObservedAsset[] = [];
  readonly registry = new Map<string, string>([[`${serverImage}:0.9.0`, `sha256:${"9".repeat(64)}`]]);
  private readonly failAt: string | undefined;
  private readonly failAlwaysAt: string | undefined;
  private readonly sideEffectThenErrorAt: string | undefined;
  private readonly sideEffectDigest: string | undefined;
  private readonly swapStageDuringUpload: boolean;
  private readonly approvedAssetChangeDuringUpload: "mutate" | "replace" | undefined;
  private readonly uploadedAssetLimit: number | undefined;
  private readonly providerAssetDigest: string | undefined;
  private readonly sideEffectReleaseBody: string | undefined;
  private readonly runConclusion: string;
  private readonly artifactDigest: string | undefined;
  private readonly mutateAfter: FakeDriverOptions["mutateAfter"];
  private failed = false;

  constructor(private readonly prepared: PreparedPublication, options: FakeDriverOptions = {}) {
    this.failAt = options.failAt;
    this.failAlwaysAt = options.failAlwaysAt;
    this.sideEffectThenErrorAt = options.sideEffectThenErrorAt;
    this.sideEffectDigest = options.sideEffectDigest;
    this.swapStageDuringUpload = options.swapStageDuringUpload ?? false;
    this.approvedAssetChangeDuringUpload = options.approvedAssetChangeDuringUpload;
    this.uploadedAssetLimit = options.uploadedAssetLimit;
    this.providerAssetDigest = options.providerAssetDigest;
    this.sideEffectReleaseBody = options.sideEffectReleaseBody;
    this.runConclusion = options.runConclusion ?? "success";
    this.artifactDigest = options.artifactDigest;
    this.mutateAfter = options.mutateAfter;
  }

  private response(command: AuthorizedProviderCommand): ProviderResult {
    const staging = this.prepared.candidate.staging;
    const tag = this.prepared.candidate.releaseTag;
    if (command.id === "candidate-run") return { exitCode: 0, stdout: JSON.stringify({ id: Number(staging.callerRunId), event: staging.eventName, head_sha: staging.callerSha, head_branch: staging.callerRef.replace(/^refs\/heads\//, ""), run_attempt: staging.callerRunAttempt, conclusion: this.runConclusion, path: staging.workflowPath }), stderr: "" };
    if (command.id === "candidate-artifact") return { exitCode: 0, stdout: JSON.stringify({ id: Number(staging.artifactId), name: staging.artifactName, expired: false, digest: `sha256:${this.artifactDigest ?? staging.artifactDigest}`, workflow_run: { id: Number(staging.runId), head_sha: staging.headSha } }), stderr: "" };
    if (command.id === "release-preflight") return this.releases.has(tag) ? { exitCode: 0, stdout: "{}", stderr: "" } : { exitCode: 1, stdout: "", stderr: "HTTP 404: not found" };
    if (command.argv.some((value) => value.endsWith(`/releases/tags/${tag}`)) && command.id !== "release-preflight") {
      const release = this.releases.get(tag);
      return release === undefined ? { exitCode: 1, stdout: "", stderr: "HTTP 404: not found" } : { exitCode: 0, stdout: JSON.stringify({ tag_name: tag, draft: release.draft, body: release.body, assets: this.uploadedAssets.map((asset) => ({ name: asset.name, size: asset.size, digest: `sha256:${this.providerAssetDigest ?? asset.sha256}` })) }), stderr: "" };
    }
    if (command.id === "registry-preflight") {
      const tags = [...this.registry.keys()].filter((value) => value.startsWith(`${serverImage}:`)).map((value) => value.slice(`${serverImage}:`.length));
      return { exitCode: 0, stdout: JSON.stringify({ Repository: serverImage, Tags: tags }), stderr: "" };
    }
    if (command.id.startsWith("registry-prior-")) return { exitCode: 0, stdout: `${this.registry.get(`${serverImage}:${command.id.slice("registry-prior-".length)}`) ?? ""}\n`, stderr: "" };
    if (command.argv.includes("inspect")) {
      const reference = [...command.argv].reverse().find((value) => value.startsWith("docker://"));
      if (reference === undefined) return { exitCode: 1, stdout: "", stderr: "missing reference" };
      const found = this.registry.get(reference.slice("docker://".length));
      return found === undefined ? { exitCode: 1, stdout: "", stderr: "manifest unknown" } : { exitCode: 0, stdout: `${found}\n`, stderr: "" };
    }
    return { exitCode: 0, stdout: "", stderr: "" };
  }

  async run(command: AuthorizedProviderCommand): Promise<ProviderResult> {
    this.commands.push(command);
    if (command.id === this.failAlwaysAt || (command.id === this.failAt && !this.failed)) { this.failed = true; return { exitCode: 1, stdout: "", stderr: "injected provider failure" }; }
    const tag = this.prepared.candidate.releaseTag;
    if (command.id === "registry-login" && this.prepared.request.dockerConfigRoot !== undefined) {
      mkdirSync(this.prepared.request.dockerConfigRoot, { recursive: true, mode: 0o700 });
      writeFileSync(join(this.prepared.request.dockerConfigRoot, "config.json"), "current-run-registry-credentials", { mode: 0o600 });
    }
    if (command.id === "registry-logout" && this.prepared.request.dockerConfigRoot !== undefined) rmSync(join(this.prepared.request.dockerConfigRoot, "config.json"), { force: true });
    if (command.id === "release-create-draft") {
      const notes = command.argv.indexOf("--notes");
      this.releases.set(tag, { draft: true, assets: [], ...(notes < 0 ? {} : { body: this.sideEffectReleaseBody ?? command.argv[notes + 1] }) });
    }
    if (command.id === "release-upload-assets") {
      const selected = this.prepared.artifacts[0]; const original = selected === undefined ? undefined : readFileSync(selected.absolutePath);
      if (this.swapStageDuringUpload && selected !== undefined) writeFileSync(selected.absolutePath, "substituted after authorization");
      const uploadPaths = command.argv.filter((value) => value.startsWith("/"));
      const approvedPath = uploadPaths[0];
      if (approvedPath !== undefined && this.approvedAssetChangeDuringUpload !== undefined) {
        if (this.approvedAssetChangeDuringUpload === "mutate") chmodSync(approvedPath, 0o600);
        else unlinkSync(approvedPath);
        writeFileSync(approvedPath, "tampered approved snapshot");
      }
      this.uploadedAssets.splice(0);
      for (const path of uploadPaths.slice(0, this.uploadedAssetLimit)) {
        const bytes = readFileSync(path); this.uploadedAssets.push({ name: basename(path), sha256: sha256(bytes), size: bytes.byteLength });
      }
      if (original !== undefined && selected !== undefined) writeFileSync(selected.absolutePath, original);
      const release = this.releases.get(tag); if (release !== undefined) release.assets = this.uploadedAssets.map((item) => item.name);
    }
    if (command.id === "release-publish") { const release = this.releases.get(tag); if (release !== undefined) release.draft = false; }
    if (command.id === "release-cleanup") this.releases.delete(tag);
    if (command.id === "registry-copy-temporary" || command.id === "registry-copy-final") {
      const reference = [...command.argv].reverse().find((value) => value.startsWith("docker://"));
      if (reference !== undefined) this.registry.set(reference.slice("docker://".length), command.id === this.sideEffectThenErrorAt && this.sideEffectDigest !== undefined ? this.sideEffectDigest : this.prepared.verified.serverOci.indexDigest);
    }
    if (command.id === "registry-remove-temporary-before-commit" || command.id === "registry-cleanup-temporary" || command.id === "registry-cleanup-final") {
      const reference = [...command.argv].reverse().find((value) => value.startsWith("docker://"));
      if (reference !== undefined) this.registry.delete(reference.slice("docker://".length));
    }
    if (this.mutateAfter?.commandId === command.id) this.mutateAfter.mutate();
    if (command.id === this.sideEffectThenErrorAt) return { exitCode: 1, stdout: "", stderr: "injected error after provider side effect" };
    return this.response(command);
  }

  stateSha256(): string {
    return sha256(JSON.stringify({ releases: [...this.releases], registry: [...this.registry] }));
  }
}

export const readReceipt = (fixture: PublicationFixture): unknown => JSON.parse(readFileSync(fixture.request.receiptPath, "utf8"));
