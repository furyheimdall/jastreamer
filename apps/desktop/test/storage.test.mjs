import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import {
  LanguagePreferenceStore,
  MAX_RECENT_SERVERS,
  RecentServerStore,
  resolveUserDataPath,
} from "../lib/storage.mjs";

const SERVER_ID = "11111111-1111-4111-8111-111111111111";

test("uses portable Windows storage and per-user installed Linux storage", () => {
  const portableExecutable = path.resolve("portable", "jastreamer-desktop.exe");
  const windowsDataPath = resolveUserDataPath({
    isPackaged: true,
    platform: "win32",
    executablePath: portableExecutable,
    appDirectory: path.resolve("source"),
    appDataPath: path.resolve("ignored-app-data"),
  });
  assert.equal(windowsDataPath, path.join(path.dirname(portableExecutable), "user-data"));

  assert.equal(resolveUserDataPath({
    isPackaged: true,
    platform: "linux",
    executablePath: path.resolve("usr", "lib", "jastreamer-desktop", "jastreamer-desktop"),
    appDirectory: path.resolve("usr", "lib", "jastreamer-desktop", "resources", "app.asar"),
    appDataPath: path.resolve("home", "listener", ".config"),
  }), path.resolve("home", "listener", ".config", "jastreamer-desktop"));

  assert.equal(resolveUserDataPath({
    isPackaged: false,
    platform: "linux",
    executablePath: path.resolve("workspace", "node_modules", "electron", "dist", "electron"),
    appDirectory: path.resolve("workspace", "apps", "desktop"),
  }), path.resolve("workspace", "apps", "desktop", ".user-data"));
});

test("requires an OS per-user data root for installed non-Windows builds", () => {
  assert.throws(() => resolveUserDataPath({
    isPackaged: true,
    platform: "linux",
    executablePath: "/usr/lib/jastreamer-desktop/jastreamer-desktop",
    appDirectory: "/usr/lib/jastreamer-desktop/resources/app.asar",
  }), /appDataPath/);
});

test("persists only bounded normalized non-secret recent metadata", async (context) => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "jastreamer-recents-"));
  context.after(() => rm(directory, { recursive: true, force: true }));
  const store = new RecentServerStore(directory);
  await store.load();

  for (let index = 0; index < MAX_RECENT_SERVERS + 3; index += 1) {
    await store.upsert({
      id: index === 0 ? SERVER_ID : `${String(index).padStart(8, "0")}-1111-4111-8111-111111111111`,
      origin: `http://media-box.local:${8_000 + index}/`,
      name: `서버 ${index}`,
      version: "2.4.0",
      password: "must-not-persist",
      cookie: "must-not-persist",
    });
  }

  const recents = store.list();
  assert.equal(recents.length, MAX_RECENT_SERVERS);
  const persisted = await readFile(path.join(directory, "recents.json"), "utf8");
  assert.equal(persisted.includes("must-not-persist"), false);
  assert.deepEqual(Object.keys(JSON.parse(persisted).recents[0]).sort(), [
    "id",
    "lastUsed",
    "name",
    "origin",
    "version",
  ]);
});

test("deduplicates only the same identity and normalized origin", async (context) => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "jastreamer-recents-"));
  context.after(() => rm(directory, { recursive: true, force: true }));
  const store = new RecentServerStore(directory);
  await store.load();

  await store.upsert({ id: SERVER_ID, origin: "http://media-box.local:8080", name: "이전 이름", version: "1.0" });
  await store.upsert({ id: SERVER_ID, origin: "http://media-box.local:8080/", name: "새 이름", version: "2.0" });
  await store.upsert({ id: SERVER_ID, origin: "http://media-box.local:8081", name: "다른 포트", version: "2.0" });

  assert.equal(store.list().length, 2);
  assert.equal(store.list()[0].origin, "http://media-box.local:8081");
  assert.equal(store.list()[1].name, "새 이름");
});

test("persists the validated language beside recent metadata without changing it", async (context) => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "jastreamer-language-"));
  context.after(() => rm(directory, { recursive: true, force: true }));
  const recents = new RecentServerStore(directory);
  await recents.load();
  await recents.upsert({ id: SERVER_ID, origin: "http://media-box.local:8080", name: "Music", version: "2.0" });

  const preference = new LanguagePreferenceStore(directory);
  assert.equal(await preference.load(), "en");
  assert.equal(await preference.set("ko"), "ko");

  const restartedPreference = new LanguagePreferenceStore(directory);
  assert.equal(await restartedPreference.load(), "ko");
  assert.equal((await recents.load())[0].name, "Music");
  await assert.rejects(preference.set("fr"), TypeError);
});
