import { createHash } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";
import { resolve, relative, isAbsolute } from "node:path";
import { reduceAuthoritativeQualification, type ChildMachineOutputs, type PublicComponent, type ReducerChild, type ReducerContext, type TerminalJobResult } from "./qualification-authoritative-reducer";
import type { ProviderArtifactObservation } from "./qualification-provider-observer";

type RecordValue = Record<string, unknown>;
const fail = (code: string): never => { throw new Error(code); };
const record = (value: unknown): RecordValue => typeof value === "object" && value !== null && !Array.isArray(value) ? Object.fromEntries(Object.entries(value)) : fail("REDUCER_INPUT_INVALID");
const string = (value: unknown): string => typeof value === "string" ? value : fail("REDUCER_INPUT_INVALID");
const within = (root: string, path: unknown): string => { const absolute = resolve(root, string(path)); const location = relative(root, absolute); if (location === "" || location === ".." || location.startsWith("../") || isAbsolute(location)) fail("REDUCER_INPUT_PATH_INVALID"); return absolute; };
const bytes = (root: string, path: unknown): Uint8Array => readFileSync(within(root, path));
const outputs = (value: unknown): ChildMachineOutputs => value === null || value === undefined ? fail("REDUCER_OUTPUT_INVALID") : record(value) as ChildMachineOutputs;
const terminal = (value: unknown): TerminalJobResult => value === "success" || value === "failure" || value === "cancelled" || value === "skipped" ? value : fail("REDUCER_INPUT_INVALID");
const component = (value: unknown): PublicComponent => value === "server" || value === "control" ? value : fail("REDUCER_INPUT_INVALID");
const contextValue = (value: unknown): ReducerContext => { const item = record(value); if (JSON.stringify(Object.keys(item).sort()) !== JSON.stringify(["repository", "runId", "runAttempt", "ref", "sha", "workflowPath"].sort()) || item["repository"] !== "furyheimdall/jastreamer" || item["workflowPath"] !== ".github/workflows/product-qualification-dispatch.yml" || typeof item["runId"] !== "string" || !/^[1-9][0-9]*$/.test(item["runId"]) || typeof item["runAttempt"] !== "number" || !Number.isSafeInteger(item["runAttempt"]) || item["runAttempt"] < 1 || typeof item["ref"] !== "string" || !/^refs\/heads\/[A-Za-z0-9._/-]{1,200}$/.test(item["ref"]) || typeof item["sha"] !== "string" || !/^[0-9a-f]{40}$/.test(item["sha"])) fail("REDUCER_CONTEXT_INVALID"); return item as ReducerContext; };
const observation = (root: string, value: unknown): ProviderArtifactObservation => { const item = record(value); const kind = item["kind"]; const artifactComponent = component(item["component"]); if (kind !== "publication-stage" && kind !== "staging-binding") fail("REDUCER_INPUT_INVALID"); const artifactKind = kind as "publication-stage" | "staging-binding"; const entryFiles = record(item["entryFiles"]); const entries = Object.fromEntries(Object.entries(entryFiles).map(([path, rawFile]) => { const file = record(rawFile); const content = bytes(root, file["path"]); if (file["size"] !== content.byteLength || typeof file["sha256"] !== "string" || createHash("sha256").update(content).digest("hex") !== file["sha256"]) fail("REDUCER_PROVIDER_FILE_INVALID"); return [path, content]; })); return { component: artifactComponent, kind: artifactKind, id: string(item["id"]), name: string(item["name"]), digest: string(item["digest"]), size: Number(item["size"]), createdAt: string(item["createdAt"]), expiresAt: string(item["expiresAt"]), expired: item["expired"] === false ? false : fail("REDUCER_INPUT_INVALID"), repository: item["repository"] === "furyheimdall/jastreamer" ? item["repository"] : fail("REDUCER_INPUT_INVALID"), runId: string(item["runId"]), runAttempt: Number(item["runAttempt"]), headSha: string(item["headSha"]), archiveSha256: string(item["archiveSha256"]), entries }; };

const arguments_ = process.argv.slice(2); if (arguments_.length !== 6 || arguments_[0] !== "--input" || arguments_[2] !== "--root" || arguments_[4] !== "--output") fail("REDUCER_USAGE");
const outputPath = arguments_[5] ?? fail("REDUCER_USAGE");
let context: ReducerContext | undefined; let results: Readonly<{ server: TerminalJobResult; control: TerminalJobResult }> | undefined;
try {
  const root = resolve(arguments_[3] ?? fail("REDUCER_USAGE")); const input = record(JSON.parse(readFileSync(arguments_[1] ?? fail("REDUCER_USAGE"), "utf8"))); context = contextValue(input["context"]);
  const childInput = input["children"]; if (!Array.isArray(childInput)) fail("REDUCER_INPUT_INVALID"); const childValues = childInput as unknown[];
  const rawChildren: RecordValue[] = childValues.map((value: unknown) => record(value)); const allSucceeded = rawChildren.every((item) => terminal(item["result"]) === "success");
  const children: ReducerChild[] = rawChildren.map((item) => { const childResult = terminal(item["result"]); return { component: component(item["component"]), result: childResult, ...(allSucceeded ? { outputs: outputs(item["outputs"]) } : {}) }; });
  const server = children.find((child) => child.component === "server"); const control = children.find((child) => child.component === "control"); if (server !== undefined && control !== undefined) results = { server: server.result, control: control.result };
  const providerInput = input["providerObservations"]; const providerObservations = allSucceeded ? (Array.isArray(providerInput) ? providerInput.map((entry: unknown) => observation(root, entry)) : fail("REDUCER_INPUT_INVALID")) : [];
  const reduced = reduceAuthoritativeQualification(context, children, providerObservations); writeFileSync(outputPath, `${JSON.stringify(reduced, null, 2)}\n`); console.log(JSON.stringify({ status: reduced.status, promotableInput: reduced.promotableInput }));
} catch (error) {
  if (context === undefined || results === undefined || !(error instanceof Error)) throw error;
  const denial = { schemaVersion: 1, kind: "authoritative_product_qualification", status: "denied", caller: context, children: { server: { result: results.server }, control: { result: results.control } }, promotableInput: false, retryDispatches: 0, integrityCode: error.message };
  writeFileSync(outputPath, `${JSON.stringify(denial, null, 2)}\n`); console.log(JSON.stringify({ status: "denied", promotableInput: false, integrityCode: error.message }));
}
