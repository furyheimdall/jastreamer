# Server bootstrap, pairing, 복구

jastreamer Server는 HTTPS `/pair/` 포털에서 최초 관리자를 만들고,
일회용 code로 Controller를 등록한다.

## 시작 설정

Server 시작에는 `JASTREAMER_SETUP_SECRET`이 필요하다.

```sh
export JASTREAMER_SETUP_SECRET='<설치별로 생성한 긴 secret>'
export JASTREAMER_ADDR=':8443'
export JASTREAMER_PAIRING_TTL='5m'
export JASTREAMER_CERT_DNS='music-server.local'
export JASTREAMER_CERT_IPS='192.168.1.20'
```

주요 변수:

| 변수 | 기본값 | 용도 |
| --- | --- | --- |
| `JASTREAMER_ADDR` | `:8443` | HTTPS listen 주소 |
| `JASTREAMER_DATA_DIR` | `./data` | DB, security state, TLS identity |
| `JASTREAMER_CATALOG_ROOT` | data 아래 `catalog` | 음악 경로 |
| `JASTREAMER_SETUP_SECRET` | 없음 | 최초 관리자 bootstrap |
| `JASTREAMER_PAIRING_TTL` | `5m` | 일회용 code 유효 시간 |
| `JASTREAMER_CERT_DNS` | `localhost` | 쉼표 구분 인증서 DNS |
| `JASTREAMER_CERT_IPS` | loopback | 쉼표 구분 인증서 IP |
| `JASTREAMER_ALLOWED_ORIGINS` | 설정 파일 값 | Web Control CORS origin |

설정 파일 실행:

```sh
jastreamer-server --config /etc/jastreamer/server.json
```

`JASTREAMER_SETUP_SECRET`을 URL, 로그, Compose 파일, 화면 캡처에 남기지
않는다. 현재 별도 first-admin recovery API는 없으므로 data/security state와
관리자 credential을 반드시 백업한다.

## HTTPS identity 확인

Server 준비 로그와 `/pair/` 포털은 인증서 SHA-256 지문을 표시한다.

```text
https://<server-address>:8443/pair/
```

공개 identity API:

```http
GET /api/v1/identity
```

응답의 `sha256_fingerprint`와 Server 콘솔 또는 관리자가 별도 경로로
제공한 지문을 비교한다. 다르면 bootstrap이나 pairing을 진행하지 않는다.
인증서 identity가 저장된 data directory를 잃으면 지문이 바뀔 수 있다.

## 최초 관리자 bootstrap

`/pair/`의 **First administrator**에서 장치 이름과 setup secret을
입력한다. API 계약은 다음과 같다.

```http
POST /api/v1/bootstrap
Content-Type: application/json
```

```json
{
  "setup_secret": "<JASTREAMER_SETUP_SECRET>",
  "name": "관리자 장치"
}
```

성공 시 `201 Created`와 admin token이 한 번 반환된다. Server는 token
원문이 아니라 SHA-256 digest를 저장한다. 포털은 admin token을 현재
브라우저의 `sessionStorage`에만 둔다.

- 잘못된 secret: `401 BOOTSTRAP_SECRET_INVALID`
- 이미 완료됨: `409 BOOTSTRAP_COMPLETE`
- 잘못된 요청/장치 이름: `400 INVALID_REQUEST`

bootstrap 이후 setup secret으로 새 관리자를 만들 수 없다. admin token과
Server data를 안전하게 보관한다.

## Controller pairing

### 1. 관리자가 code 생성

포털의 **Generate code**를 사용하거나 다음 API를 호출한다.

```http
POST /api/v1/pairing-codes
Authorization: Bearer <admin-token>
Content-Type: application/json

{}
```

응답은 6자리 code와 만료 시각을 포함한다. 기본 유효 시간은 5분이고 한
번만 사용할 수 있다. Controller token으로 호출하면 `403 ADMIN_REQUIRED`다.

### 2. 새 장치 등록

```http
POST /api/v1/pairings
Content-Type: application/json
```

```json
{
  "code": "123456",
  "name": "거실 Controller"
}
```

성공 시 `201 Created`와 controller token이 한 번 반환된다. 즉시 안전한
곳으로 옮기고 이후 요청에 사용한다.

```http
Authorization: Bearer <controller-token>
```

오류:

| 상태 | 코드 | 조치 |
| --- | --- | --- |
| 400 | `PAIRING_CODE_INVALID` | 입력 확인 |
| 410 | `PAIRING_CODE_EXPIRED` | 새 code 생성 |
| 409 | `PAIRING_CODE_USED` | 새 code 생성 |
| 429 | `PAIRING_RATE_LIMITED` | 반복 입력 중지 후 새 code 사용 |

token을 URL, 브라우저 history, 로그 또는 스크린샷에 넣지 않는다.

## 장치 조회와 철회

관리자만 장치 목록과 철회를 수행한다.

```http
GET /api/v1/devices
Authorization: Bearer <admin-token>
```

```http
DELETE /api/v1/devices/<device-id>
Authorization: Bearer <admin-token>
```

철회 성공은 `204 No Content`다. 해당 token은 다음 요청부터
`401 TOKEN_REVOKED`로 거부된다. Controller token으로 관리자 API를
호출하면 `403 ADMIN_REQUIRED`다.

분실하거나 공유된 장치는 즉시 철회하고 새 code로 다시 등록한다.

## 백업과 복구

함께 보존해야 하는 항목:

- `JASTREAMER_DATA_DIR` 또는 Compose의 `JASTREAMER_DATA_PATH`
- Server security state와 TLS identity
- `server.json`
- admin credential
- 설치 시 사용한 이미지 버전/digest

SQLite migration 전에는 online backup을 만들고 integrity check를
통과한 사본을 보관한다. 복구는 Server를 중지한 뒤 원래 data/config
경로로 수행한다.

복구 후 확인:

1. `/healthz`
2. `/api/v1/identity`의 인증서 지문
3. `/pair/` 관리자 세션
4. 등록 장치 목록
5. Controller 인증
6. catalog와 queue 상태

security state만 잃으면 기존 token을 복구할 수 없다. TLS identity를
잃어 지문이 바뀌면 새 지문을 독립적으로 확인하고 Controller를 다시
pairing한다.

## API 요약

| 메서드 | 경로 | 권한 |
| --- | --- | --- |
| GET | `/pair/` | public |
| GET | `/api/v1/identity` | public |
| POST | `/api/v1/bootstrap` | public/setup secret |
| POST | `/api/v1/pairing-codes` | admin |
| POST | `/api/v1/pairings` | public/code |
| GET | `/api/v1/devices` | admin |
| DELETE | `/api/v1/devices/{deviceId}` | admin |
| GET | `/healthz` | public |
