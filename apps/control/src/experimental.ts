import { useSyncExternalStore } from "react";
import { api } from "./api";

/**
 * "Experimental features" is a client preference of this browser or app profile, isolated by
 * Server UUID plus origin exactly like the saved local output alias. It is not Server
 * configuration: enabling it changes nothing on the Server and sends no playback command.
 */
const storagePrefix = "jastreamer.experimental-features.v1";
const serverIDPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export type ExperimentalStorageFailure = "" | "identity" | "read" | "save";

export interface ExperimentalFeaturesState {
  enabled: boolean;
  /** The Server identity resolved, so an explicit choice can be stored for this device. */
  ready: boolean;
  failure: ExperimentalStorageFailure;
}

/**
 * Runs before the switch is turned off, so an active experimental mode is never left running
 * behind a hidden menu. Rejecting keeps the switch on and surfaces the thrown message.
 */
export type ExperimentalDisableGuard = () => Promise<void>;

const listeners = new Set<() => void>();
let storageKey: string | null = null;
// The explicitly stored choice; null until this device saved one.
let stored: boolean | null = null;
// Migration input: a phone that already turned USB bit-perfect on keeps access to its panel.
let androidUsbDirect = false;
let failure: ExperimentalStorageFailure = "";
let disableGuard: ExperimentalDisableGuard | null = null;
let started = false;
let snapshot: ExperimentalFeaturesState = { enabled: false, ready: false, failure: "" };

function publish(): void {
  const next: ExperimentalFeaturesState = {
    enabled: stored ?? androidUsbDirect,
    ready: storageKey !== null,
    failure,
  };
  if (next.enabled === snapshot.enabled && next.ready === snapshot.ready && next.failure === snapshot.failure) return;
  snapshot = next;
  for (const listener of listeners) listener();
}

function start(): void {
  if (started || typeof window === "undefined") return;
  started = true;
  api<{ product: string; protocol: number; id: string }>("/discovery")
    .then((identity) => {
      if (identity.product !== "jastreamer" || identity.protocol !== 1 || !serverIDPattern.test(identity.id)) {
        throw new Error("unverified Server identity");
      }
      storageKey = `${storagePrefix}:${window.location.origin}:${identity.id}`;
      try {
        const saved = window.localStorage.getItem(storageKey);
        if (saved === "on") stored = true;
        else if (saved === "off") stored = false;
      } catch {
        failure = "read";
      }
      publish();
    })
    .catch(() => {
      failure = "identity";
      publish();
    });
}

function subscribe(listener: () => void): () => void {
  start();
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

function getSnapshot(): ExperimentalFeaturesState {
  return snapshot;
}

export function useExperimentalFeatures(): ExperimentalFeaturesState {
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}

export function setExperimentalDisableGuard(guard: ExperimentalDisableGuard | null): void {
  disableGuard = guard;
}

/**
 * Reports whether the phone's native USB bit-perfect option is on. Devices that enabled it
 * before this switch existed have no stored preference, and keep the panel visible until they
 * make an explicit choice.
 */
export function reportAndroidUsbDirect(enabled: boolean): void {
  if (androidUsbDirect === enabled) return;
  androidUsbDirect = enabled;
  publish();
}

/**
 * Applies an explicit user choice. Turning the switch off first lets the registered guard shut
 * down an active experimental mode; its rejection propagates and leaves the preference on.
 */
export async function requestExperimentalFeatures(next: boolean): Promise<void> {
  if (!next && disableGuard) await disableGuard();
  stored = next;
  if (storageKey === null) {
    failure = "identity";
    publish();
    return;
  }
  try {
    window.localStorage.setItem(storageKey, next ? "on" : "off");
    failure = "";
  } catch {
    failure = "save";
  }
  publish();
}
