import { describe, expect, test } from "bun:test";
import { secureWindowsSnapshot, validateSnapshotAcl } from "./windows-snapshot-acl.mjs";

const trusted = { owner: "S-1-5-21-1000", protected: true, rules: [
  { sid: "S-1-5-21-1000", type: "Allow", inherited: false, rights: "FullControl" },
  { sid: "S-1-5-18", type: "Allow", inherited: false, rights: "FullControl" },
  { sid: "S-1-5-32-544", type: "Allow", inherited: false, rights: "FullControl" },
] };

describe("Task19 Windows private snapshot ACL adapter", () => {
  test("disables inheritance and verifies only runner System and Administrators before use", () => {
    const calls = [];
    const execute = (script, path) => { calls.push({ script, path }); return { status: 0, stdout: calls.length === 1 ? trusted.owner : calls.length === 3 ? JSON.stringify(trusted) : "" }; };
    expect(secureWindowsSnapshot("C:\\task19", { platform: "win32", execute })).toEqual({ ok: true, applied: true, runnerSid: trusted.owner });
    expect(calls).toHaveLength(3); expect(calls[1].script).toContain("SetAccessRuleProtection($true, $false)"); expect(calls[2].path).toBe("C:\\task19");
  });

  test.each([
    ["inherited access", { ...trusted, rules: trusted.rules.map((rule, index) => index === 0 ? { ...rule, inherited: true } : rule) }],
    ["writable untrusted principal", { ...trusted, rules: [...trusted.rules, { sid: "S-1-1-0", type: "Allow", inherited: false, rights: "Write, Read" }] }],
    ["unprotected ACL", { ...trusted, protected: false }],
    ["attacker owner", { ...trusted, owner: "S-1-5-21-ATTACKER", rules: trusted.rules.map((rule, index) => index === 0 ? { ...rule, sid: "S-1-5-21-ATTACKER" } : rule) }],
  ])("rejects %s", (_name, acl) => expect(validateSnapshotAcl(acl, trusted.owner)).toMatchObject({ ok: false }));

  test("workflow contract requires a protected Windows identity", async () => {
    const workflow = await Bun.file(new URL("../../../.github/workflows/task19-installed-qualification.yml", import.meta.url)).text();
    const qualify = Bun.YAML.parse(workflow).jobs.qualify;
    expect(qualify["runs-on"]).toEqual(["self-hosted", "Windows", "X64", "task19-protected"]);
  });
});
