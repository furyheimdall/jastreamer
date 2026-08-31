import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";

const root = resolve(new URL("../..", import.meta.url).pathname);
const components = ["control", "server"] as const;
type Component = typeof components[number];
type YamlObject = { readonly [key: string]: unknown };

const object = (value: unknown): YamlObject => typeof value === "object" && value !== null && !Array.isArray(value)
  ? Object.fromEntries(Object.entries(value))
  : {};
const read = (path: string): string => readFileSync(join(root, path), "utf8");
const parse = (path: string): YamlObject => object(Bun.YAML.parse(read(path)));
const field = (value: unknown, key: string): YamlObject => object(object(value)[key]);
const jobs = (workflow: YamlObject): YamlObject => field(workflow, "jobs");
const job = (workflow: YamlObject, name: string): YamlObject => field(jobs(workflow), name);
const steps = (value: YamlObject): readonly YamlObject[] => {
  const valueSteps = value["steps"];
  return Array.isArray(valueSteps) ? valueSteps.map(object) : [];
};
const paths = (component: Component): Readonly<{ staging: string; platforms: string; signing: string; stage: string }> => ({
  staging: `.github/workflows/${component}-qualification-staging.yml`,
  platforms: `.github/workflows/${component}-qualification-platforms.yml`,
  signing: component === "control"
    ? ".github/workflows/control-qualification-signed-platforms.yml"
    : ".github/workflows/server-qualification-windows.yml",
  stage: `.github/workflows/${component}-qualification-stage.yml`,
});
const needs = (value: YamlObject): readonly string[] => {
  const dependency = value["needs"];
  if (typeof dependency === "string") return [dependency];
  return Array.isArray(dependency) ? dependency.filter((item): item is string => typeof item === "string") : [];
};
const reaches = (workflowJobs: YamlObject, source: string, target: string, seen = new Set<string>()): boolean => {
  if (source === target) return true;
  if (seen.has(source)) return false;
  seen.add(source);
  return needs(object(workflowJobs[source])).some((dependency) => reaches(workflowJobs, dependency, target, seen));
};
const actionUses = (workflow: YamlObject): readonly string[] => Object.values(jobs(workflow)).flatMap((value) =>
  steps(object(value)).map((step) => step["uses"]).filter((uses): uses is string => typeof uses === "string"),
);
const actionStep = (workflow: YamlObject, jobName: string, action: string, id?: string): YamlObject => object(
  steps(job(workflow, jobName)).find((step) => step["uses"] === action && (id === undefined || step["id"] === id)),
);
const workflowCall = (workflow: YamlObject): YamlObject => field(field(workflow, "on"), "workflow_call");

const outputNames = [
  "caller_run_id", "caller_run_attempt", "caller_ref", "caller_sha", "called_workflow_path", "called_job",
  "called_output_identity", "artifact_id", "artifact_digest", "artifact_name", "staging_binding_artifact_id",
  "staging_binding_artifact_digest", "staging_binding_artifact_name", "staging_binding_sha256",
] as const;

describe("qualification reusable workflow graph", () => {
  test("K17 is an already-completed protected provider that dominates observation and cannot self-observe", () => {
    // Given: the authoritative qualification workflow and its protected dispatch contract.
    const workflow = parse(".github/workflows/product-qualification-dispatch.yml");
    const dispatchInputs = field(field(field(workflow, "on"), "workflow_dispatch"), "inputs");
    const reducer = job(workflow, "authoritative-reducer");
    const reducerSteps = steps(reducer);
    const observer = reducerSteps.find((step) => step["id"] === "task19-k17-provider") ?? {};
    const materializer = reducerSteps.find((step) => step["id"] === "task19-materialize") ?? {};
    const consumer = reducerSteps.find((step) => step["id"] === "task19-producer") ?? {};

    // When: the K17 producer-to-consumer edge and protected tuple fields are inspected.
    const observerRun = String(observer["run"] ?? "");
    const materializerRun = String(materializer["run"] ?? "");
    const consumerRun = String(consumer["run"] ?? "");
    const inputNames = Object.keys(dispatchInputs).filter((name) => name.startsWith("k17_provider_"));

    // Then: a completed server provider dominates one observer, one private materialization, and the closure consumer.
    expect(inputNames).toEqual([
      "k17_provider_repository", "k17_provider_workflow", "k17_provider_event", "k17_provider_run_id",
      "k17_provider_run_attempt", "k17_provider_current_sha", "k17_provider_conclusion", "k17_provider_artifact_id",
      "k17_provider_artifact_name", "k17_provider_artifact_digest", "k17_provider_artifact_size",
      "k17_provider_artifact_created_at", "k17_provider_artifact_expires_at",
    ]);
    for (const name of inputNames) expect(field(dispatchInputs, name)["required"]).toBe(true);
    expect(observerRun).toContain("K17_PROVIDER_RUN_ID");
    expect(observerRun).not.toContain('runId "$GITHUB_RUN_ID"');
    expect(observerRun).toContain('qualificationRunId "$GITHUB_RUN_ID"');
    expect(observerRun).toContain('workflowPath "$K17_PROVIDER_WORKFLOW"');
    expect(observerRun).toContain('conclusion "$K17_PROVIDER_CONCLUSION"');
    expect(materializer["if"]).toContain("task19-k17-provider");
    expect(materializerRun).toContain("k17-qualification.json");
    expect(consumerRun).toContain("task19-physical/k17-qualification.json");
    expect(read(".github/workflows/product-qualification-dispatch.yml")).toContain('test "$K17_PROVIDER_RUN_ID" != "$GITHUB_RUN_ID"');
  });
  test.each([...components])("%s authorization reaches every executable outer job when the graph is expanded", (component) => {
    // Given: the parsed public qualification workflow.
    const workflow = parse(paths(component).staging);
    const workflowJobs = jobs(workflow);

    // When: every job with executable capabilities is traced through needs.
    const executable = Object.entries(workflowJobs).filter(([, value]) => {
      const parsed = object(value);
      return parsed["uses"] !== undefined || parsed["steps"] !== undefined || parsed["environment"] !== undefined;
    });

    // Then: authorization dominates every job, and staging waits for all platform jobs.
    for (const [name] of executable) expect(reaches(workflowJobs, name, "invocation-authorization"), name).toBe(true);
    expect(needs(job(workflow, "platforms"))).toEqual(["validate"]);
    expect(needs(job(workflow, component === "control" ? "signed-platforms" : "windows"))).toEqual(["validate"]);
    expect(needs(job(workflow, "stage"))).toEqual(component === "control"
      ? ["validate", "platforms", "signed-platforms"]
      : ["validate", "platforms", "windows"]);
  });

  test.each([...components])("%s nested calls preserve typed inputs, outputs, SHA context, and secret isolation", (component) => {
    // Given: the public, platform, and staging workflow contracts.
    const contract = paths(component);
    const outer = parse(contract.staging);
    const platform = parse(contract.platforms);
    const signing = parse(contract.signing);
    const stage = parse(contract.stage);

    // When: reusable boundaries are inspected structurally.
    const platformCall = job(outer, "platforms");
    const signingCall = job(outer, component === "control" ? "signed-platforms" : "windows");
    const stageCall = job(outer, "stage");
    const stageInputs = field(stageCall, "with");

    // Then: local current-SHA calls bind validated values and expose the unchanged provider contract.
    expect(platformCall["uses"]).toBe(`./${contract.platforms}`);
    expect(signingCall["uses"]).toBe(`./${contract.signing}`);
    expect(stageCall["uses"]).toBe(`./${contract.stage}`);
    expect(field(workflowCall(platform), "inputs")).toEqual({ version: { required: true, type: "string" } });
    expect(field(workflowCall(signing), "inputs")).toEqual({ version: { required: true, type: "string" } });
    expect(stageInputs["version"]).toBe("${{ needs.validate.outputs.version }}");
    expect(stageInputs["candidate_tag"]).toBe("${{ needs.validate.outputs.candidate_tag }}");
    expect(stageInputs["caller_sha"]).toBe("${{ inputs.caller_sha || github.sha }}");
    expect(Object.keys(field(workflowCall(platform), "secrets"))).toEqual([]);
    expect(Object.keys(field(workflowCall(signing), "secrets")).length).toBeGreaterThan(0);
    expect(Object.keys(field(workflowCall(stage), "secrets"))).toEqual([]);
    const outputs = field(workflowCall(outer), "outputs");
    const nestedOutputs = field(workflowCall(stage), "outputs");
    for (const name of outputNames) {
      expect(outputs[name]).toEqual({ value: `\${{ jobs.stage.outputs.${name} }}` });
      expect(nestedOutputs[name]).toEqual({ value: `\${{ jobs.stage.outputs.${name} }}` });
    }
  });

  test("platform jobs retain the exact native matrix and signing environments", () => {
    // Given: both parsed platform workflows.
    const server = parse(paths("server").platforms);
    const serverWindows = parse(paths("server").signing);
    const control = parse(paths("control").signing);

    // When: scheduler and protected-environment bindings are read.
    const matrix = field(field(job(server, "linux"), "strategy"), "matrix")["include"];

    // Then: both native architectures and all signing gates remain reachable.
    expect(matrix).toEqual([
      { arch: "amd64", runner: "ubuntu-24.04", classification: "native", debian: "debian@sha256:362e64223cc0da95422b3b13c045186fc0a81250e765d31c025fbddf257f6143", rocky: "rockylinux@sha256:197b1569a8e5d46de75412cfd80b88a437d25bb2a5338dc82d5421d835245ec7" },
      { arch: "arm64", runner: "ubuntu-24.04-arm", classification: "native", debian: "debian@sha256:817e6cf99d6fc127ff4ffe8580049b60deba0adfbbb2bd65ddc3ef8fbb7aade0", rocky: "rockylinux@sha256:99a073e7e92dc4cd2882c9418936bdd1c2298279c5af0f3642261286e135f6c7" },
    ]);
    expect(job(serverWindows, "windows")["environment"]).toBe("server-signing");
    expect(job(control, "android")["environment"]).toBe("control-android-signing");
    expect(job(control, "windows")["environment"]).toBe("control-windows-signing");
  });

  test.each([...components])("%s staging binds exact upload provenance and cannot publish", (component) => {
    // Given: every workflow in the component graph.
    const contract = paths(component);
    const workflows = [parse(contract.staging), parse(contract.platforms), parse(contract.signing), parse(contract.stage)];
    const stageWorkflow = workflows[3] ?? {};
    const stageJob = job(stageWorkflow, "stage");
    const uploadSha = component === "control" ? "actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02" : "actions/upload-artifact@50769540e7f4bd5e21e526ee35c689e35e0d6874";

    // When: provider-facing uploads, permissions, actions, and scripts are inspected.
    const publication = actionStep(stageWorkflow, "stage", uploadSha, "publication-stage");
    const binding = actionStep(stageWorkflow, "stage", uploadSha, "staging-binding");
    const source = [contract.staging, contract.platforms, contract.signing, contract.stage].map(read).join("\n");

    // Then: output IDs come from exact uploads, all actions are pinned, and no publication capability exists.
    expect(field(stageJob, "outputs")["artifact_id"]).toBe("${{ steps.publication-stage.outputs.artifact-id }}");
    expect(field(stageJob, "outputs")["staging_binding_artifact_id"]).toBe("${{ steps.staging-binding.outputs.artifact-id }}");
    expect(field(publication, "with")["path"]).toBe("publication-stage\nqualification\n");
    expect(field(binding, "with")["path"]).toBe(`${component}-publication-staging.json`);
    for (const workflow of workflows) expect(workflow["permissions"]).toEqual({ contents: "read" });
    for (const uses of workflows.flatMap(actionUses)) expect(uses).toMatch(/^[^@]+@[0-9a-f]{40}$/);
    expect(source).not.toMatch(/contents:\s*write|packages:\s*write|actions:\s*write|id-token:|secrets:\s*inherit|gh release|\/dispatches/);
  });

  test("signing cleanup remains unconditional and public staging excludes private formats", () => {
    // Given: platform cleanup jobs and staging allowlists.
    const control = parse(paths("control").signing);
    const server = parse(paths("server").signing);
    const stageSource = `${read(paths("control").stage)}\n${read(paths("server").stage)}`;

    // When: cleanup conditions are selected by their machine-visible names.
    const cleanup = [
      steps(job(control, "android")).find((step) => step["name"] === "Prove no Android signing material remains"),
      steps(job(control, "windows")).find((step) => step["name"] === "Prove no Windows signing material remains"),
      steps(job(server, "windows")).find((step) => step["name"] === "Prove no signing material remains"),
    ].map(object);

    // Then: every cleanup executes on failure and staging denies signing material and AAB publication.
    for (const step of cleanup) expect(step["if"]).toBe("always()");
    expect(stageSource).toMatch(/\.aab/);
    expect(stageSource).toMatch(/\.pfx/);
    expect(stageSource).toMatch(/\.key/);
    expect(stageSource).toContain("external_writes:[]");
  });
});
