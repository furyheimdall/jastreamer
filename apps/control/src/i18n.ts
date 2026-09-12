import { useSyncExternalStore } from "react";
import { appMessages } from "./locales/app";
import { commonMessages } from "./locales/common";
import { libraryMessages } from "./locales/library";
import { playerMessages } from "./locales/player";

export type Language = "en" | "ko";
const storageKey = "jastreamer.language";
const cookieName = "jastreamer_language";
const messages = { ...commonMessages, ...appMessages, ...libraryMessages, ...playerMessages };
export type MessageKey = keyof typeof messages;

function isLanguage(value: unknown): value is Language {
  return value === "en" || value === "ko";
}

function cookieLanguage(): Language | null {
  if (typeof document === "undefined") return null;
  try {
    const prefix = `${cookieName}=`;
    const value = document.cookie.split(";").map((part) => part.trim()).find((part) => part.startsWith(prefix))?.slice(prefix.length);
    return isLanguage(value) ? value : null;
  } catch {
    return null;
  }
}

function storedLanguage(): Language {
  const cookie = cookieLanguage();
  if (cookie) return cookie;
  try {
    const saved = typeof window === "undefined" ? null : window.localStorage.getItem(storageKey);
    if (isLanguage(saved)) return saved;
  } catch {
    // Storage can be unavailable; the initial language remains English.
  }
  return "en";
}

let language = storedLanguage();
const listeners = new Set<() => void>();

function applyLanguage(next: Language): void {
  if (typeof document !== "undefined") document.documentElement.lang = next;
  if (language === next) return;
  language = next;
  for (const listener of listeners) listener();
}

export function getLanguage(): Language {
  return language;
}

export function getLocale(): "en-US" | "ko-KR" {
  return language === "ko" ? "ko-KR" : "en-US";
}

export function setLanguage(next: Language): boolean {
  if (!isLanguage(next)) return false;
  let persisted = false;
  try {
    // This non-secret preference also lets the trusted desktop shell follow
    // language changes without exposing native IPC to the remote Web view.
    const secure = window.location.protocol === "https:" ? "; Secure" : "";
    document.cookie = `${cookieName}=${next}; Path=/; Max-Age=31536000; SameSite=Strict${secure}`;
    persisted = cookieLanguage() === next;
  } catch {
    // localStorage can still persist the preference when cookies are blocked.
  }
  try {
    window.localStorage.setItem(storageKey, next);
    persisted = true;
  } catch {
    // Apply in memory and let the caller report a persistence failure.
  }
  applyLanguage(next);
  return persisted;
}

export function t(key: MessageKey, params?: Record<string, string | number>): string {
  const message = messages[key][language];
  return params ? message.replace(/\{([A-Za-z0-9_]+)\}/g, (match, name: string) => Object.hasOwn(params, name) ? String(params[name]) : match) : message;
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}

const snapshots = {
  en: { language: "en", locale: "en-US", t, setLanguage },
  ko: { language: "ko", locale: "ko-KR", t, setLanguage },
} as const;

export function useI18n() {
  const selected = useSyncExternalStore(subscribe, getLanguage, () => "en" as const);
  return snapshots[selected];
}

if (typeof window !== "undefined") {
  applyLanguage(language);
  window.addEventListener("storage", (event) => {
    if (event.key === storageKey || event.key === null) {
      applyLanguage(isLanguage(event.newValue) ? event.newValue : storedLanguage());
    }
  });
}
