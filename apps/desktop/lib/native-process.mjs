import { spawn as spawnChild } from "node:child_process";
import { EventEmitter } from "node:events";
import path from "node:path";

export const MAX_HELPER_LINE_BYTES = 256 * 1024;
const REQUEST_TIMEOUT_MS = 15_000;
const SHUTDOWN_TIMEOUT_MS = 2_000;

export class NativeProcessError extends Error {
  constructor(code, message, cause) {
    super(message, cause ? { cause } : undefined);
    this.name = "NativeProcessError";
    this.code = code;
  }
}

export function nativeHelperPath({ isPackaged, resourcesPath, appDirectory }) {
  return isPackaged
    ? path.join(resourcesPath, "native-audio", "jastreamer-audio.exe")
    : path.join(appDirectory, "native", "audio", "dist", "jastreamer-audio.exe");
}

export class NativeAudioProcess extends EventEmitter {
  #executable;
  #spawn;
  #child = null;
  #buffer = Buffer.alloc(0);
  #pending = new Map();
  #nextID = 1;
  #terminal = false;
  #intentional = false;

  constructor(executable, { spawn = spawnChild } = {}) {
    super();
    this.#executable = executable;
    this.#spawn = spawn;
  }

  get running() {
    return childIsRunning(this.#child) && !this.#terminal;
  }

  start() {
    if (this.running) return;
    if (this.#terminal) throw new NativeProcessError("helper_terminal", "Native audio must be re-enabled after the helper stopped.");
    let child;
    try {
      child = this.#spawn(this.#executable, [], {
        stdio: ["pipe", "pipe", "pipe"],
        windowsHide: true,
        shell: false,
      });
    } catch (error) {
      this.#fail("helper_start_failed", "Native audio could not start.", error);
      throw new NativeProcessError("helper_start_failed", "Native audio could not start.", error);
    }
    this.#child = child;
    child.stdout.on("data", (chunk) => {
      if (this.#child === child) this.#consume(chunk);
    });
    child.stdin.on("error", (error) => {
      if (this.#child === child) this.#fail("helper_write_failed", "Native audio communication failed.", error);
    });
    for (const stream of [child.stdout, child.stderr]) {
      stream.on("error", (error) => {
        if (this.#child === child) this.#fail("helper_read_failed", "Native audio communication failed.", error);
      });
    }
    // Helper stderr is deliberately neither forwarded nor retained: it may contain
    // decoder input details and is not part of the protocol.
    child.stderr.on("data", () => {});
    child.once("error", (error) => {
      if (this.#child === child) this.#fail("helper_start_failed", "Native audio could not start.", error);
    });
    child.once("exit", () => {
      if (this.#child !== child) return;
      if (!this.#intentional) this.#fail("helper_terminated", "Native audio stopped unexpectedly.");
      else this.#rejectPending(new NativeProcessError("helper_stopped", "Native audio stopped."));
    });
  }

  restart() {
    if (!this.#terminal) {
      this.start();
      return;
    }
    if (childIsRunning(this.#child)) {
      throw new NativeProcessError("helper_stopping", "Native audio is still stopping.");
    }
    this.#child = null;
    this.#buffer = Buffer.alloc(0);
    this.#terminal = false;
    this.#intentional = false;
    this.start();
  }

  async request(method, params = {}, { signal, timeoutMs = REQUEST_TIMEOUT_MS } = {}) {
    this.start();
    if (signal?.aborted) throw signal.reason ?? new DOMException("Cancelled", "AbortError");
    const id = String(this.#nextID++);
    const payload = JSON.stringify({ id, method, params });
    if (Buffer.byteLength(payload, "utf8") > MAX_HELPER_LINE_BYTES) {
      throw new NativeProcessError("request_too_large", "Native audio request is too large.");
    }
    return new Promise((resolve, reject) => {
      const timeout = setTimeout(() => {
        this.#pending.delete(id);
        reject(new NativeProcessError("helper_timeout", "Native audio did not respond."));
      }, timeoutMs);
      timeout.unref?.();
      const abort = () => {
        if (!this.#pending.delete(id)) return;
        clearTimeout(timeout);
        reject(signal.reason ?? new DOMException("Cancelled", "AbortError"));
      };
      this.#pending.set(id, { resolve, reject, timeout, signal, abort });
      signal?.addEventListener("abort", abort, { once: true });
      this.#child.stdin.write(`${payload}\n`, "utf8", (error) => {
        if (!error) return;
        const pending = this.#takePending(id);
        pending?.reject(new NativeProcessError("helper_write_failed", "Native audio communication failed.", error));
      });
    });
  }

  notify(method, params = {}) {
    if (!this.running) return false;
    const payload = JSON.stringify({ method, params });
    if (Buffer.byteLength(payload, "utf8") > MAX_HELPER_LINE_BYTES) return false;
    this.#child.stdin.write(`${payload}\n`, "utf8", () => {});
    return true;
  }

  async shutdown() {
    if (!this.#child) return;
    if (this.#terminal) {
      if (childIsRunning(this.#child)) {
        const exited = new Promise((resolve) => this.#child.once("exit", resolve));
        try { this.#child.kill(); } catch {}
        if (childIsRunning(this.#child)) {
          await Promise.race([
            exited,
            new Promise((resolve) => setTimeout(resolve, SHUTDOWN_TIMEOUT_MS)),
          ]);
        }
      }
      if (childIsRunning(this.#child)) {
        try { this.#child.kill(); } catch {}
        throw new NativeProcessError("helper_stop_failed", "Native audio process termination could not be confirmed.");
      }
      return;
    }
    this.#intentional = true;
    try {
      await this.request("shutdown", {}, { timeoutMs: SHUTDOWN_TIMEOUT_MS });
    } catch {
      try { this.#child.kill(); } catch {}
    }
    if (childIsRunning(this.#child)) {
      await Promise.race([
        new Promise((resolve) => this.#child.once("exit", resolve)),
        new Promise((resolve) => setTimeout(resolve, SHUTDOWN_TIMEOUT_MS)),
      ]);
    }
    if (childIsRunning(this.#child)) {
      try { this.#child.kill(); } catch {}
    }
    this.#terminal = true;
    this.#rejectPending(new NativeProcessError("helper_stopped", "Native audio stopped."));
    if (childIsRunning(this.#child)) {
      throw new NativeProcessError("helper_stop_failed", "Native audio process termination could not be confirmed.");
    }
  }

  #consume(chunk) {
    if (this.#terminal) return;
    this.#buffer = Buffer.concat([this.#buffer, Buffer.from(chunk)]);
    if (this.#buffer.length > MAX_HELPER_LINE_BYTES && this.#buffer.indexOf(0x0a) < 0) {
      this.#fail("helper_protocol", "Native audio returned an oversized message.");
      return;
    }
    while (true) {
      const newline = this.#buffer.indexOf(0x0a);
      if (newline < 0) {
        if (this.#buffer.length > MAX_HELPER_LINE_BYTES) {
          this.#fail("helper_protocol", "Native audio returned an oversized message.");
        }
        return;
      }
      const line = this.#buffer.subarray(0, newline);
      this.#buffer = this.#buffer.subarray(newline + 1);
      if (line.length === 0) continue;
      if (line.length > MAX_HELPER_LINE_BYTES) {
        this.#fail("helper_protocol", "Native audio returned an oversized message.");
        return;
      }
      let value;
      try {
        value = JSON.parse(line.toString("utf8"));
      } catch (error) {
        this.#fail("helper_protocol", "Native audio returned an invalid message.", error);
        return;
      }
      if (!value || typeof value !== "object" || Array.isArray(value)) {
        this.#fail("helper_protocol", "Native audio returned an invalid message.");
        return;
      }
      if (typeof value.id === "string") {
        const pending = this.#takePending(value.id);
        if (!pending) continue;
        if (value.ok === true && value.result && typeof value.result === "object" && !Array.isArray(value.result)) {
          pending.resolve(value.result);
        } else if (value.ok === false && value.error && typeof value.error.code === "string") {
          pending.reject(new NativeProcessError(safeCode(value.error.code), safeMessage(value.error.message)));
        } else {
          pending.reject(new NativeProcessError("helper_protocol", "Native audio returned an invalid reply."));
        }
        continue;
      }
      if (typeof value.event !== "string") {
        this.#fail("helper_protocol", "Native audio returned an invalid notification.");
        return;
      }
      this.emit("notification", value);
    }
  }

  #takePending(id) {
    const pending = this.#pending.get(id);
    if (!pending) return null;
    this.#pending.delete(id);
    clearTimeout(pending.timeout);
    pending.signal?.removeEventListener("abort", pending.abort);
    return pending;
  }

  #rejectPending(error) {
    for (const id of [...this.#pending.keys()]) this.#takePending(id)?.reject(error);
  }

  #fail(code, message, cause) {
    if (this.#terminal) return;
    this.#terminal = true;
    const error = new NativeProcessError(code, message, cause);
    this.#rejectPending(error);
    if (childIsRunning(this.#child)) {
      try { this.#child.kill(); } catch {}
    }
    this.emit("terminal", error);
  }
}

function childIsRunning(child) {
  return Boolean(child && child.exitCode === null && child.signalCode == null);
}

function safeCode(value) {
  return /^[a-z0-9_.-]{1,64}$/.test(value) ? value : "helper_error";
}

function safeMessage(value) {
  const text = typeof value === "string" ? value.trim() : "";
  return text && text.length <= 240 && !/[\u0000-\u001f\u007f]/.test(text)
    ? text
    : "Native audio request failed.";
}
