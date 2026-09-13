import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { api, ApiError } from "./api";
import { useI18n, type Language, type MessageKey } from "./i18n";
import ServerPathPicker from "./ServerPathPicker";
import type { ConfigDocument, ConfigRoot, FilesystemEntryKind, NetworkInterfacesDocument, ScanJob, ServerConfig } from "./types";

interface SettingsProps {
  configRevision: number;
  libraryRevision: number;
  onNotice: (message: string, error?: boolean) => void;
  onSignedOut: () => void;
}

type PathPickerTarget =
  | { field: "data_dir" | "certificate_file" | "private_key_file" | "ffmpeg_path" | "helper_path" }
  | { field: "library_root"; rootID: string };

interface OpenPathPicker {
  kind: FilesystemEntryKind;
  label: string;
  value: string;
  target: PathPickerTarget;
}

interface PathFieldProps {
  id: string;
  label: string;
  value: string;
  browseLabel: string;
  browseText: string;
  onChange: (value: string) => void;
  onBrowse: () => void;
  disabled?: boolean;
  required?: boolean;
  placeholder?: string;
}

function PathField({ id, label, value, browseLabel, browseText, onChange, onBrowse, disabled, required, placeholder }: PathFieldProps) {
  return (
    <div className="server-path-field">
      <label className="field-label" htmlFor={id}>{label}</label>
      <div className="server-path-input-row">
        <input
          className="input"
          id={id}
          value={value}
          disabled={disabled}
          required={required}
          placeholder={placeholder}
          spellCheck={false}
          onChange={(event) => onChange(event.target.value)}
        />
        <button className="button button-ghost" type="button" disabled={disabled} aria-label={browseLabel} onClick={onBrowse}>{browseText}</button>
      </div>
    </div>
  );
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

function listenerPort(address: string): string | null {
  const match = address.match(/:(\d{1,5})$/);
  if (!match) return null;
  const port = Number(match[1]);
  return port >= 1 && port <= 65_535 ? match[1] : null;
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
  const [pathPicker, setPathPicker] = useState<OpenPathPicker | null>(null);
  const [networkInterfaces, setNetworkInterfaces] = useState<NetworkInterfacesDocument | null>(null);
  const [networkInterfacesLoading, setNetworkInterfacesLoading] = useState(true);
  const [networkInterfacesError, setNetworkInterfacesError] = useState("");
  const [networkInterfacesRequest, setNetworkInterfacesRequest] = useState(0);
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

  useEffect(() => {
    const controller = new AbortController();
    let active = true;
    setNetworkInterfacesLoading(true);
    setNetworkInterfacesError("");
    api<NetworkInterfacesDocument>("/network/interfaces", { signal: controller.signal })
      .then((next) => {
        if (active) setNetworkInterfaces(next);
      })
      .catch((caught: unknown) => {
        if (!active || (caught instanceof DOMException && caught.name === "AbortError")) return;
        setNetworkInterfacesError(requestMessage(caught, t));
      })
      .finally(() => {
        if (active) setNetworkInterfacesLoading(false);
      });
    return () => {
      active = false;
      controller.abort();
    };
  }, [networkInterfacesRequest, t]);

  function updateDraft(update: (config: ServerConfig) => ServerConfig) {
    setDraft((current) => (current ? update(current) : current));
  }

  function choosePath(path: string) {
    if (!pathPicker) return;
    const target = pathPicker.target;
    updateDraft((config) => {
      switch (target.field) {
        case "data_dir":
          return { ...config, data_dir: path };
        case "certificate_file":
          return { ...config, https: { ...config.https, certificate_file: path } };
        case "private_key_file":
          return { ...config, https: { ...config.https, private_key_file: path } };
        case "ffmpeg_path":
          return { ...config, media: { ...config.media, ffmpeg_path: path } };
        case "helper_path":
          return { ...config, airplay: { ...config.airplay, helper_path: path } };
        case "library_root":
          return {
            ...config,
            library_roots: config.library_roots.map((root) => root.id === target.rootID ? { ...root, path } : root),
          };
      }
    });
    setPathPicker(null);
  }

  function replaceSelectedInterfaces(names: string[]) {
    const text = listText(names);
    setInterfacesText(text);
    updateDraft((config) => ({
      ...config,
      network: { ...config.network, interfaces: names },
    }));
  }

  function changeInterfaceSelection(name: string, selected: boolean) {
    const current = parseList(interfacesText);
    if (selected) {
      if (!current.includes(name)) replaceSelectedInterfaces([...current, name]);
      return;
    }
    replaceSelectedInterfaces(current.filter((item) => item !== name));
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

  const selectedInterfaceNames = parseList(interfacesText);
  const eligibleInterfaces = networkInterfaces?.interfaces.filter((item) =>
    item.addresses.some((address) => address.upnp_usable),
  ) ?? [];
  const enabledMediaListeners = [
    { scheme: "http", port: draft.http.enabled ? listenerPort(draft.http.address) : null },
    { scheme: "https", port: draft.https.enabled ? listenerPort(draft.https.address) : null },
  ].filter((listener): listener is { scheme: string; port: string } => listener.port !== null);
  const mediaOriginOptions = eligibleInterfaces.flatMap((item) =>
    item.addresses.filter((address) => address.upnp_usable).flatMap((address) =>
      enabledMediaListeners.map((listener) => ({
        value: `${listener.scheme}://${address.address}:${listener.port}`,
        label: `${item.name} — ${listener.scheme.toUpperCase()} — ${address.address}:${listener.port}`,
      })),
    ),
  );
  const selectedMediaOrigin = draft.media.base_url === ""
    ? ""
    : mediaOriginOptions.some((option) => option.value === draft.media.base_url)
      ? draft.media.base_url
      : "__manual__";

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
          <PathField
            id="data-directory"
            label={t("settings.storage.dataDirectory")}
            value={draft.data_dir}
            required
            browseText={t("settings.pathPicker.browse")}
            browseLabel={t("settings.pathPicker.browseLabel", { label: t("settings.storage.dataDirectory") })}
            onChange={(value) => updateDraft((config) => ({ ...config, data_dir: value }))}
            onBrowse={() => setPathPicker({
              kind: "directory",
              label: t("settings.storage.dataDirectory"),
              value: draft.data_dir,
              target: { field: "data_dir" },
            })}
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
            <PathField
              id="https-certificate-file"
              label={t("settings.https.certificate")}
              value={draft.https.certificate_file}
              disabled={!draft.https.enabled}
              required={draft.https.enabled}
              placeholder="/etc/jastreamer/server.crt"
              browseText={t("settings.pathPicker.browse")}
              browseLabel={t("settings.pathPicker.browseLabel", { label: t("settings.https.certificate") })}
              onChange={(value) => updateDraft((config) => ({
                ...config,
                https: { ...config.https, certificate_file: value },
              }))}
              onBrowse={() => setPathPicker({
                kind: "file",
                label: t("settings.https.certificate"),
                value: draft.https.certificate_file,
                target: { field: "certificate_file" },
              })}
            />
            <PathField
              id="https-private-key-file"
              label={t("settings.https.privateKey")}
              value={draft.https.private_key_file}
              disabled={!draft.https.enabled}
              required={draft.https.enabled}
              placeholder="/etc/jastreamer/server.key"
              browseText={t("settings.pathPicker.browse")}
              browseLabel={t("settings.pathPicker.browseLabel", { label: t("settings.https.privateKey") })}
              onChange={(value) => updateDraft((config) => ({
                ...config,
                https: { ...config.https, private_key_file: value },
              }))}
              onBrowse={() => setPathPicker({
                kind: "file",
                label: t("settings.https.privateKey"),
                value: draft.https.private_key_file,
                target: { field: "private_key_file" },
              })}
            />
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
                  <PathField
                    id={`library-root-path-${root.id}`}
                    label={t("settings.roots.path")}
                    value={root.path}
                    required
                    placeholder="/music"
                    browseText={t("settings.pathPicker.browse")}
                    browseLabel={t("settings.pathPicker.browseLabel", { label: `${root.name || t("settings.roots.fallbackName")} — ${t("settings.roots.path")}` })}
                    onChange={(value) => changeRoot(index, "path", value)}
                    onBrowse={() => setPathPicker({
                      kind: "directory",
                      label: `${root.name || t("settings.roots.fallbackName")} — ${t("settings.roots.path")}`,
                      value: root.path,
                      target: { field: "library_root", rootID: root.id },
                    })}
                  />
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
          <section className="server-interface-picker" aria-labelledby="server-adapters-heading">
            <div className="server-interface-heading">
              <div>
                <h3 id="server-adapters-heading">{t("settings.network.adapters.title")}</h3>
                <p id="server-adapters-description">{t("settings.network.adapters.description")} {t("settings.network.adapters.automaticHelp")}</p>
              </div>
              <button
                className={`button ${selectedInterfaceNames.length === 0 ? "button-primary" : "button-ghost"}`}
                type="button"
                aria-pressed={selectedInterfaceNames.length === 0}
                aria-describedby="server-adapters-description"
                title={t("settings.network.adapters.automaticHelp")}
                onClick={() => replaceSelectedInterfaces([])}
              >
                {t("settings.network.adapters.automatic")}
              </button>
            </div>
            {networkInterfacesLoading && <p className="server-interface-status" role="status">{t("settings.network.adapters.loading")}</p>}
            {networkInterfacesError && (
              <div className="inline-error" role="alert">
                <span>{networkInterfacesError}</span>
                <button className="button button-ghost" type="button" onClick={() => setNetworkInterfacesRequest((value) => value + 1)}>{t("settings.network.adapters.retry")}</button>
              </div>
            )}
            {!networkInterfacesLoading && !networkInterfacesError && networkInterfaces?.interfaces.length === 0 && (
              <p className="server-interface-status">{t("settings.network.adapters.empty")}</p>
            )}
            {!networkInterfacesLoading && !networkInterfacesError && networkInterfaces && networkInterfaces.interfaces.length > 0 && (
              <fieldset className="server-interface-list">
                <legend>{t("settings.network.adapters.list")}</legend>
                {networkInterfaces.interfaces.map((item) => {
                  const selected = selectedInterfaceNames.includes(item.name);
                  const eligible = item.addresses.some((address) => address.upnp_usable);
                  return (
                    <label className={`server-interface-option${eligible ? "" : " is-unavailable"}`} key={`${item.index}:${item.name}`}>
                      <input
                        type="checkbox"
                        checked={selected}
                        disabled={!eligible && !selected}
                        onChange={(event) => changeInterfaceSelection(item.name, event.target.checked)}
                      />
                      <span className="server-interface-copy">
                        <span className="server-interface-name">
                          <strong>{item.name}</strong>
                          {!eligible && <em>{t(selected ? "settings.network.adapters.selectedUnavailable" : "settings.network.adapters.unavailable")}</em>}
                        </span>
                        <span className="server-interface-flags">
                          {!item.up && <span>{t("settings.network.adapters.down")}</span>}
                          {!item.multicast && <span>{t("settings.network.adapters.noMulticast")}</span>}
                          {item.loopback && <span>{t("settings.network.adapters.loopback")}</span>}
                        </span>
                        <span className="server-interface-addresses">
                          {item.addresses.length === 0 ? t("settings.network.adapters.noAddresses") : item.addresses.map((address) => (
                            <span key={`${address.address}/${address.prefix_length}`}>
                              <code>{address.address}/{address.prefix_length}</code>
                              {!address.upnp_usable && <em>{t("settings.network.adapters.addressUnavailable")}</em>}
                            </span>
                          ))}
                        </span>
                      </span>
                    </label>
                  );
                })}
              </fieldset>
            )}
          </section>
        </section>

        <section className="settings-card protocol-settings">
          <div className="settings-card-heading">
            <div>
              <h2>{t("settings.cast.title")}</h2>
              <p className="muted">{t("settings.cast.description")}</p>
            </div>
            <label className="switch-label">
              <input
                type="checkbox"
                checked={draft.cast.enabled}
                onChange={(event) => updateDraft((config) => ({
                  ...config,
                  cast: { ...config.cast, enabled: event.target.checked },
                }))}
              />
              {t("settings.protocol.enabled")}
            </label>
          </div>
          <p className="field-help">{t("settings.cast.help")}</p>
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
          <PathField
            id="airplay-helper-path"
            label={t("settings.airplay.helperPath")}
            value={draft.airplay.helper_path}
            disabled={!draft.airplay.enabled}
            required={draft.airplay.enabled}
            placeholder="/path/to/airplay-helper"
            browseText={t("settings.pathPicker.browse")}
            browseLabel={t("settings.pathPicker.browseLabel", { label: t("settings.airplay.helperPath") })}
            onChange={(value) => updateDraft((config) => ({
              ...config,
              airplay: { ...config.airplay, helper_path: value },
            }))}
            onBrowse={() => setPathPicker({
              kind: "file",
              label: t("settings.airplay.helperPath"),
              value: draft.airplay.helper_path,
              target: { field: "helper_path" },
            })}
          />
          <p className="field-help">{t("settings.airplay.help")}</p>
        </section>

        <section className="settings-card">
          <h2>{t("settings.media.title")}</h2>
          <div className="field-grid">
            <div className="media-base-url-field">
              <label className="field-label" htmlFor="media-base-url">{t("settings.media.baseUrl")}</label>
              <input
                className="input"
                id="media-base-url"
                value={draft.media.base_url}
                placeholder={t("settings.media.baseUrlPlaceholder")}
                onChange={(event) => updateDraft((config) => ({
                  ...config,
                  media: { ...config.media, base_url: event.target.value },
                }))}
              />
              <label className="field-label media-quick-url-label" htmlFor="media-quick-url">{t("settings.media.quickUrl")}</label>
              <select
                className="input"
                id="media-quick-url"
                value={selectedMediaOrigin}
                onChange={(event) => {
                  if (event.target.value === "__manual__") return;
                  updateDraft((config) => ({
                    ...config,
                    media: { ...config.media, base_url: event.target.value },
                  }));
                }}
              >
                <option value="">{t("settings.media.quickUrlAutomatic")}</option>
                {selectedMediaOrigin === "__manual__" && <option value="__manual__">{t("settings.media.quickUrlManual")}</option>}
                {mediaOriginOptions.length === 0 && <option value="__unavailable__" disabled>{t("settings.media.quickUrlUnavailable")}</option>}
                {mediaOriginOptions.map((option) => <option value={option.value} key={`${option.label}:${option.value}`}>{option.label}</option>)}
              </select>
              <p className="field-help">{t("settings.media.quickUrlHelp")}</p>
            </div>
            <PathField
              id="ffmpeg-path"
              label={t("settings.media.ffmpegPath")}
              value={draft.media.ffmpeg_path}
              placeholder="/usr/bin/ffmpeg"
              browseText={t("settings.pathPicker.browse")}
              browseLabel={t("settings.pathPicker.browseLabel", { label: t("settings.media.ffmpegPath") })}
              onChange={(value) => updateDraft((config) => ({
                ...config,
                media: { ...config.media, ffmpeg_path: value },
              }))}
              onBrowse={() => setPathPicker({
                kind: "file",
                label: t("settings.media.ffmpegPath"),
                value: draft.media.ffmpeg_path,
                target: { field: "ffmpeg_path" },
              })}
            />
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
      {pathPicker && (
        <ServerPathPicker
          kind={pathPicker.kind}
          label={pathPicker.label}
          value={pathPicker.value}
          onChoose={choosePath}
          onClose={() => setPathPicker(null)}
        />
      )}
    </section>
  );
}
