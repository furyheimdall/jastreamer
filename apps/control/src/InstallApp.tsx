import { useEffect, useState, useSyncExternalStore } from "react";
import { isAppleMobile } from "./device";
import "./InstallApp.css";
import { useI18n } from "./i18n";

interface BeforeInstallPromptEvent extends Event {
  prompt: () => Promise<void>;
  userChoice: Promise<{ outcome: "accepted" | "dismissed"; platform: string }>;
}

interface InstallationSnapshot {
  installed: boolean;
  prompt: BeforeInstallPromptEvent | null;
}

const installationListeners = new Set<() => void>();
const serverSnapshot: InstallationSnapshot = { installed: false, prompt: null };

function isStandalone(): boolean {
  if (typeof window === "undefined") return false;
  const navigatorWithStandalone = navigator as Navigator & { standalone?: boolean };
  return window.matchMedia("(display-mode: standalone)").matches || navigatorWithStandalone.standalone === true;
}

let installationSnapshot: InstallationSnapshot = {
  installed: isStandalone(),
  prompt: null,
};

function publishInstallationSnapshot(next: InstallationSnapshot): void {
  if (
    installationSnapshot.installed === next.installed
    && installationSnapshot.prompt === next.prompt
  ) return;
  installationSnapshot = next;
  for (const listener of installationListeners) listener();
}

function isUsableInstallPrompt(event: Event): event is BeforeInstallPromptEvent {
  const candidate = event as Event & {
    prompt?: unknown;
    userChoice?: { then?: unknown };
  };
  return typeof candidate.prompt === "function"
    && typeof candidate.userChoice?.then === "function";
}

function subscribeInstallation(listener: () => void): () => void {
  installationListeners.add(listener);
  return () => { installationListeners.delete(listener); };
}

function getInstallationSnapshot(): InstallationSnapshot {
  return installationSnapshot;
}

function consumeInstallPrompt(prompt: BeforeInstallPromptEvent): void {
  if (installationSnapshot.prompt !== prompt) return;
  publishInstallationSnapshot({ ...installationSnapshot, prompt: null });
}


function isLocalDevelopmentOrigin(): boolean {
  const hostname = window.location.hostname;
  return window.location.protocol === "http:"
    && (hostname === "localhost" || hostname === "127.0.0.1" || hostname === "::1" || hostname === "[::1]");
}


if (typeof window !== "undefined") {
  window.addEventListener("beforeinstallprompt", (event: Event) => {
    if (!isUsableInstallPrompt(event) || installationSnapshot.installed) return;
    event.preventDefault();
    publishInstallationSnapshot({ installed: false, prompt: event });
  });

  window.addEventListener("appinstalled", () => {
    publishInstallationSnapshot({ installed: true, prompt: null });
  });

  window.matchMedia("(display-mode: standalone)").addEventListener("change", () => {
    publishInstallationSnapshot({ installed: isStandalone(), prompt: null });
  });
}

type InstallResult = "idle" | "accepted" | "dismissed" | "failed";

export default function InstallApp() {
  const { t } = useI18n();
  const installation = useSyncExternalStore(
    subscribeInstallation,
    getInstallationSnapshot,
    () => serverSnapshot,
  );
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<InstallResult>("idle");

  useEffect(() => {
    if (installation.prompt) setResult("idle");
  }, [installation.prompt]);

  if (/(?:^|\s)Electron\/\d/i.test(navigator.userAgent)) return null;

  const localDevelopmentOrigin = isLocalDevelopmentOrigin();
  const trustedOrigin = window.isSecureContext && (window.location.protocol === "https:" || localDevelopmentOrigin);

  async function install(): Promise<void> {
    const prompt = installation.prompt;
    if (!prompt || busy) return;

    consumeInstallPrompt(prompt);
    setBusy(true);
    setResult("idle");
    try {
      await prompt.prompt();
      const choice = await prompt.userChoice;
      if (!getInstallationSnapshot().installed) {
        setResult(choice.outcome === "accepted" ? "accepted" : "dismissed");
      }
    } catch {
      setResult("failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="settings-card install-app-card" aria-labelledby="install-app-heading">
      <div className="install-app-heading">
        <img className="install-app-icon" src="/icons/jastreamer-192.png" alt="" width="56" height="56" />
        <div>
          <h2 id="install-app-heading">{t("pwa.title")}</h2>
          <p className="muted">{t("pwa.description")}</p>
        </div>
      </div>

      {installation.installed ? (
        <div className="install-app-status">
          <strong>{t("pwa.installed.title")}</strong>
          <p>{t("pwa.installed.description")}</p>
        </div>
      ) : !trustedOrigin ? (
        <div className="install-app-status install-app-warning">
          <strong>{t("pwa.https.title")}</strong>
          <p>{t("pwa.https.description")}</p>
        </div>
      ) : installation.prompt || busy ? (
        <div className="install-app-action">
          <p>{t("pwa.prompt.description")}</p>
          <button
            className="button button-primary"
            type="button"
            disabled={busy || !installation.prompt}
            onClick={() => void install()}
          >
            {t(busy ? "pwa.install.installing" : "pwa.install.action")}
          </button>
        </div>
      ) : isAppleMobile ? (
        <div className="install-app-status">
          <strong>{t("pwa.ios.title")}</strong>
          <p>{t("pwa.ios.description")}</p>
        </div>
      ) : (
        <div className="install-app-status">
          <strong>{t("pwa.browser.title")}</strong>
          <p>{t("pwa.browser.description")}</p>
        </div>
      )}

      {result !== "idle" && !installation.installed && (
        <p className={result === "failed" ? "install-app-result error-text" : "install-app-result"} role="status">
          {t(`pwa.install.${result}`)}
        </p>
      )}
      {localDevelopmentOrigin && <p className="install-app-note">{t("pwa.localhost")}</p>}
      <div className="install-app-disclosure">
        <p>{t("pwa.network")}</p>
        <p>{t("pwa.limitations")}</p>
      </div>
    </section>
  );
}
