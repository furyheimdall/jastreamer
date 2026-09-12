import { normalizeEndpoint, normalizeServerId } from "./security.mjs";

const MAX_RESPONSE_BYTES = 32 * 1024;
const MAX_NAME_LENGTH = 128;
const MAX_VERSION_LENGTH = 64;
const DISPLAY_CONTROL_PATTERN = /[\u0000-\u001f\u007f]/;


export class ProbeError extends Error {
  constructor(code, message, cause) {
    super(message, cause ? { cause } : undefined);
    this.name = "ProbeError";
    this.code = code;
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
        throw new ProbeError("response_too_large", "서버 응답이 허용 크기를 초과했습니다.");
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
  if (timedOut) return new ProbeError("timeout", "서버 응답 시간이 초과되었습니다.", error);
  if (externallyAborted) return new ProbeError("cancelled", "서버 확인이 취소되었습니다.", error);

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
      "HTTPS 인증서를 확인할 수 없습니다. 인증서와 서버 이름을 확인해 주세요.",
      error,
    );
  }
  return new ProbeError("unreachable", "서버에 연결할 수 없습니다.", error);
}

function validateMetadata(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new ProbeError("invalid_metadata", "서버 확인 응답 형식이 올바르지 않습니다.");
  }
  if (value.product !== "jastreamer" || value.protocol !== 1) {
    throw new ProbeError("incompatible_server", "호환되는 JASTREAMER 서버가 아닙니다.");
  }

  let id;
  try {
    id = normalizeServerId(value.id);
  } catch (error) {
    throw new ProbeError("invalid_metadata", "서버 ID가 올바르지 않습니다.", error);
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
    throw new ProbeError("invalid_metadata", "서버 이름 또는 버전 정보가 올바르지 않습니다.");
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
      throw new ProbeError("redirect", "서버 확인 요청이 다른 주소로 이동되었습니다.");
    }
    if (!response.ok) {
      throw new ProbeError("http_status", `서버 확인 요청이 HTTP ${response.status}로 실패했습니다.`);
    }

    const contentType = response.headers.get("content-type")?.toLowerCase() ?? "";
    if (contentType && !contentType.includes("application/json")) {
      throw new ProbeError("invalid_metadata", "서버 확인 응답이 JSON이 아닙니다.");
    }

    const rawBody = await readBoundedBody(response);
    let parsed;
    try {
      parsed = JSON.parse(rawBody);
    } catch (error) {
      throw new ProbeError("invalid_metadata", "서버 확인 응답의 JSON이 올바르지 않습니다.", error);
    }

    const metadata = validateMetadata(parsed);
    if (requiredId && metadata.id !== requiredId) {
      throw new ProbeError(
        "identity_mismatch",
        "저장된 서버와 현재 주소의 서버 ID가 다릅니다. 자격 증명을 공유하지 않습니다.",
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
  if (error instanceof ProbeError) return error.message;
  return "서버를 확인하는 중 알 수 없는 오류가 발생했습니다.";
}
