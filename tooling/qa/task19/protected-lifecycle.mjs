import { createInterface } from "node:readline";

export const TASK19_PHASE_LIMITS = Object.freeze({ setup: 1_200_000, driver: 5_100_000, cleanup: 600_000, watchdogMargin: 60_000 });
const timeoutFor = (phase) => TASK19_PHASE_LIMITS[phase] + TASK19_PHASE_LIMITS.watchdogMargin;

export const protectLifecycle = (options) => {
  let phase = "setup"; let timer;
  const arm = (next) => { phase = next; if (timer !== undefined) options.clearTimer(timer); timer = options.setTimer(() => options.terminateTree(options.child.pid, phase), timeoutFor(phase)); };
  arm("setup");
  const lines = createInterface({ input: options.child.stdout });
  lines.on("line", (line) => {
    const match = /^\[TASK19_PHASE](setup|driver|cleanup|done)$/.exec(line);
    if (match?.[1] === "done") { if (timer !== undefined) options.clearTimer(timer); }
    else if (match?.[1] !== undefined) arm(match[1]);
    options.forward(`${line}\n`);
  });
  options.child.once("exit", () => { if (timer !== undefined) options.clearTimer(timer); lines.close(); });
  return { phase: () => phase };
};

export class Task19LifecycleCleanupError extends Error {
  constructor(failures, postInventory, cause, receipt) { super("TASK19_LIFECYCLE_CLEANUP_AGGREGATE_FAILED"); this.name = "Task19LifecycleCleanupError"; this.failures = failures; this.postInventory = postInventory; this.cause = cause; this.receipt = receipt; }
}

const executeLifecycleHarness = async (options) => {
  const installed = []; const descendants = []; let productCommandsExecuted = 0; let failure;
  try {
    for (const installer of options.installers) {
      const current = options.hash(installer.identity); if (current !== installer.sha256) throw new Error(`TASK19_PLAN_FILE_DIGEST_MISMATCH:${installer.identity}`);
      const result = await installer.run(); productCommandsExecuted += 1; installed.push(installer.identity); descendants.push(...result.descendants);
      if (result.exitCode !== 0) throw new Error(`TASK19_INSTALLER_EXIT_CODE:${installer.identity}:${result.exitCode}`);
    }
    if (options.hash(options.driver.identity) !== options.driver.sha256) throw new Error("TASK19_REPOSITORY_DRIVER_DIGEST_MISMATCH");
    const result = await options.driver.run(); productCommandsExecuted += 1; descendants.push(...result.descendants);
    if (result.timedOut) throw new Error("TASK19_EXECUTION_TIMEOUT");
    if (result.exitCode !== 0) throw new Error(`TASK19_SCENARIO_DRIVER_FAILED:${result.exitCode}`);
  } catch (error) { failure = error; }
  const terminated = [...new Set(descendants)].sort((left, right) => left - right); const cleaned = [...installed].reverse(); const cleanupFailures = [];
  for (const pid of terminated) { try { await options.terminate(pid); } catch (error) { cleanupFailures.push({ operation: "terminate", pid, message: error instanceof Error ? error.message : String(error) }); } }
  for (const identity of cleaned) { try { await options.cleanup(identity); } catch (error) { cleanupFailures.push({ operation: "cleanup", identity, message: error instanceof Error ? error.message : String(error) }); } }
  let after; try { after = await options.inventory(); } catch (error) { cleanupFailures.push({ operation: "post-inventory", message: error instanceof Error ? error.message : String(error) }); }
  const cleanupComplete = after !== undefined && JSON.stringify(after) === JSON.stringify(options.before); if (!cleanupComplete && after !== undefined) cleanupFailures.push({ operation: "post-state", message: "TASK19_CLEANUP_POST_STATE_FAILED" });
  const receipt = { productCommandsExecuted, terminated, cleaned, cleanupComplete };
  if (cleanupFailures.length) throw new Task19LifecycleCleanupError(cleanupFailures, after, failure, receipt);
  if (failure !== undefined) throw Object.assign(failure, { receipt });
  return receipt;
};

export const executeProtectedLifecycle = (options) => {
  if (options.kind === "harness") return executeLifecycleHarness(options);
  if (options.kind !== "process") throw new Error("TASK19_LIFECYCLE_ADAPTER_INVALID");
  return new Promise((resolve, reject) => {
    let settled = false;
    protectLifecycle({ ...options, terminateTree: (pid, phase) => {
      if (settled) return; settled = true;
      options.terminateTree(pid, phase, (error) => error === undefined ? reject(new Error(`TASK19_PROTECTED_${phase.toUpperCase()}_WATCHDOG_TIMEOUT`)) : reject(error));
    } });
    options.child.once("error", (error) => { if (!settled) { settled = true; reject(error); } });
    options.child.once("exit", (code) => { if (!settled) { settled = true; resolve(code); } });
  });
};
