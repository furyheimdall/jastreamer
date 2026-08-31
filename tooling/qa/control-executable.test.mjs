import { expect, test } from "bun:test";
import { spawnSync } from "node:child_process";
import { constants } from "node:fs";
import { access, readFile, readdir, stat } from "node:fs/promises";
import { join, resolve } from "node:path";

const root = resolve(import.meta.dirname, "../..");

test("Control QA keeps its direct executable surface", async () => {
  const path = join(root, "tooling/qa/control.sh");
  await access(path, constants.X_OK);
  expect((await stat(path)).mode & 0o111).not.toBe(0);

  const direct = spawnSync(join(root, "tooling/componentctl"), ["qa", "control"], {
    cwd: root,
    encoding: "utf8",
  });
  expect(direct.status).toBe(64);
  expect(direct.status).not.toBe(126);
});

test("Control browser scenario is split into focused modules", async () => {
  const modules = [
    "control.playwright.mjs",
    "control-playwright-actions.mjs",
    "control-scenario-fixture.mjs",
    "control-happy-flow.mjs",
    "control-failure-flow.mjs",
  ];
  for (const module of modules) {
    const path = join(root, "tooling/qa", module);
    const source = await readFile(path, "utf8");
    const pure = source.split("\n").filter((line) => line.trim() !== "" && !line.trim().startsWith("//")).length;
    expect(pure, module).toBeLessThanOrEqual(250);
    expect(spawnSync("node", ["--check", path]).status, module).toBe(0);
  }
});

test("Playwright entrypoints stay outside Bun unit-test discovery", async () => {
  const entries = await readdir(join(root, "tooling/qa"));
  expect(entries.filter((entry) => entry.endsWith(".spec.mjs"))).toEqual([]);
  expect(entries.filter((entry) => entry.endsWith(".playwright.mjs")).sort()).toEqual([
    "admin-restart.playwright.mjs",
    "admin.playwright.mjs",
    "control.playwright.mjs",
    "pairing-api.playwright.mjs",
  ]);
});
