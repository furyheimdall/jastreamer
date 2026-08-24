import { describe, expect, test } from "bun:test";
import { X509Certificate } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { parseArgs } from "../tooling/args";
import { distributables } from "../tooling/finalize";
import { sourceIdentity } from "../tooling/identity";

describe("Server release contract", () => {
  test("prints release rehearsal help without building", async () => {
    const root = resolve(new URL("../../..", import.meta.url).pathname);
    const child = Bun.spawn(["./tooling/componentctl", "release", "dry-run", "--component", "server", "--help"], {
      cwd: root, stdout: "pipe", stderr: "pipe",
    });
    const [code, stdout, stderr] = await Promise.all([
      child.exited, new Response(child.stdout).text(), new Response(child.stderr).text(),
    ]);
    expect(code).toBe(0);
    expect(stdout).toContain("Usage: componentctl release dry-run");
    expect(stderr).toBe("");
  });
  test("accepts only the exact no-publish Server tag surface", () => {
    expect(parseArgs(["--component", "server", "--tag", "server-v1.2.3", "--no-publish", "--output", "out"]).version).toBe("1.2.3");
    expect(() => parseArgs(["--component", "control", "--tag", "control-v1.2.3", "--no-publish", "--output", "out"])).toThrow();
    expect(() => parseArgs(["--component", "server", "--tag", "server-v1.2", "--no-publish", "--output", "out"])).toThrow();
  });
  test("allowlists real native package formats and one OCI archive", () => {
    expect(distributables("1.2.3")).toEqual([
      "jastreamer-server_1.2.3_windows_amd64.exe", "jastreamer-server_1.2.3_windows_amd64.msi",
      "jastreamer-server_1.2.3_linux_amd64.deb", "jastreamer-server_1.2.3_linux_amd64.rpm",
      "jastreamer-server_1.2.3_linux_arm64.deb", "jastreamer-server_1.2.3_linux_arm64.rpm",
      "jastreamer-server_1.2.3_linux_amd64-arm64.oci",
    ]);
  });
  test("consumed music fixture mutation changes local source identity", () => {
    const root = resolve(new URL("../../..", import.meta.url).pathname);
    const fixture = join(root, "tooling/fixtures/music/real.wav.b64"); const original = readFileSync(fixture);
    const before = sourceIdentity(root); let after = before;
    try { writeFileSync(fixture, Buffer.concat([original, Buffer.from("\nidentity-mutation")])); after = sourceIdentity(root); }
    finally { writeFileSync(fixture, original); }
    expect(after).not.toBe(before); expect(sourceIdentity(root)).toBe(before);
  });
  test("injected native arm64 failure is atomic and leaves promotion unreachable", async () => {
    const directory = mkdtempSync(join(tmpdir(), "server-release-failure-")); const marker = join(directory, "marker");
    writeFileSync(marker, "immutable");
    try {
      const root = resolve(new URL("../../..", import.meta.url).pathname);
      const child = Bun.spawn(["./tooling/componentctl", "release", "dry-run", "--component", "server", "--tag", "server-v1.2.3", "--no-publish", "--output", directory, "--inject-failure", "native-linux-arm64-smoke"], { cwd: root, stdout: "pipe", stderr: "pipe" });
      const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text()]);
      expect(code).toBe(70); expect(readFileSync(marker, "utf8")).toBe("immutable");
      expect(stderr).toContain('"release":"unreachable"'); expect(stderr).toContain('"control":"passed"'); expect(stderr).toContain('"renderer":"passed"');
    } finally { rmSync(directory, { recursive: true, force: true }); }
  });
  test("MSI requires explicit TrustedPeople trust while allowing uninstall", () => {
    for (const source of ["server-local.wxs", "server.wxs"]) {
      const wix = readFileSync(new URL(`../${source}`, import.meta.url), "utf8");
      expect(wix).toContain("Installed OR TRUSTEDCERT"); expect(wix).toContain("TrustedPeople"); expect(wix).toContain("ServiceInstall");
    }
  });
  test("published Server certificate is a matching code-signing leaf", () => {
    const certificate = new X509Certificate(readFileSync(new URL("../cert/server.cer", import.meta.url)));
    const fingerprint = readFileSync(new URL("../cert/fingerprint.txt", import.meta.url), "utf8").trim();
    expect(certificate.subject).toBe("CN=jastreamer");
    expect(certificate.ca).toBe(false);
    expect(certificate.keyUsage).toContain("1.3.6.1.5.5.7.3.3");
    expect(fingerprint).toBe(`SHA256: ${certificate.fingerprint256}`);
  });
  test("Windows PFX loading and cleanup are noninteractive, ephemeral, and fail-safe", () => {
    const signing = readFileSync(new URL("../sign-windows.ps1", import.meta.url), "utf8");
    expect(signing).not.toContain("Get-PfxCertificate $pfx");
    expect(signing).not.toContain("Get-FileHash $Cer");
    expect(signing).toContain("CertificateSha256 $publishedCertificate");
    expect(signing).toContain("X509Certificate2"); expect(signing).toContain("WINDOWS_SIGNING_PFX_PASSWORD"); expect(signing).toContain("EphemeralKeySet");
    expect(signing.indexOf("Remove-Item $pfx -Force")).toBeLessThan(signing.lastIndexOf("AggregateException"));
    const workflow = readFileSync(new URL("../../../.github/workflows/server-release.yml", import.meta.url), "utf8");
    const cleanupStep = workflow.slice(workflow.indexOf("Prove no signing material remains"), workflow.indexOf("actions/upload-artifact", workflow.indexOf("Prove no signing material remains")));
    expect(cleanupStep).toContain("if: always()"); expect(cleanupStep).toContain("TrustedPeople"); expect(cleanupStep).toContain("JASTREAMER_SETUP_SECRET");
  });
  test("Windows signs embedded inputs before WiX and verifies extracted EXEs", () => {
    const signing = readFileSync(new URL("../sign-windows.ps1", import.meta.url), "utf8");
    expect(signing.indexOf("foreach ($exe in $sourceExecutables)")).toBeLessThan(signing.indexOf("build-windows-msi.ps1"));
    expect(signing.indexOf("build-windows-msi.ps1")).toBeLessThan(signing.indexOf("$env:JASTREAMER_SIGNTOOL sign /fd SHA256 /f $pfx /p $env:WINDOWS_SIGNING_PFX_PASSWORD $msi"));
    expect(signing).toContain("$extractedExecutables"); expect(signing).toContain("WaitForStatus"); expect(signing).toContain("/healthz");
    expect(readFileSync(new URL("../build-windows.ps1", import.meta.url), "utf8")).not.toContain(" wix build ");
  });
  test("Windows PFX loading and cleanup are unattended and fail-safe", () => {
    const signing = readFileSync(new URL("../sign-windows.ps1", import.meta.url), "utf8");
    const workflow = readFileSync(new URL("../../../.github/workflows/server-release.yml", import.meta.url), "utf8");
    expect(signing).toContain("X509Certificate2");
    expect(signing).toContain("EphemeralKeySet");
    expect(signing).not.toContain("Get-PfxCertificate $pfx");
    expect(signing).toContain("$cleanupFailures");
    expect(workflow).toContain("if: always()");
  });
  test("workflow installs exact release tools instead of minimum-only assertions", () => {
    const workflow = readFileSync(new URL("../../../.github/workflows/server-release.yml", import.meta.url), "utf8");
    const installer = readFileSync(new URL("../install-linux-tools.sh", import.meta.url), "utf8");
    expect(workflow).toContain("actions/setup-dotnet@67a3573c9a986a3f9c594539f4ab511d57bb3ce9");
    expect(workflow).toContain("docker/setup-docker-action@b60f85385d03ac8acfca6d9996982511d8620a19");
    expect(workflow).toContain("Microsoft.Windows.SDK.BuildTools -Version 10.0.26100.3916");
    expect(installer).toContain("gh_2.76.2_linux_$arch.tar.gz");
    expect(installer).toContain("jq-linux-$arch");
    expect(workflow).not.toContain("sort -V | head -1");
  });
  test("selects the exact .NET SDK installed for Windows packaging", () => {
    const root = resolve(new URL("../../..", import.meta.url).pathname);
    const configuration = JSON.parse(readFileSync(join(root, "global.json"), "utf8")) as {
      sdk?: { version?: string; rollForward?: string };
    };
    expect(configuration.sdk).toEqual({
      version: "8.0.419",
      rollForward: "disable",
    });
  });
  test("uses a Buildx container driver that supports OCI attestations", () => {
    const workflow = readFileSync(new URL("../../../.github/workflows/server-release.yml", import.meta.url), "utf8");
    const oci = workflow.slice(workflow.indexOf("  oci:"), workflow.indexOf("  stage:"));
    expect(oci).toContain("docker/setup-buildx-action@37fe631027851001ddb9b187196cc803df7f5f0e");
    expect(oci.indexOf("docker/setup-buildx-action@")).toBeLessThan(oci.indexOf("container build-qa"));
  });
  test("promotion refuses overwrite, checks digest, and never suppresses cleanup", () => {
    const workflow = readFileSync(new URL("../../../.github/workflows/server-release.yml", import.meta.url), "utf8");
    const promotion = workflow.slice(workflow.indexOf("  promote:"));
    expect(promotion).toContain("absent_release"); expect(promotion).toContain("absent_registry_tag");
    expect(promotion).toContain("gh release create"); expect(promotion).toContain("gh release upload");
    expect(promotion).toContain('test "$pushed_digest" = "$expected_digest"');
    expect(promotion).not.toContain("|| true"); expect(promotion).toContain("registry-auth-still-present");
  });
  test("promotion owns only resources it creates and mounts the staged OCI archive", () => {
    const workflow = readFileSync(new URL("../../../.github/workflows/server-release.yml", import.meta.url), "utf8");
    const promotion = workflow.slice(workflow.indexOf("  promote:"));
    expect(promotion).toContain("actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683");
    expect(promotion).toContain('-v "$GITHUB_WORKSPACE/stage:/stage:ro"');
    expect(promotion).toContain("release_created=false");
    expect(promotion).toContain("image_promotion_started=false");
    expect(promotion).toContain("[[ $release_created == true ]]");
    expect(promotion).toContain("[[ $image_promotion_started == true");
  });
  test("workflow is canonical, protected, pinned, and has no OIDC", () => {
    const workflow = readFileSync(new URL("../../../.github/workflows/server-release.yml", import.meta.url), "utf8");
    expect(workflow).toContain("furyheimdall/jastreamer");
    expect(workflow).not.toContain("id-token:");
    expect(workflow).not.toContain("sort -V");
    expect(workflow).toContain("actions/setup-dotnet@"); expect(workflow).toContain("dotnet-version: '8.0.419'");
    expect(workflow).not.toMatch(/uses:\s+[^\s@]+@(?![0-9a-f]{40})/);
  });
});
