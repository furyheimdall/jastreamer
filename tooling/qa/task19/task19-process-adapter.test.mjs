import { describe, expect, test } from "bun:test";
import { EventEmitter } from "node:events";
import { activateAndroid, createProcessAdapter, createTask19ApiRequest, observeInstalledServerFingerprint, provisionRuntime } from "./task19-process-adapter.mjs";

const serverPlan = { role: "server", packagePath: "candidate.deb", installCommand: ["wsl.exe", "--exec", "sudo", "dpkg", "-i", "candidate.deb"], packageArgumentIndex: 5, launchCommand: ["wsl.exe", "--exec", "systemctl", "start", "jastreamer-server.service"] };
const windowsPlan = { role: "control-windows", packagePath: "candidate.msix", installCommand: ["Add-AppxPackage", "candidate.msix"], packageArgumentIndex: 1, launchCommand: ["jastreamer-control"] };
const androidPlan = { role: "control-android", packagePath: "candidate.apk", installCommand: ["adb", "install", "candidate.apk"], packageArgumentIndex: 2, launchCommand: ["adb", "shell", "am", "start"] };
const response = { request: { method: "GET", route: "/api/v1/discovery", headers: { authenticationScheme: "bearer" }, body: null }, response: { status: 200, body: { pairing_url: "https://server/pair/" } } };
const fixture = ({ activations = [], capture = async () => response, androidActivations = [], executePatch, tlsIdentity } = {}) => {
  const calls = []; let launched = 100; const run = async (command) => { calls.push(command); if (executePatch) { const patched = await executePatch(command); if (patched !== undefined) return patched; } return { exitCode: 0, stdout: command[1] === "get-serialno" ? "authorized-serial\n" : "", stderr: "" }; }; const stop = async (process) => { calls.push(["stop-owned", process.pid]); return { exitCode: 0 }; }; const adapter = createProcessAdapter({ execute: run, launch: async (_command, owner) => ({ pid: ++launched, owner }), terminate: stop, windowsActivation: async () => { const value = activations.shift(); if (value instanceof Error) throw value; return value; }, androidActivation: async (serial) => { calls.push(["android-activate", serial]); const value = androidActivations.shift(); if (value instanceof Error) throw value; return value; }, serverCertificateFingerprint: async () => "f".repeat(64), provision: async () => ({ origin: "https://127.0.0.1:8443", token: "controller", controllerId: "controller", adminToken: "admin", fingerprint: "f".repeat(64), rendererToken: "renderer", rendererId: "renderer", deviceSerial: "authorized-serial", catalogRoot: "/catalog" }), tlsIdentity: tlsIdentity ?? (async () => ({ certificateSha256: "c".repeat(64), spkiSha256: "s".repeat(64), cleanup: async () => {} })), captureProxy: async () => ({ url: "https://127.0.0.1:9555", host: "127.0.0.1", port: 9555, next: capture, nextEvent: async () => ({}), dropNextEvent: () => {}, restoreEvents: () => {}, rotate: async () => {}, reset: async () => {}, close: async () => {} }) }); return { adapter, calls };
};
const startServer = (adapter, runId) => adapter.start({ runId, role: "server", process: serverPlan, packageSha256: "a".repeat(64) });

describe("Task19 Server bootstrap identity", () => {
  test("reads the installed durable certificate fingerprint through the exact privileged boundary", async () => {
    const commands = []; const fingerprint = "f".repeat(64); const observed = await observeInstalledServerFingerprint(async (command) => { commands.push(command); return { exitCode: 0, stdout: `${fingerprint}  -\n`, stderr: "" }; });
    expect(observed).toBe(fingerprint); expect(commands).toEqual([["wsl.exe", "--exec", "sudo", "sh", "-c", "openssl x509 -in /var/lib/jastreamer/identity/tls-cert.pem -outform DER | sha256sum"]]);
  });

  test("rejects an unauthenticated peer before sending the bootstrap setup secret", async () => {
    const calls = []; const trusted = "a".repeat(64); const rogue = "b".repeat(64);
    await expect(provisionRuntime({ setupSecret: "never-send", expectedFingerprint: trusted }, async (request) => { calls.push(request); if (request.path === "/healthz") return { status: 200, body: { status: "ready" } }; if (request.path === "/api/v1/identity") return { status: 200, body: { sha256_fingerprint: rogue }, certificate: rogue }; throw new Error("BOOTSTRAP_REACHED"); })).rejects.toThrow("TASK19_SERVER_CERTIFICATE_BINDING_FAILED");
    expect(calls.map(({ path }) => path)).toEqual(["/healthz", "/api/v1/identity"]);
  });

  test("does not send credentials before the exact TLS peer is pinned", async () => {
    const socket = new EventEmitter();
    socket.getPeerCertificate = () => ({ fingerprint256: "BB:BB" });
    const request = new EventEmitter();
    let sent;
    let destroyed;
    request.setTimeout = () => {};
    request.end = (body) => { sent = body; };
    request.destroy = (error) => {
      destroyed = error;
      request.emit("error", error);
    };
    const apiRequest = createTask19ApiRequest((_options, _response) => request);
    const result = apiRequest({
      method: "POST",
      path: "/api/v1/bootstrap",
      token: "live-admin",
      body: { setup_secret: "live-setup" },
      expectedFingerprint: "a".repeat(64),
    });
    request.emit("socket", socket);
    socket.emit("secureConnect");
    await expect(result).rejects.toThrow("TASK19_SERVER_CERTIFICATE_BINDING_FAILED");
    expect(sent).toBeUndefined();
    expect(destroyed).toBeInstanceOf(Error);
  });
});

describe("Task19 cleanup retry ownership", () => {
  test("retains process and session ownership until TLS cleanup succeeds", async () => {
    let cleanupAttempts = 0;
    const value = fixture({
      tlsIdentity: async () => ({
        certificateSha256: "c".repeat(64),
        spkiSha256: "s".repeat(64),
        cleanup: async () => {
          cleanupAttempts += 1;
          if (cleanupAttempts === 1) throw new Error("transient cleanup");
        },
      }),
    });
    const server = await startServer(value.adapter, "task19-cleanup-retry");

    await expect(
      value.adapter.terminate({
        id: server.id,
        pid: server.pid,
        role: server.role,
      }),
    ).rejects.toThrow("TASK19_PROCESS_TERMINATION_FAILED");
    expect(value.adapter.resources()).toHaveLength(1);

    await value.adapter.terminate({
      id: server.id,
      pid: server.pid,
      role: server.role,
    });
    expect(value.adapter.resources()).toEqual([]);
    expect(cleanupAttempts).toBeGreaterThanOrEqual(3);
  });
});

 describe("Task19 exact Control restart ownership", () => {
  test("replaces the exact Windows app PID/window and cleans the replacement handle", async () => {
    const runId = "task19-windows-server-first"; let releaseDiscovery; const value = fixture({ activations: [{ pid: 200, mainWindowHandle: 300 }, { pid: 201, mainWindowHandle: 301 }], capture: () => new Promise((resolve) => { releaseDiscovery = resolve; }) }); const server = await startServer(value.adapter, runId); const control = await value.adapter.start({ runId, role: "control-windows", process: windowsPlan, packageSha256: "b".repeat(64) }); const runSession = {};
    const observed = await value.adapter.restartControl({ runId, platform: "windows", expectedRequest: { method: "GET", route: "/api/v1/discovery" }, restore: async ({ pid }) => { expect(pid).toBe(201); releaseDiscovery(response); }, runSession });
    expect(observed.lifecycle).toMatchObject({ oldPid: 200, newPid: 201, processId: control.id }); expect(runSession).toMatchObject({ nativeProcessId: 201, mainWindowHandle: 301 }); expect(value.adapter.resources().find(({ id }) => id === control.id)?.pid).toBe(201); expect(value.calls).toContainEqual(["stop-owned", 200]);
    await value.adapter.terminate({ id: control.id, pid: control.pid, role: control.role }); await value.adapter.terminate({ id: server.id, pid: server.pid, role: server.role }); expect(value.calls).toContainEqual(["stop-owned", 201]);
  });

  test("rejects stale or zero-window Windows replacement and still owns failure cleanup", async () => {
    for (const replacement of [{ pid: 200, mainWindowHandle: 301 }, { pid: 201, mainWindowHandle: 0 }]) { const runId = "task19-windows-server-first"; const value = fixture({ activations: [{ pid: 200, mainWindowHandle: 300 }, replacement] }); const server = await startServer(value.adapter, runId); const control = await value.adapter.start({ runId, role: "control-windows", process: windowsPlan, packageSha256: "b".repeat(64) }); await expect(value.adapter.restartControl({ runId, platform: "windows", expectedRequest: { method: "GET", route: "/api/v1/discovery" }, restore: async () => {}, runSession: {} })).rejects.toThrow("TASK19_WINDOWS_RESTART_IDENTITY_INVALID"); expect(value.calls).toContainEqual(["stop-owned", replacement.pid]); await value.adapter.terminate({ id: control.id, pid: control.pid, role: control.role }); await value.adapter.terminate({ id: server.id, pid: server.pid, role: server.role }); expect(value.adapter.resources()).toEqual([]); }
  });

  test("uses only the authorized Android serial/package/activity and rebinds the replacement PID", async () => {
    const runId = "task19-android-server-first"; const value = fixture({ androidActivations: [{ pid: 400, deviceSerial: "authorized-serial", component: "io.jastreamer.control/.MainActivity" }, { pid: 401, deviceSerial: "authorized-serial", component: "io.jastreamer.control/.MainActivity" }] }); const server = await startServer(value.adapter, runId); const control = await value.adapter.start({ runId, role: "control-android", process: androidPlan, packageSha256: "b".repeat(64) }); const runSession = {}; const observed = await value.adapter.restartControl({ runId, platform: "android", expectedRequest: { method: "GET", route: "/api/v1/discovery" }, restore: async () => {}, runSession }); expect(observed.lifecycle).toMatchObject({ oldPid: 400, newPid: 401 }); expect(runSession.nativeProcessId).toBe(401); expect(value.calls).toContainEqual(["adb.exe", "-s", "authorized-serial", "shell", "am", "force-stop", "io.jastreamer.control"]); await value.adapter.terminate({ id: control.id, pid: control.pid, role: control.role }); await value.adapter.terminate({ id: server.id, pid: server.pid, role: server.role });
  });

  test("rejects wrong Android package/activity readiness and a missing discovery capture", async () => {
    await expect(activateAndroid(async () => ({ exitCode: 0, stdout: "" }), "wrong serial")).rejects.toThrow("TASK19_ANDROID_DEVICE_SERIAL_UNAVAILABLE"); const commands = []; await expect(activateAndroid(async (command) => { commands.push(command); if (command.includes("pidof")) return { exitCode: 0, stdout: "444\n" }; if (command.includes("dumpsys")) return { exitCode: 0, stdout: "mCurrentFocus=evil.package/.MainActivity" }; return { exitCode: 0, stdout: "" }; }, "authorized-serial")).rejects.toThrow("TASK19_ANDROID_ACTIVITY_NOT_FOREGROUND"); expect(commands.every((command) => command[1] === "-s" && command[2] === "authorized-serial")).toBe(true); expect(commands[0]).toContain("io.jastreamer.control/.MainActivity"); expect(commands.find((command) => command.includes("pidof"))).toContain("io.jastreamer.control");
    const runId = "task19-windows-server-first"; const value = fixture({ activations: [{ pid: 200, mainWindowHandle: 300 }, { pid: 201, mainWindowHandle: 301 }], capture: async () => { throw new Error("TASK19_NATIVE_CAPTURE_REQUIRED_DISCOVERY"); } }); const server = await startServer(value.adapter, runId); const control = await value.adapter.start({ runId, role: "control-windows", process: windowsPlan, packageSha256: "b".repeat(64) }); await expect(value.adapter.restartControl({ runId, platform: "windows", expectedRequest: { method: "GET", route: "/api/v1/discovery" }, restore: async () => {}, runSession: {} })).rejects.toThrow("TASK19_NATIVE_CAPTURE_REQUIRED_DISCOVERY"); expect(value.adapter.resources().find(({ id }) => id === control.id)?.pid).toBe(201); await value.adapter.terminate({ id: control.id, pid: control.pid, role: control.role }); await value.adapter.terminate({ id: server.id, pid: server.pid, role: server.role }); expect(value.adapter.resources()).toEqual([]);
  });
});
