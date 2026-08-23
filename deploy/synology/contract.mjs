import { readFile } from "node:fs/promises";

const requiredFixtureFields = [
  "model",
  "dsmVersion",
  "architecture",
  "ociPlatform",
  "containerManager",
  "dockerVersion",
  "advertisedAddress",
  "runtimeCertification",
];

const secretKey = /(password|credential|private.?key|token|secret)/i;
const ipv4Address = /\b(?:\d{1,3}\.){3}\d{1,3}\b/;

const values = (input) => {
  if (Array.isArray(input)) return input.flatMap(values);
  if (input && typeof input === "object") {
    return Object.entries(input).flatMap(([key, value]) => [
      ...(secretKey.test(key) ? [`SECRET_KEY:${key}`] : []),
      ...values(value),
    ]);
  }
  return [String(input ?? "")];
};

export const parseFixture = async (path) => JSON.parse(await readFile(path, "utf8"));

export const validateFixture = (fixture) => {
  const interfaces = Array.isArray(fixture.interfaces) ? fixture.interfaces : [];
  if (interfaces.length > 1 && !fixture.advertisedAddress) {
    return { exitCode: 78, findings: ["AMBIGUOUS_ADVERTISED_ADDRESS"] };
  }

  const findings = [];
  const missing = requiredFixtureFields.filter((field) => !fixture[field]);
  if (missing.length) findings.push(`MISSING_FIELD:${missing.join(",")}`);
  if (fixture.model !== "DS918+") findings.push("UNEXPECTED_MODEL");
  if (fixture.dsmVersion !== "7.2.2-72806 Update 9") findings.push("UNEXPECTED_DSM_VERSION");
  if (fixture.architecture !== "x86_64" || fixture.ociPlatform !== "linux/amd64") findings.push("UNEXPECTED_ARCHITECTURE");
  if (fixture.containerManager !== "Container Manager" || fixture.dockerVersion !== "24.0.2") findings.push("UNEXPECTED_CONTAINER_RUNTIME");
  if (fixture.runtimeCertification !== "candidate-pending-runtime-authorization") findings.push("RUNTIME_CERTIFICATION_MUST_REMAIN_PENDING");
  if (fixture.architecture === "armv7") findings.push("UNSUPPORTED_ARMV7");
  if (interfaces.some((item) => item?.redacted !== true || item?.name !== "REDACTED" || item?.address !== "REDACTED")) findings.push("UNREDACTED_INTERFACE");
  if (fixture.advertisedAddress && fixture.advertisedAddress !== "REDACTED") findings.push("UNREDACTED_ADVERTISED_ADDRESS");
  if (values(fixture).some((value) => value.startsWith("SECRET_KEY:") || ipv4Address.test(value))) findings.push("RETAINED_SECRET_OR_ADDRESS");
  return { exitCode: findings.length ? 65 : 0, findings };
};

export const validateCompose = (source) => {
  const findings = [];
  if (!/network_mode:\s*host/.test(source)) findings.push("HOST_NETWORK_REQUIRED");
  if (!/JSTREAMER_LAN_INTERFACE:/.test(source)) findings.push("LAN_INTERFACE_REQUIRED");
  if (!/JSTREAMER_ADVERTISED_ADDRESS:/.test(source)) findings.push("ADVERTISED_ADDRESS_REQUIRED");
  if (!/target:\s*\/var\/lib\/jstreamer/.test(source)) findings.push("DATA_MOUNT_REQUIRED");
  if (!/user:\s*["']?(?!0(?::0)?["']?$)\d+:\d+/m.test(source)) findings.push("NON_ROOT_USER_REQUIRED");
  if (!/healthcheck:/.test(source)) findings.push("HEALTHCHECK_REQUIRED");
  if (/privileged:\s*true/i.test(source)) findings.push("PRIVILEGED_CONTAINER");
  if (/^\s*platform:/m.test(source)) findings.push("ARCHITECTURE_PIN");
  if (/^\s*ports:/m.test(source) || /network_mode:\s*bridge/.test(source)) findings.push("BRIDGE_NETWORK_REQUIREMENT");
  if (secretKey.test(source)) findings.push("EMBEDDED_SECRET");
  if (ipv4Address.test(source)) findings.push("RETAINED_LAN_ADDRESS");
  if (/image:.*(?::latest|-(?:amd64|arm64)\b)/.test(source)) findings.push("UNSAFE_IMAGE_REFERENCE");
  return findings;
};

export const validateSupportMatrix = (source) => {
  const required = [
    "DS918+",
    "7.2.2-72806 Update 9",
    "linux/amd64",
    "candidate-pending-runtime-authorization",
    "Docker 24.0.2",
    "FiiO K17",
    "V261+",
    "Windows 11 amd64 Renderer",
    "linux/arm64",
    "status: unverified",
    "armv7",
    "status: unsupported",
  ];
  return required.filter((value) => !source.includes(value)).map((value) => `SUPPORT_MATRIX_MISSING:${value}`);
};

export const redactedContractEvidence = (fixture) => ({
  schema_version: 1,
  target: {
    model: fixture.model,
    dsm_version: fixture.dsmVersion,
    architecture: fixture.architecture,
    oci_platform: fixture.ociPlatform,
    container_manager: fixture.containerManager,
    docker_version: fixture.dockerVersion,
  },
  required_external_services: ["github"],
  network_identifiers: "REDACTED",
  credentials_retained: false,
  runtime_certification: "candidate-pending-runtime-authorization",
  runtime_mutation_authorized: false,
  synology_arm64_hardware: "unverified",
  armv7: "unsupported",
});
