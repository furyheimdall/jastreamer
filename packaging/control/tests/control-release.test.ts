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

  test("pins Flutter integration test Espresso resolution offline", () => {
    const gradle = readFileSync(
      new URL("../../../apps/control/android/build.gradle.kts", import.meta.url),
      "utf8",
    );
    expect(gradle).toContain('requested.version?.endsWith("+")');
    expect(gradle).toContain(
      '"androidx.test.espresso:espresso-core" -> useVersion("3.2.0")',
    );
    expect(gradle).toContain(
      '"androidx.test:runner" -> useVersion("1.2.0")',
    );
    expect(gradle).toContain(
      '"androidx.test:rules" -> useVersion("1.2.0")',
    );
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
    expect(release).toContain(
      '-v "$android_ndk_cache:/opt/android-sdk-linux/ndk/27.0.12077973:ro"',
    );
    expect(release).toContain(
      '-v "$android_platform_cache:/opt/android-sdk-linux/platforms/android-36:ro"',
    );
    expect(release).toContain(
      '-v "$android_cmake_cache:/opt/android-sdk-linux/cmake/3.22.1:ro"',
    );
    expect(release).toContain("-e GRADLE_OPTS=-Dorg.gradle.offline=true");
    expect(release).toContain("packaging/control/tooling/normalize-web.ts");
    expect(release).toContain("JASTREAMER_CONTROL_WEB_ONLY");
    expect(release.match(/flutter build web --no-pub --release --no-web-resources-cdn/g)?.length).toBe(2);
    expect(
      release.match(
        /rm -rf build\/web\s+flutter build web --no-pub --release --no-web-resources-cdn/g,
      )?.length,
    ).toBe(2);
    expect(
      release.match(
        /rm -rf \/workspace\/apps\/control \/workspace\/contracts\s+cp -a \/source\/apps\/control \/workspace\/apps\/control\s+cp -a \/source\/contracts \/workspace\/contracts/g,
      )?.length,
    ).toBe(2);
    expect(release).toMatch(
      /cd \/workspace\/apps\/control\s+rm -rf \.dart_tool build\s+flutter_root=/,
    );
    const registrantGeneration =
      "flutter build apk --release --config-only";
    expect(release).toContain(registrantGeneration);
    expect(release.lastIndexOf("flutter build web --no-pub")).toBeLessThan(
      release.indexOf(registrantGeneration),
    );
    expect(release.indexOf(registrantGeneration)).toBeLessThan(
      release.indexOf("flutter build apk --no-pub"),
    );
    expect(release).not.toContain(
      "rm -f android/app/src/main/java/io/flutter/plugins/GeneratedPluginRegistrant.java",
    );
    expect(release).not.toContain("flutter pub get");
    expect(release.match(/offline_package_config\.py/g)?.length).toBe(2);
    expect(release.match(/flutter_web_sdk_preflight\.py/g)?.length).toBe(2);
    expect(release.match(/flutter_web_sdk\.stamp/g)?.length).toBeGreaterThanOrEqual(6);
    expect(release).not.toContain("packaging/control/flutter-package-config.json");
    const webSdkPreflight = readFileSync(new URL("../tooling/flutter_web_sdk_preflight.py", import.meta.url), "utf8");
    expect(webSdkPreflight).toContain("e4b3b674e149286126615768067fbb6ce0e68988b67017df1bb872eacb44b81a");
    expect(webSdkPreflight).toContain("SDK_SIZE: Final = 27_133_134");
    expect(webSdkPreflight).toContain("1e9a811bf8e70466596bcf0ea3a8b5adb5f17f7f");
    expect(webSdkPreflight).toContain("b8962555571d8c170cff8e76023ea7bf60e5ec4b");
    expect(release.match(/docker run --rm --network none/g)?.length).toBe(2);
    expect(release.match(/bin\/cache\/artifacts\/material_fonts\/Roboto-Regular\.ttf/g)?.length).toBe(4);
    expect(release.match(/79e851404657dac2106b3d22ad256d47824a9a5765458edb72c9102a45816d95/g)?.length).toBe(2);
    expect(release.match(/font-contract\.ts/g)?.length).toBe(1);
    expect(release.match(/\.release-web-fonts\/Roboto-Regular\.ttf/g)?.length).toBeGreaterThanOrEqual(4);
    expect(readFileSync(new URL("../../../apps/control/web/flutter_bootstrap.js", import.meta.url), "utf8")).toContain("assets/font-fallback-disabled/");
    expect(release).not.toContain("gstatic.com");
    expect(release).not.toContain(
      "sha256:e33916141978e14e3af996f08feece1a380173a60f6f16a0ecf41b5c42b6363d",
    );
  });

  test("pins a Flutter stable version available on every release platform", () => {
    const workflow = readFileSync(
      new URL(
        "../../../.github/workflows/control-qualification-staging.yml",
        import.meta.url,
      ),
      "utf8",
    ) + readFileSync(
      new URL("../../../.github/workflows/control-qualification-platforms.yml", import.meta.url),
      "utf8",
    ) + readFileSync(
      new URL("../../../.github/workflows/control-qualification-signed-platforms.yml", import.meta.url),
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
    const windows = workflow.slice(workflow.indexOf("  windows:"));
    expect(windows).toContain("runs-on: windows-2022");
    expect(windows).not.toContain("runs-on: windows-2025");
    expect(windows).toContain("$PSNativeCommandUseErrorActionPreference = $true");
    expect(windows).toContain('Description="jastreamer Control"');
    expect(windows).toContain("powershell.exe -NoProfile -NonInteractive -Command");
    expect(windows).toContain("Add-AppxPackage -Path");
    expect(windows).not.toContain("Add-AppxPackage -LiteralPath");
    expect(windows).toContain("$untrustedExit");
    expect(windows).not.toContain("try { Add-AppxPackage $msix");
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
    expect(android).toContain("api-level: 29");
    expect(android).toContain("profile: pixel_2");
    expect(android).toContain("disable-animations: false");
    expect(android).toContain("-no-snapshot");
    expect(android).not.toContain("api-level: 35");
    expect(android).not.toContain("profile: pixel_6");
    expect(android).toContain("script: sh packaging/control/test-android-upgrade.sh");
    expect(android).not.toContain("uid_before=$(adb");
    const upgradeScript = readFileSync(
      new URL("../test-android-upgrade.sh", import.meta.url),
      "utf8",
    );
    expect(upgradeScript).toContain("set -eu");
    expect(upgradeScript).toContain('test -n "$uid_before"');
    expect(upgradeScript).toContain('test "$uid_before" = "$uid_after"');
    expect(upgradeScript).toContain('test "$version_after" = 1002003');
  });

  test("runs browser QA against the exact staged Web archive", () => {
    const workflow = readFileSync(
      new URL(
        "../../../.github/workflows/control-qualification-platforms.yml",
        import.meta.url,
      ),
      "utf8",
    );
    const web = workflow.slice(
      workflow.indexOf("  web:"),
      workflow.indexOf("  android:"),
    );
    expect(web).toContain("Run exact staged Web QA");
    expect(web).toContain('unzip -q "$archive" -d "$CONTROL_WEB_ROOT"');
    expect(web).toContain("CONTROL_WEB_ROOT:");
    expect(web).toContain(
      "playwright test --config playwright.config.mjs control.playwright.mjs",
    );
    const qa = readFileSync(
      new URL("../../../tooling/qa/control.playwright.mjs", import.meta.url),
      "utf8",
    );
    expect(qa).toContain("process.env.CONTROL_WEB_ROOT");
  });
  test("loads Windows SDK base types before package identity APIs", () => {
    const source = readFileSync(
      new URL("../../../apps/control/windows/runner/credential_vault.cpp", import.meta.url),
      "utf8",
    );
    expect(source.indexOf("#include <windows.h>")).toBeLessThan(
      source.indexOf("#include <appmodel.h>"),
    );
  });
});
