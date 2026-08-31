import { describe, expect, test } from "bun:test";
import { materializePrivateDirectory, type PrivateMaterializationAdapter } from "./task19-private-materializer";

const entries = { "nested/candidate.bin": Uint8Array.from([1, 2, 3]) };
const adapter = (events: string[], failAt?: string): PrivateMaterializationAdapter => ({
  destinationExists: () => false,
  assertNoReparseComponents: (path) => { events.push(`paths:${path}`); if (failAt === "paths") throw new Error("REPARSE"); },
  createPrivateSibling: () => { events.push("create"); return "/root/.stage"; },
  queryRunnerSid: () => { events.push("sid"); return "S-1-5-21-1000"; },
  applyPrivateAcl: () => { events.push("acl"); if (failAt === "acl") throw new Error("ACL"); },
  verifyPrivateAcl: () => { events.push("verify-acl"); },
  createDirectory: (path) => { events.push(`mkdir:${path}`); },
  createFileExclusive: (path) => { events.push(`write:${path}`); if (failAt === "write") throw new Error("WRITE"); },
  readFile: () => failAt === "final" && events.includes("rename") ? Uint8Array.from([9]) : Uint8Array.from([1, 2, 3]),
  listFiles: () => failAt === "inventory" ? ["nested/candidate.bin", "injected.bin"] : ["nested/candidate.bin"],
  renameAbsent: () => { events.push("rename"); if (failAt === "rename") throw new Error("RENAME"); },
  removeTree: (path) => { events.push(`remove:${path}`); },
});

describe("Task19 private all-or-nothing materializer", () => {
  test("applies and verifies the independently queried DACL before the first write then rehashes final names", () => {
    const events: string[] = []; materializePrivateDirectory("/root/final", entries, adapter(events));
    expect(events.indexOf("sid")).toBeLessThan(events.findIndex((value) => value.startsWith("write:")));
    expect(events.indexOf("verify-acl")).toBeLessThan(events.findIndex((value) => value.startsWith("write:")));
    expect(events.at(-1)).toBe("paths:/root/final/nested/candidate.bin");
  });

  test.each(["acl", "write", "inventory", "rename", "final"])("removes staging or committed destination and leaves no partial output when %s fails", (phase) => {
    const events: string[] = []; expect(() => materializePrivateDirectory("/root/final", entries, adapter(events, phase))).toThrow();
    expect(events).toContain(`remove:${phase === "final" ? "/root/final" : "/root/.stage"}`);
  });

  test("rejects a reparse parent before creating staging", () => {
    const events: string[] = []; expect(() => materializePrivateDirectory("/root/final", entries, adapter(events, "paths"))).toThrow("REPARSE");
    expect(events).toEqual(["paths:/root"]);
  });
});
