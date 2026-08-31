import { describe, expect, test } from "bun:test";
import { isGitStatusClean } from "./scope-audit-status.mjs";

describe("final scope audit Git status detection", () => {
  test("treats successful git status output as dirty", () => {
    expect(isGitStatusClean("")).toBe(true);
    expect(isGitStatusClean(" M tooling/final/scope-audit.mjs\n")).toBe(false);
  });
});
