import { EventEmitter } from "node:events";
import { performance } from "node:perf_hooks";
import { MediaGrant, NativeHTTPError, NativeServerClient, strictSameOriginURL } from "./native-http.mjs";

const REGISTRATIONS_PATH = "/api/v1/browser-output/registrations";
const PLAYER_PATH = "/api/v1/player";
const MIME_CANDIDATES = [
  "audio/mpeg",
  "audio/mp4; codecs=mp4a.40.2",
  "audio/flac",
  "audio/ogg; codecs=vorbis",
  "audio/ogg; codecs=opus",
  "audio/wav",
];
const MAX_LEASE_MS = 60_000;
const LEASE_RENEW_MS = 2_000;
const IDENTITY_CHECK_MS = 10_000;
const MAX_SMTC_ARTWORK_BYTES = 128 * 1024;

export class NativeControllerError extends Error {
  constructor(code, message, cause) {
    super(message, cause ? { cause } : undefined);
    this.name = "NativeControllerError";
    this.code = code;
  }
}

export class NativeWindowsController extends EventEmitter {
  #preferences;
  #helper;
  #probe;
  #platform;
  #settings = { enabled: false, device_id: "default", exclusive: false, volume: 1 };
  #available = false;
  #devices = [];
  #audio = emptyAudio();
  #registration = null;
  #publicError = null;
  #generation = 0;
  #media = null;
  #completedSequence = 0;
  #cancelBeforeSequence = 0;
  #activeSequence = 0;
  #metadata = null;
  #lastObservation = null;
  #lastTimeReportAt = 0;
  #transportChain = Promise.resolve();
  #actionChain = Promise.resolve();
  #deferredObservation = null;
  #localStop = Promise.resolve();
  #terminal = false;
  #shuttingDown = false;

  constructor({ platform = process.platform, preferences, helper, probe }) {
    super();
    this.#platform = platform;
    this.#preferences = preferences;
    this.#helper = helper;
    this.#probe = probe;
    helper?.on("notification", (value) => this.#onHelperNotification(value));
    helper?.on("terminal", (error) => void this.#loseRegistration("helper_terminated", error.message, true));
  }

  async initialize() {
    this.#settings = await this.#preferences.load();
    this.#audio.volume = this.#settings.volume;
    if (this.#platform !== "win32" || !this.#helper) return this.state();
    try {
      const devices = await this.#helper.request("devices");
      this.#available = devices.available === true;
      this.#devices = sanitizeDevices(devices.devices);
      const status = await this.#helper.request("status");
      this.#applyAudio(status);
      await this.#helper.request("set_volume", { volume: this.#settings.volume });
    } catch (error) {
      this.#available = false;
      this.#publicError = publicFailure(error, "native_unavailable", "Native Windows audio is unavailable.");
    }
    this.#emitState();
    return this.state();
  }

  state() {
    return {
      device: this.#registration ? structuredClone(this.#registration.device) : null,
      recovering: false,
      volume: this.#registration ? this.#settings.volume : null,
      ...(this.#publicError ? { error: { ...this.#publicError } } : {}),
      audio: {
        available: this.#available,
        enabled: this.#settings.enabled,
        devices: this.#devices.map((value) => ({ ...value })),
        requested: { device_id: this.#settings.device_id, exclusive: this.#settings.exclusive },
        state: this.#audio.state,
        actual: this.#audio.actual ? { ...this.#audio.actual } : null,
        can_configure: ["stopped", "error"].includes(this.#audio.state),
      },
    };
  }

  async assertCanChangeServer() {
    return this.#serialize(async () => {
      if (!this.#registration) return;
      const status = await this.#helper.request("status");
      this.#applyAudio(status);
      if (["loaded", "playing", "paused"].includes(this.#audio.state) || this.#media) {
        throw new NativeControllerError("stop_required", "Stop Windows playback before changing servers.");
      }
    });
  }
  async prepareRemoteServer(server) {
    return this.#serialize(async () => {
      const existing = this.#registration;
      if (!existing || existing.key === serverKey(server)) return;
      const status = await this.#helper.request("status");
      this.#applyAudio(status);
      if (["loaded", "playing", "paused"].includes(this.#audio.state) || this.#media) {
        throw new NativeControllerError("stop_required", "Stop Windows playback before changing servers.");
      }
      await this.#disconnectRegistration(existing, true);
    });
  }

  async request(server, targetSession, action, args = {}, { signal } = {}) {
    if (this.#platform !== "win32") throw new NativeControllerError("unavailable", "Native Windows audio is unavailable.");
    if (!server || !targetSession) throw new NativeControllerError("invalid_sender", "The native audio request was denied.");
    if (this.#shuttingDown) throw new NativeControllerError("shutting_down", "Windows playback is shutting down.");
    if (signal?.aborted) throw signal.reason ?? new DOMException("Cancelled", "AbortError");
    if (action === "status") {
      requireKeys(args, []);
      if (this.#helper.running && !this.#terminal) {
        const inventory = await this.#helper.request("devices", {}, { signal });
        this.#devices = sanitizeDevices(inventory.devices);
        this.#available = inventory.available === true;
      }
      return this.state();
    }
    return this.#serialize(async () => {
      if (signal?.aborted) throw signal.reason ?? new DOMException("Cancelled", "AbortError");
      switch (action) {
        case "connect":
          requireKeys(args, ["name"]);
          await this.#connect(server, targetSession, requireName(args.name), false, signal);
          break;
        case "connect_if_available":
          requireKeys(args, ["name"]);
          requireName(args.name);
          if (!this.#registration || this.#registration.key !== serverKey(server)) return this.state();
          break;
        case "disconnect":
          requireKeys(args, []);
          await this.#disconnect(server);
          break;
        case "rename":
          requireKeys(args, ["name"]);
          await this.#rename(server, requireName(args.name), signal);
          break;
        case "set_volume":
          requireKeys(args, ["volume"]);
          await this.#setVolume(server, requireVolume(args.volume), signal);
          break;
        case "configure":
          requireKeys(args, ["enabled", "device_id", "exclusive"]);
          await this.#configure(args, signal);
          break;
        default:
          throw new NativeControllerError("invalid_request", "Native audio action is not supported.");
      }
      return this.state();
    });
  }

  #serialize(operation) {
    const result = this.#actionChain.then(operation, operation);
    this.#actionChain = result.catch(() => {});
    return result;
  }

  async shutdown() {
    if (this.#shuttingDown) return;
    this.#shuttingDown = true;
    const existing = this.#registration;
    if (existing) await this.#disconnectRegistration(existing, true).catch(() => {});
    try { await this.#helper?.shutdown(); } catch {}
  }

  async #connect(server, targetSession, name, onlyIfAvailable, signal) {
    if (this.#terminal) {
      if (onlyIfAvailable) return;
      await this.#restartHelper(signal);
    }
    if (!this.#available || !this.#settings.enabled) {
      if (onlyIfAvailable) return;
      throw new NativeControllerError("native_disabled", "Enable native Windows audio before connecting.");
    }
    const key = serverKey(server);
    if (this.#registration?.key === key) return;
    if (this.#registration) {
      if (onlyIfAvailable) return;
      if (["loaded", "playing", "paused"].includes(this.#audio.state) || this.#media) {
        throw new NativeControllerError("stop_required", "Stop Windows playback before changing servers.");
      }
      await this.#disconnectRegistration(this.#registration, true);
    }
    const client = new NativeServerClient(targetSession, server.origin);
    const started = performance.now();
    let response;
    try {
      response = await client.json("POST", REGISTRATIONS_PATH, {
        body: { name, protocol_info: MIME_CANDIDATES }, signal: AbortSignal.timeout(15_000),
      });
    } catch (error) {
      throw publicNativeError(error, "registration_failed", "Windows playback could not connect.");
    }
    let parsed;
    try {
      parsed = parseRegistration(response, server, key, client, started);
    } catch (error) {
      await releaseUnclaimed(client, response).catch(() => {});
      throw publicNativeError(error, "invalid_response", "The Server returned an invalid playback response.");
    }
    if (signal?.aborted || this.#shuttingDown) {
      await this.#release(parsed).catch(() => {});
      throw signal?.reason ?? new DOMException("Cancelled", "AbortError");
    }
    this.#registration = parsed;
    this.#completedSequence = 0;
    this.#cancelBeforeSequence = 0;
    this.#publicError = null;
    this.#startLoops(parsed);
    this.#emitState();
  }
  async #restartHelper(signal) {
    try {
      this.#helper.restart();
      const devices = await this.#helper.request("devices", {}, { signal });
      const status = await this.#helper.request("status", {}, { signal });
      await this.#helper.request("set_volume", { volume: this.#settings.volume }, { signal });
      this.#devices = sanitizeDevices(devices.devices);
      this.#available = devices.available === true;
      if (!this.#available) throw new NativeControllerError("native_unavailable", "Native Windows audio is unavailable.");
      this.#applyAudio(status);
      this.#terminal = false;
      this.#publicError = null;
      this.#emitState();
    } catch (error) {
      this.#terminal = true;
      this.#available = false;
      throw publicNativeError(error, "native_unavailable", "Native Windows audio could not restart.");
    }
  }


  async #disconnect(server) {
    const existing = this.#registration;
    if (!existing || existing.key !== serverKey(server)) return;
    await this.#disconnectRegistration(existing, true);
  }

  async #disconnectRegistration(existing, release) {
    if (this.#registration !== existing) return;
    this.#registration = null;
    this.#generation++;
    existing.controller.abort();
    let stopFailure = null;
    try {
      await this.#stopLocal();
    } catch (error) {
      stopFailure = error;
      this.#terminal = true;
      this.#available = false;
      await this.#helper.shutdown().catch(() => {});
    }
    if (release) await this.#release(existing).catch(() => {});
    this.#publicError = stopFailure
      ? { code: "stop_failed", message: "Windows audio could not confirm that playback stopped." }
      : null;
    this.#emitState();
    if (stopFailure) throw new NativeControllerError("stop_failed", this.#publicError.message, stopFailure);
  }

  async #release(existing) {
    await existing.client.json("DELETE", registrationPath(existing.id), {
      ownerToken: existing.ownerToken, allowEmpty: true, signal: AbortSignal.timeout(3_000),
    });
  }

  async #rename(server, name, signal) {
    const existing = this.#requireRegistration(server);
    try {
      const device = await existing.client.json("PUT", registrationPath(existing.id), {
        ownerToken: existing.ownerToken, body: { name }, signal,
      });
      if (this.#registration !== existing) throw new NativeControllerError("not_connected", "Windows playback disconnected.");
      existing.device = sanitizeDevice(device);
      this.#publicError = null;
      this.#emitState();
    } catch (error) {
      this.#handleTransportFailure(existing, error);
      throw publicNativeError(error, "rename_failed", "The Windows output name could not be changed.");
    }
  }

  async #setVolume(server, volume, signal) {
    this.#requireRegistration(server);
    const result = await this.#helper.request("set_volume", { volume }, { signal });
    this.#applyAudio(result);
    this.#settings = await this.#preferences.set({ volume });
    this.#publicError = null;
    this.#emitState();
  }

  async #configure(args, signal) {
    if (typeof args.enabled !== "boolean" || typeof args.exclusive !== "boolean" ||
      typeof args.device_id !== "string" || !args.device_id || args.device_id.length > 512 || /[\u0000-\u001f\u007f]/.test(args.device_id)) {
      throw new NativeControllerError("invalid_request", "Native audio settings are invalid.");
    }
    if (this.#helper.running && !this.#terminal) {
      const status = await this.#helper.request("status", {}, { signal });
      if (signal?.aborted) throw signal.reason ?? new DOMException("Cancelled", "AbortError");
      this.#applyAudio(status);
    } else if (args.enabled) {
      await this.#restartHelper(signal);
    } else {
      this.#audio = emptyAudio();
    }
    if (["loaded", "playing", "paused"].includes(this.#audio.state) || this.#media) {
      this.#emitState();
      throw new NativeControllerError("stop_required", "Stop Windows playback before changing audio settings.");
    }
    const existing = this.#registration;
    if (existing) await this.#disconnectRegistration(existing, true);
    if (args.enabled) {
      if (!this.#available) throw new NativeControllerError("native_unavailable", "Native Windows audio is unavailable.");
      if (args.device_id !== "default" && !this.#devices.some((value) => value.id === args.device_id)) {
        throw new NativeControllerError("device_unavailable", "The selected Windows audio endpoint is unavailable.");
      }
    }
    this.#settings = await this.#preferences.set({
      enabled: args.enabled, device_id: args.device_id, exclusive: args.exclusive,
    });
    this.#publicError = null;
    this.#emitState();
  }

  #requireRegistration(server) {
    const existing = this.#registration;
    if (!existing || existing.key !== serverKey(server)) {
      throw new NativeControllerError("not_connected", "Windows playback is not connected to this Server.");
    }
    return existing;
  }

  #startLoops(expected) {
    const generation = ++this.#generation;
    const run = async () => {
      void this.#pollLoop(expected, generation);
      void this.#leaseLoop(expected, generation);
      void this.#identityLoop(expected, generation);
      this.#armLeaseWatchdog(expected, generation);
    };
    void run();
  }

  async #pollLoop(expected, generation) {
    let delayMS = expected.pollAfter;
    while (this.#owns(expected, generation)) {
      const started = performance.now();
      try {
        const response = await expected.client.json("GET", `${registrationPath(expected.id)}/commands`, {
          ownerToken: expected.ownerToken, signal: expected.controller.signal,
        });
        this.#applyLease(expected, response, started, generation);
        if (!this.#owns(expected, generation)) return;
        delayMS = clampPoll(response.poll_after_ms, expected.pollAfter);
        const command = response.command;
        if (command && this.#shouldExecute(command.sequence)) await this.#executeCommand(expected, command, generation);
      } catch (error) {
        if (!this.#owns(expected, generation)) return;
        if (this.#handleTransportFailure(expected, error)) return;
        delayMS = 1_000;
      }
      await delay(delayMS, expected.controller.signal).catch(() => {});
    }
  }

  async #leaseLoop(expected, generation) {
    while (this.#owns(expected, generation)) {
      await delay(LEASE_RENEW_MS, expected.controller.signal).catch(() => {});
      if (!this.#owns(expected, generation)) return;
      const started = performance.now();
      try {
        const response = await expected.client.json("PUT", `${registrationPath(expected.id)}/lease`, {
          ownerToken: expected.ownerToken, body: {}, signal: expected.controller.signal,
        });
        this.#applyLease(expected, response, started, generation);
        if (!this.#owns(expected, generation)) return;
        if (this.#media && this.#activeSequence === 0 && this.#lastObservation &&
          ["loaded", "playing", "paused"].includes(this.#audio.state)) {
          void this.#sendObservation(expected, { ...this.#lastObservation, event: "timeupdate" }, this.#completedSequence);
        }
      } catch (error) {
        if (!this.#owns(expected, generation)) return;
        if (this.#handleTransportFailure(expected, error)) return;
      }
    }
  }

  async #identityLoop(expected, generation) {
    while (this.#owns(expected, generation)) {
      await delay(IDENTITY_CHECK_MS, expected.controller.signal).catch(() => {});
      if (!this.#owns(expected, generation)) return;
      try {
        await this.#probe(expected.server.origin, { expectedId: expected.server.id, timeoutMs: 4_000, signal: expected.controller.signal });
      } catch (error) {
        if (!this.#owns(expected, generation)) return;
        if (["identity_mismatch", "invalid_metadata", "incompatible_server", "tls"].includes(error?.code)) {
          await this.#loseRegistration("server_identity", "The Server identity could not be verified.", true, expected);
          return;
        }
      }
    }
  }

  #applyLease(expected, response, started, generation) {
    if (!this.#owns(expected, generation)) return;
    const duration = Number(response.lease_duration_ms);
    if (!Number.isFinite(duration) || duration <= 0 || duration > MAX_LEASE_MS) {
      void this.#loseRegistration("invalid_lease", "The Windows playback connection ended.", true, expected);
      return;
    }
    expected.leaseDeadline = Math.max(expected.leaseDeadline, started + duration);
    this.#armLeaseWatchdog(expected, generation);
    const cancelBefore = positiveSafeInteger(response.cancel_before_sequence) ?? 0;
    if (cancelBefore > this.#cancelBeforeSequence) this.#cancelBeforeSequence = cancelBefore;
    if ((this.#activeSequence > 0 && this.#activeSequence <= cancelBefore) ||
      (this.#media?.sequence > 0 && this.#media.sequence <= cancelBefore)) {
      this.#cancelMedia(cancelBefore);
    }
  }

  #armLeaseWatchdog(expected, generation) {
    clearTimeout(expected.leaseTimer);
    expected.leaseTimer = setTimeout(() => {
      if (this.#owns(expected, generation) && performance.now() >= expected.leaseDeadline) {
        void this.#loseRegistration("lease_expired", "The Windows playback connection expired.", true, expected);
      }
    }, Math.max(0, expected.leaseDeadline - performance.now()));
    expected.leaseTimer.unref?.();
  }

  #shouldExecute(sequence) {
    const value = positiveSafeInteger(sequence);
    return value !== null && value > this.#completedSequence && value > this.#cancelBeforeSequence;
  }

  async #executeCommand(expected, command, generation) {
    const sequence = positiveSafeInteger(command.sequence);
    await this.#localStop;
    if (sequence === null || !this.#shouldExecute(sequence) || !this.#owns(expected, generation)) return;
    this.#activeSequence = sequence;
    try {
      let result;
      switch (command.action) {
        case "set_uri": result = await this.#setURI(expected, command, sequence); break;
        case "play":
        case "pause":
        case "stop":
        case "seek": result = await this.#transport(command, sequence, expected.controller.signal); break;
        default: throw new NativeControllerError("action_failed", "Unsupported media action.");
      }
      if (sequence <= this.#cancelBeforeSequence || !this.#owns(expected, generation)) return;
      await this.#sendFrozenReport(expected, {
        sequence, result: "succeeded", observation: result.observation,
      }, generation, this.#media);
      this.#completedSequence = sequence;
      if (this.#media) this.#media.sequence = sequence;
      if (command.action === "stop") this.#clearMedia();
    } catch (error) {
      if (!this.#owns(expected, generation) || sequence <= this.#cancelBeforeSequence || isAbort(error)) return;
      if (isAuthenticationFailure(error)) {
        await this.#loseRegistration("authentication_required", "Sign in again to use Windows playback.", true, expected);
        return;
      }
      const errorCode = reportErrorCode(error, command.action);
      try {
        await this.#sendFrozenReport(expected, { sequence, result: "failed", error_code: errorCode }, generation);
        this.#completedSequence = sequence;
      } catch {}
      if (command.action === "set_uri") this.#clearMedia();
      this.#publicError = publicFailure(error, "playback_failed", "Windows playback failed.");
      this.#emitState();
    } finally {
      if (this.#activeSequence === sequence) this.#activeSequence = 0;
      const deferred = this.#deferredObservation;
      this.#deferredObservation = null;
      if (deferred && deferred.expected === this.#registration && deferred.media === this.#media &&
        this.#completedSequence === sequence && sequence > this.#cancelBeforeSequence) {
        this.#onHelperNotification({ ...deferred.value, sequence });
      }
    }
  }

  async #setURI(expected, command, sequence) {
    const playID = requireOpaque(command.play_id, "play_id");
    const resource = command.resource;
    if (!resource || typeof resource !== "object" || Array.isArray(resource)) throw new NativeControllerError("action_failed", "Missing media resource.");
    if (!MIME_CANDIDATES.some((value) => value.split(";")[0] === String(resource.mime).toLowerCase().split(";")[0])) {
      throw new NativeControllerError("media_unsupported", "Unsupported media type.");
    }
    if (!strictSameOriginURL(expected.server.origin, resource.url, { media: true })) {
      throw new NativeControllerError("media_unsupported", "Unsafe media resource.");
    }
    this.#clearMedia();
    const grant = new MediaGrant(expected.client, {
      playID, url: resource.url, size: resource.size, seekable: resource.seekable,
    });
    const metadata = {
      title: safeText(resource.title, 512), artist: safeText(resource.artist, 512), album: safeText(resource.album, 512),
      duration_ms: nonnegativeInteger(resource.duration_ms) ?? 0,
      seekable: resource.seekable === true,
      artwork_base64: undefined,
    };
    this.#media = { grant, playID, sequence, metadata, readChain: Promise.resolve() };
    this.#metadata = metadata;
    const artwork = strictSameOriginURL(expected.server.origin, resource.artwork_url);
    if (artwork) {
      try {
        const bytes = await expected.client.bytes(artwork.href, MAX_SMTC_ARTWORK_BYTES, { signal: expected.controller.signal });
        metadata.artwork_base64 = Buffer.from(bytes).toString("base64");
      } catch (error) {
        if (error instanceof NativeHTTPError && [401, 403].includes(error.status)) throw error;
      }
    }
    const params = {
      play_id: playID,
      sequence,
      size: grant.size ?? -1,
      seekable: grant.seekable,
      mime: String(resource.mime),
      duration_ms: metadata.duration_ms,
      title: metadata.title,
      artist: metadata.artist,
      album: metadata.album,
      ...(typeof resource.transformed === "boolean" ? { transformed: resource.transformed } : {}),
      device_id: this.#settings.device_id,
      exclusive: this.#settings.exclusive,
      volume: this.#settings.volume,
    };
    const result = await this.#helper.request("set_uri", params, { signal: expected.controller.signal });
    return this.#acceptHelperResult(result, playID, sequence);
  }

  async #transport(command, sequence, signal) {
    const playID = requireOpaque(command.play_id, "play_id");
    if (this.#media && this.#media.playID !== playID) throw new NativeControllerError("action_failed", "Media identity changed.");
    const params = { play_id: playID, sequence };
    if (command.action === "seek") {
      const position = command.position_ms === undefined ? 0 : nonnegativeInteger(command.position_ms);
      if (position === null) throw new NativeControllerError("action_failed", "Invalid seek position.");
      params.position_ms = position;
    }
    const result = await this.#helper.request(command.action, params, { signal });
    return this.#acceptHelperResult(result, playID, sequence);
  }

  #acceptHelperResult(result, playID, sequence) {
    if (!result?.observation || result.observation.play_id !== playID) {
      throw new NativeControllerError("helper_protocol", "Native audio returned an invalid observation.");
    }
    this.#applyAudio(result.audio);
    const observation = sanitizeObservation(result.observation, playID);
    this.#lastObservation = observation;
    this.#updateSmtc(observation);
    this.#emitState();
    return { observation, audio: this.#audio, sequence };
  }

  async #sendFrozenReport(expected, body, generation, expectedMedia = null) {
    let retryMS = 150;
    while (this.#owns(expected, generation)) {
      if (expectedMedia && this.#media !== expectedMedia) throw new DOMException("Superseded", "AbortError");
      if (performance.now() >= expected.leaseDeadline) {
        await this.#loseRegistration("lease_expired", "The Windows playback connection expired.", true, expected);
        throw new DOMException("Lease expired", "AbortError");
      }
      try {
        await expected.client.json("POST", `${registrationPath(expected.id)}/reports`, {
          ownerToken: expected.ownerToken, body, allowEmpty: true, signal: expected.controller.signal,
        });
        if (!this.#owns(expected, generation) || (expectedMedia && this.#media !== expectedMedia) ||
          body.sequence <= this.#cancelBeforeSequence) throw new DOMException("Superseded", "AbortError");
        return;
      } catch (error) {
        if (isAbort(error)) throw error;
        if (error instanceof NativeHTTPError && (error.status === 409 || error.serverCode === "STALE_BROWSER_REPORT")) {
          if (expectedMedia && this.#registration === expected && this.#media === expectedMedia) {
            this.#cancelMedia(Math.max(this.#cancelBeforeSequence, body.sequence));
          }
          throw new DOMException("Stale report", "AbortError");
        }
        if (this.#handleTransportFailure(expected, error)) throw new DOMException("Registration lost", "AbortError");
        const remaining = expected.leaseDeadline - performance.now();
        if (remaining <= retryMS) {
          await this.#loseRegistration("lease_expired", "The Windows playback connection expired.", true, expected);
          throw new DOMException("Lease expired", "AbortError");
        }
        await delay(retryMS, expected.controller.signal);
        retryMS = Math.min(retryMS * 2, 1_000);
      }
    }
    throw new DOMException("Registration changed", "AbortError");
  }

  async #sendObservation(expected, observation, sequence, errorCode = null) {
    const media = this.#media;
    if (this.#registration !== expected || !media || !positiveSafeInteger(sequence) ||
      observation.play_id !== media.playID) return;
    if (["ended", "error"].includes(observation.event)) {
      try {
        await this.#sendFrozenReport(expected, {
          sequence, observation, ...(errorCode ? { error_code: errorCode } : {}),
        }, this.#generation, media);
      } catch (error) {
        if (!isAbort(error)) this.#handleTransportFailure(expected, error);
      }
      return;
    }
    try {
      await expected.client.json("POST", `${registrationPath(expected.id)}/reports`, {
        ownerToken: expected.ownerToken,
        body: { sequence, observation, ...(errorCode ? { error_code: errorCode } : {}) },
        allowEmpty: true, signal: expected.controller.signal,
      });
    } catch (error) {
      if (this.#registration !== expected || this.#media !== media || media.sequence !== sequence) return;
      if (error instanceof NativeHTTPError && (error.status === 409 || error.serverCode === "STALE_BROWSER_REPORT")) {
        this.#cancelMedia(Math.max(this.#cancelBeforeSequence, sequence));
        return;
      }
      this.#handleTransportFailure(expected, error);
    }
  }

  #onHelperNotification(value) {
    if (value.event === "read") {
      void this.#serveRead(value);
      return;
    }
    if (value.event === "transport") {
      this.#queuePlayerControl(value);
      return;
    }
    if (value.event === "smtc_error") {
      this.#surfaceSmtcError(value.error);
      return;
    }
    if (value.event === "state") {
      this.#applyAudio(value.audio);
      this.#emitState();
      return;
    }
    if (value.event !== "observation") return;
    const expected = this.#registration;
    const media = this.#media;
    const sequence = positiveSafeInteger(value.sequence);
    if (!expected || !media || sequence === null || value.play_id !== media.playID) return;
    if (this.#activeSequence !== 0) {
      if (sequence >= media.sequence && sequence <= this.#activeSequence &&
        ["ended", "error"].includes(value.observation?.event)) {
        this.#deferredObservation = { expected, media, value };
      }
      return;
    }
    if (sequence !== media.sequence || sequence !== this.#completedSequence) return;
    try {
      const observation = sanitizeObservation(value.observation, media.playID);
      this.#applyAudio(value.audio);
      this.#lastObservation = observation;
      if (observation.event === "error") {
        this.#publicError = publicFailure(value.error, "playback_failed", "Windows playback failed.");
      }
      this.#updateSmtc(observation);
      this.#emitState();
      const now = performance.now();
      if (observation.event === "timeupdate" && now - this.#lastTimeReportAt < 900) return;
      if (observation.event === "timeupdate") this.#lastTimeReportAt = now;
      void this.#sendObservation(
        expected,
        observation,
        sequence,
        observation.event === "error"
          ? (["media_failed", "decode_failed"].includes(value.error?.code) ? "media_failed" : "action_failed")
          : null,
      );
    } catch {}
  }

  async #serveRead(value) {
    const readID = typeof value.read_id === "string" && value.read_id.length <= 256 ? value.read_id : "";
    const media = this.#media;
    const expected = this.#registration;
    if (!readID) return;
    if (!media || value.play_id !== media.playID) {
      this.#helper.notify("read_result", { read_id: readID, play_id: String(value.play_id ?? ""), data: "", eof: true, error: { code: "stale_media", message: "Media grant is no longer active." } });
      return;
    }
    try {
      const operation = () => media.grant.read(value.offset, value.length);
      const reading = media.grant.seekable ? operation() : media.readChain.then(operation, operation);
      if (!media.grant.seekable) media.readChain = reading.catch(() => {});
      const result = await reading;
      if (this.#media !== media) throw new NativeControllerError("stale_media", "Media grant is no longer active.");
      this.#helper.notify("read_result", {
        read_id: readID, play_id: media.playID, data: Buffer.from(result.data).toString("base64"), eof: result.eof,
        ...(result.size === undefined ? {} : { size: result.size }),
      });
    } catch (error) {
      if (isAuthenticationFailure(error)) {
        await this.#loseRegistration("authentication_required", "Sign in again to use Windows playback.", true, expected);
      }
      this.#helper.notify("read_result", {
        read_id: readID, play_id: media.playID, data: "", eof: true,
        error: { code: safeCode(error?.serverCode ?? error?.code ?? "media_read_failed"), message: "Media data could not be read." },
      });
    }
  }

  #queuePlayerControl(value) {
    const action = ["play", "pause", "stop", "seek", "next", "previous"].includes(value.action) ? value.action : null;
    if (!action) return;
    const expected = this.#registration;
    const media = this.#media;
    if (!expected || !media || this.#activeSequence !== 0 ||
      !["loaded", "playing", "paused"].includes(this.#audio.state)) return;
    const body = { action };
    if (action === "seek") {
      const position = nonnegativeInteger(value.position_ms);
      if (position === null) return;
      body.position_ms = position;
    }
    this.#transportChain = this.#transportChain.then(async () => {
      if (this.#registration !== expected || this.#media !== media || this.#activeSequence !== 0) return;
      try {
        const player = await expected.client.json("GET", PLAYER_PATH, { signal: expected.controller.signal });
        if (this.#registration !== expected || this.#media !== media || this.#activeSequence !== 0 ||
          !["loaded", "playing", "paused"].includes(this.#audio.state)) return;
        if (player.renderer_id !== expected.device.id) {
          this.#withdrawSmtc();
          return;
        }
        await expected.client.json("POST", PLAYER_PATH, { body, signal: expected.controller.signal });
        if (this.#registration === expected) {
          this.#publicError = null;
          this.#emitState();
        }
      } catch (error) {
        if (!this.#handleTransportFailure(expected, error) && this.#registration === expected) {
          this.#publicError = { code: "control_failed", message: "The Server could not apply the media control." };
          this.#emitState();
        }
      }
    }, () => {});
  }

  #updateSmtc(observation) {
    if (!this.#registration || !this.#metadata) return;
    if (["stopped", "ended", "error"].includes(observation.event)) {
      this.#withdrawSmtc();
      return;
    }
    const metadata = this.#metadata;
    void this.#helper.request("smtc", {
      enabled: true,
      state: observation.state ?? stateForObservation(observation.event, this.#audio.state),
      position_ms: observation.position_ms,
      duration_ms: observation.duration_ms || metadata.duration_ms,
      seekable: metadata.seekable,
      title: metadata.title,
      artist: metadata.artist,
      album: metadata.album,
      ...(metadata.artwork_base64 ? { artwork_base64: metadata.artwork_base64 } : {}),
    }).catch((error) => {
      if (error?.code === "smtc_unavailable") this.#surfaceSmtcError(error);
    });
  }

  #withdrawSmtc() {
    if (!this.#helper.running) return;
    void this.#helper.request("smtc", {
      enabled: false, state: "stopped", position_ms: 0, duration_ms: 0,
      title: "", artist: "", album: "", seekable: false,
    }).catch(() => {});
  }

  #surfaceSmtcError(error) {
    if (!this.#registration) return;
    const message = typeof error?.message === "string" && error.message.trim() &&
      error.message.length <= 240 && !/[\u0000-\u001f\u007f]/.test(error.message)
      ? error.message
      : "Windows media controls could not be updated.";
    this.#publicError = { code: "smtc_unavailable", message };
    this.#emitState();
  }

  #cancelMedia(sequence) {
    const media = this.#media;
    if (!media) return;
    const expected = this.#registration;
    this.#clearMedia();
    this.#localStop = this.#helper.request("stop", { play_id: media.playID, sequence }).then((result) => {
      if (result?.observation?.event !== "stopped" || result.observation.play_id !== media.playID ||
        result?.audio?.state !== "stopped") {
        throw new NativeControllerError("stop_failed", "Native audio did not confirm cancellation.");
      }
      if (this.#registration !== expected || this.#media) return;
      this.#applyAudio(result.audio);
      this.#emitState();
    }).catch(async () => {
      if (this.#registration !== expected) return;
      this.#terminal = true;
      this.#available = false;
      try {
        await this.#helper.shutdown();
        this.#audio = { ...emptyAudio(), state: "error", volume: this.#settings.volume };
      } catch {}
      await this.#loseRegistration("stop_failed", "Windows audio could not confirm cancellation.", false, expected);
    });
  }

  #clearMedia() {
    this.#media?.grant.cancel();
    this.#media = null;
    this.#metadata = null;
    this.#lastObservation = null;
    this.#lastTimeReportAt = 0;
    this.#deferredObservation = null;
    this.#withdrawSmtc();
  }

  async #stopLocal() {
    const media = this.#media;
    const active = media || ["loaded", "playing", "paused"].includes(this.#audio.state);
    const playID = media?.playID || this.#audio.play_id;
    const sequence = Math.max(media?.sequence ?? 0, this.#audio.sequence, this.#completedSequence, 1);
    this.#clearMedia();
    if (active && this.#helper.running) {
      if (!playID) {
        await this.#helper.shutdown().catch(() => {});
        this.#terminal = true;
        this.#available = false;
        throw new NativeControllerError("stop_failed", "Windows audio identity was lost while stopping.");
      }
      const result = await this.#helper.request("stop", { play_id: playID, sequence });
      if (result?.observation?.event !== "stopped" || result.observation.play_id !== playID ||
        result?.audio?.state !== "stopped") {
        throw new NativeControllerError("stop_failed", "Native audio did not confirm that playback stopped.");
      }
      this.#applyAudio(result.audio);
    } else if (active) {
      await this.#helper.shutdown();
      this.#audio = { ...emptyAudio(), state: "error", volume: this.#settings.volume };
    }
    if (this.#helper.running) {
      await this.#helper.request("smtc", {
        enabled: false, state: "stopped", position_ms: 0, duration_ms: 0,
        title: "", artist: "", album: "", seekable: false,
      }).catch(() => {});
    }
  }

  #applyAudio(value) {
    if (!value || typeof value !== "object") return;
    const state = ["stopped", "loaded", "playing", "paused", "error"].includes(value.state) ? value.state : this.#audio.state;
    const volume = Number(value.volume);
    this.#audio = {
      state,
      play_id: typeof value.play_id === "string" ? value.play_id : "",
      sequence: positiveSafeInteger(value.sequence) ?? 0,
      volume: Number.isFinite(volume) && volume >= 0 && volume <= 1 ? volume : this.#settings.volume,
      actual: sanitizeActual(value.actual),
    };
  }

  #handleTransportFailure(expected, error) {
    if (this.#registration !== expected) return true;
    const fatal = error instanceof NativeHTTPError && [401, 403, 404].includes(error.status);
    const tls = isTLSError(error);
    if (fatal || tls) {
      void this.#loseRegistration(tls ? "server_tls" : "authentication_required", tls ? "The Server certificate could not be verified." : "Sign in again to use Windows playback.", true, expected);
      return true;
    }
    return false;
  }

  async #loseRegistration(code, message, stop, expected = this.#registration) {
    if (expected && this.#registration !== expected) return;
    if (expected) {
      this.#registration = null;
      this.#generation++;
      expected.controller.abort();
      clearTimeout(expected.leaseTimer);
    }
    if (stop) {
      try {
        await this.#stopLocal();
      } catch {
        this.#terminal = true;
        this.#available = false;
        await this.#helper.shutdown().catch(() => {});
      }
    }
    if (code === "helper_terminated") {
      this.#terminal = true;
      this.#available = false;
    }
    this.#publicError = { code, message };
    this.#emitState();
    if (expected) await this.#release(expected).catch(() => {});
  }

  #owns(expected, generation) {
    return !this.#shuttingDown && this.#registration === expected && this.#generation === generation && !expected.controller.signal.aborted;
  }

  #emitState() { this.emit("state", this.state()); }
}

function emptyAudio() { return { state: "stopped", play_id: "", sequence: 0, volume: 1, actual: null }; }
function serverKey(server) { return `${server.id}\0${new URL(server.origin).origin}`; }
function registrationPath(id) { return `${REGISTRATIONS_PATH}/${encodeURIComponent(id)}`; }
function clampPoll(value, fallback) { const n = Number(value); return Number.isFinite(n) ? Math.max(100, Math.min(1_000, n)) : fallback; }
function positiveSafeInteger(value) { const n = Number(value); return Number.isSafeInteger(n) && n > 0 ? n : null; }
function nonnegativeInteger(value) { const n = Number(value); return Number.isSafeInteger(n) && n >= 0 ? n : null; }
function requireOpaque(value, field) { if (typeof value !== "string" || !value || value.length > 512 || /[\u0000-\u001f\u007f]/.test(value)) throw new NativeControllerError("action_failed", `Invalid ${field}.`); return value; }
function safeText(value, maximum) { const text = typeof value === "string" ? value.trim() : ""; return text.slice(0, maximum).replace(/[\u0000-\u001f\u007f]/g, ""); }
function requireName(value) { const name = safeText(value, 81); if (!name || Buffer.byteLength(name, "utf8") > 80) throw new NativeControllerError("invalid_name", "Enter a name between 1 and 80 bytes."); return name; }
function requireVolume(value) { const volume = Number(value); if (!Number.isFinite(volume) || volume < 0 || volume > 1) throw new NativeControllerError("invalid_volume", "Volume must be between 0 and 1."); return volume; }
function requireKeys(value, allowed) { if (!value || typeof value !== "object" || Array.isArray(value) || Object.keys(value).some((key) => !allowed.includes(key)) || allowed.some((key) => !(key in value))) throw new NativeControllerError("invalid_request", "Native audio request is invalid."); }
function delay(ms, signal) {
  return new Promise((resolve, reject) => {
    let timer;
    const cleanup = () => signal?.removeEventListener("abort", abort);
    const finish = () => {
      cleanup();
      resolve();
    };
    const abort = () => {
      clearTimeout(timer);
      cleanup();
      reject(signal.reason ?? new DOMException("Cancelled", "AbortError"));
    };
    if (signal?.aborted) {
      abort();
      return;
    }
    timer = setTimeout(finish, ms);
    timer.unref?.();
    signal?.addEventListener("abort", abort, { once: true });
  });
}
function isAbort(error) { return error?.name === "AbortError"; }
function isTLSError(error) { for (let value = error; value; value = value.cause) if (typeof value.code === "string" && /CERT|TLS|SSL/.test(value.code.toUpperCase())) return true; return false; }
function isAuthenticationFailure(error) { return isTLSError(error) || (error instanceof NativeHTTPError && [401, 403].includes(error.status)); }
function safeCode(value, fallback = "media_read_failed") { return typeof value === "string" && /^[a-z0-9_.-]{1,64}$/.test(value) ? value : fallback; }
function publicFailure(error, fallbackCode, fallbackMessage) { return { code: safeCode(error?.code ?? error?.serverCode, fallbackCode), message: typeof error?.message === "string" && error.message.length <= 240 ? error.message : fallbackMessage }; }
function publicNativeError(error, code, message) { if (error instanceof NativeControllerError) return error; return new NativeControllerError(safeCode(error?.serverCode ?? error?.code, code), message, error); }
function reportErrorCode(error, action) { const code = error?.code ?? error?.serverCode; if (code === "media_unsupported") return "media_unsupported"; if (code === "media_failed" || code === "decode_failed") return "media_failed"; if (code === "media_error" || (action !== "set_uri" && code === "media_read_failed")) return "media_error"; return "action_failed"; }
function stateForObservation(event, fallback) { if (event === "playing") return "playing"; if (event === "pause") return "paused"; if (event === "loaded") return "loaded"; if (["stopped", "ended", "error"].includes(event)) return "stopped"; return fallback; }

function parseRegistration(value, server, key, client, started) {
  const id = requireResponseString(value.registration_id, 256);
  const ownerToken = requireResponseString(value.owner_token, 256);
  const duration = Number(value.lease_duration_ms);
  if (!Number.isFinite(duration) || duration <= 0 || duration > MAX_LEASE_MS) throw new NativeControllerError("invalid_response", "Invalid playback lease.");
  return {
    server, key, client, id, ownerToken, device: sanitizeDevice(value.device),
    pollAfter: clampPoll(value.poll_after_ms, 250), leaseDeadline: started + duration,
    controller: new AbortController(), leaseTimer: null,
  };
}
async function releaseUnclaimed(client, value) { if (typeof value?.registration_id !== "string" || typeof value?.owner_token !== "string") return; await client.json("DELETE", registrationPath(value.registration_id), { ownerToken: value.owner_token, allowEmpty: true, signal: AbortSignal.timeout(3_000) }); }
function requireResponseString(value, maximum) { if (typeof value !== "string" || !value || value.length > maximum || /[\u0000-\u001f\u007f]/.test(value)) throw new NativeControllerError("invalid_response", "The Server returned an invalid response."); return value; }
function sanitizeDevice(value) {
  if (!value || typeof value !== "object") throw new NativeControllerError("invalid_response", "The Server returned an invalid output.");
  const result = {
    id: requireResponseString(value.id, 256), name: requireResponseString(value.name, 256),
    manufacturer: safeText(value.manufacturer, 256), model: safeText(value.model, 256), address: safeText(value.address, 512),
    online: value.online === true, last_seen: safeText(value.last_seen, 128), protocol: safeText(value.protocol, 64),
    capabilities: { play: value.capabilities?.play === true, pause: value.capabilities?.pause === true, stop: value.capabilities?.stop === true, seek: value.capabilities?.seek === true },
    protocol_info: Array.isArray(value.protocol_info) ? value.protocol_info.filter((item) => typeof item === "string" && item.length <= 256) : [],
    pairing_required: value.pairing_required === true, password_required: value.password_required === true,
  };
  return result;
}
function sanitizeDevices(value) {
  if (!Array.isArray(value)) return [];
  const seen = new Set();
  return value.flatMap((item) => {
    if (!item || typeof item.id !== "string" || !item.id || item.id.length > 512 ||
      /[\u0000-\u001f\u007f]/.test(item.id) || seen.has(item.id) || typeof item.name !== "string") {
      return [];
    }
    const name = safeText(item.name, 256);
    if (!name) return [];
    seen.add(item.id);
    return [{ id: item.id, name, is_default: item.is_default === true }];
  });
}
function sanitizeActual(value) {
  if (value === null || value === undefined) return null;
  if (!value || typeof value !== "object" || !["shared", "exclusive"].includes(value.mode)) return null;
  const deviceID = safeText(value.device_id, 512);
  const name = safeText(value.name, 256);
  const sampleRate = positiveSafeInteger(value.sample_rate);
  const channels = positiveSafeInteger(value.channels);
  const containerBits = positiveSafeInteger(value.container_bits);
  const validBits = positiveSafeInteger(value.valid_bits);
  if (!deviceID || !name || !sampleRate || !channels || !containerBits || !validBits || validBits > containerBits) return null;
  return {
    device_id: deviceID, name, mode: value.mode,
    sample_rate: sampleRate, channels, container_bits: containerBits, valid_bits: validBits,
    bit_transparent: value.bit_transparent === true, reason: safeText(value.reason, 512),
  };
}
function sanitizeObservation(value, playID) {
  if (!value || typeof value !== "object" || value.play_id !== playID || !["loaded", "playing", "pause", "stopped", "seeked", "timeupdate", "ended", "error"].includes(value.event)) throw new NativeControllerError("helper_protocol", "Native audio returned an invalid observation.");
  const result = { event: value.event, play_id: playID, position_ms: nonnegativeInteger(value.position_ms) ?? 0, duration_ms: nonnegativeInteger(value.duration_ms) ?? 0, has_position: value.has_position === true };
  if (["playing", "paused"].includes(value.state)) result.state = value.state;
  return result;
}
