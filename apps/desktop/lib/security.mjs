import { createHash } from "node:crypto";
import { fileURLToPath } from "node:url";

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const MAX_ENDPOINT_LENGTH = 2_048;

export class EndpointValidationError extends Error {
  constructor(message, code = "invalid_endpoint") {
    super(message);
    this.name = "EndpointValidationError";
    this.code = code;
  }
}

export function normalizeServerId(value) {
  const id = typeof value === "string" ? value.trim().toLowerCase() : "";
  if (!UUID_PATTERN.test(id)) {
    throw new EndpointValidationError("서버 ID가 올바른 UUID가 아닙니다.", "invalid_server_id");
  }
  return id;
}

export function normalizeEndpoint(value, { allowImplicitHttp = true } = {}) {
  let input = typeof value === "string" ? value.trim() : "";
  if (!input || input.length > MAX_ENDPOINT_LENGTH) {
    throw new EndpointValidationError("서버 주소를 입력해 주세요.");
  }
  if (/[\u0000-\u0020\u007f]/.test(input)) {
    throw new EndpointValidationError("주소에 공백이나 제어 문자를 포함할 수 없습니다.");
  }


  if (!/^[a-z][a-z\d+.-]*:\/\//i.test(input)) {
    if (!allowImplicitHttp) {
      throw new EndpointValidationError("주소는 http:// 또는 https://로 시작해야 합니다.");
    }
    input = `http://${input}`;
  }

  let url;
  try {
    url = new URL(input);
  } catch {
    throw new EndpointValidationError("올바른 서버 주소가 아닙니다.");
  }

  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw new EndpointValidationError("HTTP 또는 HTTPS 주소만 사용할 수 있습니다.");
  }
  if (!url.hostname) {
    throw new EndpointValidationError("서버 호스트 이름이 없습니다.");
  }
  if (url.hostname.endsWith(".")) {
    url.hostname = url.hostname.slice(0, -1);
  }
  if (url.username || url.password) {
    throw new EndpointValidationError("사용자 정보가 포함된 주소는 사용할 수 없습니다.");
  }
  if (input.includes("?") || input.includes("#")) {
    throw new EndpointValidationError("주소에 쿼리나 조각을 포함할 수 없습니다.");
  }
  if (url.pathname !== "/") {
    throw new EndpointValidationError("서버의 루트 주소만 입력해 주세요. 경로는 사용할 수 없습니다.");
  }

  return url.origin;
}

export function sessionPartitionFor(serverId, endpoint) {
  const id = normalizeServerId(serverId);
  const origin = normalizeEndpoint(endpoint, { allowImplicitHttp: false });
  const digest = createHash("sha256").update(id).update("\0").update(origin).digest("hex");
  return `persist:jastreamer-${digest}`;
}

export function isSameServerNavigation(target, endpoint) {
  try {
    const expectedOrigin = normalizeEndpoint(endpoint, { allowImplicitHttp: false });
    const url = new URL(target);
    return (
      (url.protocol === "http:" || url.protocol === "https:") &&
      !url.username &&
      !url.password &&
      url.origin === expectedOrigin
    );
  } catch {
    return false;
  }
}

function isSameShellDocument(target, shellUrl) {
  try {
    const actual = new URL(target);
    const expected = new URL(shellUrl);
    return (
      !actual.search &&
      !actual.hash &&
      !expected.search &&
      !expected.hash &&
      fileURLToPath(actual) === fileURLToPath(expected)
    );
  } catch {
    return false;
  }
}

export function isTrustedShellSender(event, shellContents, shellUrl) {
  return Boolean(
    event &&
      shellContents &&
      event.sender === shellContents &&
      event.senderFrame === shellContents.mainFrame &&
      isSameShellDocument(event.senderFrame?.url, shellUrl),
  );
}

export function restrictSession(targetSession) {
  targetSession.setPermissionCheckHandler(() => false);
  targetSession.setPermissionRequestHandler((_webContents, _permission, callback) => callback(false));
  if (typeof targetSession.setDevicePermissionHandler === "function") {
    targetSession.setDevicePermissionHandler(() => false);
  }
  targetSession.on("will-download", (event) => event.preventDefault());
}

export function restrictLocalContents(contents, shellUrl) {
  contents.setWindowOpenHandler(() => ({ action: "deny" }));
  const preventOtherNavigation = (event, targetUrl) => {
    if (!isSameShellDocument(targetUrl, shellUrl)) event.preventDefault();
  };
  contents.on("will-navigate", preventOtherNavigation);
  contents.on("will-redirect", preventOtherNavigation);
}

export function restrictRemoteContents(contents, endpoint) {
  contents.setWindowOpenHandler(() => ({ action: "deny" }));
  const preventOtherOrigin = (event, targetUrl) => {
    if (!isSameServerNavigation(targetUrl, endpoint)) event.preventDefault();
  };
  contents.on("will-navigate", preventOtherOrigin);
  contents.on("will-redirect", preventOtherOrigin);
}
