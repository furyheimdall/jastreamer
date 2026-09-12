export const LANGUAGE_COOKIE_NAME = "jastreamer_language";
export const DEFAULT_LANGUAGE = "en";

const messages = {
  "shell.current.none": { en: "Select a server", ko: "서버를 선택하세요" },
  "shell.current.searching": { en: "Searching for servers on your local network", ko: "로컬 네트워크의 서버를 찾고 있습니다" },
  "shell.changeServer": { en: "Change server", ko: "서버 변경" },
  "shell.language": { en: "Language", ko: "언어" },
  "shell.language.english": { en: "English", ko: "영어" },
  "shell.language.korean": { en: "Korean", ko: "한국어" },
  "shell.intro.eyebrow": { en: "DESKTOP CONNECTION", ko: "데스크톱 연결" },
  "shell.intro.title": { en: "Select a server to play from", ko: "재생할 서버를 선택하세요" },
  "shell.intro.detail": {
    en: "You can connect only to verified JASTREAMER servers on your network. You must always choose a server yourself; the app never connects automatically.",
    ko: "같은 네트워크에서 확인된 JASTREAMER 서버만 연결할 수 있습니다. 서버 선택은 항상 직접 해야 하며 자동으로 연결하지 않습니다.",
  },
  "shell.manual.title": { en: "Connect by address", ko: "주소로 연결" },
  "shell.manual.detail": { en: "Enter a host name or an HTTP(S) root URL.", ko: "호스트 이름 또는 HTTP(S) 루트 주소를 입력하세요." },
  "shell.manual.label": { en: "Server address", ko: "서버 주소" },
  "shell.manual.placeholder": { en: "Example: music-server.local:8080 or https://music.example.local", ko: "예: music-server.local:8080 또는 https://music.example.local" },
  "shell.manual.connect": { en: "Verify and connect", ko: "확인 후 연결" },
  "shell.manual.httpNote": {
    en: "Use HTTP only on a trusted private LAN. HTTPS certificate checks cannot be disabled.",
    ko: "HTTP 연결은 신뢰할 수 있는 사설 LAN에서만 사용하세요. HTTPS 인증서 검사는 끌 수 없습니다.",
  },
  "shell.discovered.eyebrow": { en: "LOCAL NETWORK", ko: "로컬 네트워크" },
  "shell.discovered.title": { en: "Discovered servers", ko: "발견된 서버" },
  "shell.refresh": { en: "Refresh", ko: "새로 고침" },
  "shell.refreshing": { en: "Checking…", ko: "확인 중…" },
  "shell.recent.eyebrow": { en: "RECENT", ko: "최근" },
  "shell.recent.title": { en: "Recent servers", ko: "최근 서버" },
  "shell.connecting.eyebrow": { en: "CONNECTING", ko: "연결 중" },
  "shell.connecting.title": { en: "Verifying the server", ko: "서버를 확인하고 있습니다" },
  "shell.error.eyebrow": { en: "CONNECTION ERROR", ko: "연결 오류" },
  "shell.error.title": { en: "Cannot connect to the server", ko: "서버에 연결할 수 없습니다" },
  "shell.error.detail": { en: "Check the address and network status.", ko: "주소와 네트워크 상태를 확인해 주세요." },
  "shell.retry": { en: "Try again", ko: "다시 시도" },
  "shell.otherServer": { en: "Choose another server", ko: "다른 서버 선택" },
  "shell.connectedUnderlay": { en: "Displaying the server interface.", ko: "서버 화면을 표시하고 있습니다." },
  "shell.requestFailed": { en: "The request could not be completed.", ko: "요청을 처리하지 못했습니다." },
  "shell.availability.available": { en: "Available", ko: "사용 가능" },
  "shell.availability.unavailable": { en: "Unavailable", ko: "연결 안 됨" },
  "shell.availability.checking": { en: "Checking", ko: "확인 중" },
  "shell.version": { en: "Version {version}", ko: "버전 {version}" },
  "shell.connectAgain": { en: "Verify and reconnect", ko: "다시 확인 및 연결" },
  "shell.connectThis": { en: "Connect to this server", ko: "이 서버 연결" },
  "shell.empty.discovered": { en: "No verified servers found. Make sure the server and this PC are on the same network.", ko: "확인된 서버가 없습니다. 서버와 이 PC가 같은 네트워크인지 확인해 주세요." },
  "shell.empty.recent": { en: "No recently connected servers.", ko: "최근에 연결한 서버가 없습니다." },
  "shell.state.connected": { en: "Connected", ko: "연결됨" },
  "shell.state.connecting": { en: "Connecting", ko: "연결 중" },
  "shell.state.error": { en: "Connection error", ko: "연결 오류" },

  "main.start.title": { en: "JASTREAMER failed to start", ko: "JASTREAMER 시작 실패" },
  "main.start.portable": { en: "The portable data folder could not be created.\n\n{path}\n\nMove the app to a writable folder and try again.", ko: "휴대용 데이터 폴더를 만들 수 없습니다.\n\n{path}\n\n쓰기 가능한 폴더로 앱을 이동한 뒤 다시 실행해 주세요." },
  "main.shell.title": { en: "JASTREAMER interface error", ko: "JASTREAMER 화면 오류" },
  "main.shell.openFailed": { en: "The server selection screen could not be opened.\n\n{message}", ko: "서버 선택 화면을 열 수 없습니다.\n\n{message}" },
  "main.data.title": { en: "JASTREAMER data folder error", ko: "JASTREAMER 데이터 폴더 오류" },
  "main.data.unavailable": { en: "The portable data folder cannot be read or written.\n\n{path}\n\nCheck the folder permissions or move the app to a writable location.", ko: "휴대용 데이터 폴더를 읽거나 쓸 수 없습니다.\n\n{path}\n\n폴더 권한을 확인하거나 앱을 쓰기 가능한 위치로 이동해 주세요." },
  "main.remote.cert": { en: "The HTTPS certificate could not be verified. Check its validity period and server name.", ko: "HTTPS 인증서를 확인할 수 없습니다. 인증서 유효 기간과 서버 이름을 확인해 주세요." },
  "main.remote.name": { en: "The server name could not be resolved. Check the address and network connection.", ko: "서버 이름을 찾을 수 없습니다. 주소와 네트워크 연결을 확인해 주세요." },
  "main.remote.load": { en: "The server interface could not be loaded. Check the server and network, then try again.", ko: "서버 화면을 불러올 수 없습니다. 서버와 네트워크 상태를 확인한 뒤 다시 시도해 주세요." },
  "main.remote.disconnected": { en: "The server connection was lost", ko: "서버 연결이 끊어졌습니다" },
  "main.remote.processGone": { en: "The server interface process ended. Connect again.", ko: "서버 화면 프로세스가 종료되었습니다. 다시 연결해 주세요." },
  "main.remote.unresponsive": { en: "The server interface is not responding. Check the network and try again.", ko: "서버 화면이 응답하지 않습니다. 네트워크 상태를 확인한 뒤 다시 시도해 주세요." },
  "main.remote.timeout": { en: "Loading the server interface timed out. Check the network and try again.", ko: "서버 화면을 불러오는 시간이 초과되었습니다. 네트워크 상태를 확인해 주세요." },
  "main.selection.stale": { en: "This entry is no longer in the server list. Refresh and try again.", ko: "현재 서버 목록에서 이 항목을 찾을 수 없습니다. 새로 고침한 뒤 다시 시도해 주세요." },
  "main.server.verifying": { en: "Verifying server", ko: "서버 확인 중" },
  "main.recents.saveFailed": { en: "Recent server information could not be saved to the portable data folder.", ko: "최근 서버 정보를 휴대용 데이터 폴더에 저장할 수 없습니다." },
  "main.ipc.denied": { en: "This IPC request is not allowed.", ko: "허용되지 않은 IPC 요청입니다." },
  "main.retry.none": { en: "There is no server to reconnect to.", ko: "다시 연결할 서버가 없습니다." },
  "main.language.saveFailed": { en: "The language preference could not be saved.", ko: "언어 설정을 저장할 수 없습니다." },

  "status.checking": { en: "Checking the server.", ko: "서버를 확인하는 중입니다." },
  "status.rechecking": { en: "Checking the server again.", ko: "서버를 다시 확인하는 중입니다." },
  "status.available": { en: "Ready to connect.", ko: "연결할 수 있습니다." },

  "main.language.invalid": { en: "Choose English or Korean.", ko: "영어 또는 한국어를 선택해 주세요." },
  "probe.responseTooLarge": { en: "The server response exceeds the allowed size.", ko: "서버 응답이 허용 크기를 초과했습니다." },
  "probe.timeout": { en: "The server response timed out.", ko: "서버 응답 시간이 초과되었습니다." },
  "probe.cancelled": { en: "Server verification was cancelled.", ko: "서버 확인이 취소되었습니다." },
  "probe.tls": { en: "The HTTPS certificate could not be verified. Check the certificate and server name.", ko: "HTTPS 인증서를 확인할 수 없습니다. 인증서와 서버 이름을 확인해 주세요." },
  "probe.unreachable": { en: "The server could not be reached.", ko: "서버에 연결할 수 없습니다." },
  "probe.invalidObject": { en: "The server verification response has an invalid format.", ko: "서버 확인 응답 형식이 올바르지 않습니다." },
  "probe.incompatible": { en: "This is not a compatible JASTREAMER server.", ko: "호환되는 JASTREAMER 서버가 아닙니다." },
  "main.language.cookieFailed": { en: "The language preference could not be applied to the server session.", ko: "서버 세션에 언어 설정을 적용할 수 없습니다." },
  "probe.invalidId": { en: "The server ID is invalid.", ko: "서버 ID가 올바르지 않습니다." },
  "probe.invalidInfo": { en: "The server name or version is invalid.", ko: "서버 이름 또는 버전 정보가 올바르지 않습니다." },
  "probe.redirect": { en: "The server verification request was redirected to another address.", ko: "서버 확인 요청이 다른 주소로 이동되었습니다." },
  "probe.httpStatus": { en: "The server verification request failed with HTTP {status}.", ko: "서버 확인 요청이 HTTP {status}로 실패했습니다." },
  "probe.notJson": { en: "The server verification response is not JSON.", ko: "서버 확인 응답이 JSON이 아닙니다." },
  "probe.invalidJson": { en: "The server verification response contains invalid JSON.", ko: "서버 확인 응답의 JSON이 올바르지 않습니다." },
  "probe.identityMismatch": { en: "The server ID at this address differs from the saved server. Credentials are not shared.", ko: "저장된 서버와 현재 주소의 서버 ID가 다릅니다. 자격 증명을 공유하지 않습니다." },
  "probe.unknown": { en: "An unknown error occurred while verifying the server.", ko: "서버를 확인하는 중 알 수 없는 오류가 발생했습니다." },

  "security.invalidServerId": { en: "The server ID is not a valid UUID.", ko: "서버 ID가 올바른 UUID가 아닙니다." },
  "security.addressRequired": { en: "Enter a server address.", ko: "서버 주소를 입력해 주세요." },
  "security.addressCharacters": { en: "The address cannot contain spaces or control characters.", ko: "주소에 공백이나 제어 문자를 포함할 수 없습니다." },
  "security.schemeRequired": { en: "The address must begin with http:// or https://.", ko: "주소는 http:// 또는 https://로 시작해야 합니다." },
  "security.invalidAddress": { en: "Enter a valid server address.", ko: "올바른 서버 주소가 아닙니다." },
  "security.httpOnly": { en: "Only HTTP or HTTPS addresses are allowed.", ko: "HTTP 또는 HTTPS 주소만 사용할 수 있습니다." },
  "security.missingHost": { en: "The server host name is missing.", ko: "서버 호스트 이름이 없습니다." },
  "security.credentials": { en: "Addresses containing user information are not allowed.", ko: "사용자 정보가 포함된 주소는 사용할 수 없습니다." },
  "security.queryFragment": { en: "The address cannot contain a query or fragment.", ko: "주소에 쿼리나 조각을 포함할 수 없습니다." },
  "security.rootOnly": { en: "Enter only the server root address; paths are not allowed.", ko: "서버의 루트 주소만 입력해 주세요. 경로는 사용할 수 없습니다." },

  "storage.recentsCorrupt": { en: "The recent servers file is corrupted.", ko: "최근 서버 목록 파일이 손상되었습니다." },
  "storage.recentsInvalid": { en: "The recent servers file format could not be read.", ko: "최근 서버 목록 파일 형식을 읽을 수 없습니다." },
  "storage.serverInvalid": { en: "The server information to save is invalid.", ko: "저장할 서버 정보가 올바르지 않습니다." },
};

let currentLanguage = DEFAULT_LANGUAGE;

export function normalizeLanguage(value) {
  return value === "en" || value === "ko" ? value : null;
}

export function getLanguage() {
  return currentLanguage;
}

export function setLanguage(value) {
  const language = normalizeLanguage(value);
  if (!language) return false;
  currentLanguage = language;
  return true;
}

export function t(key, params = {}) {
  const template = messages[key]?.[currentLanguage] ?? messages[key]?.[DEFAULT_LANGUAGE] ?? key;
  return template.replace(/\{([A-Za-z][A-Za-z0-9]*)\}/g, (match, name) =>
    Object.prototype.hasOwnProperty.call(params, name) ? String(params[name]) : match,
  );
}
