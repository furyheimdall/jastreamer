# jastreamer 0.2 사용자 안내서

[English user guide](INSTRUCTION.md) · [프로젝트 소개](README.ko.md)

jastreamer는 Web 화면을 제공하고 음악을 UPnP/DLNA 또는 AirPlay 출력으로 보내는 Linux 서버 컨테이너 하나로 실행됩니다. 선택 사항인 Windows·Linux 데스크톱 앱은 이 서버에 접속만 합니다.

## 1. 요구 사항과 안전 주의

- Docker Engine과 Compose v2가 있는 Linux `amd64`·`arm64` 또는 Container Manager가 있는 Synology DSM. `arm/v7`은 지원하지 않으며 DS918+는 `amd64`입니다.
- 게시된 jastreamer 0.2 프리뷰의 정확한 이미지 다이제스트 또는 별도로 전달받아 검증한 오프라인 패키지. 프리뷰 및 실장비 검증 한계는 그대로 적용됩니다.
- 서버, 브라우저·앱과 출력이 연결된 신뢰할 수 있는 사설 LAN. 자동 검색에는 멀티캐스트가 필요합니다.
- 컨테이너 UID/GID `10001:10001`이 쓸 수 있는 분리된 config·data 폴더.
- UID 10001이 읽을 수 있고 읽기 전용으로 연결된 기존 음악 폴더.

`chmod 777`을 사용하거나 음악 보관함 전체의 소유권을 변경하지 마세요. HTTP 8080은 자격 증명과 오디오를 암호화하지 않습니다. LAN 경로 전체를 신뢰할 수 없다면 직접 준비한 PEM 인증서·키로 내장 HTTPS를 사용하세요. 서버를 공용 인터넷에 직접 노출하지 마세요.

## 2. 릴리즈 확보와 검증

### 공개 레지스트리 이미지

[GitHub Releases](https://github.com/furyheimdall/jastreamer/releases)에서 게시된 프리뷰를 선택하고, 검증 한계를 읽은 뒤 릴리즈 설명·manifest의 정확한 서버 이미지 주소를 확인하세요. 프리릴리즈는 최신 production 릴리즈로 표시하지 않으므로 `/releases/latest`로 프리뷰를 자동 선택하지 마세요.

레지스트리는 `ghcr.io/furyheimdall/jastreamer-server`이며 공개 이미지는 GitHub 토큰 없이 내려받을 수 있습니다. 아래 예시의 다이제스트를 선택한 릴리즈의 실제 전체 값으로 바꾸고, 태그를 추측하지 마세요.

```sh
export JASTREAMER_SERVER_IMAGE='ghcr.io/furyheimdall/jastreamer-server@sha256:<digest-from-release>'
docker pull "$JASTREAMER_SERVER_IMAGE"
docker image inspect --format '{{.Os}}/{{.Architecture}} {{.Id}}' "$JASTREAMER_SERVER_IMAGE"
```

멀티아키텍처 이미지에서 호스트에 맞는 `amd64` 또는 `arm64`가 선택됩니다. 정확한 다이제스트를 영구 배포 환경 설정에 보관하고, 호스트에 FFmpeg나 Python을 따로 설치하지 마세요. 데스크톱 파일과 체크섬도 같은 릴리즈에서 받으세요.

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

## 3. Compose로 설치

Linux와 Synology 모두 저장소의 `deploy/docker/server/compose.synology.yaml`을 사용합니다. 이 파일은 호스트 네트워크, 읽기 전용 컨테이너 파일 시스템, 임시 `/tmp`와 필요한 UID/GID를 적용합니다.

서버에 맞는 경로를 선택하세요.

| 설치 대상 | 설정 | 데이터 | 음악 |
|---|---|---|---|
| Linux 예시 | `/srv/jastreamer/config` | `/srv/jastreamer/data` | `/srv/music` |
| Synology 예시 | `/volume1/docker/jastreamer/config` | `/volume1/docker/jastreamer/data` | `/volume1/music` |

서버에 맞는 세 경로를 정하고 제공된 설정 파일을 설치합니다.

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

Synology Container Manager에서는 같은 Compose 파일과 네 환경 변수를 **프로젝트**에 입력할 수 있습니다. 상태 확인 주소는 `http://<NAS-LAN-IP>:8080/healthz`입니다. HTTPS를 켰다면 8443 포트를 사용하세요.

## 4. 최초 설정과 일상 사용

1. `http://<server-LAN-IP>:8080/`을 열고 최초 관리자 계정을 만듭니다. 비밀번호는 10자 이상이어야 합니다.
2. 기본 언어는 영어입니다. **Settings** 메뉴의 **Language / 언어**에서 **English** 또는 **한국어**를 선택하세요. 메뉴 이름은 두 언어 모두 **Settings**로 유지합니다. 언어 선택은 즉시 적용·저장되며 서버 설정 저장이나 재생 명령을 보내지 않습니다.
3. **Settings**에서 음악 폴더 경로가 `/music`인지 확인해 저장하고 **지금 스캔**을 선택하세요. FLAC, MP3, WAV/WAVE, Ogg/Vorbis, Opus, M4A를 원본 수정 없이 스캔하며 심볼릭 링크는 건너뜁니다.
4. 보관함을 탐색·검색해 곡이나 묶음 화면을 대기열에 추가하고, 재생이 정지된 상태에서 출력을 선택합니다. 방금 켠 수신기가 없으면 출력 목록을 새로 고치세요.
5. 수신기가 지원할 때 하단 플레이 바에서 재생/일시 정지, 정지, 이전, 다음과 탐색을 사용합니다. AirPlay가 PIN·암호를 요구하면 정지 상태에서 페어링합니다.

대기열은 서버 전체에서 공유되며 순서와 중복을 보존하고 재시작 뒤에도 남습니다. 서버 재시작 뒤 재생은 자동으로 이어지지 않습니다.

### 앨범 아트와 대기열 동작

- 하단 플레이 바의 앨범 아트를 누르면 **대기열**로 이동합니다. 곡 정보를 열거나 재생을 시작하지 않습니다.
- 보관함 `(i)`와 대기열의 앨범 아트는 재생 없이 곡 정보를 엽니다. 대기열 앨범 아트에 마우스를 올리거나 키보드로 포커스하면 큰 `(i)`가 표시되며, 터치 화면에서는 항상 표시됩니다.
- 대기열 행 오른쪽 동작 버튼 중 맨 앞의 삼각형 재생 버튼만 해당 항목을 재생합니다.

재생 기기 상태 조회가 한 번 실패해도 재생을 다시 시작하거나 팝업을 띄우지 않습니다. 조회가 세 번 연속 실패하면 상태 경고를 표시하고, 다음 조회가 성공하면 자동으로 닫습니다. 경고를 직접 닫으면 같은 실패가 이어지는 동안 다시 띄우지 않습니다. 재생 명령 실패와 확인된 연결 끊김은 즉시 표시합니다.

UPnP/AirPlay 기능은 수신기마다 다릅니다. 실제 장비에서 소리와 필요한 제어 기능을 확인하세요.

## 5. 선택 사항인 데스크톱 앱

### Windows x64 무설치 ZIP

Windows 10/11 x64에서 선택한 프리뷰의 미서명 `jastreamer-desktop_0.2.0_windows-x64.zip`을 사용합니다. 릴리즈 체크섬과 비교하세요.

```powershell
Get-FileHash .\jastreamer-desktop_0.2.0_windows-x64.zip -Algorithm SHA256
```

ZIP 전체를 쓰기 가능한 새 로컬 폴더에 풀고 `jastreamer-desktop.exe`를 실행하세요. ZIP 안에서 실행하거나 EXE만 복사하지 마세요. 발견된 서버를 선택하거나 완전한 HTTP(S) 주소를 입력합니다. 앱은 접속 전에 서버를 확인하며 검색·접속은 재생을 시작하지 않습니다.

서버 검색은 활성 IPv4 네트워크 어댑터마다 5초 간격으로 수행하며 어댑터 변경도 반영합니다. 방화벽이나 멀티캐스트 제한이 있으면 서버 주소를 직접 입력해야 할 수 있습니다.

최근 서버, 언어, 쿠키와 로그인 상태는 EXE 옆 `user-data`에 저장됩니다. 앱을 닫아도 서버 재생은 멈추지 않습니다. 업데이트할 때 완전히 종료하고 기존 폴더를 백업한 뒤 새 ZIP을 새 폴더에 풀고 기존 `user-data`를 새 EXE 옆에 보존하세요. 다른 Windows 계정이나 PC에서는 다시 로그인해야 할 수 있습니다.
### Linux amd64 DEB

그래픽 환경이 있는 Linux amd64에서 `jastreamer-desktop_0.2.0_linux-amd64.deb`을 사용합니다. 네이티브 설치·sandbox 검증 대상은 Ubuntu 24.04 amd64입니다. arm64 데스크톱 패키지나 Linux 서버 패키지가 아닙니다.

선택한 릴리즈의 체크섬과 DEB를 비교하고, 의존성도 설치하도록 APT를 사용하세요.

```sh
sha256sum -c jastreamer-desktop_0.2.0_linux-amd64.deb.sha256
sudo apt install ./jastreamer-desktop_0.2.0_linux-amd64.deb
```

일반 사용자로 앱 메뉴의 **JASTREAMER**를 열거나 `/usr/lib/jastreamer-desktop/jastreamer-desktop`을 실행하세요. `sudo`로 앱을 실행하거나 `--no-sandbox`를 추가하지 마세요. 서버를 선택하거나 완전한 HTTP(S) URL을 입력하면 되며, 이 클라이언트에 FFmpeg나 오디오 플레이어를 따로 설치할 필요는 없습니다.

설치 파일은 root 소유이며, `chrome-sandbox`는 `root:root`, `4755` 권한으로 설치합니다. 호환되는 AppArmor 시스템에서는 `/usr/lib/jastreamer-desktop/jastreamer-desktop` 실행 파일에 한정된 사용자 네임스페이스 프로필을 설치합니다. AppArmor나 시스템 전체의 사용자 네임스페이스 제한을 끄지 않습니다. 기존 사용자 관리 정책은 보존하며 로컬 추가 규칙은 `/etc/apparmor.d/local/jastreamer-desktop`에 둡니다. 실행이 실패하면 sandbox를 약화하지 말고 오류와 설치 권한을 확인하세요.

최근 서버, 언어, 쿠키와 로그인 상태는 root 소유 설치 폴더가 아닌 `$XDG_CONFIG_HOME/jastreamer-desktop`, 보통 `~/.config/jastreamer-desktop`에 저장됩니다. 새 DEB를 설치하기 전에 완전히 종료하고 이 프로필을 백업하세요. 패키지 버전이 같은 프리뷰를 교체한다면 `sudo apt install --reinstall ./jastreamer-desktop_0.2.0_linux-amd64.deb`을 사용합니다. 롤백용 이전 검증 DEB를 보관하고, 업데이트를 위해 프로필을 삭제하지 마세요.


## 6. 백업과 업데이트

현재 앱 내 새 버전 확인이나 자동 업데이트 기능은 없습니다. 업데이트는 최초 계정 생성을 다시 하는 것이 아니라, 검증된 이미지로 기존 서버 컨테이너를 교체하는 작업입니다. 이미지에는 해당 아키텍처의 Web 화면, FFmpeg와 AirPlay 실행 환경이 포함됩니다. 서버가 자신을 업데이트하도록 Docker 소켓을 연결하거나 호스트 관리 권한을 부여하지 마세요.

### 업데이트 절차

모든 작업에 **기존** Compose 프로젝트 이름·프로젝트 폴더·Compose 파일·환경 파일을 사용하세요. 기본 저장 경로를 사용하는 두 번째 설치를 새로 만들면 안 됩니다.

1. **대상 버전 확인:** 변경 사항, 설정·데이터베이스 호환성, 중간 버전을 거쳐야 하는지 확인합니다. 현재 이미지 식별자, 아키텍처, 접속 URL, 마운트와 영구 설정을 기록하고, 변경 전에 백업 공간과 롤백 계획을 확인합니다.
2. **중단 전에 다운로드:** 선택한 공개 릴리즈의 정확한 대상 다이제스트를 내려받습니다. 공개 이미지는 로그인이 필요 없으며, 명시적으로 선택한 비공개 레지스트리에만 기존 Docker 인증이나 비공개 대화형 인증을 사용하세요. 토큰을 대화나 설정 파일에 넣지 마세요. 멀티아키텍처 이미지에서는 호스트에 맞는 아키텍처가 자동 선택되며, `amd64` 또는 `arm64`인지 확인합니다. 오프라인 패키지는 2절의 검증·가져오기 절차를 따르세요. 가변 `latest` 태그를 사용하거나 레지스트리 주소를 지어내거나 이전 이미지를 삭제하지 마세요.
3. **중단 시점 합의:** 재생을 정지하고 정지 상태를 확인합니다. 상태를 백업하기 전에 기존 Compose 프로젝트의 `jastreamer-server`만 멈춥니다. 다른 서비스를 중지하거나 볼륨을 제거하거나 `down -v`를 사용하지 마세요.
4. **일관된 백업:** 서버가 멈춘 상태에서 config와 data 폴더 전체, Compose 파일과 환경 파일을 백업하고 해당 시점의 이전 이미지 식별자를 기록합니다. 백업을 읽을 수 있고 필요한 파일이 포함되었는지 확인하세요. 계정·세션·AirPlay 정보와 TLS 자격 증명이 포함될 수 있으므로 비공개 데이터로 보호해야 합니다. 읽기 전용 원본 음악은 앱 상태가 아니며 덮어쓰거나 수정하면 안 됩니다.
5. **저장된 이미지 참조 변경:** 영구 배포 설정의 `JASTREAMER_SERVER_IMAGE`를 검증된 대상 다이제스트 또는 가져온 로컬 이미지 ID로 바꿉니다. 릴리즈에서 명시적으로 요구하고 검토한 마이그레이션 외에는 기존 설정과 마운트를 보존하세요. `server.json`을 신규 설치 템플릿으로 교체하지 마세요. UID/GID `10001:10001`, host networking, 읽기 전용 rootfs·music, 쓰기 가능한 config·data와 나머지 보안 제한을 유지합니다. 시작 전에 `docker compose config`로 변경 구성을 확인하고 대상 이미지의 `--check-config` 명령으로 기존 설정을 검증합니다.
6. **서버만 재생성:** 같은 프로젝트에서 `up -d --no-deps jastreamer-server`를 실행합니다. 실제 실행 이미지가 선택한 다이제스트·아키텍처와 일치하는지, 컨테이너 상태와 로그, `/healthz`, 클라이언트 LAN에서 Web 접속을 확인하세요. 컨테이너가 생성됐다는 사실만으로 완료라고 판단하지 마세요.
7. **접속과 테스트:** 기존에 사용하던 실제 HTTP(S) URL을 포트까지 포함해 안내합니다. 브라우저나 Windows 앱의 Web 화면을 새로고침하고 로그인, 보관함·앨범 아트, 대기열 순서, 플레이리스트, 설정과 출력 검색이 업데이트 전과 같은지 확인하세요. 사용자가 명시적으로 시작하기 전까지 재생은 정지 상태여야 합니다. 원하는 곡을 직접 재생해 소리를 확인하고, 지원되는 일시 정지·탐색을 시험한 뒤 정지하도록 안내하세요. 실패하면 비밀정보 없이 어느 단계에서 어떤 오류가 났는지 알려 달라고 합니다.

서버가 제공하는 Web 화면을 갱신하는 데 데스크톱 패키지 교체가 필요한 것은 아닙니다. 앱 실행 파일도 변경된 릴리즈라면 5절의 별도 절차를 따르고, Windows는 EXE 옆 `user-data`, Linux는 사용자별 프로필을 보존하세요.

### 롤백

시작이나 검증이 실패하면 새 서버를 멈추고 로그·상태를 보존한 뒤 승인된 롤백 계획을 따르세요. 이전 이미지와 **그에 대응하는 config/data 및 배포 설정 백업을 함께** 복구해야 합니다. 데이터베이스 마이그레이션 이후에는 이미지 태그만 되돌려도 정상 동작하지 않을 수 있습니다. 백업 이후의 변경이 사라질 수 있으므로 복구 전에 영향을 확인하세요. 원래 접속 주소와 상태 보존을 다시 검증하고 재생은 자동으로 시작하지 마세요. 업데이트나 롤백 중 원본 음악을 삭제하거나 변경하면 안 됩니다.

### 에이전트와 업데이트하기

서버에 허가된 방식으로 접근할 수 있는 에이전트에게 아래 프롬프트를 복사해 전달하세요. 현재 서버 URL과 접속 방법부터 알려주면, 사용자가 설정 파일을 다시 작성하는 대신 에이전트가 기존 배포 구성을 조사하도록 되어 있습니다. 비밀번호와 개인 키는 프롬프트가 아닌 비공개 인증 수단으로 처리하세요.

<details>
<summary>업데이트 프롬프트 펼쳐서 복사하기</summary>

```text
기존 데이터를 보존하면서 내 jastreamer 서버를 업데이트하도록 도와줘.
일치하는 저장소 리비전의 README와 INSTRUCTION.ko.md, 특히 백업과
업데이트 절차를 먼저 읽어줘. 신규 설치가 아니라 기존 서버 업데이트야.

모르는 경우 현재 서버 URL과 허용된 SSH 접속 방법을 물어봐.
실제 설치를 조사해 Compose 프로젝트·파일·영구 환경 설정,
이미지·다이제스트, 아키텍처, 마운트, 설정과 재생 상태를 확인해.
예시 경로를 실제 설치 경로로 가정하지 마.
부족한 정보만 묻고 어떤 배포 버전으로 업데이트할지 확인해.
변경 사항을 설명하고 호환되는 검증된 버전을 추천해줘.
게시된 이미지를 지어내거나 latest를 쓰거나 다른 빌드로 대체하지 마.

현재·대상 버전, 예상 중단, 보존할 상태, 백업 위치, 검증과 롤백
계획을 보여주고 승인받아. 문서화된 마이그레이션을 승인한 경우가
아니면 기존 URL·설정·계정·보관함·앨범 아트·대기열 순서·
플레이리스트·AirPlay 상태를 유지해.
중단 전에 해당 아키텍처의 FFmpeg·AirPlay 실행 환경을 포함한
전체 이미지를 내려받아 검증해. 공개 GHCR 이미지는 로그인이 필요 없어.
명시적으로 선택한 비공개 레지스트리에만 안전한 인증을 사용하거나
문서의 오프라인 가져오기를 따르고 비밀정보를 대화·로그에 기록하지 마.

승인 후 재생 정지를 확인하고 이 서버만 멈춰.
서비스가 정지한 상태에서 config/data 전체와 배포 설정을 백업하고
백업을 확인한 뒤 이전 이미지를 보관해. 실행 중인 SQLite 파일만
복사하거나 볼륨 제거·계정 초기화·음악 수정·음악 폴더의 재귀적
소유권 변경·컨테이너 보안 완화를 하지 마.
필요한 영구 배포 설정만 바꾸고 Compose와 대상 이미지의
--check-config로 준비한 설정을 검증한 뒤, 같은 프로젝트·마운트로
해당 서비스만 재생성해. 실제 설정 파일과 명령은 직접 작성하고
자리표시자를 남기거나 임시 셸의 export에만 의존하지 마.

실행 이미지, 로그, /healthz, 클라이언트 LAN의 Web 접속과 기존
상태 보존을 검증해. 자동으로 출력을 페어링하거나 재생을 시작하거나,
실패하는 컨테이너를 계속 교체하지 마.
검증이 실패하면 근거를 보존하고 승인된 롤백 계획만 수행해.
백업 이후 데이터가 사라질 수 있음을 설명하고, 막힌 단계가 있으면
성공했다고 하지 말고 정확히 보고해.

마지막에는 실제로 클릭할 수 있는 서버 URL을 주고, 그 주소를 열어
Web 화면을 새로고침하고 로그인·보관함·대기열·플레이리스트·출력을
확인하도록 안내해. 내가 원하는 곡을 직접 재생해 소리를 듣고,
지원되는 일시 정지·탐색을 시험한 뒤 정지하도록 제안해.
각 단계의 기대 결과와, 문제 발생 시 실패한 단계·비밀정보를 제거한
오류 문구를 알려 달라는 안내를 포함해.
에이전트가 확인한 결과와 사용자가 직접 할 청취 검증을 구분하고,
최종 버전·다이제스트, 백업 위치와 롤백 방법을 정리해줘.
데스크톱 패키지는 별도이므로 그것도 업데이트할 때는 Windows의
user-data 또는 Linux의 사용자별 프로필을 보존해.
서버의 Web 화면만 갱신하려고 데스크톱 앱을 교체하지 마.
```

</details>

## 7. 문제 해결

| 문제 | 확인할 항목 |
|---|---|
| 웹 화면이 열리지 않음 | `/healthz`, Compose 로그, 수신 주소 설정, TCP 8080/8443 방화벽 |
| Windows에서 서버를 찾지 못함 | mDNS UDP 5353을 허용하거나 완전한 서버 URL 직접 입력 |
| 출력이 보이지 않음 | 호스트 네트워크 유지, UPnP용 SSDP UDP 1900, AirPlay용 mDNS UDP 5353, Wi-Fi 기기 격리 해제 |
| 출력이 재생하지 못함 | 서버→수신기 제어·스트림과 수신기→서버 미디어 통신 허용; 필요한 경우에만 수신기가 접근할 주소로 `media.base_url` 지정 |
| 보관함이 비어 있음 | 음악 폴더가 `/music`에 연결되었는지, UID 10001 읽기·탐색 권한과 스캔 완료 여부 |
| 설정 저장 실패 | config 폴더와 `server.json`을 UID/GID 10001이 쓸 수 있는지 |
| AirPlay 인증 실패 | 재생 정지, 표시된 PIN·암호 재입력, 패키지의 helper·FFmpeg 경로 유지 |
| 비밀번호 분실 | 서버를 멈추고 같은 config/data를 연결한 유지보수 컨테이너에서 `jastreamer-server --reset-password USER --config /etc/jastreamer/server.json` 실행; 새 비밀번호는 입력 프롬프트에만 입력 |

문제를 보고할 때 서버 버전, 정확한 이미지 다이제스트, 서버 아키텍처, 관련 로그와 수신기 모델을 포함하세요. 비밀번호, 쿠키, 인증서와 개인 키는 제거하고 원본 진단 문구는 그대로 보존하세요.
