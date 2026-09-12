import { DEFAULT_LANGUAGE, normalizeLanguage, setLanguage as setCatalogLanguage, t } from "./lib/i18n.mjs";


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
  language: document.querySelector("#language"),
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
let language = DEFAULT_LANGUAGE;

function applyStaticTranslations() {
  document.documentElement.lang = language;
  document.querySelectorAll("[data-i18n]").forEach((element) => {
    element.textContent = t(element.dataset.i18n);
  });
  document.querySelectorAll("[data-i18n-placeholder]").forEach((element) => {
    element.placeholder = t(element.dataset.i18nPlaceholder);
  });
}

function showLocalError(error) {
  elements.inlineError.textContent = error?.message || t("shell.requestFailed");
  elements.inlineError.hidden = false;
}

function clearLocalError() {
  elements.inlineError.hidden = true;
  elements.inlineError.textContent = "";
}

function availabilityLabel(value) {
  if (value === "available") return t("shell.availability.available");
  if (value === "unavailable") return t("shell.availability.unavailable");
  return t("shell.availability.checking");
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
  address.textContent = `${server.origin}\n${t("shell.version", { version: server.version })}`;

  const message = document.createElement("p");
  message.className = "server-meta";
  message.textContent = server.message;

  const button = document.createElement("button");
  button.type = "button";
  button.className = server.connectable ? "button button-primary compact" : "button button-ghost compact";
  button.textContent = server.availability === "unavailable" && kind === "recent"
    ? t("shell.connectAgain")
    : t("shell.connectThis");
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
      ? t("shell.empty.discovered")
      : t("shell.empty.recent");
    container.append(empty);
    return;
  }
  for (const server of servers) container.append(createServerCard(server, kind));
}

function render(nextState) {
  state = nextState;
  const nextLanguage = normalizeLanguage(nextState.language) ?? DEFAULT_LANGUAGE;
  if (language !== nextLanguage) {
    language = nextLanguage;
    setCatalogLanguage(language);
  }
  elements.language.value = language;
  applyStaticTranslations();
  const selecting = state.mode === "selection";
  elements.selectionScreen.hidden = !selecting;
  elements.workspaceScreen.hidden = selecting;
  elements.loading.hidden = state.mode !== "connecting";
  elements.error.hidden = state.mode !== "error";
  elements.connected.hidden = state.mode !== "connected";

  const current = state.current;
  elements.currentName.textContent = current?.name || t("shell.current.none");
  elements.currentOrigin.textContent = current?.origin || t("shell.current.searching");
  elements.changeServer.hidden = selecting;
  elements.connectionState.textContent = state.mode === "connected"
    ? t("shell.state.connected")
    : state.mode === "connecting"
      ? t("shell.state.connecting")
      : state.mode === "error"
        ? t("shell.state.error")
        : "";

  elements.loadingOrigin.textContent = current?.origin || "";
  elements.errorTitle.textContent = state.error?.title || t("shell.error.title");
  elements.errorDetail.textContent = state.error?.detail || t("shell.error.detail");
  elements.retry.hidden = state.error?.retryable === false;
  elements.retry.disabled = actionPending;
  elements.refresh.disabled = actionPending || state.refreshing;
  elements.language.disabled = actionPending;
  elements.refresh.textContent = state.refreshing ? t("shell.refreshing") : t("shell.refresh");

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

elements.language.addEventListener("change", () => {
  const nextLanguage = normalizeLanguage(elements.language.value);
  if (!nextLanguage || nextLanguage === language) return;
  runAction(() => api.setLanguage(nextLanguage));
});

elements.refresh.addEventListener("click", () => runAction(() => api.refresh()));
elements.changeServer.addEventListener("click", () => runAction(() => api.changeServer()));
elements.retry.addEventListener("click", () => runAction(() => api.retry()));
document.querySelector("#error-change-server").addEventListener("click", () => runAction(() => api.changeServer()));

api.onState((nextState) => render(nextState));
api.getState().then(render).catch(showLocalError);
