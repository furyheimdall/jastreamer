import { readFileSync } from "node:fs";
import { isAbsolute, normalize } from "node:path";
import { COMPONENTS, type ComponentName, type InjectionId, type IsolationInput, type ScopeManifest } from "./types.ts";

export class InputError extends Error {
  override readonly name = "InputError";
  constructor(readonly detail: string) { super(detail); }
}

function fail(detail: string): never { throw new InputError(detail); }
const isComponent = (value: string): value is ComponentName => COMPONENTS.some((component) => component === value);
type UnknownRecord = Readonly<Record<string, unknown>>;
const isRecord = (value: unknown): value is UnknownRecord => typeof value === "object" && value !== null && !Array.isArray(value);
const ownKeys = (value: object): readonly string[] => Object.keys(value).sort();
const hasExactKeys = (value: object, expected: readonly string[]): boolean =>
  ownKeys(value).join("\0") === [...expected].sort().join("\0");

const readJson = (path: string): unknown => {
  try { const value: unknown = JSON.parse(readFileSync(path, "utf8")); return value; }
  catch (error) {
    if (error instanceof Error) throw new InputError(`invalid JSON in ${path}: ${error.message}`);
    throw error;
  }
};

const parseComponents = (raw: string): readonly ComponentName[] => {
  const values = raw.split(",");
  if (values.length === 0 || values.some((value) => !isComponent(value))) fail("invalid component list");
  if (new Set(values).size !== values.length) fail("duplicate component");
  const parsed: ComponentName[] = [];
  for (const value of values) {
    switch (value) {
      case "server": case "control": case "renderer": parsed.push(value); break;
      default: fail("invalid component list");
    }
  }
  return parsed;
};

export function parseArguments(args: readonly string[]): IsolationInput {
  const allowed = new Set(["--component", "--sparse", "--trace-files", "--output"]);
  const values = new Map<string, string>();
  const switches = new Set<string>();
  for (let index = 0; index < args.length; index += 1) {
    const flag = args[index];
    if (flag === undefined) fail("missing option");
    if (!allowed.has(flag)) fail(`unknown option: ${flag}`);
    if (switches.has(flag) || values.has(flag)) fail(`duplicate option: ${flag}`);
    if (flag === "--sparse" || flag === "--trace-files") { switches.add(flag); continue; }
    const value = args[index + 1];
    if (value === undefined || value.startsWith("--")) fail(`missing value: ${flag}`);
    values.set(flag, value);
    index += 1;
  }
  if (!switches.has("--sparse") || !switches.has("--trace-files")) fail("--sparse and --trace-files are required");
  const raw = values.get("--component");
  return { components: parseComponents(raw ?? fail("--component is required")) };
}

export function parseFixture(path: string): IsolationInput {
  const value = readJson(path);
  if (!isRecord(value) || !hasExactKeys(value, ["injection"])) fail("fixture must contain only injection");
  if (value["injection"] !== "server-imports-control") fail("unknown injection");
  const injection: InjectionId = value["injection"];
  return { components: COMPONENTS, injection };
}

const parsePaths = (value: unknown, component: ComponentName): readonly string[] => {
  if (!isRecord(value) || !hasExactKeys(value, ["paths"]) || !Array.isArray(value["paths"])) fail(`invalid ${component} scope`);
  const paths: string[] = [];
  for (const item of value["paths"]) {
    if (typeof item !== "string" || item.length === 0 || isAbsolute(item) || normalize(item) !== item || item === ".." || item.startsWith("../") || item.includes("/../") || item.includes("\\")) fail(`invalid ${component} path`);
    paths.push(item);
  }
  if (paths.length === 0 || new Set(paths).size !== paths.length) fail(`duplicate or empty ${component} paths`);
  return paths;
};

export function parseScopeManifest(path: string): ScopeManifest {
  const value = readJson(path);
  if (!isRecord(value) || !hasExactKeys(value, ["schema", "components"])) fail("invalid manifest");
  const components = value["components"];
  if (value["schema"] !== 1 || !isRecord(components) || !hasExactKeys(components, COMPONENTS)) fail("invalid manifest shape");
  return { schema: 1, components: {
    server: { paths: parsePaths(components["server"], "server") },
    control: { paths: parsePaths(components["control"], "control") },
    renderer: { paths: parsePaths(components["renderer"], "renderer") },
  } };
}
