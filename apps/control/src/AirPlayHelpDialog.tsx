import { useEffect, useRef } from "react";
import { createPortal } from "react-dom";
import { useI18n } from "./i18n";
import "./AirPlayHelpDialog.css";

interface AirPlayHelpDialogProps {
  open: boolean;
  onClose: () => void;
}

const pyatvURL = "https://github.com/postlund/pyatv/tree/v0.18.0";
const helperSourceURL = "https://github.com/furyheimdall/jastreamer/blob/main/apps/server/internal/airplay/helper.py";
const requirementsSourceURL = "https://github.com/furyheimdall/jastreamer/blob/main/packaging/server/requirements-airplay.txt";

export default function AirPlayHelpDialog({ open, onClose }: AirPlayHelpDialogProps) {
  const { t } = useI18n();
  const dialogRef = useRef<HTMLElement | null>(null);
  const closeRef = useRef<HTMLButtonElement | null>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  useEffect(() => {
    if (!open) return;
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
  }, [open]);

  if (!open) return null;

  return createPortal(
    <div
      className="airplay-help-backdrop"
      role="presentation"
      onMouseDown={(event) => {
        if (event.currentTarget === event.target) onClose();
      }}
    >
      <section
        ref={dialogRef}
        className="airplay-help-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="airplay-help-title"
        aria-describedby="airplay-help-introduction"
        tabIndex={-1}
      >
        <header className="airplay-help-heading">
          <div>
            <p className="airplay-help-eyebrow">{t("settings.airplay.title")}</p>
            <h2 id="airplay-help-title">{t("settings.airplay.dialog.title")}</h2>
          </div>
          <button
            ref={closeRef}
            className="airplay-help-close"
            type="button"
            aria-label={t("settings.airplay.dialog.close")}
            title={t("common.close")}
            onClick={onClose}
          >
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18" /></svg>
          </button>
        </header>

        <div className="airplay-help-content">
          <p id="airplay-help-introduction">{t("settings.airplay.dialog.introduction")}</p>

          <section aria-labelledby="airplay-help-packaged-heading">
            <h3 id="airplay-help-packaged-heading">{t("settings.airplay.dialog.packagedTitle")}</h3>
            <p>{t("settings.airplay.dialog.packagedBody")}</p>
            <code>/usr/local/bin/jastreamer-airplay</code>
          </section>

          <section className="airplay-help-warning" aria-labelledby="airplay-help-windows-heading">
            <h3 id="airplay-help-windows-heading">{t("settings.airplay.dialog.windowsTitle")}</h3>
            <p>{t("settings.airplay.dialog.windowsBody")}</p>
          </section>

          <section aria-labelledby="airplay-help-separate-heading">
            <h3 id="airplay-help-separate-heading">{t("settings.airplay.dialog.separateTitle")}</h3>
            <p>{t("settings.airplay.dialog.separateBody")}</p>
          </section>

          <section aria-labelledby="airplay-help-links-heading">
            <h3 id="airplay-help-links-heading">{t("settings.airplay.dialog.linksTitle")}</h3>
            <p>{t("settings.airplay.dialog.linksHelp")}</p>
            <ul className="airplay-help-links">
              <li>
                <a href={helperSourceURL} target="_blank" rel="noopener noreferrer">
                  <span>{t("settings.airplay.dialog.helperSourceLink")}</span>
                  <span className="airplay-help-url">{helperSourceURL}</span>
                </a>
              </li>
              <li>
                <a href={requirementsSourceURL} target="_blank" rel="noopener noreferrer">
                  <span>{t("settings.airplay.dialog.requirementsLink")}</span>
                  <span className="airplay-help-url">{requirementsSourceURL}</span>
                </a>
              </li>
              <li>
                <a href={pyatvURL} target="_blank" rel="noopener noreferrer">
                  <span>{t("settings.airplay.dialog.pyatvLink")}</span>
                  <span className="airplay-help-url">{pyatvURL}</span>
                </a>
              </li>
            </ul>
          </section>
        </div>
      </section>
    </div>,
    document.body,
  );
}
