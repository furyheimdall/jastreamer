import { cpSync, existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { describe, expect, test } from "bun:test";
import { verifyControlFont } from "../tooling/font-contract";

const root = resolve(import.meta.dirname, "../../..");
const control = resolve(root, "apps/control");

describe("Control Noto Sans KR contract", () => {
  test("verifies exact source metadata, font bytes, variable axis, OFL, and Korean glyphs", () => {
    // Given / When: the repository-controlled font boundary is parsed.
    const evidence = verifyControlFont(control);
    // Then: immutable hashes and Korean coverage bind the release input.
    expect(evidence).toEqual({ family: "Noto Sans KR", fontSha256: "194018e6b2b293a7964f037b25c0249ce1418bc9ab3c971060a03aa57861e252", licenseSha256: "1c05c68c34f9708415aada51f17e1b0092d2cea709bf4a94cd38114f9e73d7d9", weightAxis: [100, 100, 900], representativeText: "안녕하세요 재생 목록" });
  });

  test("fails closed when vendored font bytes drift", () => {
    // Given: an isolated copy with one changed font byte.
    const temporary = mkdtempSync(join(tmpdir(), "control-font-drift-")); cpSync(control, temporary, { recursive: true }); const path = join(temporary, "assets/fonts/noto_sans_kr/NotoSansKR-wght.ttf"); const bytes = readFileSync(path); bytes[bytes.length - 1] = (bytes[bytes.length - 1] ?? 0) ^ 1; writeFileSync(path, bytes);
    // When / Then: the build verifier rejects before Flutter runs.
    try { expect(() => verifyControlFont(temporary)).toThrow("CONTROL_FONT_BYTES_DRIFT"); } finally { rmSync(temporary, { recursive: true, force: true }); }
  });

  test("binds pubspec, theme, bootstrap, and policy-generated notices", () => {
    // Given: machine-consumed Control and policy sources.
    const pubspec = readFileSync(join(control, "pubspec.yaml"), "utf8"); const theme = readFileSync(join(control, "lib/control_theme.dart"), "utf8"); const bootstrap = readFileSync(join(control, "web/flutter_bootstrap.js"), "utf8"); const closure = readFileSync(join(root, "tooling/policy/closures/control.json"), "utf8");
    // When / Then: every runtime and licensing boundary names the bundled family.
    expect(existsSync(join(root, "packaging/control/flutter-package-config.json"))).toBe(false); expect(existsSync(join(root, "packaging/control/tooling/offline_package_config.py"))).toBe(true); expect(pubspec.match(/asset: assets\/fonts\/noto_sans_kr\/NotoSansKR-wght\.ttf/g)).toHaveLength(9); expect(pubspec).toContain("weight: 100"); expect(pubspec).toContain("weight: 900"); expect(theme).toContain("fontFamily: controlFontFamily"); expect(bootstrap).toContain('fontFallbackBaseUrl: "assets/font-fallback-disabled/"'); expect(bootstrap).not.toContain("fonts.gstatic"); expect(closure).toContain('"package":"Noto Sans KR"'); expect(existsSync(join(root, "tooling/policy/licenses/google-fonts-Noto-Sans-KR-OFL-1.1.txt"))).toBe(true);
  });
});
