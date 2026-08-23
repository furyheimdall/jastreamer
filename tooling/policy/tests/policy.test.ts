import { expect, test } from "bun:test";
import { scanInventory } from "../scanner";
type Entry = Readonly<Record<string, unknown>>;
type Inventory = Readonly<{
  dependencies: readonly Readonly<{ name: string; paid?: boolean }>[];
  required_external_services: readonly string[];
}>;
type NoticeSchema = Readonly<{ required: readonly string[] }>;
const policy = await Bun.file("tooling/policy/policy.json").json() as Entry;

const runPolicy = (...args: readonly string[]) =>
  Bun.spawn(["./tooling/componentctl", "policy", "verify", ...args], {
    stdout: "pipe",
    stderr: "pipe",
  });

const outputOf = async (process: ReturnType<typeof runPolicy>) => {
  const [exitCode, stdout, stderr] = await Promise.all([
    process.exited,
    new Response(process.stdout).text(),
    new Response(process.stderr).text(),
  ]);
  return { exitCode, stdout, stderr };
};

test("accepts the zero-paid packaged inventory", async () => {
  const inventory = await Bun.file("tooling/policy/tests/fixtures/policy/valid.json").json();
  expect(scanInventory(inventory, policy)).toEqual([]);
});

test("reports every forbidden closure finding with stable codes", async () => {
  const inventory = await Bun.file("tooling/policy/tests/fixtures/policy/forbidden.json").json();
  expect(scanInventory(inventory, policy).map((finding) => finding.code)).toEqual([
    "FORBIDDEN_LICENSE", "NONCOMMERCIAL_MODEL", "UNKNOWN_LICENSE",
    "KEYLESS_SIGSTORE", "ACME_SERVICE", "EXTERNAL_TIMESTAMP",
    "OIDC_PERMISSION", "UNPINNED_ACTION", "PACKAGED_WIX",
  ]);
});

test("uses machine policy for licenses services and action pins", () => {
  const configured = {
    ...policy,
    allowedPackagedLicenses: ["Apache-2.0"],
    forbiddenPackagedLicenses: ["MIT"],
    forbiddenServices: [{ kind: "custom-service", code: "CUSTOM_SERVICE" }],
    actionPin: "^[A-Z]{4}$",
  };
  expect(scanInventory({
    dependencies: [{ name: "dependency", license: "MIT" }],
    services: [{ name: "service", kind: "custom-service" }],
    actions: [{ name: "action", sha: "abcd" }],
  }, configured).map((finding) => finding.code)).toEqual([
    "FORBIDDEN_LICENSE",
    "CUSTOM_SERVICE",
    "UNPINNED_ACTION",
  ]);
});

test("enforces exact build-only tool classifications", () => {
  expect(scanInventory({
    tools: [
      { name: "packaged", version: "1.0.0", license: "MIT", buildOnly: true, packaged: true, source: "https://example.invalid/packaged" },
      { name: "runtime", version: "1.0.0", license: "MIT", buildOnly: false, packaged: false, source: "https://example.invalid/runtime" },
      { name: "unknown", version: "1.0.0", license: "LicenseRef-Unknown", buildOnly: true, packaged: false, source: "https://example.invalid/unknown" },
      { name: "placeholder", version: "pinned", license: "MIT", buildOnly: true, packaged: false, source: "https://example.invalid/placeholder" },
    ],
  }, policy).map((finding) => finding.code)).toEqual([
    "TOOL_PACKAGED",
    "TOOL_NOT_BUILD_ONLY",
    "UNKNOWN_BUILD_LICENSE",
    "UNPINNED_TOOL",
  ]);
});

test("blocks excluded analyzers and unreviewed checkpoints", () => {
  expect(scanInventory({
    dependencies: [{ name: "TensorFlow", license: "Apache-2.0" }],
    checkpoints: [{ name: "MTG weights", reviewed: false }],
  }, policy).map((finding) => finding.code)).toEqual([
    "FORBIDDEN_PACKAGE",
    "UNREVIEWED_CHECKPOINT",
  ]);
});

test("invokes the real CLI successfully for all policy inputs", async () => {
  expect((await outputOf(runPolicy("--all"))).exitCode).toBe(0);
});

test("generates deterministic exact third-party notices", async () => {
  expect((await outputOf(runPolicy("--all"))).exitCode).toBe(0);
  const first = await Bun.file("tooling/policy/THIRD_PARTY_NOTICES.generated").text();
  expect((await outputOf(runPolicy("--all"))).exitCode).toBe(0);
  expect(await Bun.file("tooling/policy/THIRD_PARTY_NOTICES.generated").text()).toBe(first);
  const flutterLicense = await Bun.file("tooling/policy/licenses/flutter-BSD-3-Clause.txt").text();
  const notices = first.trim().split("\n").map((line: string) => JSON.parse(line)) as readonly Readonly<Record<string, string>>[];
  expect(notices).toHaveLength(11);
  expect(notices[0]).toEqual({
    component: "control",
    package: "Flutter runtime",
    version: "3.44.0",
    license: "BSD-3-Clause",
    source: "https://github.com/flutter/flutter",
    license_text: flutterLicense,
  });
  expect(notices.filter((notice) => notice.component === "server").map((notice) => notice.package).toSorted()).toEqual([
    "github.com/dhowden/tag",
    "github.com/dustin/go-humanize",
    "github.com/google/uuid",
    "github.com/remyoudompheng/bigfft",
    "golang.org/x/sys",
    "golang.org/x/text",
    "modernc.org/libc",
    "modernc.org/mathutil",
    "modernc.org/memory",
    "modernc.org/sqlite",
  ]);
  expect(notices.every((notice) => notice.license_text.length > 100)).toBe(true);
});

test("keeps the exact packaged runtime source and version", async () => {
  const inventory = await Bun.file("tooling/policy/inventory.json").json() as Inventory;
  const flutter = inventory.dependencies.find((item) => item.name === "Flutter runtime");
  expect(flutter).toMatchObject({ version: "3.44.0", source: "https://github.com/flutter/flutter", packaged: true });
});

test("real CLI rejects the forbidden fixture with nine findings", async () => {
  const result = await outputOf(runPolicy("--fixture", "tooling/fixtures/policy/forbidden-services-and-licenses.yaml"));
  expect(result.exitCode).toBe(65);
  const findings = JSON.parse(result.stdout).findings as readonly Readonly<{ code: string; subject: string }>[];
  expect([...new Set(findings.map((finding) => finding.code))]).toEqual([
    "FORBIDDEN_LICENSE", "NONCOMMERCIAL_MODEL", "UNKNOWN_LICENSE",
    "KEYLESS_SIGSTORE", "ACME_SERVICE", "EXTERNAL_TIMESTAMP",
    "OIDC_PERMISSION", "UNPINNED_ACTION", "PACKAGED_WIX",
  ]);
  expect(findings.filter((finding) => finding.code === "FORBIDDEN_LICENSE").map((finding) => finding.subject)).toEqual([
    "gpl2", "gpl3", "agpl", "sspl", "commons",
  ]);
});

test("audits workflow permissions pins urls and canonical license", async () => {
  const result = await outputOf(runPolicy(
    "--all",
    "--workspace", "tooling/policy/tests/fixtures/workspace-forbidden",
  ));
  expect(result.exitCode).toBe(65);
  expect(JSON.parse(result.stdout).findings.map((finding: { readonly code: string }) => finding.code)).toEqual([
    "LICENSE_MISMATCH",
    "UNPINNED_ACTION",
    "OIDC_PERMISSION",
    "FORBIDDEN_SERVICE_URL",
  ]);
});

test("rejects unclassified closures without overwriting valid notices", async () => {
  expect((await outputOf(runPolicy("--all"))).exitCode).toBe(0);
  const before = await Bun.file("tooling/policy/THIRD_PARTY_NOTICES.generated").text();
  const result = await outputOf(runPolicy(
    "--all",
    "--closure-dir", "tooling/policy/tests/fixtures/closures-unclassified",
  ));
  expect(result.exitCode).toBe(65);
  expect(JSON.parse(result.stdout).findings.map((finding: { readonly code: string }) => finding.code)).toContain("CLOSURE_INVENTORY_MISMATCH");
  expect(await Bun.file("tooling/policy/THIRD_PARTY_NOTICES.generated").text()).toBe(before);
});

test("does not retain placeholder versions", async () => {
  const inventory = await Bun.file("tooling/policy/inventory.json").text();
  expect(inventory.includes('"pinned"')).toBe(false);
});

test("ships a zero-paid inventory and notices generation contract", async () => {
  const inventory = await Bun.file("tooling/policy/inventory.json").json() as Inventory;
  expect(inventory.required_external_services).toEqual(["github"]);
  expect(inventory.dependencies.every((item: { readonly paid?: boolean }) => item.paid !== true)).toBe(true);
  const contract = await Bun.file("tooling/policy/notices.schema.json").json() as NoticeSchema;
  expect(contract.required).toEqual(["component", "package", "version", "license", "source", "license_text"]);
});
