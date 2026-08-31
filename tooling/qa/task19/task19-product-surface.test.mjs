import { describe, expect, test } from "bun:test";
import { readFile } from "node:fs/promises";
import { join } from "node:path";

const repository = join(import.meta.dirname, "../../..");
const source = (path) => readFile(join(repository, path), "utf8");

describe("Task19 production product-surface cross references", () => {
  test("uses only routes mounted by the Server product", async () => {
    // Given: the production adapter and Server route table.
    const [adapter, contract, api] = await Promise.all([source("tooling/qa/task19/task19-operation-adapter.mjs"), source("tooling/qa/task19/scenario-contract.mjs"), source("apps/server/internal/api/api.go")]);
    // When: every contract and adapter API path is inventoried.
    const routes = [...`${adapter}\n${contract}`.matchAll(/"(\/api\/v1\/[^"?`]*)/g)].map((match) => match[1]);
    // Then: every route is product-mounted and no QA namespace exists.
    expect(routes.length).toBeGreaterThan(0); expect(routes.every((route) => api.includes(route.replace(/\/:zoneId\//, "/{zoneID}/").replace(/\/:zoneId$/, "/{zoneID}").replace(/\/:controllerId$/, "/{deviceID}")) || route === "/api/v1/events")).toBe(true); expect(adapter).not.toContain("/api/v1/qa/");
  });

  test("pins Flutter semantics and native activation identities from product contracts", async () => {
    // Given: adapter operations and shipped Control contracts.
    const [adapter, discovery, transport, android, msix] = await Promise.all([source("tooling/qa/task19/task19-operation-adapter.mjs"), source("apps/control/lib/discovery_panel.dart"), source("apps/control/lib/control_workflow_transport.dart"), source("apps/control/android/app/src/main/AndroidManifest.xml"), source("packaging/control/release.sh")]);
    // When: representative platform selectors and identities are selected.
    // Then: they are present in the shipped Flutter and package contracts.
    expect(discovery).toContain("Discover Server"); expect(transport).toContain("label: 'Play'"); expect(android).toContain('android:name=".MainActivity"'); expect(msix).toContain('<Applications><Application Id="Control"'); expect(adapter).toContain("io.jastreamer.control/.MainActivity"); expect(adapter).toContain("io.jastreamer.control!Control");
    expect(adapter).toContain("AutomationElement]::FromHandle"); expect(adapter).not.toContain("AutomationElement]::RootElement"); expect(adapter).toContain('["adb.exe", "-s", credentials.deviceSerial]'); expect(adapter).toContain("TASK19_ANDROID_ACTIVITY_NOT_FOREGROUND");
  });

  test("keeps normal installs out of the protected-runner setup phase", async () => {
    // Given: the protected runner's setup and fail-safe cleanup phases.
    const runner = await source("tooling/qa/task19/protected-runner.ps1");
    // When: normal setup is separated from the outer cleanup boundary.
    const setup = runner.slice(runner.indexOf("Write-Output '[TASK19_PHASE]setup'"), runner.indexOf("Write-Output '[TASK19_PHASE]driver'")); const cleanup = runner.slice(runner.indexOf("Write-Output '[TASK19_PHASE]cleanup'"));
    // Then: setup executes no installer and cleanup uninstalls only observed residue.
    for (const command of ["Add-AppxPackage", "@('install'", "@('/i'", "dpkg', '-i'"]) expect(setup).not.toContain(command);
    expect(cleanup).toContain("if (Test-MsiProductCodeInstalled $rendererCode)"); expect(cleanup).toContain("elseif ($androidRemaining.Count -ne 0)");
  });

  test("launches the installed Renderer through its real CLI and WASAPI product", async () => {
    // Given: Task19 launch contract and Renderer CLI implementation.
    const [adapter, processAdapter, cli, main] = await Promise.all([source("tooling/qa/task19/task19-operation-adapter.mjs"), source("tooling/qa/task19/task19-process-adapter.mjs"), source("apps/renderer/src/cli.rs"), source("apps/renderer/src/main.rs")]);
    // When: required CLI arguments and observation routes are compared.
    // Then: Task19 references the installed executable, all required arguments, and Server events without a QA protocol.
    for (const argument of ["--server-origin", "--server-fingerprint", "--renderer-id", "--output-device", "--share-mode", "--state-directory", "--token-stdin"]) { expect(cli).toContain(argument.slice(2).replaceAll("-", "_")); expect(processAdapter).toContain(argument); }
    expect(processAdapter).toContain("jastreamer-renderer.exe"); expect(main).toContain("WasapiBackend::new"); expect(adapter).toContain("/api/v1/event-tickets"); expect(adapter).toContain("/api/v1/events?ticket=");
  });
});
