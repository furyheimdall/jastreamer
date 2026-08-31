import { describe, expect, test } from "bun:test";
import { existsSync, readFileSync, rmSync } from "node:fs";
import { join, resolve } from "node:path";

const root = resolve(new URL("../..", import.meta.url).pathname);
const orchestratorPath = ".github/workflows/product-qualification-dispatch.yml";
const stagingPath = (component: "server" | "control"): string => `.github/workflows/${component}-qualification-staging.yml`;
const qualificationPaths = (component: "server" | "control"): readonly string[] => component === "control" ? [
  stagingPath(component),
  ".github/workflows/control-qualification-platforms.yml",
  ".github/workflows/control-qualification-signed-platforms.yml",
  ".github/workflows/control-qualification-stage.yml",
] : [
  stagingPath(component),
  ".github/workflows/server-qualification-platforms.yml",
  ".github/workflows/server-qualification-windows.yml",
  ".github/workflows/server-qualification-stage.yml",
];
const releasePath = (component: "server" | "control"): string => `.github/workflows/${component}-release.yml`;
type YamlObject = { readonly [key: string]: unknown };
const read = (path: string): string => readFileSync(join(root, path), "utf8");
const yamlObject = (value: unknown): YamlObject => typeof value === "object" && value !== null && !Array.isArray(value) ? Object.fromEntries(Object.entries(value)) : {};
const yaml = (path: string): YamlObject => yamlObject(Bun.YAML.parse(read(path)));
const field = (value: unknown, key: string): YamlObject => yamlObject(yamlObject(value)[key]);
const jobs = (workflow: YamlObject): YamlObject => field(workflow, "jobs");
const job = (workflow: YamlObject, name: string): YamlObject => field(jobs(workflow), name);
const runBlocks = (workflow: string): readonly string[] => [...workflow.matchAll(/^([ ]+)run: \|\n((?:(?:\1  ).*\n?)*)/gm)].map((match) => match[2] ?? "");

const localWorkflowCalls = (workflow: YamlObject): readonly string[] => Object.values(jobs(workflow))
  .map((value) => yamlObject(value)["uses"])
  .filter((uses: unknown): uses is string => typeof uses === "string" && uses.startsWith("./.github/workflows/"));

const permissionEntries = (permissions: unknown): readonly [string, unknown][] => Object.entries(yamlObject(permissions));

describe("qualification workflow capability isolation", () => {
  test.each(["line\nPWNED", "'\nprintf PWNED", "$(printf PWNED)", "x\nprintf 'owned=true' >> $GITHUB_OUTPUT"])("caller input never enters shell source: %s", (payload) => {
    const sentinel = join(root, ".qualification-injection-sentinel");
    rmSync(sentinel, { force: true });
    for (const path of [orchestratorPath, ...qualificationPaths("server"), ...qualificationPaths("control")]) {
      const rendered = read(path).replaceAll(/\$\{\{\s*inputs\.[^}]+}}/g, payload);
      for (const script of runBlocks(rendered)) expect(script).not.toContain(payload);
    }
    expect(existsSync(sentinel)).toBe(false);
  });

  test("physical provider step graph reaches one complete private materialization before production", () => {
    const workflow = yaml(orchestratorPath);
    const reducerSteps = job(workflow, "authoritative-reducer")["steps"];
    const parsedSteps = Array.isArray(reducerSteps) ? reducerSteps.map(yamlObject) : [];
    const index = (id: string): number => parsedSteps.findIndex((step) => step["id"] === id);
    expect(index("task19-renderer-provider")).toBeGreaterThan(0);
    expect(index("task19-k17-provider")).toBeGreaterThan(index("task19-renderer-provider"));
    expect(index("task19-materialize")).toBeGreaterThan(index("task19-k17-provider"));
    expect(index("task19-producer")).toBeGreaterThan(index("task19-materialize"));
    expect(index("task19-cleanup")).toBeGreaterThan(index("task19-producer"));
    const materialize = parsedSteps[index("task19-materialize")] ?? {};
    expect(materialize["if"]).toBe("steps.task19-renderer-provider.outcome == 'success' && steps.task19-k17-provider.outcome == 'success'");
    const runs = parsedSteps.map((step) => step["run"]).filter((run): run is string => typeof run === "string");
    expect(runs.filter((run) => run.includes("task19-provider-materialize-cli.ts"))).toHaveLength(1);
    expect(parsedSteps[index("task19-k17-provider")]?.["run"]).toContain('--output-root "$PROVIDER_INPUT_ROOT/k17"');
    expect(read(orchestratorPath)).toContain("${{ runner.temp }}/task19-provider-inputs-");
    expect(read(orchestratorPath)).not.toContain("--output task19-physical/k17-qualification.json");
  });

  test("orchestrator calls only the two qualification staging workflows", () => {
    const workflow = yaml(orchestratorPath);
    expect(localWorkflowCalls(workflow)).toEqual([
      "./.github/workflows/server-qualification-staging.yml",
      "./.github/workflows/control-qualification-staging.yml",
    ]);
    expect(workflow["permissions"]).toEqual({ contents: "read" });
    expect(job(workflow, "authorize")["environment"]).toBe("product-qualification-dispatch");
    expect(read(orchestratorPath)).not.toMatch(/gh\s|curl\s|\/dispatches|actions:\s*write|contents:\s*write|packages:\s*write/);
  });

  test.each(["server", "control"] as const)("%s staging DAG is read-only and publication-free", (component) => {
    const workflows = qualificationPaths(component).map(yaml);
    expect(workflows.map((workflow) => workflow["permissions"])).toEqual([
      { contents: "read" },
      { contents: "read" },
      { contents: "read" },
      { contents: "read" },
    ]);
    expect(localWorkflowCalls(workflows[0] ?? {})).toEqual(component === "control" ? [
      "./.github/workflows/control-qualification-platforms.yml",
      "./.github/workflows/control-qualification-signed-platforms.yml",
      "./.github/workflows/control-qualification-stage.yml",
    ] : [
      "./.github/workflows/server-qualification-platforms.yml",
      "./.github/workflows/server-qualification-windows.yml",
      "./.github/workflows/server-qualification-stage.yml",
    ]);
    for (const workflow of workflows) {
      expect(jobs(workflow)["product-qualification-handoff"]).toBeUndefined();
      expect(jobs(workflow)["publish-qualified"]).toBeUndefined();
      for (const [jobName, value] of Object.entries(jobs(workflow))) {
        const parsedJob = yamlObject(value);
        expect(parsedJob["environment"], `${jobName} must not enter promotion`).not.toBe("product-promotion");
        for (const [scope, access] of permissionEntries(parsedJob["permissions"])) expect(`${scope}:${access}`, `${jobName} has a forbidden capability`).not.toMatch(/^(actions:read|contents:write|packages:write)$/);
      }
    }
    expect(qualificationPaths(component).map(read).join("\n")).not.toMatch(/PUBLICATION_RECEIPT_HMAC_KEY|product-promotion|contents:\s*write|packages:\s*write|actions:\s*read/);
  });

  test("staging retains signing environments and candidate artifact outputs", () => {
    const serverWindows = yaml(".github/workflows/server-qualification-windows.yml");
    expect(job(serverWindows, "windows")["environment"]).toBe("server-signing");
    const controlPlatforms = yaml(".github/workflows/control-qualification-signed-platforms.yml");
    expect(job(controlPlatforms, "android")["environment"]).toBe("control-android-signing");
    expect(job(controlPlatforms, "windows")["environment"]).toBe("control-windows-signing");
    for (const component of ["server", "control"] as const) {
      const workflow = yaml(stagingPath(component));
      const outputs = field(field(field(workflow, "on"), "workflow_call"), "outputs");
      for (const output of ["caller_run_id", "caller_run_attempt", "caller_ref", "caller_sha", "called_workflow_path", "called_job", "called_output_identity", "artifact_id", "artifact_digest", "artifact_name", "staging_binding_artifact_id", "staging_binding_artifact_digest", "staging_binding_artifact_name", "staging_binding_sha256"]) expect(outputs[output]).toBeDefined();
      const stage = yaml(`.github/workflows/${component}-qualification-stage.yml`);
      expect(field(job(stage, "stage"), "outputs")["called_workflow_path"]).toBe(`.github/workflows/${component}-qualification-staging.yml`);
    }
  });

  test.each(["server", "control"] as const)("%s release authenticates complete handoff metadata before either provider download", (component) => {
    const workflow = read(releasePath(component));
    expect(workflow).toContain("provider-observation-cli.ts");
    expect(workflow).toContain("PRODUCT_QUALIFICATION_RUN_ATTEMPT");
    expect(workflow).toContain("PRODUCT_GATE_ARTIFACT_SIZE");
    expect(workflow.indexOf("Authenticate complete upstream run and artifact metadata before download")).toBeLessThan(workflow.indexOf(`artifact-ids: \${{ vars.PRODUCT_GATE_ARTIFACT_ID }}`));
    expect(workflow.indexOf(`Independently authenticate signed ${component === "server" ? "Server" : "Control"} candidate provider metadata before download`)).toBeLessThan(workflow.lastIndexOf("path: candidate-download"));
  });

  test.each(["server", "control"] as const)("%s release and qualification share one staging DAG without exposing publication", (component) => {
    const release = yaml(releasePath(component));
    const orchestrator = yaml(orchestratorPath);
    const expected = `./.github/workflows/${component}-qualification-staging.yml`;
    const triggers = field(release, "on");
    expect(triggers["push"]).toBeDefined();
    expect(triggers["workflow_dispatch"]).toBeDefined();
    expect(triggers["workflow_call"]).toBeUndefined();
    expect(job(release, "staging")["uses"]).toBe(expected);
    expect(job(orchestrator, component)["uses"]).toBe(expected);
    expect(job(release, "product-qualification-handoff")["environment"]).toBe("product-promotion");
    expect(job(release, "publish-qualified")["environment"]).toBe("product-promotion");
    expect(field(job(release, "publish-qualified"), "permissions")["contents"]).toBe("write");
  });
});
