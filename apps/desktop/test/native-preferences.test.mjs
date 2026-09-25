import assert from "node:assert/strict";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { NativePreferenceStore } from "../lib/native-preferences.mjs";

test("native backend, endpoint, exclusive mode and app-local volume persist under user-data", async (context) => {
  const directory = await mkdtemp(path.join(tmpdir(), "jastreamer-native-preferences-"));
  context.after(() => rm(directory, { recursive: true, force: true }));
  const store = new NativePreferenceStore(directory);
  assert.deepEqual(await store.load(), { enabled: false, device_id: "default", exclusive: false, volume: 1 });
  await store.set({ enabled: true, device_id: "opaque-endpoint-id", exclusive: true, volume: 0.375 });

  const restored = new NativePreferenceStore(directory);
  assert.deepEqual(await restored.load(), {
    enabled: true,
    device_id: "opaque-endpoint-id",
    exclusive: true,
    volume: 0.375,
  });
  const onDisk = JSON.parse(await readFile(path.join(directory, "native-audio.json"), "utf8"));
  assert.equal(onDisk.version, 1);
  assert.equal(onDisk.device_id, "opaque-endpoint-id");
});

test("corrupt preferences fail closed to browser output without substituting a fixed endpoint", async (context) => {
  const directory = await mkdtemp(path.join(tmpdir(), "jastreamer-native-preferences-"));
  context.after(() => rm(directory, { recursive: true, force: true }));
  const file = path.join(directory, "native-audio.json");
  await writeFile(file, "{broken", "utf8");
  const store = new NativePreferenceStore(directory);
  assert.deepEqual(await store.load(), { enabled: false, device_id: "default", exclusive: false, volume: 1 });
});

test("an invalid persisted fixed endpoint disables native output instead of substituting default", async (context) => {
  const directory = await mkdtemp(path.join(tmpdir(), "jastreamer-native-preferences-"));
  context.after(() => rm(directory, { recursive: true, force: true }));
  await writeFile(
    path.join(directory, "native-audio.json"),
    JSON.stringify({ version: 1, enabled: true, device_id: "fixed\nendpoint", exclusive: true, volume: 0.2 }),
    "utf8",
  );
  const store = new NativePreferenceStore(directory);
  assert.deepEqual(await store.load(), { enabled: false, device_id: "default", exclusive: false, volume: 1 });
});
