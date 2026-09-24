import { useCallback, useEffect, useMemo, useState } from "react";
import { api, apiBlob, ApiError } from "./api";
import { embeddedClient } from "./device";
import { useI18n, type MessageKey } from "./i18n";
import type { HistoryEvent, HistoryKind, HistoryPage, HistoryRenderer, VerificationState, VerificationStatus as VerificationStatusDocument } from "./types";
import "./diagnostics.css";

function isAbort(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

function requestMessage(error: unknown, fallback: string): string {
  return error instanceof ApiError ? error.message : fallback;
}

const verificationStateKeys: Record<VerificationState, MessageKey> = {
  idle: "settings.verification.state.idle",
  queued: "settings.verification.state.queued",
  running: "settings.verification.state.running",
  paused: "settings.verification.state.paused",
  complete: "settings.verification.state.complete",
  unavailable: "settings.verification.state.unavailable",
};

export function VerificationStatus({ revision }: { revision: number }) {
  const { locale, t } = useI18n();
  const [status, setStatus] = useState<VerificationStatusDocument | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [refresh, setRefresh] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    api<VerificationStatusDocument>("/library/verification", { signal: controller.signal })
      .then((next) => {
        if (controller.signal.aborted) return;
        setStatus(next);
        setError("");
      })
      .catch((caught: unknown) => {
        if (!controller.signal.aborted && !isAbort(caught)) setError(requestMessage(caught, t("settings.verification.loadFailed")));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [refresh, revision, t]);

  const formatter = useMemo(() => new Intl.NumberFormat(locale), [locale]);
  const completed = status ? status.verified + status.failed + status.unverified : 0;
  const progressTotal = Math.max(status?.total ?? 0, 1);

  return (
    <section className="verification-panel" aria-labelledby="verification-heading" aria-busy={loading}>
      <div className="diagnostics-heading-row">
        <div>
          <h3 id="verification-heading">{t("settings.verification.title")}</h3>
          <p>{t("settings.verification.description")}</p>
        </div>
        <button className="button button-ghost diagnostics-refresh" type="button" disabled={loading} onClick={() => setRefresh((value) => value + 1)}>
          {t("common.refresh")}
        </button>
      </div>
      <p className="verification-distinction">{t("settings.verification.indexingDistinct")}</p>
      {loading && !status && <p className="diagnostics-status" role="status">{t("settings.verification.loading")}</p>}
      {error && (
        <div className="inline-error diagnostics-inline-error" role="alert">
          <span>{error}</span>
          <button className="button button-ghost" type="button" onClick={() => setRefresh((value) => value + 1)}>{t("common.retry")}</button>
        </div>
      )}
      {status && (
        <div className={`verification-body verification-state-${status.state}`}>
          <div className="verification-state-row" role="status" aria-live="polite">
            <strong>{t(verificationStateKeys[status.state])}</strong>
            {status.engine && <span>{t("settings.verification.engine", { engine: status.engine })}</span>}
          </div>
          {status.state === "paused" && status.reason === "playback" && <p className="verification-callout">{t("settings.verification.playbackPaused")}</p>}
          {status.state === "paused" && status.reason === "scan" && <p className="verification-callout">{t("settings.verification.scanPaused")}</p>}
          {status.reason && status.state !== "paused" && <p className="diagnostics-secondary">{t("settings.verification.reason", { reason: status.reason })}</p>}
          {status.error && <p className="error-text diagnostics-message" role="alert">{status.error}</p>}
          <div className="verification-progress-copy">
            <span>{t("settings.verification.progress", {
              completed: formatter.format(completed),
              total: formatter.format(status.total),
            })}</span>
            <span>{t("settings.verification.counts", {
              verified: formatter.format(status.verified),
              failed: formatter.format(status.failed),
              unverified: formatter.format(status.unverified),
              pending: formatter.format(status.pending),
            })}</span>
          </div>
          <progress
            className="verification-progress"
            max={progressTotal}
            value={Math.min(completed, progressTotal)}
            aria-label={t("settings.verification.progress", { completed: formatter.format(completed), total: formatter.format(status.total) })}
          />
          {(status.current_track_title || status.current_track_id) && (
            <p className="verification-current">{t("settings.verification.current", { title: status.current_track_title || status.current_track_id })}</p>
          )}
        </div>
      )}
    </section>
  );
}

const historyPageSize = 25;


function formatPosition(milliseconds: number): string {
  const bounded = Math.max(0, milliseconds);
  const totalSeconds = Math.floor(bounded / 1000);
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  const fraction = Math.floor(bounded % 1000);
  return `${minutes}:${String(seconds).padStart(2, "0")}.${String(fraction).padStart(3, "0")}`;
}

function hasDetails(details: unknown): boolean {
  if (details === null || details === undefined || details === "") return false;
  if (Array.isArray(details)) return details.length > 0;
  if (typeof details === "object") return Object.keys(details).length > 0;
  return true;
}

function formatDetails(details: unknown): string {
  if (typeof details === "string") return details;
  const formatted = JSON.stringify(details, null, 2);
  return formatted === undefined ? String(details) : formatted;
}

interface HistoryEntryProps {
  event: HistoryEvent;
  renderers: HistoryRenderer[];
  formatTime: (value: string) => string;
}

function HistoryEntry({ event, renderers, formatTime }: HistoryEntryProps) {
  const { t } = useI18n();
  const knownRenderer = renderers.find((renderer) => renderer.id === event.renderer_id);
  const name = event.renderer_name || knownRenderer?.name || "";
  const protocol = event.protocol || knownRenderer?.protocol || "";
  const integrity = event.kind === "integrity";
  const path = event.root_name && event.relative_path
    ? `${event.root_name} / ${event.relative_path}`
    : event.relative_path || event.root_name || t("settings.history.pathUnavailable");
  const title = integrity
    ? event.track_title || event.track_id || t("settings.history.unknownTrack")
    : name || event.renderer_id || t("settings.history.unknownRenderer");

  return (
    <li className={`history-entry history-entry-${event.kind}`}>
      <div className="history-entry-heading">
        <div className="history-entry-title">
          <span className="history-kind">{t(integrity ? "settings.history.kind.integrity" : "settings.history.kind.renderer")}</span>
          <strong>{title}</strong>
          {!integrity && event.renderer_id && name && <span className="history-renderer-id">{t("settings.history.rendererID", { id: event.renderer_id })}</span>}
          {integrity && <span className="history-path">{path}</span>}
        </div>
        <div className="history-entry-state">
          <span className={`history-outcome history-outcome-${event.outcome}`}>{t(`settings.history.outcome.${event.outcome}`)}</span>
          <time dateTime={event.received_at}>{formatTime(event.received_at)}</time>
        </div>
      </div>
      {event.message && <p className="history-message">{event.message}</p>}
      <dl className="history-facts">
        {protocol && <div><dt>{t("settings.history.protocol")}</dt><dd>{protocol}</dd></div>}
        {event.stage && <div><dt>{t("settings.history.stage")}</dt><dd><code>{event.stage}</code></dd></div>}
        {event.code && <div><dt>{t("settings.history.code")}</dt><dd><code>{event.code}</code></dd></div>}
        {!integrity && typeof event.position_ms === "number" && <div><dt>{t("settings.history.position")}</dt><dd>{formatPosition(event.position_ms)}</dd></div>}
      </dl>
      {hasDetails(event.details) && (
        <details className="history-details">
          <summary>{t("settings.history.details")}</summary>
          <pre>{formatDetails(event.details)}</pre>
        </details>
      )}
    </li>
  );
}

export function HistoryPanel({ revision }: { revision: number }) {
  const { locale, t } = useI18n();
  const [open, setOpen] = useState(false);
  const [kind, setKind] = useState<"" | HistoryKind>("");
  const [rendererID, setRendererID] = useState("");
  const [offset, setOffset] = useState(0);
  const [page, setPage] = useState<HistoryPage | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [refresh, setRefresh] = useState(0);
  const [exporting, setExporting] = useState(false);
  const [exportError, setExportError] = useState("");

  const load = useCallback((signal: AbortSignal) => {
    const params = new URLSearchParams({ offset: String(offset), limit: String(historyPageSize) });
    if (kind) params.set("kind", kind);
    if (rendererID) params.set("renderer_id", rendererID);
    setLoading(true);
    return api<HistoryPage>(`/history?${params}`, { signal })
      .then((next) => {
        if (signal.aborted) return;
        setPage({ ...next, items: next.items ?? [], renderers: next.renderers ?? [] });
        setError("");
      })
      .catch((caught: unknown) => {
        if (!signal.aborted && !isAbort(caught)) setError(requestMessage(caught, t("settings.history.loadFailed")));
      })
      .finally(() => {
        if (!signal.aborted) setLoading(false);
      });
  }, [kind, offset, rendererID, t]);

  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    void load(controller.signal);
    return () => controller.abort();
  }, [load, open, refresh, revision]);

  const dateFormatter = useMemo(() => new Intl.DateTimeFormat(locale, {
    dateStyle: "medium",
    timeStyle: "medium",
  }), [locale]);
  const numberFormatter = useMemo(() => new Intl.NumberFormat(locale), [locale]);
  const formatTime = useCallback((value: string) => {
    const parsed = new Date(value);
    return Number.isNaN(parsed.getTime()) ? value : dateFormatter.format(parsed);
  }, [dateFormatter]);

  const exportHistory = async () => {
    const params = new URLSearchParams();
    if (kind) params.set("kind", kind);
    if (rendererID) params.set("renderer_id", rendererID);
    setExporting(true);
    setExportError("");
    try {
      const serialized = params.toString();
      const blob = await apiBlob(`/history/export${serialized ? `?${serialized}` : ""}`);
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      try {
        link.href = url;
        link.download = "jastreamer-diagnostic-history.csv";
        link.style.display = "none";
        document.body.append(link);
        link.click();
      } finally {
        link.remove();
        window.setTimeout(() => URL.revokeObjectURL(url), 1_000);
      }
    } catch (caught) {
      setExportError(requestMessage(caught, t("settings.history.exportFailed")));
    } finally {
      setExporting(false);
    }
  };


  const pageOffset = page?.offset ?? offset;
  const total = page?.total ?? 0;
  const start = total === 0 ? 0 : pageOffset + 1;
  const end = Math.min(pageOffset + (page?.items.length ?? 0), total);

  return (
    <details className="settings-card history-panel" onToggle={(event) => setOpen(event.currentTarget.open)}>
      <summary>
        <span>
          <strong>{t("settings.history.title")}</strong>
          <small>{t("settings.history.summary")}</small>
        </span>
      </summary>
      <div className="history-panel-body">
        <p className="muted">{t("settings.history.description")}</p>
        <div className="history-toolbar">
          <label>
            <span className="field-label">{t("settings.history.typeFilter")}</span>
            <select
              className="input"
              value={kind}
              onChange={(event) => {
                const next = event.target.value as "" | HistoryKind;
                setKind(next);
                if (next === "integrity") setRendererID("");
                setOffset(0);
                setPage(null);
              }}
            >
              <option value="">{t("settings.history.type.all")}</option>
              <option value="renderer">{t("settings.history.type.renderer")}</option>
              <option value="integrity">{t("settings.history.type.integrity")}</option>
            </select>
          </label>
          <label>
            <span className="field-label">{t("settings.history.rendererFilter")}</span>
            <select
              className="input"
              value={rendererID}
              disabled={kind === "integrity"}
              onChange={(event) => {
                setRendererID(event.target.value);
                setOffset(0);
                setPage(null);
              }}
            >
              <option value="">{t("settings.history.renderer.all")}</option>
              {(page?.renderers ?? []).map((renderer) => (
                <option value={renderer.id} key={renderer.id}>{renderer.name ? `${renderer.name} — ${renderer.id}` : renderer.id}</option>
              ))}
            </select>
          </label>
          <button
            className="button button-ghost diagnostics-refresh"
            type="button"
            disabled={loading}
            aria-label={t("settings.history.refresh")}
            onClick={() => setRefresh((value) => value + 1)}
          >
            {t("common.refresh")}
          </button>
          {embeddedClient ? (
            <p className="history-export-guidance">{t("settings.history.exportBrowserOnly")}</p>
          ) : (
            <button
              className="button button-ghost history-export"
              type="button"
              disabled={exporting}
              aria-describedby="history-export-scope"
              onClick={() => void exportHistory()}
            >
              {t(exporting ? "settings.history.exporting" : "settings.history.export")}
            </button>
          )}
        </div>
        <p className="history-export-scope" id="history-export-scope">{t("settings.history.exportScope")}</p>
        {exporting && <p className="diagnostics-loading-inline" role="status">{t("settings.history.exporting")}</p>}
        {exportError && <div className="inline-error diagnostics-inline-error" role="alert"><span>{exportError}</span></div>}
        {error && (
          <div className="inline-error diagnostics-inline-error" role="alert">
            <span>{error}</span>
            <button className="button button-ghost" type="button" onClick={() => setRefresh((value) => value + 1)}>{t("common.retry")}</button>
          </div>
        )}
        {loading && <p className={page ? "diagnostics-loading-inline" : "diagnostics-status"} role="status">{t("settings.history.loading")}</p>}
        {page && (
          <div aria-busy={loading}>
            {page.items.length === 0 ? (
              <p className="history-empty">{t("settings.history.empty")}</p>
            ) : (
              <ol className="history-list" start={pageOffset + 1}>
                {page.items.map((event) => <HistoryEntry event={event} renderers={page.renderers} formatTime={formatTime} key={event.id} />)}
              </ol>
            )}
            <div className="history-pagination">
              <span>{t("settings.history.page", {
                start: numberFormatter.format(start),
                end: numberFormatter.format(end),
                total: numberFormatter.format(total),
              })}</span>
              <div>
                <button className="button button-ghost" type="button" disabled={loading || pageOffset === 0} onClick={() => setOffset(Math.max(0, pageOffset - historyPageSize))}>{t("common.previous")}</button>
                <button className="button button-ghost" type="button" disabled={loading || pageOffset + page.items.length >= total} onClick={() => setOffset(pageOffset + historyPageSize)}>{t("common.next")}</button>
              </div>
            </div>
            <p className="history-retention">{t("settings.history.retention", { limit: numberFormatter.format(page.retention_limit) })}</p>
          </div>
        )}
      </div>
    </details>
  );
}
