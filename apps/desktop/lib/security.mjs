import { createHash } from "node:crypto";
import { fileURLToPath } from "node:url";
import { LANGUAGE_COOKIE_NAME, normalizeLanguage, t } from "./i18n.mjs";

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
    throw new EndpointValidationError(t("security.invalidServerId"), "invalid_server_id");
  }
  return id;
}

export function normalizeEndpoint(value, { allowImplicitHttp = true } = {}) {
  let input = typeof value === "string" ? value.trim() : "";
  if (!input || input.length > MAX_ENDPOINT_LENGTH) {
    throw new EndpointValidationError(t("security.addressRequired"));
  }
  if (/[\u0000-\u0020\u007f]/.test(input)) {
    throw new EndpointValidationError(t("security.addressCharacters"));
  }


  if (!/^[a-z][a-z\d+.-]*:\/\//i.test(input)) {
    if (!allowImplicitHttp) {
      throw new EndpointValidationError(t("security.schemeRequired"));
    }
    input = `http://${input}`;
  }

  let url;
  try {
    url = new URL(input);
  } catch {
    throw new EndpointValidationError(t("security.invalidAddress"));
  }

  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw new EndpointValidationError(t("security.httpOnly"));
  }
  if (!url.hostname) {
    throw new EndpointValidationError(t("security.missingHost"));
  }
  if (url.hostname.endsWith(".")) {
    url.hostname = url.hostname.slice(0, -1);
  }
  if (url.username || url.password) {
    throw new EndpointValidationError(t("security.credentials"));
  }
  if (input.includes("?") || input.includes("#")) {
    throw new EndpointValidationError(t("security.queryFragment"));
  }
  if (url.pathname !== "/") {
    throw new EndpointValidationError(t("security.rootOnly"));
  }

  return url.origin;
}

export function sessionPartitionFor(serverId, endpoint) {
  const id = normalizeServerId(serverId);
  const origin = normalizeEndpoint(endpoint, { allowImplicitHttp: false });
  const digest = createHash("sha256").update(id).update("\0").update(origin).digest("hex");
  return `persist:jastreamer-${digest}`;
}

export function isLanguageCookieForOrigin(cookie, endpoint) {
  if (
    !cookie ||
    cookie.name !== LANGUAGE_COOKIE_NAME ||
    !normalizeLanguage(cookie.value) ||
    cookie.removed ||
    cookie.hostOnly !== true ||
    cookie.httpOnly !== false ||
    cookie.session !== false ||
    !Number.isFinite(cookie.expirationDate) ||
    cookie.expirationDate <= Date.now() / 1_000 ||
    cookie.path !== "/" ||
    cookie.sameSite !== "strict"
  ) {
    return false;
  }
  try {
    const cookieHost = cookie.domain.toLowerCase().replace(/^\[|\]$/g, "");
    const endpointHost = new URL(endpoint).hostname.toLowerCase().replace(/^\[|\]$/g, "");
    return cookieHost === endpointHost;
  } catch {
    return false;
  }
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
