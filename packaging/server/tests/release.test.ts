import { describe, expect, test } from "bun:test";
import { X509Certificate } from "node:crypto";
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
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
  test("keeps externally supplied FFmpeg outside the image on an opt-in read-only mount", () => {
    const root = resolve(new URL("../../..", import.meta.url).pathname);
    const override = readFileSync(join(root, "deploy/docker/server/compose.ffmpeg.yaml"), "utf8");
    const image = readFileSync(join(root, "apps/server/Dockerfile"), "utf8");
    expect(override).toContain('target: /opt/jastreamer-external/ffmpeg');
    expect(override).toContain("read_only: true");
    expect(image).not.toMatch(/apk add[^\n]*ffmpeg|COPY[^\n]*ffmpeg/i);
  });
  test("allowlists real native package formats and one OCI archive", () => {
    expect(distributables("1.2.3")).toEqual([
      "jastreamer-server_1.2.3_windows_amd64.exe", "jastreamer-server_1.2.3_windows_amd64.msi",
      "jastreamer-server_1.2.3_linux_amd64.deb", "jastreamer-server_1.2.3_linux_amd64.rpm",
      "jastreamer-server_1.2.3_linux_arm64.deb", "jastreamer-server_1.2.3_linux_arm64.rpm",
      "jastreamer-server_1.2.3_linux_amd64-arm64.oci",
    ]);
  });
  test("consumed music fixture mutation changes an isolated source identity", () => {
    const repository = resolve(new URL("../../..", import.meta.url).pathname);
    const root = mkdtempSync(join(tmpdir(), "server-source-identity-"));
    const fixture = join(root, "tooling/fixtures/music/real.wav.b64");
    try {
      mkdirSync(join(root, "tooling/fixtures/music"), { recursive: true });
      cpSync(join(repository, "tooling/fixtures/music/real.wav.b64"), fixture);
      const before = sourceIdentity(root, ["tooling/fixtures/music"]);
      writeFileSync(fixture, Buffer.concat([readFileSync(fixture), Buffer.from("\nidentity-mutation")]));
      expect(sourceIdentity(root, ["tooling/fixtures/music"])).not.toBe(before);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });
  test("concurrent source identity checks never mutate the tracked fixture", async () => {
    const repository = resolve(new URL("../../..", import.meta.url).pathname);
    const fixture = join(repository, "tooling/fixtures/music/real.wav.b64");
    const before = readFileSync(fixture);
    await Promise.all(Array.from({ length: 8 }, async () => {
      const root = mkdtempSync(join(tmpdir(), "server-source-concurrent-"));
      try {
        mkdirSync(join(root, "tooling/fixtures/music"), { recursive: true });
        cpSync(fixture, join(root, "tooling/fixtures/music/real.wav.b64"));
        sourceIdentity(root, ["tooling/fixtures/music"]);
      } finally {
        rmSync(root, { recursive: true, force: true });
      }
    }));
    expect(readFileSync(fixture)).toEqual(before);
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
      expect(wix).toContain("004_server_state.sql");
      expect(wix).toContain("005_renderer_sessions.sql");
      expect(wix).toContain("006_transport_mutations.sql");
      expect(wix).toContain("007_previous_history.sql");
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
    expect(signing).not.toContain("CreateFromPemFile");
    expect(signing).toContain("[Security.Cryptography.X509Certificates.X509Certificate2]::new((Resolve-Path $Cer).Path)");
    expect(signing).toContain("CertificateSha256 $publishedCertificate");
    expect(signing).toContain("X509Certificate2"); expect(signing).toContain("WINDOWS_SIGNING_PFX_PASSWORD"); expect(signing).toContain("EphemeralKeySet");
    expect(signing.indexOf("Remove-Item $pfx -Force")).toBeLessThan(signing.lastIndexOf("AggregateException"));
    const workflow = readFileSync(new URL("../../../.github/workflows/server-release.yml", import.meta.url), "utf8");
    const cleanupStep = workflow.slice(workflow.indexOf("Prove no signing material remains"), workflow.indexOf("actions/upload-artifact", workflow.indexOf("Prove no signing material remains")));
    expect(cleanupStep).toContain("if: always()"); expect(cleanupStep).toContain("TrustedPeople"); expect(cleanupStep).toContain("JASTREAMER_SETUP_SECRET");
    expect(cleanupStep).toContain("if (Test-Path dist)");
  });
  test("Windows signs embedded inputs before WiX and verifies extracted EXEs", () => {
    const signing = readFileSync(new URL("../sign-windows.ps1", import.meta.url), "utf8");
    const serviceHost = readFileSync(new URL("../windows-service.go", import.meta.url), "utf8");
    expect(signing.indexOf("foreach ($exe in $sourceExecutables)")).toBeLessThan(signing.indexOf("build-windows-msi.ps1"));
    expect(signing.indexOf("build-windows-msi.ps1")).toBeLessThan(signing.indexOf("$env:JASTREAMER_SIGNTOOL sign /fd SHA256 /f $pfx /p $env:WINDOWS_SIGNING_PFX_PASSWORD $msi"));
    expect(signing).not.toContain("Get-ChildItem $extract -Recurse -Filter '*.exe'");
    expect(signing).toContain("@('ServerExe', 'ServerCoreExe')");
    expect(signing).not.toContain("SelectSingleNode");
    expect(signing).toContain("Where-Object Name -eq $id");
    expect(signing).toContain('$msi = (Resolve-Path "$Directory/jastreamer-server_${Version}_windows_amd64.msi").Path');
    expect(signing).toContain("[ServiceProcess.ServiceControllerStatus]::Running, [TimeSpan]::FromSeconds(90)");
    expect(signing).toContain("service readiness failed:");
    expect(signing).toContain("service.log");
    expect(serviceHost).toContain('"JASTREAMER_CATALOG_ROOT="+filepath.Join(dataDirectory, "catalog")');
    expect(serviceHost).toContain("command.Stderr = serviceLog");
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
    expect(workflow).toContain("Run native Windows security tests");
    expect(workflow).toContain("go test ./internal/security");
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
    expect(oci).toContain("docker/setup-qemu-action@96fe6ef7f33517b61c61be40b68a1882f3264fb8");
    expect(oci).toContain("docker/setup-buildx-action@37fe631027851001ddb9b187196cc803df7f5f0e");
    expect(oci.indexOf("docker/setup-qemu-action@")).toBeLessThan(oci.indexOf("docker/setup-buildx-action@"));
    expect(oci.indexOf("docker/setup-buildx-action@")).toBeLessThan(oci.indexOf("container build-qa"));
  });
  test("workflow publishes only exact prequalified Server bytes behind Todo 22", () => {
    // Given: the complete Server candidate and publication workflow.
    const workflow = readFileSync(new URL("../../../.github/workflows/server-release.yml", import.meta.url), "utf8");

    // When: candidate and final-job permission surfaces are isolated.
    const publishAt = workflow.indexOf("  publish-qualified:");
    const candidate = workflow.slice(0, publishAt);
    const publish = workflow.slice(publishAt);

    // Then: staging stays read-only and the protected final job uses the typed exact-byte driver.
    expect(candidate).not.toContain("contents: write");
    expect(candidate).not.toContain("packages: write");
    expect(publish).toContain("contents: write");
    expect(publish).toContain("packages: write");
    expect(publish).toContain("environment: product-promotion");
    expect(publish).toContain("publication-cli.ts");
    expect(publish).toContain("artifact-ids:");
    expect(workflow).toContain("server-publication-stage");
    expect(workflow).toContain("candidate.json");
    expect(workflow).toContain("promotionReady:false");
  });
  test("workflow dispatch and protected tags share the exact candidate pipeline", () => {
    // Given: the Server candidate workflow.
    const workflow = readFileSync(new URL("../../../.github/workflows/server-release.yml", import.meta.url), "utf8");

    // When: trigger and staged identity handling are inspected.
    const stage = workflow.slice(workflow.indexOf("  stage:"));

    // Then: dispatch supplies only a version and stage records exact digests with no writes.
    expect(workflow).toContain("workflow_dispatch:");
    expect(workflow).toContain("workflow_call:");
    expect(workflow).toContain("candidate_tag=\"server-v$CANDIDATE_VERSION\"");
    expect(stage).toContain("manifest_sha256");
    expect(stage).toContain("artifact_set_sha256");
    expect(stage).toContain("external_writes:[]");
  });
  test("binds the complete K17 emulator matrix to the staged manifest", () => {
    const workflow = readFileSync(new URL("../../../.github/workflows/server-release.yml", import.meta.url), "utf8");
    const validate = workflow.slice(workflow.indexOf("  validate:"), workflow.indexOf("  linux:"));
    const stage = workflow.slice(workflow.indexOf("  stage:"), workflow.indexOf("  k17-physical:"));

    expect(validate).not.toContain("k17-emulator-matrix.json");
    expect(stage).toContain("actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16");
    expect(stage).toContain("candidate_sha256=$(sha256sum stage/manifest.json");
    expect(stage).toContain("bun tooling/qa/k17/cli.mjs emulator");
    expect(stage).toContain("name: k17-emulator-matrix");
    expect(stage.indexOf("manifest.json")).toBeLessThan(stage.indexOf("Run manifest-bound deterministic K17 emulator matrix"));
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
