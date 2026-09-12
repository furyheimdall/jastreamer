"use strict";

const api = window.jastreamerDesktop;
const elements = {
  currentName: document.querySelector("#current-name"),
  currentOrigin: document.querySelector("#current-origin"),
  connectionState: document.querySelector("#connection-state"),
  changeServer: document.querySelector("#change-server"),
  selectionScreen: document.querySelector("#selection-screen"),
  workspaceScreen: document.querySelector("#workspace-screen"),
  manualForm: document.querySelector("#manual-form"),
  manualInput: document.querySelector("#manual-form input"),
  inlineError: document.querySelector("#inline-error"),
  refresh: document.querySelector("#selection-screen #refresh"),
  discovered: document.querySelector("#discovered-list"),
  recents: document.querySelector("#recent-list"),
  loading: document.querySelector("#loading-panel"),
  loadingOrigin: document.querySelector("#loading-origin"),
  error: document.querySelector("#error-panel"),
  errorTitle: document.querySelector("#error-title"),
  errorDetail: document.querySelector("#error-detail"),
  retry: document.querySelector("#retry"),
  connected: document.querySelector("#connected-underlay"),
};

let state = null;
let actionPending = false;

function showLocalError(error) {
  elements.inlineError.textContent = error?.message || "요청을 처리하지 못했습니다.";
  elements.inlineError.hidden = false;
}

function clearLocalError() {
  elements.inlineError.hidden = true;
  elements.inlineError.textContent = "";
}

function availabilityLabel(value) {
  if (value === "available") return "사용 가능";
  if (value === "unavailable") return "연결 안 됨";
  return "확인 중";
}

function createServerCard(server, kind) {
  const card = document.createElement("article");
  card.className = `server-card ${server.availability}`;

  const header = document.createElement("div");
  header.className = "server-card-header";
  const title = document.createElement("h3");
  title.textContent = server.name;
  const availability = document.createElement("span");
  availability.className = `server-status ${server.availability}`;
  availability.textContent = availabilityLabel(server.availability);
  header.append(title, availability);

  const address = document.createElement("p");
  address.className = "server-meta server-address";
  address.textContent = `${server.origin}\n버전 ${server.version}`;

  const message = document.createElement("p");
  message.className = "server-meta";
  message.textContent = server.message;

  const button = document.createElement("button");
  button.type = "button";
  button.className = server.connectable ? "button button-primary compact" : "button button-ghost compact";
  button.textContent = server.availability === "unavailable" && kind === "recent"
    ? "다시 확인 및 연결"
    : "이 서버 연결";
  button.disabled = actionPending || server.availability === "checking" || (kind === "discovered" && !server.connectable);
  button.addEventListener("click", () => runAction(() => api.connect(server.origin, server.id)));

  card.append(header, address, message, button);
  return card;
}

function renderServerList(container, servers, kind) {
  container.replaceChildren();
  if (!servers.length) {
    const empty = document.createElement("div");
    empty.className = "empty-state";
    empty.textContent = kind === "discovered"
      ? "확인된 서버가 없습니다. 서버와 이 PC가 같은 네트워크인지 확인해 주세요."
      : "최근에 연결한 서버가 없습니다.";
    container.append(empty);
    return;
  }
  for (const server of servers) container.append(createServerCard(server, kind));
}

function render(nextState) {
  state = nextState;
  const selecting = state.mode === "selection";
  elements.selectionScreen.hidden = !selecting;
  elements.workspaceScreen.hidden = selecting;
  elements.loading.hidden = state.mode !== "connecting";
  elements.error.hidden = state.mode !== "error";
  elements.connected.hidden = state.mode !== "connected";

  const current = state.current;
  elements.currentName.textContent = current?.name || "서버를 선택하세요";
  elements.currentOrigin.textContent = current?.origin || "로컬 네트워크의 서버를 찾고 있습니다";
  elements.changeServer.hidden = selecting;
  elements.connectionState.textContent = state.mode === "connected"
    ? "연결됨"
    : state.mode === "connecting"
      ? "연결 중"
      : state.mode === "error"
        ? "연결 오류"
        : "";

  elements.loadingOrigin.textContent = current?.origin || "";
  elements.errorTitle.textContent = state.error?.title || "서버에 연결할 수 없습니다";
  elements.errorDetail.textContent = state.error?.detail || "주소와 네트워크 상태를 확인해 주세요.";
  elements.retry.hidden = state.error?.retryable === false;
  elements.retry.disabled = actionPending;
  elements.refresh.disabled = actionPending || state.refreshing;
  elements.refresh.textContent = state.refreshing ? "확인 중…" : "새로 고침";

  renderServerList(elements.discovered, state.discovered || [], "discovered");
  renderServerList(elements.recents, state.recents || [], "recent");
}

async function runAction(action) {
  if (actionPending) return;
  actionPending = true;
  clearLocalError();
  if (state) render(state);
  try {
    const nextState = await action();
    if (nextState) render(nextState);
  } catch (error) {
    showLocalError(error);
  } finally {
    actionPending = false;
    if (state) render(state);
  }
}

elements.manualForm.addEventListener("submit", (event) => {
  event.preventDefault();
  const origin = elements.manualInput.value.trim();
  if (!origin) return;
  runAction(() => api.connect(origin));
});

elements.refresh.addEventListener("click", () => runAction(() => api.refresh()));
elements.changeServer.addEventListener("click", () => runAction(() => api.changeServer()));
elements.retry.addEventListener("click", () => runAction(() => api.retry()));
document.querySelector("#error-change-server").addEventListener("click", () => runAction(() => api.changeServer()));

api.onState((nextState) => render(nextState));
api.getState().then(render).catch(showLocalError);
