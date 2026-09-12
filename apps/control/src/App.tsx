import { useCallback, useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { api, ApiError } from "./api";
import Library from "./Library";
import Playlists from "./Playlists";
import PlayerBar from "./PlayerBar";
import Queue from "./Queue";
import Settings from "./Settings";
import type { Session, SessionUser } from "./types";

type View = "library" | "playlists" | "queue" | "settings";
type EventTopic = "player" | "queue" | "library" | "playlists" | "renderers" | "config";
type Revisions = Record<EventTopic, number>;

const initialRevisions: Revisions = {
  player: 0,
  queue: 0,
  library: 0,
  playlists: 0,
  renderers: 0,
  config: 0,
};

const navigation: Array<{ id: View; label: string; icon: "library" | "playlist" | "queue" | "settings" }> = [
  { id: "library", label: "보관함", icon: "library" },
  { id: "playlists", label: "플레이리스트", icon: "playlist" },
  { id: "queue", label: "대기열", icon: "queue" },
  { id: "settings", label: "설정", icon: "settings" },
];

function AppIcon({ name }: { name: "library" | "playlist" | "queue" | "settings" | "logout" | "logo" | "close" }) {
  const paths = {
    library: <><path d="M4 19V5M8 19V5M12 19V5M16 19V5l4 14V5" /></>,
    playlist: <><path d="M4 6h11M4 10h11M4 14h7" /><path d="M17 14v5" /><circle cx="14" cy="19" r="3" /></>,
    queue: <><path d="M5 6h14M5 12h14M5 18h9" /><path d="m17 16 3 2-3 2Z" /></>,
    settings: <><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1-2.8 2.8-.1-.1a1.7 1.7 0 0 0-1.9-.3 1.7 1.7 0 0 0-1 1.6v.2h-4V21a1.7 1.7 0 0 0-1-1.6 1.7 1.7 0 0 0-1.9.3l-.1.1L4.2 17l.1-.1a1.7 1.7 0 0 0 .3-1.9A1.7 1.7 0 0 0 3 14H2.8v-4H3a1.7 1.7 0 0 0 1.6-1 1.7 1.7 0 0 0-.3-1.9L4.2 7 7 4.2l.1.1a1.7 1.7 0 0 0 1.9.3 1.7 1.7 0 0 0 1-1.6v-.2h4V3a1.7 1.7 0 0 0 1 1.6 1.7 1.7 0 0 0 1.9-.3l.1-.1L19.8 7l-.1.1a1.7 1.7 0 0 0-.3 1.9 1.7 1.7 0 0 0 1.6 1h.2v4H21a1.7 1.7 0 0 0-1.6 1Z" /></>,
    logout: <><path d="M10 5H5v14h5M14 8l4 4-4 4M8 12h10" /></>,
    logo: <><path d="M7 17V6l10-2v11" /><circle cx="5" cy="17" r="2" /><circle cx="15" cy="15" r="2" /></>,
    close: <path d="m6 6 12 12M18 6 6 18" />,
  };
  return <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false">{paths[name]}</svg>;
}

function authErrorMessage(error: unknown): string {
  return error instanceof ApiError ? error.message : "요청을 완료하지 못했습니다.";
}

interface AuthScreenProps {
  setupRequired: boolean;
  onAuthenticated: (user: SessionUser) => void;
  onSetupComplete: () => void;
}

function AuthScreen({ setupRequired, onAuthenticated, onSetupComplete }: AuthScreenProps) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (setupRequired && password !== confirmPassword) {
      setError("비밀번호가 서로 일치하지 않습니다.");
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
        setError("관리자 계정이 이미 만들어졌습니다. 계정으로 로그인해 주세요.");
      } else {
        setError(authErrorMessage(requestError));
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
          <p className="eyebrow">{setupRequired ? "처음 시작" : "다시 오신 것을 환영합니다"}</p>
          <h1 id="auth-heading">{setupRequired ? "관리자 계정 만들기" : "로그인"}</h1>
          <p className="muted">
            {setupRequired
              ? "이 계정으로 음악 보관함과 재생 기기를 관리합니다."
              : "서버 계정으로 음악 보관함에 접속하세요."}
          </p>
        </div>
        <form className="auth-form" onSubmit={(event) => void submit(event)}>
          <label>
            <span>사용자 이름</span>
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
            <span>비밀번호</span>
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
              <span>비밀번호 확인</span>
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
            {busy ? "확인 중…" : setupRequired ? "계정 만들기" : "로그인"}
          </button>
        </form>
        <p className="auth-security">비밀번호는 서버에 해시로 저장합니다. 로그인 상태는 자동 쿠키로 유지합니다.</p>
        {window.location.protocol === "http:" && (
          <p className="auth-security">현재 HTTP 연결은 암호화되지 않습니다. 신뢰하는 사설망에서만 사용하세요.</p>
        )}
      </section>
    </main>
  );
}

export default function App() {
  const [booting, setBooting] = useState(true);
  const [initialized, setInitialized] = useState(false);
  const [bootstrapAttempt, setBootstrapAttempt] = useState(0);
  const [setupRequired, setSetupRequired] = useState(false);
  const [session, setSession] = useState<Session>({ authenticated: false });
  const [view, setView] = useState<View>("library");
  const [online, setOnline] = useState(true);
  const [revisions, setRevisions] = useState<Revisions>(initialRevisions);
  const [notice, setNotice] = useState<string | null>(null);
  const [errorNotice, setErrorNotice] = useState<string | null>(null);
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

  const showNotice = useCallback((message: string, error = false) => {
    if (error) {
      if (noticeTimer.current !== null) {
        window.clearTimeout(noticeTimer.current);
        noticeTimer.current = null;
      }
      setNotice(null);
      setErrorNotice(message);
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
        showNotice(authErrorMessage(requestError), true);
      } finally {
        if (active) setBooting(false);
      }
    }
    void bootstrap();
    return () => {
      active = false;
    };
  }, [bootstrapAttempt, showNotice]);

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
    if (errorNotice === null) return;

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
  }, [dismissErrorNotice, errorNotice]);

  useEffect(() => () => {
    if (noticeTimer.current !== null) window.clearTimeout(noticeTimer.current);
  }, []);

  async function logout() {
    try {
      await api<void>("/logout", { method: "POST", body: JSON.stringify({}) });
    } catch (requestError) {
      showNotice(authErrorMessage(requestError), true);
      return;
    }
    setSession({ authenticated: false });
  }

  const noticeView = notice ? (
    <div className="toast" role="status">
      {notice}
      <button type="button" aria-label="알림 닫기" onClick={dismissNotice}>
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
            <p className="eyebrow">오류</p>
            <h2 id="error-dialog-title">요청을 완료하지 못했습니다</h2>
          </div>
          <button
            ref={errorDialogClose}
            className="icon-button error-dialog-close"
            type="button"
            aria-label="오류 닫기"
            onClick={dismissErrorNotice}
          >
            <AppIcon name="close" />
          </button>
        </div>
        <p id="error-dialog-message" className="error-dialog-message">{errorNotice}</p>
      </section>
    </div>
  ) : null;

  if (booting) {
    return (
      <main className="splash-screen" aria-live="polite">
        <span className="brand-mark"><AppIcon name="logo" /></span>
        <strong>jastreamer</strong>
        <span className="muted">서버에 연결하는 중…</span>
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
          <p className="eyebrow">연결 대기</p>
          <h1 id="connection-heading">서버에 연결할 수 없습니다</h1>
          <p className="muted">네트워크와 jastreamer 서버 상태를 확인한 뒤 다시 시도해 주세요.</p>
          <button
            className="button button-primary auth-submit"
            type="button"
            onClick={() => {
              setBooting(true);
              setBootstrapAttempt((current) => current + 1);
            }}
          >
            다시 연결
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
          <div className="offline-banner" role="status">서버에 연결할 수 없습니다. 연결을 확인해 주세요.</div>
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
            showNotice("비밀번호를 변경했습니다. 새 비밀번호로 로그인해 주세요.");
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
      {!online && <div className="offline-banner" role="status">연결이 끊겼습니다. 자동으로 다시 연결합니다.</div>}
      <aside className="sidebar">
        <div className="brand">
          <span className="brand-mark"><AppIcon name="logo" /></span>
          <span>jastreamer</span>
        </div>
        <nav className="main-nav" aria-label="주 메뉴">
          {navigation.map((item) => (
            <button
              className={view === item.id ? "is-active" : ""}
              type="button"
              aria-current={view === item.id ? "page" : undefined}
              key={item.id}
              onClick={() => setView(item.id)}
            >
              <AppIcon name={item.icon} />
              <span>{item.label}</span>
            </button>
          ))}
        </nav>
        <div className="sidebar-account">
          <span title={session.user?.username}>{session.user?.username}</span>
          <button className="icon-button" type="button" aria-label="로그아웃" onClick={() => void logout()}>
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
          <button className="icon-button" type="button" aria-label="로그아웃" onClick={() => void logout()}>
            <AppIcon name="logout" />
          </button>
        </div>
      </header>

      <main className="main-content" id="main-content">{page}</main>

      <nav className="mobile-nav" aria-label="주 메뉴">
        {navigation.map((item) => (
          <button
            className={view === item.id ? "is-active" : ""}
            type="button"
            aria-current={view === item.id ? "page" : undefined}
            key={item.id}
            onClick={() => setView(item.id)}
          >
            <AppIcon name={item.icon} />
            <span>{item.label}</span>
          </button>
        ))}
      </nav>

      <PlayerBar
        revision={revisions.player + revisions.renderers}
        onNotice={showNotice}
        onQueueChange={() => setRevisions((current) => ({ ...current, queue: current.queue + 1, player: current.player + 1 }))}
      />

      {noticeView}
      {errorDialogView}
    </div>
  );
}
