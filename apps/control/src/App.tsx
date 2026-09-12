import { useCallback, useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { api, ApiError } from "./api";
import Library from "./Library";
import Playlists from "./Playlists";
import PlayerBar from "./PlayerBar";
import Queue from "./Queue";
import Settings from "./Settings";
import { useI18n, type MessageKey } from "./i18n";
import type { Session, SessionUser, StatusWarning } from "./types";

type View = "library" | "playlists" | "queue" | "settings";
type EventTopic = "player" | "queue" | "library" | "playlists" | "renderers" | "config";
type Revisions = Record<EventTopic, number>;
type ErrorNotice =
  | { source: "error"; message: string }
  | { source: "status"; id: number; message: string };

const initialRevisions: Revisions = {
  player: 0,
  queue: 0,
  library: 0,
  playlists: 0,
  renderers: 0,
  config: 0,
};

const navigation: Array<{ id: View; labelKey: MessageKey; icon: "library" | "playlist" | "queue" | "settings" }> = [
  { id: "library", labelKey: "app.nav.library", icon: "library" },
  { id: "playlists", labelKey: "app.nav.playlists", icon: "playlist" },
  { id: "queue", labelKey: "app.nav.queue", icon: "queue" },
  { id: "settings", labelKey: "app.nav.settings", icon: "settings" },
];

const logoURL = new URL("../../../assets/jastreamer.svg", import.meta.url).href;

function AppIcon({ name }: { name: "library" | "playlist" | "queue" | "settings" | "logout" | "logo" | "close" }) {
  if (name === "logo") return <img src={logoURL} alt="" width="512" height="512" />;
  const paths = {
    library: <><path d="M4 19V5M8 19V5M12 19V5M16 19V5l4 14V5" /></>,
    playlist: <><path d="M4 6h11M4 10h11M4 14h7" /><path d="M17 14v5" /><circle cx="14" cy="19" r="3" /></>,
    queue: <><path d="M5 6h14M5 12h14M5 18h9" /><path d="m17 16 3 2-3 2Z" /></>,
    settings: <><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1-2.8 2.8-.1-.1a1.7 1.7 0 0 0-1.9-.3 1.7 1.7 0 0 0-1 1.6v.2h-4V21a1.7 1.7 0 0 0-1-1.6 1.7 1.7 0 0 0-1.9.3l-.1.1L4.2 17l.1-.1a1.7 1.7 0 0 0 .3-1.9A1.7 1.7 0 0 0 3 14H2.8v-4H3a1.7 1.7 0 0 0 1.6-1 1.7 1.7 0 0 0-.3-1.9L4.2 7 7 4.2l.1.1a1.7 1.7 0 0 0 1.9.3 1.7 1.7 0 0 0 1-1.6v-.2h4V3a1.7 1.7 0 0 0 1 1.6 1.7 1.7 0 0 0 1.9-.3l.1-.1L19.8 7l-.1.1a1.7 1.7 0 0 0-.3 1.9 1.7 1.7 0 0 0 1.6 1h.2v4H21a1.7 1.7 0 0 0-1.6 1Z" /></>,
    logout: <><path d="M10 5H5v14h5M14 8l4 4-4 4M8 12h10" /></>,
    close: <path d="m6 6 12 12M18 6 6 18" />,
  };
  return <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false">{paths[name]}</svg>;
}

function authErrorMessage(error: unknown, t: (key: MessageKey) => string): string {
  return error instanceof ApiError ? error.message : t("app.requestFailed");
}

interface AuthScreenProps {
  setupRequired: boolean;
  onAuthenticated: (user: SessionUser) => void;
  onSetupComplete: () => void;
}

function AuthScreen({ setupRequired, onAuthenticated, onSetupComplete }: AuthScreenProps) {
  const { t } = useI18n();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (setupRequired && password !== confirmPassword) {
      setError(t("app.auth.passwordMismatch"));
      return;
    }
    setBusy(true);
    setError("");
    try {
      const result = await api<{ user: SessionUser }>(setupRequired ? "/setup" : "/login", {
        method: "POST",
        body: JSON.stringify({ username: username.trim(), password }),
      });
      setPassword("");
      setConfirmPassword("");
      onAuthenticated(result.user);
    } catch (requestError) {
      if (setupRequired && requestError instanceof ApiError && requestError.status === 409) {
        onSetupComplete();
        setError(t("app.auth.accountExists"));
      } else {
        setError(authErrorMessage(requestError, t));
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="auth-page">
      <section className="auth-card" aria-labelledby="auth-heading">
        <div className="auth-brand">
          <span className="brand-mark"><AppIcon name="logo" /></span>
          <span>jastreamer</span>
        </div>
        <div className="auth-copy">
          <p className="eyebrow">{t(setupRequired ? "app.auth.setupEyebrow" : "app.auth.loginEyebrow")}</p>
          <h1 id="auth-heading">{t(setupRequired ? "app.auth.setupTitle" : "app.auth.loginTitle")}</h1>
          <p className="muted">
            {t(setupRequired ? "app.auth.setupDescription" : "app.auth.loginDescription")}
          </p>
        </div>
        <form className="auth-form" onSubmit={(event) => void submit(event)}>
          <label>
            <span>{t("app.auth.username")}</span>
            <input
              className="input"
              autoComplete="username"
              value={username}
              required
              autoFocus
              onChange={(event) => setUsername(event.target.value)}
            />
          </label>
          <label>
            <span>{t("app.auth.password")}</span>
            <input
              className="input"
              type="password"
              autoComplete={setupRequired ? "new-password" : "current-password"}
              minLength={setupRequired ? 10 : undefined}
              value={password}
              required
              onChange={(event) => setPassword(event.target.value)}
            />
          </label>
          {setupRequired && (
            <label>
              <span>{t("app.auth.confirmPassword")}</span>
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
          )}
          {error && <p className="error-text" role="alert">{error}</p>}
          <button className="button button-primary auth-submit" type="submit" disabled={busy}>
            {busy ? t("app.auth.checking") : t(setupRequired ? "app.auth.createAccount" : "app.auth.signIn")}
          </button>
        </form>
        <p className="auth-security">{t("app.auth.passwordStorage")}</p>
        {window.location.protocol === "http:" && (
          <p className="auth-security">{t("app.auth.insecureHttp")}</p>
        )}
      </section>
    </main>
  );
}

export default function App() {
  const { t } = useI18n();
  const [booting, setBooting] = useState(true);
  const [initialized, setInitialized] = useState(false);
  const [bootstrapAttempt, setBootstrapAttempt] = useState(0);
  const [setupRequired, setSetupRequired] = useState(false);
  const [session, setSession] = useState<Session>({ authenticated: false });
  const [view, setView] = useState<View>("library");
  const [online, setOnline] = useState(true);
  const [revisions, setRevisions] = useState<Revisions>(initialRevisions);
  const [notice, setNotice] = useState<string | null>(null);
  const [errorNotice, setErrorNotice] = useState<ErrorNotice | null>(null);
  const hasErrorNotice = errorNotice !== null;
  const observedStatusWarning = useRef<number | null>(null);
  const noticeTimer = useRef<number | null>(null);
  const errorDialogClose = useRef<HTMLButtonElement | null>(null);
  const errorDialogPreviousFocus = useRef<HTMLElement | null>(null);

  const dismissNotice = useCallback(() => {
    if (noticeTimer.current !== null) {
      window.clearTimeout(noticeTimer.current);
      noticeTimer.current = null;
    }
    setNotice(null);
  }, []);

  const dismissErrorNotice = useCallback(() => {
    setErrorNotice(null);
  }, []);

  const showStatusWarning = useCallback((warning: StatusWarning | null, reopen = false) => {
    if (!warning) {
      observedStatusWarning.current = null;
      setErrorNotice((current) => current?.source === "status" ? null : current);
      return;
    }
    const firstWarning = observedStatusWarning.current !== warning.id;
    observedStatusWarning.current = warning.id;
    setErrorNotice((current) => {
      if (current?.source === "error") return current;
      if (current?.source === "status" && current.id === warning.id && current.message === warning.message) return current;
      if (current || firstWarning || reopen) return { source: "status", ...warning };
      return null;
    });
  }, []);

  const showNotice = useCallback((message: string, error = false) => {
    if (error) {
      if (noticeTimer.current !== null) {
        window.clearTimeout(noticeTimer.current);
        noticeTimer.current = null;
      }
      setNotice(null);
      setErrorNotice({ source: "error", message });
      return;
    }

    setNotice(message);
    if (noticeTimer.current !== null) window.clearTimeout(noticeTimer.current);
    noticeTimer.current = window.setTimeout(() => {
      setNotice(null);
      noticeTimer.current = null;
    }, 5000);
  }, []);

  const refreshAll = useCallback(() => {
    setRevisions((current) => ({
      player: current.player + 1,
      queue: current.queue + 1,
      library: current.library + 1,
      playlists: current.playlists + 1,
      renderers: current.renderers + 1,
      config: current.config + 1,
    }));
  }, []);

  useEffect(() => {
    let active = true;
    async function bootstrap() {
      try {
        const [setup, currentSession] = await Promise.all([
          api<{ required: boolean }>("/setup"),
          api<Session>("/session"),
        ]);
        if (!active) return;
        setSetupRequired(setup.required);
        setSession(currentSession);
        setOnline(true);
        setInitialized(true);
      } catch (requestError) {
        if (!active) return;
        setOnline(false);
        showNotice(authErrorMessage(requestError, t), true);
      } finally {
        if (active) setBooting(false);
      }
    }
    void bootstrap();
    return () => {
      active = false;
    };
  }, [bootstrapAttempt, showNotice, t]);

  useEffect(() => {
    function requireAuthentication() {
      setSession({ authenticated: false });
    }
    window.addEventListener("jastreamer:auth-required", requireAuthentication);
    return () => window.removeEventListener("jastreamer:auth-required", requireAuthentication);
  }, []);

  useEffect(() => {
    if (!session.authenticated) return;
    let source: EventSource | null = null;
    let reconnectTimer: number | null = null;
    let stopped = false;
    let delay = 1000;

    function connect() {
      if (stopped) return;
      source = new EventSource("/api/v1/events", { withCredentials: true });
      source.addEventListener("open", () => {
        delay = 1000;
        setOnline(true);
        refreshAll();
      });
      source.addEventListener("change", (event) => {
        try {
          const payload = JSON.parse((event as MessageEvent<string>).data) as { type?: string };
          if (payload.type === "resync") {
            refreshAll();
            return;
          }
          if (payload.type && payload.type in initialRevisions) {
            const topic = payload.type as EventTopic;
            setRevisions((current) => ({ ...current, [topic]: current[topic] + 1 }));
          }
        } catch {
          refreshAll();
        }
      });
      source.addEventListener("error", () => {
        source?.close();
        source = null;
        setOnline(false);
        void api<Session>("/session")
          .then((current) => {
            if (!current.authenticated) setSession(current);
          })
          .catch(() => undefined);
        reconnectTimer = window.setTimeout(connect, delay);
        delay = Math.min(delay * 2, 30_000);
      });
    }

    connect();
    return () => {
      stopped = true;
      source?.close();
      if (reconnectTimer !== null) window.clearTimeout(reconnectTimer);
    };
  }, [refreshAll, session.authenticated]);

  useEffect(() => {
    if (!hasErrorNotice) return;

    errorDialogPreviousFocus.current = document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null;
    errorDialogClose.current?.focus();

    function handleDialogKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") {
        event.preventDefault();
        dismissErrorNotice();
      } else if (event.key === "Tab") {
        event.preventDefault();
        errorDialogClose.current?.focus();
      }
    }

    document.addEventListener("keydown", handleDialogKeyDown);
    return () => {
      document.removeEventListener("keydown", handleDialogKeyDown);
      const previousFocus = errorDialogPreviousFocus.current;
      errorDialogPreviousFocus.current = null;
      if (previousFocus?.isConnected) previousFocus.focus();
    };
  }, [dismissErrorNotice, hasErrorNotice]);

  useEffect(() => () => {
    if (noticeTimer.current !== null) window.clearTimeout(noticeTimer.current);
  }, []);

  async function logout() {
    try {
      await api<void>("/logout", { method: "POST", body: JSON.stringify({}) });
    } catch (requestError) {
      showNotice(authErrorMessage(requestError, t), true);
      return;
    }
    setSession({ authenticated: false });
  }

  const noticeView = notice ? (
    <div className="toast" role="status">
      {notice}
      <button type="button" aria-label={t("app.notice.dismiss")} onClick={dismissNotice}>
        <AppIcon name="close" />
      </button>
    </div>
  ) : null;

  const errorDialogView = errorNotice ? (
    <div
      className="error-dialog-backdrop"
      role="presentation"
      onMouseDown={(event) => {
        if (event.currentTarget === event.target) dismissErrorNotice();
      }}
    >
      <section
        className="error-dialog"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby="error-dialog-title"
        aria-describedby="error-dialog-message"
      >
        <div className="error-dialog-heading">
          <div>
            <p className="eyebrow">{t(errorNotice.source === "status" ? "app.statusWarning.eyebrow" : "app.error.eyebrow")}</p>
            <h2 id="error-dialog-title">{t(errorNotice.source === "status" ? "app.statusWarning.title" : "app.error.title")}</h2>
          </div>
          <button
            ref={errorDialogClose}
            className="icon-button error-dialog-close"
            type="button"
            aria-label={t(errorNotice.source === "status" ? "app.statusWarning.dismiss" : "app.error.dismiss")}
            onClick={dismissErrorNotice}
          >
            <AppIcon name="close" />
          </button>
        </div>
        <p id="error-dialog-message" className="error-dialog-message">{errorNotice.message}</p>
      </section>
    </div>
  ) : null;

  if (booting) {
    return (
      <main className="splash-screen" aria-live="polite">
        <span className="brand-mark"><AppIcon name="logo" /></span>
        <strong>jastreamer</strong>
        <span className="muted">{t("app.boot.connecting")}</span>
      </main>
    );
  }

  if (!initialized) {
    return (
      <main className="auth-page">
        <section className="auth-card connection-card" aria-labelledby="connection-heading">
          <div className="auth-brand">
            <span className="brand-mark"><AppIcon name="logo" /></span>
            <span>jastreamer</span>
          </div>
          <p className="eyebrow">{t("app.connection.eyebrow")}</p>
          <h1 id="connection-heading">{t("app.connection.title")}</h1>
          <p className="muted">{t("app.connection.description")}</p>
          <button
            className="button button-primary auth-submit"
            type="button"
            onClick={() => {
              setBooting(true);
              setBootstrapAttempt((current) => current + 1);
            }}
          >
            {t("app.connection.retry")}
          </button>
        </section>
        {noticeView}
        {errorDialogView}
      </main>
    );
  }

  if (!session.authenticated) {
    return (
      <>
        {!online && (
          <div className="offline-banner" role="status">{t("app.connection.authOffline")}</div>
        )}
        <AuthScreen
          setupRequired={setupRequired}
          onAuthenticated={(user) => {
            setSession({ authenticated: true, user });
            setSetupRequired(false);
            setOnline(true);
          }}
          onSetupComplete={() => setSetupRequired(false)}
        />
        {noticeView}
        {errorDialogView}
      </>
    );
  }

  let page: ReactNode;
  switch (view) {
    case "playlists":
      page = (
        <Playlists
          revision={revisions.playlists + revisions.library}
          onNotice={showNotice}
          onQueueChange={() => setRevisions((current) => ({ ...current, queue: current.queue + 1 }))}
        />
      );
      break;
    case "queue":
      page = (
        <Queue
          revision={revisions.queue}
          onNotice={showNotice}
          onQueueChange={() => setRevisions((current) => ({ ...current, queue: current.queue + 1, player: current.player + 1 }))}
        />
      );
      break;
    case "settings":
      page = (
        <Settings
          configRevision={revisions.config}
          libraryRevision={revisions.library}
          onNotice={showNotice}
          onSignedOut={() => {
            setSession({ authenticated: false });
            showNotice(t("app.account.passwordChanged"));
          }}
        />
      );
      break;
    default:
      page = (
        <Library
          revision={revisions.library}
          onNotice={showNotice}
          onQueueChange={() => setRevisions((current) => ({ ...current, queue: current.queue + 1 }))}
        />
      );
  }

  return (
    <div className="app-shell">
      {!online && <div className="offline-banner" role="status">{t("app.connection.reconnecting")}</div>}
      <aside className="sidebar">
        <div className="brand">
          <span className="brand-mark"><AppIcon name="logo" /></span>
          <span>jastreamer</span>
        </div>
        <nav className="main-nav" aria-label={t("app.nav.main")}>
          {navigation.map((item) => (
            <button
              className={view === item.id ? "is-active" : ""}
              type="button"
              aria-current={view === item.id ? "page" : undefined}
              key={item.id}
              onClick={() => setView(item.id)}
            >
              <AppIcon name={item.icon} />
              <span>{t(item.labelKey)}</span>
            </button>
          ))}
        </nav>
        <div className="sidebar-account">
          <span title={session.user?.username}>{session.user?.username}</span>
          <button className="icon-button" type="button" aria-label={t("app.account.signOut")} onClick={() => void logout()}>
            <AppIcon name="logout" />
          </button>
        </div>
      </aside>

      <header className="mobile-header">
        <div className="brand">
          <span className="brand-mark"><AppIcon name="logo" /></span>
          <span>jastreamer</span>
        </div>
        <div className="mobile-account">
          <span>{session.user?.username}</span>
          <button className="icon-button" type="button" aria-label={t("app.account.signOut")} onClick={() => void logout()}>
            <AppIcon name="logout" />
          </button>
        </div>
      </header>

      <main className="main-content" id="main-content">{page}</main>

      <nav className="mobile-nav" aria-label={t("app.nav.main")}>
        {navigation.map((item) => (
          <button
            className={view === item.id ? "is-active" : ""}
            type="button"
            aria-current={view === item.id ? "page" : undefined}
            key={item.id}
            onClick={() => setView(item.id)}
          >
            <AppIcon name={item.icon} />
            <span>{t(item.labelKey)}</span>
          </button>
        ))}
      </nav>

      <PlayerBar
        revision={revisions.player + revisions.renderers}
        onNotice={showNotice}
        onStatusWarning={showStatusWarning}
        onShowQueue={() => setView("queue")}
        onQueueChange={() => setRevisions((current) => ({ ...current, queue: current.queue + 1, player: current.player + 1 }))}
      />

      {noticeView}
      {errorDialogView}
    </div>
  );
}
