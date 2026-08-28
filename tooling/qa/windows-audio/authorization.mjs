import { createHash } from "node:crypto";

export const REQUIRED_RUNNER_LABELS = ["self-hosted", "windows", "x64", "jastreamer-audio"];

export class WindowsAudioAuthorizationError extends Error {
  constructor(code) {
    super(code);
    this.name = "WindowsAudioAuthorizationError";
    this.code = code;
  }
}

export const authorizeWindowsAudio = ({ labels, endpointId, endpointIdProtected }) => {
  if (!Array.isArray(labels) || labels.some((label) => typeof label !== "string" || label.trim().length === 0) || new Set(labels).size !== labels.length || REQUIRED_RUNNER_LABELS.some((required) => !labels.includes(required))) {
    return { authorized: false, code: "WINDOWS_AUDIO_RUNNER_UNAUTHORIZED" };
  }
  if (typeof endpointId !== "string" || endpointId.length === 0) {
    return { authorized: false, code: "WINDOWS_AUDIO_ENDPOINT_ID_REQUIRED" };
  }
  if (endpointIdProtected !== true) {
    return { authorized: false, code: "WINDOWS_AUDIO_ENDPOINT_ID_UNPROTECTED" };
  }
  return {
    authorized: true,
    endpointIdentitySha256: createHash("sha256").update(endpointId).digest("hex"),
  };
};

export const pendingReceipt = ({ recordedAt, binding, publication }) => {
  if (publication?.code !== "PRODUCT_GATE_REQUIRED" || publication.externalWrites !== 0) {
    throw new WindowsAudioAuthorizationError("PUBLICATION_DENIAL_UNVERIFIED");
  }
  return {
    schema_version: 1,
    kind: "windows_wasapi_qualification",
    recorded_at: recordedAt,
    qualification_status: "awaiting_external_authorization",
    binding,
    network_calls: 0,
    audio_mutations: 0,
    external_writes: 0,
    publication: { code: "PRODUCT_GATE_REQUIRED", external_writes: 0 },
  };
};
