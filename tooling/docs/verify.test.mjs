import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { cp, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const root = resolve(import.meta.dirname, "..", "..");
const verifier = join(root, "tooling/docs/verify.mjs");
const claimsSource = join(root, "docs/claims.json");
const registrySource = join(root, "tooling/release/capability-registry-v1.json");
const contractSource = join(root, "tooling/docs/claims-contract-v1.json");
const schemaSource = join(root, "tooling/qa/product-receipt.schema.json");
let directory;
let canonicalClaims;

const run = ({ claims = "claims.json", registry = "registry.json", contract = "claims-contract.json", schema = "receipt.schema.json" } = {}) => Bun.spawnSync([
  "bun", verifier,
  "--claims", join(directory, claims),
  "--receipt-schema", join(directory, schema),
  "--registry", join(directory, registry),
  "--claim-contract", join(directory, contract),
], { cwd: root, stdout: "pipe", stderr: "pipe" });

const json = async (name, value) => writeFile(join(directory, name), `${JSON.stringify(value, null, 2)}\n`);
const reject = (result, code) => {
  expect(result.exitCode).not.toBe(0);
  expect(result.stderr.toString()).toContain(code);
};

beforeAll(async () => {
  directory = await mkdtemp(join(tmpdir(), "jastreamer-doc-claims-"));
  canonicalClaims = JSON.parse(await readFile(claimsSource, "utf8"));
  await Promise.all([
    json("claims.json", canonicalClaims),
    cp(registrySource, join(directory, "registry.json")),
    cp(schemaSource, join(directory, "receipt.schema.json")),
    cp(contractSource, join(directory, "claims-contract.json")),
  ]);
});

afterAll(async () => rm(directory, { recursive: true, force: true }));

describe("canonical documentation claims", () => {
  test("accepts the exact canonical 21-entry set", () => {
    // Given: the shipped canonical claim set.
    // When: every source, gate, schema, and verifier mapping is resolved.
    const result = run();
    // Then: all 21 unique capabilities are accepted.
    expect(result.exitCode).toBe(0);
  });

  test.each([
    ["empty", [], "CLAIM_SET_INCOMPLETE"],
    ["deleted", () => canonicalClaims.slice(1), "CLAIM_SET_INCOMPLETE"],
    ["extra", () => [...canonicalClaims, { ...canonicalClaims[0], id: "extra-claim", capability: "extra-capability" }], "CLAIM_SET_EXTRA"],
    ["duplicate ID", () => canonicalClaims.map((claim, index) => index === 1 ? { ...claim, id: canonicalClaims[0].id } : claim), "DUPLICATE_CLAIM_ID"],
    ["duplicate capability", () => canonicalClaims.map((claim, index) => index === 1 ? { ...claim, capability: canonicalClaims[0].capability } : claim), "DUPLICATE_CAPABILITY"],
  ])("rejects %s claim sets", async (_name, makeValue, code) => {
    // Given: one independently malformed canonical set.
    await json("invalid-claims.json", typeof makeValue === "function" ? makeValue() : makeValue);
    // When / Then: exact-set validation fails with its machine code.
    reject(run({ claims: "invalid-claims.json" }), code);
  });

  test.each([
    ["missing claim ID", (claim) => { delete claim.id; }, "CLAIM_ID_REQUIRED"],
    ["missing document", (claim) => { delete claim.document; }, "CLAIM_DOCUMENT_REQUIRED"],
    ["missing gate", (claim) => { delete claim.required_gate; }, "CLAIM_GATE_REQUIRED"],
    ["stale document", (claim) => { claim.document = "docs/removed-guide.md"; }, "STALE_DOCUMENT_PATH"],
    ["unknown capability", (claim) => { claim.capability = "control-api-v3.not-real"; }, "UNKNOWN_CAPABILITY"],
    ["unknown gate", (claim) => { claim.required_gate = "not_a_gate"; }, "UNKNOWN_GATE"],
  ])("rejects %s", async (_name, mutate, code) => {
    const claims = structuredClone(canonicalClaims);
    mutate(claims[0]);
    await json("invalid-field-claims.json", claims);
    reject(run({ claims: "invalid-field-claims.json" }), code);
  });
});

describe("source-derived executable mappings", () => {
  test("rejects a broken payload ref even when it is a string", async () => {
    const schema = JSON.parse(await readFile(schemaSource, "utf8"));
    schema.$defs.receipt.allOf[0].then.properties.payload.$ref = "#/$defs/doesNotExist";
    await json("broken-ref.schema.json", schema);
    reject(run({ schema: "broken-ref.schema.json" }), "RECEIPT_SCHEMA_REF_INVALID");
  });

  test("rejects a capability that exists only in the docs registry", async () => {
    const registry = JSON.parse(await readFile(registrySource, "utf8"));
    registry.capabilities[0].source = {
      file: "tooling/release/capability-registry-v1.json",
      pointer: "/capabilities/0/id",
      value: registry.capabilities[0].id,
    };
    await json("registry-only.json", registry);
    reject(run({ registry: "registry-only.json" }), "CAPABILITY_SOURCE_INVALID");
  });

  test("rejects a missing verifier command mapping", async () => {
    const schema = JSON.parse(await readFile(schemaSource, "utf8"));
    schema["x-jastreamer-gates"] ??= { candidate: {} };
    schema["x-jastreamer-gates"].candidate ??= {};
    delete schema["x-jastreamer-gates"].candidate.command;
    await json("missing-command.schema.json", schema);
    reject(run({ schema: "missing-command.schema.json" }), "GATE_COMMAND_MAPPING_MISSING");
  });

  test("rejects a gate without a compiled payload target", async () => {
    const schema = JSON.parse(await readFile(schemaSource, "utf8"));
    delete schema["x-jastreamer-gates"].candidate.schema_pointer;
    await json("missing-target.schema.json", schema);
    reject(run({ schema: "missing-target.schema.json" }), "MISSING_EXECUTABLE_RECEIPT_MAPPING");
  });

  test.each(["candidate", "server_control_e2e", "k17", "wasapi", "ffmpeg", "external_authorization_pending", "cleanup"])("rejects missing receipt payload application for %s", async (gate) => {
    const schema = JSON.parse(await readFile(schemaSource, "utf8"));
    schema.$defs.receipt.allOf = schema.$defs.receipt.allOf.filter((entry) => entry.if?.properties?.kind?.const !== gate);
    await json(`missing-${gate}.schema.json`, schema);
    reject(run({ schema: `missing-${gate}.schema.json` }), "MISSING_EXECUTABLE_PAYLOAD_MAPPING");
  });

  test("rejects ambiguous receipt payload application", async () => {
    const schema = JSON.parse(await readFile(schemaSource, "utf8"));
    schema.$defs.receipt.allOf.push(structuredClone(schema.$defs.receipt.allOf[0]));
    await json("ambiguous-payload.schema.json", schema);
    reject(run({ schema: "ambiguous-payload.schema.json" }), "AMBIGUOUS_EXECUTABLE_PAYLOAD_MAPPING");
  });

  test("rejects a source pointer that does not resolve", async () => {
    const registry = JSON.parse(await readFile(registrySource, "utf8"));
    registry.capabilities[0].source ??= { file: "packaging/server/manifest.json", value: "server" };
    registry.capabilities[0].source.pointer = "/missing/value";
    await json("broken-source.json", registry);
    reject(run({ registry: "broken-source.json" }), "CAPABILITY_SOURCE_UNRESOLVED");
  });
});
