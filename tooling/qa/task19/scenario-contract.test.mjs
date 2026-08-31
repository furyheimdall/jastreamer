import { describe, expect, test } from "bun:test";
import { materializeScenarioContract, TASK19_SCENARIOS, TASK19_SCENARIO_IDS, task19Scenario } from "./scenario-contract.mjs";

const API_ROUTE = /^\/api\/v1\//;

describe("Task19 immutable semantic scenario contract", () => {
  test("defines all 30 scenarios without a fallback semantic", () => {
    // Given: the repository-owned scenario contract.
    // When: its exact scenario inventory is inspected.
    // Then: all required scenarios are explicit and unknown IDs have no fallback.
    expect(TASK19_SCENARIO_IDS).toHaveLength(30);
    expect(new Set(TASK19_SCENARIO_IDS).size).toBe(30);
    expect(task19Scenario("unknown")).toBeUndefined();
  });

  test("binds every scenario to an exact product action response state and event contract", () => {
    // Given: every immutable scenario.
    const contracts = Object.values(TASK19_SCENARIOS);
    // When: machine-consumed semantic fields are projected.
    // Then: no generic method, route, status, code, state, event, or failure expectation is missing.
    for (const contract of contracts) {
      expect(["GET", "POST", "PATCH", "PUT", "DELETE"]).toContain(contract.surface.request.method);
      expect(contract.surface.request.route).toMatch(API_ROUTE);
      expect(contract.surface.selector.name.length).toBeGreaterThan(0);
      expect(["click", "select", "restart"]).toContain(contract.surface.gesture);
      if (contract.expected.surface === "control") expect(contract.expected.status).toBe(0);
      else expect(contract.expected.status).toBeGreaterThanOrEqual(100);
      expect(contract.expected.code).not.toBe("OBSERVED");
      expect(["advanced", "unchanged"]).toContain(contract.state.revision);
      expect(["invalidation", "none"]).toContain(contract.event.kind);
      expect(typeof contract.expected.failure).toBe("boolean");
    }
  });

  test("matches the shipped Server wire instead of invented success codes", () => {
    // Given: representative pairing, scan, queue, transport, and failure scenarios.
    // When: their machine-consumed wire contracts are inspected.
    // Then: status, command, and response semantics match the mounted Server API.
    expect(task19Scenario("pair").expected).toMatchObject({ status: 200, semantic: "discovery" });
    expect(task19Scenario("multi-root-scan").expected).toMatchObject({ status: 202, semantic: "scan-job" });
    expect(task19Scenario("queue-add").expected).toMatchObject({ status: 201, semantic: "queue-mutation" });
    expect(task19Scenario("transport-start").expected).toMatchObject({ status: 202, semantic: "transport-mutation", statusField: "pending" });
    expect(task19Scenario("queue-retry").surface.request.body.command).toBe("retry_blocked");
    expect(task19Scenario("queue-skip").surface.request.body.command).toBe("skip_blocked");
    expect(task19Scenario("transport-skip").surface.request.body.command).toBe("skip");
    expect(task19Scenario("invalid-config").expected).toMatchObject({ status: 400, code: "CONFIG_VALIDATION_FAILED" });
    expect(task19Scenario("stale-revision").expected).toMatchObject({ status: 412, code: "STALE_CONFIG_REVISION" });
    expect(task19Scenario("certificate-change").expected).toMatchObject({ surface: "control", failure: true });
  });

  test("materializes setup-owned IDs without retaining static scenario literals", () => {
    // Given: a queue action and typed values returned by live setup.
    const contract = task19Scenario("queue-remove");
    // When: setup material is applied at the operation boundary.
    const materialized = materializeScenarioContract(contract, { zoneId: "zone-live-1", entryId: "entry-live-9", trackId: "track-live-4", catalogRoot: "/catalog-live" });
    // Then: route/body use only live values and the immutable template remains unresolved.
    expect(materialized.surface.request.route).toBe("/api/v1/zones/zone-live-1/queue"); expect(materialized.surface.request.body.entry_id).toBe("entry-live-9"); expect(JSON.stringify(contract)).not.toContain("task19-entry");
  });

  test("models secure credential restoration as an owned restart rather than an absent pairing control", () => {
    const contract = TASK19_SCENARIOS["secure-token-restart"]; expect(contract.surface).toMatchObject({ selector: { role: "application", name: "Restart Control" }, gesture: "restart", request: { method: "GET", route: "/api/v1/discovery" } }); expect(contract.surface.selector.name).not.toBe("Complete pairing"); expect(contract.expected.eventRequired).toBe(false);
  });

  test("deep-freezes scenario contracts and their prerequisite plans", () => {
    // Given: a scenario with nested body and prerequisite values.
    const contract = task19Scenario("queue-add");
    // When / Then: every machine-consumed layer is immutable.
    expect(Object.isFrozen(TASK19_SCENARIOS)).toBe(true);
    expect(Object.isFrozen(contract)).toBe(true);
    expect(Object.isFrozen(contract.preconditions)).toBe(true);
    expect(Object.isFrozen(contract.surface.request.body)).toBe(true);
    expect(Object.isFrozen(contract.preconditions[1])).toBe(true);
  });

  test("names exact scale and adverse prerequisites instead of synthesizing observations", () => {
    // Given: scale, stale, revocation, certificate, event-gap, renderer, and interruption scenarios.
    // When: their prerequisite boundary kinds are collected.
    const kinds = ["multi-root-scan", "stale-revision", "revocation", "certificate-change", "event-gap-resync", "offline-renderer", "interrupted-restart"].flatMap((id) => task19Scenario(id).preconditions.map((item) => `${item.kind}:${item.state ?? item.command ?? item.route}`));
    // Then: every adverse state is explicitly provisioned through an API, CLI, session, credential, or process boundary.
    expect(kinds).toEqual(expect.arrayContaining([
      "cli:task19-catalog-fixture",
      "api:/api/v1/config",
      "api:/api/v1/devices/:controllerId",
      "process:server-certificate-rotated-after-pairing",
      "process:control-event-sequence-gap",
      "process:task19-renderer-disconnected",
      "process:server-interrupted-after-durable-mutation",
    ]));
  });
});
