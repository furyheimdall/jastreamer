import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { authenticateProviderTuple, type ProviderArtifactExpectation, type ProviderTupleExpectation } from "./provider-observation-contract";

type JsonRecord = Record<string, unknown>;
class ProviderCliError extends Error { readonly code: string; constructor(code: string) { super(code); this.code = code; } }
const fail = (code: string): never => { throw new ProviderCliError(code); };
const record = (value: unknown): JsonRecord => typeof value === "object" && value !== null && !Array.isArray(value) ? Object.fromEntries(Object.entries(value)) : fail("PROVIDER_OBSERVER_INPUT_INVALID");
const text = (value: unknown): string => typeof value === "string" ? value : fail("PROVIDER_OBSERVER_INPUT_INVALID");
const integer = (value: unknown): number => typeof value === "number" && Number.isSafeInteger(value) ? value : fail("PROVIDER_OBSERVER_INPUT_INVALID");
const array = (value: unknown): readonly unknown[] => Array.isArray(value) ? value : fail("PROVIDER_OBSERVER_INPUT_INVALID");
const optionalArtifact = (value: unknown): ProviderArtifactExpectation => { const item = record(value); const result: { name: string; id?: string; digest?: string; size?: number } = { name: text(item["name"]) }; if (item["id"] !== undefined) result.id = text(item["id"]); if (item["digest"] !== undefined) result.digest = text(item["digest"]).replace(/^sha256:/, ""); if (item["size"] !== undefined) result.size = integer(item["size"]); return result; };

const main = async (): Promise<void> => {
  const args = process.argv.slice(2); if (args.length !== 4 || args[0] !== "--context" || args[2] !== "--output") fail("PROVIDER_OBSERVER_USAGE");
  const requested = record(JSON.parse(readFileSync(resolve(args[1] ?? fail("PROVIDER_OBSERVER_USAGE")), "utf8"))); const repository = requested["repository"] === "furyheimdall/jastreamer" ? requested["repository"] : fail("PROVIDER_OBSERVER_INPUT_INVALID"); const artifactExpectations = array(requested["artifacts"]); if (artifactExpectations.length < 1) fail("PROVIDER_OBSERVER_INPUT_INVALID");
  const token = process.env["GITHUB_TOKEN"] ?? fail("PROVIDER_OBSERVER_ENV_INVALID"); const api = process.env["GITHUB_API_URL"] ?? "https://api.github.com"; const headers = { authorization: `Bearer ${token}`, accept: "application/vnd.github+json", "x-github-api-version": "2022-11-28", "cache-control": "no-cache, no-store", pragma: "no-cache", "user-agent": "jastreamer-provider-observer" }; let providerDate: string | undefined;
  const get = async (path: string): Promise<unknown> => { const response = await fetch(`${api}${path}`, { method: "GET", headers, cache: "no-store", redirect: "error" }); if (!response.ok) fail("PROVIDER_OBSERVER_READ_FAILED"); const date = response.headers.get("date"); if (typeof date !== "string" || !Number.isFinite(Date.parse(date))) fail("PROVIDER_OBSERVER_DATE_INVALID"); providerDate = date ?? fail("PROVIDER_OBSERVER_DATE_INVALID"); return response.json(); };
  const runId = text(requested["runId"]); const repositoryPath = repository.split("/").map(encodeURIComponent).join("/"); const run = await get(`/repos/${repositoryPath}/actions/runs/${runId}`); const values: unknown[] = []; let total: number | undefined;
  for (let page = 1; page <= 100; page += 1) { const response = record(await get(`/repos/${repositoryPath}/actions/runs/${runId}/artifacts?per_page=100&page=${page}`)); const pageValues = array(response["artifacts"]); const pageTotal = integer(response["total_count"]); if (total !== undefined && total !== pageTotal) fail("PROVIDER_OBSERVER_PAGINATION_INVALID"); total = pageTotal; values.push(...pageValues); if (pageValues.length < 100) break; }
  if (total === undefined || values.length !== total) fail("PROVIDER_OBSERVER_PAGINATION_INVALID"); const expectation: ProviderTupleExpectation = { repository, workflowPath: text(requested["workflowPath"]), eventName: text(requested["eventName"]), runId, runAttempt: integer(requested["runAttempt"]), headSha: text(requested["headSha"]), observedAt: providerDate ?? fail("PROVIDER_OBSERVER_DATE_INVALID"), artifacts: artifactExpectations.map(optionalArtifact) };
  const result = authenticateProviderTuple(expectation, run, values); const output = resolve(args[3] ?? fail("PROVIDER_OBSERVER_USAGE")); mkdirSync(dirname(output), { recursive: true, mode: 0o700 }); writeFileSync(output, `${JSON.stringify({ schemaVersion: 1, kind: "authenticated_provider_observation", ...result }, null, 2)}\n`, { mode: 0o400, flag: "wx" });
};
try { await main(); } catch (error) { // no-excuse-ok: catch
  const code = error instanceof ProviderCliError ? error.code : error instanceof Error && /^[A-Z0-9_]+$/.test(error.message) ? error.message : "PROVIDER_OBSERVER_FAILED"; process.stdout.write(`${JSON.stringify({ status: "denied", code, externalWrites: 0 })}\n`); process.exitCode = 77;
}
