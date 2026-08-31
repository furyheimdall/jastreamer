import { EventEmitter } from "node:events";
import { PassThrough } from "node:stream";
import { describe, expect, test } from "bun:test";
import { executeProtectedLifecycle, protectLifecycle, TASK19_PHASE_LIMITS, Task19LifecycleCleanupError } from "./protected-lifecycle.mjs";

const processFixture = () => { const child = new EventEmitter(); child.pid = 4242; child.stdout = new PassThrough(); const timers = []; const cleared = []; const terminated = []; const lifecycle = protectLifecycle({ child, setTimer: (callback, milliseconds) => { const timer = { callback, milliseconds }; timers.push(timer); return timer; }, clearTimer: (timer) => cleared.push(timer), terminateTree: (pid, phase) => terminated.push({ pid, phase }), forward: () => {} }); return { child, timers, cleared, terminated, lifecycle }; };
const operation = (identity, state, patch = {}) => ({ identity, sha256: `hash-${identity}`, run: async () => { state.add(identity); return { exitCode: 0, descendants: [], ...patch }; } });
const harness = (patch = {}) => { const state = new Set(); const hashes = new Map(["server", "windows", "renderer", "driver"].map((identity) => [identity, `hash-${identity}`])); const terminated = []; const cleaned = []; return { state, hashes, terminated, cleaned, options: { before: [], hash: (identity) => hashes.get(identity), installers: [operation("server", state), operation("windows", state), operation("renderer", state)], driver: operation("driver", state), terminate: (pid) => terminated.push(pid), cleanup: async (identity) => { state.delete(identity); cleaned.push(identity); }, inventory: () => [...state].sort(), ...patch } }; };

describe("Task19 protected runtime lifecycle", () => {
  test("starts driver and cleanup timers only on exact phase transitions", () => {
    const value = processFixture();
    expect(value.timers[0].milliseconds).toBe(TASK19_PHASE_LIMITS.setup + TASK19_PHASE_LIMITS.watchdogMargin);
    value.child.stdout.write("[TASK19_PHASE]driver\n"); expect(value.lifecycle.phase()).toBe("driver");
    expect(value.timers[1].milliseconds).toBe(TASK19_PHASE_LIMITS.driver + TASK19_PHASE_LIMITS.watchdogMargin);
    value.child.stdout.write("[TASK19_PHASE]cleanup\n"); expect(value.lifecycle.phase()).toBe("cleanup");
    expect(value.timers[2].milliseconds).toBe(TASK19_PHASE_LIMITS.cleanup + TASK19_PHASE_LIMITS.watchdogMargin);
    value.child.stdout.write("[TASK19_PHASE]done\n"); expect(value.cleared).toContain(value.timers[2]);
  });

  test("watchdog terminates the exact protected process tree with its active phase", () => {
    const value = processFixture(); value.child.stdout.write("[TASK19_PHASE]cleanup\n"); value.timers.at(-1).callback();
    expect(value.terminated).toEqual([{ pid: 4242, phase: "cleanup" }]);
  });

  test("production process adapter consumes protected-runner phase events and exact exit code", async () => {
    const child = new EventEmitter(); child.pid = 5252; child.stdout = new PassThrough(); const timers = [];
    const exited = executeProtectedLifecycle({ kind: "process", child, setTimer: (callback, milliseconds) => { const timer = { callback, milliseconds }; timers.push(timer); return timer; }, clearTimer: () => {}, forward: () => {}, terminateTree: () => { throw new Error("TEST_UNEXPECTED_TIMEOUT"); } });
    child.stdout.write("[TASK19_PHASE]setup\n[TASK19_PHASE]driver\n[TASK19_PHASE]cleanup\n[TASK19_PHASE]done\n"); child.emit("exit", 9);
    expect(await exited).toBe(9); expect(timers.map(({ milliseconds }) => milliseconds)).toEqual([1_260_000, 1_260_000, 5_160_000, 660_000]);
  });

  test("production adapter boundary handles installer failure and exact cleanup", async () => {
    const value = harness(); value.options.installers[0] = operation("server", value.state, { exitCode: 1603, descendants: [31] });
    try { await executeProtectedLifecycle({ kind: "harness", ...value.options }); throw new Error("TEST_EXPECTED_FAILURE"); } catch (error) { expect(error.message).toBe("TASK19_INSTALLER_EXIT_CODE:server:1603"); expect(error.receipt).toEqual({ productCommandsExecuted: 1, terminated: [31], cleaned: ["server"], cleanupComplete: true }); }
    expect(value.cleaned).toEqual(["server"]); expect(value.terminated).toEqual([31]);
  });

  test("driver timeout terminates every descendant once and proves post-state absence", async () => {
    const value = harness({ driver: { identity: "driver", sha256: "hash-driver", run: async () => ({ exitCode: 0, timedOut: true, descendants: [44, 43, 44] }) } });
    try { await executeProtectedLifecycle({ kind: "harness", ...value.options }); throw new Error("TEST_EXPECTED_FAILURE"); } catch (error) { expect(error.message).toBe("TASK19_EXECUTION_TIMEOUT"); expect(error.receipt.terminated).toEqual([43, 44]); expect(error.receipt.cleanupComplete).toBe(true); }
    expect(value.state.size).toBe(0); expect(value.cleaned).toEqual(["renderer", "windows", "server"]);
  });

  test("rehashes each package immediately before use and fails before a mutated later installer", async () => {
    const value = harness(); value.options.installers[0] = { ...operation("server", value.state), run: async () => { value.state.add("server"); value.hashes.set("windows", "mutated"); return { exitCode: 0, descendants: [] }; } };
    let windowsRuns = 0; value.options.installers[1] = { ...operation("windows", value.state), run: async () => { windowsRuns += 1; return { exitCode: 0, descendants: [] }; } };
    await expect(executeProtectedLifecycle({ kind: "harness", ...value.options })).rejects.toThrow("TASK19_PLAN_FILE_DIGEST_MISMATCH:windows");
    expect(windowsRuns).toBe(0); expect(value.state.size).toBe(0);
  });

  test("nonzero driver exit is preserved while cleanup remains exact", async () => {
    const value = harness({ driver: { identity: "driver", sha256: "hash-driver", run: async () => ({ exitCode: 9, timedOut: false, descendants: [52] }) } });
    await expect(executeProtectedLifecycle({ kind: "harness", ...value.options })).rejects.toThrow("TASK19_SCENARIO_DRIVER_FAILED:9");
    expect(value.terminated).toEqual([52]); expect(value.state.size).toBe(0);
  });

  test.each([["first", ["renderer"]], ["last", ["server"]], ["multiple", ["renderer", "server"]]])("attempts all cleanup and post-inventory when %s cleanup operation fails", async (_name, failures) => {
    let inventories = 0; const value = harness(); value.options.driver = { identity: "driver", sha256: "hash-driver", run: async () => ({ exitCode: 0, descendants: [] }) }; value.options.inventory = () => { inventories += 1; return [...value.state].sort(); }; value.options.cleanup = async (identity) => { value.state.delete(identity); value.cleaned.push(identity); if (failures.includes(identity)) throw new Error(`UNINSTALL_FAILED:${identity}`); };
    try { await executeProtectedLifecycle({ kind: "harness", ...value.options }); throw new Error("TEST_EXPECTED_FAILURE"); } catch (error) { expect(error).toBeInstanceOf(Task19LifecycleCleanupError); expect(error.failures).toHaveLength(failures.length); expect(error.postInventory).toEqual([]); }
    expect(value.cleaned).toEqual(["renderer", "windows", "server"]); expect(inventories).toBe(1); expect(value.state.size).toBe(0);
  });
});
