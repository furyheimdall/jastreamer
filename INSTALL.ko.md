# jastreamer 설치 및 업데이트 안내

[프로젝트 소개](README.ko.md) · [사용자 안내서](INSTRUCTION.ko.md) · [English installation guide](INSTALL.md)

jastreamer는 Server 하나와 선택 사항인 클라이언트들로 이루어집니다. 보관함, 공용 대기열, 재생과 Web 화면은 Server가 소유하고, 데스크톱 앱·Android 앱·iOS 앱·휴대전화 PWA는 신뢰할 수 있는 사설 LAN에서 그 Server를 바라보는 창입니다. Server를 먼저 설치한 뒤 필요한 클라이언트만 추가하세요.

릴리즈는 [GitHub Releases](https://github.com/furyheimdall/jastreamer/releases)에 게시됩니다. 정식 릴리즈는 `vX.Y.Z` 태그로 GitHub의 **latest** 릴리즈로 표시되며 설치 대상은 이 릴리즈입니다. 이전의 `v0.2.0-preview.N` 항목은 프리릴리즈로 남고 `/releases/latest`에 포함되지 않습니다. Windows Server ZIP과 Windows 데스크톱 ZIP에는 Authenticode 서명이 없고, Android APK는 jastreamer Android release 키로 서명하며, iOS는 소스와 CI만 제공합니다. 내려받은 파일은 모두 검증한 뒤 사용하고, 실제 네트워크와 수신기에서 동작을 확인하세요.

| 하려는 작업 | 따라갈 분기 |
| --- | --- |
| Linux 또는 Synology에서 Server 실행 | [Linux Server](#linux-server) |
| Windows에서 Server 실행 | [Windows Server](#windows-server) |
| 기존 Server에 접속할 데스크톱 앱 추가 | [Windows ZIP](#desktop-windows) 또는 [Linux DEB](#desktop-linux) |
| 네이티브 Android 클라이언트 추가 | [Android APK](#android) |
| 네이티브 iOS 클라이언트 개발·검증 | 기기 설치가 아닌 [iOS 소스와 CI](#ios) |
| 휴대전화 사용 또는 홈 화면 앱 설치 | [휴대전화 PWA](#pwa) |
| 설치가 실제로 동작하는지 확인 | [검증 체크리스트](#verify) |
| 기존 설치 업데이트 또는 복구 | [업데이트](#upgrade) 또는 [롤백](#rollback) |
| 설정·데이터·로그 위치 확인 | [파일 위치](#locations) |
| 클라이언트나 Server 제거 | [제거](#uninstall) |

에이전트에게 설치나 업데이트를 맡기려면 [AGENTS.md](AGENTS.md)부터 읽으세요. 점검·승인·데이터 보호·검증·보고 규칙이 아래의 모든 단계보다 먼저 적용됩니다.

<a id="requirements"></a>
## 요구 사항과 안전 수칙

| 구성 요소 | 요구 사항 | 패키지 |
| --- | --- | --- |
| Linux·Synology Server | Docker Engine과 Compose v2가 있는 Linux `amd64`·`arm64`, 또는 Container Manager가 있는 Synology DSM. `arm/v7`은 지원하지 않으며 DS918+는 `amd64` | `ghcr.io/furyheimdall/jastreamer-server` 이미지 |
| Windows Server | Windows x64와 쓰기 가능한 로컬 폴더, 일반 사용자 계정으로 실행. Windows 서비스로 설치되지 않음 | `jastreamer-server_0.2.0_windows-x64.zip` |
| Windows 데스크톱 앱 | Windows 10/11 x64. ARM64 패키지는 없음 | `jastreamer-desktop_0.2.0_windows-x64.zip` |
| Linux 데스크톱 앱 | 그래픽 환경이 있는 Linux `amd64`. Ubuntu 24.04 amd64가 검증 대상이며 ARM64 패키지는 없음 | `jastreamer-desktop_0.2.0_linux-amd64.deb` |
| Android 클라이언트 | Android 10(API 29) 이상과 `MULTI_PROFILE`을 지원하는 Android System WebView | release 키로 서명한 `jastreamer-android_0.2.0_release.apk` |
| iOS 클라이언트 | iOS/iPadOS 18.4 이상. 고정된 CI 시나리오는 macOS의 Xcode 16.4와 iOS 18.5 시뮬레이터 런타임 사용 | 소스와 CI만 제공 |
| 휴대전화 PWA | 휴대전화 브라우저와 그 기기가 신뢰하는 HTTPS origin | 별도 패키지 없음, Server가 제공 |

Server 프로필, Server가 제어하는 휴대전화 출력, 새 가져오기에는 WebView의 `MULTI_PROFILE` 기능이 필요합니다. OS 버전만으로는 지원이 보장되지 않으며 앱은 공용 세션으로 대신 연결하지 않습니다. Android의 **저장된 음악** 자체는 Server 프로필이 없어도 됩니다.

저장 공간:

- **Linux:** 컨테이너 UID/GID `10001:10001`이 쓸 수 있는 분리된 config·data 폴더와, UID 10001이 읽고 탐색할 수 있으며 읽기 전용으로 연결하는 음악 루트.
- **Windows:** 실행 파일 옆의 `server.json`과 `data`를 Server 실행 계정이 쓸 수 있어야 하고, 음악 루트는 그 계정이 읽을 수 있어야 합니다.

Server의 config, data, music은 서로 다른 폴더에 두고, 프로젝트나 데이터를 음악 루트 안에 넣지 마세요.

### 포트와 네트워크

| 용도 | 포트 | 참고 |
| --- | --- | --- |
| Web 화면과 음원 전송, Linux 이미지 기본값 | TCP 8080 | `server.json`의 `http.address` |
| Web 화면과 음원 전송, Windows 무설치 기본값 | TCP 18080 | 패키지의 `server.template.json` |
| 내장 HTTPS 리스너(사용 설정 시) | TCP 8443, Windows는 18443 | `https.address`에 직접 준비한 PEM 인증서·키 사용 |
| UPnP/DLNA 검색과 제어 | UDP 1900, `239.255.255.250` | SSDP. 멀티캐스트가 허용되어야 합니다 |
| 클라이언트 검색(`_jastreamer._tcp`)과 Google Cast 검색 | UDP 5353 | 선택한 인터페이스의 mDNS |
| Google Cast 제어 | 각 수신기가 광고하는 TCP 포트 | 수신기가 Server의 음원 주소에도 접근할 수 있어야 합니다 |

AirPlay는 Linux 이미지에 포함된 helper로만 제공됩니다. 위 포트를 인터넷으로 포워딩하지 마세요.

### 모든 플랫폼에 적용되는 안전 수칙

- 요청한 음악 루트가 없을 때 임의로 만들거나, `chmod 777`을 쓰거나, 음악 보관함의 소유권·권한을 재귀적으로 바꾸지 마세요. 플랫폼이 허용하는 범위에서 음악은 읽기 전용으로 연결·설정합니다.
- HTTP는 자격 증명도 음원도 암호화하지 않습니다. LAN 경로 전체를 신뢰할 수 없다면 직접 준비한 인증서·키로 Server의 내장 HTTPS 리스너를 사용하세요. 인증서 경고를 무시하거나 Server를 공용 인터넷에 직접 노출하지 마세요.
- 비밀번호, 개인 키, 레지스트리 토큰, 세션 cookie를 대화창·Compose 파일·소스 저장소·지원 로그에 남기지 마세요. 기존 자격 증명 저장소나 비공개 대화형 입력을 사용합니다.
- 모든 업데이트와 롤백은 기존 `server.json`, data 폴더, 음악을 그대로 보존합니다. 패키지가 소유한 파일이나 컨테이너 이미지만 교체하세요.
- 설치를 성공시키려고 Defender, SmartScreen, 방화벽, Play Protect, AppArmor, Chromium sandbox를 끄지 마세요. 대신 오류와 실제 권한 상태를 보고합니다.
- 컨테이너가 healthy하거나 CI가 통과했거나 클라이언트가 연결되었다는 사실은 소리가 났다는 증거가 아닙니다. 실제 곡을 실제 출력으로 재생해 확인하세요.

<a id="releases"></a>
## 릴리즈 확보와 검증

### 릴리즈 선택

가장 최근의 정식 릴리즈를 우선 사용하세요. [GitHub Releases 목록](https://github.com/furyheimdall/jastreamer/releases)에서 Preview 표시가 없는 `vX.Y.Z` 항목이며 `/releases/latest`도 이 릴리즈를 가리킵니다. `vX.Y.Z-preview.N` 프리릴리즈는 그 프리뷰를 일부러 시험할 때만 선택하고, 프리뷰는 결코 latest로 표시되지 않는다는 점을 기억하세요. 어느 쪽을 고르든 호스트 아키텍처와 필요한 패키지를 지원하는지 확인하고, 최신 항목이 호환되지 않으면 이유를 밝힌 뒤 가장 최근의 호환 항목을 사용합니다.

릴리즈 provenance, source revision, 패키지·이미지 manifest, SHA-256, 아키텍처가 서로 맞는지 함께 검증하고, 이미지 참조와 Windows ZIP, 데스크톱 패키지, Android APK, 체크섬, manifest를 모두 같은 릴리즈에서 받으세요. `SHA256SUMS`는 모든 자산을 포함하고, `release-provenance.json`에는 릴리즈 태그·채널·소스 리비전·CI 실행과 Android 서명 인증서가 기록됩니다. 선택 사항인 샘플 설정은 선택한 릴리즈가 `samples/manifest.json`, MP3 세 개, seeding helper를 실제로 목록에 포함할 때만 적용됩니다. 게시되지 않은 저장소 파일을 이전 릴리즈의 기능이라고 설명해서는 안 됩니다.

### 공개 레지스트리 이미지

레지스트리는 `ghcr.io/furyheimdall/jastreamer-server`이며 공개 이미지는 GitHub 토큰 없이 받을 수 있습니다. 정식 릴리즈는 멀티 아키텍처 index를 `0.2.0` 태그로 게시하고 아키텍처별 다이제스트도 함께 제공하며, 가변 `latest` 이미지 태그는 만들지 않습니다. 태그 대신 릴리즈가 명시한 `linux/amd64` 또는 `linux/arm64` 이미지의 완전한 불변 다이제스트를 고정하세요.

```sh
export JASTREAMER_SERVER_IMAGE='ghcr.io/furyheimdall/jastreamer-server@sha256:<digest-from-release>'
docker pull "$JASTREAMER_SERVER_IMAGE"
docker image inspect --format '{{.Os}}/{{.Architecture}} {{.Id}}' "$JASTREAMER_SERVER_IMAGE"
```

검증한 다이제스트는 임시 shell 변수로만 두지 말고 배포의 영구 환경 파일에 저장하세요. Linux 이미지에는 아키텍처에 맞는 미디어 실행 환경이 이미 들어 있으므로 호스트에 FFmpeg나 Python을 따로 설치하지 않습니다.

<a id="windows-unblock"></a>
### Windows 다운로드: SmartScreen과 Mark-of-the-Web

ZIP에 Authenticode 서명이 없기 때문에 Windows는 인터넷에서 받은 파일로 표시하고, 압축을 푼 프로그램을 처음 실행할 때 SmartScreen 경고가 뜰 수 있습니다. 표시가 모든 파일로 복사되지 않도록 압축을 풀기 **전에** 차단을 해제하세요.

- 탐색기에서: ZIP 파일 우클릭 → **속성** → **차단 해제** 체크 → **확인**
- PowerShell에서: `Unblock-File -LiteralPath .\jastreamer-server_0.2.0_windows-x64.zip`

차단 해제는 릴리즈의 SHA-256과 대조해 검증한 파일에만 적용하세요. SmartScreen·Defender·방화벽을 끄거나 패키지를 관리자 권한으로 실행하지 마세요.

### 별도로 전달받은 오프라인 패키지

오프라인 Server 묶음을 별도로 받았다면 가져오기 전에 함께 제공된 체크섬을 검증합니다.

```sh
cd /path/to/supplied/release
sha256sum -c SHA256SUMS
```

명시적으로 승인된 비공개 레지스트리도 대안이 됩니다. 정확한 다이제스트와 비공개 인증 수단을 사용하세요.

멀티 플랫폼 `.oci` 파일은 `docker load`에 바로 넘길 수 없습니다. 오프라인 Docker TAR이 필요하면 Linux 장비에서 Skopeo로 대상 아키텍처만 변환하고, 전송 후 체크섬을 다시 비교한 뒤 출력된 로컬 이미지 ID를 `JASTREAMER_SERVER_IMAGE`로 사용하세요.

```sh
ARCH=amd64  # 또는 arm64
skopeo copy --override-os linux --override-arch "$ARCH" \
  oci-archive:jastreamer-server_0.2.0_linux_amd64-arm64.oci \
  docker-archive:jastreamer-server_0.2.0_linux_${ARCH}.tar:jastreamer-server:0.2.0
sha256sum jastreamer-server_0.2.0_linux_${ARCH}.tar
docker load --input jastreamer-server_0.2.0_linux_${ARCH}.tar
docker image inspect --format '{{.Id}}' jastreamer-server:0.2.0
```

<a id="linux-server"></a>
## Linux 또는 Synology Server

`amd64`·`arm64` 이미지에는 Server, 내장 Web 화면, Python 3.12와 고정된 pyatv 0.18.0, FFmpeg, `/usr/local/bin/jastreamer-airplay`가 들어 있습니다. 온보딩 샘플을 제공하는 릴리즈에는 `/usr/share/jastreamer/samples/`와 `/usr/share/jastreamer/seed-samples.py`도 포함됩니다.

Linux와 Synology 모두 릴리즈에 대응하는 소스 리비전의 `deploy/docker/server/compose.synology.yaml`을 사용하세요. 이 파일은 host 네트워킹, 읽기 전용 컨테이너 파일시스템, 64 MiB `/tmp`, 모든 capability 제거, `no-new-privileges`, UID/GID `10001:10001`, 분리된 쓰기 가능 config·data 마운트, 읽기 전용 `/music` 마운트를 적용합니다.

### 1단계. 음악 루트 결정

빈 답변이나 “건너뛰기”는 임의 경로 사용을 허락한 것이 아닙니다. 에이전트가 돕는 경우 파일을 쓰기 전에 이 결정을 먼저 받아야 합니다.

1. **기존 음악 루트 사용:** 존재하는 절대 경로를 사용합니다. 오타를 통과시키려고 `mkdir`을 실행하지 말고 `test -d "$JASTREAMER_MUSIC_PATH"`로 확인한 뒤, 그 아래 `jastreamer-samples` 자식 폴더만 새로 만들거나 재사용합니다.
2. **아직 음악 폴더가 없는 경우:** 해석된 절대 경로를 보여 주며 “`<사용자 홈>/music`을 음악 폴더로 만들어 사용해도 될까요?”라고 명시적으로 묻습니다. 동의를 받은 뒤에만 폴더를 만들고, 이미 있으면 내용을 확인한 뒤 재사용을 확정합니다. 동의가 없으면 `/data/music` 같은 다른 경로로 대체하지 말고 음악 설정을 중단합니다.

### 2단계. 프로젝트 폴더 준비

일반 Linux의 기본 프로젝트 위치는 대상 사용자의 `~/.config/jstreamer`이며 그 아래 `config`와 `data`를 분리합니다. 홈 경로는 대상 호스트에서 확인하고 sudo나 `/srv` 쓰기 권한을 가정하지 마세요. Synology에서는 승인된 영구 공유 경로, 보통 `/volume1/docker/jastreamer` 아래 `config`와 `data`를 유지합니다. 기존 설치를 별도 이전 승인 없이 옮기지 마세요.

```sh
JASTREAMER_PROJECT_PATH="$HOME/.config/jstreamer"
JASTREAMER_CONFIG_PATH="$JASTREAMER_PROJECT_PATH/config"
JASTREAMER_DATA_PATH="$JASTREAMER_PROJECT_PATH/data"
JASTREAMER_MUSIC_PATH="$HOME/music"   # 또는 확인한 기존 음악 루트
mkdir -p -- "$JASTREAMER_PROJECT_PATH" "$JASTREAMER_CONFIG_PATH" "$JASTREAMER_DATA_PATH"
# 이 정확한 경로의 생성에 동의했고 아직 존재하지 않을 때만 실행:
mkdir -- "$JASTREAMER_MUSIC_PATH"
```

홈에 두면 시스템 폴더에 쓰지 않아도 되지만, 그것만으로 UID/GID `10001:10001`의 접근 권한이 생기지는 않습니다. 실제 Docker UID 매핑, config·data 쓰기 권한, 음악 읽기·탐색 권한을 확인하세요. 권한이 부족하면 꼭 필요한 폴더에만 한정된 권한·ACL 변경을 승인받고, sudo를 가정하거나 컨테이너 사용자를 임의로 바꾸거나 홈·음악의 소유권을 재귀적으로 바꾸지 마세요. Synology에서는 공유 폴더의 소유자를 유지합니다.

### 3단계. 선택 사항인 샘플 곡 복사

이미 음악이 있다면 건너뛰세요. helper는 부모 폴더가 존재하는 절대 경로를 요구하고, `jastreamer-samples`만 0755로 만들며, payload 파일은 0644로 씁니다. manifest와 자산 해시를 검증해 CC0 1.0 Universal MP3 세 개와 `manifest.json`, `THIRD-PARTY-NOTICES.txt`를 복사하고, 바이트가 같은 기존 파일은 그대로 두며, 대상 심볼릭 링크는 거부하고, 모든 항목을 미리 점검해 충돌이 있으면 아무것도 건드리지 않고 중단합니다. manifest와 고지 파일에는 곡 제목·제작자·원본 주소·재배포 조건·해시가 들어 있으니 곡과 함께 보관하세요.

승인된 음악 소유 계정으로, 같은 고정 이미지를 권한 없는 일회성 컨테이너로 실행합니다.

```sh
docker run --rm --network none --read-only \
  --cap-drop ALL --security-opt no-new-privileges \
  --user "$(id -u):$(id -g)" \
  --mount "type=bind,source=$JASTREAMER_MUSIC_PATH,target=/seed-parent" \
  --entrypoint python3 "$JASTREAMER_SERVER_IMAGE" \
  /usr/share/jastreamer/seed-samples.py /seed-parent/jastreamer-samples
```

복사는 스캔·대기열 추가·출력 선택·재생을 하지 않습니다. 충돌이 보고되면 대상 파일을 지우거나 덮어쓰지 말고 내용을 확인하세요. 쓰기가 중단되면 새로 만든 파일이 일부만 남을 수 있는데, 무작정 재시도하지 말고 그 상태를 보고합니다.

### 4단계. 프로젝트 파일 저장

`compose.synology.yaml`을 영구 프로젝트 폴더에 `compose.yaml`이라는 이름으로 복사하고, 릴리즈에 대응하는 소스 리비전의 `packaging/server/server.json`을 비어 있는 config 폴더에 복사한 뒤, `JASTREAMER_SERVER_IMAGE`, `JASTREAMER_CONFIG_PATH`, `JASTREAMER_DATA_PATH`, `JASTREAMER_MUSIC_PATH`의 실제 값을 담은 프로젝트 `.env`를 작성합니다. 자리 표시자나 일시적인 `export`에 의존하지 말고 실제 값을 저장하며, 기존 `server.json`은 절대 교체하지 않습니다.

저장한 `server.json`을 검토하세요. `data_dir`은 `/var/lib/jastreamer`, 보관함 루트는 `/music`, 패키지 경로는 `/usr/local/bin/ffmpeg`와 `/usr/local/bin/jastreamer-airplay`로 유지합니다. `server_name`, HTTPS, 선택 출력은 승인된 범위에서만 설정합니다. `media.base_url`은 재생 기기가 음원을 받아 갈 Server 주소이지 Web 화면 주소가 아니므로, 특정 수신기가 고정된 HTTP(S) origin을 요구하지 않는 한 비워 두어 자동 선택에 맡깁니다. Google Cast를 쓰지 않는다면 `cast.enabled`는 false로 둡니다. Web **설정** 화면은 `server.json`을 원자적으로 다시 쓰므로 파일과 폴더 모두 UID 10001이 쓸 수 있어야 합니다.

### 5단계. 시작과 확인

```sh
docker compose -f compose.yaml config
docker compose -f compose.yaml up -d
docker compose -f compose.yaml ps
docker compose -f compose.yaml logs --tail 100 jastreamer-server
curl --fail http://127.0.0.1:8080/healthz
```

Synology Container Manager에서는 같은 Compose 파일과 `.env` 값을 **프로젝트**로 가져옵니다. 이때 상태 확인 주소는 `http://<NAS-LAN-IP>:8080/healthz`이고, 내장 HTTPS를 켰다면 8443 포트입니다.

음악 폴더가 비어 있으면 재생할 곡도 없습니다. 승인된 루트에 음악을 넣고 **설정 → 보관함 → 지금 스캔**을 실행하세요. 나중에 추가한 음악도 다시 스캔해야 합니다. 샘플을 복사했다면 개인 음악과 구분해 안내하고, 보관함이 비었으면 재생 준비가 끝났다고 말하지 말고 사실대로 보고하세요. 이어서 [검증 체크리스트](#verify)와 [최초 설정](INSTRUCTION.ko.md#1-first-setup-and-everyday-use)으로 진행합니다.

<a id="windows-server"></a>
## 네이티브 Windows x64 무설치 Server

선택한 릴리즈에서 Windows Server ZIP과 `.sha256`, manifest, 검증 영수증을 내려받고 [ZIP 차단을 해제](#windows-unblock)한 뒤, 압축을 풀기 전에 바이트를 대조합니다.

```powershell
$file = '.\jastreamer-server_0.2.0_windows-x64.zip'
$expected = (Get-Content "$file.sha256" -Raw).Split()[0].ToLowerInvariant()
$actual = (Get-FileHash $file -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actual -ne $expected) { throw 'Windows Server ZIP checksum mismatch' }
```

`jastreamer-server-windows-x64` 폴더 전체를 쓰기 가능한 새 로컬 위치에 풀고, Server를 운영할 일반 계정으로 `start-server.cmd`를 실행하세요. ZIP 안에서 실행하거나 EXE만 복사하거나 관리자 권한으로 실행하지 마세요. 런처는 설정을 검증한 뒤 콘솔 창에서 Server를 시작하며 시스템이나 정책을 영구적으로 바꾸지 않습니다. 사용하는 동안 창을 열어 두고 Ctrl+C로 정지합니다.

`server.json`이 없는 첫 실행에서는 옆에 `data`와 `music\jastreamer-samples`를 만들고, 패키지 샘플을 `samples\manifest.json`과 대조해 충돌하지 않는 파일만 복사하고, 절대 경로를 기록한 뒤 시작합니다. 기존 `server.json`이나 기존 음악 파일은 절대 교체하지 않습니다. 샘플 자산을 목록에 포함하지 않은 패키지를 샘플이 있는 것처럼 설명해서는 안 됩니다.

에이전트가 돕는다면 첫 실행 전에 **기존 절대 음악 루트**와 **건너뛰기** 중 하나를 정해야 합니다. 건너뛰기는 의도적으로 설치 폴더 옆의 샘플 전용 보관함을 사용한다는 뜻입니다. 기존 루트를 쓸 때는 그 루트를 확인하고, `jastreamer-samples` 자식 폴더만 만들고, 검증한 샘플 중 충돌하지 않는 파일만 복사하고, 그 루트를 사용하는 실제 `server.json`을 옆에 저장합니다. 없는 루트를 새로 만들거나 사용자에게 JSON을 직접 고치라고 요구하지 마세요.

같은 PC에서는 `http://127.0.0.1:18080`, 다른 클라이언트에서는 해당 PC의 사설 LAN 주소와 18080 포트를 열어 첫 관리자 계정을 만듭니다. Windows 방화벽이 물으면 사설 네트워크에서만 허용하세요. UPnP/DLNA와 Google Cast에 필요한 포트는 [포트와 네트워크](#requirements)를 참고합니다.

이 패키지는 [Windows 데스크톱 앱](#desktop-windows)이 아니라 Server입니다. UPnP/DLNA와 선택 사항인 Google Cast 출력을 제공하지만 PC 스피커를 직접 열지는 않으며, 접속한 브라우저에서 **이 기기**를 선택할 수는 있습니다. Google Cast에는 Chrome이나 Python helper가 필요 없고 **설정**에서 켜고 저장한 뒤 Server를 재시작하면 적용됩니다. 패키지에는 FFmpeg도 AirPlay helper도 없으므로 변환과 AirPlay는 비활성 상태로 두며, 임의의 실행 파일 경로를 입력해도 Windows에서 AirPlay가 생기지 않습니다. 별도의 Linux 송신 구성은 설치한 Server와 같은 릴리즈의 어댑터 소스·의존성을 사용해 동일한 통신 규약을 구현해야 하며 `atvremote`, Python, pyatv 단독, Shairport Sync 같은 수신 소프트웨어는 대체재가 아닙니다.

<a id="windows-server-update"></a>
### 무설치 Windows Server 업데이트

1. 먼저 실제 런처와 `server.json`을 살펴 `data_dir`과 모든 보관함 루트를 확인합니다. 외장 드라이브, UNC 공유, junction·심볼릭 링크 대상도 포함합니다. 옆의 `data`와 `music` 폴더라고 단정하지 말고 실제 경로와 권한을 기록하며, 서로 어긋나거나 읽을 수 없으면 중단하세요. 저장 위치 이전은 별도 승인이 필요합니다.
2. 새 ZIP을 검증·차단 해제한 뒤 별도의 임시 폴더에 풀고, 릴리즈의 설정·데이터베이스 호환성을 확인합니다.
3. 실행 중인 Server를 Ctrl+C로 정지하고 콘솔이 닫힐 때까지 기다립니다.
4. 기존 설치 폴더에서 패키지 소유 파일만 교체합니다. 실행 파일, 런처, 최초 설치 스크립트, 템플릿, 안내 문서, 라이선스, 고지 파일, 패키지의 `samples` 폴더가 여기에 해당합니다. `server.json`, `data`, `music`과 사용자 파일은 그대로 두고 복사·보관·이동하지 않습니다. 패키지의 `samples` 폴더는 음악 보관함이 아닙니다.
5. 다시 `start-server.cmd`를 실행합니다. `server.json`이 이미 있으므로 런처는 검증만 하고 파일을 쓰지 않으며 샘플 복사도 하지 않습니다. 주소·계정·보관함·앨범 아트·대기열·재생목록·세션이 그대로인지 확인하고, 이전 검증 패키지는 [롤백](#rollback)을 위해 보관하세요.

기존 설치에 샘플을 추가하는 것은 [업데이트 뒤 선택적 샘플 추가](#samples-after-upgrade)에서 다루는 별도의 선택 작업입니다.

<a id="desktop-windows"></a>
## 선택 사항인 Windows x64 데스크톱 앱

데스크톱 앱은 Windows 10/11 x64에서 기존 Server에 접속하기 위한 앱입니다. `jastreamer-desktop_0.2.0_windows-x64.zip`의 [차단을 해제](#windows-unblock)하고 릴리즈 체크섬과 대조한 뒤, `jastreamer-desktop` 폴더 전체를 쓰기 가능한 새 로컬 위치에 풀고 `jastreamer-desktop.exe`를 실행하세요. ZIP 안에서 실행하거나 EXE만 복사하면 안 됩니다.

```powershell
Get-FileHash .\jastreamer-desktop_0.2.0_windows-x64.zip -Algorithm SHA256
```

Server 화면은 지금 검색된 Server와 최근 연결을 구분해 보여 줍니다. 카드의 연결 동작을 선택하거나 **주소로 직접 연결**에서 호스트 이름 또는 완전한 HTTP(S) 루트 주소를 입력하세요. 앱은 연결 전에 Server를 확인하며, 검색이나 연결만으로 재생이 시작되지는 않습니다. 검색은 활성 IPv4 어댑터마다 5초 간격으로 질의하고 어댑터 변경도 따라가지만, 방화벽이나 멀티캐스트 제한이 있으면 주소를 직접 입력해야 할 수 있습니다.

선택 사항인 Windows 네이티브(WASAPI) 출력에는 `resources\native-audio\jastreamer-audio.exe`와 함께 제공되는 FFmpeg DLL이 든 데스크톱 패키지와, 호환되는 Server 제공 Web 화면이 모두 필요합니다. Web 페이지만 갱신해도 helper가 생기지 않으며 예전 패키지는 브라우저 오디오 전용으로 남습니다. 네이티브 출력은 사용자가 직접 켜는 기능이고 기본 장치에서 공유 모드로 시작하며 설정은 Web 화면에서 합니다. 자세한 내용은 [Windows 오디오](INSTRUCTION.ko.md#windows-audio)를 참고하세요. 패키지에 포함된 helper와 DLL을 임의의 코덱 DLL로 바꾸지 말고, 빌드나 CI 결과가 실제 소리나 DAC 수준의 비트 퍼펙트 전달을 입증한다고 말하지 마세요.

최근 Server 목록, 언어 설정, cookie, 세션, 네이티브 오디오 환경설정은 EXE 옆 `user-data`에 저장되므로 폴더는 계속 쓰기 가능해야 합니다. X로 창을 닫으면 앱은 알림 영역에 숨고 연결과 로컬 재생은 유지됩니다. 트레이 아이콘을 클릭하면 다시 열리고, 우클릭 후 **종료**를 선택하면 완전히 끝납니다. 종료해도 다른 네트워크 출력에 정지 명령을 보내지는 않습니다.

업그레이드할 때는 새 ZIP을 검증해 별도 임시 폴더에 풀고, 트레이에서 종료한 뒤 기존 폴더의 패키지 소유 파일만 교체하세요. `user-data`는 그대로 두며, Windows 계정이나 PC가 다르면 다시 로그인해야 할 수 있습니다.

<a id="desktop-linux"></a>
## 선택 사항인 Linux amd64 데스크톱 앱

그래픽 환경이 있는 Linux amd64에서 `jastreamer-desktop_0.2.0_linux-amd64.deb`를 사용합니다. 릴리즈 체크섬으로 검증한 뒤 의존성이 함께 해결되도록 APT로 설치하세요.

```sh
sha256sum -c jastreamer-desktop_0.2.0_linux-amd64.deb.sha256
sudo apt install ./jastreamer-desktop_0.2.0_linux-amd64.deb
```

응용 프로그램 메뉴의 **JASTREAMER**를 일반 사용자로 실행하거나 `/usr/lib/jastreamer-desktop/jastreamer-desktop`를 실행합니다. `sudo`로 실행하거나 `--no-sandbox`를 붙이지 마세요. 검색된 Server를 고르거나 완전한 HTTP(S) 주소를 입력하면 되고, 이 클라이언트에는 별도 FFmpeg나 오디오 플레이어가 필요 없습니다. Windows와 달리 트레이 동작이나 네이티브 WASAPI 출력은 없습니다.

패키지는 응용 프로그램 파일을 root 소유로 유지하고 `chrome-sandbox`를 `root:root` 4755로 설치하며, AppArmor를 지원하는 시스템에서는 `/usr/lib/jastreamer-desktop/jastreamer-desktop` 전용 user namespace 프로파일을 설치합니다. AppArmor나 시스템 전역 user namespace 제한을 끄지 않고 관리 대상이 아닌 정책은 보존하므로, 로컬 추가 설정은 `/etc/apparmor.d/local/jastreamer-desktop`에 두세요. 실행이 실패하면 sandbox 설정을 약화하지 말고 오류와 설치된 권한을 보고합니다.

최근 Server 목록, 언어, cookie, 세션은 root 소유 설치 폴더가 아니라 `$XDG_CONFIG_HOME/jastreamer-desktop`(보통 `~/.config/jastreamer-desktop`)에 저장됩니다. 새 DEB를 설치하기 전에 앱을 완전히 종료하고 이 프로필은 그대로 두세요. 같은 버전을 다시 설치하려면 `sudo apt install --reinstall ./jastreamer-desktop_0.2.0_linux-amd64.deb`를 사용합니다. 이전에 검증한 DEB는 롤백용으로 보관하세요.

<a id="android"></a>
## 선택 사항인 네이티브 Android 클라이언트

Kotlin 앱은 `_jastreamer._tcp` Server를 검색하고 `/api/v1/discovery`로 확인한 뒤 선택한 Server의 Web 화면을 엽니다. 홈 화면의 **로컬 재생**은 앱이 소유한 파일을 위한 독립 **저장된 음악** 보관함을 열며, 자체 다운로드·재생목록·폴더·기기 대기열과 Media3 재생을 갖추고 Server 접속이나 로그인이 필요 없습니다. Server 모드에서는 같은 Media3 서비스가 **이 기기** 출력을 제공하되 대기열은 Server가 소유하고, Android 시스템 미디어 제어의 명령도 Server로 보고됩니다. PWA 설치, 광범위한 로컬 음악 권한, 위치 권한은 필요하지 않습니다.

정식 릴리즈에는 Server와 같은 커밋에서 CI가 빌드한 서명된 APK가 첨부됩니다.

| 자산 | 내용 |
| --- | --- |
| `jastreamer-android_0.2.0_release.apk` | 설치용 앱 본체. jastreamer Android release 키로 v2·v3 서명 방식을 사용해 서명했고 application ID는 `io.jastreamer.android`, version name 0.2.0, version code 20000입니다 |
| `jastreamer-android_0.2.0_release.apk.sha256` | 게시된 바이트에 대한 체크섬 사이드카. `SHA256SUMS`에도 모든 자산의 같은 값이 들어 있습니다 |
| `jastreamer-android_0.2.0_release.manifest.json` | 소스 리비전, application ID, SDK 범위, 서명 방식, 서명 인증서 SHA-256, 서명 대상이 된 미서명 CI APK를 기록한 확인서 |

### APK 검증과 설치

1. 릴리즈에서 APK와 `.sha256` 사이드카, `SHA256SUMS`를 내려받습니다.
2. 아래 두 명령으로 내려받은 바이트와 서명 인증서를 확인합니다. `apksigner`는 Android SDK build-tools에 들어 있으며, Windows에서는 체크섬 확인에 `Get-FileHash .\jastreamer-android_0.2.0_release.apk -Algorithm SHA256`을 사용하세요.
3. 검증한 APK를 휴대전화로 옮겨 엽니다. Android가 물을 때만 파일을 연 앱에 **알 수 없는 앱 설치** 권한을 허용하고, 설치가 끝나면 그 권한을 해제하세요. 이미 승인된 ADB 연결이 있다면 `adb install jastreamer-android_0.2.0_release.apk`도 가능합니다.
4. Play Protect가 Google Play에서 받은 앱이 아니라고 경고할 수 있습니다. 이는 배포 경로를 알리는 것이지 문제를 찾았다는 뜻이 아닙니다. 2단계가 일치할 때만 계속하고, Play Protect·인증서 검사·기기 보안은 끄지 마세요.

```sh
sha256sum -c jastreamer-android_0.2.0_release.apk.sha256
apksigner verify --print-certs jastreamer-android_0.2.0_release.apk
```

출력된 `Signer #1 certificate SHA-256 digest` 값은 `53285c2c239aff2927ebe6f5c6aebb82fdbb50956ed84b1e9f0222b2d925943e`여야 합니다. 도구에 따라 같은 지문을 대문자와 콜론 형식(`53:28:5C:2C:…:25:94:3E`)으로 표시하므로 릴리즈 페이지에 적힌 값과 비교하세요. 값이 다르면 중단합니다. 키가 다르면 업데이트가 아니라 다른 앱입니다.

같은 release 키로 서명한 다음 릴리즈는 기존 앱 위에 덮어쓰기 설치되어 데이터를 유지합니다. 덮어쓰기 업데이트에는 같은 application ID와 서명자, 낮아지지 않은 version code가 필요하기 때문입니다. 업데이트를 강행하려고 앱을 제거하거나 저장 공간을 지우지 마세요. 아래에서 설명하는 앱 소유 음악과 상태가 모두 사라집니다. Google은 Google Play 밖에서 설치하는 앱에 개발자 신원 확인을 요구하겠다고 발표했고 국가별로 순차 적용할 예정이므로, 해당 지역이라면 릴리즈 페이지에서 현재 배포 상태를 확인하세요.

### 개발용 CI 빌드

CI 산출물은 개발·테스트용입니다. 성공한 [Android CI 실행](https://github.com/furyheimdall/jastreamer/actions/workflows/android.yml)은 시험한 소스 리비전에 대해 `jastreamer-android-debug-test-signed-and-release-unsigned-<revision>`을 제공하며, PR 산출물은 보호된 main의 릴리즈가 아닙니다.

| 산출물 | 상태 |
| --- | --- |
| `*_debug-test-signed.apk` | 개발 테스트 전용. application ID가 `io.jastreamer.android.debug`라 정식 앱과 별개의 앱이며, 정식 앱을 업데이트할 수 없고 저장된 음악·프로필·환경설정도 공유하지 않습니다. debug 인증서는 CI 실행마다 달라질 수 있습니다 |
| `*_release-unsigned.apk` | 게시된 상태로는 설치할 수 없습니다. 릴리즈 워크플로가 바로 이 파일을 release 키로 서명하고 두 파일의 다이제스트를 릴리즈 확인서에 기록합니다 |

구성 요소 간 의존 관계:

- 새 가져오기는 플랫폼 공용 v1 다운로드 기능을 광고하는 APK **와** Server/Web UI가 모두 있어야 하고, 폴더 가져오기에는 폴더 대상을 구현한 Server 빌드가 추가로 필요합니다. APK만 또는 Web 페이지만 갱신해서는 생기지 않습니다.
- 구형이거나 접속할 수 없는 Server가 앱을 막아서는 안 됩니다. 이미 저장된 음악은 계속 쓸 수 있고, Server 업데이트는 별도로 승인된 절차입니다.

네트워크: HTTP는 신뢰할 수 있는 사설 LAN에서만 사용하고, HTTPS는 기기의 정상적인 신뢰 저장소로 검증되어야 합니다. 인증서가 호스트 이름만 포함하면 불일치를 무시하지 말고 그 이름을 입력하세요. 앱의 target은 API 36이며 위치 권한도 target 37용 `ACCESS_LOCAL_NETWORK`도 요청하지 않습니다. Android 17은 이전 target에 LAN 접근을 암시적으로 허용하지만, 접근이 차단되면 오류가 그대로 드러납니다. Wi-Fi 기기 격리, 멀티캐스트 차단, VPN 경로 때문에 검색이 안 될 수 있으며 주소 직접 입력은 계속 사용할 수 있습니다. 테스트를 통과시키려고 기기 호환성 플래그나 네트워크 권한을 바꾸지 마세요.

같은 ID와 같은 서명으로 덮어쓰는 업데이트는 앱 전용 저장 음원, 복사한 앨범 아트, 로컬 재생목록, 저장된 장르 메타데이터와 기기 좋아요, 기기 대기열과 재생 위치, 폴더, 다운로드·언어 환경설정, 최근 Server, 격리된 Server 프로필을 보존합니다. 장르와 좋아요를 담는 로컬 메타데이터 DB는 스키마 버전 2를 사용하며, 버전 1 보관함을 열면 테이블을 다시 만들지 않고 열만 추가합니다. 이 DB의 다운그레이드는 지원하지 않습니다. **최근 서버 목록에서 제거**는 바로가기만 지울 뿐 로그아웃도, 프로필이나 저장된 음악 삭제도 아닙니다. 세션을 끝내고 해당 Server의 미완료 가져오기를 중지하려면 Server 화면에서 로그아웃하세요. 그래도 이미 완료된 기기 소유 음악은 그대로 남습니다. 앱을 제거하거나 Android의 **저장 공간/데이터 지우기**를 사용하면 앱 컨테이너 전체가 지워지며, 이 데이터는 클라우드·기기 이전 백업에서 제외됩니다.

소스 개발에는 `apps/android`의 고정된 Gradle Wrapper와 JDK 17, SDK platform 36, Build Tools 35.0.0을 사용하고 `./gradlew :app:testDebugUnitTest :app:lint :app:assembleDebug :app:assembleRelease`를 실행합니다. 전체 계측 검증은 `.github/workflows/android.yml`의 격리된 API 36 에뮬레이터와 실제 Server/Web fixture를 사용하며 사용자가 설치한 Server는 건드리지 않습니다. 에뮬레이터 성공은 실물 기기 설치·네트워크, Bluetooth·헤드셋 동작, 실제 소리를 입증하지 않습니다.

Server 모드, 저장된 음악, 다운로드, 재생 전환, 수명주기 동작은 [Android 화면](INSTRUCTION.ko.md#android-controls)을 참고하세요.

<a id="ios"></a>
## 네이티브 iOS 소스와 CI

`apps/ios`의 SwiftUI/WKWebView 앱은 `_jastreamer._tcp` Server를 검색하고 `/api/v1/discovery`로 확인한 뒤 선택한 Server의 Web 화면을 사용합니다. 네이티브 파일 선택기 요청을 거부하는 공개 WebKit API를 포함해 **iOS/iPadOS 18.4** 이상이 필요합니다. 제어 전용이라 WebView가 미디어 로드를 차단하므로 네이티브 오디오 엔진, 저장 음악 플레이어, 로컬 재생 진입점은 없습니다.

**현재 범위는 소스, 미서명 기기용 빌드, 시뮬레이터 CI뿐입니다.** 성공한 [iOS CI 실행](https://github.com/furyheimdall/jastreamer/actions/workflows/ios.yml)은 `jastreamer-ios-development-unsigned-and-simulator-<revision>`을 제공하며, 사용하기 전에 `SHA256SUMS`, `provenance.json`, 소스 리비전, bundle identifier, 플랫폼을 확인하세요. PR 산출물은 검증한 merge 리비전을 기록할 뿐 보호된 main의 릴리즈가 아닙니다.

- `*_device-development-unsigned-not-installable.zip`은 미서명 `.app`이며 IPA도, 설치 가능한 iPhone/iPad 패키지도 아닙니다.
- `*_simulator-development-test-adhoc.zip`은 Xcode의 ad-hoc 테스트 서명과 아키텍처가 맞는 시뮬레이터를 요구하며 휴대전화나 태블릿에는 설치할 수 없습니다.
- 이 CI는 Apple 서명 자격 정보, provisioning profile, 기기 설치, TestFlight, App Store 게시, production 업데이트 경로를 구성하지 않습니다.

승인된 macOS 개발 환경에서는 `apps/ios/Jastreamer.xcodeproj`를 열고 공유 **Jastreamer** scheme을 사용합니다. 재현 가능한 격리된 iPhone 16 시나리오는 저장소 루트에서 다음 명령으로 실행합니다.

```sh
export DEVELOPER_DIR=/Applications/Xcode_16.4.app/Contents/Developer
make build
bash tooling/qa/ios-simulator-ci.sh
```

[워크플로](.github/workflows/ios.yml)는 서명을 끈 generic iPhone/iPad 빌드도 수행하고 단위 테스트와 실제 Web UI 테스트가 통과한 뒤에만 패키징하며, 언제나 사용자가 설치한 Server가 아닌 일회용 fixture를 사용합니다. 실물 기기 설치, Wi-Fi·Bonjour 동작, 실제 소리를 검증하지는 않습니다.

별도로 승인한 기기용 빌드에서는 검색에 **로컬 네트워크** 접근 권한과 알맞은 Wi-Fi·멀티캐스트 경로가 필요합니다. 사설 LAN HTTP는 암호화되지 않으며 iOS의 로컬 네트워크 ATS 예외는 로컬 이름과 IP 주소에만 해당하고 임의의 HTTP 도메인 이름에는 적용되지 않습니다. 필요하면 신뢰할 수 있는 HTTPS 호스트 이름을 쓰되 인증서 검증은 우회할 수 없습니다. 승인된 업데이트에서도 앱을 제거하거나 데이터를 지우지 말고 앱 컨테이너와 격리된 프로필을 보존하세요.

Server 선택, 세션, 키보드 탐색, 수명주기 동작은 [iOS 화면](INSTRUCTION.ko.md#ios-controls)을 참고하세요.

<a id="pwa"></a>
## 선택 사항인 휴대전화 PWA

설치형 휴대전화 앱은 Server가 제공하는 화면을 다른 형태로 여는 창입니다. 그 Server에 네트워크로 접근할 수 있어야 하고 service worker, 캐시된 보관함, 오프라인 재생은 제공하지 않으며, 데스크톱 앱에는 필요하지 않습니다.

1. 휴대전화 브라우저에서 Server의 완전한 주소를 열고 로그인합니다.
2. 브라우저 자체의 **앱 설치** 또는 **홈 화면에 추가** 메뉴를 사용하고 확인 단계를 완료합니다. iPhone Safari에서는 **공유 → 홈 화면에 추가**를 사용합니다.
3. 저장된 바로가기를 실행합니다. iPad는 휴대전화 화면 대신 태블릿·표준 화면을 사용하지만 Safari의 홈 화면 추가 안내는 나타날 수 있습니다.

설치에는 해당 휴대전화와 브라우저가 신뢰하는 HTTPS origin이 필요합니다. 일반 HTTP는 `localhost`, `127.0.0.1`, `::1`의 로컬 개발에서만 허용됩니다. **설정**에서 인증서, 개인 키, HTTPS 리스너를 지정해 저장하고 요청되면 Server를 재시작한 뒤 신뢰할 수 있는 HTTPS 주소를 다시 여세요. 인증서 경고를 무시하거나 신뢰하지 않는 인증서를 우회책으로 등록하거나 NAS를 공용 인터넷에 노출하지 말고, HTTP 리스너 앞에 단순한 TLS 종료 프록시를 두지 마세요. Server는 변경 요청의 origin을 자신의 리스너 scheme과 비교합니다.

휴대전화 화면과 일반 제어는 설치 없이도 동작하므로, 신뢰하는 사설 LAN의 HTTP 배포에서는 Server 페이지를 새로고침해 브라우저에서 그대로 사용하면 됩니다. scheme·호스트 이름·포트가 달라지면 브라우저 origin이 달라져 다시 로그인해야 할 수 있습니다. **설정**에는 설치 상태 카드가 없으며 기존 바로가기는 계속 동작합니다.

휴대전화 화면과 재생 제어는 [휴대전화 화면](INSTRUCTION.ko.md#phone-controls)을 참고하세요.

<a id="verify"></a>
## 검증 체크리스트

새로 설치했을 때, 업데이트했을 때, 롤백했을 때 아래를 확인하세요. 검증한 이미지 다이제스트나 패키지 SHA-256을 결과와 함께 기록해 둡니다.

1. Server 호스트와 LAN의 다른 클라이언트 양쪽에서 `curl --fail http://<server>:<port>/healthz`가 성공한다.
2. 평소 사용할 실제 주소와 포트로 Web 화면이 열리고 로그인된다(최초 설정에서 관리자 계정을 만든다).
3. **설정 → 보관함**에 의도한 음악 루트가 보이고 **지금 스캔**이 끝난 뒤 예상한 곡 수와 앨범 아트가 나타난다.
4. 출력이 준비된다. 검색된 수신기이거나 브라우저·데스크톱 앱의 **이 기기**면 된다.
5. 고른 곡 하나가 그 출력에서 실제로 들리고 일시정지·탐색·정지가 동작한다.
6. 업데이트한 경우 대기열, 현재 곡, 재생목록, 좋아요, 재생 횟수, 설정, 세션이 이전 상태와 같고, 직접 시작하기 전까지 재생은 정지 상태로 유지된다.
7. Server를 재시작해도 설정·보관함·계정과 정지 상태가 그대로 유지된다.

실패하면 비밀 정보를 뺀 실패 단계와 오류 문구를 그대로 남기고, data 폴더의 `logs/server.log`를 확인한 뒤 [문제 해결](INSTRUCTION.ko.md#troubleshooting)에서 증상을 찾아보세요. 그 전에는 다른 설정을 바꾸지 마세요.

<a id="locations"></a>
## 파일 위치

| 내용 | Linux 컨테이너 | Windows 무설치 |
| --- | --- | --- |
| 설정 | `/etc/jastreamer/server.json`(호스트의 `${JASTREAMER_CONFIG_PATH}`) | `jastreamer-server.exe` 옆의 `server.json` |
| 데이터베이스: 계정·세션·보관함·재생목록·좋아요·대기열·현재 곡·재생 횟수·다운로드 작업·진단 기록 | `/var/lib/jastreamer/server.sqlite` | `data\server.sqlite` |
| 앨범 아트 캐시 | `/var/lib/jastreamer/artwork` | `data\artwork` |
| 준비된 다운로드 | `/var/lib/jastreamer/downloads` | `data\downloads` |
| AirPlay 식별자와 자격 증명 | `/var/lib/jastreamer/airplay` | 해당 없음 |
| 진단 로그 | `/var/lib/jastreamer/logs/server.log`와 회전 보관 파일 최대 3개, 각 5 MiB | `data\logs\server.log`, 회전 방식 동일 |
| 음악 | 읽기 전용으로 연결한 `/music` | 설정한 보관함 루트 |

클라이언트 쪽 상태는 다음과 같습니다. Windows 데스크톱 앱은 최근 Server, 언어, 세션, 네이티브 오디오 환경설정을 EXE 옆 `user-data`에 두고, Linux 데스크톱 앱은 `~/.config/jastreamer-desktop`을 사용하며, Android 앱은 저장된 음악과 프로필을 앱 전용 저장소에 둡니다. 백업할 때는 Server의 data 폴더와 `server.json`을 함께 보관하고, 로그를 보고서에 첨부하기 전에는 주소나 계정 정보가 의도치 않게 나가지 않도록 내용을 한 번 읽어 보세요.

<a id="upgrade"></a>
## 업데이트

Linux Server 업데이트는 검증한 이미지로 컨테이너를 교체하는 작업이며 최초 계정 설정을 다시 하는 것이 아닙니다. 이미지에는 해당 아키텍처의 Web 화면, FFmpeg, AirPlay 실행 환경이 들어 있습니다. Server가 스스로 업데이트하도록 Docker 소켓을 연결하거나 호스트 관리 권한을 주지 마세요. 무설치 Windows Server는 아래 Compose 절차 대신 [무설치 Windows Server 업데이트](#windows-server-update)를 따릅니다.

### 시작할 때 적용되는 스키마 변경

새 Server를 배포하기 전에 아래 변경을 검토하고 승인하세요. 모두 기존 데이터베이스 안에서 수행되며 계정·보관함·설정·음악을 보존하고, 자동 재생이나 자동 스캔을 하지 않습니다.

| 변경 | 효과 | 다운그레이드 |
| --- | --- | --- |
| 폴더 작업을 위한 `download_jobs.kind` 확장 | 기존 작업, 하위 행, 인덱스, 외래 키, artifact 참조를 보존하는 트랜잭션 마이그레이션 | 구형 빌드는 폴더 작업을 구현하지 않으므로 이미지만 되돌리는 다운그레이드는 호환되지 않음 |
| `player_current` 추가 | 선택된 대기열 항목에서 트랜잭션으로 초기화하며, 그 항목이 대기열에서 빠져도 불러온 곡과 이동 위치를 유지 | 구형 Server는 대기열 밖의 곡을 해석하지 못하므로 이미지만 되돌리는 다운그레이드는 호환되지 않음 |
| `player_mode`·`player_shuffle` 추가 | 셔플·반복 설정과 항목 기반 이동 순서를 보존 | 구형 Server는 무시하며, 다시 돌아오면 그동안의 대기열 변화를 반영 |
| `track_play_counts` 추가 | 재생 횟수는 0에서 시작하며 과거 청취 기록을 소급하지 않음 | 구형 빌드는 테이블을 쓰지 않아 아무것도 기록하지 않음 |
| `error_history`와 보관함 검사 테이블 추가 | 수신기 오류와 음원 무결성 기록을 정해진 개수만큼 보관 | 구형 빌드는 사용하지 않음 |

다운그레이드를 강행하려고 데이터를 초기화하거나 작업을 지우거나 삭제된 대기열 항목을 되살리지 마세요. Server와 Web을 함께 업데이트한 뒤에는 열려 있는 Control 화면을 새로고침하세요. 이제 대기열 비우기는 다음 곡들만이 아니라 전체 항목을 지웁니다.

### 업데이트 절차

처음부터 끝까지 **기존** Compose 프로젝트 이름, 프로젝트 폴더, Compose 파일, 환경 파일을 사용하고 기본 저장 경로로 두 번째 설치를 만들지 마세요. config·data와 음악은 현재 경로에 그대로 두고 같은 마운트로 다시 사용하며, 이미지 업데이트를 위해 복사하거나 보관하지 않습니다.

1. **대상과 실제 저장 상태를 점검합니다.** 릴리즈의 변경 사항과 설정·데이터베이스 호환성 안내, 필요한 중간 버전을 읽습니다. 실행 중인 컨테이너의 `Mounts`와 config 인자를 현재 Compose·환경 파일, 현재 설정의 `data_dir`과 모든 보관함 루트와 대조하세요. 호스트 경로나 named volume, 컨테이너 대상 경로, 심볼릭 링크의 실제 대상, 소유자, 권한, 읽기·쓰기 모드와 현재 이미지·아키텍처·서비스 주소를 기록합니다. 불일치, 누락된 마운트, 읽을 수 없는 경로가 있으면 중단하고, 저장 위치 이전은 별도 승인을 받습니다.
2. **정지 전에 내려받습니다.** 선택한 릴리즈의 정확한 다이제스트를 pull합니다. 공개 레지스트리는 로그인이 필요 없고, 명시적으로 선택한 비공개 레지스트리에서만 대화형 인증을 사용합니다. `amd64`인지 `arm64`인지 확인하거나 [오프라인 패키지](#releases) 절차로 맞는 플랫폼을 가져오세요. 가변 태그를 쓰거나 레지스트리 주소를 지어내거나 기존 이미지를 지우지 마세요.
3. **중단 시점을 합의합니다.** 재생을 정지하고 정지 상태를 확인한 뒤, 기존 프로젝트에서 `jastreamer-server`만 정지합니다. 다른 서비스를 정지하거나 볼륨을 제거하거나 `down -v`를 쓰지 마세요.
4. **저장된 이미지 참조만 바꿉니다.** `JASTREAMER_SERVER_IMAGE`를 검증한 다이제스트나 가져온 로컬 이미지 ID로 설정하고, UID/GID `10001:10001`, host 네트워킹, 읽기 전용 루트 파일시스템과 음악 마운트, 쓰기 가능한 config·data 마운트를 비롯한 나머지 설정과 마운트는 모두 유지합니다. `server.json`을 새 설치용 템플릿으로 바꾸지 마세요. `docker compose config`로 결과를 확인하고, 시작 전에 대상 이미지의 `--check-config`로 기존 설정을 검증합니다.
5. **Server만 다시 만듭니다.** 기존 프로젝트에서 `up -d --no-deps jastreamer-server`를 실행하고, 실행 중인 이미지가 의도한 다이제스트·플랫폼과 같은지 확인한 뒤 컨테이너 상태와 로그를 살핍니다.
6. **직접 확인합니다.** 이전에 쓰던 주소와 포트로 브라우저나 데스크톱의 Web 화면을 새로고침하고 [검증 체크리스트](#verify)를 따릅니다. 컨테이너가 만들어진 것만으로는 성공이 아닙니다.

<a id="samples-after-upgrade"></a>
### 업데이트 뒤 선택적 샘플 추가

업데이트는 현재 음악·설정·데이터를 보존하며 샘플을 자동으로 추가하지 않습니다. 업데이트한 Server를 검증한 뒤, 운영자가 명시적으로 승인하면 재생을 정지한 상태에서 릴리즈의 샘플 세 곡을 추가할 수 있습니다. Linux·Synology에서는 `/music` 마운트나 `server.json`을 바꾸지 말고 고정 이미지의 복사 명령을 `<기존-음악-루트>/jastreamer-samples`에 대해 다시 실행합니다. Windows에서는 `samples\manifest.json`을 검증한 뒤 승인된 루트의 `jastreamer-samples` 자식 폴더로 충돌하지 않는 파일과 고지 문서만 복사합니다. 두 경우 모두 동일한 파일은 그대로 두고, 충돌이 있으면 중단하며, 루트의 소유자와 권한을 유지합니다. 파일을 넣는 것만으로 스캔·대기열 추가·재생이 일어나지는 않습니다.

### 데스크톱과 클라이언트 업데이트

데스크톱 앱은 휴대전화·PWA 화면을 포함해 Server가 제공하는 최신 Web 화면을 보기 위해 패키지를 교체할 필요가 없습니다. 릴리즈가 데스크톱 실행 파일도 갱신한 경우에만 [Windows](#desktop-windows) 또는 [Linux](#desktop-linux) 절차를 따르고, Windows의 `user-data` 폴더나 Linux의 사용자 프로필을 보존하세요. 같은 release 키로 서명한 새 Android APK는 기존 앱 위에 덮어쓰기 설치되어 데이터를 유지하며, iOS는 해당 절의 절차를 따릅니다.

<a id="rollback"></a>
## 롤백

시작이나 검증에 실패하면 새 Server를 정지하고 로그와 상태를 보존하세요. 이전에 검증한 이미지로 돌아가기 전에 그 버전이 현재 설정과 데이터베이스를 지원하는지 위의 스키마 표로 확인합니다. 마이그레이션이 끝난 뒤에는 이미지만 되돌려서는 동작하지 않을 수 있습니다. 호환 여부를 알 수 없거나 다운그레이드가 지원되지 않으면 데이터를 초기화하거나 고쳐 쓰지 말고 Server를 정지한 채 필요한 결정 사항을 보고하세요.

승인된 호환 롤백에서는 저장된 이미지 참조만 바꾸고 같은 설정·마운트로 Server를 다시 만든 뒤, 재생을 자동으로 시작하지 않은 상태에서 원래 주소와 보존된 상태를 다시 확인합니다. 무설치 Windows Server도 같은 호환성 확인을 거쳐 Server를 정지하고, `server.json`·`data`·음악은 그대로 둔 채 이전 검증 패키지의 패키지 소유 파일만 되돌립니다. 데스크톱만 롤백할 때도 패키지 소유 파일만 교체하고 Windows의 `user-data`나 Linux의 사용자 프로필은 유지합니다.

업데이트와 롤백 중 원본 음악을 지우거나 바꾸지 마세요. 원래 버전이 정상 동작하면 [사용자 안내서](INSTRUCTION.ko.md)로 돌아가 사용하면 됩니다.

<a id="uninstall"></a>
## 제거

소프트웨어를 지우려고 음악을 삭제할 필요는 없습니다. 앱 소유 저장소를 지우는 단계 전에 보관할 자료를 먼저 복사하세요.

| 대상 | 방법 |
| --- | --- |
| Linux·Synology Server | `docker compose -f compose.yaml down`으로 프로젝트를 내린 뒤(`-v`는 사용 금지) 고정한 다이제스트를 `docker image rm`으로 지웁니다. 호스트의 config·data·음악 폴더는 남으며, 계정·보관함 상태·대기열·재생목록·좋아요·재생 횟수를 잃어도 괜찮을 때만 `config`와 `data`를 삭제하세요 |
| Windows Server | 콘솔에서 Ctrl+C로 정지합니다. 설치 폴더를 지우기 전에 `data`와 폴더 안에 둔 음악을 옮기거나 백업하세요. `jastreamer-server.exe`에 허용했던 Windows 방화벽 규칙도 정리합니다 |
| Windows 데스크톱 앱 | 트레이에서 **종료**한 뒤 압축을 푼 폴더를 삭제합니다. `user-data`도 함께 지워지므로 저장된 세션과 네이티브 오디오 설정이 사라집니다 |
| Linux 데스크톱 앱 | `sudo apt remove jastreamer-desktop`을 실행하면 패키지가 설치했던 AppArmor 프로파일도 제거됩니다. 저장된 세션까지 지우려면 `~/.config/jastreamer-desktop`을 따로 삭제하세요 |
| Android | Android에서 앱을 제거하면 저장된 음악, 앨범 아트, 로컬 재생목록, 기기 대기열, 폴더, 환경설정, Server 프로필을 포함한 앱 컨테이너 전체가 사라지며 이 데이터는 클라우드 백업 대상이 아닙니다. 정식 앱(`io.jastreamer.android`)과 개발용 CI 빌드(`io.jastreamer.android.debug`)는 서로 다른 앱이므로 각각 제거해야 합니다 |
| 휴대전화 PWA | 홈 화면 바로가기를 삭제합니다. 세션까지 끝내려면 먼저 Server 화면에서 로그아웃하세요 |
