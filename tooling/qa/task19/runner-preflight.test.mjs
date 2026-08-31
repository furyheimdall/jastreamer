import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dirname, "../../..");
const workflow = () => readFileSync(resolve(root, ".github/workflows/task19-installed-qualification.yml"), "utf8");
const script = () => readFileSync(resolve(root, "tooling/qa/task19/runner-preflight.ps1"), "utf8");

describe("Task19 authoritative protected-runner preflight", () => {
  test("authorizes protected default-branch context before repository execution", () => {
    const value = workflow(); const authorize = value.slice(value.indexOf("  authorize:"), value.indexOf("  qualify:")); const qualify = value.slice(value.indexOf("  qualify:"));
    expect(value).toContain("workflow_dispatch:"); expect(value).toContain("github.ref_protected == true");
    expect(authorize).not.toContain("actions/checkout"); expect(qualify).toContain("needs: authorize");
    expect(qualify).toContain("runs-on: [self-hosted, Windows, X64, task19-protected]");
    expect(qualify).toContain("actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683"); expect(qualify).toContain("persist-credentials: false");
    expect(value).not.toMatch(/contents:\s*write|packages:\s*write|actions:\s*write/);
  });

  test("requires one authorized Android device without retaining its serial", () => {
    const value = script();
    expect(value).toMatch(/& \$AdbPath devices -l/); expect(value).toContain("TASK19_ADB_DEVICE_COUNT_INVALID"); expect(value).toContain("wait-for-device");
    expect(value).toContain("WaitForExit(120000)"); expect(value).toContain("androidDeviceSerialSha256"); expect(value).toContain("publicationWrites = 0");
    expect(value).not.toContain("Start-Sleep"); expect(value).not.toMatch(/androidDeviceSerial\s*=/);
  });

  test("uses one pinned Android-tools and physical preflight flow", () => {
    const value = workflow();
    for (const token of ["platform-tools_r37.0.1-win.zip", "45f4d63113e895ebde0c90f194099a4676b6ac653bd28d54314a9e022bbc1a99", "build-tools;35.0.0", "Get-FileHash", "Expand-Archive", "runner-preflight.ps1 -Output task19-runner-preflight.json"]) expect(value).toContain(token);
    expect(value).not.toMatch(/\b(?:choco|winget)\b/); expect(value).not.toContain("task19-runner-preflight.yml");
  });

  test("preserves stale-receipt safety by generating preflight in the executing job", () => {
    const value = workflow(); const preflight = value.indexOf("runner-preflight.ps1 -Output task19-runner-preflight.json"); const execute = value.indexOf("installed-runner.mjs --execute");
    expect(preflight).toBeGreaterThan(0); expect(execute).toBeGreaterThan(preflight); expect(value.slice(preflight, execute)).not.toContain("actions/upload-artifact@");
  });
});
