import { forwardRef, useEffect, useImperativeHandle, useRef } from "react";
import type { Device } from "./types";

const RESPONSE_TIMEOUT_MS = 10_000;
// The state message carries the bounded mixer diagnostics list, the parsed USB descriptor
// capabilities of the direct output test and the output state.
const MAX_MESSAGE_BYTES = 32_768;
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

export type NativeAndroidMixerEntry = {
  bit_perfect: boolean;
  encoding: number;
  encoding_label: string;
  sample_rate: number;
  channel_mask: number;
  channel_index_mask: number;
  rejection: string;
};

export type NativeAndroidMixerReport = {
  device_id: string;
  device_name: string;
  device_type: string;
  total: number;
  bit_perfect: number;
  usable_bit_perfect: number;
  rejected: number;
  entries: NativeAndroidMixerEntry[];
};

export type NativeAndroidUsbDirectFormat = {
  bits: number;
  subslot_bytes: number;
  channels: number;
  sync: string;
  feedback: boolean;
  rates: number[];
};

export type NativeAndroidUsbDirect = {
  state: "idle" | "opening" | "playing" | "error";
  device_name: string;
  can_start: boolean;
  track: string;
  decoder: string;
  decoder_encoding: string;
  uac_version: number;
  high_speed: boolean;
  formats: NativeAndroidUsbDirectFormat[];
  requested: { sample_rate: number; bits: number; channels: number };
  actual: {
    sample_rate: number;
    bits: number;
    channels: number;
    subslot_bytes: number;
    sync: string;
    feedback: boolean;
  };
  packets: number;
  underruns: number;
  feedback_rate: number;
  error?: { code: string; message: string };
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
  api_level: number;
  mixer_report: NativeAndroidMixerReport[];
  actual: NativeAndroidActual | null;
  // Debug builds only: the direct USB output test never ships in a release build.
  debug_build: boolean;
  usb_direct: NativeAndroidUsbDirect | null;
  error?: { code: string; message: string };
};

export interface NativeAndroidOutputHandle {
  connect: () => Promise<Device>;
  connectAutomatically: () => Promise<Device | null>;
  disconnect: () => Promise<void>;
  rename: (name: string) => Promise<void>;
  setVolume: (volume: number) => Promise<void>;
  configure: (bitPerfect: boolean) => Promise<void>;
  usbDirectScan: () => Promise<void>;
  usbDirectStart: () => Promise<void>;
  usbDirectStop: () => Promise<void>;
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

type BridgeAction = "status" | "connect" | "connect_if_available" | "disconnect" | "rename" | "set_volume" | "configure"
  | "usb_direct_scan" | "usb_direct_start" | "usb_direct_stop";

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

function nonNegativeInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}

function parseMixerReport(value: unknown): NativeAndroidMixerReport[] | undefined {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) return undefined;
  const reports: NativeAndroidMixerReport[] = [];
  for (const item of value) {
    const report = record(item);
    if (!report
      || typeof report.device_id !== "string"
      || typeof report.device_name !== "string"
      || typeof report.device_type !== "string"
      || !nonNegativeInteger(report.total)
      || !nonNegativeInteger(report.bit_perfect)
      || !nonNegativeInteger(report.usable_bit_perfect)
      || !nonNegativeInteger(report.rejected)
      || !Array.isArray(report.entries)) return undefined;
    const entries: NativeAndroidMixerEntry[] = [];
    for (const rawEntry of report.entries) {
      const entry = record(rawEntry);
      if (!entry
        || typeof entry.bit_perfect !== "boolean"
        || !Number.isSafeInteger(entry.encoding)
        || typeof entry.encoding_label !== "string"
        || !nonNegativeInteger(entry.sample_rate)
        || !Number.isSafeInteger(entry.channel_mask)
        || !Number.isSafeInteger(entry.channel_index_mask)
        || typeof entry.rejection !== "string") return undefined;
      entries.push({
        bit_perfect: entry.bit_perfect,
        encoding: entry.encoding as number,
        encoding_label: entry.encoding_label,
        sample_rate: entry.sample_rate,
        channel_mask: entry.channel_mask as number,
        channel_index_mask: entry.channel_index_mask as number,
        rejection: entry.rejection,
      });
    }
    reports.push({
      device_id: report.device_id,
      device_name: report.device_name,
      device_type: report.device_type,
      total: report.total,
      bit_perfect: report.bit_perfect,
      usable_bit_perfect: report.usable_bit_perfect,
      rejected: report.rejected,
      entries,
    });
  }
  return reports;
}

// A USB audio class device exposes a handful of streaming alternate settings, each with a
// short discrete rate list: anything longer is a malformed report rather than a large DAC.
const MAX_USB_DIRECT_FORMATS = 16;
const MAX_USB_DIRECT_RATES = 64;

function parseUsbDirect(value: unknown): NativeAndroidUsbDirect | null | undefined {
  if (value === undefined || value === null) return null;
  const candidate = record(value);
  const requested = record(candidate?.requested);
  const actual = record(candidate?.actual);
  if (!candidate || !requested || !actual
    || !["idle", "opening", "playing", "error"].includes(String(candidate.state))
    || typeof candidate.device_name !== "string"
    || typeof candidate.can_start !== "boolean"
    || typeof candidate.track !== "string"
    || typeof candidate.decoder !== "string"
    || typeof candidate.decoder_encoding !== "string"
    || !nonNegativeInteger(candidate.uac_version)
    || typeof candidate.high_speed !== "boolean"
    || !nonNegativeInteger(candidate.packets)
    || !nonNegativeInteger(candidate.underruns)
    || !nonNegativeInteger(candidate.feedback_rate)
    || !Array.isArray(candidate.formats) || candidate.formats.length > MAX_USB_DIRECT_FORMATS
    || !nonNegativeInteger(requested.sample_rate)
    || !nonNegativeInteger(requested.bits)
    || !nonNegativeInteger(requested.channels)
    || !nonNegativeInteger(actual.sample_rate)
    || !nonNegativeInteger(actual.bits)
    || !nonNegativeInteger(actual.channels)
    || !nonNegativeInteger(actual.subslot_bytes)
    || typeof actual.sync !== "string"
    || typeof actual.feedback !== "boolean") return undefined;

  const formats: NativeAndroidUsbDirectFormat[] = [];
  for (const rawFormat of candidate.formats) {
    const format = record(rawFormat);
    if (!format
      || !nonNegativeInteger(format.bits)
      || !nonNegativeInteger(format.subslot_bytes)
      || !nonNegativeInteger(format.channels)
      || typeof format.sync !== "string"
      || typeof format.feedback !== "boolean"
      || !Array.isArray(format.rates)
      || format.rates.length > MAX_USB_DIRECT_RATES) return undefined;
    const rates: number[] = [];
    for (const rate of format.rates) {
      if (!nonNegativeInteger(rate)) return undefined;
      rates.push(rate);
    }
    formats.push({
      bits: format.bits,
      subslot_bytes: format.subslot_bytes,
      channels: format.channels,
      sync: format.sync,
      feedback: format.feedback,
      rates,
    });
  }

  let usbError: NativeAndroidUsbDirect["error"];
  if (candidate.error !== undefined) {
    const failure = record(candidate.error);
    if (!failure || typeof failure.code !== "string" || typeof failure.message !== "string") return undefined;
    usbError = { code: failure.code, message: failure.message };
  }

  return {
    state: candidate.state as NativeAndroidUsbDirect["state"],
    device_name: candidate.device_name,
    can_start: candidate.can_start,
    track: candidate.track,
    decoder: candidate.decoder,
    decoder_encoding: candidate.decoder_encoding,
    uac_version: candidate.uac_version,
    high_speed: candidate.high_speed,
    formats,
    requested: {
      sample_rate: requested.sample_rate,
      bits: requested.bits,
      channels: requested.channels,
    },
    actual: {
      sample_rate: actual.sample_rate,
      bits: actual.bits,
      channels: actual.channels,
      subslot_bytes: actual.subslot_bytes,
      sync: actual.sync,
      feedback: actual.feedback,
    },
    packets: candidate.packets,
    underruns: candidate.underruns,
    feedback_rate: candidate.feedback_rate,
    ...(usbError ? { error: usbError } : {}),
  };
}

function parseAudio(value: unknown): NativeAndroidAudioState | null | undefined {
  if (value === undefined || value === null) return null;
  const candidate = record(value);
  const requested = record(candidate?.requested);
  const actual = parseActual(candidate?.actual);
  const mixerReport = parseMixerReport(candidate?.mixer_report);
  const usbDirect = parseUsbDirect(candidate?.usb_direct);
  if (!candidate || !requested || actual === undefined || mixerReport === undefined || usbDirect === undefined
    || (candidate.api_level !== undefined && !nonNegativeInteger(candidate.api_level))
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
    api_level: nonNegativeInteger(candidate.api_level) ? candidate.api_level : 0,
    mixer_report: mixerReport,
    actual,
    debug_build: candidate.debug_build === true,
    usb_direct: usbDirect,
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
    async usbDirectScan() {
      const request = requestRef.current;
      if (!request) throw new Error(bridgeErrorRef.current);
      await request("usb_direct_scan");
    },
    async usbDirectStart() {
      const request = requestRef.current;
      if (!request) throw new Error(bridgeErrorRef.current);
      await request("usb_direct_start");
    },
    async usbDirectStop() {
      const request = requestRef.current;
      if (!request) throw new Error(bridgeErrorRef.current);
      await request("usb_direct_stop");
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
