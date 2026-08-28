# Synology DSM Server 운영

> 공개 GHCR 이미지는 아직 없고 Container Manager 실기기 변경 승인도 false다.
> DS918+ DSM 7.2.2는 `candidate-pending-runtime-authorization`, Synology arm64는
> `unverified`, ARMv7은 `unsupported`다. 모든 Synology를 지원한다는 뜻이 아니다.

## 지원 계약과 준비

배포 방식은 SPK가 아니라 `deploy/docker/server/compose.synology.yaml` 기반 Compose다.
OCI 대상은 정확히 `linux/amd64`, `linux/arm64`다. UPnP/K17 검색 때문에 host network를
사용하며 container는 UID/GID `10001:10001`, read-only root, dropped capabilities,
`no-new-privileges`로 실행된다.

```text
/volume1/docker/jastreamer/
├── config/server.json
├── data/
├── media/
└── external/ffmpeg/       # 선택, 관리자가 제공
```

`data`와 허용 media root는 UID/GID 10001이 접근할 수 있어야 한다. repository의
`packaging/server/server.json`을 복사하고 컨테이너 내부 경로를 사용한다. 기본 catalog
root `/var/lib/jastreamer/catalog`를 NAS 음악에 연결하려면 Compose에 read-only bind
mount를 명시한다. 임의 host path 탐색은 지원하지 않는다.

필수 project 변수:

```text
JASTREAMER_SERVER_IMAGE=<실제로 staged된 immutable digest>
JASTREAMER_SETUP_SECRET=<최초 bootstrap용 secret>
JASTREAMER_DATA_PATH=/volume1/docker/jastreamer/data
JASTREAMER_CONFIG_PATH=/volume1/docker/jastreamer/config
```

`latest`나 문서 예제의 미게시 tag를 사용하지 않는다. secret을 Compose YAML에 저장하지
않는다. **현재 기본 Compose는 매 expansion과 start에서 non-empty
`JASTREAMER_SETUP_SECRET`을 요구하고 그대로 container에 전달한다.** 최초 admin 생성 뒤
Server는 이 값으로 다시 bootstrap하지 않지만 Compose 계약은 계속 값을 요구한다. 따라서
Container Manager의 보호된 project 변수에 같은 secret을 유지하거나, 별도로 검토한
operator override에서 해당 environment 항목과 required expansion을 함께 제거해야 한다.
기본 파일을 그대로 쓰면서 변수만 삭제하면 `docker compose config`부터 실패한다.

## disposable config 검사와 배포

현재 repository 경로에서 외부 write 없이 Compose expansion을 검사할 수 있다.

```sh
JASTREAMER_SERVER_IMAGE='ghcr.io/furyheimdall/jastreamer-server@sha256:<digest>' \
JASTREAMER_SETUP_SECRET='validation-only-not-production' \
JASTREAMER_CONFIG_PATH='/volume1/docker/jastreamer/config' \
JASTREAMER_DATA_PATH='/volume1/docker/jastreamer/data' \
docker compose -f deploy/docker/server/compose.synology.yaml config
```

DSM에서 **Container Manager → 프로젝트 → 생성** 후 같은 파일과 변수를 사용한다.
배포 뒤 상태:

```sh
docker compose -f deploy/docker/server/compose.synology.yaml ps
docker compose -f deploy/docker/server/compose.synology.yaml logs --tail 100 jastreamer-server
docker compose -f deploy/docker/server/compose.synology.yaml exec jastreamer-server \
  /usr/local/bin/jastreamer-server health
```

`/pair/` bootstrap과 `/admin/` 설정은 [Server 운영](server-pairing.md)을 따른다.
health가 실패하면 config mount, 10001 쓰기 권한, 8443 충돌, 인증서 SAN, migration 경로를
확인한다. 잘못된 config를 고치기 위해 data를 삭제하지 않는다.

## catalog, K17, FFmpeg

K17 지원은 FiiO K17 V261 이상에만 한정한다. private LAN의 명시 interface를 `/admin/`에
설정한다. host networking은 범용 UPnP 지원이나 자동 장치 신뢰를 의미하지 않는다.
물리 K17 gate는 현재 pending이며 publication readiness를 제공하지 않는다.

호환 원본을 항상 먼저 stream한다. L16 fallback이 필요하면 관리자가 Synology CPU
architecture에 맞는 FFmpeg를 취득·검증한다. project는 FFmpeg를 배포하거나 다운로드하지
않는다. canonical override `deploy/docker/server/compose.ffmpeg.yaml`은 host executable을
container의 `/opt/jastreamer-external/ffmpeg`에 read-only로 mount한다.

```sh
JASTREAMER_SERVER_IMAGE='ghcr.io/furyheimdall/jastreamer-server@sha256:<digest>' \
JASTREAMER_SETUP_SECRET='<보호된-project-secret>' \
JASTREAMER_CONFIG_PATH='/volume1/docker/jastreamer/config' \
JASTREAMER_DATA_PATH='/volume1/docker/jastreamer/data' \
JASTREAMER_FFMPEG_PATH='/volume1/docker/jastreamer/external/ffmpeg/ffmpeg' \
docker compose \
  -f deploy/docker/server/compose.synology.yaml \
  -f deploy/docker/server/compose.ffmpeg.yaml config
```

같은 두 `-f` 인수를 Container Manager project 배포 또는 `docker compose up -d`에도
사용한다. `/admin/`의 `ffmpeg_path`는 정확히
`/opt/jastreamer-external/ffmpeg`로 설정한다. 실행 권한과
`diagnostics.ffmpeg` probe를 확인한다. FFmpeg가 없으면 L16만 disabled다.

K17가 self-signed HTTPS media를 받지 못한다는 물리 검증이 있을 때만 private interface의
media-only HTTP listener를 opt-in한다. 이 listener는 signed media GET/HEAD 전용이며
admin/API를 제공하지 않는다. NAS firewall/router에서 외부에 노출하지 않는다.

## backup, upgrade, rollback

upgrade 전 프로젝트를 중지하고 SQLite 일관성 backup, `data`, `config/server.json`,
현재 image digest, 비밀 제외 환경 기록을 보존한다. 새 digest로 바꾼 뒤 health, identity
fingerprint, admin config revision, devices, catalog scan, zone/queue를 확인한다.

schema upgrade 뒤 old image가 새 data를 읽는다고 가정하지 않는다. old binary의 downgrade
거부는 안전 장치다. rollback은 프로젝트를 중지하고 **이전 image digest와 upgrade 전
전체 data backup을 함께** 복구한 뒤 시작한다. 빈 volume이나 부분 DB 교체는 rollback이
아니다.

## 제거와 trust removal

```sh
docker compose -f deploy/docker/server/compose.synology.yaml down
```

이 명령은 bind-mounted data를 지우지 않는다. 영구 제거 전에 모든 device token을
철회하고 backup 정책을 확인한다. 그 뒤 관리자가 project와 전용 data/config/media bind
경로만 삭제한다. shared media나 다른 certificate를 삭제하지 않는다.

## 현재 qualification gate

- `candidate`: exact OCI digest와 구조를 묶는 stage gate
- `server_control_e2e`: 설치된 Server/Control workflow gate
- `k17`: 승인된 물리 K17 V261+ gate
- `wasapi`: 승인된 native Windows audio runner gate
- `external_authorization_pending`: device/audio 변경 0과 publication denied를 증명하는
  pending receipt

현재 K17/Windows native qualification이 pending이므로 workflow나 emulator 성공만으로
Server/Control을 공개하면 안 된다.
