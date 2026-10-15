import { useEffect, useRef } from "react";
import { createPortal } from "react-dom";
import { useI18n } from "./i18n";
import type { ServerSignalPath } from "./signalPath";
import "./TrackInfoDialog.css";

type Props = {
  path: ServerSignalPath;
  format: string;
  device: string;
  onClose: () => void;
};

// Read-only explanation of the server-delivery badge. It deliberately offers no playback
// or output actions: opening it must never change what is playing.
export default function SignalPathDialog({ path, format, device, onClose }: Props) {
  const { locale, t } = useI18n();
  const dialogRef = useRef<HTMLElement | null>(null);
  const closeRef = useRef<HTMLButtonElement | null>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  useEffect(() => {
    previousFocusRef.current = document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null;
    if (!document.querySelector('[role="alertdialog"][aria-modal="true"]')) {
      closeRef.current?.focus();
    }

    function handleKeyDown(event: KeyboardEvent) {
      if (document.querySelector('[role="alertdialog"][aria-modal="true"]')) return;
      if (event.key === "Escape") {
        event.preventDefault();
        onCloseRef.current();
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
      if (document.querySelector('[role="alertdialog"][aria-modal="true"]')) return;
      if (previousFocus?.isConnected) previousFocus.focus();
    };
  }, []);

  const unavailable = t("info.unavailable");
  const details: Array<[string, string]> = [
    [t("info.field.format"), format.trim() || unavailable],
    [t("info.field.sampleRate"), t("player.signalPath.server.rate", { rate: path.sampleRate.toLocaleString(locale) })],
    [t("player.signalPath.server.validBits"), t("info.bitDepth", { count: path.validBits.toLocaleString(locale) })],
    ...(path.mode === "pcm"
      ? [[t("player.signalPath.server.containerBits"), t("info.bitDepth", { count: path.containerBits.toLocaleString(locale) })] as [string, string]]
      : []),
    [t("info.field.channels"), t(path.channels === 1 ? "info.channelCountOne" : "info.channelCount", { count: path.channels.toLocaleString(locale) })],
    [t("player.signalPath.server.delivery"), t(path.mode === "original" ? "player.signalPath.server.delivery.original" : "player.signalPath.server.delivery.pcm")],
  ];

  return createPortal(
    <div
      className="track-info-backdrop"
      role="presentation"
      onMouseDown={(event) => {
        if (event.currentTarget === event.target) onClose();
      }}
    >
      <section
        ref={dialogRef}
        className="track-info-dialog signal-path-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="signal-path-title"
        aria-describedby="signal-path-introduction"
        tabIndex={-1}
        // The portal still bubbles through the player bar in React; marking Escape as handled
        // keeps the phone player from collapsing underneath the dialog.
        onKeyDown={(event) => {
          if (event.key === "Escape") event.preventDefault();
        }}
      >
        <header className="track-info-heading">
          <div>
            <p className="track-info-eyebrow">{t("player.signalPath.server.eyebrow")}</p>
            <h2 id="signal-path-title">{t("player.signalPath.server.label")}</h2>
          </div>
          <button
            ref={closeRef}
            className="track-info-close"
            type="button"
            aria-label={t("player.signalPath.server.close")}
            title={t("common.close")}
            onClick={onClose}
          >
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18" /></svg>
          </button>
        </header>

        <div className="track-info-content">
          <p id="signal-path-introduction">{t("player.signalPath.server.introduction", { device })}</p>

          <section className="track-info-section" aria-labelledby="signal-path-format-heading">
            <h3 id="signal-path-format-heading">{t("player.signalPath.server.formatHeading")}</h3>
            <dl className="track-info-grid">
              {details.map(([label, value]) => (
                <div className="track-info-detail" key={label}>
                  <dt>{label}</dt>
                  <dd>{value}</dd>
                </div>
              ))}
            </dl>
          </section>

          <section className="track-info-section" aria-labelledby="signal-path-verified-heading">
            <h3 id="signal-path-verified-heading">{t("player.signalPath.server.verifiedHeading")}</h3>
            <p>
              {path.mode === "original"
                ? t("player.signalPath.server.verified.original")
                : t("player.signalPath.server.verified.pcm", {
                    valid: path.validBits.toLocaleString(locale),
                    container: path.containerBits.toLocaleString(locale),
                  })}
            </p>
          </section>

          <section className="track-info-section signal-path-unverified" aria-labelledby="signal-path-unverified-heading">
            <h3 id="signal-path-unverified-heading">{t("player.signalPath.server.unverifiedHeading")}</h3>
            <p>{t("player.signalPath.server.unverified")}</p>
          </section>
        </div>
      </section>
    </div>,
    document.body,
  );
}
