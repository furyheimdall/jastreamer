import { existsSync, lstatSync, mkdtempSync, mkdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { describe, expect, test } from "bun:test";

const execute = (rendererRoot: string, k17: string, outputRoot: string) => Bun.spawnSync([
  "bun",
  resolve(import.meta.dirname, "task19-provider-materialize-cli.ts"),
  "--renderer-input-root",
  rendererRoot,
  "--k17-input",
  k17,
  "--output-root",
  outputRoot,
]);

const fixture = (): Readonly<{ root: string; rendererRoot: string; k17: string; outputRoot: string }> => {
  const root = mkdtempSync(join(tmpdir(), "task19-provider-materialize-"));
  const rendererRoot = join(root, "inputs", "renderer");
  mkdirSync(rendererRoot, { recursive: true, mode: 0o700 });
  writeFileSync(join(rendererRoot, "manifest.json"), "{}", { mode: 0o400 });
  writeFileSync(join(rendererRoot, "jastreamer-renderer_1.2.3_windows_amd64.msi"), "renderer", { mode: 0o400 });
  writeFileSync(join(rendererRoot, "windows-audio-qualification.json"), "{}", { mode: 0o400 });
  const k17 = join(root, "inputs", "k17", "k17-qualification.json");
  mkdirSync(join(root, "inputs", "k17"), { recursive: true, mode: 0o700 });
  writeFileSync(k17, "{}", { mode: 0o400 });
  return { root, rendererRoot, k17, outputRoot: join(root, "physical") };
};

const withFixture = (body: (value: ReturnType<typeof fixture>) => void): void => {
  const value = fixture();
  try { body(value); } finally { rmSync(value.root, { recursive: true, force: true }); }
};

describe("Task19 complete physical provider materialization CLI", () => {
  test("materializes Renderer WASAPI and K17 together into one private physical root", () => withFixture((value) => {
    const result = execute(value.rendererRoot, value.k17, value.outputRoot);
    expect(result.exitCode).toBe(0);
    expect(readFileSync(join(value.outputRoot, "k17-qualification.json"), "utf8")).toBe("{}");
    expect(lstatSync(value.outputRoot).mode & 0o077).toBe(0);
    expect(lstatSync(join(value.outputRoot, "manifest.json")).mode & 0o077).toBe(0);
  }));

  test("missing K17 input leaves zero final bytes", () => withFixture((value) => {
    rmSync(value.k17);
    const result = execute(value.rendererRoot, value.k17, value.outputRoot);
    expect(result.exitCode).toBe(77);
    expect(existsSync(value.outputRoot)).toBe(false);
  }));

  test("reparse input component is rejected before final visibility", () => withFixture((value) => {
    const outside = join(value.root, "outside");
    writeFileSync(outside, "attacker");
    symlinkSync(outside, join(value.rendererRoot, "extra-link"));
    const result = execute(value.rendererRoot, value.k17, value.outputRoot);
    expect(result.exitCode).toBe(77);
    expect(existsSync(value.outputRoot)).toBe(false);
  }));
});
