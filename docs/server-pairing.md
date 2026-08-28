# Server 운영: 설정, pairing, 관리자, FFmpeg, K17

> 이 문서는 현재 구현된 Server를 설명한다. Server와 Control의 공개 배포는 아직
> 승인되지 않았고, 물리 K17 및 native Windows 오디오 자격 검증도 대기 중이다.

## 시작과 설치 경로

Linux 패키지는 `jastreamer-server.service`, `/etc/jastreamer/server.json`,
`/etc/jastreamer/server.env`, `/var/lib/jastreamer`를 사용한다. OCI 이미지는
`/usr/local/bin/jastreamer-server`, `/etc/jastreamer/server.json`,
`/var/lib/jastreamer`를 사용한다. 처음 시작할 때만 긴 bootstrap secret이 필요하다.

```sh
sudoedit /etc/jastreamer/server.env
sudo systemctl enable --now jastreamer-server.service
systemctl status jastreamer-server.service --no-pager
journalctl -u jastreamer-server.service -n 100 --no-pager
```

```text
JASTREAMER_SETUP_SECRET=<설치별 고엔트로피 secret>
```

첫 관리자가 생성된 뒤 security state가 남아 있으면 이 환경 변수 없이도 시작한다.
secret, bearer, pairing code를 명령 기록, URL, 로그, screenshot에 남기지 않는다.

정적 시작 설정은 `/etc/jastreamer/server.json`이다. 실제 배포 예제는
`packaging/server/server.json`과 Synology 문서를 기준으로 한다. 환경 변수
`JASTREAMER_ADDR`, `JASTREAMER_DATA_DIR`, `JASTREAMER_CATALOG_ROOT`,
`JASTREAMER_PAIRING_TTL`, `JASTREAMER_CERT_DNS`, `JASTREAMER_CERT_IPS`,
`JASTREAMER_ALLOWED_ORIGINS`가 파일보다 우선한다. 환경 변수로 잠긴 값은 Web에서
바꿀 수 없고 `CONFIG_FIELD_LOCKED`가 정상 결과다.

## 상태와 TLS identity

Linux package와 OCI container에서 각각 다음 status command를 사용한다.

```sh
systemctl is-active jastreamer-server.service
# OCI container 내부:
/usr/local/bin/jastreamer-server health
curl --fail --cacert <server-ca.pem> https://<server>:8443/healthz
curl --fail --cacert <server-ca.pem> https://<server>:8443/api/v1/identity
```

`sha256_fingerprint`를 Server 콘솔과 독립 경로로 비교한다. native Control은 인증서
DER 지문을 고정하지만 Web Control은 브라우저 TLS를 사용한다. 브라우저 pinning이나
경고 우회는 제공하지 않는다. data directory의 TLS identity가 바뀌면 자동 신뢰하지
말고 원인을 조사한 뒤 새 identity를 별도로 확인하고 다시 pairing한다.

## bootstrap과 pairing

`https://<server>:8443/pair/`에서 최초 관리자 이름과 setup secret을 입력한다.
성공한 admin token은 한 번만 보이며 포털은 현재 tab의 `sessionStorage`에만 둔다.
그 뒤 관리자는 controller 또는 renderer 역할을 지정해 일회용 code를 만든다.
code는 설정된 60~3600초 동안 한 번만 쓸 수 있고 소비자가 역할을 바꿀 수 없다.

API 사용 시 bearer는 shell history에 직접 쓰지 말고 보호된 임시 header 입력을 쓴다.

```http
POST /api/v1/bootstrap
POST /api/v1/pairing-codes
POST /api/v1/pairings
GET /api/v1/devices
DELETE /api/v1/devices/{deviceId}
```

`PAIRING_CODE_INVALID`, `PAIRING_CODE_EXPIRED`, `PAIRING_CODE_USED`에는 기존 code를
반복하지 말고 관리자가 새 code를 만든다. `PAIRING_RATE_LIMITED`면 입력을 중단한다.
renderer token은 Control/admin API에 접근할 수 없고 controller token은 renderer
session이나 media 권한을 얻지 못한다.

## `/admin/`과 revision 설정

관리자는 `/admin/`에서 display name, 최대 32개 catalog root, 정확한 HTTPS Control
origin, pairing TTL, 명시 UPnP interface, K17 media-only HTTP, 절대 FFmpeg 경로를
관리한다. listen address, data directory, 인증서 지문/SAN, 허용 catalog base는
읽기 전용이다. root는 허용 base 아래의 실제 경로여야 하고 symlink escape는 거부된다.

API 자동화는 먼저 ETag를 읽고 같은 revision으로 PATCH한다.

```sh
curl --fail --cacert <server-ca.pem> \
  -H 'Authorization: Bearer <admin-token>' \
  -D /tmp/jastreamer-config.headers \
  https://<server>:8443/api/v1/config

curl --fail-with-body --cacert <server-ca.pem> -X PATCH \
  -H 'Authorization: Bearer <admin-token>' \
  -H 'Content-Type: application/json' \
  -H 'If-Match: "<revision>"' \
  -H 'Idempotency-Key: <unique-operation-id>' \
  --data '{"pairing_ttl_seconds":300}' \
  https://<server>:8443/api/v1/config
```

누락된 `If-Match`는 `428 REVISION_REQUIRED`, stale 값은
`412 STALE_CONFIG_REVISION`, 잘못된 값은 `400 CONFIG_VALIDATION_FAILED`다. 최신
설정을 다시 읽고 보존된 의도를 검토한 뒤 새 idempotency key로 명시적으로 재시도한다.
`restart_required`와 `restart_fields`가 있으면 상태를 백업하고 Server를 재시작한다.

## catalog, Renderer와 K17

root 추가 후 `/admin/`에서 scan을 시작한다. readiness는 scan보다 먼저 올라오며 마지막
완전 snapshot을 계속 제공한다. search는 title/artist/album/album artist를 대상으로
하고 stale cursor는 `CATALOG_REVISION_CHANGED`다.

UPnP 지원 범위는 **FiiO K17 firmware V261 이상**뿐이다. 임의 UPnP 장치 지원을 뜻하지
않는다. 관리자가 private interface를 명시하고 discovery 결과의 model, firmware,
protocolInfo 상태를 확인한 뒤 zone에 할당한다. V260 이하나 다른 model은 unsupported
진단으로 남고 할당/재생하지 않는다. 현재 물리 K17 gate는 승인 대기이므로 emulator
통과만으로 release-ready가 아니다.

K17가 원본 MIME을 광고하면 원본을 먼저 제공한다. 원본이 맞지 않고 K17가 `audio/L16`을
받으며 FFmpeg probe도 성공한 경우에만 stereo 44.1 kHz, 16-bit big-endian L16으로
변환한다. Server intent, pending command, 실제 Renderer observed state는 서로 다른
값이며 `202`는 물리 성공이 아니라 작업 접수다. 외부에서 K17 재생을 바꿔도 Server
queue truth로 채택하지 않는다.

HTTPS를 받지 못하는 검증된 K17에 한해 `k17_http.enabled`를 명시적으로 켤 수 있다.
listener는 선택한 private interface 주소여야 하며 이 plaintext listener는 서명된
`GET/HEAD /media/v1/{token}`만 제공한다. API, admin, pairing, bearer media는 노출하지
않는다. `0.0.0.0`, public 주소, port forwarding은 거부한다.

## 외부 FFmpeg

FFmpeg는 포함되거나 자동 다운로드되지 않으며 PATH를 검색하지 않는다. 관리자가 설치하고
`ffmpeg_path`에 **절대 실행 파일 경로**를 지정한다.

- Debian/Ubuntu: 배포판의 서명된 package repository로 설치한 뒤 보통
  `/usr/bin/ffmpeg`를 지정한다: `sudo apt-get install ffmpeg`.
- OCI/Synology: 관리자가 제공한 실행 파일을 컨테이너에 read-only bind mount하고
  컨테이너 내부 절대 경로를 지정한다. 현재 기본 Compose에는 이 mount가 없으므로 직접
  추가해야 한다.
- Windows Server: 관리자가 검증한 `ffmpeg.exe`를 설치하고 `C:\...\ffmpeg.exe`를
  지정한다.

재시작 후 `/api/v1/config`의 `diagnostics.ffmpeg`에서 `status`, executable digest,
version, error code를 확인한다. FLAC/MP3/Vorbis/Opus/WAV decoder와 `pcm_s16be`가
필요하다. 미설치, 잘못된 경로, codec 누락 시 PCM fallback만 비활성화되고 호환 원본
재생은 유지된다. project SBOM은 외부 FFmpeg를 소유하거나 포함한다고 주장하지 않는다.

## Windows Server operator lifecycle

> 다음 PowerShell 명령은 Windows amd64 runner 전용이다. 이 Linux 작업에서는 실행하지
> 않았으며 native Windows Server install/status/remove qualification은 pending이다.

같은 candidate의 아래 파일과 record를 사용한다.

```text
jastreamer-server_<버전>_windows_amd64.msi
jastreamer-server_<버전>_windows_amd64.exe
server.cer
fingerprint.txt
SHA256SUMS
```

MSI는 service host와 core를 `C:\Program Files\jastreamer Server\`에 설치하고 SCM service
이름 `jastreamer-server`, display name `jastreamer Server`를 등록한다. 독립 EXE는 같은
core의 foreground artifact이며 service 설치를 하지 않는다. 두 artifact digest와
certificate SHA-256을 먼저 검증한다.

```powershell
Get-FileHash .\jastreamer-server_<버전>_windows_amd64.msi -Algorithm SHA256
Get-FileHash .\jastreamer-server_<버전>_windows_amd64.exe -Algorithm SHA256
certutil -dump .\server.cer
Import-Certificate -FilePath .\server.cer `
  -CertStoreLocation Cert:\LocalMachine\TrustedPeople
```

확인한 leaf certificate만 TrustedPeople에 넣는다. MSI는 trust가 없으면 install을
거부한다. 최초 start 전에 elevated PowerShell에서 service 전용 bootstrap environment를
설정하고 설치한다. registry 값이나 출력에 secret을 표시하지 않는다.

```powershell
$serviceKey = 'HKLM:\SYSTEM\CurrentControlSet\Services\jastreamer-server'
$msi = Resolve-Path .\jastreamer-server_<버전>_windows_amd64.msi
$install = Start-Process msiexec.exe `
  -ArgumentList @('/i', $msi, '/qn', '/norestart') -Wait -PassThru
if ($install.ExitCode -ne 0) { throw "MSI install failed: $($install.ExitCode)" }
New-ItemProperty -Path $serviceKey -Name Environment -PropertyType MultiString `
  -Value @('JASTREAMER_SETUP_SECRET=<보호된-bootstrap-secret>') -Force
Start-Service jastreamer-server
```

service wrapper는 core에 설치 directory의 `server.json`을 전달하고 data/catalog를
`C:\ProgramData\jastreamer\Server` 아래에 둔다. stderr는
`C:\ProgramData\jastreamer\Server\service.log`에 append한다.

```powershell
Get-Service jastreamer-server
Get-CimInstance Win32_Service -Filter "Name='jastreamer-server'"
Get-Content "$env:ProgramData\jastreamer\Server\service.log" -Tail 100
Stop-Service jastreamer-server
Start-Service jastreamer-server
```

foreground EXE를 service와 동시에 같은 data에 실행하지 않는다. 별도 disposable data와
config를 명시할 때만 사용한다.

```powershell
$env:JASTREAMER_SETUP_SECRET = '<보호된-bootstrap-secret>'
$env:JASTREAMER_DATA_DIR = "$env:TEMP\jastreamer-server-foreground"
& .\jastreamer-server_<버전>_windows_amd64.exe `
  --config 'C:\Program Files\jastreamer Server\server.json'
Remove-Item Env:\JASTREAMER_SETUP_SECRET
```

upgrade 전 service를 stop하고 SQLite online backup, 전체
`C:\ProgramData\jastreamer\Server`, 설치 directory의 `server.json`, 현재 MSI/digest를
보존한다. 검증한 newer MSI를 `/i`로 적용한 뒤 service와 health/config/catalog/queue를
확인한다. WiX `MajorUpgrade`는 older MSI downgrade를 명시적으로 거부한다. downgrade가
필요하면 new service를 stop/uninstall하고, old MSI를 설치한 뒤 **upgrade 전 data backup
전체**를 복구한다. 새 schema data에 old binary를 강제로 연결하지 않는다.

```powershell
Stop-Service jastreamer-server
$uninstall = Start-Process msiexec.exe `
  -ArgumentList @('/x', $msi, '/qn', '/norestart') -Wait -PassThru
if ($uninstall.ExitCode -ne 0) { throw "MSI uninstall failed: $($uninstall.ExitCode)" }
Get-Service jastreamer-server -ErrorAction SilentlyContinue
```

uninstall은 service와 Program Files payload를 제거하지만
`C:\ProgramData\jastreamer\Server`의 운영 data는 자동 삭제하지 않는다. device를 철회하고
backup 결정을 마친 뒤에만 해당 전용 data를 별도 삭제한다. 마지막으로 `fingerprint.txt`와
일치하고 subject가 `CN=jastreamer`인 certificate만 제거한다.

```powershell
$wanted = ((Get-Content .\fingerprint.txt) -replace '^SHA256:\s*','' -replace ':','').Trim()
Get-ChildItem Cert:\LocalMachine\TrustedPeople | Where-Object {
  $_.Subject -eq 'CN=jastreamer' -and
  ((Get-FileHash -InputStream ([IO.MemoryStream]::new($_.RawData)) `
    -Algorithm SHA256).Hash -eq $wanted)
} | Remove-Item
```

## 백업, 복구, upgrade와 downgrade

중지 전후가 섞인 파일 복사 대신 SQLite online backup을 사용하고 다음을 함께 보존한다.

- 전체 data directory(설정, DB, security state, TLS identity, migration backup)
- `/etc/jastreamer/server.json` 및 환경 설정의 비밀 제외 기록
- 정확한 설치 버전/이미지 digest와 관리자 credential

upgrade 전에 backup과 integrity check를 완료한다. upgrade 뒤 `/healthz`, identity,
admin config revision, devices, catalog, zones/queue를 확인한다. 새 schema를 읽지 못하는
구버전은 downgrade를 거부한다. downgrade가 필요하면 Server를 중지하고 **upgrade 전
backup 전체**와 이전 binary/image를 함께 복구한다. 빈 data directory로 교체하지 않는다.

## 철회, 제거와 장애 복구

분실 장치는 `/admin/` 또는 `DELETE /api/v1/devices/{deviceId}`로 즉시 철회한다.
다음 요청은 `401 TOKEN_REVOKED`이며 Control은 저장 token을 지우고 새 code로 pairing해야
한다. 마지막 admin은 철회할 수 없다.

Linux package 제거는 service/binary만 제거하고 복구를 위해 `/var/lib/jastreamer`와
service account를 남긴다. 완전 삭제는 backup과 device 철회를 마친 뒤 관리자가 해당
경로를 별도로 삭제한다. 인증서 trust도 정확한 게시 지문을 확인한 뒤에만 제거한다.

안전한 장애 결과:

| 증상 | 상태 | 복구 |
| --- | --- | --- |
| FFmpeg 없음/불일치 | `diagnostics.ffmpeg.status`가 unconfigured/unavailable/incompatible | 절대 경로와 codec을 고치고 재시작; 원본 경로는 유지 |
| K17 V261 미만/다른 model | unsupported discovery 진단, zone 재생 없음 | 지원 장치/firmware를 사용; 범용 UPnP로 우회하지 않음 |
| 물리 gate 없음 | `awaiting_external_authorization`, publication denied | 승인된 물리 runner receipt를 기다림 |
| invalid config | `CONFIG_VALIDATION_FAILED`, active revision 불변 | field/rule을 수정해 새 revision으로 재제출 |
| revoked token | `401 TOKEN_REVOKED` | 저장 credential 삭제 후 새 code로 pairing |
