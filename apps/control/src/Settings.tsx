import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { api, ApiError } from "./api";
import { useI18n, type Language, type MessageKey } from "./i18n";
import type { ConfigDocument, ConfigRoot, ScanJob, ServerConfig } from "./types";

interface SettingsProps {
  configRevision: number;
  libraryRevision: number;
  onNotice: (message: string, error?: boolean) => void;
  onSignedOut: () => void;
}

function requestMessage(error: unknown, t: (key: MessageKey) => string): string {
  return error instanceof ApiError ? error.message : t("settings.requestFailed");
}

function listText(items: string[]): string {
  return items.join("\n");
}

function parseList(value: string): string[] {
  return value
    .split(/[\n,]/)
    .map((item) => item.trim())
    .filter(Boolean);
}

function newRootID(): string {
  const random = new Uint8Array(16);
  crypto.getRandomValues(random);
  return `root-${Array.from(random, (value) => value.toString(16).padStart(2, "0")).join("")}`;
}


export default function Settings({ configRevision, libraryRevision, onNotice, onSignedOut }: SettingsProps) {
  const { language, locale, t, setLanguage } = useI18n();
  const [document, setDocument] = useState<ConfigDocument | null>(null);
  const [draft, setDraft] = useState<ServerConfig | null>(null);
  const [scans, setScans] = useState<ScanJob[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [scanBusy, setScanBusy] = useState("");
  const [error, setError] = useState("");
  const [remoteConfigPending, setRemoteConfigPending] = useState(false);
  const [restartRequired, setRestartRequired] = useState(false);
  const [interfacesText, setInterfacesText] = useState("");
  const [cidrsText, setCidrsText] = useState("");
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [passwordBusy, setPasswordBusy] = useState(false);
  const [languagePersistFailed, setLanguagePersistFailed] = useState(false);
  const configDirtyRef = useRef(false);
  configDirtyRef.current = Boolean(
    document
    && draft
    && (
      JSON.stringify(draft) !== JSON.stringify(document.config)
      || interfacesText !== listText(document.config.network.interfaces)
      || cidrsText !== listText(document.config.network.allowed_cidrs)
    ),
  );

  const loadConfig = useCallback(async () => {
    try {
      const next = await api<ConfigDocument>("/config");
      setDocument(next);
      setDraft(structuredClone(next.config));
      setInterfacesText(listText(next.config.network.interfaces));
      setCidrsText(listText(next.config.network.allowed_cidrs));
      setError("");
      setRemoteConfigPending(false);
    } catch (requestError) {
      setError(requestMessage(requestError, t));
    } finally {
      setLoading(false);
    }
  }, [t]);

  const loadScans = useCallback(async () => {
    try {
      const result = await api<{ items: ScanJob[] }>("/library/scans");
      setScans(result.items ?? []);
    } catch (requestError) {
      setError(requestMessage(requestError, t));
    }
  }, [t]);

  useEffect(() => {
    if (configDirtyRef.current) {
      setRemoteConfigPending(true);
      return;
    }
    void loadConfig();
  }, [configRevision, loadConfig]);

  useEffect(() => {
    void loadScans();
  }, [libraryRevision, loadScans]);

  useEffect(() => {
    if (!scans.some((scan) => scan.status === "queued" || scan.status === "running")) return;
    const timer = window.setInterval(() => void loadScans(), 2500);
    return () => window.clearInterval(timer);
  }, [loadScans, scans]);

  function updateDraft(update: (config: ServerConfig) => ServerConfig) {
    setDraft((current) => (current ? update(current) : current));
  }

  async function saveConfig(event: FormEvent) {
    event.preventDefault();
    if (!document || !draft) return;
    setSaving(true);
    try {
      const next = await api<ConfigDocument>("/config", {
        method: "PUT",
        body: JSON.stringify({ config: draft, revision: document.revision }),
      });
      setDocument(next);
      setDraft(structuredClone(next.config));
      setInterfacesText(listText(next.config.network.interfaces));
      setCidrsText(listText(next.config.network.allowed_cidrs));
      setRestartRequired(Boolean(next.restart_required));
      setError("");
      setRemoteConfigPending(false);
      onNotice(t(next.restart_required ? "settings.savedRestart" : "settings.saved"));
    } catch (requestError) {
      const conflict = requestError instanceof ApiError && requestError.status === 409;
      const message = conflict
        ? t("settings.conflict")
        : requestMessage(requestError, t);
      setError(message);
      onNotice(message, true);
      if (conflict) await loadConfig();
    } finally {
      setSaving(false);
    }
  }

  function addRoot() {
    const root: ConfigRoot = {
      id: newRootID(),
      name: "",
      path: "",
    };
    updateDraft((config) => ({
      ...config,
      library_roots: [...config.library_roots, root],
    }));
  }

  function changeRoot(index: number, field: "name" | "path", value: string) {
    updateDraft((config) => ({
      ...config,
      library_roots: config.library_roots.map((root, rootIndex) =>
        rootIndex === index ? { ...root, [field]: value } : root,
      ),
    }));
  }

  function removeRoot(index: number) {
    updateDraft((config) => ({
      ...config,
      library_roots: config.library_roots.filter((_, rootIndex) => rootIndex !== index),
    }));
  }

  async function startScan() {
    setScanBusy("new");
    try {
      const job = await api<ScanJob>("/library/scans", {
        method: "POST",
        body: JSON.stringify({}),
      });
      setScans((current) => [job, ...current.filter((item) => item.id !== job.id)]);
      onNotice(t("settings.scan.started"));
    } catch (requestError) {
      onNotice(requestMessage(requestError, t), true);
    } finally {
      setScanBusy("");
    }
  }

  async function cancelScan(id: string) {
    setScanBusy(id);
    try {
      await api<void>(`/library/scans/${encodeURIComponent(id)}`, { method: "DELETE" });
      await loadScans();
      onNotice(t("settings.scan.cancelRequested"));
    } catch (requestError) {
      onNotice(requestMessage(requestError, t), true);
    } finally {
      setScanBusy("");
    }
  }

  async function changePassword(event: FormEvent) {
    event.preventDefault();
    if (newPassword !== confirmPassword) {
      onNotice(t("settings.account.passwordMismatch"), true);
      return;
    }
    setPasswordBusy(true);
    try {
      await api<void>("/account/password", {
        method: "POST",
        body: JSON.stringify({
          current_password: currentPassword,
          new_password: newPassword,
        }),
      });
      setCurrentPassword("");
      setNewPassword("");
      setConfirmPassword("");
      onSignedOut();
    } catch (requestError) {
      onNotice(requestMessage(requestError, t), true);
    } finally {
      setPasswordBusy(false);
    }
  }

  const settingsHeader = (
    <header className="page-heading">
      <p className="eyebrow">{t("settings.eyebrow")}</p>
      <h1 id="settings-heading">{t("settings.title")}</h1>
      <p className="muted">{t("settings.description")}</p>
    </header>
  );
  const languageSettings = (
    <section className="settings-card" aria-labelledby="language-heading">
      <h2 id="language-heading">{t("settings.language.title")}</h2>
      <p className="muted" id="language-description">{t("settings.language.description")}</p>
      <label className="field-label" htmlFor="language-select">{t("settings.language.label")}</label>
      <select
        className="input"
        id="language-select"
        value={language}
        aria-describedby={languagePersistFailed ? "language-description language-warning" : "language-description"}
        onChange={(event) => {
          const persisted = setLanguage(event.target.value as Language);
          setLanguagePersistFailed(!persisted);
        }}
      >
        <option value="en">{t("settings.language.english")}</option>
        <option value="ko">{t("settings.language.korean")}</option>
      </select>
      {languagePersistFailed && (
        <p className="error-text" id="language-warning" role="status">{t("settings.language.persistWarning")}</p>
      )}
    </section>
  );

  if (loading && !draft) {
    return (
      <section className="content-section settings-page" aria-labelledby="settings-heading">
        {settingsHeader}
        {languageSettings}
        <div className="loading-block" aria-live="polite">{t("settings.loading")}</div>
      </section>
    );
  }

  if (!draft) {
    return (
      <section className="content-section settings-page" aria-labelledby="settings-heading">
        {settingsHeader}
        {languageSettings}
        <div className="inline-error" role="alert">
          <span>{error || t("settings.loadFailed")}</span>
          <button className="button button-ghost" type="button" onClick={() => void loadConfig()}>
            {t("settings.retry")}
          </button>
        </div>
      </section>
    );
  }

  return (
    <section className="content-section settings-page" aria-labelledby="settings-heading">
      {settingsHeader}
      {languageSettings}

      {error && <p className="error-text" role="alert">{error}</p>}
      {remoteConfigPending && (
        <div className="inline-error" role="alert">
          <span>{t("settings.remoteChanged")}</span>
          <button className="button button-ghost" type="button" onClick={() => void loadConfig()}>
            {t("settings.loadLatest")}
          </button>
        </div>
      )}
      {restartRequired && (
        <div className="restart-banner" role="status">
          {t("settings.restartRequired")}
        </div>
      )}

      <form className="settings-form" onSubmit={(event) => void saveConfig(event)}>
        <section className="settings-card">
          <h2>{t("settings.serverName.title")}</h2>
          <label className="field-label" htmlFor="server-name">{t("settings.serverName.label")}</label>
          <input
            className="input"
            id="server-name"
            value={draft.server_name ?? ""}
            maxLength={64}
            placeholder={t("settings.serverName.placeholder")}
            onChange={(event) => updateDraft((config) => ({ ...config, server_name: event.target.value }))}
          />
          <p className="field-help">{t("settings.serverName.help")}</p>
        </section>

        <section className="settings-card">
          <h2>{t("settings.storage.title")}</h2>
          <label className="field-label" htmlFor="data-directory">{t("settings.storage.dataDirectory")}</label>
          <input
            className="input"
            id="data-directory"
            value={draft.data_dir}
            required
            onChange={(event) => updateDraft((config) => ({ ...config, data_dir: event.target.value }))}
          />
          <p className="field-help">{t("settings.storage.help")}</p>
        </section>

        <section className="settings-card protocol-settings">
          <div className="settings-card-heading">
            <div>
              <h2>HTTP</h2>
              <p className="muted">{t("settings.protocol.privateNetwork")}</p>
            </div>
            <label className="switch-label">
              <input
                type="checkbox"
                checked={draft.http.enabled}
                onChange={(event) => updateDraft((config) => ({
                  ...config,
                  http: { ...config.http, enabled: event.target.checked },
                }))}
              />
              {t("settings.protocol.enabled")}
            </label>
          </div>
          <label className="field-label" htmlFor="http-address">{t("settings.protocol.listenAddress")}</label>
          <input
            className="input"
            id="http-address"
            value={draft.http.address}
            disabled={!draft.http.enabled}
            required={draft.http.enabled}
            placeholder=":8080"
            onChange={(event) => updateDraft((config) => ({
              ...config,
              http: { ...config.http, address: event.target.value },
            }))}
          />
        </section>

        <section className="settings-card protocol-settings">
          <div className="settings-card-heading">
            <div>
              <h2>HTTPS</h2>
              <p className="muted">{t("settings.https.description")}</p>
            </div>
            <label className="switch-label">
              <input
                type="checkbox"
                checked={draft.https.enabled}
                onChange={(event) => updateDraft((config) => ({
                  ...config,
                  https: { ...config.https, enabled: event.target.checked },
                }))}
              />
              {t("settings.protocol.enabled")}
            </label>
          </div>
          <div className="field-grid">
            <label>
              <span className="field-label">{t("settings.protocol.listenAddress")}</span>
              <input
                className="input"
                value={draft.https.address}
                disabled={!draft.https.enabled}
                required={draft.https.enabled}
                placeholder=":8443"
                onChange={(event) => updateDraft((config) => ({
                  ...config,
                  https: { ...config.https, address: event.target.value },
                }))}
              />
            </label>
            <label>
              <span className="field-label">{t("settings.https.certificate")}</span>
              <input
                className="input"
                value={draft.https.certificate_file}
                disabled={!draft.https.enabled}
                required={draft.https.enabled}
                placeholder="/etc/jastreamer/server.crt"
                onChange={(event) => updateDraft((config) => ({
                  ...config,
                  https: { ...config.https, certificate_file: event.target.value },
                }))}
              />
            </label>
            <label>
              <span className="field-label">{t("settings.https.privateKey")}</span>
              <input
                className="input"
                value={draft.https.private_key_file}
                disabled={!draft.https.enabled}
                required={draft.https.enabled}
                placeholder="/etc/jastreamer/server.key"
                onChange={(event) => updateDraft((config) => ({
                  ...config,
                  https: { ...config.https, private_key_file: event.target.value },
                }))}
              />
            </label>
          </div>
        </section>

        <section className="settings-card roots-settings">
          <div className="settings-card-heading">
            <div>
              <h2>{t("settings.roots.title")}</h2>
              <p className="muted">{t("settings.roots.description")}</p>
            </div>
            <button className="button button-ghost" type="button" onClick={addRoot}>{t("settings.roots.add")}</button>
          </div>
          {draft.library_roots.length === 0 ? (
            <p className="empty-inline">{t("settings.roots.empty")}</p>
          ) : (
            <div className="root-list">
              {draft.library_roots.map((root, index) => (
                <div className="root-row" key={root.id}>
                  <label>
                    <span className="field-label">{t("settings.roots.name")}</span>
                    <input
                      className="input"
                      value={root.name}
                      required
                      placeholder={t("settings.roots.namePlaceholder")}
                      onChange={(event) => changeRoot(index, "name", event.target.value)}
                    />
                  </label>
                  <label>
                    <span className="field-label">{t("settings.roots.path")}</span>
                    <input
                      className="input"
                      value={root.path}
                      required
                      placeholder="/music"
                      onChange={(event) => changeRoot(index, "path", event.target.value)}
                    />
                  </label>
                  <button
                    className="button button-ghost danger-button"
                    type="button"
                    aria-label={t("settings.roots.removeLabel", { name: root.name || t("settings.roots.fallbackName") })}
                    onClick={() => removeRoot(index)}
                  >
                    {t("settings.roots.remove")}
                  </button>
                </div>
              ))}
            </div>
          )}
        </section>

        <section className="settings-card">
          <h2>{t("settings.network.title")}</h2>
          <div className="field-grid">
            <label>
              <span className="field-label">{t("settings.network.interfaces")}</span>
              <textarea
                className="input"
                rows={3}
                value={interfacesText}
                placeholder={t("settings.network.interfacesPlaceholder")}
                onChange={(event) => {
                  setInterfacesText(event.target.value);
                  updateDraft((config) => ({
                    ...config,
                    network: { ...config.network, interfaces: parseList(event.target.value) },
                  }));
                }}
              />
            </label>
            <label>
              <span className="field-label">{t("settings.network.cidrs")}</span>
              <textarea
                className="input"
                rows={3}
                value={cidrsText}
                placeholder={t("settings.network.cidrsPlaceholder")}
                onChange={(event) => {
                  setCidrsText(event.target.value);
                  updateDraft((config) => ({
                    ...config,
                    network: { ...config.network, allowed_cidrs: parseList(event.target.value) },
                  }));
                }}
              />
            </label>
            <label>
              <span className="field-label">{t("settings.network.discoveryInterval")}</span>
              <input
                className="input"
                type="number"
                min={5}
                value={draft.network.discovery_interval_seconds}
                onChange={(event) => updateDraft((config) => ({
                  ...config,
                  network: { ...config.network, discovery_interval_seconds: Number(event.target.value) },
                }))}
              />
            </label>
            <label>
              <span className="field-label">{t("settings.network.pollInterval")}</span>
              <input
                className="input"
                type="number"
                min={1}
                value={draft.network.poll_interval_seconds}
                onChange={(event) => updateDraft((config) => ({
                  ...config,
                  network: { ...config.network, poll_interval_seconds: Number(event.target.value) },
                }))}
              />
            </label>
          </div>
        </section>

        <section className="settings-card protocol-settings">
          <div className="settings-card-heading">
            <div>
              <h2>{t("settings.airplay.title")}</h2>
              <p className="muted">{t("settings.airplay.description")}</p>
            </div>
            <label className="switch-label">
              <input
                type="checkbox"
                checked={draft.airplay.enabled}
                onChange={(event) => updateDraft((config) => ({
                  ...config,
                  airplay: { ...config.airplay, enabled: event.target.checked },
                }))}
              />
              {t("settings.protocol.enabled")}
            </label>
          </div>
          <label className="field-label" htmlFor="airplay-helper-path">{t("settings.airplay.helperPath")}</label>
          <input
            className="input"
            id="airplay-helper-path"
            value={draft.airplay.helper_path}
            disabled={!draft.airplay.enabled}
            required={draft.airplay.enabled}
            placeholder="/path/to/airplay-helper"
            onChange={(event) => updateDraft((config) => ({
              ...config,
              airplay: { ...config.airplay, helper_path: event.target.value },
            }))}
          />
          <p className="field-help">{t("settings.airplay.help")}</p>
        </section>

        <section className="settings-card">
          <h2>{t("settings.media.title")}</h2>
          <div className="field-grid">
            <label>
              <span className="field-label">{t("settings.media.baseUrl")}</span>
              <input
                className="input"
                value={draft.media.base_url}
                placeholder={t("settings.media.baseUrlPlaceholder")}
                onChange={(event) => updateDraft((config) => ({
                  ...config,
                  media: { ...config.media, base_url: event.target.value },
                }))}
              />
            </label>
            <label>
              <span className="field-label">{t("settings.media.ffmpegPath")}</span>
              <input
                className="input"
                value={draft.media.ffmpeg_path}
                placeholder="/usr/bin/ffmpeg"
                onChange={(event) => updateDraft((config) => ({
                  ...config,
                  media: { ...config.media, ffmpeg_path: event.target.value },
                }))}
              />
            </label>
          </div>
          <label className="switch-label inline-switch">
            <input
              type="checkbox"
              checked={draft.media.transcode}
              onChange={(event) => updateDraft((config) => ({
                ...config,
                media: { ...config.media, transcode: event.target.checked },
              }))}
            />
            {t("settings.media.transcode")}
          </label>
        </section>

        <div className="settings-save-row">
          <button
            className="button button-ghost"
            type="button"
            disabled={saving}
            onClick={() => {
              setDraft(structuredClone(document?.config ?? draft));
              setInterfacesText(listText((document?.config ?? draft).network.interfaces));
              setCidrsText(listText((document?.config ?? draft).network.allowed_cidrs));
              setError("");
            }}
          >
            {t("settings.discard")}
          </button>
          <button className="button button-primary" type="submit" disabled={saving}>
            {saving ? t("settings.saving") : t("settings.save")}
          </button>
        </div>
      </form>

      <section className="settings-card scan-settings" aria-labelledby="scan-heading">
        <div className="settings-card-heading">
          <div>
            <h2 id="scan-heading">{t("settings.scan.title")}</h2>
            <p className="muted">{t("settings.scan.description")}</p>
          </div>
          <button
            className="button button-primary"
            type="button"
            disabled={Boolean(scanBusy) || scans.some((scan) => scan.status === "queued" || scan.status === "running")}
            onClick={() => void startScan()}
          >
            {t(scanBusy === "new" ? "settings.scan.starting" : "settings.scan.start")}
          </button>
        </div>
        {scans.length === 0 ? (
          <p className="empty-inline">{t("settings.scan.empty")}</p>
        ) : (
          <ul className="scan-list">
            {scans.map((scan) => {
              const active = scan.status === "queued" || scan.status === "running";
              return (
                <li key={scan.id}>
                  <div className="scan-summary">
                    <strong>{t(`settings.scan.status.${scan.status}`)}</strong>
                    <span>{t("settings.scan.progress", {
                      processed: scan.processed.toLocaleString(locale),
                      discovered: scan.discovered.toLocaleString(locale),
                    })}</span>
                    <span>{t("settings.scan.changes", {
                      added: scan.added.toLocaleString(locale),
                      updated: scan.updated.toLocaleString(locale),
                      unavailable: scan.unavailable.toLocaleString(locale),
                    })}</span>
                    {scan.error && <span className="error-text">{scan.error}</span>}
                  </div>
                  {active && (
                    <button
                      className="button button-ghost danger-button"
                      type="button"
                      disabled={scanBusy === scan.id}
                      onClick={() => void cancelScan(scan.id)}
                    >
                      {t("settings.scan.cancel")}
                    </button>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </section>

      <section className="settings-card account-settings" aria-labelledby="password-heading">
        <h2 id="password-heading">{t("settings.account.title")}</h2>
        <p className="muted">{t("settings.account.description")}</p>
        <form className="password-form" onSubmit={(event) => void changePassword(event)}>
          <label>
            <span className="field-label">{t("settings.account.currentPassword")}</span>
            <input
              className="input"
              type="password"
              autoComplete="current-password"
              value={currentPassword}
              required
              onChange={(event) => setCurrentPassword(event.target.value)}
            />
          </label>
          <label>
            <span className="field-label">{t("settings.account.newPassword")}</span>
            <input
              className="input"
              type="password"
              autoComplete="new-password"
              minLength={10}
              value={newPassword}
              required
              onChange={(event) => setNewPassword(event.target.value)}
            />
          </label>
          <label>
            <span className="field-label">{t("settings.account.confirmPassword")}</span>
            <input
              className="input"
              type="password"
              autoComplete="new-password"
              minLength={10}
              value={confirmPassword}
              required
              onChange={(event) => setConfirmPassword(event.target.value)}
            />
          </label>
          <button className="button button-primary" type="submit" disabled={passwordBusy}>
            {passwordBusy ? t("settings.account.changing") : t("settings.account.change")}
          </button>
        </form>
      </section>
    </section>
  );
}
