import { describe, expect, test } from "bun:test";
import { createOperationAdapter } from "./task19-operation-adapter.mjs";

const serverPlan = {
  role: "server",
  packagePath: "candidate.deb",
  installCommand: [
    "wsl.exe",
    "--exec",
    "sudo",
    "dpkg",
    "-i",
    "candidate.deb",
  ],
  packageArgumentIndex: 5,
  launchCommand: [
    "wsl.exe",
    "--exec",
    "systemctl",
    "start",
    "jastreamer-server.service",
  ],
};

describe("Task19 operation adapter lifecycle", () => {
  test("retains the Server through probes and performance until terminate", async () => {
    const stops = [];
    const closedRuns = [];
    const provisionScenario = Object.assign(
      async () => {
        throw new Error("TASK19_UNEXPECTED_SCENARIO_PROVISION");
      },
      {
        closeRun: async (runId) => {
          closedRuns.push(runId);
        },
      },
    );
    const adapter = createOperationAdapter({
      execute: async (command) => ({
        exitCode: 0,
        stdout: command[1] === "get-serialno" ? "authorized-serial\n" : "",
        stderr: "",
      }),
      launch: async (_command, owner) => ({ pid: 101, owner }),
      terminate: async (process) => {
        stops.push(process.pid);
        return { exitCode: 0 };
      },
      serverCertificateFingerprint: async () => "a".repeat(64),
      provision: async () => ({
        origin: "https://127.0.0.1:8443",
        fingerprint: "a".repeat(64),
        adminToken: "admin-token",
        controllerToken: "controller-token",
        rendererId: "renderer-live",
      }),
      tlsIdentity: async ({ root }) => ({
        root,
        certificateSha256: "b".repeat(64),
        spkiSha256: "c".repeat(64),
        spkiPinBase64: "spki-pin",
        cleanup: async () => {},
      }),
      captureProxy: async () => ({
        url: "https://localhost:9443",
        host: "localhost",
        port: 9443,
        next: async () => ({}),
        nextEvent: async () => ({}),
        dropNextEvent: () => {},
        restoreEvents: () => {},
        rotate: async () => {},
        reset: async () => {},
        close: async () => {},
      }),
      provisionScenario,
      probe: async ({ id, runId }) => ({ id, runId, exitCode: 1 }),
      performance: async ({ runIds }) => ({ runIds, qualified: true }),
    });
    const runId = "task19-retained-server";
    const server = await adapter.execute("start", {
      runId,
      role: "server",
      process: serverPlan,
      packageSha256: "a".repeat(64),
    });

    await adapter.execute("probe", { id: "negative-probe", runId });
    await adapter.execute("performance", { runIds: [runId] });
    expect(stops).toEqual([]);

    await adapter.execute("terminate", {
      id: server.id,
      pid: server.pid,
      role: server.role,
    });
    expect(stops).toEqual([101]);
    expect(closedRuns).toEqual([runId]);
  });
});
