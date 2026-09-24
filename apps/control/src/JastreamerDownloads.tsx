import { useCallback, useEffect, useId, useMemo, useRef, useState, type MouseEvent } from "react";
import { createPortal } from "react-dom";
import { api } from "./api";
import { useI18n, type MessageKey } from "./i18n";

const RESPONSE_TIMEOUT_MS = 10_000;
const DOWNLOAD_CONFIRMATION_TIMEOUT_MS = 5 * 60_000;
const MAX_MESSAGE_BYTES = 64 * 1024;
const MAX_ID_BYTES = 512;
const MAX_TARGET_PATH_BYTES = 4_096;
const MAX_STATUS_JOBS = 100;
const POLL_INTERVAL_MS = 1_500;
const textEncoder = new TextEncoder();
let requestSequence = 0;

export type DownloadQuality = "original" | "aac_256";
export type DownloadTarget =
  | { kind: "track" | "album" | "playlist"; id: string }
  | { kind: "folder"; root_id: string; path: string };
export type DownloadStatus = "waiting" | "preparing" | "downloading" | "paused" | "importing" | "completed" | "partial" | "failed" | "cancelled";

export interface JastreamerDownloadsBridge {
  postMessage(message: string): void;
  addEventListener(type: "message", listener: EventListenerOrEventListenerObject): void;
  removeEventListener(type: "message", listener: EventListenerOrEventListenerObject): void;
}

declare global {
  interface Window {
    JastreamerDownloads?: JastreamerDownloadsBridge;
  }
}

export type NativeDownloadJob = {
  id: string;
  status: DownloadStatus;
  completed_tracks: number;
  total_tracks: number;
  failed_tracks: number;
  received_bytes: number;
  total_bytes: number;
  error?: { code: string; message: string };
};
export type NativeDownloadDisplayJob = NativeDownloadJob & {
  title: string;
};

type BridgeAction = "capabilities" | "download" | "status" | "logout" | "open_library" | "configure_network" | "open_downloads";
type PendingRequest = {
  timer: number;
  parse: (value: unknown) => unknown | null;
  resolve: (value: unknown) => void;
  reject: (error: Error) => void;
};

type ServerCapabilities = {
  version: 1;
  qualities: DownloadQuality[];
  max_tracks: number;
};

export type JastreamerDownloads = {
  available: boolean;
  qualities: DownloadQuality[];
  jobs: NativeDownloadDisplayJob[];
  statusError: string;
  statusOpen: boolean;
  jobFor: (target: DownloadTarget) => NativeDownloadJob | undefined;
  start: (target: DownloadTarget, quality: DownloadQuality, title: string) => Promise<string>;
  openStatus: () => void;
  closeStatus: () => void;
  configureNetwork: (jobID: string) => Promise<void>;
  openDownloads: () => Promise<void>;
  openLibrary: () => Promise<void>;
  logout: () => Promise<void>;
};

export class JastreamerDownloadsError extends Error {
  readonly code: string;

  constructor(code: string, message: string) {
    super(message);
    this.name = "JastreamerDownloadsError";
    this.code = code;
  }
}

function record(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? value as Record<string, unknown>
    : null;
}

function hasOnlyKeys(value: Record<string, unknown>, required: string[], optional: string[] = []): boolean {
  const keys = Object.keys(value);
  return required.every((key) => Object.hasOwn(value, key))
    && keys.every((key) => required.includes(key) || optional.includes(key));
}

function boundedString(value: unknown, maxBytes = MAX_ID_BYTES): value is string {
  return typeof value === "string"
    && value.length > 0
    && textEncoder.encode(value).length <= maxBytes
    && !/[\u0000-\u001f\u007f]/u.test(value);
}

function boundedTargetPath(value: unknown): value is string {
  return typeof value === "string"
    && textEncoder.encode(value).length <= MAX_TARGET_PATH_BYTES
    && !/[\u0000-\u001f\u007f]/u.test(value);
}

function nonNegativeInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}
function isDownloadStatus(value: unknown): value is DownloadStatus {
  return value === "waiting"
    || value === "preparing"
    || value === "downloading"
    || value === "paused"
    || value === "importing"
    || value === "completed"
    || value === "partial"
    || value === "failed"
    || value === "cancelled";
}


function parseError(value: unknown): { code: string; message: string } | null {
  const candidate = record(value);
  if (!candidate || !hasOnlyKeys(candidate, ["code", "message"])) return null;
  if (!boundedString(candidate.code, 128) || !boundedString(candidate.message, 4_096)) return null;
  return { code: candidate.code, message: candidate.message };
}

function parseNativeCapabilities(value: unknown): { version: 1 } | null {
  const candidate = record(value);
  return candidate && hasOnlyKeys(candidate, ["version"]) && candidate.version === 1
    ? { version: 1 }
    : null;
}

function parseDownloadResult(value: unknown): { job_id: string } | null {
  const candidate = record(value);
  return candidate && hasOnlyKeys(candidate, ["job_id"]) && boundedString(candidate.job_id)
    ? { job_id: candidate.job_id }
    : null;
}

function parseJob(value: unknown): NativeDownloadJob | null {
  const candidate = record(value);
  if (!candidate || !hasOnlyKeys(candidate, [
    "id", "status", "completed_tracks", "total_tracks", "failed_tracks", "received_bytes", "total_bytes",
  ], ["error"])) return null;
  if (!boundedString(candidate.id)
    || !isDownloadStatus(candidate.status)
    || !nonNegativeInteger(candidate.completed_tracks)
    || !nonNegativeInteger(candidate.total_tracks)
    || !nonNegativeInteger(candidate.failed_tracks)
    || !nonNegativeInteger(candidate.received_bytes)
    || !nonNegativeInteger(candidate.total_bytes)
    || candidate.completed_tracks + candidate.failed_tracks > candidate.total_tracks
    || (candidate.total_bytes > 0 && candidate.received_bytes > candidate.total_bytes)) return null;
  const error = candidate.error === undefined ? undefined : parseError(candidate.error);
  if (candidate.error !== undefined && !error) return null;
  return {
    id: candidate.id,
    status: candidate.status,
    completed_tracks: candidate.completed_tracks,
    total_tracks: candidate.total_tracks,
    failed_tracks: candidate.failed_tracks,
    received_bytes: candidate.received_bytes,
    total_bytes: candidate.total_bytes,
    ...(error ? { error } : {}),
  };
}

function parseStatusResult(value: unknown): { jobs: NativeDownloadJob[] } | null {
  const candidate = record(value);
  if (!candidate || !hasOnlyKeys(candidate, ["jobs"]) || !Array.isArray(candidate.jobs) || candidate.jobs.length > MAX_STATUS_JOBS) return null;
  const jobs: NativeDownloadJob[] = [];
  const ids = new Set<string>();
  for (const value of candidate.jobs) {
    const job = parseJob(value);
    if (!job || ids.has(job.id)) return null;
    ids.add(job.id);
    jobs.push(job);
  }
  return { jobs };
}

function parseEmptyResult(value: unknown): Record<string, never> | null {
  const candidate = record(value);
  return candidate && hasOnlyKeys(candidate, []) ? {} : null;
}

function parseServerCapabilities(value: unknown): ServerCapabilities | null {
  const candidate = record(value);
  if (!candidate || !hasOnlyKeys(candidate, ["version", "qualities", "max_tracks"])
    || candidate.version !== 1
    || !nonNegativeInteger(candidate.max_tracks)
    || candidate.max_tracks === 0
    || !Array.isArray(candidate.qualities)) return null;
  const qualities = candidate.qualities.filter((quality): quality is DownloadQuality => quality === "original" || quality === "aac_256");
  if (qualities.length !== candidate.qualities.length || qualities.length === 0 || new Set(qualities).size !== qualities.length) return null;
  return { version: 1, qualities, max_tracks: candidate.max_tracks };
}

function messageData(event: Event): string | null {
  const data = (event as MessageEvent<unknown>).data;
  if (typeof data !== "string" || data.length > MAX_MESSAGE_BYTES) return null;
  return textEncoder.encode(data).length <= MAX_MESSAGE_BYTES ? data : null;
}

function targetKey(target: DownloadTarget): string {
  return target.kind === "folder"
    ? JSON.stringify(["folder", target.root_id, target.path])
    : JSON.stringify([target.kind, target.id]);
}

function validTarget(target: DownloadTarget): boolean {
  const candidate = record(target);
  if (!candidate) return false;
  if (candidate.kind === "folder") {
    return hasOnlyKeys(candidate, ["kind", "root_id", "path"])
      && boundedString(candidate.root_id)
      && boundedTargetPath(candidate.path);
  }
  return hasOnlyKeys(candidate, ["kind", "id"])
    && (candidate.kind === "track" || candidate.kind === "album" || candidate.kind === "playlist")
    && boundedString(candidate.id);
}

export function nativeDownloadsBridge(): JastreamerDownloadsBridge | null {
  if (typeof window === "undefined" || window.top !== window) return null;
  const bridge = window.JastreamerDownloads;
  return bridge
    && typeof bridge.postMessage === "function"
    && typeof bridge.addEventListener === "function"
    && typeof bridge.removeEventListener === "function"
    ? bridge
    : null;
}

export class JastreamerDownloadsClient {
  private readonly pending = new Map<string, PendingRequest>();
  private disposed = false;
  private readonly receive: EventListener;

  constructor(private readonly bridge: JastreamerDownloadsBridge) {
    this.receive = (event) => {
      const data = messageData(event);
      if (data === null) return;
      let value: unknown;
      try {
        value = JSON.parse(data);
      } catch {
        return;
      }
      const payload = record(value);
      if (!payload || !hasOnlyKeys(payload, ["id"], ["result", "error"]) || !boundedString(payload.id, 128)) return;
      const pending = this.pending.get(payload.id);
      if (!pending) return;
      if ((Object.hasOwn(payload, "result") ? 1 : 0) + (Object.hasOwn(payload, "error") ? 1 : 0) !== 1) {
        this.fail(payload.id, pending, new JastreamerDownloadsError("invalid_bridge_response", "The native download service returned an invalid response."));
        return;
      }
      if (Object.hasOwn(payload, "error")) {
        const error = parseError(payload.error);
        this.fail(payload.id, pending, error
          ? new JastreamerDownloadsError(error.code, error.message)
          : new JastreamerDownloadsError("invalid_bridge_response", "The native download service returned an invalid response."));
        return;
      }
      const result = pending.parse(payload.result);
      if (result === null) {
        this.fail(payload.id, pending, new JastreamerDownloadsError("invalid_bridge_response", "The native download service returned an invalid response."));
        return;
      }
      this.pending.delete(payload.id);
      window.clearTimeout(pending.timer);
      pending.resolve(result);
    };
    bridge.addEventListener("message", this.receive);
  }

  capabilities(): Promise<{ version: 1 }> {
    return this.request("capabilities", {}, parseNativeCapabilities);
  }

  download(target: DownloadTarget, quality: DownloadQuality): Promise<{ job_id: string }> {
    if (!validTarget(target) || (quality !== "original" && quality !== "aac_256")) {
      return Promise.reject(new JastreamerDownloadsError("invalid_request", "The download request is invalid."));
    }
    return this.request("download", { target, quality }, parseDownloadResult, DOWNLOAD_CONFIRMATION_TIMEOUT_MS);
  }

  async status(jobIds: string[]): Promise<NativeDownloadJob[]> {
    if (jobIds.length === 0) return [];
    if (jobIds.length > MAX_STATUS_JOBS
      || new Set(jobIds).size !== jobIds.length
      || jobIds.some((id) => !boundedString(id))) {
      throw new JastreamerDownloadsError("invalid_request", "The download status request is invalid.");
    }
    const result = await this.request("status", { job_ids: jobIds }, parseStatusResult);
    const requested = new Set(jobIds);
    if (result.jobs.some((job) => !requested.has(job.id))) {
      throw new JastreamerDownloadsError("invalid_bridge_response", "The native download service returned an invalid response.");
    }
    return result.jobs;
  }

  async logout(): Promise<void> {
    await this.request("logout", {}, parseEmptyResult);
  }

  async openLibrary(): Promise<void> {
    await this.request("open_library", {}, parseEmptyResult);
  }

  async configureNetwork(jobID: string): Promise<void> {
    if (!boundedString(jobID)) {
      throw new JastreamerDownloadsError("invalid_request", "The download network request is invalid.");
    }
    await this.request("configure_network", { job_id: jobID }, parseEmptyResult, DOWNLOAD_CONFIRMATION_TIMEOUT_MS);
  }

  async openDownloads(): Promise<void> {
    await this.request("open_downloads", {}, parseEmptyResult);
  }

  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    try {
      this.bridge.removeEventListener("message", this.receive);
    } catch {
      // Every request is bounded and disposal still rejects all callers.
    }
    for (const [id, pending] of this.pending) {
      window.clearTimeout(pending.timer);
      pending.reject(new DOMException("Native downloads detached.", "AbortError"));
      this.pending.delete(id);
    }
  }

  private request<T>(action: BridgeAction, fields: Record<string, unknown>, parse: (value: unknown) => T | null, timeout = RESPONSE_TIMEOUT_MS): Promise<T> {
    if (this.disposed) return Promise.reject(new DOMException("Native downloads detached.", "AbortError"));
    const id = `downloads-${Date.now().toString(36)}-${++requestSequence}`;
    return new Promise<T>((resolve, reject) => {
      const pending: PendingRequest = {
        timer: window.setTimeout(() => {
          const current = this.pending.get(id);
          if (current) this.fail(id, current, new JastreamerDownloadsError("bridge_timeout", "The native download service did not respond in time."));
        }, timeout),
        parse,
        resolve: (value) => resolve(value as T),
        reject,
      };
      this.pending.set(id, pending);
      try {
        this.bridge.postMessage(JSON.stringify({ id, action, ...fields }));
      } catch {
        this.fail(id, pending, new JastreamerDownloadsError("bridge_unavailable", "The native download service is unavailable."));
      }
    });
  }

  private fail(id: string, pending: PendingRequest, error: Error): void {
    this.pending.delete(id);
    window.clearTimeout(pending.timer);
    pending.reject(error);
  }
}

function isTerminal(status: string): boolean {
  return status === "completed" || status === "partial" || status === "failed" || status === "cancelled";
}


export function useJastreamerDownloads(authenticated: boolean): JastreamerDownloads {
  const [available, setAvailable] = useState(false);
  const [qualities, setQualities] = useState<DownloadQuality[]>([]);
  const [jobs, setJobs] = useState<Record<string, NativeDownloadJob>>({});
  const [targetJobs, setTargetJobs] = useState<Record<string, string>>({});
  const [jobTitles, setJobTitles] = useState<Record<string, string>>({});
  const [jobOrder, setJobOrder] = useState<string[]>([]);
  const [statusError, setStatusError] = useState("");
  const [statusOpen, setStatusOpen] = useState(false);
  const [pollGeneration, setPollGeneration] = useState(0);
  const clientRef = useRef<JastreamerDownloadsClient | null>(null);
  const enabledRef = useRef(false);
  const jobIdsRef = useRef<Set<string>>(new Set());
  const jobsRef = useRef<Record<string, NativeDownloadJob>>({});
  const refreshingRef = useRef(false);
  const trackingGenerationRef = useRef(0);

  useEffect(() => {
    setAvailable(false);
    setQualities([]);
    enabledRef.current = false;
    trackingGenerationRef.current += 1;
    if (!authenticated) {
      setJobs({});
      setTargetJobs({});
      setJobTitles({});
      setJobOrder([]);
      setStatusOpen(false);
      setStatusError("");
      setPollGeneration(0);
      jobIdsRef.current = new Set();
      jobsRef.current = {};
      return;
    }
    const bridge = nativeDownloadsBridge();
    if (!bridge) return;
    let disposed = false;
    const controller = new AbortController();
    let client: JastreamerDownloadsClient;
    try {
      client = new JastreamerDownloadsClient(bridge);
    } catch {
      return;
    }
    clientRef.current = client;
    void Promise.all([
      client.capabilities(),
      api<unknown>("/downloads/capabilities", { signal: controller.signal }).then((value) => {
        const capabilities = parseServerCapabilities(value);
        if (!capabilities) throw new JastreamerDownloadsError("invalid_server_capabilities", "The server returned invalid download capabilities.");
        return capabilities;
      }),
    ]).then(([, server]) => {
      if (disposed) return;
      enabledRef.current = true;
      setQualities(server.qualities);
      setAvailable(true);
    }).catch(() => {
      if (!disposed) {
        enabledRef.current = false;
        setAvailable(false);
        setQualities([]);
      }
    });
    return () => {
      disposed = true;
      trackingGenerationRef.current += 1;
      controller.abort();
      enabledRef.current = false;
      if (clientRef.current === client) clientRef.current = null;
      client.dispose();
    };
  }, [authenticated]);

  const refreshStatuses = useCallback(async (includeFinished = false) => {
    const client = clientRef.current;
    if (!client || !enabledRef.current || refreshingRef.current) return;
    const ids = includeFinished ? Object.keys(jobsRef.current) : [...jobIdsRef.current];
    if (ids.length === 0) return;
    refreshingRef.current = true;
    const generation = trackingGenerationRef.current;
    try {
      const pages: NativeDownloadJob[][] = [];
      for (let offset = 0; offset < ids.length; offset += MAX_STATUS_JOBS) {
        pages.push(await client.status(ids.slice(offset, offset + MAX_STATUS_JOBS)));
      }
      if (trackingGenerationRef.current !== generation) return;
      const next = pages.flat();
      const returned = new Set(next.map((job) => job.id));
      const removed = new Set(ids.filter((id) => !returned.has(id)));
      const updatedRef = { ...jobsRef.current };
      for (const id of removed) delete updatedRef[id];
      for (const job of next) updatedRef[job.id] = job;
      jobsRef.current = updatedRef;
      jobIdsRef.current = new Set([...jobIdsRef.current].filter((id) => !removed.has(id) && (!updatedRef[id] || !isTerminal(updatedRef[id].status))));
      setJobs((current) => {
        const updated = { ...current };
        for (const id of removed) delete updated[id];
        for (const job of next) updated[job.id] = job;
        return updated;
      });
      if (removed.size > 0) {
        setJobOrder((current) => current.filter((id) => !removed.has(id)));
        setJobTitles((current) => {
          const updated = { ...current };
          for (const id of removed) delete updated[id];
          return updated;
        });
        setTargetJobs((current) => {
          const updated = { ...current };
          for (const [target, id] of Object.entries(current)) {
            if (removed.has(id)) delete updated[target];
          }
          return updated;
        });
      }
      setStatusError("");
    } catch (error) {
      if (trackingGenerationRef.current === generation
        && !(error instanceof DOMException && error.name === "AbortError")) {
        setStatusError(error instanceof Error ? error.message : "The native download service is unavailable.");
      }
    } finally {
      refreshingRef.current = false;
    }
  }, []);

  useEffect(() => {
    if (!available || (statusOpen ? Object.keys(jobsRef.current).length === 0 : jobIdsRef.current.size === 0)) return;
    void refreshStatuses(statusOpen);
    const timer = window.setInterval(() => {
      const hasTrackedJobs = statusOpen ? Object.keys(jobsRef.current).length > 0 : jobIdsRef.current.size > 0;
      if (!hasTrackedJobs) window.clearInterval(timer);
      else void refreshStatuses(statusOpen);
    }, POLL_INTERVAL_MS);
    return () => window.clearInterval(timer);
  }, [available, pollGeneration, refreshStatuses, statusOpen]);

  const start = useCallback(async (target: DownloadTarget, quality: DownloadQuality, title: string): Promise<string> => {
    const client = clientRef.current;
    if (!client || !enabledRef.current || !qualities.includes(quality)) {
      throw new JastreamerDownloadsError("downloads_unavailable", "Downloads are not available in this app or from this server.");
    }
    const result = await client.download(target, quality);
    jobIdsRef.current = new Set(jobIdsRef.current).add(result.job_id);
    setPollGeneration((current) => current + 1);
    setTargetJobs((current) => ({ ...current, [targetKey(target)]: result.job_id }));
    const waitingJob: NativeDownloadJob = {
      id: result.job_id,
      status: "waiting",
      completed_tracks: 0,
      total_tracks: 0,
      failed_tracks: 0,
      received_bytes: 0,
      total_bytes: 0,
    };
    jobsRef.current = { ...jobsRef.current, [result.job_id]: waitingJob };
    setJobs((current) => ({ ...current, [result.job_id]: waitingJob }));
    setJobTitles((current) => ({ ...current, [result.job_id]: title }));
    setJobOrder((current) => current.includes(result.job_id) ? current : [...current, result.job_id]);
    setStatusOpen(true);
    return result.job_id;
  }, [qualities]);

  const logout = useCallback(async () => {
    const client = clientRef.current;
    if (!client) return;
    await client.logout();
    trackingGenerationRef.current += 1;
    jobIdsRef.current = new Set();
    jobsRef.current = {};
    setJobs({});
    setTargetJobs({});
    setJobTitles({});
    setJobOrder([]);
    setStatusOpen(false);
    setStatusError("");
    setPollGeneration((current) => current + 1);
  }, []);

  const openLibrary = useCallback(async () => {
    const client = clientRef.current;
    if (!client) throw new JastreamerDownloadsError("bridge_unavailable", "The native download service is unavailable.");
    await client.openLibrary();
  }, []);

  const configureNetwork = useCallback(async (jobID: string) => {
    const client = clientRef.current;
    if (!client || !enabledRef.current || !jobsRef.current[jobID]) {
      throw new JastreamerDownloadsError("bridge_unavailable", "The native download service is unavailable.");
    }
    await client.configureNetwork(jobID);
    if (isTerminal(jobsRef.current[jobID]?.status ?? "")) return;
    jobIdsRef.current = new Set(jobIdsRef.current).add(jobID);
    setPollGeneration((current) => current + 1);
  }, []);

  const openDownloads = useCallback(async () => {
    const client = clientRef.current;
    if (!client || !enabledRef.current) {
      throw new JastreamerDownloadsError("bridge_unavailable", "The native download service is unavailable.");
    }
    await client.openDownloads();
  }, []);

  const openStatus = useCallback(() => setStatusOpen(true), []);
  const closeStatus = useCallback(() => setStatusOpen(false), []);

  const jobFor = useCallback((target: DownloadTarget) => {
    const id = targetJobs[targetKey(target)];
    return id ? jobs[id] : undefined;
  }, [jobs, targetJobs]);

  const displayJobs = useMemo(() => jobOrder.flatMap((id) => {
    const job = jobs[id];
    return job ? [{ ...job, title: jobTitles[id] ?? "" }] : [];
  }), [jobOrder, jobTitles, jobs]);

  return useMemo(() => ({
    available,
    qualities,
    jobs: displayJobs,
    statusError,
    statusOpen,
    jobFor,
    start,
    openStatus,
    closeStatus,
    configureNetwork,
    openDownloads,
    openLibrary,
    logout,
  }), [
    available,
    closeStatus,
    configureNetwork,
    displayJobs,
    jobFor,
    logout,
    openDownloads,
    openLibrary,
    openStatus,
    qualities,
    start,
    statusError,
    statusOpen,
  ]);
}

function DownloadIcon() {
  return <svg className="native-download-icon" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><path d="M12 3v12m-5-5 5 5 5-5" /><path d="M5 20h14" /></svg>;
}

function formatBytes(bytes: number, locale: string): string {
  if (bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const unit = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const value = bytes / 1024 ** unit;
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: unit === 0 ? 0 : 1 }).format(value)} ${units[unit]}`;
}

function statusKey(status: string): MessageKey {
  switch (status) {
    case "waiting": return "downloads.status.waiting";
    case "preparing": return "downloads.status.preparing";
    case "downloading": return "downloads.status.downloading";
    case "paused": return "downloads.status.paused";
    case "importing": return "downloads.status.importing";
    case "completed": return "downloads.status.completed";
    case "partial": return "downloads.status.partial";
    case "failed": return "downloads.status.failed";
    case "cancelled": return "downloads.status.cancelled";
    default: return "downloads.status.unknown";
  }
}

function downloadErrorText(error: NonNullable<NativeDownloadJob["error"]>, t: (key: MessageKey) => string): string {
  switch (error.code) {
    case "network_unmetered_required": return t("downloads.network.metered");
    case "network_roaming": return t("downloads.network.roaming");
    case "network_unavailable": return t("downloads.network.unavailable");
    default: return error.message;
  }
}

function jobStatus(job: NativeDownloadJob, locale: string, t: (key: MessageKey, params?: Record<string, string | number>) => string): string {
  const errorMessage = job.error ? downloadErrorText(job.error, t) : "";
  if (job.status === "partial") {
    const summary = t("downloads.status.partialCounts", { completed: job.completed_tracks, total: job.total_tracks, failed: job.failed_tracks });
    return errorMessage ? `${summary}: ${errorMessage}` : summary;
  }
  if (job.status === "completed" && job.total_tracks > 1) {
    return t("downloads.status.completedCounts", { completed: job.completed_tracks, total: job.total_tracks });
  }
  if (job.status === "downloading" && job.total_bytes > 0) {
    const percent = Math.min(100, Math.floor((job.received_bytes / job.total_bytes) * 100));
    return t("downloads.status.progress", {
      percent,
      received: formatBytes(job.received_bytes, locale),
      total: formatBytes(job.total_bytes, locale),
    });
  }
  const label = t(statusKey(job.status));
  return errorMessage ? `${label}: ${errorMessage}` : label;
}

function compactStatus(job: NativeDownloadJob): string {
  if (job.status === "partial") return `${job.completed_tracks}/${job.total_tracks}`;
  if (job.status === "completed") return "✓";
  if (job.status === "failed" || job.status === "cancelled") return "!";
  if (job.total_bytes > 0 && job.received_bytes > 0) return `${Math.min(100, Math.floor((job.received_bytes / job.total_bytes) * 100))}%`;
  return "…";
}

function networkActionKey(code: string | undefined): MessageKey | null {
  if (code === "network_unmetered_required") return "downloads.panel.configureMetered";
  if (code === "network_roaming") return "downloads.panel.configureRoaming";
  return null;
}

export function NativeDownloadsSettings({ downloads }: { downloads: JastreamerDownloads }) {
  const { t } = useI18n();
  if (!downloads.available) return null;
  const activeCount = downloads.jobs.filter((job) => !isTerminal(job.status)).length;
  const countLabel = activeCount > 0
    ? t("downloads.panel.activeCount", { count: activeCount })
    : downloads.jobs.length > 0
      ? t("downloads.panel.count", { count: downloads.jobs.length })
      : "";

  return (
    <section className="settings-card" aria-labelledby="download-settings-heading">
      <h2 id="download-settings-heading">{t("downloads.panel.open")}</h2>
      <p className="muted">{t("downloads.settings.description")}</p>
      <button
        className="button button-ghost native-download-status-open"
        type="button"
        data-download-status="true"
        aria-haspopup="dialog"
        aria-expanded={downloads.statusOpen}
        aria-label={t("downloads.panel.openLabel")}
        onClick={downloads.openStatus}
      >
        <DownloadIcon />
        <span>{t("downloads.viewStatus")}</span>
        {countLabel && <span className="native-download-status-count" aria-live="polite">{countLabel}</span>}
      </button>
    </section>
  );
}

export function NativeDownloadsStatus({ downloads }: { downloads: JastreamerDownloads }) {
  const { locale, t } = useI18n();
  const [networkBusy, setNetworkBusy] = useState("");
  const [managerBusy, setManagerBusy] = useState(false);
  const [actionError, setActionError] = useState("");
  const dialogRef = useRef<HTMLElement | null>(null);
  const closeRef = useRef<HTMLButtonElement | null>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    if (!downloads.statusOpen) return;
    previousFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    closeRef.current?.focus();

    function isTopmostDialog(): boolean {
      const dialogs = document.querySelectorAll<HTMLElement>('[aria-modal="true"][role="dialog"], [aria-modal="true"][role="alertdialog"]');
      return dialogs[dialogs.length - 1] === dialogRef.current;
    }

    function handleKeyDown(event: KeyboardEvent) {
      if (!isTopmostDialog()) return;
      if (event.key === "Escape") {
        event.preventDefault();
        downloads.closeStatus();
        return;
      }
      if (event.key !== "Tab") return;
      const focusable = Array.from(dialogRef.current?.querySelectorAll<HTMLElement>(
        'button:not(:disabled), [href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])',
      ) ?? []).filter((element) => !element.hasAttribute("hidden"));
      if (focusable.length === 0) {
        event.preventDefault();
        dialogRef.current?.focus();
        return;
      }
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && (document.activeElement === first || !dialogRef.current?.contains(document.activeElement))) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && (document.activeElement === last || !dialogRef.current?.contains(document.activeElement))) {
        event.preventDefault();
        first.focus();
      }
    }

    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
      const previousFocus = previousFocusRef.current;
      previousFocusRef.current = null;
      if (previousFocus?.isConnected) previousFocus.focus();
    };
  }, [downloads.closeStatus, downloads.statusOpen]);

  if (!downloads.available) return null;

  async function configureNetwork(jobID: string) {
    setNetworkBusy(jobID);
    setActionError("");
    try {
      await downloads.configureNetwork(jobID);
    } catch (error) {
      if (!(error instanceof DOMException && error.name === "AbortError")) {
        setActionError(error instanceof Error ? error.message : t("downloads.panel.configureFailed"));
      }
    } finally {
      setNetworkBusy("");
    }
  }

  async function openNativeDownloads() {
    setManagerBusy(true);
    setActionError("");
    try {
      await downloads.openDownloads();
    } catch (error) {
      if (!(error instanceof DOMException && error.name === "AbortError")) {
        setActionError(error instanceof Error ? error.message : t("downloads.panel.openManagerFailed"));
      }
    } finally {
      setManagerBusy(false);
    }
  }

  return downloads.statusOpen ? createPortal(
        <div className="library-dialog-backdrop native-download-status-backdrop" role="presentation" onMouseDown={(event) => {
          if (event.currentTarget === event.target) downloads.closeStatus();
        }}>
          <section
            ref={dialogRef}
            className="library-dialog native-download-status-dialog"
            role="dialog"
            aria-modal="true"
            aria-labelledby="native-download-status-title"
            tabIndex={-1}
          >
            <div className="library-dialog-heading">
              <div>
                <p className="library-eyebrow">{t("downloads.panel.eyebrow")}</p>
                <h2 id="native-download-status-title">{t("downloads.panel.heading")}</h2>
              </div>
              <button ref={closeRef} className="button button-ghost" type="button" onClick={downloads.closeStatus}>{t("common.close")}</button>
            </div>
            {downloads.statusError && <p className="native-download-status-error" role="status">{t("downloads.status.unavailable")}: {downloads.statusError}</p>}
            {downloads.jobs.length === 0 ? (
              <p className="native-download-status-empty">{t("downloads.panel.empty")}</p>
            ) : (
              <div className="native-download-job-list">
                {[...downloads.jobs].reverse().map((job) => {
                  const percent = job.total_bytes > 0 ? Math.min(100, Math.floor((job.received_bytes / job.total_bytes) * 100)) : 0;
                  const networkKey = networkActionKey(job.error?.code);
                  return (
                    <article className={`native-download-job native-download-job-${job.status}`} key={job.id}>
                      <div className="native-download-job-heading">
                        <strong>{job.title}</strong>
                        <span>{t(statusKey(job.status))}</span>
                      </div>
                      {job.total_tracks > 0 ? (
                        <p>{t("downloads.panel.trackProgress", { completed: job.completed_tracks, total: job.total_tracks, failed: job.failed_tracks })}</p>
                      ) : (
                        <p>{t("downloads.panel.trackPreparing")}</p>
                      )}
                      {job.total_bytes > 0 ? (
                        <>
                          <progress max={job.total_bytes} value={job.received_bytes} aria-label={t("downloads.status.progress", {
                            percent,
                            received: formatBytes(job.received_bytes, locale),
                            total: formatBytes(job.total_bytes, locale),
                          })} />
                          <p>{t("downloads.status.progress", {
                            percent,
                            received: formatBytes(job.received_bytes, locale),
                            total: formatBytes(job.total_bytes, locale),
                          })}</p>
                        </>
                      ) : job.received_bytes > 0 ? (
                        <p>{t("downloads.panel.bytesReceived", { received: formatBytes(job.received_bytes, locale) })}</p>
                      ) : null}
                      {job.error && <p className="native-download-job-error" role="status">{downloadErrorText(job.error, t)}</p>}
                      {networkKey && (
                        <button className="button button-ghost native-download-network-action" type="button" disabled={networkBusy === job.id} onClick={() => void configureNetwork(job.id)}>
                          {t(networkBusy === job.id ? "downloads.panel.configuringNetwork" : networkKey)}
                        </button>
                      )}
                    </article>
                  );
                })}
              </div>
            )}
            {actionError && <p className="error-text native-download-panel-action-error" role="alert">{actionError}</p>}
            <div className="native-download-panel-actions">
              <button className="button button-ghost" type="button" disabled={managerBusy} onClick={() => void openNativeDownloads()}>
                {t(managerBusy ? "downloads.panel.openingManager" : "downloads.panel.openManager")}
              </button>
            </div>
          </section>
        </div>,
        document.body,
  ) : null;
}

export function NativeDownloadAction({
  downloads,
  target,
  title,
  disabled = false,
  variant = "button",
}: {
  downloads: JastreamerDownloads;
  target: DownloadTarget;
  title: string;
  disabled?: boolean;
  variant?: "button" | "icon";
}) {
  const { locale, t } = useI18n();
  const [open, setOpen] = useState(false);
  const [quality, setQuality] = useState<DownloadQuality>(downloads.qualities[0] ?? "original");
  const [submitting, setSubmitting] = useState(false);
  const [requestError, setRequestError] = useState("");
  const [openingLibrary, setOpeningLibrary] = useState(false);
  const [libraryError, setLibraryError] = useState("");
  const dialogID = useId();
  const dialogRef = useRef<HTMLElement | null>(null);
  const closeRef = useRef<HTMLButtonElement | null>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);
  const submittingRef = useRef(submitting);
  submittingRef.current = submitting;
  const job = downloads.jobFor(target);
  const active = Boolean(job && !isTerminal(job.status));
  const saved = job?.status === "completed" || job?.status === "partial";
  const status = downloads.statusError && active ? t("downloads.status.unavailable") : job ? jobStatus(job, locale, t) : "";

  useEffect(() => {
    if (!downloads.available) setOpen(false);
  }, [downloads.available]);

  useEffect(() => {
    if (!downloads.qualities.includes(quality)) setQuality(downloads.qualities[0] ?? "original");
  }, [downloads.qualities, quality]);

  useEffect(() => {
    if (!open) return;
    previousFocusRef.current = document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null;
    closeRef.current?.focus();

    function isTopmostDialog(): boolean {
      const dialogs = document.querySelectorAll<HTMLElement>('[role="alertdialog"][aria-modal="true"]');
      return dialogs[dialogs.length - 1] === dialogRef.current;
    }

    function handleKeyDown(event: KeyboardEvent) {
      if (!isTopmostDialog()) return;
      if (event.key === "Escape") {
        if (!submittingRef.current) {
          event.preventDefault();
          setOpen(false);
        }
        return;
      }
      if (event.key !== "Tab") return;
      const focusable = Array.from(dialogRef.current?.querySelectorAll<HTMLElement>(
        'button:not(:disabled), [href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])',
      ) ?? []).filter((element) => !element.hasAttribute("hidden"));
      if (focusable.length === 0) {
        event.preventDefault();
        dialogRef.current?.focus();
        return;
      }
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && (document.activeElement === first || !dialogRef.current?.contains(document.activeElement))) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && (document.activeElement === last || !dialogRef.current?.contains(document.activeElement))) {
        event.preventDefault();
        first.focus();
      }
    }

    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
      const previousFocus = previousFocusRef.current;
      previousFocusRef.current = null;
      const anotherAlertDialog = Array.from(document.querySelectorAll<HTMLElement>('[role="alertdialog"][aria-modal="true"]'))
        .some((element) => element !== dialogRef.current);
      if (!anotherAlertDialog && previousFocus?.isConnected) previousFocus.focus();
    };
  }, [open]);

  if (!downloads.available) return null;

  function openDialog(event: MouseEvent<HTMLButtonElement>) {
    event.preventDefault();
    event.stopPropagation();
    if (active) {
      downloads.openStatus();
      return;
    }
    setRequestError("");
    setQuality(downloads.qualities.includes("original") ? "original" : downloads.qualities[0] ?? "original");
    setOpen(true);
  }

  async function submit() {
    setSubmitting(true);
    setRequestError("");
    try {
      await downloads.start(target, quality, title);
      setOpen(false);
    } catch (error) {
      if (error instanceof JastreamerDownloadsError && error.code === "cancelled") {
        setOpen(false);
      } else if (!(error instanceof DOMException && error.name === "AbortError")) {
        setRequestError(error instanceof JastreamerDownloadsError ? downloadErrorText(error, t) : error instanceof Error ? error.message : t("downloads.requestFailed"));
      }
    } finally {
      setSubmitting(false);
    }
  }

  async function openSavedLibrary(event: MouseEvent<HTMLButtonElement>) {
    event.preventDefault();
    event.stopPropagation();
    setOpeningLibrary(true);
    setLibraryError("");
    try {
      await downloads.openLibrary();
    } catch (error) {
      if (!(error instanceof DOMException && error.name === "AbortError")) {
        setLibraryError(error instanceof Error ? error.message : t("downloads.openLibraryFailed"));
      }
    } finally {
      setOpeningLibrary(false);
    }
  }

  const buttonLabel = job && !active
    ? t("downloads.againFor", { title })
    : job ? t("downloads.actionWithStatus", { title, status }) : t("downloads.actionFor", { title });
  const dialogTitleID = `${dialogID}-title`;
  const dialogDescriptionID = `${dialogID}-description`;
  const targetID = target.kind === "folder" ? undefined : target.id;
  const targetRootID = target.kind === "folder" ? target.root_id : undefined;
  const targetPath = target.kind === "folder" ? target.path : undefined;

  return (
    <>
      <div className={`native-download-action native-download-action-${variant}${saved ? " native-download-action-saved" : ""}`} onClick={(event) => event.stopPropagation()}>
        <button
          className={variant === "button" ? "button button-ghost native-download-button" : "native-download-icon-button"}
          type="button"
          data-download-kind={target.kind}
          data-download-id={targetID}
          data-download-root-id={targetRootID}
          data-download-path={targetPath}
          disabled={disabled && !active}
          aria-label={buttonLabel}
          title={status || t("downloads.action")}
          onClick={openDialog}
        >
          <DownloadIcon />
          {variant === "button" && <span>{job ? t(active ? "downloads.viewStatus" : "downloads.again") : t("downloads.action")}</span>}
        </button>
        {variant === "icon" && job && <span className={`native-download-compact-status native-download-status-${job.status}`} title={status} aria-hidden="true">{downloads.statusError && active ? "?" : compactStatus(job)}</span>}
        {saved && (
          <button
            className="button button-ghost native-download-open-library"
            type="button"
            data-download-open-library="true"
            disabled={openingLibrary}
            aria-label={t("downloads.viewInSavedMusicFor", { title })}
            onClick={(event) => void openSavedLibrary(event)}
          >
            {t(openingLibrary ? "downloads.openingLibrary" : libraryError ? "downloads.openLibraryFailed" : "downloads.viewInSavedMusic")}
          </button>
        )}
        {libraryError && <span className="library-visually-hidden" role="alert">{libraryError}</span>}
      </div>
      {open && createPortal(
        <div className="library-dialog-backdrop native-download-dialog-backdrop" role="presentation" onMouseDown={(event) => { if (event.currentTarget === event.target && !submittingRef.current) setOpen(false); }}>
          <section
            ref={dialogRef}
            className="library-dialog native-download-dialog"
            role="alertdialog"
            aria-modal="true"
            aria-labelledby={dialogTitleID}
            aria-describedby={dialogDescriptionID}
            tabIndex={-1}
          >
            <div className="library-dialog-heading">
              <div><p className="library-eyebrow">{t("downloads.eyebrow")}</p><h2 id={dialogTitleID}>{t("downloads.heading", { title })}</h2></div>
              <button ref={closeRef} className="button button-ghost" type="button" disabled={submitting} onClick={() => setOpen(false)}>{t("common.close")}</button>
            </div>
            <fieldset className="native-download-quality" disabled={submitting}>
              <legend>{t("downloads.quality.legend")}</legend>
              {downloads.qualities.includes("original") && <label><input type="radio" name={`quality-${dialogID}`} value="original" checked={quality === "original"} onChange={() => setQuality("original")} /><span><strong>{t("downloads.quality.original")}</strong><small>{t("downloads.quality.originalDescription")}</small></span></label>}
              {downloads.qualities.includes("aac_256") && <label><input type="radio" name={`quality-${dialogID}`} value="aac_256" checked={quality === "aac_256"} onChange={() => setQuality("aac_256")} /><span><strong>{t("downloads.quality.spaceSaving")}</strong><small>{t("downloads.quality.spaceSavingDescription")}</small></span></label>}
            </fieldset>
            <p className="native-download-folder-note" id={dialogDescriptionID}>{t("downloads.folderConfirmation")}</p>
            {requestError && <p className="error-text" role="alert">{requestError}</p>}
            <div className="native-download-dialog-actions">
              <button className="button button-ghost" type="button" disabled={submitting} onClick={() => setOpen(false)}>{t("common.cancel")}</button>
              <button className="button button-primary" type="button" data-download-confirm="true" data-download-kind={target.kind} data-download-id={targetID} data-download-root-id={targetRootID} data-download-path={targetPath} disabled={submitting} onClick={() => void submit()}>{t(submitting ? "downloads.requesting" : "downloads.continue")}</button>
            </div>
          </section>
        </div>,
        document.body,
      )}
    </>
  );
}
