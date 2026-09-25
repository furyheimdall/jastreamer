const MAX_JSON_BYTES = 64 * 1024;
export const MAX_MEDIA_READ_BYTES = 65_536;
const MAX_STREAM_CHUNK_BYTES = 1024 * 1024;
const OWNER_HEADER = "X-Jastreamer-Browser-Token";
const MEDIA_READ_TIMEOUT_MS = 10_000;
const MAX_INFLIGHT_READS = 8;

export class NativeHTTPError extends Error {
  constructor(status, serverCode, message = "Server request failed.", cause) {
    super(message, cause ? { cause } : undefined);
    this.name = "NativeHTTPError";
    this.status = status;
    this.serverCode = serverCode;
  }
}

export function strictSameOriginURL(origin, value, { media = false } = {}) {
  if (typeof value !== "string" || !value || /[\u0000-\u0020\u007f\\]/.test(value)) return null;
  try {
    const base = new URL(origin);
    const target = new URL(value, `${base.origin}/`);
    if ((target.protocol !== "http:" && target.protocol !== "https:") || target.origin !== base.origin) return null;
    if (target.username || target.password || target.search || target.hash) return null;
    if (media) {
      const decodedPath = decodeURIComponent(target.pathname);
      if (!decodedPath.startsWith("/media/") || decodedPath.includes("\\") ||
        decodedPath.split("/").some((segment) => segment === "." || segment === "..")) {
        return null;
      }
    }
    return target;
  } catch {
    return null;
  }
}

export class NativeServerClient {
  #origin;
  #fetch;

  constructor(targetSession, origin) {
    this.#origin = new URL(origin).origin;
    this.#fetch = (url, init) => targetSession.fetch(String(url), init);
  }

  get origin() {
    return this.#origin;
  }

  async json(method, path, { ownerToken, body, signal, allowEmpty = false } = {}) {
    const target = strictSameOriginURL(this.#origin, path);
    if (!target) throw new NativeHTTPError(0, "INVALID_URL");
    const response = await this.#request(target, method, { ownerToken, body, signal });
    if (!response.ok) throw await httpFailure(response);
    const bytes = await readBounded(response.body, MAX_JSON_BYTES, signal);
    if (bytes.length === 0 && allowEmpty) return {};
    try {
      const value = JSON.parse(Buffer.from(bytes).toString("utf8"));
      if (!value || typeof value !== "object" || Array.isArray(value)) throw new TypeError();
      return value;
    } catch (error) {
      throw new NativeHTTPError(response.status, "INVALID_RESPONSE", "The Server returned an invalid response.", error);
    }
  }

  async bytes(value, maximum, { signal } = {}) {
    const target = strictSameOriginURL(this.#origin, value);
    if (!target) throw new NativeHTTPError(0, "INVALID_URL");
    const response = await this.#request(target, "GET", { signal });
    if (!response.ok) throw await httpFailure(response);
    return readBounded(response.body, maximum, signal);
  }

  async mediaResponse(value, { offset, length, range, signal }) {
    const target = strictSameOriginURL(this.#origin, value, { media: true });
    if (!target) throw new NativeHTTPError(0, "INVALID_MEDIA_URL", "Unsafe media resource.");
    const headers = { Accept: "*/*", "Cache-Control": "no-store", Origin: this.#origin };
    if (range) headers.Range = `bytes=${offset}-${offset + length - 1}`;
    return this.#request(target, "GET", { headers, signal });
  }

  async #request(target, method, { ownerToken, body, headers: extraHeaders, signal } = {}) {
    if (target.origin !== this.#origin) throw new NativeHTTPError(0, "INVALID_URL");
    const headers = new Headers(extraHeaders);
    headers.set("Cache-Control", "no-store");
    headers.set("Accept", headers.get("Accept") ?? "application/json");
    if (ownerToken) headers.set(OWNER_HEADER, ownerToken);
    const init = {
      method,
      headers,
      credentials: "include",
      redirect: "error",
      signal,
    };
    if (method !== "GET" && method !== "HEAD") {
      headers.set("Origin", this.#origin);
      headers.set("X-Jastreamer-Request", "web");
      if (body !== undefined) {
        headers.set("Content-Type", "application/json; charset=utf-8");
        init.body = JSON.stringify(body);
      }
    }
    let response;
    try {
      response = await this.#fetch(target, init);
    } catch (error) {
      if (signal?.aborted) throw signal.reason ?? error;
      throw new NativeHTTPError(0, "NETWORK_ERROR", "The Server could not be reached.", error);
    }
    if (response.redirected || new URL(response.url || target).origin !== this.#origin) {
      response.body?.cancel?.().catch?.(() => {});
      throw new NativeHTTPError(response.status, "REDIRECT_DENIED", "Server redirects are not allowed.");
    }
    return response;
  }
}

export class MediaGrant {
  #client;
  #playID;
  #url;
  #size;
  #seekable;
  #controller = new AbortController();
  #reader = null;
  #streamOffset = 0;
  #pending = new Uint8Array(0);
  #closed = false;
  #activeReads = 0;

  constructor(client, { playID, url, size, seekable }) {
    if (typeof playID !== "string" || !playID) throw new NativeHTTPError(0, "INVALID_MEDIA_ID");
    if (!strictSameOriginURL(client.origin, url, { media: true })) throw new NativeHTTPError(0, "INVALID_MEDIA_URL");
    this.#client = client;
    this.#playID = playID;
    this.#url = url;
    this.#size = Number.isSafeInteger(size) && size > 0 ? size : null;
    this.#seekable = seekable === true;
  }

  get playID() { return this.#playID; }
  get size() { return this.#size; }
  get seekable() { return this.#seekable; }

  cancel() {
    if (this.#closed) return;
    this.#closed = true;
    this.#controller.abort(new DOMException("Media grant cancelled.", "AbortError"));
    this.#reader?.cancel?.().catch?.(() => {});
    this.#reader = null;
    this.#pending = new Uint8Array(0);
  }

  async read(offset, length) {
    if (this.#closed) throw new NativeHTTPError(0, "MEDIA_CANCELLED", "Media grant was cancelled.");
    if (!Number.isSafeInteger(offset) || offset < 0 || !Number.isSafeInteger(length) || length < 1 || length > MAX_MEDIA_READ_BYTES) {
      throw new NativeHTTPError(0, "INVALID_RANGE", "Invalid media byte range.");
    }
    if (this.#size !== null && offset >= this.#size) return { data: new Uint8Array(0), eof: true, size: this.#size };
    if (this.#activeReads >= MAX_INFLIGHT_READS) {
      throw new NativeHTTPError(0, "TOO_MANY_MEDIA_READS", "Too many media reads are pending.");
    }
    this.#activeReads++;
    const deadline = new AbortController();
    const timeout = setTimeout(() => {
      deadline.abort(new DOMException("Media read timed out.", "TimeoutError"));
    }, MEDIA_READ_TIMEOUT_MS);
    timeout.unref?.();
    const signal = AbortSignal.any([this.#controller.signal, deadline.signal]);
    const cancelSequential = () => this.cancel();
    try {
      if (!this.#seekable) {
        signal.addEventListener("abort", cancelSequential, { once: true });
        return await this.#readSequential(offset, length);
      }
      return await this.#readRange(offset, length, signal);
    } finally {
      clearTimeout(timeout);
      signal.removeEventListener("abort", cancelSequential);
      this.#activeReads--;
    }
  }

  async #readRange(offset, length, signal) {
    const response = await this.#client.mediaResponse(this.#url, {
      offset, length, range: true, signal,
    });
    if (response.status === 416) {
      const size = parseUnsatisfiedSize(response.headers.get("content-range"));
      if (size === null || offset < size || (this.#size !== null && size !== this.#size)) {
        await response.body?.cancel?.();
        throw new NativeHTTPError(response.status, "INVALID_RANGE_RESPONSE", "The Server returned an invalid unsatisfied media range.");
      }
      this.#size = size;
      await response.body?.cancel?.();
      return { data: new Uint8Array(0), eof: true, ...(this.#size === null ? {} : { size: this.#size }) };
    }
    if (response.status !== 206) {
      await response.body?.cancel?.();
      throw new NativeHTTPError(response.status, "INVALID_RANGE_RESPONSE", "The Server did not honor the media byte range.");
    }
    const range = parseContentRange(response.headers.get("content-range"));
    if (!range || range.start !== offset || range.end < range.start || range.end >= offset + length ||
      (range.size !== null && range.end >= range.size) ||
      (this.#size !== null && range.size !== null && range.size !== this.#size)) {
      await response.body?.cancel?.();
      throw new NativeHTTPError(response.status, "INVALID_RANGE_RESPONSE", "The Server returned an invalid media byte range.");
    }
    if (range.size !== null) this.#size = range.size;
    const data = await readBounded(response.body, length, signal);
    if (data.length !== range.end - range.start + 1) {
      throw new NativeHTTPError(response.status, "TRUNCATED_MEDIA", "The Server returned a truncated media byte range.");
    }
    const eof = this.#size !== null ? offset + data.length >= this.#size : data.length < length;
    return { data, eof, ...(this.#size === null ? {} : { size: this.#size }) };
  }

  async #readSequential(offset, length) {
    if (offset !== this.#streamOffset) {
      throw new NativeHTTPError(0, "NONSEEKABLE_RANGE", "Non-seekable media must be read sequentially.");
    }
    if (!this.#reader) {
      if (offset !== 0) throw new NativeHTTPError(0, "NONSEEKABLE_RANGE", "Non-seekable media must start at byte zero.");
      const response = await this.#client.mediaResponse(this.#url, {
        offset: 0, length, range: false, signal: this.#controller.signal,
      });
      if (!response.ok || !response.body) throw await httpFailure(response);
      const contentLength = parseNonnegativeInteger(response.headers.get("content-length"));
      if (contentLength !== null) this.#size = contentLength;
      this.#reader = response.body.getReader();
    }
    const output = new Uint8Array(length);
    let written = 0;
    if (this.#pending.length) {
      const used = Math.min(length, this.#pending.length);
      output.set(this.#pending.subarray(0, used), 0);
      written = used;
      this.#pending = this.#pending.subarray(used);
    }
    let done = false;
    while (written < length && !done) {
      const next = await this.#reader.read();
      if (this.#controller.signal.aborted) {
        throw this.#controller.signal.reason ?? new DOMException("Media grant cancelled.", "AbortError");
      }
      done = next.done;
      if (!next.value?.length) continue;
      if (next.value.length > MAX_STREAM_CHUNK_BYTES) {
        await this.#reader.cancel().catch(() => {});
        this.#reader = null;
        throw new NativeHTTPError(0, "MEDIA_CHUNK_TOO_LARGE", "The Server returned an oversized media chunk.");
      }
      const used = Math.min(length - written, next.value.length);
      output.set(next.value.subarray(0, used), written);
      written += used;
      if (used < next.value.length) this.#pending = next.value.subarray(used);
    }
    this.#streamOffset += written;
    if (done) {
      this.#reader = null;
      if (this.#size === null) this.#size = this.#streamOffset;
    }
    return {
      data: output.subarray(0, written),
      eof: done && this.#pending.length === 0,
      ...(this.#size === null ? {} : { size: this.#size }),
    };
  }
}

async function httpFailure(response) {
  let code = `HTTP_${response.status}`;
  try {
    const bytes = await readBounded(response.body, MAX_JSON_BYTES);
    const value = JSON.parse(Buffer.from(bytes).toString("utf8"));
    if (typeof value?.error?.code === "string" && value.error.code) code = value.error.code;
  } catch {}
  return new NativeHTTPError(response.status, code);
}

async function readBounded(body, maximum, signal) {
  if (!body) return new Uint8Array(0);
  const reader = body.getReader();
  const chunks = [];
  let total = 0;
  try {
    while (true) {
      if (signal?.aborted) throw signal.reason ?? new DOMException("Cancelled", "AbortError");
      const { done, value } = await reader.read();
      if (signal?.aborted) throw signal.reason ?? new DOMException("Cancelled", "AbortError");
      if (done) break;
      if (!value?.length) continue;
      total += value.length;
      if (total > maximum) throw new NativeHTTPError(0, "RESPONSE_TOO_LARGE", "The Server response is too large.");
      chunks.push(value);
    }
  } catch (error) {
    await reader.cancel().catch(() => {});
    throw error;
  }
  const result = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    result.set(chunk, offset);
    offset += chunk.length;
  }
  return result;
}

function parseContentRange(value) {
  const match = /^bytes (\d+)-(\d+)\/(\d+|\*)$/.exec(value ?? "");
  if (!match) return null;
  const start = Number(match[1]);
  const end = Number(match[2]);
  const size = match[3] === "*" ? null : Number(match[3]);
  if (![start, end].every(Number.isSafeInteger) || (size !== null && !Number.isSafeInteger(size))) return null;
  return { start, end, size };
}

function parseUnsatisfiedSize(value) {
  const match = /^bytes \*\/(\d+)$/.exec(value ?? "");
  return match ? parseNonnegativeInteger(match[1]) : null;
}

function parseNonnegativeInteger(value) {
  if (!/^(0|[1-9]\d*)$/.test(value ?? "")) return null;
  const parsed = Number(value);
  return Number.isSafeInteger(parsed) ? parsed : null;
}
