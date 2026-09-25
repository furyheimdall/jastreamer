import { forwardRef, useCallback, useEffect, useImperativeHandle, useRef } from "react";
import type { Device } from "./types";

const textEncoder = new TextEncoder();

export type NativeWindowsDevice = {
  id: string;
  name: string;
  is_default: boolean;
};

export type NativeWindowsActual = {
  device_id: string;
  name: string;
  mode: "shared" | "exclusive";
  sample_rate: number;
  channels: number;
  container_bits: number;
  valid_bits: number;
  bit_transparent: boolean;
  reason: string;
};

export type NativeWindowsState = {
  device: Device | null;
  recovering: false;
  volume: number | null;
  error?: { code: string; message: string };
  audio: {
    available: boolean;
    enabled: boolean;
    devices: NativeWindowsDevice[];
    requested: { device_id: string; exclusive: boolean };
    state: "stopped" | "loaded" | "playing" | "paused" | "error";
    actual: NativeWindowsActual | null;
    can_configure: boolean;
  };
};

export interface JastreamerWindowsAudioBridge {
  request(action: string, args?: Record<string, unknown>): Promise<NativeWindowsState>;
  subscribe(listener: (state: NativeWindowsState) => void): () => void;
}

declare global {
  interface Window {
    JastreamerWindowsAudio?: JastreamerWindowsAudioBridge;
  }
}

export type NativeWindowsConfiguration = {
  enabled: boolean;
  device_id: string;
  exclusive: boolean;
};

export interface NativeWindowsOutputHandle {
  connect: () => Promise<Device>;
  connectAutomatically: () => Promise<Device | null>;
  disconnect: () => Promise<void>;
  rename: (name: string) => Promise<void>;
  setVolume: (volume: number) => Promise<void>;
}

interface NativeWindowsOutputProps {
  bridge: JastreamerWindowsAudioBridge;
  state: NativeWindowsState;
  name: string;
  registrationError: string;
  bridgeError: string;
  onStateChange: (state: NativeWindowsState) => void;
  onDeviceChange: (device: Device | null) => void;
  onVolumeChange: (volume: number | null) => void;
  onRecoveryChange: (recovering: boolean, message: string) => void;
  onError: (message: string) => void;
}

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

function parseActual(value: unknown): NativeWindowsActual | null | undefined {
  if (value === null) return null;
  const candidate = record(value);
  if (!candidate
    || typeof candidate.device_id !== "string" || candidate.device_id.length === 0
    || typeof candidate.name !== "string" || candidate.name.length === 0
    || (candidate.mode !== "shared" && candidate.mode !== "exclusive")
    || !positiveInteger(candidate.sample_rate)
    || !positiveInteger(candidate.channels)
    || !positiveInteger(candidate.container_bits)
    || !positiveInteger(candidate.valid_bits)
    || candidate.valid_bits > candidate.container_bits
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
    bit_transparent: candidate.bit_transparent,
    reason: candidate.reason,
  };
}

export function parseNativeWindowsState(value: unknown): NativeWindowsState | null {
  const candidate = record(value);
  const audio = record(candidate?.audio);
  const requested = record(audio?.requested);
  if (!candidate || !audio || !requested || candidate.recovering !== false) return null;
  const device = parseDevice(candidate.device);
  const actual = parseActual(audio.actual);
  if (device === undefined || actual === undefined
    || typeof audio.available !== "boolean"
    || typeof audio.enabled !== "boolean"
    || !Array.isArray(audio.devices)
    || typeof requested.device_id !== "string" || requested.device_id.length === 0
    || typeof requested.exclusive !== "boolean"
    || !["stopped", "loaded", "playing", "paused", "error"].includes(String(audio.state))
    || typeof audio.can_configure !== "boolean") return null;

  const devices: NativeWindowsDevice[] = [];
  const deviceIDs = new Set<string>();
  for (const value of audio.devices) {
    const item = record(value);
    if (!item || typeof item.id !== "string" || item.id.length === 0
      || typeof item.name !== "string" || item.name.length === 0
      || typeof item.is_default !== "boolean" || deviceIDs.has(item.id)) return null;
    deviceIDs.add(item.id);
    devices.push({ id: item.id, name: item.name, is_default: item.is_default });
  }

  let volume: number | null = null;
  if (candidate.volume !== null) {
    if (typeof candidate.volume !== "number" || !Number.isFinite(candidate.volume)
      || candidate.volume < 0 || candidate.volume > 1) return null;
    volume = candidate.volume;
  }

  let error: NativeWindowsState["error"];
  if (candidate.error !== undefined) {
    const parsedError = record(candidate.error);
    if (!parsedError || typeof parsedError.code !== "string" || typeof parsedError.message !== "string") return null;
    error = { code: parsedError.code, message: parsedError.message };
  }

  return {
    device,
    recovering: false,
    volume,
    ...(error ? { error } : {}),
    audio: {
      available: audio.available,
      enabled: audio.enabled,
      devices,
      requested: { device_id: requested.device_id, exclusive: requested.exclusive },
      state: audio.state as NativeWindowsState["audio"]["state"],
      actual,
      can_configure: audio.can_configure,
    },
  };
}

export function nativeWindowsAudioBridge(): JastreamerWindowsAudioBridge | null {
  if (typeof window === "undefined" || window.top !== window) return null;
  const bridge = window.JastreamerWindowsAudio;
  return bridge && typeof bridge.request === "function" && typeof bridge.subscribe === "function" ? bridge : null;
}

export async function requestNativeWindowsAudio(
  bridge: JastreamerWindowsAudioBridge,
  action: string,
  args?: Record<string, unknown>,
): Promise<NativeWindowsState> {
  const state = parseNativeWindowsState(await bridge.request(action, args));
  if (!state) throw new Error();
  return state;
}

function validOutputName(name: string): boolean {
  return name.length > 0 && name === name.trim() && textEncoder.encode(name).length <= 80;
}

const NativeWindowsOutput = forwardRef<NativeWindowsOutputHandle, NativeWindowsOutputProps>(function NativeWindowsOutput(
  { bridge, state, name, registrationError, bridgeError, onStateChange, onDeviceChange, onVolumeChange, onRecoveryChange, onError },
  ref,
) {
  const propsRef = useRef({ name, registrationError, bridgeError, onStateChange });
  propsRef.current = { name, registrationError, bridgeError, onStateChange };
  const reportedErrorRef = useRef("");

  const request = useCallback(async (action: string, args?: Record<string, unknown>): Promise<NativeWindowsState> => {
    try {
      const next = await requestNativeWindowsAudio(bridge, action, args);
      propsRef.current.onStateChange(next);
      return next;
    } catch (caught) {
      if (caught instanceof Error && caught.message) throw caught;
      throw new Error(propsRef.current.bridgeError);
    }
  }, [bridge]);

  useImperativeHandle(ref, () => ({
    async connect() {
      const current = propsRef.current;
      if (!validOutputName(current.name)) throw new Error(current.registrationError);
      const next = await request("connect", { name: current.name });
      if (!next.device) throw new Error(next.error?.message || current.registrationError);
      return next.device;
    },
    async connectAutomatically() {
      const current = propsRef.current;
      if (!validOutputName(current.name)) throw new Error(current.registrationError);
      return (await request("connect_if_available", { name: current.name })).device;
    },
    async disconnect() {
      await request("disconnect");
    },
    async rename(nextName) {
      const current = propsRef.current;
      if (!validOutputName(nextName)) throw new Error(current.registrationError);
      await request("rename", { name: nextName });
    },
    async setVolume(volume) {
      if (!Number.isFinite(volume) || volume < 0 || volume > 1) {
        throw new Error(propsRef.current.bridgeError);
      }
      await request("set_volume", { volume });
    },
  }), [request]);

  useEffect(() => {
    onDeviceChange(state.device);
    onRecoveryChange(false, "");
    onVolumeChange(state.volume);
    const errorKey = state.error && state.error.code !== "media_failed"
      ? `${state.error.code}\n${state.error.message}`
      : "";
    if (errorKey && reportedErrorRef.current !== errorKey) onError(state.error?.message || bridgeError);
    reportedErrorRef.current = errorKey;
  }, [bridgeError, onDeviceChange, onError, onRecoveryChange, onVolumeChange, state]);

  return null;
});

export default NativeWindowsOutput;
