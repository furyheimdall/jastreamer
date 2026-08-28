import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dirname, "../../..");
const workflow = () => readFileSync(
  resolve(root, ".github/workflows/task19-runner-preflight.yml"),
  "utf8",
);
const script = () => readFileSync(
  resolve(root, "tooling/qa/task19/runner-preflight.ps1"),
  "utf8",
);

describe("Task19 self-hosted runner preflight", () => {
  test("authorizes the default branch before scheduling repository code", () => {
    const value = workflow();
    const authorize = value.slice(
      value.indexOf("  authorize:"),
      value.indexOf("  preflight:"),
    );
    const preflight = value.slice(value.indexOf("  preflight:"));

    expect(value).toContain("workflow_dispatch:");
    expect(value).toContain("permissions: { contents: read }");
    expect(authorize).not.toContain("actions/checkout");
    expect(preflight).toContain("needs: authorize");
    expect(preflight).toContain("runs-on: [self-hosted, Windows, X64]");
    expect(preflight).toContain(
      "actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683",
    );
    expect(preflight).toContain("persist-credentials: false");
    expect(preflight).toContain("shell: powershell");
    expect(preflight).not.toContain("shell: pwsh");
    expect(value).not.toMatch(/contents:\s*write|packages:\s*write|actions:\s*write/);
  });

  test("requires one authorized Android device without retaining its serial", () => {
    const value = script();

    expect(value).toMatch(/& \$AdbPath devices -l/);
    expect(value).toContain("TASK19_ADB_DEVICE_COUNT_INVALID");
    expect(value).toContain("wait-for-device");
    expect(value).toContain("WaitForExit(120000)");
    expect(value).toContain("TASK19_ADB_AUTHORIZATION_TIMEOUT");
    expect(value).toContain("androidDeviceSerialSha256");
    expect(value).toContain("publicationWrites = 0");
    expect(value).toContain("[Security.Cryptography.SHA256]::Create()");
    expect(value).toContain("ComputeHash");
    expect(value).not.toContain("::HashData");
    expect(value).not.toContain("[Convert]::ToHexString");
    expect(value).not.toContain("Start-Sleep");
    expect(value).not.toMatch(/androidDeviceSerial\s*=/);
  });

  test("uploads only the redacted machine preflight receipt", () => {
    const value = workflow();

    expect(value).toContain(
      "tooling/qa/task19/runner-preflight.ps1 -Output task19-runner-preflight.json",
    );
    expect(value).toContain("name: task19-runner-preflight");
    expect(value).toContain("path: task19-runner-preflight.json");
    expect(value).toContain("if-no-files-found: error");
  });

  test("bootstraps digest-pinned Android platform tools without package managers", () => {
    const value = workflow();
    const install = value.slice(
      value.indexOf("Install exact Android platform tools"),
      value.indexOf("Verify Windows runner and one authorized USB Android device"),
    );

    expect(install).toContain(
      "platform-tools_r37.0.1-win.zip",
    );
    expect(install).toContain(
      "45f4d63113e895ebde0c90f194099a4676b6ac653bd28d54314a9e022bbc1a99",
    );
    expect(install).toContain("Get-FileHash");
    expect(install).toContain("Expand-Archive");
    expect(install).toContain("$env:GITHUB_PATH");
    expect(value).toContain("if: always()");
    expect(value).not.toMatch(/\b(?:choco|winget)\b/);
  });
});
