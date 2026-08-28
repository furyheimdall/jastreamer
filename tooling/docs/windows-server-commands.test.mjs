import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dirname, "..", "..");

describe("Windows Server service command contract", () => {
  test("uses the valid idempotent MultiString registry operation for the WiX service", () => {
    // Given: the machine-consumed operator command contract and WiX service definition.
    const commands = JSON.parse(readFileSync(resolve(root, "tooling/docs/windows-server-commands.json"), "utf8"));
    const wix = readFileSync(resolve(root, "packaging/server/server.wxs"), "utf8");
    const serviceName = wix.match(/<ServiceInstall[^>]* Name="([^"]+)"/)?.[1];

    // When: registry setup and service startup are resolved.
    const registry = commands.environment_registry;

    // Then: PowerShell receives its actual New-ItemProperty parameter names and the exact WiX service.
    expect(registry).toEqual({
      command: "New-ItemProperty",
      path: `HKLM:\\SYSTEM\\CurrentControlSet\\Services\\${serviceName}`,
      name: "Environment",
      property_type: "MultiString",
      force: true,
    });
    expect(commands.startup).toEqual({ command: "Start-Service", name: serviceName });
  });
});
