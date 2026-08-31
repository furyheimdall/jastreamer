# jastreamer 사용자 매뉴얼

이 문서는 jastreamer Server와 Control candidate를 검증·운영하는 사용자를 위한 통합
매뉴얼이다. 설치부터 최초 관리자 생성, Control 연결, 음악 검색과 재생, 백업, 장애 복구,
제거까지의 일반적인 순서를 설명한다.

> **현재 배포 상태**
>
> - Server와 Control은 검증된 product candidate이지만 아직 공개 출시되지 않았다.
> - Windows MSIX production signing, Android production signing lineage, 물리 FiiO K17,
>   native Windows WASAPI qualification 및 publication은 승인 대기 상태다.
> - Windows Renderer는 사용자 제품이 아니라 CI/qualification 전용 test harness다.
> - candidate를 production-ready 또는 일반 공개 release로 표시하지 않는다.

## 1. 구성 요소와 지원 범위

| 구성 요소 | 용도 | 현재 제공 형태 |
| --- | --- | --- |
| Server | 음악 catalog, queue, 재생 정책, HTTPS API, pairing | Linux package, OCI candidate, Windows candidate |
| Control | Server 검색·pairing, catalog 검색, queue와 transport 조작 | Web PWA, Android APK, Windows MSIX candidate |
| Renderer | native Windows audio qualification | CI/test 전용, 일반 사용자 설치 금지 |
| FiiO K17 | 현재 한정된 UPnP 재생 대상 | firmware V261 이상만 구현 대상, 물리 qualification 대기 |

jastreamer는 범용 UPnP, multi-room 동기화, gapless playback 또는 임의 Renderer 지원을
주장하지 않는다. 현재 지원 계약은 정확한 protocol major와 capability로 결정된다.

## 2. 설치 전 준비

다음을 준비한다.

- Server를 실행할 Linux/Windows host 또는 OCI/Synology 환경
- 정상적으로 검증되는 HTTPS hostname과 인증서
- Server data와 음악 directory를 위한 영구 저장 공간
- Control을 설치할 Web host, Windows PC 또는 Android 기기
- 선택 사항: 검증된 FFmpeg 실행 파일
- 선택 사항: FiiO K17 firmware V261 이상
- 같은 candidate set의 `manifest.json`, `SHA256SUMS`, provenance 및 설치 파일

### 2.1 Candidate 무결성 확인

설치 전에 candidate directory에서 checksum을 검증한다.

```sh
sha256sum -c SHA256SUMS
```

Windows에서는 각 파일을 다음처럼 확인한다.

```powershell
Get-FileHash .\jastreamer-server_<버전>_windows_amd64.msi -Algorithm SHA256
Get-FileHash .\jastreamer-control_<버전>_windows.msix -Algorithm SHA256
```

checksum, signer, artifact 이름 또는 source 기록 중 하나라도 다르면 설치하지 않는다.
문서 예제의 `<버전>`, `<server>`, `<digest>` 같은 placeholder를 그대로 실행하지 않는다.

## 3. Server 설치

### 3.1 Linux package

다음은 Debian/Ubuntu amd64 예제다. 먼저 `uname -m`으로 architecture를 확인한다.
`x86_64`는 `amd64`, `aarch64`는 `arm64` artifact를 사용한다. RPM 계열에서는 같은
architecture의 `.rpm`을 사용한다.

```sh
sudo dpkg -i jastreamer-server_<버전>_linux_amd64.deb
# Debian/Ubuntu arm64:
# sudo dpkg -i jastreamer-server_<버전>_linux_arm64.deb
# Fedora/RHEL amd64:
# sudo dnf install ./jastreamer-server_<버전>_linux_amd64.rpm
# Fedora/RHEL arm64:
# sudo dnf install ./jastreamer-server_<버전>_linux_arm64.rpm
sudoedit /etc/jastreamer/server.env
```

최초 관리자 생성에 사용할 설치별 고엔트로피 secret을 지정한다.

```text
JASTREAMER_SETUP_SECRET=<설치별-보호된-bootstrap-secret>
```

secret을 shell history, URL, screenshot, log 또는 공유 문서에 기록하지 않는다.

Server 설정은 `/etc/jastreamer/server.json`, 영구 data는
`/var/lib/jastreamer`에 둔다. 설정을 검토한 뒤 service를 시작한다.

```sh
sudo systemctl enable --now jastreamer-server.service
systemctl status jastreamer-server.service --no-pager
journalctl -u jastreamer-server.service -n 100 --no-pager
```

### 3.2 OCI 또는 Synology

반드시 staged OCI digest를 사용하고 `latest` tag를 사용하지 않는다.

Synology의 기본 경로 예:

```text
/volume1/docker/jastreamer/
├── config/server.json
├── data/
├── media/
└── external/ffmpeg/       # 선택 사항
```

Container Manager project 변수:

```text
JASTREAMER_SERVER_IMAGE=<검증된-image@sha256:digest>
JASTREAMER_SETUP_SECRET=<보호된-bootstrap-secret>
JASTREAMER_DATA_PATH=/volume1/docker/jastreamer/data
JASTREAMER_CONFIG_PATH=/volume1/docker/jastreamer/config
```

기본 Compose 계약은 매 expansion과 start에서 non-empty setup secret을 요구한다. 최초
관리자 생성 뒤에도 기본 Compose를 그대로 쓴다면 보호된 project 변수에 같은 값을
유지한다.

DSM shell에서 실행할 때는 같은 네 변수를 mode `0600`인 전용 env file에 저장한다. 이
파일을 backup할 때는 secret 저장소로 취급하고 일반 config archive에 평문으로 넣지 않는다.

```text
# /volume1/docker/jastreamer/project.env
JASTREAMER_SERVER_IMAGE=<검증된-image@sha256:digest>
JASTREAMER_SETUP_SECRET=<보호된-bootstrap-secret>
JASTREAMER_DATA_PATH=/volume1/docker/jastreamer/data
JASTREAMER_CONFIG_PATH=/volume1/docker/jastreamer/config
```

```sh
chmod 600 /volume1/docker/jastreamer/project.env
```

배포 전 expansion을 같은 env file로 확인한다.

```sh
docker compose \
  --env-file /volume1/docker/jastreamer/project.env \
  -f deploy/docker/server/compose.synology.yaml config
```

배포 후 상태를 확인한다.

```sh
docker compose --env-file /volume1/docker/jastreamer/project.env \
  -f deploy/docker/server/compose.synology.yaml ps
docker compose --env-file /volume1/docker/jastreamer/project.env \
  -f deploy/docker/server/compose.synology.yaml \
  logs --tail 100 jastreamer-server
docker compose --env-file /volume1/docker/jastreamer/project.env \
  -f deploy/docker/server/compose.synology.yaml exec jastreamer-server \
  /usr/local/bin/jastreamer-server health
```

자세한 Synology 제약은 [Synology DSM Server 운영](synology.md)을 따른다.

### 3.3 Windows Server

Windows amd64에서는 같은 candidate set의 MSI, EXE, `server.cer`, `fingerprint.txt`,
`SHA256SUMS`를 사용한다. 독립 EXE는 foreground 진단 artifact이며 service 설치를 하지
않는다.

Elevated PowerShell에서 digest와 certificate를 확인하고, 확인한 leaf certificate만
`TrustedPeople`에 넣은 뒤 MSI를 설치한다.

```powershell
Get-FileHash .\jastreamer-server_<버전>_windows_amd64.msi -Algorithm SHA256
Get-FileHash .\jastreamer-server_<버전>_windows_amd64.exe -Algorithm SHA256
$expected = ((Get-Content .\fingerprint.txt) `
  -replace '^SHA256:\s*','' -replace ':','').Trim().ToUpperInvariant()
$certificate = [Security.Cryptography.X509Certificates.X509Certificate2]::new(
  (Resolve-Path .\server.cer).Path
)
$actual = (Get-FileHash `
  -InputStream ([IO.MemoryStream]::new($certificate.RawData)) `
  -Algorithm SHA256).Hash.ToUpperInvariant()
if ($actual -ne $expected) { throw 'Server certificate SHA-256 mismatch' }
certutil -dump .\server.cer
Import-Certificate -FilePath .\server.cer `
  -CertStoreLocation Cert:\LocalMachine\TrustedPeople

$msi = Resolve-Path .\jastreamer-server_<버전>_windows_amd64.msi
$install = Start-Process msiexec.exe `
  -ArgumentList @('/i', $msi, '/qn', '/norestart') -Wait -PassThru
if ($install.ExitCode -ne 0) { throw "MSI install failed: $($install.ExitCode)" }
```

최초 start 전에 service 전용 bootstrap environment를 설정한다. 실제 secret을 transcript에
출력하지 않는다.

```powershell
$serviceKey = 'HKLM:\SYSTEM\CurrentControlSet\Services\jastreamer-server'
New-ItemProperty -Path $serviceKey -Name Environment -PropertyType MultiString `
  -Value @('JASTREAMER_SETUP_SECRET=<보호된-bootstrap-secret>') -Force
Start-Service jastreamer-server
Get-Service jastreamer-server
Get-Content "$env:ProgramData\jastreamer\Server\service.log" -Tail 100
```

Windows native install/status/remove qualification은 아직 pending이다. 자세한 backup,
upgrade와 제거 절차는 [Server 운영 문서의 Windows lifecycle](server-pairing.md#windows-server-operator-lifecycle)을
따른다.

### 3.4 Server 상태와 TLS identity 확인

```sh
systemctl is-active jastreamer-server.service
curl --fail --cacert <server-ca.pem> https://<server>:8443/healthz
curl --fail --cacert <server-ca.pem> https://<server>:8443/api/v1/identity
```

`sha256_fingerprint`를 Server console과 별도의 신뢰 가능한 경로로 비교한다. 인증서
hostname/chain 오류를 우회하지 않는다. data directory의 TLS identity가 예상 없이
바뀌면 연결을 중단하고 원인을 확인한 뒤 모든 Control을 다시 pairing한다.

## 4. 최초 관리자 생성

1. 정상 TLS로 `https://<server>:8443/pair/`를 연다.
2. 관리자 이름과 `JASTREAMER_SETUP_SECRET`을 입력한다.
3. 한 번만 표시되는 admin token을 보호된 password manager에 저장한다.
4. `/admin/`에 접속해 Server display name과 운영 설정을 확인한다.
5. bootstrap이 끝났는지 확인한 뒤 setup secret 취급 정책을 결정한다.

마지막 admin device는 철회할 수 없다. admin token을 Controller나 Renderer token 대신
사용하지 않는다.

## 5. Server 기본 설정

`https://<server>:8443/admin/`에서 다음을 설정한다.

- display name
- catalog root
- Web Control의 정확한 HTTPS origin
- pairing code TTL
- 명시적인 private UPnP interface
- zone과 Renderer/K17 assignment
- 선택 사항: 절대 FFmpeg 실행 파일 경로
- 선택 사항: K17 media-only HTTP listener

Web Control origin은 path나 trailing slash가 없는 정확한 origin이어야 한다.

```text
https://control.example.internal
```

`*` wildcard CORS는 지원하지 않는다. 환경 변수로 잠긴 설정은 Web admin에서 바꿀 수
없으며 `CONFIG_FIELD_LOCKED`가 정상 결과다.

### 5.1 Catalog root와 scan

catalog root는 설정된 허용 base 아래의 실제 directory여야 한다. symlink escape나 임의
host path는 거부된다.

1. `/admin/`에서 catalog root를 추가한다.
2. scan을 시작한다.
3. scan 상태가 `complete`인지 확인한다.
4. 검색 결과에서 title, artist, album, album artist가 보이는지 확인한다.

scan 중에도 마지막 완전 snapshot은 유지된다. stale cursor에
`CATALOG_REVISION_CHANGED`가 나오면 첫 page부터 다시 읽는다.

### 5.2 선택 사항: FFmpeg

FFmpeg는 jastreamer에 포함되지 않으며 자동 다운로드되지 않는다. 관리자가 검증한
실행 파일을 설치하고 절대 경로를 지정한다.

```text
/usr/bin/ffmpeg
```

재시작 후 `/api/v1/config`의 `diagnostics.ffmpeg`에서 status, digest, version과 codec
지원 상태를 확인한다. FFmpeg가 없거나 호환되지 않아도 지원되는 원본 재생은 유지되며
L16 fallback만 비활성화된다.

## 6. Control 설치

사용할 플랫폼 하나를 선택한다.

### 6.1 Web Control

검증된 ZIP을 HTTPS static host의 별도 release directory에 푼다.

```sh
unzip -t jastreamer-control_<버전>_web.zip
mkdir -p /srv/www/jastreamer-control/<버전>
unzip jastreamer-control_<버전>_web.zip \
  -d /srv/www/jastreamer-control/<버전>
```

`index.html`, `flutter_bootstrap.js`, `main.dart.js`, `manifest.json`을 같은 release에서
제공한다. 브라우저 TLS 경고를 무시하지 않는다. 자세한 배포 및 service worker 정리는
[Web Control 배포와 운영](control-web.md)을 참고한다.

### 6.2 Android Control

APK signer와 ABI를 확인한다.

```sh
expected=$(
  awk '{print tolower($2)}' Android-CERT-SHA256.txt \
    | tr -d ':'
)
actual=$(
  apksigner verify --verbose --print-certs \
    jastreamer-control_<버전>_android_universal.apk \
    | awk -F ': ' '/Signer #1 certificate SHA-256 digest/ {
        value=tolower($2); gsub(":","",value); print value; exit
      }'
)
test -n "$actual"
test "$actual" = "$expected"
apksigner verify --verbose --print-certs \
  jastreamer-control_<버전>_android_universal.apk
unzip -Z1 jastreamer-control_<버전>_android_universal.apk \
  | grep -E '^lib/(armeabi-v7a|arm64-v8a|x86_64)/libapp\.so$' | sort -u
adb install jastreamer-control_<버전>_android_universal.apk
```

`armeabi-v7a`, `arm64-v8a`, `x86_64` 중 하나라도 없거나 signer가 다르면 설치하지 않는다.
설치 뒤 sideload 권한을 다시 끈다. 자세한 내용은
[Android Control 설치와 운영](control-android.md)을 참고한다.

### 6.3 Windows Control

MSIX와 certificate digest를 확인한 뒤 확인한 certificate만 `TrustedPeople`에 넣는다.

```powershell
Get-FileHash .\jastreamer-control_<버전>_windows.msix -Algorithm SHA256
$expected = ((Get-Content .\Windows-CERT-SHA256.txt) `
  -replace '^SHA256:\s*','' -replace ':','').Trim().ToUpperInvariant()
$certificate = [Security.Cryptography.X509Certificates.X509Certificate2]::new(
  (Resolve-Path .\control-windows.cer).Path
)
$actual = (Get-FileHash `
  -InputStream ([IO.MemoryStream]::new($certificate.RawData)) `
  -Algorithm SHA256).Hash.ToUpperInvariant()
if ($actual -ne $expected) { throw 'Control certificate SHA-256 mismatch' }
certutil -dump .\control-windows.cer
Import-Certificate -FilePath .\control-windows.cer `
  -CertStoreLocation Cert:\LocalMachine\TrustedPeople
Add-AppxPackage .\jastreamer-control_<버전>_windows.msix
Get-AppxPackage -Name io.jastreamer.control
```

자체 서명 certificate는 Public Trust 또는 SmartScreen 평판을 의미하지 않는다. 자세한
설치와 trust removal은 [Windows Control 설치와 운영](control-windows.md)을 참고한다.

## 7. Control pairing

### 7.1 Controller code 생성

1. 관리자가 Server `/pair/`를 연다.
2. `controller` 역할을 선택한다.
3. 알아볼 수 있는 device 이름을 입력한다.
4. 일회용 pairing code를 만든다.

code는 설정된 60~3600초 동안 한 번만 사용할 수 있다. 만료되거나 이미 사용한 code는
폐기하고 새 code를 만든다.

### 7.2 Control 연결

1. Control에 Server HTTPS origin을 입력한다.
2. 독립 경로로 확인한 Server certificate SHA-256 fingerprint를 입력한다.
3. **Discover Server**를 실행한다.
4. Server identity, protocol major 3과 required capability를 확인한다.
5. pairing code를 소비한다.
6. 한 번 표시되는 controller token을 **Complete pairing** 입력란에 입력한다.
7. Control을 재시작하거나 Web page를 새로 열어 reconnect를 확인한다.

Web Control은 token을 현재 tab/browser session의 `sessionStorage`에만 보관한다. Windows는
현재 사용자 DPAPI, Android는 Android Keystore AES-GCM으로 보호한다. token을 URL,
command line, log, clipboard history 또는 screenshot에 남기지 않는다.

## 8. Renderer 또는 K17 연결

일반 사용자는 Windows Renderer test harness를 설치하지 않는다. 실제 재생 대상은 현재
지원 계약상 FiiO K17 firmware V261 이상으로 한정된다.

1. K17 firmware가 V261 이상인지 확인한다.
2. Server `/admin/`에서 사용할 private network interface를 명시한다.
3. discovery 결과의 model, firmware와 `protocolInfo`를 확인한다.
4. K17을 zone에 할당한다.
5. Renderer observed state가 online인지 확인한다.

다른 model 또는 V260 이하는 unsupported로 남겨야 한다. 범용 UPnP 장치로 강제 우회하지
않는다.

K17가 원본 MIME을 광고하면 원본이 우선한다. 원본이 맞지 않고 K17의 L16 capability와
FFmpeg probe가 모두 통과할 때만 stereo 44.1 kHz, 16-bit big-endian L16을 사용한다.

## 9. 음악 검색과 queue

### 9.1 Catalog 검색

Control의 **Browse catalog**에서 title, artist, album, album artist를 검색한다.
Server가 `available`로 표시한 track만 explicit queue에 추가한다.

### 9.2 Queue 조작

지원되는 작업:

- Add
- Remove
- Earlier / Later 순서 변경
- Clear
- Retry blocked
- Skip blocked

enqueue는 자동 재생이 아니다. active entry 보호 또는 stale revision 오류가 발생하면
자동으로 mutation을 반복하지 않는다. **Refresh Server truth**를 실행하고 현재 queue와
보존된 의도를 비교한 뒤 수동으로 다시 실행한다.

automatic preview는 read-only이며 explicit queue row가 아니다. Control은 next track을
로컬에서 결정하지 않는다.

## 10. 재생 조작과 상태 읽기

지원되는 transport:

- Previous
- Play / Start
- Pause
- Resume
- Next / Skip
- Stop
- Seek

Start에는 zone에 할당된 online Renderer가 필요하다. Stop은 다음 track으로 자동 진행하지
않고, Next는 현재 stop 확인 뒤 Server가 다음 항목을 선택한다. 지원하지 않는 seek는
typed failure로 남는다.

화면에서 다음 세 상태를 구분한다.

| 상태 | 의미 |
| --- | --- |
| Server intent | Server가 의도한 queue·transport 상태 |
| Renderer observed | 실제 Renderer가 보고한 상태 |
| pending command | 처리 또는 확인을 기다리는 명령 |

HTTP `202` 또는 pending은 실제 speaker 재생 성공을 의미하지 않는다. event는
invalidation 신호이며 Control은 authoritative REST state를 다시 읽는다. reconnect,
sequence/epoch gap 또는 overflow 뒤에는 full resync가 수행된다.

## 11. 자주 발생하는 문제

| 증상 또는 code | 의미 | 안전한 복구 |
| --- | --- | --- |
| `TOKEN_REVOKED` | device token이 철회됨 | 저장 token을 지우고 새 controller code로 pairing |
| certificate mismatch/change | Server TLS identity가 예상과 다름 | 연결 중단, 별도 경로로 identity 확인, 다시 pairing |
| `PAIRING_CODE_INVALID` | code 오류 | 입력 중단 후 관리자가 새 code 생성 |
| `PAIRING_CODE_EXPIRED` | code 만료 | 새 code 생성 |
| `PAIRING_CODE_USED` | 이미 소비된 code | 기존 code 재사용 금지, 새 code 생성 |
| `PAIRING_RATE_LIMITED` | pairing rate limit | 반복 입력 중단 후 관리자 확인 |
| stale revision/conflict | 화면 state가 최신이 아님 | Server truth refresh 후 수동 재실행 |
| Renderer offline | 할당 대상이 연결되지 않음 | network, assignment, K17 상태 복구 후 명시적 retry |
| blocked track | head를 재생할 수 없음 | 원인 수정 후 Retry blocked 또는 Skip blocked |
| Server offline | HTTPS/API 연결 불가 | service, TLS, firewall 확인 후 reconnect와 full resync |
| FFmpeg unavailable | L16 fallback 사용 불가 | 절대 경로·codec 수정 후 재시작; 원본 경로는 유지 |
| `CONFIG_FIELD_LOCKED` | 환경 변수가 설정을 잠금 | environment 또는 operator config에서 변경 |
| `CATALOG_REVISION_CHANGED` | scan으로 cursor가 stale | 첫 page부터 다시 검색 |

문제를 해결하기 위해 Server data directory를 먼저 삭제하지 않는다.

## 12. 백업, upgrade와 rollback

upgrade 전 다음을 보존한다.

- SQLite online backup
- 전체 Server data directory
- `server.json`
- 비밀을 제외한 환경 설정 기록
- 현재 binary/image와 artifact digest
- 현재 Control artifact와 signer 기록

`sqlite3` CLI는 jastreamer에 포함되지 않는다. Debian/Ubuntu에서는 서명된 배포판
repository에서 설치한다.

```sh
sudo apt-get update
sudo apt-get install sqlite3
```

쓰기 작업을 중단한 maintenance window에서, Server가 실행 중인 동안 SQLite online
backup을 만든다. backup directory는 관리자만 읽을 수 있어야 한다.

```sh
backup_dir="/var/backups/jastreamer/$(date -u +%Y%m%dT%H%M%SZ)"
sudo install -d -m 0700 "$backup_dir"

sudo sqlite3 /var/lib/jastreamer/catalog.sqlite \
  ".timeout 10000" ".backup '$backup_dir/catalog.sqlite'"
sudo sqlite3 /var/lib/jastreamer/playback.sqlite \
  ".timeout 10000" ".backup '$backup_dir/playback.sqlite'"

test "$(sudo sqlite3 "$backup_dir/catalog.sqlite" 'PRAGMA integrity_check;')" = ok
test "$(sudo sqlite3 "$backup_dir/playback.sqlite" 'PRAGMA integrity_check;')" = ok
```

두 DB backup의 integrity가 모두 `ok`인 경우에만 Server를 중지하고 전체 data와 static
config를 보존한다. archive에는 security state와 TLS private material이 있으므로 encrypted,
access-controlled storage로 옮긴다.

```sh
sudo systemctl stop jastreamer-server.service
sudo tar --acls --xattrs -C / -cpf "$backup_dir/server-state.tar" \
  var/lib/jastreamer etc/jastreamer
sudo sha256sum "$backup_dir"/* > "$backup_dir/SHA256SUMS"
```

Synology에서는 동일한 두 DB에 SQLite online backup을 수행한 뒤 project를 중지하고
`data`, `config/server.json`, env file, 현재 image digest를 함께 보존한다. Windows에서는
검증한 `sqlite3.exe`로
`C:\ProgramData\jastreamer\Server\catalog.sqlite`와 `playback.sqlite`에 같은
`.backup`·`PRAGMA integrity_check` 절차를 적용한 뒤 service를 중지하고 전체 ProgramData
directory를 보존한다.

upgrade 순서:

1. 새 candidate checksum과 signer를 확인한다.
2. 위 online backup과 integrity 검사를 완료한다.
3. Server service/project를 중지하고 전체 data/config snapshot을 만든다.
4. 새 package 또는 immutable image digest를 적용한다.
5. Server를 시작하고 `/healthz`, identity, config revision, devices, catalog, zone/queue를
   확인한다.
6. 같은 signer와 package identity의 Control을 update한다.
7. Control reconnect와 실제 transport 상태를 확인한다.

Android update:

```sh
adb install -r jastreamer-control_<새버전>_android_universal.apk
```

새 schema data를 구버전 binary가 읽을 수 있다고 가정하지 않는다. rollback은 이전
binary/image만 되돌리는 것이 아니라 **upgrade 전 전체 data backup과 이전 artifact를
함께** 복구하는 작업이다. Control rollback은 Server queue/catalog를 되돌리지 않는다.

## 13. Device 철회와 제거

분실·폐기한 device는 `/admin/`에서 즉시 철회한다. 다음 요청부터
`401 TOKEN_REVOKED`가 되어야 한다.

### 13.1 Control 제거

Android:

```sh
adb uninstall io.jastreamer.control
```

Windows:

```powershell
Get-AppxPackage -Name io.jastreamer.control | Remove-AppxPackage
```

Web:

1. static release pointer를 제거한다.
2. browser site data와 service worker를 삭제한다.
3. Server에서 해당 Web Control device를 철회한다.

Windows certificate는 배포 기록과 정확히 일치하는 thumbprint만 제거한다. 관련 없는
certificate를 삭제하지 않는다.

### 13.2 Server 제거

Linux package 제거는 service와 binary만 제거하고 복구를 위해 data와 service account를
남길 수 있다. Synology에서는 다음 명령이 bind-mounted data를 삭제하지 않는다.

```sh
docker compose -f deploy/docker/server/compose.synology.yaml down
```

영구 삭제 전:

1. 모든 controller, renderer와 사용하지 않는 추가 admin device를 철회한다.
2. backup 보존 정책을 확인한다.
3. 전용 data/config path인지 다시 확인한다.
4. 해당 전용 path만 삭제한다.
5. 정확한 Server certificate만 trust store에서 제거한다.

마지막 admin은 API로 철회할 수 없다. 영구 제거에서는 service를 중지한 뒤 전용
security/data state 전체를 삭제하는 것이 마지막 admin credential을 폐기하는 단계다.
shared media directory나 다른 service certificate를 삭제하지 않는다.

## 14. 운영 점검표

### 설치 직후

- [ ] 모든 candidate checksum과 signer를 확인했다.
- [ ] Server HTTPS chain, hostname과 fingerprint를 확인했다.
- [ ] 최초 admin token을 보호된 위치에 보관했다.
- [ ] Control origin을 exact HTTPS origin으로 등록했다.
- [ ] catalog scan이 complete다.
- [ ] zone과 supported Renderer/K17 assignment를 확인했다.
- [ ] Control을 재시작해 pairing token reconnect를 확인했다.
- [ ] Server intent와 Renderer observed state를 구분해 확인했다.

### 정기 운영

- [ ] `/healthz`, service/container 상태와 log를 확인한다.
- [ ] data와 SQLite online backup을 검증한다.
- [ ] device 목록에서 사용하지 않는 credential을 철회한다.
- [ ] TLS identity와 artifact digest 변경을 기록한다.
- [ ] FFmpeg 및 K17 진단 상태를 확인한다.
- [ ] publication·physical qualification 상태를 실제 receipt보다 앞서 표시하지 않는다.

## 15. 상세 문서

- [Server 운영, pairing, 관리자와 FFmpeg](server-pairing.md)
- [Synology DSM Server 운영](synology.md)
- [Web Control 배포와 운영](control-web.md)
- [Android Control 설치와 운영](control-android.md)
- [Windows Control 설치와 운영](control-windows.md)
- [Windows Renderer test harness](renderer-windows.md)
- [Release targets와 운영](releasing.md)
