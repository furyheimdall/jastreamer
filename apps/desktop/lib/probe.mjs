import { normalizeEndpoint, normalizeServerId } from "./security.mjs";
import { t } from "./i18n.mjs";

const MAX_RESPONSE_BYTES = 32 * 1024;
const MAX_NAME_LENGTH = 128;
const MAX_VERSION_LENGTH = 64;
const DISPLAY_CONTROL_PATTERN = /[\u0000-\u001f\u007f]/;


export class ProbeError extends Error {
  constructor(code, messageKey, cause, params = {}) {
    super(t(messageKey, params), cause ? { cause } : undefined);
    this.name = "ProbeError";
    this.code = code;
    this.messageKey = messageKey;
    this.params = params;
  }
}

async function readBoundedBody(response) {
  if (!response.body) return "";

  const reader = response.body.getReader();
  const chunks = [];
  let size = 0;
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > MAX_RESPONSE_BYTES) {
        throw new ProbeError("response_too_large", "probe.responseTooLarge");
      }
      chunks.push(value);
    }
  } finally {
    if (size > MAX_RESPONSE_BYTES) await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
  return Buffer.concat(chunks, size).toString("utf8");
}

function classifyFetchFailure(error, timedOut, externallyAborted) {
  if (timedOut) return new ProbeError("timeout", "probe.timeout", error);
  if (externallyAborted) return new ProbeError("cancelled", "probe.cancelled", error);

  const causeCode = String(error?.cause?.code ?? error?.code ?? "").toUpperCase();
  const message = String(error?.cause?.message ?? error?.message ?? "").toUpperCase();
  if (
    causeCode.includes("CERT") ||
    causeCode.includes("TLS") ||
    causeCode.includes("SSL") ||
    causeCode === "DEPTH_ZERO_SELF_SIGNED_CERT" ||
    causeCode === "UNABLE_TO_VERIFY_LEAF_SIGNATURE" ||
    message.includes("CERTIFICATE") ||
    message.includes("TLS") ||
    message.includes("ERR_CERT_")
  ) {
    return new ProbeError(
      "tls",
      "probe.tls",
      error,
    );
  }
  return new ProbeError("unreachable", "probe.unreachable", error);
}

function validateMetadata(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new ProbeError("invalid_metadata", "probe.invalidObject");
  }
  if (value.product !== "jastreamer" || value.protocol !== 1) {
    throw new ProbeError("incompatible_server", "probe.incompatible");
  }

  let id;
  try {
    id = normalizeServerId(value.id);
  } catch (error) {
    throw new ProbeError("invalid_metadata", "probe.invalidId", error);
  }

  const name = typeof value.name === "string" ? value.name.trim() : "";
  const version = typeof value.version === "string" ? value.version.trim() : "";
  if (
    !name ||
    name.length > MAX_NAME_LENGTH ||
    DISPLAY_CONTROL_PATTERN.test(name) ||
    !version ||
    version.length > MAX_VERSION_LENGTH ||
    DISPLAY_CONTROL_PATTERN.test(version)
  ) {
    throw new ProbeError("invalid_metadata", "probe.invalidInfo");
  }

  return { id, name, version };
}

export async function probeEndpoint(
  endpoint,
  { expectedId = null, timeoutMs = 4_000, signal = null, fetchImpl = globalThis.fetch } = {},
) {
  const origin = normalizeEndpoint(endpoint, { allowImplicitHttp: false });
  const requiredId = expectedId ? normalizeServerId(expectedId) : null;
  const controller = new AbortController();
  let timedOut = false;
  let externallyAborted = Boolean(signal?.aborted);

  const abortFromCaller = () => {
    externallyAborted = true;
    controller.abort(signal?.reason);
  };
  signal?.addEventListener("abort", abortFromCaller, { once: true });
  if (externallyAborted) controller.abort(signal?.reason);

  const timer = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, timeoutMs);

  try {
    const response = await fetchImpl(new URL("/api/v1/discovery", `${origin}/`), {
      method: "GET",
      headers: { Accept: "application/json" },
      redirect: "manual",
      credentials: "omit",
      cache: "no-store",
      referrerPolicy: "no-referrer",
      signal: controller.signal,
    });

    if (response.status >= 300 && response.status < 400) {
      throw new ProbeError("redirect", "probe.redirect");
    }
    if (!response.ok) {
      throw new ProbeError("http_status", "probe.httpStatus", null, { status: response.status });
    }

    const contentType = response.headers.get("content-type")?.toLowerCase() ?? "";
    if (contentType && !contentType.includes("application/json")) {
      throw new ProbeError("invalid_metadata", "probe.notJson");
    }

    const rawBody = await readBoundedBody(response);
    let parsed;
    try {
      parsed = JSON.parse(rawBody);
    } catch (error) {
      throw new ProbeError("invalid_metadata", "probe.invalidJson", error);
    }

    const metadata = validateMetadata(parsed);
    if (requiredId && metadata.id !== requiredId) {
      throw new ProbeError(
        "identity_mismatch",
        "probe.identityMismatch",
      );
    }
    return { ...metadata, origin };
  } catch (error) {
    if (error instanceof ProbeError) throw error;
    throw classifyFetchFailure(error, timedOut, externallyAborted);
  } finally {
    controller.abort();
    clearTimeout(timer);
    signal?.removeEventListener("abort", abortFromCaller);
  }
}

export function probeErrorMessage(error) {
  if (error instanceof ProbeError) return t(error.messageKey, error.params);
  return t("probe.unknown");
}
