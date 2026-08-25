import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { validateControlRelease } from "../validate";
describe("Control release policy", () => {
  test("rejects a public AAB and signing lineage change", () => {
    const result = validateControlRelease({ publicAssets: ["jastreamer-control-0.1.0.apk", "jastreamer-control-0.1.0.aab"], applicationId: "io.jastreamer.control", signingLineage: "changed", protectedSigningMaterial: true });
    expect(result.errors).toEqual(["FORBIDDEN_AAB_ASSET", "SIGNING_LINEAGE_CHANGED"]);
  });
  test("requires the protected project keystore for Android release builds", () => {
    const gradle = readFileSync(new URL("../../../apps/control/android/app/build.gradle.kts", import.meta.url), "utf8");
    expect(gradle).toContain("CONTROL_ANDROID_KEYSTORE");
    expect(gradle).toContain("CONTROL_ANDROID_STORE_PASSWORD");
    expect(gradle).toContain("CONTROL_ANDROID_KEY_ALIAS");
    expect(gradle).toContain("CONTROL_ANDROID_KEY_PASSWORD");
    expect(gradle).not.toContain('signingConfig = signingConfigs.getByName("debug")');
  });

  test("uses the pinned amd64 Flutter image required by the Android NDK", () => {
    const release = readFileSync(
      new URL("../release.sh", import.meta.url),
      "utf8",
    );
    expect(release).toContain(
      "sha256:6260e72570abf56db2d2e3ce5520453e996f14cb2a29131535743d568d424639",
    );
    expect(release).toContain("--platform linux/amd64");
    expect(release).not.toContain(
      "sha256:e33916141978e14e3af996f08feece1a380173a60f6f16a0ecf41b5c42b6363d",
    );
  });

  test("pins a Flutter stable version available on every release platform", () => {
    const workflow = readFileSync(
      new URL("../../../.github/workflows/control-release.yml", import.meta.url),
      "utf8",
    );
    expect(workflow.match(/flutter-version: '3\.35\.1'/g)?.length).toBe(4);
    expect(workflow).not.toContain("flutter-version: '3.35.0'");
    const cleanup = workflow.slice(
      workflow.indexOf("Prove no Windows signing material remains"),
      workflow.indexOf("actions/upload-artifact", workflow.indexOf("Prove no Windows signing material remains")),
    );
    expect(cleanup).toContain("if (Test-Path dist)");
    expect(workflow).not.toContain('@"');
    expect(workflow).not.toContain('"@ | Set-Content');
    expect(workflow).toContain('$manifest = @(');
    expect(workflow).toContain(') -join "`n"');
    const windows = workflow.slice(
      workflow.indexOf("  windows:"),
      workflow.indexOf("  stage:"),
    );
    expect(windows).toContain("runs-on: windows-2022");
    expect(windows).not.toContain("runs-on: windows-2025");
    expect(windows).toContain("$PSNativeCommandUseErrorActionPreference = $true");
    expect(windows).toContain('Description="jastreamer Control"');
    const android = workflow.slice(
      workflow.indexOf("  android:"),
      workflow.indexOf("  windows:"),
    );
    expect(android).toContain('$ANDROID_HOME/cmdline-tools/latest/bin/sdkmanager');
    expect(android).toContain('"build-tools;35.0.0"');
    expect(android).toContain('$ANDROID_HOME/build-tools/35.0.0/apksigner');
    expect(android).toContain('rm -rf "$RUNNER_TEMP/control-android-signing"');
    expect(android).not.toContain("jarsigner -verify -strict");
    expect(android).toContain("keytool -printcert -jarfile");
    expect(android).toContain("test \"$aab_fingerprint\" = \"$fingerprint\"");
    expect(android).toContain("| grep -q .");
  });
});
