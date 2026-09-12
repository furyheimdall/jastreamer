import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { MAX_RECENT_SERVERS, RecentServerStore } from "../lib/storage.mjs";

const SERVER_ID = "11111111-1111-4111-8111-111111111111";

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
