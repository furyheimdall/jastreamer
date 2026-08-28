import Ajv2020 from "../qa/node_modules/ajv/dist/2020.js";
import { isAbsolute, resolve } from "node:path";
import schema from "./product-gate-trust.schema.json" with { type: "json" };
import { registerSchemaFormats } from "../qa/schema-validation.mjs";

const validate = registerSchemaFormats(new Ajv2020({ allErrors: true, strict: false })).compile(schema);
const pinned = {
  production: "tooling/release/product-gate-production-trust-v1.json",
  "current-audit": "tooling/release/product-gate-current-audit-trust-v1.json",
};

export const loadTrustConfig = (options, roots, tools) => {
  const profile = options.profile ?? "production";
  const expected = pinned[profile];
  const absolute = isAbsolute(options.trustConfigPath) ? options.trustConfigPath : resolve(profile === "fixture" ? roots.bundle : roots.repository, options.trustConfigPath);
  if (expected !== undefined && absolute !== resolve(roots.repository, expected)) return { issue: tools.denied("TRUST_CONFIG_REJECTED", "trustConfig") };
  try {
    const config = JSON.parse(tools.readFile(absolute));
    if (!validate(config) || config.profile !== profile) return { issue: tools.denied(profile === "production" ? "PRODUCTION_TRUST_INCOMPLETE" : "TRUST_CONFIG_INVALID", validate.errors?.[0]?.instancePath ?? "trustConfig") };
    if (config.artifactSigning.keyIds.length !== config.artifactSigning.publicKeys.length || config.artifactSigning.publicKeys.some((item) => !config.artifactSigning.keyIds.includes(item.keyId))) return { issue: tools.denied(profile === "production" ? "PRODUCTION_TRUST_INCOMPLETE" : "TRUST_CONFIG_INVALID", "artifactSigning") };
    return { config, profile };
  } catch { return { issue: tools.denied(profile === "production" ? "PRODUCTION_TRUST_INCOMPLETE" : "TRUST_CONFIG_INVALID", "trustConfig") }; }
};
