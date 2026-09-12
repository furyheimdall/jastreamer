# jastreamer 0.2.0 설치·운영 안내

이 문서는 현재 소스 트리의 Server, Web, 선택적 Windows 데스크톱을 빌드하고 운영하는 방법을 설명한다. jastreamer의 필수 실행 단위는 React Web을 포함한 Go Server 하나다. 재생 대상은 Server가 LAN에서 찾은 UPnP/DLNA 또는 AirPlay 출력이며, 데스크톱 앱은 Server를 찾고 같은 Web 화면을 여는 선택적 접속 셸이다.

## 1. 운영 모델과 안전 경계

필요한 경로는 세 종류다.

- **config:** `server.json`과 선택적 HTTPS PEM 파일을 두는 쓰기 가능한 디렉터리
- **data:** `server.sqlite`, artwork cache, AirPlay 상태를 두는 쓰기 가능한 디렉터리
- **music:** 관리자가 승인한 기존 음악 디렉터리. 읽기 전용 마운트를 권장한다.

Server 중지, 제거, 초기화 또는 rollback 때 music을 삭제·이동하거나 재귀적으로 `chown`/`chmod`하지 않는다. config와 data만 jastreamer 애플리케이션 상태다.

기본 HTTP는 사설 LAN 전용이다. 비밀번호, session cookie, metadata, 제어 명령과 음악 byte가 암호화되지 않으므로 경로상의 장비와 사용자를 모두 신뢰할 수 없으면 내장 HTTPS를 사용한다. HTTP/HTTPS 포트를 라우터에서 전달하거나 Server를 공용 인터넷에 직접 노출하지 않는다.

## 2. Server 명령

설정 경로의 우선순위는 `--config`, `JASTREAMER_CONFIG`, `/etc/jastreamer/server.json` 순서다.

```sh
jastreamer-server --version
jastreamer-server --init-config /absolute/path/server.json
jastreamer-server --check-config /absolute/path/server.json
jastreamer-server --config /absolute/path/server.json
jastreamer-server --reset-password USER --config /absolute/path/server.json
```

- 인수 없이 실행하면 기본 설정 경로를 사용한다.
- `--init-config`는 부모 디렉터리를 만들고 mode `0600`으로 기본 설정을 생성하지만 기존 파일을 덮어쓰지 않는다.
- 설정 파일이 없는 상태에서 일반 실행하면 같은 기본 설정을 한 번 생성한 뒤 시작한다.
- `--check-config`는 Server를 시작하지 않고 JSON 구조와 설정 제약을 검사한다.
- 유지보수 명령은 한 번에 하나만 지정할 수 있다.
- `--reset-password`는 새 비밀번호를 TTY 또는 표준 입력에서 받고 해당 계정의 모든 session을 폐기한다. 비밀번호를 명령행 인수나 환경 변수에 넣지 않는다.

## 3. 정확한 설정 schema와 기본값

`--init-config`가 생성하는 현재 기본값은 다음과 같다.

```json
{
  "version": 1,
  "data_dir": "/var/lib/jastreamer",
  "server_name": "",
  "http": {
    "enabled": true,
    "address": ":8080"
  },
  "https": {
    "enabled": false,
    "address": ":8443",
    "certificate_file": "",
    "private_key_file": ""
  },
  "library_roots": [],
  "network": {
    "interfaces": [],
    "discovery_interval_seconds": 30,
    "poll_interval_seconds": 1,
    "allowed_cidrs": []
  },
  "media": {
    "base_url": "",
    "ffmpeg_path": "",
    "transcode": false
  },
  "airplay": {
    "enabled": false,
    "helper_path": ""
  }
}
```

JSON은 1 MiB 이하여야 하고 정확히 객체 하나만 포함해야 하며 알 수 없는 field는 거부된다. 모든 field를 유지한다.

| field | 의미와 제약 | 기본값 |
|---|---|---|
| `version` | schema version. 정확히 `1` | `1` |
| `data_dir` | DB와 cache용 clean absolute path. filesystem root 금지 | `/var/lib/jastreamer` |
| `server_name` | 비우면 OS hostname. 지정 시 공백으로 둘러싸이지 않은 제어문자 없는 1–64자 | `""` |
| `http.enabled` / `address` | HTTP listener. 주소는 numeric port와 unspecified/private/loopback IP 또는 `localhost` 사용 | `true`, `:8080` |
| `https.enabled` / `address` | 내장 TLS listener | `false`, `:8443` |
| `https.certificate_file` / `private_key_file` | 함께 지정하는 clean absolute PEM 경로. HTTPS를 켜면 유효한 key pair 필수 | 빈 문자열 |
| `library_roots` | 최대 64개. 각 항목은 `id`, `name`, `path`를 가짐 | `[]` |
| `library_roots[].id` | 고유한 1–64 byte 영문자·숫자·`-`·`_` | 없음 |
| `library_roots[].name` | 공백으로 둘러싸이지 않은 제어문자 없는 1–128자 표시 이름 | 없음 |
| `library_roots[].path` | 고유한 clean absolute path. filesystem root 금지 | 없음 |
| `network.interfaces` | 정확한 interface 이름 목록, 최대 64개. 빈 배열이면 적합한 interface를 자동 선택 | `[]` |
| `network.discovery_interval_seconds` | UPnP/AirPlay 출력 재검색 간격, 5–3600초 | `30` |
| `network.poll_interval_seconds` | 선택 출력 상태 조회 간격, 1–300초 | `1` |
| `network.allowed_cidrs` | 최대 64개의 고유하고 canonical한 private/loopback CIDR. 빈 배열이어도 public peer는 거부 | `[]` |
| `media.base_url` | 출력이 media를 가져올 명시적 HTTP(S) origin. credential/path/query/fragment 금지. 비우면 출력까지의 local route와 listener에서 계산 | `""` |
| `media.ffmpeg_path` | FFmpeg executable의 clean absolute path | `""` |
| `media.transcode` | UPnP 출력이 원본 형식을 받지 못할 때 L16 변환 허용. 켜면 FFmpeg 경로 필수 | `false` |
| `airplay.enabled` | AirPlay 출력 backend 사용. Linux amd64/arm64에서만 지원 | `false` |
| `airplay.helper_path` | pyatv sender helper executable의 clean absolute path. AirPlay를 켜면 FFmpeg와 함께 필수 | `""` |

HTTP와 HTTPS를 동시에 끌 수 없다. listener port는 1–65535여야 한다. `allowed_cidrs`에는 `10/8`, `172.16/12`, `192.168/16`, `127/8`, `fc00::/7`, `::1/128` 안의 canonical prefix만 넣을 수 있다. 예를 들어 단일 IPv4 LAN만 허용하려면 `192.168.10.0/24`처럼 network address로 적는다.

Web 설정 화면은 revision을 확인한 뒤 설정 파일을 mode `0600`의 원자적 교체로 저장한다. 따라서 config **파일뿐 아니라 부모 디렉터리도** Server UID가 쓸 수 있어야 한다. `library_roots` 변경은 실행 중 반영되며 그 밖의 변경은 응답의 `restart_required`에 따라 재시작해야 한다.

### 컨테이너용 제공 설정

`packaging/server/server.json`은 코드 기본값과 다른 배포 예시다. `/music` root 하나를 추가하고 다음을 켠다.

```json
"media": {
  "base_url": "",
  "ffmpeg_path": "/usr/local/bin/ffmpeg",
  "transcode": false
},
"airplay": {
  "enabled": true,
  "helper_path": "/usr/local/bin/jastreamer-airplay"
}
```

컨테이너 image에는 해당 FFmpeg, helper와 기본 `server.json`이 포함되어 있다. 운영 config 디렉터리를 bind mount하면 image의 기본 파일이 가려지므로 mount할 디렉터리에 운영 설정을 별도로 준비해야 한다. AirPlay가 필요 없으면 `airplay.enabled`를 `false`로 바꿔도 된다.

## 4. 계정, library와 재생

Server origin의 `/`을 처음 열면 최초 관리자 생성 화면이 나온다. 사용자 이름은 정규화 후 1–64자, 비밀번호는 최소 10자이면서 최대 1024 byte다. 최초 생성은 transaction으로 한 번만 성공한다. 이후에는 HttpOnly, SameSite=Strict, host-only cookie session을 사용하며 유효 기간은 7일이다. HTTPS 요청에서만 cookie에 Secure가 붙는다.

설정 화면에서 비밀번호를 바꾸면 모든 기존 session이 폐기된다. 비밀번호를 잃어버렸다면 Server를 안전하게 중지하고 다음처럼 표준 입력을 사용한다.

```sh
jastreamer-server --reset-password USER --config /absolute/path/server.json
```

이 작업은 계정 session만 폐기하며 library, playlist, queue, 선택 output과 AirPlay 자격 증명은 `server.sqlite`에 그대로 남긴다.

지원하는 library 확장자와 실제 확인 형식은 FLAC, MP3, WAV/WAVE, Ogg/Vorbis, Opus와 M4A다. symlink는 scan하지 않는다. scan은 원본을 수정하지 않고 metadata와 artwork cache를 data에 쓴다. root를 사용할 수 없거나 scan이 실패·취소되면 정상 완료로 위장하지 않는다.

하나의 Server에는 전역 queue 하나와 선택 output 하나가 있다. queue와 playlist는 중복 track과 순서를 보존한다. output 선택과 AirPlay 인증은 player가 정지했고 command가 진행 중이지 않을 때만 가능하다. queue 변경, Server 재시작, output 발견, 데스크톱 연결은 자동 재생을 일으키지 않는다. 재시작 후 재생은 명시적으로 다시 시작해야 한다.

## 5. 소스에서 native 실행

필수 도구는 `.tool-versions`와 동일한 Go 1.25.0, Node.js 22.20.0, npm이다.

```sh
make build
apps/server/dist/jastreamer-server --init-config "$PWD/server.json"
```

생성된 설정에서 `data_dir`와 `library_roots[].path`를 현재 사용자가 접근할 수 있는 absolute path로 바꾼다. 예:

```json
"data_dir": "/home/me/.local/share/jastreamer",
"library_roots": [
  { "id": "music", "name": "Music", "path": "/home/me/Music" }
]
```

그다음 검사하고 시작한다.

```sh
apps/server/dist/jastreamer-server --check-config "$PWD/server.json"
apps/server/dist/jastreamer-server --config "$PWD/server.json"
```

UPnP 원본 스트리밍만 쓸 때 Python이나 FFmpeg는 필요 없다. UPnP L16 변환에는 FFmpeg 8.1.2와 `media.transcode=true`가 필요하다. 이 구현은 seek 가능한 `fd:` 입력을 사용하므로 오래된 FFmpeg와의 호환을 가정하지 않는다. 동시에 처리할 수 있는 변환 stream은 두 개로 제한된다.

### native Linux AirPlay 의존성

AirPlay는 Linux amd64/arm64에서만 활성화된다. 배포 image는 hash로 고정한 pyatv 0.18.0과 Python 의존성, audio-only FFmpeg 8.1.2를 포함하며 각각 `/usr/local/bin/jastreamer-airplay`, `/usr/local/bin/ffmpeg`를 사용한다. `airplay-cli`, `cliairplay`, `libraop`은 사용하거나 포함하지 않는다.

native 개발에서는 Python 3.12 환경에 고정 dependency를 설치하고, repository helper를 실행하는 별도 executable wrapper를 만든다. 아래의 `ROOT`는 symlink가 아닌 absolute checkout path여야 한다.

```sh
ROOT="$PWD"
python3.12 -m venv "$ROOT/.venv-airplay"
"$ROOT/.venv-airplay/bin/pip" install --require-hashes \
  -r "$ROOT/packaging/server/requirements-airplay.txt"
cat > "$ROOT/.venv-airplay/bin/jastreamer-airplay" <<EOF
#!/bin/sh
exec "$ROOT/.venv-airplay/bin/python" -I \
  "$ROOT/apps/server/internal/airplay/helper.py" "\$@"
EOF
chmod 700 "$ROOT/.venv-airplay/bin/jastreamer-airplay"
```

`media.ffmpeg_path`에는 FFmpeg 8.1.2 executable의 absolute path를, `airplay.helper_path`에는 위 wrapper의 absolute path를 넣는다. 배포 image와 같은 FFmpeg를 직접 만들 때는 FFmpeg 8.1.2 source와 `packaging/server/build-ffmpeg.sh`를 사용하며 compiler, make, nasm과 pkg-config는 선택한 Linux 배포판에서 별도로 준비한다.

## 6. 로컬 container 실행

Docker host architecture가 linux/amd64 또는 linux/arm64여야 한다. 현재 source revision과 생성 시각을 image metadata에 넣어 로컬 image를 만든다.

```sh
VERSION="$(cat apps/server/VERSION)"
REVISION="$(git rev-parse HEAD)"
CREATED="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
docker build \
  --build-arg "VERSION=$VERSION" \
  --build-arg "REVISION=$REVISION" \
  --build-arg "CREATED=$CREATED" \
  -f apps/server/Dockerfile \
  -t "jastreamer-server:$VERSION" .
```

운영 디렉터리와 설정을 준비한다. 아래 명령은 music 경로의 소유권을 변경하지 않는다.

```sh
install -d -m 700 run/jastreamer/config run/jastreamer/data
install -m 600 packaging/server/server.json run/jastreamer/config/server.json
sudo chown -R 10001:10001 run/jastreamer/config run/jastreamer/data
```

`run/jastreamer/config/server.json`을 검토한 뒤 absolute music 경로를 read-only로 마운트해 시작한다.

```sh
docker run -d --name jastreamer-server \
  --network host \
  --user 10001:10001 \
  --read-only \
  --tmpfs /tmp:size=64m,mode=1777 \
  --cap-drop ALL \
  --security-opt no-new-privileges:true \
  -v "$PWD/run/jastreamer/config:/etc/jastreamer" \
  -v "$PWD/run/jastreamer/data:/var/lib/jastreamer" \
  -v "/absolute/path/to/music:/music:ro" \
  "jastreamer-server:$VERSION"

curl --fail http://127.0.0.1:8080/healthz
```

SSDP, mDNS와 renderer callback을 위해 host network를 사용한다. 컨테이너를 교체할 때 `run/jastreamer/config`와 `run/jastreamer/data` bind mount를 보존한다. `docker rm`은 이 두 host 디렉터리를 삭제하지 않지만 삭제 전에 항상 별도로 backup한다.

## 7. Synology Container Manager

제공 Compose는 linux/amd64와 linux/arm64용이며 linux/arm/v7은 지원하지 않는다. runtime UID/GID는 `10001:10001`, network는 host, root filesystem은 read-only다. DS918+와 임의의 arm64 Synology 실기기에서 검증되었다고 가정하지 않는다.
자동 upgrade는 사용하지 않는다. image 교체나 재시작 전에는 Web에서 재생을 Stop하고 player가 `stopped`인지 확인하며, config/data bind와 read-only music mount를 그대로 보존한다. Compose가 읽는 설정 파일 이름은 `${JASTREAMER_CONFIG_PATH}/server.json`이다.

NAS에서 다음 구조를 준비한다.

```text
/volume1/docker/jastreamer/config/   # writable: server.json, optional PEM
/volume1/docker/jastreamer/data/     # writable: server.sqlite, artwork, AirPlay state
/volume1/music/                      # read-only bind: existing music
```

source checkout에서는 `packaging/server/server.json`, `make package` 결과에서는 함께 생성된 `dist/release/server.json`을 사용한다. 아래 예시는 source checkout 기준이다.

```sh
sudo mkdir -p \
  /volume1/docker/jastreamer/config \
  /volume1/docker/jastreamer/data
sudo cp packaging/server/server.json \
  /volume1/docker/jastreamer/config/server.json
sudo chown -R 10001:10001 \
  /volume1/docker/jastreamer/config \
  /volume1/docker/jastreamer/data
sudo chmod 700 \
  /volume1/docker/jastreamer/config \
  /volume1/docker/jastreamer/data
sudo chmod 600 /volume1/docker/jastreamer/config/server.json
```

music share에는 UID 10001이 읽고 경로를 탐색할 최소 권한만 부여한다. music 전체를 UID 10001로 바꾸거나 `chmod 777`하지 않는다.

`deploy/docker/server/compose.synology.yaml`에는 기본 image가 없다. `JASTREAMER_SERVER_IMAGE`는 **필수**이며, 운영자가 private registry나 승인된 전달 경로에서 확인한 exact digest 또는 로컬 image ID를 넣어야 한다. 이 repository가 public registry image를 제공한다고 가정하지 않는다.

```sh
export JASTREAMER_SERVER_IMAGE='registry.example/jastreamer-server@sha256:<verified-digest>'
export JASTREAMER_CONFIG_PATH='/volume1/docker/jastreamer/config'
export JASTREAMER_DATA_PATH='/volume1/docker/jastreamer/data'
export JASTREAMER_MUSIC_PATH='/volume1/music'

docker compose -f deploy/docker/server/compose.synology.yaml config
docker compose -f deploy/docker/server/compose.synology.yaml up -d
docker compose -f deploy/docker/server/compose.synology.yaml ps
docker compose -f deploy/docker/server/compose.synology.yaml logs --tail 100 jastreamer-server
```

DSM UI의 Project 기능을 쓰더라도 같은 네 환경 변수와 bind mount를 적용한다. `docker compose config`가 실제 digest와 경로를 보여 주는지 먼저 확인한다. 기본 health 확인은 다음과 같다.

```sh
curl --fail http://<NAS-LAN-IP>:8080/healthz
```

### OCI archive를 오프라인으로 전달하기

Skopeo가 설치된 Linux build host에서 검증한 OCI archive의 NAS architecture만 선택해 Docker-loadable TAR로 바꿀 수 있다. DS918+는 `amd64`를 사용한다. 이 변환은 소스를 다시 빌드하지 않는다.

```sh
make package-verify
skopeo copy --override-os linux --override-arch amd64 \
  oci-archive:dist/release/jastreamer-server_0.2.0_linux_amd64-arm64.oci \
  docker-archive:dist/jastreamer-server_0.2.0_linux_amd64.tar:jastreamer-server:0.2.0
sha256sum dist/jastreamer-server_0.2.0_linux_amd64.tar
```

TAR를 NAS에 전달하고 전송 전후 checksum을 비교한 뒤 가져온다. OCI 파일 자체를 `docker load`에 넣지 않는다.

```sh
docker load --input jastreamer-server_0.2.0_linux_amd64.tar
docker image inspect --format '{{.Id}}' jastreamer-server:0.2.0
export JASTREAMER_SERVER_IMAGE='sha256:<verified-local-image-id>'
```

manifest digest와 config digest는 서로 다른 값이다. 선택한 OCI platform의 config digest와 Docker TAR 안 config 파일의 SHA-256이 같은지 확인하고 새 TAR checksum도 보관한다. `docker image inspect`의 image ID는 Docker storage backend에 따라 config 또는 manifest를 가리킬 수 있으므로 실제 가져온 image ID를 사용한다. image를 가져오는 일은 실행 중인 container를 교체하지 않는다. 재생이 정지되기 전에는 Compose `up`으로 기존 container를 교체하지 않는다.

## 8. LAN, 방화벽과 발견

세 가지 discovery를 구분한다.

1. **Server discovery:** Server가 `_jastreamer._tcp.local` DNS-SD를 광고한다. desktop이 Server를 찾는 용도다.
2. **UPnP output discovery:** Server가 SSDP multicast로 MediaRenderer를 찾고 SOAP AVTransport를 제어한다.
3. **AirPlay output discovery:** AirPlay backend가 `_raop._tcp.local`과 `_airplay._tcp.local`을 찾는다.

`network.interfaces`가 비어 있으면 적합한 interface를 선택한다. 여러 VLAN/NIC 중 하나만 써야 하면 OS의 정확한 interface 이름을 배열에 지정한다. container와 Synology에서는 multicast와 callback route를 보존하기 위해 host network를 사용한다.

LAN firewall과 AP/VLAN 설정에서 최소한 다음 흐름을 허용한다.

- browser/desktop/output에서 Server의 TCP 8080 또는 8443
- mDNS UDP 5353 multicast: Server 광고, desktop Server discovery, AirPlay discovery
- SSDP UDP 1900 multicast와 응답: UPnP discovery
- Server에서 출력의 광고된 HTTP/SOAP control port와 AirPlay의 receiver-advertised port
- UPnP 출력에서 Server의 media listener로 돌아오는 HTTP(S) 요청
- AirPlay 재생에 필요한 receiver와 Server 사이의 동적 LAN stream traffic

게스트 Wi-Fi multicast 차단, client isolation, VLAN ACL, host firewall은 host network만으로 해결되지 않는다. 자동 발견이 실패해도 desktop에는 Server HTTP(S) root를 직접 입력할 수 있지만, output discovery는 Server와 출력 사이의 multicast 경로를 고쳐야 한다.

Server는 HTTPS가 켜져 있으면 HTTPS listener를, 아니면 HTTP listener를 desktop discovery에 광고한다. desktop은 mDNS query를 5초마다 갱신하고 발견된 Server의 `/api/v1/discovery` identity를 60초 간격으로 다시 확인한다. 최근 Server도 60초 간격으로 확인한다. 수동 연결 시에는 즉시 identity를 확인한다. 발견만으로 자동 연결하거나 playback command를 보내지 않는다.

`/api/v1/discovery`의 UUID는 `server.sqlite`에 저장되므로 data를 보존하면 재시작과 upgrade 뒤에도 유지된다. data를 초기화하면 UUID가 달라진다. 이 UUID와 mDNS 광고는 인증 비밀이나 소유권 증명이 아니다.

내장 guard는 실제 TCP peer가 private/loopback/link-local인지, `allowed_cidrs`, Host와 Origin이 현재 Server인지 확인한다. TLS를 다른 proxy에서 종료했다는 사실을 전달하는 forwarded-header 설정은 제공하지 않는다. 별도 reverse proxy를 지원된 배포 방식으로 가정하지 말고 내장 HTTPS를 사용한다.

## 9. UPnP와 AirPlay 동작

### UPnP/DLNA

Server는 광고된 `SetAVTransportURI`, `Play`, `Stop`, 상태 조회와 선택적 Pause/REL_TIME Seek capability를 검사한다. 원본 MIME을 출력이 지원하면 byte-range 가능한 원본을 제공하고, 그렇지 않으며 `media.transcode=true`이고 출력이 L16을 받으면 FFmpeg 변환을 사용한다. 제조사·모델명만으로 실제 capability를 보장하지 않는다.

출력에 전달하는 media URL은 `/media/{token}`이고 artwork는 `/media/{token}/artwork`다. grant는 선택 renderer의 source IP, 현재 track 파일 identity와 playback 수명에 묶여 있고 만료·교체·취소 후 재사용할 수 없다. 이 경로는 일반 browser artwork URL이 아니며 bookmark하지 않는다.

### AirPlay

AirPlay는 FFmpeg로 원본을 decode하고 pyatv RAOP sender에 signed 16-bit big-endian PCM을 전달한다. wire format은 L16, 44.1 kHz, stereo, 16-bit로 고정된다. `sr`, `ch`, `ss`를 명시적으로 다른 값으로 광고하는 receiver는 지원하지 않는다.

AirPlay receiver가 PIN pairing을 요구하면 player를 먼저 Stop하고 Web의 output 인증 절차를 시작한 뒤 receiver에 표시된 정확한 네 자리 PIN을 2분 안에 입력한다. 별도 AirPlay password를 요구하는 receiver에는 해당 password도 입력한다. 성공한 credentials/password와 jastreamer client identity는 `server.sqlite`에 저장된다. data를 잃으면 다시 인증해야 한다.

Pause는 현재 sender stream을 중지하고 위치를 보존한다. resume은 새 stream을 설정한다. 재생 중 Seek도 현재 stream을 중지한 뒤 목표 위치에서 새 stream을 설정한다. 따라서 receiver에 따라 짧은 재연결 공백이 생길 수 있다. metadata와 원본 library JPEG/PNG artwork는 receiver가 처리할 수 있을 때 전달된다.

pyatv나 특정 AirPlay receiver가 설치된 사실만으로 호환을 가정하지 않는다. 인증 실패, 명시적 format 불일치와 transport 오류는 성공으로 바꾸거나 무한 재시도하지 않는다.

## 10. HTTP surface

모든 route는 같은 Server origin에 있다. public 표시는 로그인 없이 접근 가능하다는 뜻일 뿐이며 private-network, Host와 Origin guard는 그대로 적용된다.

| route | 접근 | 기능 |
|---|---|---|
| `GET /`와 Web asset | public | embedded React application과 client-side route |
| `GET /healthz` | public | process HTTP readiness (`{"status":"ready"}`) |
| `GET /api/v1/discovery` | public | `product`, protocol, Server UUID, name, version |
| `GET/POST /api/v1/setup` | public | 최초 계정 필요 여부/최초 계정 생성 |
| `GET /api/v1/session`, `POST /api/v1/login` | public | session 상태/로그인 |
| `POST /api/v1/logout`, `POST /api/v1/account/password` | session | logout/비밀번호 변경 |
| `GET/PUT /api/v1/config` | session | revision 기반 설정 읽기/저장 |
| `GET /api/v1/library/{tracks,albums,artists,genres,folders}` | session | 검색·filter·paging browse |
| `GET /api/v1/library/tracks/{id}`, `GET /api/v1/artwork/{id}` | session | track와 Web artwork |
| `GET/POST /api/v1/library/scans`, `DELETE /api/v1/library/scans/{id}` | session | scan 상태·시작·취소 |
| `GET/POST /api/v1/playlists`, `GET/PUT/DELETE /api/v1/playlists/{id}` | session | revision 기반 playlist 작업 |
| `GET /api/v1/renderers`, `POST /api/v1/renderers/refresh` | session | UPnP/AirPlay output 목록과 재검색 |
| `POST /api/v1/renderers/{id}/pairing` | session | AirPlay PIN/password 인증 |
| `GET/POST /api/v1/player`, `PUT /api/v1/player/output` | session | snapshot, transport command, stopped-only output 선택 |
| `GET/POST /api/v1/queue` | session | revision 기반 queue 읽기/변경 |
| `GET /api/v1/events` | session | 변경 알림 SSE; authoritative state 자체가 아님 |
| `GET/HEAD /media/{token}`와 `/media/{token}/artwork` | renderer-bound | 현재 UPnP 출력에 부여한 제한 media grant |

Web의 변경 요청은 `application/json`과 `X-Jastreamer-Request: web`을 사용한다. API는 cross-origin client용 CORS surface가 아니다. SSE event는 다시 읽을 snapshot 종류를 알리는 신호이며 command 성공이나 전체 state를 대신하지 않는다.

## 11. 선택적 Windows portable desktop

소스 의존성을 확인하고 x64 portable ZIP을 만든다.

```sh
make desktop-verify
make desktop-package
```

결과는 `apps/desktop/dist/jastreamer-desktop_0.2.0_windows-x64.zip`, `.sha256`, `manifest.json`이다. ZIP은 서명되지 않은 private build다. Windows 10/11 x64의 쓰기 가능한 로컬 폴더에 **전체 ZIP**을 풀고 `jastreamer-desktop.exe`를 실행한다. ZIP 안에서 실행하거나 EXE 하나만 옮기지 않는다.

앱은 server selection 화면부터 시작한다. `_jastreamer._tcp.local`에서 발견한 항목이나 사용자가 입력한 HTTP(S) root를 `/api/v1/discovery`로 확인한 뒤에만 선택할 수 있다. 각 Server UUID와 전체 origin별 Chromium session을 격리하며, discovery connection은 로그인·자동 연결·playback command를 수행하지 않는다. 앱을 닫거나 Server를 바꿔도 Server에서 이미 진행 중인 재생을 멈추지 않는다.

최근 Server, cookie와 Chromium profile은 EXE 옆 `user-data`에 있다. 폴더가 쓰기 불가능하면 AppData로 우회하지 않고 시작을 중단한다. upgrade할 때:

1. desktop을 완전히 종료한다.
2. 기존 앱 폴더 전체와 `user-data`를 backup한다.
3. 새 ZIP을 새 쓰기 가능한 폴더에 완전히 푼다.
4. 기존 `user-data` 디렉터리를 새 EXE 옆으로 보존해 옮긴 뒤 실행한다.

다른 Windows 계정이나 PC에서는 OS로 보호된 session을 읽지 못할 수 있으므로 다시 로그인한다. package ZIP에는 `user-data`가 포함되지 않으며, packaging output에 기존 `user-data`가 있으면 packaging script는 이를 삭제하지 않고 중단한다.

## 12. 개발, 검증과 private packaging

전체 기본 검증:

```sh
make verify
```

이 명령은 lockfile 기반 Web production build, Go 전체 test와 `go vet`을 실행한다. embedded Web을 포함한 native Server build는 다음과 같다.

```sh
make build
```

실제 embedded Web과 cookie account session을 Chromium에서 확인하려면 browser를 한 번 설치하고 smoke를 실행한다.

```sh
cd apps/control
npx playwright install chromium
cd ../..
make browser-smoke
```

Windows desktop 단위 검증과 package 무결성 검사는 다음 target에 포함된다.

```sh
make desktop-verify
make desktop-package
```

multi-platform linux/amd64+linux/arm64 OCI archive를 로컬에 만들 때 `make package`는 Git `HEAD`에서 full source revision과 creation epoch를 가져온다.

선택한 Docker Buildx builder는 두 architecture의 Linux 프로그램을 실행할 수 있어야 한다. native worker 또는 격리된 emulation을 준비하고 `docker buildx inspect --bootstrap`으로 builder 상태를 확인한다. 별도 builder를 지정하려면 `BUILDX_BUILDER=builder-name make package`를 사용한다. 이 package 명령은 host의 binfmt 설정을 자동 변경하지 않는다.

```sh
make package
make package-verify
```

재현할 revision/epoch를 명시하려면 Make variable로 덮어쓴다.

```sh
make \
  SOURCE_REVISION="<full-40-or-64-character-object-id>" \
  SOURCE_DATE_EPOCH="<integer-commit-epoch>" \
  package
make package-verify
```

`packaging/server/release.sh`를 직접 실행할 때만 대응 환경 변수 이름을 사용한다.

```sh
JASTREAMER_SOURCE_REVISION="<full-40-or-64-character-object-id>" \
SOURCE_DATE_EPOCH="<integer-commit-epoch>" \
packaging/server/release.sh dist/release
```

생성되는 파일은 `dist/release/jastreamer-server_0.2.0_linux_amd64-arm64.oci`, `manifest.json`, `server.json`, `LICENSE`, `THIRD-PARTY-NOTICES.txt`, `SHA256SUMS`다. `make package-verify`는 `dist/release`의 package 계약을 확인한다.

이 명령들은 local/private artifact를 만들고 검사할 뿐 registry push, GitHub Release, code signing, 자동 update 또는 public publication을 수행하지 않는다. public 배포가 승인되었다고 해석하지 않는다.

자동 검증은 해당 test와 격리된 runtime path만 증명한다. 실제 speaker로 들은 오디오, 모든 UPnP/AirPlay receiver, 실제 Synology hardware, 운영 TLS/LAN, 실제 Windows PC 실행이나 서명 상태를 대신하지 않는다. 대상 환경에서는 account, scan, artwork, output discovery, PIN/password, pause/seek/resume, EOF queue advance와 실제 소리를 각각 확인한다.

## 13. Backup, upgrade, rollback과 제거

일관된 backup을 위해 Server를 먼저 중지하고 다음을 함께 보존한다.

- config 디렉터리 전체와 HTTPS PEM
- data 디렉터리 전체 (`server.sqlite`, artwork, AirPlay 상태 포함)
- 실행한 exact image digest 또는 binary version과 full source revision

Synology Compose에서는 현재 환경 변수를 유지한 같은 shell에서:

```sh
docker compose -f deploy/docker/server/compose.synology.yaml down
```

upgrade는 중지 → config/data backup → `JASTREAMER_SERVER_IMAGE`를 새 verified digest로 변경 → `docker compose config` 검토 → `up -d` 순서로 수행한다. 새 schema에 이전 binary가 호환된다고 가정하지 않는다. rollback은 이전 image만 선택하지 말고 그 image 시점의 config/data backup을 함께 복구한다.

reset 또는 제거 시 backup 후 jastreamer config와 data만 지운다. `/volume1/music`, 다른 NAS music share, native music root는 어떤 경우에도 reset 대상이 아니다. upgrade 뒤에는 health만 보지 말고 login, library scan, artwork, queue 보존, output 발견과 대상 기기의 실제 재생을 명시적으로 확인한다.
