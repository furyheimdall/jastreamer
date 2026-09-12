# jastreamer 0.2 사용자 안내서

[English user guide](INSTRUCTION.md) · [프로젝트 소개](README.ko.md)

jastreamer는 웹 화면을 제공하고 음악을 UPnP/DLNA 또는 AirPlay 출력으로 보내는 Linux 서버 컨테이너 하나로 실행됩니다. 선택 사항인 Windows 앱은 이 서버에 접속만 합니다.

## 1. 요구 사항과 안전 주의

- Docker Engine과 Compose v2가 있는 Linux `amd64`·`arm64` 또는 Container Manager가 있는 Synology DSM. `arm/v7`은 지원하지 않으며 DS918+는 `amd64`입니다.
- 검증된 비공개 jastreamer 0.2 이미지 또는 전달받은 배포 패키지. 공개 이미지나 다운로드는 없습니다.
- 서버, 브라우저·앱과 출력이 연결된 신뢰할 수 있는 사설 LAN. 자동 검색에는 멀티캐스트가 필요합니다.
- 컨테이너 UID/GID `10001:10001`이 쓸 수 있는 분리된 config·data 폴더.
- UID 10001이 읽을 수 있고 읽기 전용으로 연결된 기존 음악 폴더.

`chmod 777`을 사용하거나 음악 보관함 전체의 소유권을 변경하지 마세요. HTTP 8080은 자격 증명과 오디오를 암호화하지 않습니다. LAN 경로 전체를 신뢰할 수 없다면 직접 준비한 PEM 인증서·키로 내장 HTTPS를 사용하세요. 서버를 공용 인터넷에 직접 노출하지 마세요.

## 2. 비공개 패키지 확인

전달받은 배포 폴더에서 다음을 실행합니다.

```sh
cd /path/to/supplied/release
sha256sum -c SHA256SUMS
```

승인된 비공개 레지스트리 주소를 받았다면 `registry.example/name@sha256:<verified-digest>`처럼 정확한 다이제스트가 포함된 주소를 `JASTREAMER_SERVER_IMAGE`로 사용하세요.

제공된 `.oci`에는 여러 아키텍처가 포함되어 있으므로 `docker load`에 직접 넣을 수 없습니다. 오프라인 Docker TAR가 필요하면 Skopeo가 있는 Linux 컴퓨터에서 대상 아키텍처 하나만 변환합니다.

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

## 5. 선택 사항인 Windows 무설치 앱

Windows 10/11 x64에서 전달받은 비공개·미서명 `jastreamer-desktop_0.2.0_windows-x64.zip`을 사용합니다. 제공된 `.sha256` 값과 비교하세요.

```powershell
Get-FileHash .\jastreamer-desktop_0.2.0_windows-x64.zip -Algorithm SHA256
```

ZIP 전체를 쓰기 가능한 새 로컬 폴더에 풀고 `jastreamer-desktop.exe`를 실행하세요. ZIP 안에서 실행하거나 EXE만 복사하지 마세요. 발견된 서버를 선택하거나 완전한 HTTP(S) 주소를 입력합니다. 앱은 접속 전에 서버를 확인하며 검색·접속은 재생을 시작하지 않습니다.

서버 검색은 활성 IPv4 네트워크 어댑터마다 5초 간격으로 수행하며 어댑터 변경도 반영합니다. 방화벽이나 멀티캐스트 제한이 있으면 서버 주소를 직접 입력해야 할 수 있습니다.

최근 서버, 언어, 쿠키와 로그인 상태는 EXE 옆 `user-data`에 저장됩니다. 앱을 닫아도 서버 재생은 멈추지 않습니다. 업데이트할 때 완전히 종료하고 기존 폴더를 백업한 뒤 새 ZIP을 새 폴더에 풀고 기존 `user-data`를 새 EXE 옆에 보존하세요. 다른 Windows 계정이나 PC에서는 다시 로그인해야 할 수 있습니다.

## 6. 백업과 업데이트

자동 업데이트는 없습니다.

1. 재생을 정지하고 플레이어가 정지 상태인지 확인합니다.
2. `docker compose -f deploy/docker/server/compose.synology.yaml down`을 실행합니다.
3. 서버가 멈춘 상태에서 config 폴더 전체(PEM 포함)와 data 폴더 전체를 백업합니다.
4. 새 비공개 이미지를 확인·가져오고 `JASTREAMER_SERVER_IMAGE`를 정확한 다이제스트 또는 로컬 이미지 ID로 바꿉니다.
5. 같은 config, data와 읽기 전용 음악 경로를 유지하고 `docker compose ... config`를 검토한 뒤 `up -d`를 실행합니다.
6. 로그인, 보관함, 앨범 아트, 대기열 보존, 출력 검색과 실제 재생을 확인합니다.

이전 버전으로 되돌릴 때는 이전 이미지와 그 시점의 config/data 백업을 함께 복구하세요. 업데이트나 초기화 중 `/music`, `/srv/music`, `/volume1/music` 또는 다른 원본 음악 폴더를 절대 삭제하지 마세요.

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
