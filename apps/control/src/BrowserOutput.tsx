import { forwardRef, useEffect, useImperativeHandle, useRef } from "react";
import { api, ApiError } from "./api";
import type { Device } from "./types";

const OWNER_HEADER = "X-Jastreamer-Browser-Token";
const HEARTBEAT_MS = 2_000;
const MIME_CANDIDATES = [
  "audio/mpeg",
  "audio/mp4; codecs=mp4a.40.2",
  "audio/flac",
  "audio/ogg; codecs=vorbis",
  "audio/ogg; codecs=opus",
  "audio/wav",
] as const;

type BrowserResource = {
  url: string;
  mime: string;
  title: string;
  artist: string;
  album: string;
  artwork_url: string;
  duration_ms: number;
  size: number;
  seekable: boolean;
};

type BrowserCommand = {
  sequence: number;
  action: "set_uri" | "play" | "pause" | "stop" | "seek";
  play_id?: string;
  position_ms?: number;
  resource?: BrowserResource;
};

type Registration = {
  registration_id: string;
  owner_token: string;
  lease_expires_at: string;
  lease_duration_ms: number;
  poll_after_ms: number;
  device: Device;
};

type Lease = {
  lease_expires_at: string;
  lease_duration_ms: number;
  cancel_before_sequence: number;
};

type CommandPoll = Lease & {
  poll_after_ms: number;
  command?: BrowserCommand;
};

type ActiveResource = {
  playID: string;
  url: string;
  terminalSequence?: number;
};

type Observation = {
  event: "loaded" | "playing" | "pause" | "stopped" | "seeked" | "timeupdate" | "ended" | "error";
  play_id: string;
  state?: "playing" | "paused";
  position_ms: number;
  duration_ms: number;
  has_position: boolean;
};

type CommandExecution = {
  sequence: number;
  abort: AbortController;
  retry?: () => void;
};

export interface BrowserOutputHandle {
  connect: () => Promise<Device>;
  rename: (name: string) => Promise<void>;
  retryPlayback: () => void;
}

export interface BrowserOutputProps {
  name: string;
  disconnectedError: string;
  registrationError: string;
  actionError: string;
  onDeviceChange: (device: Device | null) => void;
  onAutoplayBlocked: (blocked: boolean) => void;
  onError: (message: string) => void;
}

function apiMessage(error: unknown, fallback: string): string {
  return error instanceof ApiError ? error.message : fallback;
}

function mediaPosition(audio: HTMLAudioElement): Pick<Observation, "position_ms" | "duration_ms" | "has_position"> {
  const current = Number.isFinite(audio.currentTime) && audio.currentTime >= 0 ? audio.currentTime : 0;
  const duration = Number.isFinite(audio.duration) && audio.duration >= 0 ? audio.duration : 0;
  return {
    position_ms: Math.round(current * 1000),
    duration_ms: Math.round(duration * 1000),
    has_position: Number.isFinite(audio.currentTime),
  };
}

function eventError(signal: AbortSignal): Error {
  return signal.aborted
    ? new DOMException("Browser output command was cancelled.", "AbortError")
    : new Error("Browser audio action failed.");
}

function waitForMediaEvent(
  audio: HTMLAudioElement,
  success: readonly string[],
  signal: AbortSignal,
): Promise<void> {
  return new Promise((resolve, reject) => {
    const cleanup = () => {
      for (const name of success) audio.removeEventListener(name, succeeded);
      audio.removeEventListener("error", failed);
      signal.removeEventListener("abort", failed);
    };
    const succeeded = () => {
      cleanup();
      resolve();
    };
    const failed = () => {
      cleanup();
      reject(eventError(signal));
    };
    for (const name of success) audio.addEventListener(name, succeeded, { once: true });
    audio.addEventListener("error", failed, { once: true });
    signal.addEventListener("abort", failed, { once: true });
  });
}

const BrowserOutput = forwardRef<BrowserOutputHandle, BrowserOutputProps>(function BrowserOutput(
  { name, disconnectedError, registrationError, actionError, onDeviceChange, onAutoplayBlocked, onError },
  ref,
) {
  const audioRef = useRef<HTMLAudioElement>(null);
  const registrationRef = useRef<Registration | null>(null);
  const leaseExpiryRef = useRef(0);
  const resourceRef = useRef<ActiveResource | null>(null);
  const completedSequenceRef = useRef(0);
  const executionRef = useRef<CommandExecution | null>(null);
  const reportChainRef = useRef<Promise<void>>(Promise.resolve());
  const aliveRef = useRef(false);
  const lastTimeReportRef = useRef(0);
  const timeReportPendingRef = useRef(false);
  const mediaEventsRef = useRef<AbortController | null>(null);
  const connectRef = useRef<(() => Promise<Device>) | null>(null);
  const connectionAbortRef = useRef<AbortController | null>(null);
  const messagesRef = useRef({ name, disconnectedError, registrationError, actionError });
  messagesRef.current = { name, disconnectedError, registrationError, actionError };

  const stopLocalMedia = () => {
    const audio = audioRef.current;
    executionRef.current?.abort.abort();
    executionRef.current = null;
    mediaEventsRef.current?.abort();
    mediaEventsRef.current = null;
    resourceRef.current = null;
    completedSequenceRef.current = 0;
    onAutoplayBlocked(false);
    if (!audio) return;
    audio.pause();
    audio.removeAttribute("src");
    audio.load();
  };

  const ownerOptions = (registration: Registration, options: RequestInit = {}): RequestInit => ({
    ...options,
    headers: {
      ...Object.fromEntries(new Headers(options.headers).entries()),
      [OWNER_HEADER]: registration.owner_token,
    },
  });

  const sendReport = async (
    registration: Registration,
    sequence: number,
    result: "succeeded" | "failed" | "",
    observation?: Observation,
    errorCode?: "media_unsupported" | "media_error" | "action_failed",
    signal?: AbortSignal,
  ): Promise<void> => {
    if (!aliveRef.current || registrationRef.current !== registration) {
      throw new DOMException("Browser output disconnected.", "AbortError");
    }
    await api<void>(`/browser-output/registrations/${encodeURIComponent(registration.registration_id)}/reports`, ownerOptions(registration, {
      method: "POST",
      signal: signal ?? executionRef.current?.abort.signal ?? connectionAbortRef.current?.signal,
      body: JSON.stringify({
        sequence,
        ...(result ? { result } : {}),
        ...(errorCode ? { error_code: errorCode } : {}),
        ...(observation ? { observation } : {}),
      }),
    }));
  };

  const queueObservation = (event: Observation["event"], expectedResource: ActiveResource, expectedSequence: number) => {
    const registration = registrationRef.current;
    const resource = resourceRef.current;
    const audio = audioRef.current;
    if (!aliveRef.current || !registration || !resource || resource !== expectedResource ||
      completedSequenceRef.current !== expectedSequence || !audio || expectedSequence === 0) return;
    if (event === "ended" || event === "error") {
      if (resource.terminalSequence === expectedSequence) return;
      resource.terminalSequence = expectedSequence;
    }
    if (event === "timeupdate") {
      const now = performance.now();
      if (audio.ended || timeReportPendingRef.current || now - lastTimeReportRef.current < 900) return;
      lastTimeReportRef.current = now;
      timeReportPendingRef.current = true;
    }
    const observation: Observation = {
      event,
      play_id: resource.playID,
      ...mediaPosition(audio),
      ...(event === "timeupdate" ? { state: audio.paused ? "paused" as const : "playing" as const } : {}),
    };
    const reportSignal = mediaEventsRef.current?.signal;
    reportChainRef.current = reportChainRef.current
      .then(() => {
        if (!aliveRef.current || registrationRef.current !== registration ||
          resourceRef.current !== expectedResource || completedSequenceRef.current !== expectedSequence) return;
        return sendReport(registration, expectedSequence, "", observation, undefined, reportSignal);
      })
      .catch((error: unknown) => {
        const registrationLost = error instanceof ApiError && error.code === "BROWSER_OUTPUT_NOT_FOUND"
          && registrationRef.current === registration;
        const currentReportRejected = error instanceof ApiError
          && error.code === "STALE_BROWSER_REPORT"
          && resourceRef.current === expectedResource
          && expectedSequence === (executionRef.current?.sequence ?? completedSequenceRef.current);
        if (registrationLost || currentReportRejected) stopLocalMedia();
      })
      .finally(() => { if (event === "timeupdate") timeReportPendingRef.current = false; });
  };
  const observeResource = (audio: HTMLAudioElement, resource: ActiveResource, sequence: number) => {
    mediaEventsRef.current?.abort();
    const events = new AbortController();
    mediaEventsRef.current = events;
    audio.addEventListener("ended", () => queueObservation("ended", resource, sequence), { signal: events.signal });
    audio.addEventListener("error", () => queueObservation("error", resource, sequence), { signal: events.signal });
    audio.addEventListener("timeupdate", () => queueObservation("timeupdate", resource, sequence), { signal: events.signal });
  };


  const executeCommand = async (registration: Registration, command: BrowserCommand): Promise<void> => {
    const audio = audioRef.current;
    if (!audio) return;
    const abort = new AbortController();
    const execution: CommandExecution = { sequence: command.sequence, abort };
    executionRef.current = execution;

    const observation = (event: Observation["event"], playID: string): Observation => ({
      event,
      play_id: playID,
      ...mediaPosition(audio),
      state: audio.paused ? "paused" : "playing",
    });

    try {
      switch (command.action) {
        case "set_uri": {
          if (!command.resource || !command.play_id || audio.canPlayType(command.resource.mime) === "") {
            await sendReport(registration, command.sequence, "failed", undefined, "media_unsupported");
            return;
          }
          const mediaURL = new URL(command.resource.url, window.location.origin);
          if (mediaURL.origin !== window.location.origin || !mediaURL.pathname.startsWith("/media/") || mediaURL.username || mediaURL.password || mediaURL.search || mediaURL.hash) {
            await sendReport(registration, command.sequence, "failed", undefined, "media_unsupported");
            return;
          }
          audio.pause();
          audio.removeAttribute("src");
          audio.load();
          mediaEventsRef.current?.abort();
          resourceRef.current = null;
          completedSequenceRef.current = 0;
          onAutoplayBlocked(false);
          const activeResource = { playID: command.play_id, url: command.resource.url };
          resourceRef.current = activeResource;
          observeResource(audio, activeResource, command.sequence);
          audio.src = mediaURL.href;
          audio.load();
          await waitForMediaEvent(audio, ["loadedmetadata", "canplay"], abort.signal);
          await sendReport(registration, command.sequence, "succeeded", observation("loaded", command.play_id));
          break;
        }
        case "play": {
          const resource = resourceRef.current;
          if (!resource || resource.playID !== command.play_id) throw new Error("The active browser media changed.");
          const playing = audio.paused
            ? waitForMediaEvent(audio, ["playing"], abort.signal)
            : Promise.resolve();
          try {
            await Promise.all([audio.play(), playing]);
          } catch (error) {
            if (!(error instanceof DOMException) || error.name !== "NotAllowedError") throw error;
            onAutoplayBlocked(true);
            let retryPlayback!: () => void;
            let cancelRetry!: (reason: unknown) => void;
            const retryGate = new Promise<void>((resolve, reject) => {
              retryPlayback = resolve;
              cancelRetry = reject;
            });
            const cancelled = () => cancelRetry(eventError(abort.signal));
            execution.retry = retryPlayback;
            abort.signal.addEventListener("abort", cancelled, { once: true });
            await retryGate;
            abort.signal.removeEventListener("abort", cancelled);
            await playing;
          }
          onAutoplayBlocked(false);
          await sendReport(registration, command.sequence, "succeeded", observation("playing", resource.playID));
          break;
        }
        case "pause": {
          const resource = resourceRef.current;
          if (!resource || resource.playID !== command.play_id) throw new Error("The active browser media changed.");
          const paused = audio.paused ? Promise.resolve() : waitForMediaEvent(audio, ["pause"], abort.signal);
          audio.pause();
          await paused;
          await sendReport(registration, command.sequence, "succeeded", observation("pause", resource.playID));
          break;
        }
        case "stop": {
          const resource = resourceRef.current;
          if (!resource || resource.playID !== command.play_id) throw new Error("The active browser media changed.");
          mediaEventsRef.current?.abort();
          mediaEventsRef.current = null;
          const emptied = waitForMediaEvent(audio, ["emptied"], abort.signal);
          audio.pause();
          audio.removeAttribute("src");
          audio.load();
          await emptied;
          await sendReport(registration, command.sequence, "succeeded", observation("stopped", resource.playID));
          resourceRef.current = null;
          onAutoplayBlocked(false);
          break;
        }
        case "seek": {
          const resource = resourceRef.current;
          if (!resource || resource.playID !== command.play_id || !Number.isFinite(command.position_ms)) throw new Error("The active browser media changed.");
          const sought = waitForMediaEvent(audio, ["seeked"], abort.signal);
          audio.currentTime = Math.max(0, command.position_ms ?? 0) / 1000;
          await sought;
          await sendReport(registration, command.sequence, "succeeded", observation("seeked", resource.playID));
          break;
        }
      }
      completedSequenceRef.current = command.sequence;
      const resource = resourceRef.current;
      if (resource) {
        observeResource(audio, resource, command.sequence);
        // Media can finish or fail while the command acknowledgment is in flight.
        if (audio.error) queueObservation("error", resource, command.sequence);
        else if (audio.ended) queueObservation("ended", resource, command.sequence);
      }
    } catch (error) {
      if (abort.signal.aborted) return;
      const errorCode = audio.error ? "media_error" : "action_failed";
      try {
        await sendReport(registration, command.sequence, "failed", undefined, errorCode);
      } catch {
        stopLocalMedia();
      }
      onError(apiMessage(error, messagesRef.current.actionError));
    } finally {
      if (executionRef.current === execution) executionRef.current = null;
    }
  };

  useImperativeHandle(ref, () => ({
    connect() {
      return connectRef.current
        ? connectRef.current()
        : Promise.reject(new Error(messagesRef.current.registrationError));
    },
    async rename(nextName) {
      const registration = registrationRef.current;
      if (!registration) return;
      const device = await api<Device>(`/browser-output/registrations/${encodeURIComponent(registration.registration_id)}`, ownerOptions(registration, {
        method: "PUT",
        body: JSON.stringify({ name: nextName }),
        signal: connectionAbortRef.current?.signal,
      }));
      if (registrationRef.current !== registration) throw new Error(messagesRef.current.disconnectedError);
      registration.device = device;
      onDeviceChange(device);
    },
    retryPlayback() {
      const execution = executionRef.current;
      const audio = audioRef.current;
      if (!execution?.retry || !audio) return;
      const retry = execution.retry;
      void audio.play().then(() => retry()).catch(() => onAutoplayBlocked(true));
    },
  }), [onAutoplayBlocked, onDeviceChange]);

  useEffect(() => {
    const audio = audioRef.current;
    if (!audio) return;
    let disposed = false;
    let connecting: Promise<Device> | null = null;
    let pollTimer = 0;
    let heartbeatTimer = 0;
    let expiryTimer = 0;
    let heartbeatPending = false;

    const releaseRegistration = (registration: Registration) => {
      void api<void>(`/browser-output/registrations/${encodeURIComponent(registration.registration_id)}`, ownerOptions(registration, {
        method: "DELETE",
        keepalive: true,
      })).catch(() => undefined);
    };
    const disconnect = () => {
      const registration = registrationRef.current;
      aliveRef.current = false;
      connectionAbortRef.current?.abort();
      connectionAbortRef.current = null;
      window.clearTimeout(pollTimer);
      window.clearInterval(heartbeatTimer);
      window.clearTimeout(expiryTimer);
      stopLocalMedia();
      registrationRef.current = null;
      connecting = null;
      onDeviceChange(null);
      if (registration) releaseRegistration(registration);
    };
    const loseLease = () => {
      const hadMedia = resourceRef.current !== null;
      disconnect();
      if (hadMedia) onError(messagesRef.current.disconnectedError);
    };
    const applyLease = (lease: Lease, startedAt: number) => {
      // A monotonic, conservative deadline does not depend on either host's wall clock.
      if (!Number.isFinite(lease.lease_duration_ms) || lease.lease_duration_ms <= 0 || lease.lease_duration_ms > 60_000) {
        loseLease();
        return;
      }
      leaseExpiryRef.current = startedAt + lease.lease_duration_ms;
      window.clearTimeout(expiryTimer);
      expiryTimer = window.setTimeout(loseLease, Math.max(0, leaseExpiryRef.current - performance.now()));
      const execution = executionRef.current;
      const activeSequence = execution?.sequence ?? completedSequenceRef.current;
      if (activeSequence > 0 && lease.cancel_before_sequence >= activeSequence) stopLocalMedia();
    };
    const heartbeat = async () => {
      const registration = registrationRef.current;
      const connection = connectionAbortRef.current;
      if (!aliveRef.current || !registration || !connection || heartbeatPending) return;
      heartbeatPending = true;
      const startedAt = performance.now();
      try {
        const lease = await api<Lease>(`/browser-output/registrations/${encodeURIComponent(registration.registration_id)}/lease`, ownerOptions(registration, {
          method: "PUT",
          body: JSON.stringify({}),
          signal: connection.signal,
        }));
        if (!connection.signal.aborted) applyLease(lease, startedAt);
        const resource = resourceRef.current;
        if (!connection.signal.aborted && resource && !executionRef.current) {
          queueObservation("timeupdate", resource, completedSequenceRef.current);
        }
      } catch (error) {
        if (connection.signal.aborted) return;
        if (error instanceof ApiError && [401, 403, 404].includes(error.status)) loseLease();
      } finally {
        heartbeatPending = false;
      }
    };
    const poll = async () => {
      const registration = registrationRef.current;
      const connection = connectionAbortRef.current;
      if (!aliveRef.current || !registration || !connection) return;
      let delay = registration.poll_after_ms;
      const startedAt = performance.now();
      try {
        const next = await api<CommandPoll>(`/browser-output/registrations/${encodeURIComponent(registration.registration_id)}/commands`, ownerOptions(registration, {
          signal: connection.signal,
        }));
        if (connection.signal.aborted) return;
        applyLease(next, startedAt);
        delay = Math.max(100, Math.min(next.poll_after_ms, 1_000));
        if (aliveRef.current && next.command && next.command.sequence > completedSequenceRef.current) {
          await executeCommand(registration, next.command);
        }
      } catch (error) {
        if (connection.signal.aborted) return;
        if (error instanceof ApiError && [401, 403, 404].includes(error.status)) {
          loseLease();
          return;
        }
        delay = 1_000;
      }
      if (aliveRef.current && !connection.signal.aborted) pollTimer = window.setTimeout(() => void poll(), delay);
    };
    const connect = (): Promise<Device> => {
      if (disposed) return Promise.reject(new DOMException("Browser output disconnected.", "AbortError"));
      if (registrationRef.current) return Promise.resolve(registrationRef.current.device);
      if (connecting) return connecting;
      const connection = new AbortController();
      connectionAbortRef.current = connection;
      aliveRef.current = true;
      const startedAt = performance.now();
      connecting = (async () => {
        const protocolInfo = MIME_CANDIDATES.filter((value) => audio.canPlayType(value) !== "");
        if (protocolInfo.length === 0) throw new Error(messagesRef.current.registrationError);
        const registration = await api<Registration>("/browser-output/registrations", {
          method: "POST",
          body: JSON.stringify({ name: messagesRef.current.name, protocol_info: protocolInfo }),
          signal: connection.signal,
        });
        if (disposed || connection.signal.aborted) {
          releaseRegistration(registration);
          throw new DOMException("Browser output disconnected.", "AbortError");
        }
        registrationRef.current = registration;
        applyLease({ ...registration, cancel_before_sequence: 0 }, startedAt);
        onDeviceChange(registration.device);
        heartbeatTimer = window.setInterval(() => void heartbeat(), HEARTBEAT_MS);
        void poll();
        return registration.device;
      })().finally(() => { connecting = null; });
      return connecting;
    };

    connectRef.current = connect;
    window.addEventListener("pagehide", disconnect);
    return () => {
      disposed = true;
      connectRef.current = null;
      window.removeEventListener("pagehide", disconnect);
      disconnect();
    };
  }, [onAutoplayBlocked, onDeviceChange, onError]);

  return <audio ref={audioRef} data-jastreamer-browser-output="true" preload="auto" hidden />;
});

export default BrowserOutput;
