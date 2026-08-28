const tokenKey = "jastreamer-admin-token";

export class APIError extends Error {
  constructor(status, body) {
    super(body?.message || `Request failed with status ${status}`);
    this.name = "APIError";
    this.status = status;
    this.code = body?.code || "REQUEST_FAILED";
    this.field = body?.field || "";
    this.rule = body?.rule || "";
  }
}

export const session = {
  get token() { return sessionStorage.getItem(tokenKey) || ""; },
  set token(value) { sessionStorage.setItem(tokenKey, value); },
  clear() { sessionStorage.removeItem(tokenKey); },
};

export async function requestJSON(path, options = {}) {
  const headers = new Headers(options.headers || {});
  if (options.body !== undefined) headers.set("Content-Type", "application/json");
  if (session.token) headers.set("Authorization", `Bearer ${session.token}`);
  const response = await fetch(path, { ...options, headers });
  if (response.status === 204) return { body: null, etag: response.headers.get("ETag") || "" };
  let body;
  try { body = await response.json(); }
  catch { body = { message: `Request failed with status ${response.status}` }; }
  if (!response.ok) throw new APIError(response.status, body);
  return { body, etag: response.headers.get("ETag") || "" };
}

export function idempotencyKey(prefix) {
  return `${prefix}-${crypto.randomUUID()}`;
}
