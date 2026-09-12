import type { PlayerState, QueueState } from "./types";

const API_PREFIX = "/api/v1";

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

interface ErrorEnvelope {
  error?: {
    code?: string;
    message?: string;
  };
}

export async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  if (!path.startsWith("/") || path.startsWith("//")) {
    throw new Error("API 경로는 /로 시작해야 합니다.");
  }

  const headers = new Headers(options.headers);
  headers.set("Accept", "application/json");
  headers.set("Content-Type", "application/json");
  headers.set("X-Jastreamer-Request", "web");

  let response: Response;
  try {
    response = await fetch(`${API_PREFIX}${path}`, {
      ...options,
      headers,
      credentials: "same-origin",
    });
  } catch (error) {
    if (error instanceof DOMException && error.name === "AbortError") throw error;
    throw new ApiError(0, "NETWORK_ERROR", "서버에 연결할 수 없습니다.");
  }

  if (!response.ok) {
    let envelope: ErrorEnvelope | undefined;
    try {
      envelope = (await response.json()) as ErrorEnvelope;
    } catch {
      envelope = undefined;
    }
    const error = new ApiError(
      response.status,
      envelope?.error?.code ?? "REQUEST_FAILED",
      envelope?.error?.message ?? "요청을 완료하지 못했습니다.",
    );
    if (response.status === 401) {
      window.dispatchEvent(new Event("jastreamer:auth-required"));
    }
    throw error;
  }

  if (response.status === 204 || response.status === 205) {
    return undefined as T;
  }
  const body = await response.text();
  if (!body) return undefined as T;
  try {
    return JSON.parse(body) as T;
  } catch {
    throw new ApiError(response.status, "INVALID_RESPONSE", "서버 응답을 읽을 수 없습니다.");
  }
}


async function waitUntilStopped(): Promise<void> {
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    const state = await api<PlayerState>("/player");
    if (state.state === "stopped" && !state.pending_command) {
      return;
    }
    await new Promise<void>((resolve) => window.setTimeout(resolve, 300));
  }
  throw new ApiError(409, "PLAYER_BUSY", "재생기가 멈출 때까지 기다리지 못했습니다.");
}

export async function playTracks(trackIDs: string[]): Promise<void> {
  if (trackIDs.length === 0) return;

  const player = await api<PlayerState>("/player");
  if (player.state !== "stopped") {
    await api<PlayerState>("/player", {
      method: "POST",
      body: JSON.stringify({ action: "stop" }),
    });
  }
  if (player.state !== "stopped" || player.pending_command) {
    await waitUntilStopped();
  }

  const queue = await api<QueueState>("/queue");
  await api<QueueState>("/queue", {
    method: "POST",
    body: JSON.stringify({
      action: "replace",
      track_ids: trackIDs,
      revision: queue.revision,
    }),
  });
  await api<PlayerState>("/player", {
    method: "POST",
    body: JSON.stringify({ action: "play" }),
  });
}

export async function addTracks(
  trackIDs: string[],
  action: "append" | "next",
): Promise<void> {
  if (trackIDs.length === 0) return;

  const queue = await api<QueueState>("/queue");
  await api<QueueState>("/queue", {
    method: "POST",
    body: JSON.stringify({
      action,
      track_ids: trackIDs,
      revision: queue.revision,
    }),
  });
}
