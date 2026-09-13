import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { createPortal } from "react-dom";
import { api, ApiError } from "./api";
import { useI18n } from "./i18n";
import type { FilesystemEntryKind, FilesystemPage } from "./types";
import "./ServerPathPicker.css";

interface ServerPathPickerProps {
  kind: Extract<FilesystemEntryKind, "directory" | "file">;
  label: string;
  value: string;
  onChoose: (path: string) => void;
  onClose: () => void;
}

interface FailedNavigation {
  path: string;
  offset: number;
  preserveSelection: boolean;
}

function parentServerPath(path: string): string {
  const slash = Math.max(path.lastIndexOf("/"), path.lastIndexOf("\\"));
  if (slash < 0) return "";
  if (slash === 0) return path.slice(0, 1);
  if (slash === 2 && /^[A-Za-z]:[\\/]$/.test(path.slice(0, 3))) return path.slice(0, 3);
  return path.slice(0, slash);
}

const folderIcon = <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 6.5h6l2 2h10v9.5H3z" /></svg>;
const fileIcon = <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 3h8l4 4v14H6zM14 3v5h5" /></svg>;

export default function ServerPathPicker({ kind, label, value, onChoose, onClose }: ServerPathPickerProps) {
  const { locale, t } = useI18n();
  const initialDirectoryRef = useRef(kind === "file" && value ? parentServerPath(value) : value);
  const [pathInput, setPathInput] = useState(initialDirectoryRef.current);
  const [page, setPage] = useState<FilesystemPage | null>(null);
  const [selectedPath, setSelectedPath] = useState(kind === "file" ? value : "");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [failedNavigation, setFailedNavigation] = useState<FailedNavigation | null>(null);
  const dialogRef = useRef<HTMLElement | null>(null);
  const closeRef = useRef<HTMLButtonElement | null>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);
  const controllerRef = useRef<AbortController | null>(null);
  const requestIDRef = useRef(0);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  const loadDirectory = useCallback(async (path: string, offset = 0, preserveSelection = false) => {
    controllerRef.current?.abort();
    const controller = new AbortController();
    controllerRef.current = controller;
    const requestID = ++requestIDRef.current;
    setLoading(true);
    setError("");
    setFailedNavigation(null);
    if (offset === 0) {
      setPage(null);
      if (kind === "file" && !preserveSelection) setSelectedPath("");
    }

    const query = new URLSearchParams({ path, kind, offset: String(offset) });
    try {
      const next = await api<FilesystemPage>(`/filesystem?${query.toString()}`, { signal: controller.signal });
      if (requestIDRef.current !== requestID) return;
      setPathInput(next.path);
      setPage((current) => {
        if (offset === 0 || !current || current.path !== next.path) return next;
        return { ...next, entries: [...current.entries, ...next.entries] };
      });
    } catch (caught: unknown) {
      if (requestIDRef.current !== requestID || (caught instanceof DOMException && caught.name === "AbortError")) return;
      setError(caught instanceof ApiError ? caught.message : t("settings.pathPicker.requestFailed"));
      setFailedNavigation({ path, offset, preserveSelection });
    } finally {
      if (requestIDRef.current === requestID) setLoading(false);
    }
  }, [kind, t]);

  useEffect(() => {
    void loadDirectory(initialDirectoryRef.current, 0, true);
    return () => {
      requestIDRef.current += 1;
      controllerRef.current?.abort();
    };
  }, [loadDirectory]);

  useEffect(() => {
    previousFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    if (!document.querySelector('[role="alertdialog"][aria-modal="true"]')) closeRef.current?.focus();

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
      if (!document.querySelector('[role="alertdialog"][aria-modal="true"]') && previousFocus?.isConnected) previousFocus.focus();
    };
  }, []);

  const pageEntries = page?.entries;
  const entries = useMemo(() => {
    const collator = new Intl.Collator(locale, { numeric: true, sensitivity: "base" });
    return [...(pageEntries ?? [])].sort((left, right) => {
      if (left.kind !== right.kind) return left.kind === "directory" ? -1 : 1;
      return collator.compare(left.name, right.name);
    });
  }, [locale, pageEntries]);

  function navigate(event: FormEvent) {
    event.preventDefault();
    event.stopPropagation();
    void loadDirectory(pathInput);
  }

  const chosenPath = kind === "directory" ? page?.path ?? "" : selectedPath;
  const canChoose = Boolean(page && chosenPath);
  const nextOffset = page?.next_offset ?? null;

  return createPortal(
    <div className="server-path-backdrop" role="presentation" onMouseDown={(event) => {
      if (event.currentTarget === event.target) onClose();
    }}>
      <section
        ref={dialogRef}
        className="server-path-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="server-path-title"
        aria-describedby="server-path-description"
        tabIndex={-1}
      >
        <header className="server-path-heading">
          <div>
            <p className="server-path-eyebrow">{t(kind === "directory" ? "settings.pathPicker.directoryEyebrow" : "settings.pathPicker.fileEyebrow")}</p>
            <h2 id="server-path-title">{label}</h2>
          </div>
          <button ref={closeRef} className="server-path-close" type="button" aria-label={t("settings.pathPicker.close")} title={t("common.close")} onClick={onClose}>
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18" /></svg>
          </button>
        </header>

        <p className="server-path-description" id="server-path-description">{t("settings.pathPicker.description")}</p>

        <form className="server-path-navigation" onSubmit={navigate}>
          <label className="field-label" htmlFor="server-path-input">{t("settings.pathPicker.path")}</label>
          <div>
            <input
              className="input"
              id="server-path-input"
              value={pathInput}
              autoComplete="off"
              spellCheck={false}
              dir="auto"
              onChange={(event) => setPathInput(event.target.value)}
            />
            <button className="button button-primary" type="submit" disabled={loading}>{t("settings.pathPicker.go")}</button>
          </div>
        </form>

        <nav className="server-path-toolbar" aria-label={t("settings.pathPicker.navigation")}>
          <button className="button button-ghost" type="button" disabled={loading || page?.path === ""} onClick={() => void loadDirectory("")}>{t("settings.pathPicker.roots")}</button>
          <button className="button button-ghost" type="button" disabled={loading || !page?.parent} onClick={() => { if (page?.parent) void loadDirectory(page.parent); }}>{t("settings.pathPicker.parent")}</button>
        </nav>

        <div className="server-path-selection" aria-live="polite">
          <span>{t(kind === "directory" ? "settings.pathPicker.currentDirectory" : "settings.pathPicker.selectedFile")}</span>
          <code dir="auto">{chosenPath || t("settings.pathPicker.noneSelected")}</code>
        </div>

        <div className="server-path-browser" aria-busy={loading}>
          {loading && !page && <div className="server-path-status" role="status">{t("settings.pathPicker.loading")}</div>}
          {error && <div className="server-path-error" role="alert"><p>{error}</p><button className="button button-ghost" type="button" disabled={loading || !failedNavigation} onClick={() => { if (failedNavigation) void loadDirectory(failedNavigation.path, failedNavigation.offset, failedNavigation.preserveSelection); }}>{t("common.retry")}</button></div>}

          {!loading && !error && page?.path === "" && (
            page.roots.length ? (
              <div className="server-path-roots" role="group" aria-label={t("settings.pathPicker.availableRoots")}>
                {page.roots.map((root) => (
                  <button className="server-path-entry" type="button" key={root.path} onClick={() => void loadDirectory(root.path)}>
                    <span className="server-path-entry-icon">{folderIcon}</span>
                    <span><strong>{root.name}</strong><code dir="auto">{root.path}</code></span>
                  </button>
                ))}
              </div>
            ) : <p className="server-path-empty">{t("settings.pathPicker.noRoots")}</p>
          )}

          {page?.path !== "" && entries.length > 0 && (
            <div className="server-path-entries" role="group" aria-label={t("settings.pathPicker.entries")}>
              {entries.map((entry) => entry.kind === "directory" ? (
                <button className="server-path-entry" type="button" key={`directory:${entry.path}`} aria-label={t("settings.pathPicker.openDirectory", { name: entry.name })} onClick={() => void loadDirectory(entry.path)}>
                  <span className="server-path-entry-icon">{folderIcon}</span>
                  <span><strong>{entry.name}</strong><code dir="auto">{entry.path}</code></span>
                  <span className="server-path-entry-action" aria-hidden="true">›</span>
                </button>
              ) : (
                <button className={`server-path-entry${selectedPath === entry.path ? " is-selected" : ""}`} type="button" key={`file:${entry.path}`} aria-pressed={selectedPath === entry.path} aria-label={t("settings.pathPicker.selectFile", { name: entry.name })} onClick={() => setSelectedPath(entry.path)}>
                  <span className="server-path-entry-icon">{fileIcon}</span>
                  <span><strong>{entry.name}</strong><code dir="auto">{entry.path}</code></span>
                </button>
              ))}
            </div>
          )}

          {!loading && !error && page?.path !== "" && entries.length === 0 && <p className="server-path-empty">{t("settings.pathPicker.empty")}</p>}
          {!error && nextOffset !== null && page && (
            <button className="button button-ghost server-path-more" type="button" disabled={loading} onClick={() => void loadDirectory(page.path, nextOffset, true)}>
              {t(loading ? "settings.pathPicker.loadingMore" : "settings.pathPicker.loadMore")}
            </button>
          )}
        </div>

        <footer className="server-path-actions">
          <button className="button button-ghost" type="button" onClick={onClose}>{t("common.cancel")}</button>
          <button className="button button-primary" type="button" disabled={!canChoose || loading} onClick={() => { if (chosenPath) onChoose(chosenPath); }}>{t("settings.pathPicker.choose")}</button>
        </footer>
      </section>
    </div>,
    document.body,
  );
}
