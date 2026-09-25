import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import path from "node:path";
import { Writable } from "node:stream";
import test from "node:test";
import { MAX_HELPER_LINE_BYTES, NativeAudioProcess, NativeProcessError, nativeHelperPath } from "../lib/native-process.mjs";

class FakeStream extends EventEmitter {
  writes = [];
  write(value, _encoding, callback) {
    this.writes.push(value);
    callback?.();
    return true;
  }
}

class FakeChild extends EventEmitter {
  stdin = new FakeStream();
  stdout = new FakeStream();
  stderr = new FakeStream();
  exitCode = null;
  signalCode = null;
  kill() {
    if (this.exitCode !== null || this.signalCode !== null) return;
    this.signalCode = "SIGTERM";
    this.emit("exit", null, this.signalCode);
  }
  reply(value) { this.stdout.emit("data", Buffer.from(`${JSON.stringify(value)}\n`)); }
}

function fixture() {
  const child = new FakeChild();
  const process = new NativeAudioProcess("C:\\app\\native-audio\\jastreamer-audio.exe", { spawn: () => child });
  return { child, process };
}

test("helper path is fixed for packaged resources and the repository development distribution", () => {
  assert.equal(
    nativeHelperPath({ isPackaged: true, resourcesPath: "C:\\portable\\resources", appDirectory: "ignored" }),
    path.join("C:\\portable\\resources", "native-audio", "jastreamer-audio.exe"),
  );
  assert.equal(
    nativeHelperPath({ isPackaged: false, resourcesPath: "ignored", appDirectory: "/repo/apps/desktop" }),
    path.join("/repo/apps/desktop", "native", "audio", "dist", "jastreamer-audio.exe"),
  );
});

test("request correlates one reply and ignores a stale duplicate id", async () => {
  const { child, process: helper } = fixture();
  const first = helper.request("status");
  const firstRequest = JSON.parse(child.stdin.writes[0]);
  child.reply({ id: firstRequest.id, ok: true, result: { state: "stopped" } });
  assert.deepEqual(await first, { state: "stopped" });

  child.reply({ id: firstRequest.id, ok: true, result: { state: "playing" } });
  const second = helper.request("devices");
  const secondRequest = JSON.parse(child.stdin.writes[1]);
  child.reply({ id: secondRequest.id, ok: true, result: { available: true, devices: [] } });
  assert.deepEqual(await second, { available: true, devices: [] });
});

test("request cancellation fences a late helper reply", async () => {
  const { child, process: helper } = fixture();
  const controller = new AbortController();
  const pending = helper.request("play", { play_id: "p", sequence: 1 }, { signal: controller.signal });
  const request = JSON.parse(child.stdin.writes[0]);
  controller.abort();
  await assert.rejects(pending, (error) => error.name === "AbortError");
  child.reply({ id: request.id, ok: true, result: { observation: { event: "playing" } } });
});

test("malformed or oversized stdout is terminal and rejects every pending command", async () => {
  for (const payload of [Buffer.from("not-json\n"), Buffer.alloc(MAX_HELPER_LINE_BYTES + 1, 0x61)]) {
    const { child, process: helper } = fixture();
    const pending = helper.request("status");
    child.stdout.emit("data", payload);
    await assert.rejects(pending, (error) => error instanceof NativeProcessError && error.code === "helper_protocol");
    assert.equal(helper.running, false);
  }
});

test("an oversized unterminated line after a valid reply is still terminal", async () => {
  const { child, process: helper } = fixture();
  const pending = helper.request("status");
  const request = JSON.parse(child.stdin.writes[0]);
  child.stdout.emit("data", Buffer.concat([
    Buffer.from(`${JSON.stringify({ id: request.id, ok: true, result: { state: "stopped" } })}\n`),
    Buffer.alloc(MAX_HELPER_LINE_BYTES + 1, 0x61),
  ]));
  assert.deepEqual(await pending, { state: "stopped" });
  assert.equal(helper.running, false);
});

test("a terminal helper starts a new process only after an explicit restart", async () => {
  const first = new FakeChild();
  const second = new FakeChild();
  const children = [first, second];
  const helper = new NativeAudioProcess("C:\\app\\native-audio\\jastreamer-audio.exe", {
    spawn: () => children.shift(),
  });
  const failed = helper.request("status");
  first.stdout.emit("data", Buffer.from("not-json\n"));
  await assert.rejects(failed, (error) => error.code === "helper_protocol");
  assert.equal(children.length, 1);

  helper.restart();
  const pending = helper.request("status");
  const request = JSON.parse(second.stdin.writes[0]);
  second.reply({ id: request.id, ok: true, result: { state: "stopped" } });
  assert.deepEqual(await pending, { state: "stopped" });
  assert.equal(children.length, 0);
});

test("helper error replies preserve only bounded safe public errors", async () => {
  const { child, process: helper } = fixture();
  const pending = helper.request("set_uri", {});
  const request = JSON.parse(child.stdin.writes[0]);
  child.reply({ id: request.id, ok: false, error: { code: "decoder.unsupported", message: "Unsupported stream" } });
  await assert.rejects(pending, (error) => error.code === "decoder.unsupported" && error.message === "Unsupported stream");
});

test("a broken helper pipe is contained and rejects outstanding commands", async () => {
  const { child, process: helper } = fixture();
  child.stdin = new Writable({
    write(_chunk, _encoding, callback) {
      queueMicrotask(() => callback(Object.assign(new Error("pipe closed"), { code: "EPIPE" })));
    },
  });
  let terminal;
  helper.once("terminal", (error) => { terminal = error; });
  const pending = helper.request("status");
  await assert.rejects(pending, (error) => error.code === "helper_write_failed");
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(terminal?.code, "helper_write_failed");
  assert.equal(helper.running, false);
  await assert.rejects(helper.request("status"), (error) => error.code === "helper_terminal");
});
