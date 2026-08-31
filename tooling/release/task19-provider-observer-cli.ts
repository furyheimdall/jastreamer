import { readFileSync } from "node:fs";
import { isAbsolute, relative, resolve, sep } from "node:path";
import { authenticateTask19ProviderArtifact, observeTask19ProviderArtifact, type ProviderApiArtifact, type Task19ProviderContext } from "./qualification-provider-observer";
import { materializePrivateDirectory, platformPrivateMaterializationAdapter } from "./task19-private-materializer";
import { authenticateProviderTuple } from "./provider-observation-contract";

type JsonRecord = Record<string, unknown>;
class Task19ProviderError extends Error {
  readonly code: string;
  constructor(code: string) { super(code); this.code = code; }
}
const fail = (code: string): never => { throw new Task19ProviderError(code); };
const record = (value: unknown): JsonRecord => typeof value === "object" && value !== null && !Array.isArray(value) ? Object.fromEntries(Object.entries(value)) : fail("TASK19_PROVIDER_INPUT_INVALID");
const denied = (code: string) => ({ schemaVersion: 1, kind: "task19_provider_observation_receipt", status: "denied", code, productCommandsExecuted: 0, externalWrites: 0 });

const main = async (): Promise<void> => {
  const args = process.argv.slice(2);
  if (args.length !== 6 || args[0] !== "--context" || args[2] !== "--output-root" || args[4] !== "--output") fail("TASK19_PROVIDER_USAGE");
  const contextPath = args[1] ?? fail("TASK19_PROVIDER_USAGE"); const outputRoot = resolve(args[3] ?? fail("TASK19_PROVIDER_USAGE")); const outputPath = resolve(args[5] ?? fail("TASK19_PROVIDER_USAGE"));
  const token = process.env["GITHUB_TOKEN"]; const api = process.env["GITHUB_API_URL"] ?? "https://api.github.com";
  if (token === undefined || token.length === 0) fail("TASK19_PROVIDER_ENV_INVALID");
  const requested = record(JSON.parse(readFileSync(contextPath, "utf8"))); const repository = requested["repository"] === "furyheimdall/jastreamer" ? requested["repository"] : fail("TASK19_PROVIDER_INPUT_INVALID"); const runId = typeof requested["runId"] === "string" ? requested["runId"] : fail("TASK19_PROVIDER_INPUT_INVALID"); const runAttempt = typeof requested["runAttempt"] === "number" ? requested["runAttempt"] : fail("TASK19_PROVIDER_INPUT_INVALID"); const headSha = typeof requested["headSha"] === "string" ? requested["headSha"] : fail("TASK19_PROVIDER_INPUT_INVALID");
  const headers = { authorization: `Bearer ${token}`, accept: "application/vnd.github+json", "x-github-api-version": "2022-11-28", "cache-control": "no-cache, no-store", pragma: "no-cache", "user-agent": "jastreamer-task19-provider-observer" };
  let providerDate: string | undefined;
  const get = async (path: string, binary = false): Promise<unknown> => { const response = await fetch(`${api}${path}`, { method: "GET", headers, cache: "no-store", redirect: "follow" }); if (!response.ok) fail("TASK19_PROVIDER_READ_FAILED"); const responseDate = response.headers.get("date"); const date = typeof responseDate === "string" ? responseDate : fail("TASK19_PROVIDER_DATE_INVALID"); if (!Number.isFinite(Date.parse(date))) fail("TASK19_PROVIDER_DATE_INVALID"); providerDate = date; return binary ? new Uint8Array(await response.arrayBuffer()) : response.json(); };
  const repositoryPath = repository.split("/").map(encodeURIComponent).join("/"); const run = record(await get(`/repos/${repositoryPath}/actions/runs/${runId}`)); const runRepository = record(run["repository"]);
  if (String(run["id"]) !== runId || run["run_attempt"] !== runAttempt || run["head_sha"] !== headSha || runRepository["full_name"] !== repository || run["path"] !== ".github/workflows/product-qualification-dispatch.yml" || run["event"] !== "workflow_dispatch") fail("PROVIDER_RUN_INVALID");
  if (run["conclusion"] !== "success") fail("PROVIDER_RUN_UNSUCCESSFUL");
  const pages: ProviderApiArtifact[][] = []; let total: number | undefined;
  for (let page = 1; page <= 100; page += 1) { const response = record(await get(`/repos/${repositoryPath}/actions/runs/${runId}/artifacts?per_page=100&page=${page}`)); const rawValues = response["artifacts"]; const rawTotal = response["total_count"]; const valuesUnknown: unknown[] = Array.isArray(rawValues) ? rawValues : fail("PROVIDER_PAGINATION_INVALID"); const pageTotal = typeof rawTotal === "number" ? rawTotal : fail("PROVIDER_PAGINATION_INVALID"); const values: ProviderApiArtifact[] = valuesUnknown.map((value) => { const item = record(value); return { id: item["id"], name: item["name"], digest: item["digest"], size_in_bytes: item["size_in_bytes"], created_at: item["created_at"], expires_at: item["expires_at"], expired: item["expired"], workflow_run: item["workflow_run"] }; }); if (total === undefined) total = pageTotal; else if (total !== pageTotal) fail("PROVIDER_PAGINATION_CHANGED"); pages.push(values); if (values.length < 100) break; }
  const records = pages.flat(); if (total === undefined || records.length !== total) fail("PROVIDER_PAGINATION_INCOMPLETE");
  const observedAt = providerDate ?? fail("TASK19_PROVIDER_DATE_INVALID"); const context: Task19ProviderContext = { repository, workflowPath: ".github/workflows/product-qualification-dispatch.yml", eventName: "workflow_dispatch", runId, runAttempt, headSha, observedAt }; authenticateProviderTuple({ ...context, artifacts: [{ name: `task19-exact-candidates-${runId}-${runAttempt}` }] }, run, records); const boundRecords = records.map((record) => ({ ...record, workflow_run: { id: runId, run_attempt: runAttempt, head_sha: headSha, repository, conclusion: run["conclusion"] } })); const authenticated = authenticateTask19ProviderArtifact(context, boundRecords); const archiveValue = await get(`/repos/${repositoryPath}/actions/artifacts/${authenticated.artifactId}/zip`, true); const archive = archiveValue instanceof Uint8Array ? archiveValue : fail("TASK19_PROVIDER_READ_FAILED"); const observed = observeTask19ProviderArtifact(context, boundRecords, archive); const closureBytes = observed.entries["task19-candidate-closure.json"] ?? fail("PROVIDER_ARCHIVE_REQUIRED_FILE_MISSING"); const closure = record(JSON.parse(Buffer.from(closureBytes).toString("utf8")));
  const outputRelative = relative(outputRoot, outputPath); if (outputRelative === "" || outputRelative === ".." || outputRelative.startsWith(`..${sep}`) || isAbsolute(outputRelative)) fail("TASK19_PROVIDER_OUTPUT_INVALID");
  const exact = { ...closure, provider: observed.provider }; const materialized: Record<string, Uint8Array> = { [outputRelative.split(sep).join("/")]: Buffer.from(`${JSON.stringify(exact, null, 2)}\n`) };
  for (const [path, bytes] of Object.entries(observed.entries)) if (path !== "task19-candidate-closure.json") materialized[path] = bytes;
  materializePrivateDirectory(outputRoot, materialized, platformPrivateMaterializationAdapter()); console.log(JSON.stringify({ status: "observed", provider: observed.provider }));
};

try { await main(); } catch (error) { // no-excuse-ok: catch
  const code = error instanceof Task19ProviderError ? error.code : error instanceof Error && /^[A-Z0-9_]+$/.test(error.message) ? error.message : "TASK19_PROVIDER_OBSERVATION_FAILED";
  process.stdout.write(`${JSON.stringify(denied(code))}\n`); process.exitCode = 77;
}
