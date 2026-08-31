import { describe, expect, test } from "bun:test";
import { createWebControlRuntime } from "./task19-web-control.mjs";

describe("Task19 process-owned persistent Control Web", () => {
  test("records and drives one Playwright browser process with origin verification on every operation", async () => {
    // Given: one exact Playwright process and a bound HTTPS origin.
    const calls = []; const page = { goto: async (url) => calls.push(["goto", url]), locator: () => ({ isVisible: async () => false }), close: async () => calls.push(["page-close"]) }; const context = { newPage: async () => page, close: async () => calls.push(["context-close"]) }; const browser = { process: () => ({ pid: 4242 }), newContext: async () => context, close: async () => calls.push(["browser-close"]) }; const chromium = { launch: async (options) => { calls.push(["launch", options]); return browser; } }; const verifyOrigin = async (url, binding) => calls.push(["verify", url, binding]); const runtime = createWebControlRuntime({ chromium, verifyOrigin }); const binding = { host: "127.0.0.1", port: 9443, certificateSha256: "c".repeat(64), spkiSha256: "s".repeat(64) };
    // When: the installed process starts and two scenarios use it.
    const started = await runtime.start({ runId: "task19-web-server-first", url: "https://127.0.0.1:9443", binding, spkiPinBase64: "exact-pin" }); const first = await runtime.operation({ runId: "task19-web-server-first", scenarioId: "pair", perform: async (ownedPage) => ownedPage === page ? "first" : "wrong" }); const second = await runtime.operation({ runId: "task19-web-server-first", scenarioId: "admin", perform: async () => "second" }); await runtime.close("task19-web-server-first");
    // Then: lifecycle PID is the driven browser PID, one browser exists, and binding precedes navigation and each operation.
    expect(started.pid).toBe(4242); expect([first, second]).toEqual(["first", "second"]); expect(calls.filter(([kind]) => kind === "launch")).toHaveLength(1); expect(calls.filter(([kind]) => kind === "verify")).toHaveLength(3); expect(calls.slice(0, 3).map(([kind]) => kind)).toEqual(["verify", "launch", "goto"]);
  });

  test("reloads a replacement page in the same owned context without looking for Complete pairing", async () => {
    // Given: paired credential storage belongs to one persistent browser context.
    const calls = []; let currentPage; let pid = 4242; const page = (name) => ({ goto: async (url) => calls.push([`${name}-goto`, url]), reload: async () => calls.push([`${name}-reload`]), locator: () => ({ isVisible: async () => false }), close: async () => calls.push([`${name}-close`]) }); const original = page("original"); const admin = page("admin"); const replacement = page("replacement"); const context = { pages: () => [original, admin], newPage: async () => (currentPage = currentPage === undefined ? original : replacement), close: async () => {}, storageState: async () => ({ origins: [{ origin: "control", localStorage: [{ name: "credential", value: "stored" }] }] }) }; const browser = { process: () => ({ pid }), isConnected: () => true, newContext: async () => context, close: async () => {} }; const runtime = createWebControlRuntime({ chromium: { launch: async () => browser }, verifyOrigin: async () => {} }); const input = { runId: "task19-web-server-first", url: "https://127.0.0.1:9443", binding: {}, spkiPinBase64: "pin" };
    // When: secure-token restart replaces the page.
    await runtime.start(input); const beforeStorage = await context.storageState(); const restarted = await runtime.restart(input.runId); const afterStorage = await context.storageState();
    // Then: browser/context/storage ownership is unchanged and no absent pairing control is used.
    expect(restarted.pid).toBe(4242); expect(afterStorage).toEqual(beforeStorage); expect(calls).toEqual([["original-goto", input.url], ["original-reload"]]); expect(JSON.stringify(calls)).not.toContain("Complete pairing");
    // And: stale replacement browser identity is rejected.
    pid = 5252; await expect(runtime.restart(input.runId)).rejects.toThrow("TASK19_WEB_BROWSER_PID_INVALID"); await runtime.close(input.runId);
  });

  test("rejects a missing browser PID and never substitutes a launcher wrapper", async () => {
    // Given: a browser launcher without the actual browser process handle.
    const runtime = createWebControlRuntime({ chromium: { launch: async () => ({ process: () => null, close: async () => {} }) }, verifyOrigin: async () => {} });
    // When / Then: process inventory cannot use a wrapper or fabricated PID.
    await expect(runtime.start({ runId: "task19-web-server-first", url: "https://127.0.0.1:9443", binding: {}, spkiPinBase64: "pin" })).rejects.toThrow("TASK19_WEB_BROWSER_PID_INVALID");
  });
});
