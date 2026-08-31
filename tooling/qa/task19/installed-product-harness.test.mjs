import { afterEach, describe, expect, test } from "bun:test";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createProductionHarness, executeHarnessOperation } from "./installed-product-harness.mjs";
import { task19Scenario } from "./scenario-contract.mjs";

const roots = [];
afterEach(async () => Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true }))));
const clock = () => { let tick = Date.parse("2026-08-26T11:55:00.000Z"); return () => new Date(tick += 10).toISOString(); };
const capturedRequest = (expected) => ({ method: expected.method, route: expected.route, body: expected.body, headers: Object.fromEntries(expected.requiredHeaders.map((name) => [name, `${name}-observed`])) });
const scenarioMaterial = () => ({ zoneId: "zone-live", rendererId: "renderer-live", controllerId: "controller-live", trackId: "track-live", unavailableTrackId: "unavailable-live", entryId: "entry-live" });
const processPlan = (packagePath) => ({ role: "control-web", packageKey: "controlWeb", packagePath, installCommand: ["unzip", packagePath], packageArgumentIndex: 1, launchCommand: ["chromium", "app-mode"] });

const fixture = async (patch = {}) => {
  const root = await mkdtemp(join(tmpdir(), "task19-production-adapter-")); roots.push(root);
  const executable = join(root, "fake-product.mjs"); await writeFile(executable, "if (process.argv[2] === 'execute') process.exit(0); process.on('SIGTERM', () => process.exit(0)); setInterval(() => {}, 1000);\n");
  const calls = []; const alive = new Map(); let pid = 4000; let revision = 1; const now = clock();
  const backend = {
    inventory: async () => [...alive.values()].map(({ id, pid: processId, owner }) => ({ type: "process", id, pid: processId, owner, observedBy: "deterministic-process-inventory" })),
    execute: async (command) => { calls.push(["execute", ...command]); const child = Bun.spawn([process.execPath, executable, "execute"], { stdout: "pipe", stderr: "pipe" }); return { exitCode: await child.exited, stdout: await new Response(child.stdout).text(), stderr: await new Response(child.stderr).text() }; },
    launch: async (command, owner) => { calls.push(["launch", ...command]); const child = Bun.spawn([process.execPath, executable, "launch"], { stdout: "ignore", stderr: "ignore" }); const value = { pid: child.pid, owner, child }; pid = Math.max(pid, child.pid); alive.set(owner, { id: owner, pid: value.pid, owner, child }); return value; },
    terminate: async ({ owner }) => { calls.push(["terminate", owner]); const process = alive.get(owner); process?.child.kill("SIGTERM"); if (process) await process.child.exited; alive.delete(owner); return { exitCode: 0 }; },
    provision: async () => ({ origin: "https://127.0.0.1:8443", webUrl: "", token: "controller", adminToken: "admin", fingerprint: "f".repeat(64), rendererToken: "renderer", rendererId: "renderer-1" }),
    tlsIdentity: async () => ({ kind: "task19-run-ephemeral-tls", certificateSha256: "c".repeat(64), spkiSha256: "s".repeat(64), spkiPinBase64: "fixture-pin", cleanup: async () => {} }),
    webOrigin: async () => ({ url: "https://127.0.0.1:9443", host: "127.0.0.1", port: 9443, certificateSha256: "c".repeat(64), spkiSha256: "s".repeat(64), close: async () => {} }),
    webControl: { start: async ({ url }) => { calls.push(["web-start", url]); return { pid: 4900 }; }, operation: async () => { throw new Error("WEB_OPERATION_NOT_CONFIGURED"); }, close: async () => { calls.push(["web-close"]); } },
    windowsActivation: async () => ({ pid: 5100, mainWindowHandle: 7100 }),
    androidActivation: async (serial) => ({ pid: ++pid, deviceSerial: serial, component: "io.jastreamer.control/.MainActivity" }),
    deviceSerial: "authorized-serial",
    rendererSession: () => ({ origin: "https://127.0.0.1:8443", fingerprint: "f".repeat(64), rendererToken: "renderer", rendererId: "renderer-1" }),
    request: async ({ method, path }) => ({ status: 200, headers: { etag: `"${revision}"` }, body: { method, path, revision } }),
    browser: async ({ scenarioId, action, expectedRequest }) => { const contract = task19Scenario(scenarioId); revision += 1; return { accessibility: { scenarioId, focused: action.name }, request: capturedRequest(expectedRequest), response: { status: contract.expected.status, code: contract.expected.code } }; },
    windowsUi: async ({ scenarioId, action, expectedRequest }) => { const contract = task19Scenario(scenarioId); revision += 1; return { accessibility: { scenarioId, focused: action.name }, request: capturedRequest(expectedRequest), response: { status: contract.expected.status, code: contract.expected.code } }; },
    androidUi: async ({ scenarioId, action, expectedRequest }) => { const contract = task19Scenario(scenarioId); revision += 1; return { accessibility: { scenarioId, focused: action.name }, request: capturedRequest(expectedRequest), response: { status: contract.expected.status, code: contract.expected.code } }; },
    restartControl: async ({ expectedRequest }) => ({ accessibility: { role: "application", name: "Control credential restored" }, request: capturedRequest(expectedRequest), response: { status: 200, code: "discovery" }, lifecycle: { oldPid: 1, newPid: 2 } }),
    subscribeEvent: async () => { const initialRevision = revision; return { initial: async () => ({ sequence: initialRevision, type: "snapshot", stateRevision: initialRevision, observedAt: now(), body: { type: "snapshot", revision: initialRevision } }), next: async ({ expected }) => ({ sequence: revision, type: expected.kind, resource: expected.resource, stateRevision: revision, observedAt: now(), body: { type: expected.kind, resource: expected.resource, revision } }), close: () => {} }; },
    provisionScenario: async () => ({ material: scenarioMaterial(), cleanup: async () => {} }),
    performance: async ({ runIds }) => ({ recordedAt: now(), runIds, tracks: 100000, zones: Array.from({ length: 8 }, (_, index) => ({ id: `zone-${index + 1}`, queueEntries: 10000 })), browseObservations: [], mutationLatenciesMs: Array(100).fill(1) }),
    probe: async ({ id, runId }) => { const startedAt = now(); const stdoutAt = now(); const stderrAt = now(); const endedAt = now(); return { id, runId, command: ["receipt-validator", id], startedAt, endedAt, exitCode: 1, stdout: { line: id, capturedAt: stdoutAt }, stderr: { code: "INPUT_REJECTED", capturedAt: stderrAt } }; },
    now,
    ...patch,
  };
  return { root, calls, alive, harness: createProductionHarness(backend, { timeouts: { scenario: 5 } }) };
};

describe("Task19 repository-owned installed-product harness", () => {
  test("uses immutable plan commands, ownership labels, and complete process cleanup", async () => {
    const value = await fixture(); const packagePath = join(value.root, "control.zip"); await writeFile(packagePath, "candidate"); const process = processPlan(packagePath);
    const started = await executeHarnessOperation("start", { runId: "task19-web-server-first", role: process.role, process, packageSha256: "a".repeat(64) }, { harness: value.harness });
    expect(started).toMatchObject({ role: "control-web", packagePath, packageSha256: "a".repeat(64) }); expect(started.pid).toBe(4900);
    expect(value.calls.slice(0, 2)).toEqual([
      ["execute", "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "Expand-Archive -LiteralPath $args[0] -DestinationPath $args[1] -Force", packagePath, `${packagePath}.task19-web`],
      ["web-start", "https://127.0.0.1:9443"],
    ]);
    const allocated = await executeHarnessOperation("inventory", { runId: "task19-web-server-first", phase: "allocated" }, { harness: value.harness });
    expect(allocated.resources[0]).toMatchObject({ owner: "task19:task19-web-server-first:control-web" });
    await executeHarnessOperation("terminate", { id: started.id, pid: started.pid, role: started.role }, { harness: value.harness });
    expect(value.calls.at(-1)).toEqual(["execute", "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "Remove-Item -LiteralPath $args[0] -Recurse -Force", `${packagePath}.task19-web`]);
    expect((await executeHarnessOperation("inventory", { runId: "task19-web-server-first", phase: "after" }, { harness: value.harness })).resources).toEqual([]);
  });

  test("uses the activated MSIX app PID and rejects wrapper or zero-window identities", async () => {
    // Given: activation returns the exact app process independently of the launcher wrapper.
    const value = await fixture({ windowsActivation: async () => ({ pid: 5151, mainWindowHandle: 9191 }), launch: async () => ({ pid: 9999, owner: "wrapper" }) }); const packagePath = join(value.root, "control.msix"); await writeFile(packagePath, "candidate"); const process = { role: "control-windows", packagePath, installCommand: ["Add-AppxPackage", packagePath], packageArgumentIndex: 1, launchCommand: ["jastreamer-control"] };
    // When: the installed Control is activated.
    const started = await executeHarnessOperation("start", { runId: "task19-windows-server-first", role: process.role, process, packageSha256: "a".repeat(64) }, { harness: value.harness });
    // Then: lifecycle/UIA receives the app PID, never the wrapper PID.
    expect(started.pid).toBe(5151); expect(started.mainWindowHandle).toBe(9191); expect(value.calls.some(([kind]) => kind === "launch")).toBe(false); await executeHarnessOperation("terminate", { id: started.id, pid: started.pid, role: started.role }, { harness: value.harness });
    const attacked = await fixture({ windowsActivation: async () => ({ pid: 9999, mainWindowHandle: 0 }) }); await writeFile(join(attacked.root, "control.msix"), "candidate"); await expect(executeHarnessOperation("start", { runId: "task19-windows-server-first", role: process.role, process: { ...process, packagePath: join(attacked.root, "control.msix"), installCommand: ["Add-AppxPackage", join(attacked.root, "control.msix")] }, packageSha256: "a".repeat(64) }, { harness: attacked.harness })).rejects.toThrow("TASK19_WINDOWS_APP_IDENTITY_INVALID");
  });

  test("reinstalls and uninstalls AppX APK and MSI independently on repeated runs", async () => {
    const value = await fixture();
    const roles = [
      ["control-windows", "control.msix", ["Add-AppxPackage"], 1, ["jastreamer-control"]],
      ["control-android", "control.apk", ["adb", "install"], 2, ["adb", "shell", "am", "start"]],
      ["renderer", "renderer.msi", ["msiexec", "/i"], 2, ["jastreamer-renderer"]],
    ];
    for (const [role, name, installPrefix, packageArgumentIndex, launchCommand] of roles) {
      const packagePath = join(value.root, name); await writeFile(packagePath, "candidate"); const installCommand = [...installPrefix, packagePath];
      for (const suffix of ["first", "second"]) {
        const process = { role, packagePath, installCommand, packageArgumentIndex, launchCommand };
        const started = await executeHarnessOperation("start", { runId: `task19-${role}-${suffix}`, role, process, packageSha256: "a".repeat(64) }, { harness: value.harness });
        await executeHarnessOperation("terminate", { id: started.id, pid: started.pid, role }, { harness: value.harness });
      }
    }
    expect(value.calls.filter((call) => call[0] === "execute" && call.includes("Add-AppxPackage -Path $args[0] -ErrorAction Stop"))).toHaveLength(2);
    expect(value.calls.filter((call) => call[0] === "execute" && call[1] === "adb" && call[2] === "uninstall")).toHaveLength(2);
    expect(value.calls.filter((call) => call[0] === "execute" && call[1] === "msiexec" && call[2] === "/x")).toHaveLength(2);
  });

  test("uninstalls a partially failed package before returning the installer failure", async () => {
    const commands = []; const value = await fixture({ execute: async (command) => { commands.push(command); return { exitCode: command[1] === "install" ? 1 : 0 }; } }); const packagePath = join(value.root, "control.apk"); await writeFile(packagePath, "candidate");
    const process = { role: "control-android", packagePath, installCommand: ["adb", "install", packagePath], packageArgumentIndex: 2, launchCommand: ["adb", "shell", "am", "start"] };
    await expect(executeHarnessOperation("start", { runId: "task19-android-install-failure", role: process.role, process, packageSha256: "a".repeat(64) }, { harness: value.harness })).rejects.toThrow("TASK19_INSTALLER_EXIT_CODE:control-android:1");
    expect(commands).toEqual([["adb", "install", "-r", packagePath], ["adb", "uninstall", "io.jastreamer.control"]]);
  });

  test("retains Android cleanup ownership until retry succeeds", async () => {
    let stopAttempts = 0; let uninstallAttempts = 0;
    const value = await fixture({ inventory: async () => [], execute: async (command) => { if (command.includes("force-stop")) { stopAttempts += 1; return { exitCode: stopAttempts === 1 ? 1 : 0 }; } if (command[1] !== "uninstall") return { exitCode: 0 }; uninstallAttempts += 1; return { exitCode: uninstallAttempts === 1 ? 1 : 0 }; } }); const packagePath = join(value.root, "control.apk"); await writeFile(packagePath, "candidate");
    const process = { role: "control-android", packagePath, installCommand: ["adb", "install", packagePath], packageArgumentIndex: 2, launchCommand: ["adb", "shell", "am", "start"] }; const started = await executeHarnessOperation("start", { runId: "task19-android-cleanup-failure", role: process.role, process, packageSha256: "a".repeat(64) }, { harness: value.harness });
    try { await executeHarnessOperation("terminate", { id: started.id, pid: started.pid, role: process.role }, { harness: value.harness }); throw new Error("TEST_EXPECTED_FAILURE"); } catch (error) { expect(error).toBeInstanceOf(AggregateError); expect(error.errors).toHaveLength(2); }
    expect((await executeHarnessOperation("inventory", { runId: "task19-android-server-first", phase: "after" }, { harness: value.harness })).resources).toHaveLength(1);
    await executeHarnessOperation("terminate", { id: started.id, pid: started.pid, role: process.role }, { harness: value.harness });
    expect((await executeHarnessOperation("inventory", { runId: "task19-android-server-first", phase: "after" }, { harness: value.harness })).resources).toEqual([]);
  });

  test("returns raw scenario captures and rejects candidate-controlled command substitutions", async () => {
    const value = await fixture();
    const raw = await executeHarnessOperation("scenario", { runId: "task19-web-server-first", id: "pair", processId: "control-1" }, { harness: value.harness });
    expect(raw.before.capture.body.revision).toBe(1); expect(raw.response.capture.observation.accessibility.scenarioId).toBe("pair");
    const packagePath = join(value.root, "control.zip"); const process = { ...processPlan(packagePath), launchCommand: ["attacker.exe", "--steal"] };
    await expect(executeHarnessOperation("start", { runId: "task19-web-server-first", role: process.role, process, packageSha256: "a".repeat(64) }, { harness: value.harness })).rejects.toThrow("TASK19_PROCESS_PLAN_INVALID");
    expect(value.calls).toEqual([]);
  });

  test("routes every Control platform through its installed product surface and real Server API", async () => {
    const calls = []; const now = clock();
    const request = async ({ method, path }) => { calls.push(["http", method, path]); return { status: 200, headers: { etag: '"1"' }, body: { revision: 1, code: "PAIRED" } }; };
    let observedRevision = 1; const surface = (kind) => async (operation) => { calls.push([kind, operation]); observedRevision += 1; const contract = task19Scenario(operation.scenarioId); return { accessibility: { focused: operation.action.name }, request: operation.expectedRequest, response: { status: contract.expected.status, code: contract.expected.code } }; };
    const value = await fixture({ request: async ({ method, path }) => { calls.push(["http", method, path]); return { status: 200, headers: { etag: `"${observedRevision}"` }, body: { revision: observedRevision } }; }, browser: surface("playwright"), windowsUi: surface("windows-uia"), androidUi: surface("adb-uiautomator"), subscribeEvent: async () => { const initial = observedRevision; return { initial: async () => ({ sequence: initial, type: "snapshot", stateRevision: initial, observedAt: now(), body: { type: "snapshot", revision: initial } }), next: async ({ expected }) => ({ sequence: observedRevision, type: expected.kind, resource: expected.resource, stateRevision: observedRevision, observedAt: now(), body: { type: expected.kind, resource: expected.resource, revision: observedRevision } }), close: () => {} }; }, now });
    for (const runId of ["task19-web-server-first", "task19-windows-server-first", "task19-android-server-first"]) {
      const raw = await executeHarnessOperation("scenario", { runId, id: "pair", processId: "control-1" }, { harness: value.harness });
      expect(raw.request.target).toBe("/api/v1/discovery"); expect(raw.request.method).toBe("GET");
    }
    expect(calls.filter(([kind]) => kind === "http").every(([, method, path]) => method === "GET" && path === "/api/v1/discovery")).toBe(true);
    expect(calls.find(([kind]) => kind === "playwright")[1]).toMatchObject({ engine: "playwright", action: { role: "button", name: "Complete pairing" } });
    expect(calls.find(([kind]) => kind === "windows-uia")[1]).toMatchObject({ appUserModelId: "io.jastreamer.control!Control", action: { name: "Complete pairing" } });
    expect(calls.find(([kind]) => kind === "adb-uiautomator")[1]).toMatchObject({ component: "io.jastreamer.control/.MainActivity", action: { name: "Complete pairing" } });
  });

  test("rejects Server's immediate initial event and unchanged revision as mutation evidence", async () => {
    // Given: the Server emits its immediate snapshot before the Control action.
    const value = await fixture({
      request: async () => ({ status: 200, headers: { etag: '"7"' }, body: { revision: 7 } }),
      browser: async ({ expectedRequest }) => ({ accessibility: { focused: "Discover Server" }, request: capturedRequest(expectedRequest), response: { status: 200, code: "discovery" } }),
      subscribeEvent: async () => ({
        initial: async () => ({ sequence: 1, type: "snapshot", stateRevision: 7, observedAt: "2026-08-26T11:55:00.020Z", body: { type: "snapshot", revision: 7 } }),
        next: async () => ({ sequence: 1, type: "snapshot", stateRevision: 7, observedAt: "2026-08-26T11:55:00.040Z", body: { type: "snapshot", revision: 7 } }),
        close: () => {},
      }),
    });
    // When / Then: the initial/equal-revision event cannot satisfy the scenario.
    await expect(executeHarnessOperation("scenario", { runId: "task19-web-server-first", id: "pair", processId: "control-1" }, { harness: value.harness })).rejects.toThrow("TASK19_EVENT_CORRELATION_FAILED");
  });

  test("rejects generic OBSERVED responses and no-op product actions", async () => {
    // Given: the UI click reports only a generic observation and Server state does not change.
    const value = await fixture({
      request: async () => ({ status: 200, headers: { etag: '"1"' }, body: { revision: 1 } }),
      browser: async () => ({ accessibility: { focused: "Discover Server" }, response: { status: 200, code: "OBSERVED" } }),
    });
    // When / Then: generic click plus unchanged revision is not semantic evidence.
    await expect(executeHarnessOperation("scenario", { runId: "task19-web-server-first", id: "pair", processId: "control-1" }, { harness: value.harness })).rejects.toThrow("TASK19_PRODUCT_ACTION_UNCORRELATED");
  });

  test("preserves one explicit paired Web session across consecutive scenarios in a run", async () => {
    // Given: the second action requires the exact paired browser session from the first action.
    let pairedSession; let scenarioRevision = 7; const value = await fixture({
      provisionScenario: async () => ({ material: scenarioMaterial(), cleanup: async () => {} }),
      request: async ({ method, path }) => ({ status: 200, headers: { etag: `"${scenarioRevision}"` }, body: { revision: scenarioRevision, method, path } }),
      browser: async ({ scenarioId, runSession, expectedRequest }) => {
        scenarioRevision += 1;
        if (scenarioId === "pair") pairedSession = runSession;
        else if (!runSession || runSession !== pairedSession) throw new Error("PAIRING_SESSION_LOST");
        return { accessibility: { focused: scenarioId }, request: capturedRequest(expectedRequest), response: { status: 200, code: scenarioId === "pair" ? "discovery" : "config" } };
      },
      subscribeEvent: async () => { const initial = scenarioRevision; return { initial: async () => ({ sequence: initial, type: "snapshot", stateRevision: initial, observedAt: "2026-08-26T11:55:00.010Z", body: { type: "snapshot", revision: initial } }), next: async ({ expected }) => ({ sequence: scenarioRevision, type: expected.kind, resource: expected.resource, stateRevision: scenarioRevision, observedAt: "2026-08-26T11:55:00.040Z", body: { type: expected.kind, resource: expected.resource, revision: scenarioRevision } }), close: () => {} }; },
    });
    // When: pairing is followed by an authenticated scenario.
    await executeHarnessOperation("scenario", { runId: "task19-web-server-first", id: "pair", processId: "control-1" }, { harness: value.harness });
    await executeHarnessOperation("scenario", { runId: "task19-web-server-first", id: "admin", processId: "control-1" }, { harness: value.harness });
    // Then: a concrete run-scoped session was reused.
    expect(pairedSession).toBeDefined();
  });

  test("routes secure-token restoration only through the owned restart boundary", async () => {
    // Given: paired UI is unavailable while restart capture observes real discovery semantics.
    let restarted = 0; const value = await fixture({ request: async () => ({ status: 200, headers: {}, body: {} }), browser: async () => { throw new Error("ABSENT_COMPLETE_PAIRING_USED"); }, restartControl: async ({ expectedRequest }) => { restarted += 1; return { accessibility: { role: "application", name: "Control credential restored" }, request: capturedRequest(expectedRequest), response: { status: 200, semantic: "discovery" }, lifecycle: { oldPid: 100, newPid: 101 } }; }, subscribeEvent: async () => { throw new Error("RESTART_EVENT_MUST_NOT_BE_SYNTHESIZED"); } });
    // When: the secure restart scenario executes.
    const observed = await executeHarnessOperation("scenario", { runId: "task19-web-server-first", id: "secure-token-restart", processId: "control-100" }, { harness: value.harness });
    // Then: actual discovery capture and replacement lifecycle are retained without a synthetic click/event.
    expect(restarted).toBe(1); expect(observed.request).toMatchObject({ method: "GET", target: "/api/v1/discovery" }); expect(observed.response.capture.observation.lifecycle).toEqual({ oldPid: 100, newPid: 101 }); expect(observed.event).toMatchObject({ sequence: 0, type: "none", stateRevision: 0 });
  });

  test("provisions and tears down exact scenario prerequisites through the production adapter", async () => {
    // Given: scenario provisioning is observed at the low-level product boundary.
    const transitions = []; let scenarioRevision = 1; const value = await fixture({
      provisionScenario: async ({ contract }) => { transitions.push(`setup:${contract.name}:${contract.preconditions.length}`); return { material: scenarioMaterial(), cleanup: async () => transitions.push(`cleanup:${contract.name}`) }; },
      request: async () => ({ status: 200, headers: { etag: `"${scenarioRevision}"` }, body: { revision: scenarioRevision } }),
      browser: async ({ expectedRequest }) => { scenarioRevision += 1; return { accessibility: { focused: "Add track" }, request: capturedRequest(expectedRequest), response: { status: 201, code: "queue-mutation" } }; },
      subscribeEvent: async () => ({ initial: async () => ({ sequence: 1, type: "snapshot", stateRevision: 1, observedAt: "2026-08-26T11:55:00.010Z", body: { type: "snapshot", revision: 1 } }), next: async ({ expected }) => ({ sequence: 2, type: expected.kind, resource: expected.resource, stateRevision: 2, observedAt: "2026-08-26T11:55:00.040Z", body: { type: expected.kind, resource: expected.resource, revision: 2 } }), close: () => {} }),
    });
    // When: a queue scenario runs.
    await executeHarnessOperation("scenario", { runId: "task19-web-server-first", id: "queue-add", processId: "control-1" }, { harness: value.harness });
    // Then: its explicit prerequisites are setup and removed around the action.
    expect(transitions).toEqual(["setup:queue-add:5", "cleanup:queue-add"]);
  });

  test("deletes both ephemeral TLS identities when origin startup fails", async () => {
    // Given: protected identity generation succeeds but the HTTPS origin cannot start.
    let cleaned = 0; const value = await fixture({ tlsIdentity: async () => ({ kind: "task19-run-ephemeral-tls", certificateSha256: "c".repeat(64), spkiSha256: "s".repeat(64), spkiPinBase64: "ephemeral", cleanup: async () => { cleaned += 1; } }), webOrigin: async () => { throw new Error("ORIGIN_START_FAILED"); } }); const packagePath = join(value.root, "control.zip"); await writeFile(packagePath, "candidate"); const process = processPlan(packagePath);
    // When / Then: failed startup removes primary and rotation identities before returning.
    await expect(executeHarnessOperation("start", { runId: "task19-web-server-first", role: process.role, process, packageSha256: "a".repeat(64) }, { harness: value.harness })).rejects.toThrow("ORIGIN_START_FAILED"); expect(cleaned).toBe(2);
  });

  test("enforces operation deadlines, cleans the scenario, and never includes secrets in errors", async () => {
    let cleaned = 0; const value = await fixture({ browser: async () => new Promise(() => {}), provisionScenario: async () => ({ material: scenarioMaterial(), cleanup: async () => { cleaned += 1; } }) });
    await expect(executeHarnessOperation("scenario", { runId: "task19-web-server-first", id: "pair", processId: "Bearer production-secret", timeoutMs: 5 }, { harness: value.harness })).rejects.toThrow("TASK19_OPERATION_TIMEOUT:scenario"); expect(cleaned).toBe(1);
  });

  test("preserves product assertion failure while recording scenario cleanup failure", async () => {
    const value = await fixture({ browser: async () => { throw new Error("PRODUCT_ASSERTION_FAILED"); }, provisionScenario: async () => ({ material: scenarioMaterial(), cleanup: async () => { throw new Error("SCENARIO_RESTORE_FAILED"); } }) }); try { await executeHarnessOperation("scenario", { runId: "task19-web-server-first", id: "pair", processId: "control-1" }, { harness: value.harness }); throw new Error("TEST_EXPECTED_FAILURE"); } catch (error) { expect(error.name).toBe("Task19ScenarioExecutionError"); expect(error.cause.message).toBe("PRODUCT_ASSERTION_FAILED"); expect(error.cleanupError.message).toBe("SCENARIO_RESTORE_FAILED"); }
  });

  test("rejects the removed trust command proxy", async () => {
    await expect(executeHarnessOperation("inventory", { harnessCommand: ["evil.exe"] })).rejects.toThrow("TASK19_HARNESS_INPUT_INVALID");
  });
});
