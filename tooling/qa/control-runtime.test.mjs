import { expect, test } from "bun:test";
import { spawn } from "node:child_process";
import { EventEmitter } from "node:events";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { chromium } from "@playwright/test";
import {
  replaceFlutterText,
  revealFlutterAnchor,
} from "./control-playwright-actions.mjs";
import { stopChild, trackServerConnections } from "./control-server-process.mjs";

const repository = resolve(import.meta.dirname, "../..");
const importScenarioFixture = (fixture, output) => {
  const child = spawn("node", ["--input-type=module", "--eval", `import(${JSON.stringify(join(repository, "tooling/qa/control-scenario-fixture.mjs"))})`], {
    cwd: repository,
    env: { ...process.env, CONTROL_FIXTURE: fixture, CONTROL_OUTPUT: output },
    stdio: ["ignore", "ignore", "pipe"],
  });
  let stderr = "";
  child.stderr.on("data", (chunk) => { stderr += chunk.toString(); });
  return new Promise((resolveExit, reject) => {
    child.once("error", reject);
    child.once("exit", (code) => resolveExit({ code, stderr }));
  });
};

test("replaces focused semantics text through real Chromium keyboard events", async () => {
  // Given
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage();
  await page.setContent(`<label>Controller token<input value="old-token"></label><script>
    globalThis.controlEvents = [];
    document.querySelector("input").addEventListener("keydown", (event) => controlEvents.push(event.key));
  </script>`);
  const input = page.getByLabel("Controller token");

  try {
    // When
    await replaceFlutterText(input, "new-token");

    // Then
    expect(await input.inputValue()).toBe("new-token");
    expect(await page.evaluate(() => globalThis.controlEvents)).toEqual([
      "End", "Control", "A", "Backspace", "n", "e", "w", "-", "t", "o", "k", "e", "n",
    ]);
  } finally {
    await browser.close();
  }
});

test("reveals anchors by awaiting each forward scroll event", async () => {
  // Given
  const events = [];
  const wheelDeltas = [];
  const page = {
    viewportSize: () => ({ width: 390, height: 844 }),
    evaluate: async (_callback, argument) => {
      if (argument?.observationKey) {
        events.push("subscribe");
        return undefined;
      }
      if (typeof argument === "string") {
        events.push("await-scroll");
        return { before: 0, after: 700 };
      }
      return undefined;
    },
    mouse: {
      move: async () => { events.push("move"); },
      wheel: async (_x, y) => { events.push("wheel"); wheelDeltas.push(y); },
    },
  };
  const anchor = {
    isVisible: async () => true,
    boundingBox: async () => wheelDeltas.length === 2
      ? { x: 0, y: 100, width: 100, height: 48 }
      : { x: 0, y: 900, width: 100, height: 48 },
  };

  // When
  await revealFlutterAnchor(page, anchor, 4);

  // Then
  expect(wheelDeltas).toEqual([700, 700]);
  expect(events).toEqual([
    "move", "subscribe", "wheel", "await-scroll",
    "move", "subscribe", "wheel", "await-scroll",
  ]);
});

test("reveals a visible Flutter semantic that is outside the viewport", async () => {
  // Given
  const wheelDeltas = [];
  const page = {
    viewportSize: () => ({ width: 390, height: 844 }),
    evaluate: async (_callback, argument) => typeof argument === "string" ? 700 : undefined,
    mouse: {
      move: async () => {},
      wheel: async (_x, y) => { wheelDeltas.push(y); },
    },
  };
  const anchor = {
    isVisible: async () => true,
    boundingBox: async () => wheelDeltas.length > 0
      ? { x: 0, y: 100, width: 100, height: 48 }
      : { x: 0, y: 900, width: 100, height: 48 },
  };

  // When
  await revealFlutterAnchor(page, anchor, 2);

  // Then
  expect(wheelDeltas).toEqual([700]);
});

test("completes reveal observation when a wheel reaches a scroll boundary", async () => {
  // Given
  const listeners = new Map();
  const frames = [];
  let wheelHandled = false;
  const priorDocument = globalThis.document;
  const priorWindow = globalThis.window;
  const priorElement = globalThis.Element;
  const priorAnimationFrame = globalThis.requestAnimationFrame;
  globalThis.Element = class {};
  globalThis.document = {
    scrollingElement: Object.assign(new globalThis.Element(), { scrollTop: 700 }),
    addEventListener: (name, listener) => { listeners.set(name, listener); },
    removeEventListener: (name) => { listeners.delete(name); },
  };
  globalThis.window = { innerWidth: 390, innerHeight: 844, scrollY: 700 };
  globalThis.requestAnimationFrame = (callback) => { frames.push(callback); };
  let observationSettled = false;
  const page = {
    viewportSize: () => ({ width: 390, height: 844 }),
    evaluate: async (callback, argument) => {
      if (argument?.observationKey) {
        callback(argument);
        globalThis[argument.observationKey].then(() => { observationSettled = true; });
        return undefined;
      }
      if (typeof argument === "string") {
        await Promise.resolve();
        if (!observationSettled) throw new Error("wheel completion was not observed");
        return callback(argument);
      }
      return callback();
    },
    mouse: {
      move: async () => {},
      wheel: async () => {
        wheelHandled = true;
        listeners.get("wheel")?.({ target: globalThis.document.scrollingElement });
        while (frames.length > 0) frames.shift()();
      },
    },
  };
  const anchor = {
    isVisible: async () => true,
    boundingBox: async () => wheelHandled
      ? { x: 0, y: 100, width: 100, height: 48 }
      : { x: 0, y: 900, width: 100, height: 48 },
  };

  try {
    // When
    await revealFlutterAnchor(page, anchor, 2);

    // Then
    expect(wheelHandled).toBe(true);
    expect(listeners.size).toBe(0);
  } finally {
    globalThis.document = priorDocument;
    globalThis.window = priorWindow;
    globalThis.Element = priorElement;
    globalThis.requestAnimationFrame = priorAnimationFrame;
  }
});

test("tracks a persistent TLS socket once and releases its listener", async () => {
  // Given
  const socket = new EventEmitter();
  socket.destroyed = false;
  socket.destroy = () => { socket.destroyed = true; socket.emit("close"); };
  const server = {
    listening: true,
    close: (complete) => { server.listening = false; complete(); },
  };
  const connections = trackServerConnections(server);

  // When
  for (let request = 0; request < 32; request++) connections.track(socket);

  // Then
  expect(socket.listenerCount("close")).toBe(1);
  expect(await connections.close()).toEqual({ destroyedSockets: 1 });
  expect(socket.listenerCount("close")).toBe(0);
});

test("cleanup preserves the scenario failure and attempts every owned resource", async () => {
  // Given
  const { cleanupControlResources, ControlCleanupError } = await import("./control-servers.mjs");
  const calls = [];
  const scenarioFailure = new RangeError("scenario failed");
  const operations = [
    { name: "page", state: "present", close: async () => { calls.push("page"); throw new Error("protocol failure"); } },
    { name: "context", state: "absent", close: async () => { calls.push("context"); } },
    { name: "server", state: "present", close: async () => { calls.push("server"); } },
  ];

  // When
  let observed;
  try {
    await cleanupControlResources({ operations, primaryFailure: scenarioFailure });
  } catch (error) {
    observed = error;
  }

  // Then
  expect(calls).toEqual(["page", "server"]);
  expect(observed).toBeInstanceOf(ControlCleanupError);
  expect(observed.cause).toBe(scenarioFailure);
  expect(observed.failures).toEqual([{ name: "page", message: "protocol failure" }]);
  expect(observed.details).toEqual([
    { name: "page", status: "failed" },
    { name: "context", status: "already_absent" },
    { name: "server", status: "closed" },
  ]);
});

test("stops the complete owned process group", async () => {
  // Given
  const child = spawn(process.execPath, [
    "--input-type=module",
    "--eval",
    "import { spawn } from 'node:child_process'; const nested = spawn(process.execPath, ['--input-type=module', '--eval', 'setInterval(() => {}, 60000)']); process.on('SIGTERM', () => nested.once('exit', () => process.exit(0))); process.stdout.write(String(nested.pid) + '\\n'); setInterval(() => {}, 60000);",
  ], { detached: true, stdio: ["ignore", "pipe", "ignore"] });
  child.controlQaProcessGroup = true;
  await new Promise((resolveReady, reject) => {
    child.once("error", reject);
    child.stdout.once("data", resolveReady);
  });

  try {
    // When
    await stopChild(child);

    // Then
    expect(() => process.kill(-child.pid, 0)).toThrow();
  } finally {
    try {
      process.kill(-child.pid, "SIGKILL");
    } catch (error) {
      if (!(error instanceof Error && "code" in error && error.code === "ESRCH")) throw error;
    }
  }
});

test("failed and concurrent namespaced invocations cannot expose stale success", async () => {
  // Given
  const root = await mkdtemp(join(tmpdir(), "control-invocations-"));
  const malformed = join(root, "malformed.yaml");
  const malformedOutput = join(root, "malformed-output");
  const leftOutput = join(root, "task-left");
  const rightOutput = join(root, "task-right");
  const happy = join(repository, "tooling/fixtures/e2e/control-policy-happy.yaml");
  await writeFile(malformed, "scenario: malformed\n");
  await Promise.all([malformedOutput, leftOutput, rightOutput].map(async (output) => {
    await mkdir(output);
    await writeFile(join(output, "control-policy-happy.json"), '{"status":"passed"}\n');
    await writeFile(join(output, "stale.png"), "stale");
  }));

  try {
    // When
    const [invalid, left, right] = await Promise.all([
      importScenarioFixture(malformed, malformedOutput),
      importScenarioFixture(happy, leftOutput),
      importScenarioFixture(happy, rightOutput),
    ]);

    // Then
    expect(invalid.code).not.toBe(0);
    expect(invalid.stderr).toContain("unsupported Control QA scenario");
    expect(left.code, left.stderr).toBe(0);
    expect(right.code, right.stderr).toBe(0);
    for (const output of [malformedOutput, leftOutput, rightOutput]) {
      expect(await Bun.file(join(output, "control-policy-happy.json")).exists()).toBe(false);
      expect(await Bun.file(join(output, "stale.png")).exists()).toBe(false);
    }
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
