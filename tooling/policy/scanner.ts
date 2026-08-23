export type Finding = Readonly<{ code: string; subject: string; detail: string }>;
type Entry = Readonly<Record<string, unknown>>;
const entries = (v: unknown): readonly Entry[] => Array.isArray(v) ? v.filter((x): x is Entry => typeof x === "object" && x !== null) : [];
const strings = (v: unknown): readonly string[] => Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : [];
const text = (v: unknown): string => typeof v === "string" ? v : "";
export function scanInventory(input: unknown, policy: Entry): readonly Finding[] {
  const root = typeof input === "object" && input !== null ? input as Entry : {};
  const allowed = new Set(strings(policy.allowedPackagedLicenses));
  const forbidden = new Set(strings(policy.forbiddenPackagedLicenses));
  const forbiddenPackages = new Set(strings(policy.forbiddenPackages).map((name) => name.toLowerCase()));
  const buildOnly = new Set([...allowed, ...strings(policy.buildOnlyLicenses)]);
  const allowedServices = new Set(strings(policy.allowedServices));
  const serviceCodes = new Map(entries(policy.forbiddenServices).map((service) => [text(service.kind), text(service.code)]));
  const actionPin = new RegExp(text(policy.actionPin));
  const findings: Finding[] = [];
  for (const item of entries(root.dependencies)) {
    const license = text(item.license);
    if (forbiddenPackages.has(text(item.name).toLowerCase())) findings.push({ code: "FORBIDDEN_PACKAGE", subject: text(item.name), detail: "package excluded by policy" });
    if (forbidden.has(license)) findings.push({ code: "FORBIDDEN_LICENSE", subject: text(item.name), detail: license });
    else if (!allowed.has(license)) findings.push({ code: "UNKNOWN_LICENSE", subject: text(item.name), detail: license || "missing" });
    if (item.nonCommercial === true) findings.push({ code: "NONCOMMERCIAL_MODEL", subject: text(item.name), detail: "non-commercial model" });
  }
  for (const item of entries(root.tools)) {
    if (item.packaged === true) findings.push({ code: "TOOL_PACKAGED", subject: text(item.name), detail: "build tool must not be packaged" });
    if (item.buildOnly !== true) findings.push({ code: "TOOL_NOT_BUILD_ONLY", subject: text(item.name), detail: "tool must be build-only" });
    if (!buildOnly.has(text(item.license))) findings.push({ code: "UNKNOWN_BUILD_LICENSE", subject: text(item.name), detail: text(item.license) || "missing" });
    if (!text(item.version) || text(item.version).toLowerCase() === "pinned") findings.push({ code: "UNPINNED_TOOL", subject: text(item.name), detail: text(item.version) || "missing" });
    if (item.paid === true) findings.push({ code: "PAID_TOOL", subject: text(item.name), detail: "paid build tool" });
    try { new URL(text(item.source)); } catch { findings.push({ code: "TOOL_SOURCE", subject: text(item.name), detail: "absolute source URL required" }); }
  }
  for (const service of entries(root.services)) {
    const kind = text(service.kind);
    const code = serviceCodes.get(kind) ?? "";
    if (code) findings.push({ code, subject: text(service.name), detail: kind });
    else if (!allowedServices.has(kind)) findings.push({ code: "UNKNOWN_SERVICE", subject: text(service.name), detail: kind || "missing" });
  }
  for (const item of entries(root.checkpoints)) if (item.reviewed !== true) findings.push({ code: "UNREVIEWED_CHECKPOINT", subject: text(item.name), detail: "checkpoint requires owner/legal review" });
  if (root.oidc === true) findings.push({ code: "OIDC_PERMISSION", subject: "workflow", detail: "id-token" });
  for (const action of entries(root.actions)) if (!actionPin.test(text(action.sha))) findings.push({ code: "UNPINNED_ACTION", subject: text(action.name), detail: text(action.sha) || "missing SHA" });
  for (const item of entries(root.packaged)) if (text(item.name).toLowerCase().includes("wix") && item.buildOnly !== true) findings.push({ code: "PACKAGED_WIX", subject: text(item.name), detail: "WiX is build-only" });
  return findings;
}
if (import.meta.main) {
  const path = Bun.argv[2];
  if (!path) { console.error("usage: bun scanner.ts <inventory.json>"); process.exit(64); }
  const policy = await Bun.file(new URL("./policy.json", import.meta.url)).json() as Entry;
  const findings = scanInventory(await Bun.file(path).json(), policy);
  console.log(JSON.stringify({ findings }, null, 2));
  if (findings.length) process.exit(65);
}
