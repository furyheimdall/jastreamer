const RUNNER_LABEL = /^[a-z0-9][a-z0-9-]{2,63}$/;

export class K17AuthorizationError extends Error {
  constructor(code) {
    super(code);
    this.name = "K17AuthorizationError";
    this.code = code;
  }
}

export const parseSupportMatrix = (text) => {
  if (typeof text !== "string") throw new K17AuthorizationError("SUPPORT_MATRIX_INVALID");
  const lines = text.split(/\r?\n/);
  let inCertification = false;
  let authorized;
  let runnerLabel;
  for (const line of lines) {
    if (/^[^\s#][^:]*:\s*(?:#.*)?$/.test(line)) inCertification = line.startsWith("certification:");
    if (!inCertification) continue;
    const authorization = line.match(/^  renderer_control_authorized:\s*(true|false)\s*(?:#.*)?$/);
    if (authorization !== null) {
      if (authorized !== undefined) throw new K17AuthorizationError("SUPPORT_MATRIX_INVALID");
      authorized = authorization[1] === "true";
    }
    const label = line.match(/^  k17_runner_label:\s*([^#]*?)\s*(?:#.*)?$/);
    if (label !== null) {
      if (runnerLabel !== undefined) throw new K17AuthorizationError("SUPPORT_MATRIX_INVALID");
      runnerLabel = label[1];
    }
  }
  if (authorized === undefined) throw new K17AuthorizationError("SUPPORT_MATRIX_INVALID");
  if (authorized && (runnerLabel === undefined || runnerLabel === "")) throw new K17AuthorizationError("K17_RUNNER_LABEL_REQUIRED");
  if (runnerLabel !== undefined && runnerLabel !== "" && !RUNNER_LABEL.test(runnerLabel)) throw new K17AuthorizationError("K17_RUNNER_LABEL_INVALID");
  return Object.freeze({ rendererControlAuthorized: authorized, k17RunnerLabel: runnerLabel ?? "" });
};

export const runAuthorizationGate = async (options) => {
  const publication = await options.verifyPublicationDenied();
  if (publication.code !== "PRODUCT_GATE_REQUIRED" || publication.externalWrites !== 0) {
    throw new K17AuthorizationError("PUBLICATION_DENIAL_UNVERIFIED");
  }
  if (!options.matrix.rendererControlAuthorized) {
    return {
      schema_version: 1,
      kind: "k17_qualification",
      recorded_at: options.recordedAt,
      candidate_sha256: options.candidateSha256,
      qualification_status: "awaiting_external_authorization",
      network_calls: { ssdpControl: 0, soap: 0, media: 0 },
      audio_mutations: 0,
      publication: { code: publication.code, external_writes: publication.externalWrites },
    };
  }
  if (options.actualRunnerLabel !== options.matrix.k17RunnerLabel) throw new K17AuthorizationError("K17_RUNNER_LABEL_MISMATCH");
  return options.runPhysical();
};
