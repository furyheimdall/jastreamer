import { test, expect } from "bun:test";
import { existsSync, readFileSync } from "node:fs";

const root = new URL("..", import.meta.url).pathname;
const required = ["config.json", "build-msi.ps1", "build-portable-zip.ps1", "sign.ps1", "verify.ps1", "README.md"];

test("renderer release contract has the required isolated assets", () => {
  for (const name of required) expect(existsSync(`${root}${name}`)).toBe(true);
  const config = JSON.parse(readFileSync(`${root}config.json`, "utf8"));
  expect(config.artifacts).toEqual(["msi", "diagnostic-zip"]);
  expect(config.protocol.requiredCapabilities).toEqual(["render"]);
  expect(config.wix.redistribution).toBe(false);
  expect(config.signing.oidc).toBe(false);
  const wix = readFileSync(`${root}renderer.wxs`, "utf8");
  expect(wix).toContain('Platform="x64"');
});

test("workflow is protected, pinned, and does not request OIDC", () => {
  const workflow = readFileSync(new URL("../../../.github/workflows/renderer-release.yml", import.meta.url), "utf8");
  const release = readFileSync(`${root}release.ps1`, "utf8");
  expect(workflow).toContain("renderer-v");
  expect(workflow).toContain("RENDERER_WINDOWS_SIGNING_PFX_B64");
  expect(workflow).toContain("permissions:");
  expect(workflow).toContain("rustup toolchain install 1.89.0");
  expect(workflow).toContain("Microsoft.Windows.SDK.BuildTools.10.0.26100.3916");
  expect(workflow).toContain("JASTREAMER_SIGNTOOL");
  expect(workflow).toContain("$LASTEXITCODE");
  expect(workflow).toContain("windows-msi-inspection.json");
  expect(workflow).toContain("certificate.cer");
  expect(workflow).not.toContain("id-token:");
  expect(release).toContain("--target-dir target");
});
