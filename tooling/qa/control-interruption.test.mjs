import { expect, test } from "bun:test";
import { spawn } from "node:child_process";
import { watch } from "node:fs";
import { mkdir, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const repository = resolve(import.meta.dirname, "../..");
const qaDirectory = join(repository, "tooling/qa");
const sharedPlaywrightOutput = join(qaDirectory, "test-results");

const waitForNewServerRoots = async (baseline, count) => {
  const watcher = watch(tmpdir());
  let timeout;
  try {
    return await Promise.race([
      new Promise((resolveRoots, reject) => {
        watcher.on("error", reject);
        watcher.on("change", async () => {
          const roots = (await readdir(tmpdir()))
            .filter((name) => name.startsWith("jastreamer-control-qa-") && !baseline.has(name));
          if (roots.length >= count) resolveRoots(roots.slice(0, count));
        });
      }),
      new Promise((_, reject) => {
        timeout = setTimeout(() => reject(new Error("interrupted Control roots were not created")), 60_000);
      }),
    ]);
  } finally {
    clearTimeout(timeout);
    watcher.close();
  }
};

const startControlPlaywright = (output) => {
  const child = spawn(join(qaDirectory, "control.sh"), [
    "--platform", "web,windows,android",
    "--fixture", join(repository, "tooling/fixtures/e2e/control-policy-happy.yaml"),
    "--screenshots", output,
  ], {
    cwd: repository,
    env: { ...process.env, CONTROL_QA_BROWSER_ONLY: "1" },
    stdio: ["ignore", "pipe", "pipe"],
  });
  const started = new Promise((resolveStarted) => {
    let transcript = "";
    const observe = (chunk) => {
      transcript += chunk.toString();
      if (transcript.includes("real Todo13 Control policy flow")) resolveStarted();
    };
    child.stdout.on("data", observe);
    child.stderr.on("data", observe);
  });
  return { child, started };
};

const closeResult = (child) => new Promise((resolveExit, reject) => {
  child.once("error", reject);
  child.once("close", (code, signal) => resolveExit({ code, signal }));
});

test("two concurrent interrupted Control invocations own Playwright output and server cleanup", async () => {
  // Given
  const baseline = new Set((await readdir(tmpdir())).filter((name) => name.startsWith("jastreamer-control-qa-")));
  const outputRoot = join(tmpdir(), `control-interruptions-${process.pid}`);
  const outputs = [join(outputRoot, "left"), join(outputRoot, "right")];
  await Promise.all(outputs.map(async (output) => {
    await mkdir(join(output, "playwright"), { recursive: true });
    await writeFile(join(output, "control-policy-happy.json"), '{"status":"passed"}\n');
    await writeFile(join(output, "playwright/prior-run"), "stale");
  }));
  const rootsCreated = waitForNewServerRoots(baseline, 2);
  const invocations = outputs.map(startControlPlaywright);
  const children = invocations.map(({ child }) => child);
  const exits = children.map(closeResult);

  try {
    // When
    const [roots] = await Promise.all([
      rootsCreated,
      ...invocations.map(({ started }) => started),
    ]);
    for (const child of children) child.kill("SIGTERM");
    const results = await Promise.all(exits);

    // Then
    expect(results.every(({ code, signal }) => code === 143 || signal === "SIGTERM")).toBe(true);
    for (const output of outputs) {
      expect(await Bun.file(join(output, "control-policy-happy.json")).exists()).toBe(false);
      expect(await Bun.file(join(output, "playwright/prior-run")).exists()).toBe(false);
    }
    for (const root of roots) {
      expect(await Bun.file(join(tmpdir(), root)).exists()).toBe(false);
    }
    expect(await Bun.file(sharedPlaywrightOutput).exists()).toBe(false);
  } finally {
    for (const child of children) {
      if (child.exitCode === null && child.signalCode === null) child.kill("SIGKILL");
    }
    const current = (await readdir(tmpdir())).filter((name) => name.startsWith("jastreamer-control-qa-") && !baseline.has(name));
    await Promise.all(current.map((name) => rm(join(tmpdir(), name), { recursive: true, force: true })));
    await Promise.all([rm(outputRoot, { recursive: true, force: true }), rm(sharedPlaywrightOutput, { recursive: true, force: true })]);
  }
}, 120_000);
