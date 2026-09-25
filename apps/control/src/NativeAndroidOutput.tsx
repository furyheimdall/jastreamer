import { forwardRef, useEffect, useImperativeHandle, useRef } from "react";
import type { Device } from "./types";

const RESPONSE_TIMEOUT_MS = 10_000;
const MAX_MESSAGE_BYTES = 4_096;
const textEncoder = new TextEncoder();
let requestSequence = 0;

export interface AndroidAudioBridge {
  postMessage(message: string): void;
  addEventListener(type: "message", listener: EventListenerOrEventListenerObject): void;
  removeEventListener(type: "message", listener: EventListenerOrEventListenerObject): void;
}

declare global {
  interface Window {
    JastreamerAndroidAudio?: AndroidAudioBridge;
  }
}

export type NativeAndroidAudioDevice = { id: string; name: string };

export type NativeAndroidActual = {
  device_id: string;
  name: string;
  mode: "bit_perfect" | "mixed";
  sample_rate: number;
  channels: number;
  container_bits: number;
  valid_bits: number;
  encoding: string;
  bit_transparent: boolean;
  reason: string;
};

export type NativeAndroidAudioState = {
  supported: boolean;
  available: boolean;
  reason: string;
  enabled: boolean;
  devices: NativeAndroidAudioDevice[];
  requested: { bit_perfect: boolean };
  state: "stopped" | "loaded" | "playing" | "paused" | "error";
  can_configure: boolean;
  actual: NativeAndroidActual | null;
  error?: { code: string; message: string };
};

export interface NativeAndroidOutputHandle {
  connect: () => Promise<Device>;
  connectAutomatically: () => Promise<Device | null>;
  disconnect: () => Promise<void>;
  rename: (name: string) => Promise<void>;
  setVolume: (volume: number) => Promise<void>;
  configure: (bitPerfect: boolean) => Promise<void>;
}

interface NativeAndroidOutputProps {
  bridge: AndroidAudioBridge | null;
  name: string;
  registrationError: string;
  bridgeError: string;
  onDeviceChange: (device: Device | null) => void;
  onVolumeChange: (volume: number | null) => void;
  onAudioStateChange: (audio: NativeAndroidAudioState | null) => void;
  onRecoveryChange: (recovering: boolean, message: string) => void;
  onError: (message: string) => void;
}

type BridgeAction = "status" | "connect" | "connect_if_available" | "disconnect" | "rename" | "set_volume" | "configure";

type BridgeArgument = { name: string } | { volume: number } | { bit_perfect: boolean } | undefined;

type NativeState = {
  device: Device | null;
  recovering: boolean;
  volume: number | null;
  audio: NativeAndroidAudioState | null;
  error?: { code: string; message: string };
};

type PendingRequest = {
  action: BridgeAction;
  timer: number;
  resolve: (state: NativeState) => void;
  reject: (error: Error) => void;
};

function record(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? value as Record<string, unknown>
    : null;
}

function stringArray(value: unknown): string[] | null {
  return Array.isArray(value) && value.every((item) => typeof item === "string") ? [...value] : null;
}

function parseDevice(value: unknown): Device | null | undefined {
  if (value === null) return null;
  const candidate = record(value);
  const capabilities = record(candidate?.capabilities);
  const protocolInfo = stringArray(candidate?.protocol_info);
  if (!candidate || !capabilities || !protocolInfo
    || typeof candidate.id !== "string"
    || typeof candidate.name !== "string"
    || candidate.protocol !== "browser"
    || typeof candidate.manufacturer !== "string"
    || typeof candidate.model !== "string"
    || typeof candidate.address !== "string"
    || typeof candidate.online !== "boolean"
    || typeof candidate.last_seen !== "string"
    || typeof capabilities.play !== "boolean"
    || typeof capabilities.pause !== "boolean"
    || typeof capabilities.stop !== "boolean"
    || typeof capabilities.seek !== "boolean"
    || typeof candidate.pairing_required !== "boolean"
    || typeof candidate.password_required !== "boolean") return undefined;

  return {
    id: candidate.id,
    name: candidate.name,
    protocol: "browser",
    manufacturer: candidate.manufacturer,
    model: candidate.model,
    address: candidate.address,
    online: candidate.online,
    last_seen: candidate.last_seen,
    capabilities: {
      play: capabilities.play,
      pause: capabilities.pause,
      stop: capabilities.stop,
      seek: capabilities.seek,
    },
    protocol_info: protocolInfo,
    pairing_required: candidate.pairing_required,
    password_required: candidate.password_required,
  };
}

function positiveInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value > 0;
}

function parseActual(value: unknown): NativeAndroidActual | null | undefined {
  if (value === null || value === undefined) return null;
  const candidate = record(value);
  if (!candidate
    || typeof candidate.device_id !== "string"
    || typeof candidate.name !== "string"
    || (candidate.mode !== "bit_perfect" && candidate.mode !== "mixed")
    || !positiveInteger(candidate.sample_rate)
    || !positiveInteger(candidate.channels)
    || !positiveInteger(candidate.container_bits)
    || !positiveInteger(candidate.valid_bits)
    || candidate.valid_bits > candidate.container_bits
    || typeof candidate.encoding !== "string"
    || typeof candidate.bit_transparent !== "boolean"
    || typeof candidate.reason !== "string") return undefined;
  return {
    device_id: candidate.device_id,
    name: candidate.name,
    mode: candidate.mode,
    sample_rate: candidate.sample_rate,
    channels: candidate.channels,
    container_bits: candidate.container_bits,
    valid_bits: candidate.valid_bits,
    encoding: candidate.encoding,
    bit_transparent: candidate.bit_transparent,
    reason: candidate.reason,
  };
}

function parseAudio(value: unknown): NativeAndroidAudioState | null | undefined {
  if (value === undefined || value === null) return null;
  const candidate = record(value);
  const requested = record(candidate?.requested);
  const actual = parseActual(candidate?.actual);
  if (!candidate || !requested || actual === undefined
    || typeof candidate.supported !== "boolean"
    || typeof candidate.available !== "boolean"
    || typeof candidate.reason !== "string"
    || typeof candidate.enabled !== "boolean"
    || typeof candidate.can_configure !== "boolean"
    || typeof requested.bit_perfect !== "boolean"
    || !Array.isArray(candidate.devices)
    || !["stopped", "loaded", "playing", "paused", "error"].includes(String(candidate.state))) return undefined;

  const devices: NativeAndroidAudioDevice[] = [];
  const deviceIDs = new Set<string>();
  for (const entry of candidate.devices) {
    const item = record(entry);
    if (!item || typeof item.id !== "string" || item.id.length === 0
      || typeof item.name !== "string" || item.name.length === 0 || deviceIDs.has(item.id)) return undefined;
    deviceIDs.add(item.id);
    devices.push({ id: item.id, name: item.name });
  }

  let audioError: NativeAndroidAudioState["error"];
  if (candidate.error !== undefined) {
    const failure = record(candidate.error);
    if (!failure || typeof failure.code !== "string" || typeof failure.message !== "string") return undefined;
    audioError = { code: failure.code, message: failure.message };
  }

  return {
    supported: candidate.supported,
    available: candidate.available,
    reason: candidate.reason,
    enabled: candidate.enabled,
    devices,
    requested: { bit_perfect: requested.bit_perfect },
    state: candidate.state as NativeAndroidAudioState["state"],
    can_configure: candidate.can_configure,
    actual,
    ...(audioError ? { error: audioError } : {}),
  };
}

function parseState(value: Record<string, unknown>, recoveringRequired: boolean): NativeState | null {
  if (!("device" in value)) return null;
  const device = parseDevice(value.device);
  if (device === undefined) return null;
  if (recoveringRequired && typeof value.recovering !== "boolean") return null;
  if (value.recovering !== undefined && typeof value.recovering !== "boolean") return null;

  let volume: number | null = null;
  if (value.volume !== undefined && value.volume !== null) {
    if (typeof value.volume !== "number" || !Number.isFinite(value.volume)
      || value.volume < 0 || value.volume > 1) return null;
    volume = value.volume;
  }

  const audio = parseAudio(value.audio);
  if (audio === undefined) return null;

  let error: NativeState["error"];
  if (value.error !== undefined) {
    const candidate = record(value.error);
    if (!candidate || typeof candidate.code !== "string" || typeof candidate.message !== "string") return null;
    error = { code: candidate.code, message: candidate.message };
  }
  return { device, recovering: value.recovering === true, volume, audio, ...(error ? { error } : {}) };
}

function messageData(event: Event): string | null {
  const data = (event as MessageEvent<unknown>).data;
  if (typeof data !== "string" || data.length > MAX_MESSAGE_BYTES) return null;
  return textEncoder.encode(data).length <= MAX_MESSAGE_BYTES ? data : null;
}

function validOutputName(name: string): boolean {
  return name.length > 0 && name === name.trim() && textEncoder.encode(name).length <= 80;
}

export function nativeAndroidAudioBridge(): AndroidAudioBridge | null {
  if (typeof window === "undefined") return null;
  const bridge = window.JastreamerAndroidAudio;
  return bridge
    && typeof bridge.postMessage === "function"
    && typeof bridge.addEventListener === "function"
    && typeof bridge.removeEventListener === "function"
    ? bridge
    : null;
}

const NativeAndroidOutput = forwardRef<NativeAndroidOutputHandle, NativeAndroidOutputProps>(function NativeAndroidOutput(
  { bridge, name, registrationError, bridgeError, onDeviceChange, onRecoveryChange, onVolumeChange, onAudioStateChange, onError },
  ref,
) {
  const requestRef = useRef<((action: BridgeAction, argument?: BridgeArgument) => Promise<NativeState>) | null>(null);
  const nameRef = useRef(name);
  const registrationErrorRef = useRef(registrationError);
  const bridgeErrorRef = useRef(bridgeError);
  nameRef.current = name;
  registrationErrorRef.current = registrationError;
  bridgeErrorRef.current = bridgeError;

  useImperativeHandle(ref, () => ({
    async connect() {
      if (!validOutputName(nameRef.current)) throw new Error(registrationErrorRef.current);
      const request = requestRef.current;
      if (!request) throw new Error(bridgeErrorRef.current);
      const state = await request("connect", { name: nameRef.current });
      if (!state.device) throw new Error(state.error?.message || registrationErrorRef.current);
      return state.device;
    },
    async connectAutomatically() {
      if (!validOutputName(nameRef.current)) throw new Error(registrationErrorRef.current);
      const request = requestRef.current;
      if (!request) throw new Error(bridgeErrorRef.current);
      return (await request("connect_if_available", { name: nameRef.current })).device;
    },
    async disconnect() {
      const request = requestRef.current;
      if (!request) throw new Error(bridgeErrorRef.current);
      await request("disconnect");
    },
    async rename(nextName) {
      if (!validOutputName(nextName)) throw new Error(registrationErrorRef.current);
      if (!requestRef.current) throw new Error(bridgeErrorRef.current);
      await requestRef.current("rename", { name: nextName });
    },
    async setVolume(volume) {
      if (!Number.isFinite(volume) || volume < 0 || volume > 1) {
        throw new RangeError("Volume must be between 0 and 1.");
      }
      const request = requestRef.current;
      if (!request) throw new Error(bridgeErrorRef.current);
      await request("set_volume", { volume });
    },
    async configure(bitPerfect) {
      const request = requestRef.current;
      if (!request) throw new Error(bridgeErrorRef.current);
      await request("configure", { bit_perfect: bitPerfect });
    },
  }), [bridgeError]);

  useEffect(() => {
    if (!bridge) {
      onDeviceChange(null);
      onRecoveryChange(false, "");
      onVolumeChange(null);
      onAudioStateChange(null);
      onError(bridgeError);
      return;
    }
    let disposed = false;
    let terminalError = "";
    const pending = new Map<string, PendingRequest>();

    const terminalErrorKey = (error: NativeState["error"] | undefined) =>
      error && error.code !== "media_failed" ? `${error.code}\n${error.message}` : "";
    const reportTerminalError = (error: NativeState["error"] | undefined) => {
      // Item failures remain visible in the Server queue. A final Server error
      // still opens a notice, but successful continuation must stay usable.
      const key = terminalErrorKey(error);
      if (!key) {
        terminalError = "";
        return;
      }
      if (terminalError === key) return;
      terminalError = key;
      onError(error?.message || bridgeError);
    };
    const applyState = (state: NativeState, reportTerminal: boolean) => {
      onDeviceChange(state.device);
      onRecoveryChange(state.recovering, state.recovering ? state.error?.message ?? "" : "");
      onVolumeChange(state.volume);
      onAudioStateChange(state.audio);
      if (state.recovering) terminalError = "";
      else if (reportTerminal) reportTerminalError(state.error);
      else terminalError = terminalErrorKey(state.error);
    };
    const failPending = (id: string, pendingRequest: PendingRequest, message: string) => {
      pending.delete(id);
      window.clearTimeout(pendingRequest.timer);
      pendingRequest.reject(new Error(message));
    };
    const receive: EventListener = (event) => {
      const data = messageData(event);
      if (data === null) {
        reportTerminalError({ code: "invalid_bridge_message", message: bridgeError });
        return;
      }
      let value: unknown;
      try {
        value = JSON.parse(data);
      } catch {
        reportTerminalError({ code: "invalid_bridge_message", message: bridgeError });
        return;
      }
      const payload = record(value);
      if (!payload) {
        reportTerminalError({ code: "invalid_bridge_message", message: bridgeError });
        return;
      }

      if (typeof payload.id === "string") {
        const pendingRequest = pending.get(payload.id);
        if (!pendingRequest) return;
        const state = parseState(payload, false);
        if (!state) {
          failPending(payload.id, pendingRequest, bridgeError);
          return;
        }
        pending.delete(payload.id);
        window.clearTimeout(pendingRequest.timer);
        applyState(state, false);
        const terminalActionError = state.error && !state.recovering && pendingRequest.action !== "status"
          ? state.error
          : null;
        if (terminalActionError) {
          terminalError = terminalErrorKey(terminalActionError);
          pendingRequest.reject(new Error(terminalActionError.message));
        } else {
          pendingRequest.resolve(state);
        }
        return;
      }

      if (payload.event !== "state") return;
      const state = parseState(payload, true);
      if (!state) {
        reportTerminalError({ code: "invalid_bridge_state", message: bridgeError });
        return;
      }
      applyState(state, true);
    };

    const request = (action: BridgeAction, argument?: BridgeArgument): Promise<NativeState> => {
      if (disposed) return Promise.reject(new DOMException("Native output detached.", "AbortError"));
      const id = `web-${++requestSequence}`;
      return new Promise<NativeState>((resolve, reject) => {
        const timer = window.setTimeout(() => {
          const pendingRequest = pending.get(id);
          if (!pendingRequest) return;
          pending.delete(id);
          pendingRequest.reject(new Error(bridgeError));
        }, RESPONSE_TIMEOUT_MS);
        pending.set(id, { action, timer, resolve, reject });
        try {
          bridge.postMessage(JSON.stringify({ id, action, ...(argument ?? {}) }));
        } catch {
          const pendingRequest = pending.get(id);
          if (pendingRequest) failPending(id, pendingRequest, bridgeError);
        }
      });
    };

    try {
      bridge.addEventListener("message", receive);
    } catch {
      onError(bridgeError);
      return;
    }
    requestRef.current = request;
    void request("status").catch((error: unknown) => {
      if (!disposed && (!(error instanceof DOMException) || error.name !== "AbortError")) onError(error instanceof Error ? error.message : bridgeError);
    });

    return () => {
      disposed = true;
      requestRef.current = null;
      try {
        bridge.removeEventListener("message", receive);
      } catch {
        // A broken bridge is already surfaced by bounded requests or its state messages.
      }
      for (const pendingRequest of pending.values()) {
        window.clearTimeout(pendingRequest.timer);
        pendingRequest.reject(new DOMException("Native output detached.", "AbortError"));
      }
      pending.clear();
    };
  }, [bridge, bridgeError, onAudioStateChange, onDeviceChange, onError, onRecoveryChange, onVolumeChange]);

  return null;
});

export default NativeAndroidOutput;
