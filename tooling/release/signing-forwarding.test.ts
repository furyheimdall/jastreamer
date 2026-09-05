import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const signingInputs = {
  server: ["SERVER_WINDOWS_SIGNING_PFX_B64", "SERVER_WINDOWS_SIGNING_PFX_PASSWORD"],
  control: [
    "CONTROL_ANDROID_JKS_B64", "CONTROL_ANDROID_STORE_PASSWORD",
    "CONTROL_ANDROID_KEY_ALIAS", "CONTROL_ANDROID_KEY_PASSWORD",
    "CONTROL_ANDROID_CERT_SHA256", "CONTROL_WINDOWS_PFX_B64",
    "CONTROL_WINDOWS_PFX_PASSWORD",
  ],
};

for (const component of ["server", "control"] as const) {
  for (const [file, job] of [
    [`${component}-release.yml`, "staging"],
    ["product-qualification-dispatch.yml", component],
  ] as const) {
    test(`${file} forwards ${component} signing inputs across the first reusable boundary`, () => {
      const workflow = Bun.YAML.parse(readFileSync(resolve(import.meta.dirname, "../../.github/workflows", file), "utf8")) as {
        jobs: Record<string, { secrets?: Record<string, string> }>;
      };
      expect(workflow.jobs[job]?.secrets).toEqual(Object.fromEntries(
        signingInputs[component].map(name => [name, `\${{ secrets.${name} }}`]),
      ));
    });
  }
}
