import { describe, expect, test } from "bun:test";
import { readFile } from "node:fs/promises";
import { createTask19DurableRestartTrack, createTask19PageRequestRecord, createTask19TrackPlan, createTask19ZeroRequestWindow, assertTask19ExactSpecSource } from "./task19-event-stream-contract.mjs";

const frameHash = "a".repeat(64); const observed = Object.freeze({ type: "invalidation", resource: "queue", zone_id: "main", revision: 41, server_epoch: "18446744073709551615", sequence: 41, observedFrame: Object.freeze({ frameId: `frame:${frameHash}`, epoch: "18446744073709551615", sequence: "41", kind: "invalidation", resource: "queue", zoneId: "main", revision: "41", frameHash, payloadHash: "b".repeat(64) }) });

describe("Task19 exact event-stream harness contracts", () => {
  test("constructs an immutable hash-addressed plan with exact lexical provenance", () => {
    const replay = createTask19TrackPlan({ runId: "run-exact", runtime: "node-sidecar", event: observed }); expect(replay.control).toEqual({ runId: "run-exact", ...observed.observedFrame }); expect(replay.provenance).toEqual({ kind: "node-sidecar-observed-frame", runId: "run-exact", ...observed.observedFrame }); expect(Object.isFrozen(replay)).toBe(true); expect(Object.isFrozen(replay.control)).toBe(true); expect(Object.isFrozen(replay.provenance)).toBe(true);
  });

  test("constructs a next-frame drop plan anchored to exact observed provenance", () => {
    const drop = createTask19TrackPlan({ runId: "run-exact", runtime: "node-sidecar", event: observed, relation: "next" });
    expect(drop.control).toEqual({ runId: "run-exact", ...observed.observedFrame, relation: "next" });
    expect(Object.isFrozen(drop.control)).toBe(true);
  });

  test("rejects rounded, unobserved, cross-runtime, and mutable epoch inputs", () => {
    // Given: inputs that cannot prove exact sidecar frame provenance.
    const invalid = [
      { runId: "run", runtime: "node-sidecar", event: { ...observed, server_epoch: 18_446_744_073_709_552_000 } },
      { runId: "run", runtime: "browser-mock", event: observed },
      { runId: "", runtime: "node-sidecar", event: observed },
      { runId: "run", runtime: "node-sidecar", event: { ...observed, observedFrame: { ...observed.observedFrame, frameId: undefined } } },
    ];
    // When / Then: every unbound plan is rejected.
    for (const input of invalid) expect(() => createTask19TrackPlan(input)).toThrow(/TASK19_TRACK_(?:EVENT|PROVENANCE)_INVALID/);
  });

  test("correlates a request to the exact page target and main frame", () => {
    // Given: a request whose frame is the process-owned page main frame.
    const frame = {}; const page = { mainFrame: () => frame }; const request = { method: () => "GET", url: () => "https://127.0.0.1:9443/api/v1/zones", postData: () => null, frame: () => frame };
    // When: the request record is created at the page boundary.
    const record = createTask19PageRequestRecord({ request, page, targetId: "target-1", observedAt: 123 });
    // Then: target and frame identity remain explicit evidence fields.
    expect(record).toMatchObject({ method: "GET", route: "/api/v1/zones", pageTargetId: "target-1", frameIdentity: "main", startedAt: 123 });
  });

  test("rejects page or sidecar verification traffic while duplicate/stale observation is armed", () => {
    const recoveryRoutes = ["/api/v1/zones/main/playback-state"]; const window = createTask19ZeroRequestWindow({ pageStart: 1, sidecarStart: 1, recoveryRoutes }); const baselinePage = [{ route: "/before" }]; const baselineSidecar = [{ kind: "http", request: { method: "GET", route: "/before" } }]; expect(window.observe({ pageRequests: baselinePage, sidecarTransport: baselineSidecar })).toEqual([]);
    for (const input of [{ pageRequests: [...baselinePage, { method: "GET", route: recoveryRoutes[0], pageTargetId: "target-1", frameIdentity: "main" }], sidecarTransport: baselineSidecar }, { pageRequests: baselinePage, sidecarTransport: [...baselineSidecar, { kind: "http", request: { method: "GET", route: recoveryRoutes[0] } }] }]) { try { window.observe(input); throw new Error("EXPECTED_CONTAMINATION"); } catch (error) { expect(error.message).toBe("TASK19_ZERO_REQUEST_WINDOW_CONTAMINATED"); expect(error.classified).toHaveLength(1); expect(error.classified[0]).toMatchObject({ route: recoveryRoutes[0], recoveryRoute: true }); } }
  });

  test("wires durable restart subscriptions before the restart callback", async () => {
    // Given: callbacks that record invocation order without starting a Server.
    const calls = []; const track = createTask19DurableRestartTrack({ subscribeSnapshot: () => { calls.push("snapshot"); return "snapshot-signal"; }, subscribeRecovery: () => { calls.push("recovery"); return "recovery-signal"; }, restart: async () => { calls.push("restart"); return "restarted"; } });
    // When: subscriptions are established before the restart trigger.
    const snapshot = track.subscribeSnapshot(); const recovery = track.subscribeRecovery(); const restarted = await track.restart();
    // Then: callback order and returned signals are deterministic.
    expect(calls).toEqual(["snapshot", "recovery", "restart"]); expect([snapshot, recovery, restarted]).toEqual(["snapshot-signal", "recovery-signal", "restarted"]); expect(Object.isFrozen(track)).toBe(true);
  });

  test("rejects synthetic aliases and forbidden UI methods while the exact spec passes", async () => {
    // Given: the current exact spec and each forbidden machine-consumed symbol.
    const source = await readFile(new URL("task19-event-stream-exact.playwright.mjs", import.meta.url), "utf8");
    // When / Then: current source passes and every synthetic action is rejected.
    expect(() => assertTask19ExactSpecSource(source)).not.toThrow(); const armedWindow = source.slice(source.indexOf("await capture.replayFrame(duplicatePlan.control)"), source.indexOf("const diagnostics = await capture.diagnostics()", source.indexOf("await capture.replayFrame(duplicatePlan.control)"))); expect(armedWindow).not.toContain("requestJson("); expect(armedWindow).not.toContain("/api/v1/zones/main/playback-state");
    for (const forbidden of ["capture.replayEvent(1)", "capture.dropNextEvent()", "capture.injectEvent({})", "field.fill('x')", "button.click()"]) expect(() => assertTask19ExactSpecSource(forbidden)).toThrow(/TASK19_(?:SYNTHETIC_ALIAS|FORBIDDEN_UI_ACTION)_REJECTED/);
  });
});
