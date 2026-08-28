import { existsSync, readFileSync, realpathSync } from "node:fs";
import { createRequire } from "node:module";
import { relative, resolve, sep } from "node:path";
import { registerSchemaFormats } from "../qa/schema-validation.mjs";

const root = resolve(import.meta.dirname, "..", "..");
const loadPinnedModule = createRequire(resolve(root, "tooling/qa/package.json"));
const Ajv2020 = loadPinnedModule("ajv/dist/2020.js").default;
const defaultRegistry = "tooling/release/capability-registry-v1.json";
const defaultContract = "tooling/docs/claims-contract-v1.json";
const sources = [
  "apps/server/component.yaml", "apps/control/component.yaml", "apps/renderer/component.yaml",
  "packaging/server/manifest.json", "packaging/control/manifest.json", "packaging/renderer/config.json",
  defaultRegistry, defaultContract,
];
const guides = [
  "docs/synology.md", "docs/server-pairing.md", "docs/control-windows.md", "docs/control-web.md",
  "docs/control-android.md", "docs/renderer-windows.md", "docs/releasing.md",
];
const claimFields = ["id", "document", "component", "capability", "required_gate"];
const sourcePrefixes = ["contracts/", "packaging/", "deploy/"];

const fail = (code, detail) => {
  console.error(`${code}: ${detail}`);
  return false;
};

const parseArguments = (values) => {
  const accepted = new Set(["--claims", "--receipt-schema", "--registry", "--claim-contract"]);
  const options = new Map();
  for (let index = 0; index < values.length; index += 2) {
    const name = values[index]; const value = values[index + 1];
    if (!accepted.has(name) || value === undefined || options.has(name)) return undefined;
    options.set(name, value);
  }
  if (!options.has("--claims") || !options.has("--receipt-schema")) return undefined;
  return {
    claims: resolve(root, options.get("--claims")),
    receiptSchema: resolve(root, options.get("--receipt-schema")),
    registry: resolve(root, options.get("--registry") ?? defaultRegistry),
    claimContract: resolve(root, options.get("--claim-contract") ?? defaultContract),
  };
};

const readJson = (path, code) => {
  if (!existsSync(path)) { fail(code, path); return undefined; }
  try { return JSON.parse(readFileSync(path, "utf8")); }
  catch (error) { fail(code, error instanceof Error ? error.message : path); return undefined; }
};

const insideRoot = (path) => {
  const location = relative(realpathSync(root), realpathSync(path));
  return location !== ".." && !location.startsWith(`..${sep}`);
};

const pointerValue = (input, pointer) => {
  if (pointer === "") return input;
  if (typeof pointer !== "string" || !pointer.startsWith("/")) return undefined;
  let value = input;
  for (const encoded of pointer.slice(1).split("/")) {
    const key = encoded.replaceAll("~1", "/").replaceAll("~0", "~");
    if (value === null || typeof value !== "object" || !(key in value)) return undefined;
    value = value[key];
  }
  return value;
};

const equal = (left, right) => JSON.stringify(left) === JSON.stringify(right);

const compileGates = (schema) => {
  const installedPath = resolve(root, "tooling/qa/task19/installed-product-receipt.schema.json");
  const installed = readJson(installedPath, "RECEIPT_SCHEMA_REF_INVALID");
  if (installed === undefined) return undefined;
  const ajv = registerSchemaFormats(new Ajv2020({ allErrors: true, strict: false }));
  try {
    ajv.addSchema(installed);
    ajv.addSchema(installed, "https://jastreamer.invalid/schemas/product-qa-receipt/task19/installed-product-receipt.schema.json");
    ajv.compile(schema);
  } catch (error) {
    fail("RECEIPT_SCHEMA_REF_INVALID", error instanceof Error ? error.message : "compile");
    return undefined;
  }
  const kinds = schema?.$defs?.receiptKind?.enum;
  const mappings = schema?.["x-jastreamer-gates"];
  if (!Array.isArray(kinds) || mappings === null || typeof mappings !== "object") {
    fail("RECEIPT_SCHEMA_INVALID", "receipt kinds and executable mappings are required");
    return undefined;
  }
  const applications = new Map();
  for (const entry of schema?.$defs?.receipt?.allOf ?? []) {
    const gate = entry?.if?.properties?.kind?.const;
    const payloadRef = entry?.then?.properties?.payload?.$ref;
    if (typeof gate !== "string" || typeof payloadRef !== "string") continue;
    const refs = applications.get(gate) ?? [];
    refs.push(payloadRef); applications.set(gate, refs);
  }
  const result = new Map();
  for (const gate of kinds) {
    const mapping = mappings[gate];
    if (mapping === null || typeof mapping !== "object" || !Array.isArray(mapping.command)) {
      fail("GATE_COMMAND_MAPPING_MISSING", gate); return undefined;
    }
    if (typeof mapping.schema_pointer !== "string" || pointerValue(schema, mapping.schema_pointer.replace(/^#/, "")) === undefined) {
      fail("MISSING_EXECUTABLE_RECEIPT_MAPPING", gate); return undefined;
    }
    const payloadApplications = applications.get(gate) ?? [];
    if (payloadApplications.length === 0 || payloadApplications[0] !== mapping.schema_pointer) {
      fail("MISSING_EXECUTABLE_PAYLOAD_MAPPING", gate); return undefined;
    }
    if (payloadApplications.length !== 1) {
      fail("AMBIGUOUS_EXECUTABLE_PAYLOAD_MAPPING", gate); return undefined;
    }
    try { if (ajv.getSchema(`${schema.$id}${mapping.schema_pointer}`) === undefined) throw new Error("schema pointer did not compile"); }
    catch (error) { fail("RECEIPT_SCHEMA_REF_INVALID", error instanceof Error ? error.message : gate); return undefined; }
    if (typeof mapping.module !== "string" || !existsSync(resolve(root, mapping.module)) ||
        mapping.command.length < 3 || mapping.command[0] !== "bun" || mapping.command[1] !== "test" ||
        mapping.command.slice(2).some((path) => typeof path !== "string" || !existsSync(resolve(root, path))) ||
        typeof mapping.machine_receipt_key !== "string" || !mapping.machine_receipt_key.includes(`kind=${gate}`)) {
      fail("GATE_COMMAND_MAPPING_MISSING", gate); return undefined;
    }
    result.set(gate, mapping);
  }
  if (Object.keys(mappings).length !== result.size) { fail("UNKNOWN_GATE", "receipt mapping"); return undefined; }
  return result;
};

const validateClaimShape = (claim, label) => {
  if (claim === null || typeof claim !== "object" || Array.isArray(claim)) return fail("CLAIM_INVALID", label);
  const keys = Object.keys(claim);
  if (!keys.includes("id")) return fail("CLAIM_ID_REQUIRED", label);
  if (!keys.includes("document")) return fail("CLAIM_DOCUMENT_REQUIRED", label);
  if (!keys.includes("required_gate")) return fail("CLAIM_GATE_REQUIRED", label);
  if (keys.length !== claimFields.length || claimFields.some((field) => !keys.includes(field))) return fail("CLAIM_FIELD_INVALID", label);
  return !claimFields.some((field) => typeof claim[field] !== "string" || claim[field].length === 0) || fail("CLAIM_FIELD_INVALID", label);
};

const resolveCapability = (capability, registryPath) => {
  if (capability === null || typeof capability !== "object" || typeof capability.source?.file !== "string") return fail("CAPABILITY_REGISTRY_INVALID", capability?.id ?? "entry");
  if (resolve(root, capability.source.file) === registryPath || !sourcePrefixes.some((prefix) => capability.source.file.startsWith(prefix)) || !capability.source.file.endsWith(".json")) return fail("CAPABILITY_SOURCE_INVALID", capability.id);
  const sourcePath = resolve(root, capability.source.file);
  const source = readJson(sourcePath, "CAPABILITY_SOURCE_UNRESOLVED");
  if (source === undefined || !insideRoot(sourcePath)) return false;
  const value = pointerValue(source, capability.source.pointer);
  return value !== undefined && equal(value, capability.source.value) || fail("CAPABILITY_SOURCE_UNRESOLVED", capability.id);
};

const verifyClaims = (context) => {
  const { claims, registry, contract, gates, registryPath } = context;
  if (!Array.isArray(claims)) return fail("CLAIMS_INVALID", "claims must be an array");
  if (contract?.schema_version !== 1 || contract.claim_set_id !== "jastreamer-operational-claims-v1" || !Array.isArray(contract.claims) || contract.claims.length !== 21) return fail("CLAIM_CONTRACT_INVALID", "canonical v1");
  if (registry?.schema_version !== 1 || !Array.isArray(registry.capabilities)) return fail("CAPABILITY_REGISTRY_INVALID", registryPath);
  if (claims.length < contract.claims.length) return fail("CLAIM_SET_INCOMPLETE", `${claims.length}/21`);
  if (claims.length > contract.claims.length) return fail("CLAIM_SET_EXTRA", `${claims.length}/21`);
  const capabilities = new Map(registry.capabilities.map((item) => [item.id, item]));
  const ids = new Set(); const capabilityIds = new Set(); let ok = true;
  for (const [index, claim] of claims.entries()) {
    if (!validateClaimShape(claim, `claims[${index}]`)) { ok = false; continue; }
    if (ids.has(claim.id)) ok = fail("DUPLICATE_CLAIM_ID", claim.id) && ok; ids.add(claim.id);
    if (capabilityIds.has(claim.capability)) ok = fail("DUPLICATE_CAPABILITY", claim.capability) && ok; capabilityIds.add(claim.capability);
    const documentPath = resolve(root, claim.document);
    if (!guides.includes(claim.document) || !existsSync(documentPath) || !insideRoot(documentPath)) ok = fail("STALE_DOCUMENT_PATH", claim.document) && ok;
    const capability = capabilities.get(claim.capability);
    if (capability === undefined) { ok = fail("UNKNOWN_CAPABILITY", claim.capability) && ok; continue; }
    if (capability.component !== claim.component) ok = fail("CAPABILITY_COMPONENT_MISMATCH", claim.id) && ok;
    if (!gates.has(claim.required_gate)) ok = fail("UNKNOWN_GATE", claim.required_gate) && ok;
    if (capability.required_gate !== claim.required_gate) ok = fail("CAPABILITY_GATE_MISMATCH", claim.id) && ok;
    if (!resolveCapability(capability, registryPath)) ok = false;
  }
  if (ok && !equal(claims, contract.claims)) ok = fail("CLAIM_SET_MISMATCH", contract.claim_set_id);
  if (ok && (registry.capabilities.length !== 21 || capabilities.size !== 21)) ok = fail("CAPABILITY_REGISTRY_INVALID", "canonical cardinality") && ok;
  return ok;
};

let ok = true;
for (const path of [...sources, ...guides]) if (!existsSync(resolve(root, path))) ok = fail("MISSING_SOURCE", path) && ok;
const readme = readFileSync(resolve(root, "README.md"), "utf8");
for (const product of ["jastreamer-server", "jastreamer-control", "jastreamer-renderer"]) if (!readme.includes(product)) ok = fail("README_PRODUCT_MISSING", product) && ok;
for (const guide of guides) if (!readme.includes(`(${guide})`)) ok = fail("README_GUIDE_MISSING", guide) && ok;

const options = parseArguments(process.argv.slice(2));
if (options === undefined) ok = fail("USAGE", "--claims <path> --receipt-schema <path>") && ok;
else {
  const claims = readJson(options.claims, "CLAIMS_FILE_MISSING");
  const registry = readJson(options.registry, "CAPABILITY_REGISTRY_MISSING");
  const contract = readJson(options.claimContract, "CLAIM_CONTRACT_MISSING");
  const schema = readJson(options.receiptSchema, "RECEIPT_SCHEMA_MISSING");
  const gates = schema === undefined ? undefined : compileGates(schema);
  if ([claims, registry, contract, schema, gates].some((value) => value === undefined)) ok = false;
  else ok = verifyClaims({ claims, registry, contract, gates, registryPath: options.registry }) && ok;
}
if (ok) console.log("Documentation verification PASSED"); else console.error("Documentation verification FAILED");
process.exit(ok ? 0 : 1);
