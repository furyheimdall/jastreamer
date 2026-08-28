import { describe, expect, test } from "bun:test";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { createServer } from "node:net";
import { finalizeQualification } from "./finalize-qualification.mjs";
import { stablePeerEndpoint } from "./windows-audio-server-peer.mjs";
import { PHYSICAL_SCENARIOS, runScenarioMatrix } from "./scenario-driver.mjs";

const boundedSignal = (value) => ({ signal: Promise.resolve(value), unsubscribe() {} });

describe("Windows audio native scenario driver", () => {
  test("launches every bound executable and subscribes before each mutation", async () => {
    // Given
    const calls = [];
    const runtime = {
      launchServerPeer: async () => { calls.push("launch:peer"); },
      launchRenderer: async () => { calls.push("launch:renderer"); },
      launchProbe: async () => { calls.push("launch:probe"); },
      subscribe: (scenario) => { calls.push(`subscribe:${scenario}`); return boundedSignal({ scenario, result: "passed" }); },
      mutate: async (scenario) => { calls.push(`mutate:${scenario}`); },
      cleanup: async () => { calls.push("cleanup"); },
    };

    // When
    const result = await runScenarioMatrix(runtime);

    // Then
    expect(calls.slice(0, 3)).toEqual(["launch:peer", "launch:renderer", "launch:probe"]);
    expect(result.scenarios).toEqual(PHYSICAL_SCENARIOS.map((id) => ({ id, result: "passed" })));
    for (const id of PHYSICAL_SCENARIOS) {
      expect(calls.indexOf(`subscribe:${id}`)).toBeLessThan(calls.indexOf(`mutate:${id}`));
    }
    expect(calls.at(-1)).toBe("cleanup");
  });

  test("cleans launched children when setup fails before scenario execution", async () => {
    // Given
    const calls = [];
    const runtime = {
      launchServerPeer: async () => { calls.push("launch:peer"); },
      launchRenderer: async () => { calls.push("launch:renderer"); throw new Error("RENDERER_START_FAILED"); },
      launchProbe: async () => { calls.push("launch:probe"); },
      subscribe: () => boundedSignal({}), mutate: async () => {},
      cleanup: async () => { calls.push("cleanup"); },
    };

    // When / Then
    await expect(runScenarioMatrix(runtime)).rejects.toThrow("RENDERER_START_FAILED");
    expect(calls).toEqual(["launch:peer", "launch:renderer", "cleanup"]);
  });

  test("restarts a real listener on the same reserved origin", async () => {
    // Given
    const reservation = createServer();
    await new Promise((resolve) => reservation.listen(0, "127.0.0.1", resolve));
    const address = reservation.address();
    if (typeof address === "string" || address === null) throw new Error("PORT_RESERVATION_FAILED");
    const endpoint = stablePeerEndpoint(address.port, "http");
    await new Promise((resolve, reject) => reservation.close((error) => error === undefined ? resolve() : reject(error)));
    const first = Bun.serve({ hostname: endpoint.hostname, port: endpoint.port, fetch: () => new Response("first") });
    const origin = endpoint.origin;
    expect(await fetch(origin).then((response) => response.text())).toBe("first");

    // When
    await first.stop(true);
    const restarted = Bun.serve({ hostname: endpoint.hostname, port: endpoint.port, fetch: () => new Response("restarted") });

    // Then
    try {
      expect(endpoint.origin).toBe(origin);
      expect(await fetch(origin).then((response) => response.text())).toBe("restarted");
      const [peerSource, runtimeSource] = await Promise.all([
        readFile(join(import.meta.dir, "windows-audio-server-peer.mjs"), "utf8"),
        readFile(join(import.meta.dir, "windows-audio-native-runtime.mjs"), "utf8"),
      ]);
      expect(peerSource).not.toContain("port: 0");
      expect(peerSource).toContain("port: endpoint.port");
      expect(runtimeSource).toContain("SERVER_PEER_ORIGIN_CHANGED");
    } finally { await restarted.stop(true); }
  });

  test("does not validate or write qualified output when workspace cleanup fails", async () => {
    // Given
    const calls = [];
    const failure = new Error("FORCED_CLEANUP_FAILURE");

    // When / Then
    await expect(finalizeQualification({
      evidence: { cleanup: { resources_released: true, processes_terminated: true, raw_endpoint_retained: false, external_writes: 0 } },
      binding: {}, now: "now", removeWorkspace: async () => { calls.push("remove"); throw failure; },
      validate: () => { calls.push("validate"); return { ok: true }; },
      writeOutput: async () => { calls.push("write"); },
    })).rejects.toBe(failure);
    expect(calls).toEqual(["remove"]);
  });

  test("stamps cleanup only after removal and before final validation and write", async () => {
    // Given
    const calls = [];
    let written;
    const evidence = { cleanup: { resources_released: true, processes_terminated: true, raw_endpoint_retained: false, external_writes: 0 } };

    // When
    await finalizeQualification({
      evidence, binding: {}, now: "now", removeWorkspace: async () => { calls.push("remove"); },
      validate: (receipt) => { calls.push("validate"); expect(receipt.cleanup.temporary_files_removed).toBe(true); return { ok: true }; },
      writeOutput: async (receipt) => { calls.push("write"); written = receipt; },
    });

    // Then
    expect(calls).toEqual(["remove", "validate", "write"]);
    expect(written.cleanup.temporary_files_removed).toBe(true);
    expect(evidence.cleanup.temporary_files_removed).toBeUndefined();
  });

  test("does not accept caller-provided receipt or capture evidence", async () => {
    // Given / When
    const source = await readFile(join(import.meta.dir, "scenario-driver.mjs"), "utf8");

    // Then
    expect(source).not.toMatch(/scenarioReceipt|captureFile|preexisting/i);
    expect(source).not.toMatch(/setTimeout|Start-Sleep|Bun\.sleep/);
    expect(source).toContain("subscribe");
    expect(source).toContain("AbortSignal.timeout");
  });
});
