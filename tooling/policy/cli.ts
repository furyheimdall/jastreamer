import { scanInventory, type Finding } from "./scanner";
type Entry = Readonly<Record<string, unknown>>;
type AuditResult = Readonly<{ findings: readonly Finding[]; notices: string }>;
const repository = new URL("../../", import.meta.url);
const file = (name: string): string => new URL(name, repository).pathname;
const externalPath = (name: string): string => name.startsWith("/") ? name : file(name);
const entries = (v: unknown): readonly Entry[] => Array.isArray(v) ? v.filter((x): x is Entry => typeof x === "object" && x !== null) : [];
const strings = (v: unknown): readonly string[] => Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : [];
const text = (v: unknown): string => typeof v === "string" ? v : "";
const finding = (code: string, subject: string, detail: string): Finding => ({ code, subject, detail });
const collect = async <T>(values: AsyncIterable<T>): Promise<readonly T[]> => {
  const result: T[] = [];
  for await (const value of values) result.push(value);
  return result;
};
const bytesEqual = (left: ArrayBuffer, right: ArrayBuffer): boolean => {
  const a = new Uint8Array(left);
  const b = new Uint8Array(right);
  return a.length === b.length && a.every((value, index) => value === b[index]);
};
const licenseText = async (item: Entry): Promise<string> => {
  const path = text(item.license_file);
  if (!path.startsWith("tooling/policy/licenses/") || path.includes("..")) return "";
  return Bun.file(file(path)).text();
};

const scanWorkflows = async (workspace: string, policy: Entry): Promise<readonly Finding[]> => {
  const findings: Finding[] = [];
  const workflowsRoot = `${workspace}/.github/workflows`;
  let workflows: readonly string[] = [];
  try {
    workflows = await collect(new Bun.Glob("*").scan({ cwd: workflowsRoot }));
  } catch (error: unknown) {
    const code = typeof error === "object" && error !== null && "code" in error ? error.code : undefined;
    if (code !== "ENOENT") throw error;
  }
  const contents = await Promise.all(workflows.map(async (path) => ({ path: `.github/workflows/${path}`, lines: (await Bun.file(`${workflowsRoot}/${path}`).text()).split("\n") })));
  const actionPin = new RegExp(text(policy.actionPin));
  for (const workflow of contents) for (const line of workflow.lines) {
    const match = line.match(/uses:\s*["']?([^\s"'#]+)/);
    const reference = match?.[1] ?? "";
    const revision = reference.includes("@") ? reference.slice(reference.lastIndexOf("@") + 1) : "";
    if (reference && !reference.startsWith("./") && !actionPin.test(revision)) findings.push(finding("UNPINNED_ACTION", workflow.path, reference));
  }
  for (const permission of strings(policy.forbiddenWorkflowPermissions)) for (const workflow of contents) {
    const line = workflow.lines.find((candidate) => candidate.includes(permission));
    if (line) findings.push(finding("OIDC_PERMISSION", workflow.path, line.trim()));
  }
  for (const pattern of strings(policy.forbiddenUrlPatterns)) for (const workflow of contents) {
    const expression = new RegExp(pattern, "i");
    const line = workflow.lines.find((candidate) => expression.test(candidate));
    if (line) findings.push(finding("FORBIDDEN_SERVICE_URL", workflow.path, line.trim()));
  }
  return findings;
};

const audit = async (workspace: string, closureDirectory: string): Promise<AuditResult> => {
  const findings: Finding[] = [];
  const inventory = await Bun.file(file("tooling/policy/inventory.json")).json() as Entry;
  const policy = await Bun.file(file("tooling/policy/policy.json")).json() as Entry;
  const requiredServices = strings(policy.requiredExternalServices);
  if (JSON.stringify(inventory.required_external_services) !== JSON.stringify(requiredServices)) findings.push(finding("EXTERNAL_SERVICE_POLICY", "inventory", `required services must equal ${JSON.stringify(requiredServices)}`));
  if (text(policy.sourceLicense) !== "Apache-2.0" || !bytesEqual(await Bun.file(file("tooling/policy/apache-2.0.txt")).arrayBuffer(), await Bun.file(`${workspace}/LICENSE`).arrayBuffer())) findings.push(finding("LICENSE_MISMATCH", "LICENSE", "not canonical Apache-2.0"));
  findings.push(...scanInventory(inventory, policy));
  const expectedSbom = policy.sbom && typeof policy.sbom === "object" ? policy.sbom as Entry : {};
  const inventorySbom = inventory.sbom && typeof inventory.sbom === "object" ? inventory.sbom as Entry : {};
  if (text(inventorySbom.format) !== text(expectedSbom.format) || text(inventorySbom.provenance) !== text(expectedSbom.provenance)) findings.push(finding("SBOM_CONTRACT", "inventory", "SBOM/provenance differs from policy"));
  const inventoryPackages = entries(inventory.dependencies).concat(entries(inventory.tools)).filter((x) => x.packaged === true);
  const expected = new Map(inventoryPackages.map((x) => [text(x.name), x]));
  const closureFiles = await collect(new Bun.Glob("*.json").scan({ cwd: closureDirectory }));
  const records: Entry[] = [];
  for (const path of closureFiles) {
    const closure = await Bun.file(`${closureDirectory}/${path}`).json() as Entry;
    if (text(closure.sbom && (closure.sbom as Entry).format) !== text(expectedSbom.format)) findings.push(finding("SBOM_CONTRACT", path, `format must be ${text(expectedSbom.format)}`));
    if (text(closure.provenance && (closure.provenance as Entry).format) !== text(expectedSbom.provenance)) findings.push(finding("PROVENANCE_CONTRACT", path, `format must be ${text(expectedSbom.provenance)}`));
    for (const item of entries(closure.packages)) {
      records.push({ ...item, component: text(closure.component), license_text: await licenseText(item) });
      const inventoryItem = expected.get(text(item.package));
      if (!inventoryItem || text(inventoryItem.version) !== text(item.version) || text(inventoryItem.license) !== text(item.license) || text(inventoryItem.source) !== text(item.source) || text(inventoryItem.license_file) !== text(item.license_file)) findings.push(finding("CLOSURE_INVENTORY_MISMATCH", text(item.package), "closure differs from packaged inventory"));
    }
  }
  for (const item of inventoryPackages) if (!records.some((x) => text(x.package) === text(item.name))) findings.push(finding("CLOSURE_INCOMPLETE", text(item.name), "packaged inventory item absent from closure"));
  const noticeSchema = await Bun.file(file("tooling/policy/notices.schema.json")).json() as Entry;
  const required = strings(noticeSchema.required);
  const sorted = records.toSorted((a, b) => `${text(a.component)}\0${text(a.package)}`.localeCompare(`${text(b.component)}\0${text(b.package)}`));
  for (const item of sorted) for (const key of required) if (!text(item[key])) findings.push(finding("NOTICE_SCHEMA", text(item.package), `missing ${key}`));
  const noticeKeys = new Set<string>();
  for (const item of sorted) {
    const key = `${text(item.component)}\0${text(item.package)}`;
    if (noticeKeys.has(key)) findings.push(finding("NOTICE_DUPLICATE", text(item.package), "duplicate component/package notice"));
    noticeKeys.add(key);
    try { new URL(text(item.source)); } catch { findings.push(finding("NOTICE_SCHEMA", text(item.package), "source must be an absolute URL")); }
  }
  const notices = sorted.map((item) => JSON.stringify(Object.fromEntries(required.map((key) => [key, text(item[key])])))).join("\n") + (sorted.length ? "\n" : "");
  findings.push(...await scanWorkflows(workspace, policy));
  return { findings, notices };
};
const args = Bun.argv.slice(2);
if (args[0] === "policy" && args[1] === "verify" && (args.includes("--all") || args.includes("--fixture"))) {
  const policy = await Bun.file(file("tooling/policy/policy.json")).json() as Entry;
  const workspaceIndex = args.indexOf("--workspace");
  const closureIndex = args.indexOf("--closure-dir");
  const workspace = externalPath(workspaceIndex >= 0 ? args[workspaceIndex + 1] ?? "" : ".");
  const closureDirectory = externalPath(closureIndex >= 0 ? args[closureIndex + 1] ?? "" : "tooling/policy/closures");
  const fixtureIndex = args.indexOf("--fixture");
  const audited = fixtureIndex >= 0
    ? { findings: scanInventory(await Bun.file(externalPath(args[fixtureIndex + 1] ?? "")).json(), policy), notices: "" }
    : await audit(workspace, closureDirectory);
  if (audited.findings.length === 0 && !args.includes("--fixture")) await Bun.write(file("tooling/policy/THIRD_PARTY_NOTICES.generated"), audited.notices);
  const outputIndex = args.indexOf("--output");
  const output = outputIndex >= 0 ? args[outputIndex + 1] : undefined;
  const result = { findings: audited.findings, required_external_services: strings(policy.requiredExternalServices), notices: "tooling/policy/THIRD_PARTY_NOTICES.generated" };
  if (output) await Bun.write(output, JSON.stringify(result, null, 2) + "\n");
  console.log(JSON.stringify(result, null, 2));
  process.exit(audited.findings.length ? 65 : 0);
}
console.error("usage: componentctl policy verify --all --output <json>"); process.exit(64);
