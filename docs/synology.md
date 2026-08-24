# Synology DSM 설치, 업데이트, 롤백

> 현재 GHCR 이미지와 GitHub Release는 아직 공개되지 않았다. 이 문서는
> `deploy/docker/server/compose.synology.yaml`의 배포 계약을 설명한다.
> 설치할 때는 실제로 게시된 버전 태그 또는 digest를 확인해야 한다.

## 지원 범위

| 대상 | OCI 플랫폼 | 현재 상태 |
| --- | --- | --- |
| DS918+, DSM 7.2.2 | `linux/amd64` | `candidate-pending-runtime-authorization` |
| Synology arm64 | `linux/arm64` | `unverified` |
| Synology ARMv7 | - | `unsupported` |

Server 이미지는 `linux/amd64`와 `linux/arm64`만 대상으로 한다. SPK는
제공하지 않으며 Container Manager의 Compose 프로젝트로 설치한다.

## 사전 준비

NAS에 다음 디렉터리를 만든다.

```text
/volume1/docker/jastreamer/
├── config/
│   └── server.json
└── data/
```

`data`는 카탈로그와 SQLite 상태를 보존한다. 컨테이너는 UID/GID
`10001:10001`로 실행되므로 이 사용자가 데이터 디렉터리에 쓸 수 있어야
한다. `config`는 읽기 전용으로 마운트된다.

설정 파일은 `packaging/server/server.json`을 환경에 맞게 복사한다.
특히 실제 접속 주소를 TLS 인증서 이름/IP에 포함한다.

```json
{
  "address": ":8443",
  "data_directory": "/var/lib/jastreamer",
  "catalog_root": "/var/lib/jastreamer/catalog",
  "catalog_migration": "/usr/lib/jastreamer-server/migrations/001_catalog.sql",
  "playback_migration": "/usr/lib/jastreamer-server/migrations/002_playback.sql",
  "playback_expansion": "/usr/lib/jastreamer-server/migrations/003_todo12.sql",
  "certificate_dns": ["music-server.local"],
  "certificate_ips": ["192.168.1.20"],
  "allowed_origins": [],
  "pairing_ttl": "5m"
}
```

## 필수 환경 변수

| 변수 | 설명 |
| --- | --- |
| `JASTREAMER_SERVER_IMAGE` | 게시된 이미지 태그 또는 immutable digest |
| `JASTREAMER_SETUP_SECRET` | 최초 관리자 bootstrap secret |
| `JASTREAMER_DATA_PATH` | 기본값 `/volume1/docker/jastreamer/data` |
| `JASTREAMER_CONFIG_PATH` | 기본값 `/volume1/docker/jastreamer/config` |

추가로 `JASTREAMER_ADDR`, `JASTREAMER_CATALOG_ROOT`,
`JASTREAMER_PAIRING_TTL`, `JASTREAMER_CERT_DNS`,
`JASTREAMER_CERT_IPS`, `JASTREAMER_ALLOWED_ORIGINS`를 사용할 수 있다.
환경 변수는 `server.json`보다 우선한다.

`JASTREAMER_SETUP_SECRET`은 Compose 파일에 직접 기록하지 말고 Container
Manager의 프로젝트 환경 변수나 별도 secret 관리 수단으로 주입한다.

## Container Manager 설치

1. DSM에서 **Container Manager → 프로젝트 → 생성**을 연다.
2. 프로젝트 이름을 `jastreamer`로 지정한다.
3. 저장소의 `deploy/docker/server/compose.synology.yaml`을 사용한다.
4. 위 네 환경 변수를 설정한다.
5. `JASTREAMER_SERVER_IMAGE`에는 실제 게시 여부를 확인한 버전 또는
   digest를 입력한다. `latest`는 사용하지 않는다.
6. 프로젝트를 배포한다.

CLI를 사용할 수 있다면 배포 전에 Compose를 렌더링한다.

```sh
JASTREAMER_SETUP_SECRET=compose-validation \
JASTREAMER_CONFIG_PATH=/volume1/docker/jastreamer/config \
JASTREAMER_DATA_PATH=/volume1/docker/jastreamer/data \
docker compose -f deploy/docker/server/compose.synology.yaml config
```

Compose의 보안 계약은 다음과 같다.

- `network_mode: host`
- 비루트 `10001:10001`
- 읽기 전용 root filesystem
- 모든 capability 제거
- `no-new-privileges`
- privileged 모드와 임의 포트 매핑 없음

UPnP 검색 때문에 host network를 사용한다.

## 시작 확인

healthcheck는 다음 명령을 30초 간격으로 실행한다.

```sh
/usr/local/bin/jastreamer-server health
```

HTTPS API는 다음 경로로도 확인할 수 있다.

```text
GET https://<NAS 주소>:8443/healthz
```

healthy가 되지 않으면 다음을 확인한다.

- `JASTREAMER_SETUP_SECRET`이 비어 있지 않은가
- `/etc/jastreamer/server.json`이 컨테이너에서 보이는가
- `data`가 UID/GID `10001:10001`로 쓰기 가능한가
- NAS의 8443 포트를 다른 프로세스가 사용하지 않는가
- 인증서 DNS/IP가 Controller가 사용할 주소와 일치하는가

최초 관리자와 Controller pairing은
[Server bootstrap 및 pairing](server-pairing.md)을 따른다.

## 업데이트

1. Container Manager 프로젝트를 중지한다.
2. `data`, `config/server.json`, 현재 이미지 태그/digest를 백업한다.
3. 실제 게시된 새 이미지의 플랫폼과 digest를 확인한다.
4. `JASTREAMER_SERVER_IMAGE`를 새 버전 또는 digest로 바꾼다.
5. 프로젝트를 다시 배포한다.
6. healthcheck, `/healthz`, pairing, catalog, queue 상태를 확인한다.

Server 릴리스 워크플로우는 하나의 OCI index에 정확히
`linux/amd64`, `linux/arm64`를 넣도록 검증한다.

## 롤백

1. 프로젝트를 중지한다.
2. 업데이트 전에 기록한 이전 태그 또는 digest로 이미지 참조를 되돌린다.
3. 같은 데이터와 설정 bind mount를 유지한다.
4. migration 이후 하위 버전 호환이 보장되지 않으면 업데이트 전 SQLite
   백업도 함께 복구한다.
5. 프로젝트를 시작하고 health, portal, catalog, pairing을 확인한다.

빈 볼륨으로 바꾸거나 `data`를 삭제하는 것은 롤백이 아니다.

## 백업 및 복구

최소 백업 대상:

- `/volume1/docker/jastreamer/data`
- `/volume1/docker/jastreamer/config/server.json`
- 사용한 이미지 태그와 digest
- 비밀값을 제외한 환경 설정 기록

SQLite는 가능한 경우 online backup으로 일관된 사본을 만든다.
`JASTREAMER_SETUP_SECRET`은 데이터 백업과 분리해 안전하게 보관한다.

복구 후에는 health뿐 아니라 인증서 지문, 관리자 세션, Controller pairing,
catalog와 queue 상태까지 확인한다.

## 제한

- ARMv7은 지원하지 않는다.
- DS918+는 인증 완료가 아니라 런타임 승인 대기 후보이다.
- Synology arm64 실기기는 아직 검증되지 않았다.
- 문서의 기본 이미지 문자열은 실제 게시를 보장하지 않는다.
- bridge network, privileged mode, 익명 데이터 볼륨은 지원 배포 계약이
  아니다.
