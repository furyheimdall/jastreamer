# jastreamer 0.2 설치 및 업데이트 안내서

[프로젝트 소개](README.ko.md) · [한국어 사용자 안내서](INSTRUCTION.ko.md) · [English installation guide](INSTALL.md)

실제로 사용할 패키지와 플랫폼에 맞는 아래 분기를 선택하세요. Server가 Web 화면을 제공하며, 선택 사항인 Desktop과 휴대전화 PWA는 기존 Server의 클라이언트이지 Server나 로컬 오디오 Renderer가 아닙니다.

공개 프리뷰는 미서명이며 production 검증을 마친 릴리즈가 아닙니다. 선택한 릴리즈의 제한을 읽고 내려받은 모든 파일을 검증한 뒤 실제 네트워크와 수신기에서 동작을 확인하세요. 설치 후 화면 사용법과 문제 해결은 [한국어 사용자 안내서](INSTRUCTION.ko.md)를 참고하세요.

| 하려는 작업 | 따라갈 분기 |
| --- | --- |
| Linux 또는 Synology에서 Server 실행 | [Linux Server](#linux-server) |
| Windows에서 Server 실행 | [Windows Server](#windows-server) |
| 기존 Server에 접속할 데스크톱 앱 추가 | [Windows ZIP](#desktop-windows) 또는 [Linux DEB](#desktop-linux) |
| 휴대전화 사용 또는 홈 화면 앱 설치 | [휴대전화 PWA](#pwa) |
| 기존 설치 업데이트 또는 복구 | [백업과 업데이트](#backup-and-upgrade) 또는 [롤백](#rollback) |

<a id="requirements"></a>
## 1. 요구 사항과 안전 주의

- Linux 컨테이너: Docker Engine과 Compose v2가 있는 Linux `amd64`·`arm64` 또는 Container Manager가 있는 Synology DSM. `arm/v7`은 지원하지 않으며 DS918+는 `amd64`입니다.
- 네이티브 무설치 Server: Windows x64와 현재 사용자가 쓸 수 있는 로컬 설치 폴더. Windows 서비스로 설치되지 않습니다.
- Windows 데스크톱 앱: Windows 10/11 x64. Windows ARM64 데스크톱 패키지는 없습니다.
- Linux 데스크톱 앱: 그래픽 환경이 있는 Linux `amd64`. Ubuntu 24.04 amd64가 네이티브 설치·sandbox 검증 대상이며 Linux ARM64 데스크톱 패키지는 없습니다.
- 게시된 jastreamer 0.2 프리뷰의 정확한 이미지 다이제스트 또는 Windows Server ZIP, 또는 별도로 전달받아 검증한 오프라인 패키지. 프리뷰 및 실장비 검증 한계는 그대로 적용됩니다.
- Server, 브라우저·앱과 출력이 연결된 신뢰할 수 있는 사설 LAN. 자동 검색에는 멀티캐스트가 필요합니다.
- Linux에서는 컨테이너 UID/GID `10001:10001`이 쓸 수 있는 분리된 config·data 폴더와 UID 10001이 읽을 수 있도록 읽기 전용 연결한 기존 음악 폴더.
- Windows에서는 Server 실행 계정이 읽을 수 있는 음악 경로. 기본으로 나란히 생성되는 `server.json`, `data`, `music`은 그 계정이 계속 쓸 수 있어야 합니다.

Server의 config, data와 music은 서로 분리하세요. `chmod 777`을 사용하거나 음악 보관함 전체의 소유권을 재귀적으로 바꾸지 마세요. HTTP는 자격 증명과 오디오를 암호화하지 않습니다. LAN 경로 전체를 신뢰할 수 없다면 직접 준비한 PEM 인증서·키로 Server의 내장 HTTPS 리스너를 사용하세요. 인증서 경고를 무시하거나 Server를 공용 인터넷에 직접 노출하지 마세요.

<a id="releases"></a>
## 2. 릴리즈 확보와 검증

### 공개 레지스트리 이미지

[GitHub Releases](https://github.com/furyheimdall/jastreamer/releases)에서 게시된 프리뷰를 선택하고, 검증 한계를 읽은 뒤 릴리즈 설명·manifest의 정확한 서버 이미지 주소를 확인하세요. 프리릴리즈는 최신 production 릴리즈로 표시하지 않으므로 `/releases/latest`로 프리뷰를 자동 선택하지 마세요.

레지스트리는 `ghcr.io/furyheimdall/jastreamer-server`이며 공개 이미지는 GitHub 토큰 없이 내려받을 수 있습니다. 아래 예시의 다이제스트를 선택한 릴리즈의 실제 전체 값으로 바꾸고, 태그를 추측하지 마세요.

```sh
export JASTREAMER_SERVER_IMAGE='ghcr.io/furyheimdall/jastreamer-server@sha256:<digest-from-release>'
docker pull "$JASTREAMER_SERVER_IMAGE"
docker image inspect --format '{{.Os}}/{{.Architecture}} {{.Id}}' "$JASTREAMER_SERVER_IMAGE"
```

멀티아키텍처 Linux 이미지에서 호스트에 맞는 `amd64` 또는 `arm64`가 선택됩니다. 정확한 다이제스트를 영구 배포 환경 설정에 보관하고, 호스트에 FFmpeg나 Python을 따로 설치하지 마세요. Google Cast 자체에는 Chrome이나 Python helper가 필요하지 않습니다. Windows Server 또는 데스크톱 파일과 체크섬도 같은 릴리즈에서 받으세요.

### 별도로 전달받은 오프라인 패키지

오프라인 서버 묶음을 별도로 전달받았다면 가져오기 전에 함께 제공된 체크섬을 검증하세요.

```sh
cd /path/to/supplied/release
sha256sum -c SHA256SUMS
```

명시적으로 승인된 비공개 레지스트리도 대안으로 사용할 수 있습니다. 정확한 다이제스트와 비공개 인증 수단을 사용하고 토큰을 Compose 파일·소스 저장소·지원 로그에 넣지 마세요.

전달된 묶음에 멀티아키텍처 `.oci`가 포함되어 있다면 `docker load`에 직접 넣을 수 없습니다. 오프라인 Docker TAR가 필요하면 Skopeo가 있는 Linux 컴퓨터에서 대상 아키텍처 하나만 변환합니다.

```sh
ARCH=amd64  # 또는 arm64
skopeo copy --override-os linux --override-arch "$ARCH" \
  oci-archive:jastreamer-server_0.2.0_linux_amd64-arm64.oci \
  docker-archive:jastreamer-server_0.2.0_linux_${ARCH}.tar:jastreamer-server:0.2.0
sha256sum jastreamer-server_0.2.0_linux_${ARCH}.tar
docker load --input jastreamer-server_0.2.0_linux_${ARCH}.tar
docker image inspect --format '{{.Id}}' jastreamer-server:0.2.0
```

파일을 전송했다면 새 TAR의 체크섬을 비교하세요. 출력된 `sha256:...` 로컬 이미지 ID를 `JASTREAMER_SERVER_IMAGE`로 사용합니다.

<a id="linux-server"></a>
## 3. Linux 또는 Synology Server

Linux `amd64`·`arm64` 이미지에는 Server, 내장 Web 화면, FFmpeg와 AirPlay helper·실행 환경이 포함됩니다. Linux와 Synology 모두 저장소의 `deploy/docker/server/compose.synology.yaml`을 사용합니다. 이 파일은 호스트 네트워크, 읽기 전용 컨테이너 파일 시스템, 임시 `/tmp`, capability 제거, `no-new-privileges`와 UID/GID `10001:10001`을 적용합니다.

서버에 맞는 경로를 선택하세요.

| 설치 대상 | 설정 | 데이터 | 음악 |
|---|---|---|---|
| Linux 예시 | `/srv/jastreamer/config` | `/srv/jastreamer/data` | `/srv/music` |
| Synology 예시 | `/volume1/docker/jastreamer/config` | `/volume1/docker/jastreamer/data` | `/volume1/music` |

아래 명령은 **서로 분리된 빈 config·data 경로에 새로 설치할 때만** 사용하세요. 기존 앱 상태가 들어 있다면 [백업과 업데이트](#backup-and-upgrade)를 따르고, 새 템플릿으로 기존 설정을 덮어쓰지 마세요. 서버에 맞는 세 경로를 정한 뒤 제공된 설정 파일을 설치합니다.

```sh
export JASTREAMER_CONFIG_PATH='/srv/jastreamer/config'
export JASTREAMER_DATA_PATH='/srv/jastreamer/data'
export JASTREAMER_MUSIC_PATH='/srv/music'

sudo mkdir -p "$JASTREAMER_CONFIG_PATH" "$JASTREAMER_DATA_PATH"
sudo cp /path/to/supplied/server.json "$JASTREAMER_CONFIG_PATH/server.json"
sudo chown -R 10001:10001 "$JASTREAMER_CONFIG_PATH" "$JASTREAMER_DATA_PATH"
sudo chmod 700 "$JASTREAMER_CONFIG_PATH" "$JASTREAMER_DATA_PATH"
sudo chmod 600 "$JASTREAMER_CONFIG_PATH/server.json"
```

Synology에서는 표의 `/volume1/...` 경로로 바꾸세요. UID 10001에 음악 공유 폴더의 읽기·경로 탐색 권한만 주고 폴더 전체의 소유자는 바꾸지 마세요.

복사한 `server.json`을 검토하세요.

- `data_dir`은 `/var/lib/jastreamer`로 유지합니다.
- 일반적인 음악 폴더 경로는 `/music`으로 유지합니다. Compose가 서버의 음악 폴더를 그곳에 연결합니다.
- 패키지의 실행 파일 경로 `/usr/local/bin/ffmpeg`와 `/usr/local/bin/jastreamer-airplay`를 유지합니다.
- 필요하면 `server_name`을 정하고, AirPlay를 사용하지 않을 때 끄거나, config 폴더의 PEM 파일로 내장 HTTPS를 설정합니다.
- 출력이 특정 서버 HTTP(S) 주소를 사용해야 할 때만 `media.base_url`을 지정합니다.
- Google Cast를 원할 때만 `cast.enabled`를 켭니다. 이전 설정에 이 값이 없어도 기본값은 `false`이며 Cast에는 helper 경로가 필요하지 않습니다.

웹 설정 화면이 `server.json`을 교체할 수 있도록 파일과 config 폴더 모두 UID 10001이 계속 쓸 수 있어야 합니다.

검증된 레지스트리 다이제스트 또는 가져온 로컬 이미지 ID로 시작합니다.

```sh
export JASTREAMER_SERVER_IMAGE='sha256:<verified-local-image-id>'
docker compose -f deploy/docker/server/compose.synology.yaml config
docker compose -f deploy/docker/server/compose.synology.yaml up -d
docker compose -f deploy/docker/server/compose.synology.yaml ps
docker compose -f deploy/docker/server/compose.synology.yaml logs --tail 100 jastreamer-server
curl --fail http://127.0.0.1:8080/healthz
```

GHCR에서 설치할 때는 로컬 이미지 ID 대신 완전한 공개 레지스트리 다이제스트를 사용하세요. 네 환경 변수 모두 기존 Compose 프로젝트의 영구 환경 설정에 저장해야 하며 임시 `export`만 배포 기록으로 남기면 안 됩니다.

Synology Container Manager에서는 같은 Compose 파일과 네 환경 변수를 **프로젝트**에 입력할 수 있습니다. 상태 확인 주소는 `http://<NAS-LAN-IP>:8080/healthz`입니다. 내장 HTTPS를 켰다면 8443 포트를 사용하세요. 사설 LAN의 클라이언트에서 Web 화면을 확인한 뒤 [최초 설정](INSTRUCTION.ko.md#1-최초-설정과-일상-사용)을 계속하세요. 컨테이너가 정상 상태라는 사실만으로 실제 소리가 난다고 판단하지 마세요.

<a id="windows-server"></a>
## 4. 네이티브 Windows x64 무설치 Server

선택한 프리뷰에서 `jastreamer-server_0.2.0_windows-x64.zip`과 `.sha256`, manifest, 검증 receipt를 받으세요. ZIP은 미서명이며 production 검증을 마치지 않았습니다. 압축을 풀기 전에 sidecar와 실제 바이트를 비교합니다.

```powershell
$file = '.\jastreamer-server_0.2.0_windows-x64.zip'
$expected = (Get-Content "$file.sha256" -Raw).Split()[0].ToLowerInvariant()
$actual = (Get-FileHash $file -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actual -ne $expected) { throw 'Windows Server ZIP checksum mismatch' }
```

ZIP 전체를 현재 사용자가 쓸 수 있는 새 로컬 폴더에 풀고, Server를 운영할 일반 사용자 계정으로 `start-server.cmd`를 실행하세요. ZIP 안에서 실행하거나 EXE만 복사하거나 관리자 권한으로 실행하거나 SmartScreen·Defender 전체를 약화하지 마세요. 최초 실행에서만 launcher가 없는 `server.json`과 나란한 `data`, `music` 폴더를 원자적으로 만들고 절대 경로를 기록한 뒤 설정을 검증하고 Server를 시작합니다. 기존 `server.json`은 절대 교체하지 않습니다. 콘솔 창을 열어 두고 Ctrl+C로 종료하세요.

같은 컴퓨터에서 `http://127.0.0.1:18080`을 열거나, 다른 클라이언트에서는 Windows 컴퓨터의 사설 LAN 주소와 18080 포트를 사용합니다. Windows 방화벽이 물으면 해당 실행 파일을 사설 네트워크에서만 허용하고 방화벽을 끄지 마세요. Web·미디어 접근에는 TCP 18080, UPnP 검색에는 SSDP UDP 1900, 선택적 Google Cast 검색에는 선택한 인터페이스의 mDNS UDP 5353이 필요합니다. Cast는 Server에서 각 수신기가 광고한 TCP 포트로 접근하고 수신기에서 Server 미디어 HTTP(S) URL로 접근할 수도 있어야 합니다. 나란한 `music` 폴더에 테스트 음악을 넣거나 Server를 멈추고 `library_roots`에 기존 Windows 절대 경로를 설정하세요. JSON 경로는 `C:/Music` 또는 이중 역슬래시를 사용할 수 있습니다.

이 패키지는 네이티브 Server이며 선택 사항인 Windows 데스크톱 앱이 아닙니다. UPnP/DLNA와 선택적 Google Cast 네트워크 출력을 제공하지만 Renderer를 설치하거나 PC 스피커에서 재생하지 않습니다. Google Cast에는 Chrome이나 Python helper가 필요하지 않지만 이 패키지는 FFmpeg 또는 Linux 전용 AirPlay helper를 포함하지 않습니다. 따라서 변환과 AirPlay의 기본값은 꺼짐입니다. Cast가 지원되는 원본 형식을 직접 전송할 수 있고, 변환 대체 경로를 사용하려면 운영자가 FFmpeg를 설정하고 미디어 변환을 켜야 합니다. 네이티브 CI는 패키지 바이트, 최초 설치, HTTP UI, 계정 유지, 재시작과 기존 설정 보존을 검증하지만 미서명 프리뷰를 production 검증 완료로 만들거나 모든 Windows 시스템·수신기를 인증하지는 않습니다.

기존 무설치 설치를 업데이트할 때는 Ctrl+C로 종료한 상태에서 `server.json`, `data`, `music`을 포함한 전체 폴더를 백업하세요. 새 ZIP을 검증해 별도 임시 폴더에 푼 다음 프로그램, launcher, 템플릿, 고지, 라이선스와 빌드 정보를 포함한 패키지 소유 파일만 기존 설치에서 교체합니다. `server.json`을 교체하거나 `data`·`music`을 삭제·이동하지 마세요. 그 안의 절대 경로와 기존 계정, 보관함, 앨범 아트, 대기열, 플레이리스트, 세션 상태를 유지해야 합니다. `start-server.cmd`를 실행해 변경하지 않은 설정 검증과 재시작 뒤 기존 URL·상태를 확인하세요. 롤백용 이전 검증 패키지와 그에 맞는 백업을 보관하세요.

<a id="desktop-windows"></a>
## 5. 선택 사항인 Windows 데스크톱 앱

Windows 10/11 x64에서 선택한 프리뷰의 미서명 `jastreamer-desktop_0.2.0_windows-x64.zip`을 사용합니다. 릴리즈 체크섬과 비교하세요.

```powershell
Get-FileHash .\jastreamer-desktop_0.2.0_windows-x64.zip -Algorithm SHA256
```

ZIP 전체를 쓰기 가능한 새 로컬 폴더에 풀고 `jastreamer-desktop.exe`를 실행하세요. ZIP 안에서 실행하거나 EXE만 복사하지 마세요. 발견된 서버를 선택하거나 완전한 HTTP(S) 주소를 입력합니다. 앱은 접속 전에 서버를 확인하며 검색·접속은 재생을 시작하지 않습니다.

서버 검색은 활성 IPv4 네트워크 어댑터마다 5초 간격으로 수행하며 어댑터 변경도 반영합니다. 방화벽이나 멀티캐스트 제한이 있으면 서버 주소를 직접 입력해야 할 수 있습니다.

최근 서버, 언어, 쿠키와 로그인 상태는 EXE 옆 `user-data`에 저장됩니다. 앱을 닫아도 서버 재생은 멈추지 않습니다. 업데이트할 때 완전히 종료하고 기존 폴더를 백업한 뒤 새 ZIP을 새 폴더에 풀고 기존 `user-data`를 새 EXE 옆에 보존하세요. 다른 Windows 계정이나 PC에서는 다시 로그인해야 할 수 있습니다.

<a id="desktop-linux"></a>
## 6. 선택 사항인 Linux 데스크톱 앱

그래픽 환경이 있는 Linux amd64에서 `jastreamer-desktop_0.2.0_linux-amd64.deb`을 사용합니다. 네이티브 설치·sandbox 검증 대상은 Ubuntu 24.04 amd64입니다. arm64 데스크톱 패키지나 Linux 서버 패키지가 아닙니다.

선택한 릴리즈의 체크섬과 DEB를 비교하고, 의존성도 설치하도록 APT를 사용하세요.

```sh
sha256sum -c jastreamer-desktop_0.2.0_linux-amd64.deb.sha256
sudo apt install ./jastreamer-desktop_0.2.0_linux-amd64.deb
```

일반 사용자로 앱 메뉴의 **JASTREAMER**를 열거나 `/usr/lib/jastreamer-desktop/jastreamer-desktop`을 실행하세요. `sudo`로 앱을 실행하거나 `--no-sandbox`를 추가하지 마세요. 서버를 선택하거나 완전한 HTTP(S) URL을 입력하면 되며, 이 클라이언트에 FFmpeg나 오디오 플레이어를 따로 설치할 필요는 없습니다.

설치 파일은 root 소유이며, `chrome-sandbox`는 `root:root`, `4755` 권한으로 설치합니다. 호환되는 AppArmor 시스템에서는 `/usr/lib/jastreamer-desktop/jastreamer-desktop` 실행 파일에 한정된 사용자 네임스페이스 프로필을 설치합니다. AppArmor나 시스템 전체의 사용자 네임스페이스 제한을 끄지 않습니다. 기존 사용자 관리 정책은 보존하며 로컬 추가 규칙은 `/etc/apparmor.d/local/jastreamer-desktop`에 둡니다. 실행이 실패하면 sandbox를 약화하지 말고 오류와 설치 권한을 확인하세요.

최근 서버, 언어, 쿠키와 로그인 상태는 root 소유 설치 폴더가 아닌 `$XDG_CONFIG_HOME/jastreamer-desktop`, 보통 `~/.config/jastreamer-desktop`에 저장됩니다. 새 DEB를 설치하기 전에 완전히 종료하고 이 프로필을 백업하세요. 패키지 버전이 같은 프리뷰를 교체한다면 `sudo apt install --reinstall ./jastreamer-desktop_0.2.0_linux-amd64.deb`을 사용합니다. 롤백용 이전 검증 DEB를 보관하고, 업데이트를 위해 프로필을 삭제하지 마세요.

<a id="pwa"></a>
## 7. 선택 사항인 휴대전화 Web 앱(PWA)

설치형 휴대전화 Web 앱은 Server가 제공하는 화면을 다른 형태로 여는 클라이언트이며, 선택 사항인 Desktop 앱에는 필요하지 않습니다.

PWA 설치에는 해당 휴대전화와 브라우저가 신뢰하는 인증서의 HTTPS origin이 필요합니다. HTTP는 `localhost`, `127.0.0.1`, `::1`에서 로컬 개발 용도로만 허용됩니다. 사설 LAN의 일반 HTTP 주소에서는 **Settings** 설치 카드가 신뢰할 수 있는 HTTPS가 필요하다고 표시합니다. **Settings**에서 Server가 읽을 수 있는 PEM 인증서·개인 키와 내장 HTTPS 리스너를 설정하고 저장한 뒤, 요청될 때 Server를 재시작하고 신뢰할 수 있는 HTTPS 주소를 다시 여세요.

휴대전화 화면과 일반 브라우저 제어에는 PWA 설치가 필요하지 않습니다. 신뢰하는 사설 LAN의 HTTP 배포에서는 Server 페이지를 새로고침해 브라우저로 사용하고, 설치는 신뢰할 수 있는 HTTPS가 준비된 뒤 진행하세요. 프로토콜·호스트 이름·포트가 바뀌면 별도의 브라우저 origin이 되므로 다시 로그인해야 할 수 있습니다.

인증서 경고를 무시하거나 신뢰하지 않는 인증서를 기기에 등록하거나 브라우저 보안을 약화하지 마세요. 단순한 TLS 종료 프록시를 앞에 두면 Server가 요청의 TLS 상태와 `Origin`을 비교하는 변경 API가 동작하지 않을 수 있으므로 문서화되지 않은 프록시 전달 방식을 사용하지 마세요. PWA를 위해 NAS나 Server를 공용 인터넷에 노출하지 마세요.

- Chromium 계열 브라우저가 실제 `beforeinstallprompt` 설치 이벤트를 제공하면 **Settings**의 **앱 설치** 버튼을 누르고 브라우저의 확인 단계를 완료합니다.
- iPhone에서는 Safari로 페이지를 열고 **공유** → **홈 화면에 추가**를 선택한 다음 iOS 확인 화면에서 승인합니다. iPad는 기존 태블릿·표준 화면을 유지하지만 Safari의 홈 화면 추가 안내는 표시될 수 있습니다.
- 이미 standalone 모드로 실행 중이면 설치 카드에 **설치됨** 상태가 표시됩니다. 브라우저가 설치 옵션을 제공하지 않으면 그대로 브라우저에서 사용할 수 있습니다.

설치는 launcher만 추가합니다. service worker나 미디어 cache를 사용하지 않으며 오프라인 재생, 알림, 앱 스토어 배포를 제공하지 않습니다. 앱을 열고 제어하려면 항상 jastreamer Server에 네트워크로 연결할 수 있어야 하며 재생 상태와 제어는 Server가 계속 관리합니다. 설치 후 휴대전화 화면 사용법은 [최초 설정과 일상 사용](INSTRUCTION.ko.md#1-최초-설정과-일상-사용)을 참고하세요.

<a id="backup-and-upgrade"></a>
## 8. 백업과 업데이트

Linux 컨테이너 대상은 최초 계정 생성을 다시 하는 대신 검증된 Server 컨테이너 이미지를 교체해 업데이트합니다. 이미지에는 해당 아키텍처의 Web 화면, FFmpeg와 AirPlay 실행 환경이 포함됩니다. Server가 자신을 업데이트하도록 Docker 소켓을 연결하거나 호스트 관리 권한을 부여하지 마세요. 네이티브 Windows는 [별도 무설치 업데이트 절차](#windows-server)를 따르고 아래 Compose 절차를 적용하지 마세요.

### 업데이트 절차

모든 작업에 **기존** Compose 프로젝트 이름·프로젝트 폴더·Compose 파일·환경 파일을 사용하세요. 기본 저장 경로를 사용하는 두 번째 설치를 새로 만들면 안 됩니다.

1. **대상 버전 확인:** 변경 사항, 설정·데이터베이스 호환성, 중간 버전을 거쳐야 하는지 확인합니다. 현재 이미지 식별자, 아키텍처, 접속 URL, 마운트와 영구 설정을 기록하고, 변경 전에 백업 공간과 롤백 계획을 확인합니다.
2. **중단 전에 다운로드:** 선택한 공개 릴리즈의 정확한 대상 다이제스트를 내려받습니다. 공개 이미지는 로그인이 필요 없으며, 명시적으로 선택한 비공개 레지스트리에만 기존 Docker 인증이나 비공개 대화형 인증을 사용하세요. 토큰을 대화나 설정 파일에 넣지 마세요. 멀티아키텍처 이미지에서는 호스트에 맞는 아키텍처가 자동 선택되며, `amd64` 또는 `arm64`인지 확인합니다. 오프라인 패키지는 [릴리즈 확보와 검증](#releases)의 검증·가져오기 절차를 따르세요. 가변 `latest` 태그를 사용하거나 레지스트리 주소를 지어내거나 이전 이미지를 삭제하지 마세요.
3. **중단 시점 합의:** 재생을 정지하고 정지 상태를 확인합니다. 상태를 백업하기 전에 기존 Compose 프로젝트의 `jastreamer-server`만 멈춥니다. 다른 서비스를 중지하거나 볼륨을 제거하거나 `down -v`를 사용하지 마세요.
4. **일관된 백업:** Server가 멈춘 상태에서 config와 data 폴더 전체, Compose 파일과 환경 파일을 백업하고 해당 시점의 이전 이미지 식별자를 기록합니다. 백업을 읽을 수 있고 필요한 파일이 포함되었는지 확인하세요. 계정·세션·AirPlay·Google Cast 활성화 설정 및 TLS 설정이나 자격 증명이 포함될 수 있으므로 비공개 데이터로 보호해야 합니다. 읽기 전용 원본 음악은 앱 상태가 아니며 덮어쓰거나 수정하면 안 됩니다.
5. **저장된 이미지 참조 변경:** 영구 배포 설정의 `JASTREAMER_SERVER_IMAGE`를 검증된 대상 다이제스트 또는 가져온 로컬 이미지 ID로 바꿉니다. 릴리즈에서 명시적으로 요구하고 검토한 마이그레이션 외에는 기존 설정과 마운트를 보존하세요. `server.json`을 신규 설치 템플릿으로 교체하지 마세요. UID/GID `10001:10001`, host networking, 읽기 전용 rootfs·music, 쓰기 가능한 config·data와 나머지 보안 제한을 유지합니다. 시작 전에 `docker compose config`로 변경 구성을 확인하고 대상 이미지의 `--check-config` 명령으로 기존 설정을 검증합니다.
6. **서버만 재생성:** 같은 프로젝트에서 `up -d --no-deps jastreamer-server`를 실행합니다. 실제 실행 이미지가 선택한 다이제스트·아키텍처와 일치하는지, 컨테이너 상태와 로그, `/healthz`, 클라이언트 LAN에서 Web 접속을 확인하세요. 컨테이너가 생성됐다는 사실만으로 완료라고 판단하지 마세요.
7. **접속과 테스트:** 기존에 사용하던 실제 HTTP(S) URL을 포트까지 포함해 안내합니다. 브라우저나 Windows 앱의 Web 화면을 새로고침하고 로그인, 보관함·앨범 아트, 대기열 순서, 플레이리스트, 설정과 출력 검색이 업데이트 전과 같은지 확인하세요. 사용자가 명시적으로 시작하기 전까지 재생은 정지 상태여야 합니다. 원하는 곡을 직접 재생해 소리를 확인하고, 지원되는 일시 정지·탐색을 시험한 뒤 정지하도록 안내하세요. 실패하면 비밀정보 없이 어느 단계에서 어떤 오류가 났는지 알려 달라고 합니다.

서버가 제공하는 Web 화면과 휴대전화·PWA UI를 갱신하는 데 데스크톱 패키지 교체가 필요한 것은 아닙니다. 앱 실행 파일도 변경된 릴리즈라면 [Windows 데스크톱 앱](#desktop-windows) 또는 [Linux 데스크톱 앱](#desktop-linux)의 별도 절차를 따르고, Windows는 EXE 옆 `user-data`, Linux는 사용자별 프로필을 보존하세요.

<a id="rollback"></a>
## 9. 롤백

시작이나 검증이 실패하면 새 서버를 멈추고 로그·상태를 보존한 뒤 승인된 롤백 계획을 따르세요. 이전 이미지와 **그에 대응하는 config/data 및 배포 설정 백업을 함께** 복구해야 합니다. 데이터베이스 마이그레이션 이후에는 이미지 태그만 되돌려도 정상 동작하지 않을 수 있습니다. 백업 이후의 변경이 사라질 수 있으므로 복구 전에 영향을 확인하세요. 원래 접속 주소와 상태 보존을 다시 검증하고 재생은 자동으로 시작하지 마세요. 업데이트나 롤백 중 원본 음악을 삭제하거나 변경하면 안 됩니다.

네이티브 Windows Server는 중지 상태에서 만든 일치하는 전체 설치 폴더 백업과 이전 검증 패키지를 함께 복구하세요. Desktop만 롤백할 때는 이전 검증 패키지를 복구하되 그 패키지에 맞는 Windows `user-data` 또는 Linux 사용자별 프로필을 보존하세요. 원래 상태를 복구한 뒤 [한국어 사용자 안내서](INSTRUCTION.ko.md)로 돌아가 작동과 문제 해결을 확인하세요.

에이전트에게 설치나 업데이트를 맡기려면 저장소 루트의 [AGENTS.md](AGENTS.md)를 먼저 읽고, 이 문서에서 실제 대상에 맞는 설치·업데이트 분기를 선택하게 하세요. 비밀번호, 개인 키, 레지스트리 토큰이나 jastreamer 자격 증명을 프롬프트·생성 파일·Git·로그에 넣지 말고 기존의 안전한 인증 수단 또는 비공개 대화형 입력을 사용하세요.
