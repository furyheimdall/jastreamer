import { describe, expect, test } from "bun:test";
import { sanitizeTask19EvidenceValue } from "./task19-diagnostic-sanitizer.mjs";
import { createTask19DiagnosticTrace, safeOwnFields, serializePageError } from "./task19-page-error-diagnostic.mjs";

describe("Task19 page error diagnostics", () => {
  test("captures safe error structure without secret-bearing own fields", () => { const error = new Error("anonymous"); error.cause = new TypeError("cause"); error.code = "ENGINE"; error.token = "forbidden"; const value = serializePageError(error); expect(value).toMatchObject({ name: "Error", message: "anonymous", cause: { type: "object", message: "cause" }, ownFields: { code: "ENGINE" } }); expect(JSON.stringify(value)).not.toContain("forbidden"); expect(safeOwnFields(null)).toEqual({}); });
  test("redacts credentials embedded in error text and URLs", () => { const error = new Error("request failed https://example.invalid/callback?access_token=live-access"); error.stack = "Authorization: Bearer live-bearer"; error.cause = new Error("adminToken=live-admin"); const serialized = JSON.stringify(serializePageError(error)); expect(serialized).not.toContain("live-access"); expect(serialized).not.toContain("live-bearer"); expect(serialized).not.toContain("live-admin"); expect(serialized).toContain("[redacted]"); });
  test("correlates an error with monotonic preceding and following actions", () => { let now = 10; const trace = createTask19DiagnosticTrace({ now: () => now++ }); trace.action("duplicate-trigger", { sequence: 2 }); trace.error("pageerror", { message: "Error" }); trace.action("stale-trigger", { sequence: 1 }); const report = trace.report(); expect(report.errors[0]).toMatchObject({ stage: "pageerror", precedingAction: { stage: "duplicate-trigger" }, followingAction: { stage: "stale-trigger" } }); expect(Object.isFrozen(report.errors[0])).toBe(true); });
  test("sanitizes every nested action and error trace detail", () => {
    const trace = createTask19DiagnosticTrace();
    trace.action("request", {
      url: "https://example.invalid/?access_token=live-access",
      headers: { authorization: "Bearer live-bearer" },
    });
    trace.error("cdp", {
      description: "adminToken=live-admin RuntimeError: {\"token\":\"nested-secret\"}",
      stackTrace: { callFrames: [{ url: "https://example.invalid/?ticket=live-ticket" }] },
    });
    const serialized = JSON.stringify(trace.report());
    for (const secret of ["live-access", "live-bearer", "live-admin", "live-ticket", "nested-secret"]) {
      expect(serialized).not.toContain(secret);
    }
    expect(serialized).toContain("[redacted]");
  });
  test("redacts host paths without changing API routes", () => {
    expect(sanitizeTask19EvidenceValue({
      executablePath: "/home/user/chromium/chrome",
      mediaPath: "/tmp/task19/music.wav",
      route: "/api/v1/zones",
    })).toEqual({
      executablePath: "[host-path]/chrome",
      mediaPath: "[host-path]/music.wav",
      route: "/api/v1/zones",
    });
  });
});
