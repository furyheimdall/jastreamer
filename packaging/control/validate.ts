export type ControlReleaseInput = { publicAssets: string[]; applicationId: string; signingLineage: string; protectedSigningMaterial: boolean };
export function validateControlRelease(input: ControlReleaseInput) {
  const errors: string[] = [];
  for (const asset of input.publicAssets) {
    if (/\.aab$/i.test(asset)) errors.push("FORBIDDEN_AAB_ASSET");
    else if (!/\.(pwa\.zip|msix|apk)$/i.test(asset)) errors.push("PUBLIC_ASSET_NOT_ALLOWED");
  }
  if (input.applicationId !== "io.jastreamer.control") errors.push("INVALID_APPLICATION_ID");
  if (input.signingLineage !== "io.jastreamer.control") errors.push("SIGNING_LINEAGE_CHANGED");
  if (!input.protectedSigningMaterial) errors.push("PROTECTED_SIGNING_REQUIRED");
  return { ok: errors.length === 0, errors: [...new Set(errors)] };
}
