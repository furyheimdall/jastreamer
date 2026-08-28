import { expect, test } from "bun:test";
import { cp, mkdtemp, readFile, rm } from "node:fs/promises";
import { join } from "node:path";
import Ajv2020 from "../../tooling/qa/node_modules/ajv/dist/2020.js";
import { createZoneInventoryValidator } from "../../tooling/qa/schema-validation.mjs";

const root = join(import.meta.dir, "..");
const run = (args: string[], contractsRoot = root) => Bun.spawnSync(["./tooling/componentctl", "contracts", ...args], { env: { ...process.env, CONTRACTS_ROOT: contractsRoot } });
const jsonObject = async (path: string): Promise<object> => {
  const value: unknown = JSON.parse(await readFile(path, "utf8"));
  if (typeof value !== "object" || value === null || Array.isArray(value)) throw new TypeError("JSON fixture must be an object");
  return value;
};

test("legacy root and archived contracts match their baseline SHA-256 manifest", async () => {
  const baseline: unknown = JSON.parse(await readFile(join(root, "legacy-sha256.json"), "utf8"));
  if (typeof baseline !== "object" || baseline === null || Array.isArray(baseline)) throw new TypeError("legacy SHA-256 manifest must be an object");
  expect(Object.keys(baseline).sort()).toEqual([
    "control-api/archived/v1/schema.json",
    "control-api/archived/v2/schema.json",
    "control-api/http-api-v1.json",
    "control-api/schema.json",
    "renderer-protocol/archived/v1/schema.json",
    "renderer-protocol/archived/v2/schema.json",
    "renderer-protocol/schema.json",
  ]);
  for (const [path, hash] of Object.entries(baseline)) {
    if (typeof hash !== "string") throw new TypeError(`legacy SHA-256 for ${path} must be a string`);
    expect(new Bun.CryptoHasher("sha256").update(await readFile(join(root, path))).digest("hex")).toBe(hash);
  }
});

test("locks select immutable v3 contracts explicitly", async () => {
  for (const name of ["control-api", "renderer-protocol"]) {
    expect(await Bun.file(join(root, name, "v3/schema.json")).exists()).toBe(true);
    expect(await Bun.file(join(root, name, "v3/fixtures/happy.json")).exists()).toBe(true);
  }
  expect(run(["locks"]).exitCode).toBe(0);
});

test("repeat generation detects drift without changing archives", async () => {
  const before = await readFile(join(root, "control-api/archived/v2/schema.json"));
  expect(run(["generate", "--check", "--repeat", "2"]).exitCode).toBe(0);
  expect(await readFile(join(root, "control-api/archived/v2/schema.json"))).toEqual(before);
});

test("v3 zones fixture matches the strict Server-Control wire schema", async () => {
  // Given: the captured Server response and its canonical v3 contract.
  const schema = await jsonObject(join(root, "control-api/v3/schema.json"));
  const fixture = await jsonObject(join(root, "control-api/v3/fixtures/zones-snapshot.json"));
  const validate = createZoneInventoryValidator(new Ajv2020({ allErrors: true, strict: true }), schema);

  // When: the real wire fixture is validated.
  const accepted = validate(fixture);

  // Then: the exact Server-Control payload is accepted.
  expect(accepted).toBe(true);
});

test("v3 zones schema rejects structural and semantic drift", async () => {
  // Given: malformed variants at every zone boundary.
  const schema = await jsonObject(join(root, "control-api/v3/schema.json"));
  const validate = createZoneInventoryValidator(new Ajv2020({ allErrors: true, strict: true }), schema);
  const renderer = { renderer_id: "renderer-1", name: "Renderer", kind: "custom", status: "connected", capabilities: ["command:play"], last_seen_at: "2026-08-26T00:00:00Z" } as const;
  const zone = { zone_id: "main", name: "Main", revision: 3, renderer_id: "renderer-1", transport: "playing" } as const;
  const malformed: readonly unknown[] = [
    { zones: [{ name: "Main", revision: 3, renderer_id: "renderer-1", transport: "playing" }], renderers: [renderer] },
    { zones: [{ ...zone, volume: 50 }], renderers: [renderer] },
    { zones: [{ ...zone, revision: "3" }], renderers: [renderer] },
    { zones: [{ ...zone, transport: null }], renderers: [renderer] },
    { zones: [{ ...zone, transport: "future-logical" }], renderers: [renderer] },
    { zones: [{ ...zone, revision: -1 }], renderers: [renderer] },
    { zones: [{ ...zone, renderer_id: 7 }], renderers: [renderer] },
    { zones: [{ ...zone, zoneId: "stale", zone_id: undefined }], renderers: [renderer] },
    { zones: [zone], renderers: [{ ...renderer, kind: "future-kind" }] },
    { zones: [zone], renderers: [{ ...renderer, status: "future-status" }] },
    { zones: [zone], renderers: [{ ...renderer, capabilities: ["command:play", "command:play"] }] },
    { zones: [zone], renderers: [{ ...renderer, last_seen_at: null }] },
    { zones: [zone, { ...zone, name: "Different zone" }], renderers: [renderer] },
    { zones: [zone], renderers: [renderer, { ...renderer, name: "Different Renderer" }] },
    { zones: [{ ...zone, renderer_id: "renderer-missing" }], renderers: [renderer] },
  ];

  // When: each drifted payload is validated.
  const results = malformed.map((value) => validate(value));

  // Then: every incompatible payload fails closed, while null assignment remains valid.
  expect(results).toEqual(malformed.map(() => false));
  expect(validate({ zones: [{ ...zone, renderer_id: null }], renderers: [renderer] })).toBe(true);
});

test("Control protocol v3 remains on the /api/v1 HTTP route prefix", async () => {
  const schema = JSON.parse(await readFile(join(root, "control-api/v3/schema.json"), "utf8"));
  const http = JSON.parse(await readFile(join(root, "control-api/v3/http-api.json"), "utf8"));
  expect(schema.$id).toBe("https://jastreamer.dev/contracts/control-api/v3");
  expect(http.version).toBe("control-api-v3");
  expect(http.routePrefix).toBe("/api/v1");
  expect(http.routes.filter((route: { path: string }) => route.path.startsWith("/api/")).every((route: { path: string }) => route.path.startsWith("/api/v1"))).toBe(true);
  expect(http.routes.map((route: { method: string; path: string }) => `${route.method} ${route.path}`)).toContain("GET /api/v1/events");
  const discovery = http.routes.find((route: { path: string }) => route.path === "/api/v1/discovery");
  expect(discovery.request.headers).toEqual(["X-Jake-Supported-Protocol-Majors"]);
  expect(discovery.responseHeaders).toEqual(["X-Jake-Supported-Protocol-Majors", "X-Jake-Selected-Protocol-Major"]);
  expect(JSON.stringify(discovery)).not.toContain("X-Jake-Protocol-Major");
  for (const route of http.routes) {
    expect(["public", "controller", "admin", "renderer"]).toContain(route.role);
    expect(route.success.length).toBeGreaterThan(0);
    expect(Array.isArray(route.errors)).toBe(true);
    expect(route.response ?? route.mutation ?? route.stream).toBeTruthy();
  }
  expect(http.httpErrors.STALE_POLICY_REVISION).toBe(412);
  expect(http.tlsErrors.CERTIFICATE_MISMATCH).toEqual({ layer: "client-tls-verification", httpStatus: null });
});

test("archived v2 required-field injection fails immutable verification", async () => {
  const directory = await mkdtemp(join("/tmp", "contracts-task-2-"));
  try {
    await cp(root, directory, { recursive: true });
    const archive = join(directory, "control-api/archived/v2/schema.json");
    const value: unknown = JSON.parse(await readFile(archive, "utf8"));
    if (typeof value !== "object" || value === null || !("required" in value) || !Array.isArray(value.required)) throw new TypeError("archived schema required must be an array");
    value.required.push("v3RequiredField");
    await Bun.write(archive, JSON.stringify(value));

    expect(run(["generate", "--check", "--repeat", "2"], directory).exitCode).toBe(1);
    expect(run(["locks"], directory).exitCode).toBe(1);
  } finally { await rm(directory, { recursive: true, force: true }); }
});

test("generation ignores whitespace but rejects semantic drift", async () => {
  const directory = await mkdtemp(join("/tmp", "contracts-task-3-"));
  try {
    await cp(root, directory, { recursive: true });
    const schema = join(directory, "control-api/v3/schema.json");
    const original = await readFile(schema, "utf8");
    await Bun.write(schema, `${original}\n`);
    expect(run(["generate", "--check", "--repeat", "2"], directory).exitCode).toBe(0);
    const value: unknown = JSON.parse(original);
    if (typeof value !== "object" || value === null || Array.isArray(value)) throw new TypeError("v3 schema must be an object");
    await Bun.write(schema, JSON.stringify({ ...value, title: "semantically changed" }));
    expect(run(["generate", "--check", "--repeat", "2"], directory).exitCode).toBe(1);
  } finally { await rm(directory, { recursive: true, force: true }); }
});

test("compatibility parses copied matrix fixtures", async () => {
  const directory = await mkdtemp(join("/tmp", "contracts-task-3-"));
  try {
    const matrix = join(directory, "supported.yaml");
    await cp(join(root, "fixtures/compatibility/supported-majors.yaml"), matrix);
    const output = join(directory, "result.json");
    expect(run(["compatibility", "--matrix", matrix, "--output", output]).exitCode).toBe(0);
    expect(JSON.parse(await readFile(output, "utf8"))).toEqual({ compatible: true, commonMajors: [3, 2] });
  } finally { await rm(directory, { recursive: true, force: true }); }
});

test("breaking and incompatible fixtures return contract exit 65", () => {
  expect(run(["breaking", "--fixture", join(root, "fixtures/breaking/required-field-without-major.json")]).exitCode).toBe(65);
  expect(run(["compatibility", "--matrix", join(root, "fixtures/compatibility/invalid/no-common-major.yaml")]).exitCode).toBe(65);
});
