import { expect, test } from "bun:test";
import { cp, mkdtemp, readFile, rm } from "node:fs/promises";
import { join } from "node:path";

const root = join(import.meta.dir, "..");
const run = (args: string[], contractsRoot = root) => Bun.spawnSync(["./tooling/componentctl", "contracts", ...args], { env: { ...process.env, CONTRACTS_ROOT: contractsRoot } });

test("locks name immutable v2 archives for each component", async () => {
  for (const name of ["control-api", "renderer-protocol"]) {
    expect(await Bun.file(join(root, name, "archived/v1/schema.json")).exists()).toBe(true);
    expect(await Bun.file(join(root, name, "archived/v2/schema.json")).exists()).toBe(true);
  }
  expect(run(["locks"]).exitCode).toBe(0);
});

test("repeat generation detects drift without changing archives", async () => {
  const before = await readFile(join(root, "control-api/archived/v2/schema.json"));
  expect(run(["generate", "--check", "--repeat", "2"]).exitCode).toBe(0);
  expect(await readFile(join(root, "control-api/archived/v2/schema.json"))).toEqual(before);
});

test("generation ignores whitespace but rejects semantic drift", async () => {
  const directory = await mkdtemp(join("/tmp", "contracts-task-3-"));
  try {
    await cp(root, directory, { recursive: true });
    const schema = join(directory, "control-api/schema.json");
    const original = await readFile(schema, "utf8");
    await Bun.write(schema, `${original}\n`);
    expect(run(["generate", "--check", "--repeat", "2"], directory).exitCode).toBe(0);
    const value = JSON.parse(original) as { properties: Record<string, unknown> };
    value.properties.added = { type: "string" };
    await Bun.write(schema, JSON.stringify(value));
    expect(run(["generate", "--check", "--repeat", "2"], directory).exitCode).toBe(1);
  } finally { await rm(directory, { recursive: true, force: true }); }
});

test("compatibility parses copied matrix fixtures", async () => {
  const directory = await mkdtemp(join("/tmp", "contracts-task-3-"));
  try {
    const matrix = join(directory, "supported.yaml");
    await cp(join(root, "fixtures/compatibility/supported-majors.yaml"), matrix);
    const output = join(directory, "result.json");
    expect(run(["compatibility", "--matrix", matrix, "--output", output]).exitCode).toBe(0);
    expect(JSON.parse(await readFile(output, "utf8"))).toEqual({ compatible: true, commonMajors: [2, 1] });
  } finally { await rm(directory, { recursive: true, force: true }); }
});

test("breaking and incompatible fixtures return contract exit 65", () => {
  expect(run(["breaking", "--fixture", join(root, "fixtures/breaking/required-field-without-major.json")]).exitCode).toBe(65);
  expect(run(["compatibility", "--matrix", join(root, "fixtures/compatibility/invalid/no-common-major.yaml")]).exitCode).toBe(65);
});
