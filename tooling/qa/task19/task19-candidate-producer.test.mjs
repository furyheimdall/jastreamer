import { afterEach, describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { produceTask19Closure } from "./task19-candidate-producer.mjs";

const roots = []; const revision = "a".repeat(40); const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");
const repositoryRoot = resolve(import.meta.dirname, "../../..");
const expandWorkflowJobs = async (workflowPath, ancestors = []) => {
  if (ancestors.includes(workflowPath)) throw new Error(`workflow call cycle: ${workflowPath}`);
  const workflow = Bun.YAML.parse(await Bun.file(workflowPath).text()); const expanded = [];
  for (const [job, value] of Object.entries(workflow.jobs)) {
    if (Array.isArray(value.steps)) { expanded.push({ job, workflowPath, value }); continue; }
    if (typeof value.uses !== "string" || !value.uses.startsWith("./.github/workflows/")) throw new Error(`workflow job has neither steps nor a local reusable call: ${workflowPath}#${job}`);
    const calledPath = resolve(repositoryRoot, value.uses.slice(2)); expanded.push(...await expandWorkflowJobs(calledPath, [...ancestors, workflowPath]));
  }
  return expanded;
};
afterEach(async () => Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true }))));
const fixture = async () => {
  const root = await mkdtemp(join(tmpdir(), "task19-producer-test-")); roots.push(root); await mkdir(join(root, "inputs"));
  const files = { server: "server.deb", web: "control.zip", windows: "control.msix", android: "control.apk", renderer: "renderer.msi" };
  const bytes = {}; for (const [key, name] of Object.entries(files)) { bytes[key] = Buffer.from(`observed-${key}`); await writeFile(join(root, "inputs", name), bytes[key]); }
  const reducer = { schemaVersion: 1, kind: "authoritative_product_qualification", status: "satisfied", promotableInput: true, caller: { sha: revision } };
  await writeFile(join(root, "inputs/reducer.json"), JSON.stringify(reducer));
  const serverManifest = { component: "server", source_revision: revision, artifacts: [{ name: files.server, sha256: sha256(bytes.server) }] };
  const controlManifest = { component: "control", source_revision: revision, artifacts: ["web", "windows", "android"].map((key) => ({ name: files[key], sha256: sha256(bytes[key]) })) };
  const rendererManifest = { component: "renderer", source_revision: revision, artifacts: [{ name: files.renderer, sha256: sha256(bytes.renderer) }] };
  for (const [name, value] of [["server-manifest.json", serverManifest], ["control-manifest.json", controlManifest], ["renderer-manifest.json", rendererManifest]]) await writeFile(join(root, "inputs", name), JSON.stringify(value));
  const candidateSha256 = sha256(Buffer.from(await Bun.file(join(root, "inputs/reducer.json")).arrayBuffer()));
  await writeFile(join(root, "inputs/k17.json"), JSON.stringify({ qualification_status: "qualified", source_revision: revision, candidate_sha256: candidateSha256 }));
  await writeFile(join(root, "inputs/wasapi.json"), JSON.stringify({ qualification_status: "qualified", source_revision: revision, binding: { candidate_sha256: candidateSha256 } }));
  const input = { root, reducer: "inputs/reducer.json", server: `inputs/${files.server}`, web: `inputs/${files.web}`, windows: `inputs/${files.windows}`, android: `inputs/${files.android}`, renderer: `inputs/${files.renderer}`, serverManifest: "inputs/server-manifest.json", controlManifest: "inputs/control-manifest.json", rendererManifest: "inputs/renderer-manifest.json", k17: "inputs/k17.json", wasapi: "inputs/wasapi.json", candidateSha256, outputRoot: join(root, "output") };
  return { root, input };
};

describe("authoritative immutable Task19 candidate producer", () => {
  test("binds independently observed Server Control Renderer physical receipts and trusted driver", async () => {
    const value = await fixture(); const result = produceTask19Closure(value.input);
    expect(result.ok).toBe(true); expect(result.closure.producer.driverSha256).toMatch(/^[0-9a-f]{64}$/); expect(result.closure.source.revision).toBe(revision);
  });

  test.each(["serverManifest", "controlManifest", "rendererManifest", "k17", "wasapi"])("absent %s remains non-promotable", async (field) => {
    const value = await fixture(); delete value.input[field]; expect(produceTask19Closure(value.input)).toMatchObject({ ok: false });
  });

  test("drifted independently observed input remains non-promotable", async () => {
    const value = await fixture(); await writeFile(join(value.root, value.input.server), "drifted");
    expect(produceTask19Closure(value.input)).toMatchObject({ ok: false, code: "TASK19_OBSERVED_ARTIFACT_DRIFT" });
  });

  test("expands reusable-call jobs without losing direct-step jobs", async () => {
    const serverPath = resolve(repositoryRoot, ".github/workflows/server-qualification-staging.yml"); const server = Bun.YAML.parse(await Bun.file(serverPath).text());
    expect(server.jobs.stage.steps).toBeUndefined(); expect(server.jobs.stage.uses).toBe("./.github/workflows/server-qualification-stage.yml");
    const expanded = await expandWorkflowJobs(serverPath);
    expect(expanded.some(({ job, workflowPath }) => job === "stage" && workflowPath.endsWith("server-qualification-stage.yml"))).toBe(true);
    expect(expanded.some(({ job, workflowPath }) => job === "k17-physical" && workflowPath === serverPath)).toBe(true);
  });

  test("workflow graph traces a completed K17 provider through authenticated observation to closure consumption", async () => {
    const workflowPath = resolve(repositoryRoot, ".github/workflows/product-qualification-dispatch.yml"); const serverPath = resolve(repositoryRoot, ".github/workflows/server-qualification-staging.yml"); const workflow = Bun.YAML.parse(await Bun.file(workflowPath).text());
    const expanded = await expandWorkflowJobs(serverPath); const uploads = expanded.flatMap(({ job, workflowPath: source, value }) => value.steps.filter((step) => typeof step.uses === "string" && step.uses.includes("actions/upload-artifact")).filter((step) => step.with?.name === "k17-qualification").map((step) => ({ job, source, condition: step.if ?? value.if ?? "" })));
    expect(uploads.map(({ job }) => job).sort()).toEqual(["k17-physical", "stage"]); expect(uploads.find(({ job }) => job === "stage")?.source.endsWith("server-qualification-stage.yml")).toBe(true); expect(uploads.find(({ job }) => job === "k17-physical")?.source).toBe(serverPath); expect(uploads.map(({ condition }) => condition).join(" ")).toContain("k17_authorized == 'true'"); expect(uploads.map(({ condition }) => condition).join(" ")).toContain("k17_authorized != 'true'");
    const parent = workflow.jobs["authoritative-reducer"].steps; const observerIndex = parent.findIndex((step) => step.run?.includes("task19-k17-provider-cli.ts")); const materializerIndex = parent.findIndex((step) => step.id === "task19-materialize"); const consumerIndex = parent.findIndex((step) => step.id === "task19-producer"); expect(observerIndex).toBeGreaterThanOrEqual(0); expect(materializerIndex).toBeGreaterThan(observerIndex); expect(consumerIndex).toBeGreaterThan(materializerIndex); const observer = parent[observerIndex]; const materializer = parent[materializerIndex]; const consumer = parent[consumerIndex];
    expect(observer.run).toContain('--arg runId "$K17_PROVIDER_RUN_ID"'); expect(observer.run).toContain('--arg qualificationRunId "$GITHUB_RUN_ID"'); expect(observer.run).toContain('--output-root "$PROVIDER_INPUT_ROOT/k17"'); expect(observer.run).not.toContain('--arg runId "$GITHUB_RUN_ID"'); expect(materializer.run).toContain('--k17-input "$PROVIDER_INPUT_ROOT/k17/k17-qualification.json"'); expect(materializer.run).toContain("--output-root task19-physical"); expect(consumer.run).toContain("--k17 task19-physical/k17-qualification.json"); expect(workflow.jobs.server.uses).toBe("./.github/workflows/server-qualification-staging.yml"); expect(workflow.on.workflow_dispatch.inputs.k17_provider_workflow.required).toBe(true);
  });
});
