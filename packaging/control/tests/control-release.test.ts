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
});
