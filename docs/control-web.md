# Web Control 배포 및 사용

> 현재 공개 릴리스는 아직 생성되지 않았다. Control Web은 Server의
> `/pair/` 관리 포털과 별도로 배포하는 Flutter Web PWA이다.

## 빌드 및 산출물

공개 산출물 이름:

```text
jastreamer-control_<버전>_web.zip
```

로컬 빌드:

```sh
cd apps/control
flutter pub get --enforce-lockfile
flutter build web --release --build-name '<버전>'
```

`apps/control/build/web`의 내용을 ZIP 루트에 담아 HTTPS 정적 호스트에
배포한다. 공식 워크플로우는 ZIP을 생성하지만 정적 호스트 배포까지
자동화하지 않는다.

## HTTPS 정적 배포

예:

```text
Control Web: https://control.example.local/
Server:      https://music-server.example.local:8443/
```

ZIP의 `index.html`, `flutter_bootstrap.js`, `manifest.json`, icons와
나머지 파일을 같은 웹 루트에 둔다. 기본 빌드는 `/` scope를 사용하므로
하위 경로 배포 시 `<base href>`와 웹 서버 fallback 설정을 별도로 맞춰야
한다.

배포 후 다음을 확인한다.

- `/`가 `index.html`을 반환하는가
- `/manifest.json`과 Flutter bootstrap 파일이 200을 반환하는가
- 모든 정적 파일이 HTTPS로 제공되는가

## Server TLS와 CORS

브라우저는 자체 TLS 검증을 사용한다. 자체 서명 Server 인증서를 사용하는
경우 OS/브라우저에서 정확한 인증서를 먼저 신뢰해야 한다. 인증서 경고를
무시하는 것은 지원 절차가 아니다.

Server 인증서에는 실제 접속 DNS/IP가 포함되어야 한다.

```sh
JASTREAMER_CERT_DNS=music-server.example.local
JASTREAMER_CERT_IPS=192.168.1.20
```

Control Web과 Server가 다른 origin이면 정확한 Control origin을
허용한다.

```sh
JASTREAMER_ALLOWED_ORIGINS=https://control.example.local
```

여러 origin은 쉼표로 구분한다. origin에는 path나 끝 `/`를 넣지 않는다.

```text
https://control.example.local
https://control.example.local:8443
```

허용되지 않은 browser API/WebSocket 요청은 `ORIGIN_FORBIDDEN`으로
거부된다. `Access-Control-Allow-Origin: *`로 우회하지 않는다.

## 서버 찾기와 Pairing

1. 브라우저에서 Server 주소를 직접 열어 TLS 신뢰와 인증서 지문을
   확인한다.
2. Control Web의 **Server HTTPS address**에 Server 주소를 입력한다.
3. **Discover Server**를 누른다.
4. Server 카드의 주소와 SHA-256 지문을 별도 경로로 받은 값과 비교한다.
5. **Open pairing page**로 Server의 `/pair/` 포털을 연다.
6. 관리자가 5분 유효한 일회용 code를 생성한다.
7. Web 기기를 등록하고 한 번 표시되는 controller token을 복사한다.
8. Control Web의 **Complete pairing return**에 token을 입력한다.
9. 다시 discovery하여 인증과 protocol 협상을 확인한다.

Web에서는 앱이 TLS 인증서를 직접 우회하거나 승인하지 않는다. 브라우저
신뢰와 사용자의 지문 비교를 모두 통과해야 한다.

## 사용

pairing 후 다음 기능을 사용할 수 있다.

- catalog와 분석 상태 확인
- explicit queue 조회와 변경
- continuation policy 조회와 저장
- automatic preview와 decision 설명
- Server 상태 새로 고침
- 인증된 WebSocket 상태 이벤트

관리자 작업과 Controller 작업은 구분된다. pairing code 생성이나 device
철회는 관리자 권한이 필요하다.

## 업데이트

1. 새 ZIP을 별도 디렉터리에 푼다.
2. `unzip -t`와 파일 digest를 확인한다.
3. 새 디렉터리를 정적 호스트에 배포한다.
4. symlink나 원자적 디렉터리 전환으로 새 버전을 활성화한다.
5. bootstrap 파일, discovery, pairing과 protocol 호환성을 확인한다.

페이지 새로고침 뒤 token이 유지된다고 가정하지 않는다. 필요하면 다시
pairing한다.

## 롤백

이전 검증 ZIP을 보관하고 정적 호스트의 활성 디렉터리를 이전 버전으로
되돌린다. 다음을 함께 확인한다.

- 이전 Web이 현재 Server protocol major를 지원하는가
- Control origin이 바뀌지 않았는가
- 브라우저/CDN이 새 파일과 이전 파일을 혼합해 제공하지 않는가

Web 롤백은 Server 데이터, catalog, queue 또는 device 등록을 되돌리지
않는다.

## PWA와 캐시

manifest는 `/` start URL과 scope, standalone 표시를 정의한다. 실제
service worker와 캐시 동작은 해당 Flutter 빌드 산출물 및 브라우저 버전에
따른다. 완전한 오프라인 동작을 보장하지 않는다.

업데이트가 보이지 않으면:

1. 일반 새로고침을 한다.
2. 사이트 데이터와 service worker/cache 상태를 확인한다.
3. CDN의 HTML 및 JavaScript 캐시를 무효화한다.
4. 새 `flutter_bootstrap.js`와 `main.dart.js`가 제공되는지 확인한다.

Server API 응답은 `Cache-Control: no-store`이므로 인증 응답과 제어
상태를 CDN이나 브라우저에서 캐시하지 않는다.

## 제한

- HTTPS와 정확한 CORS origin 구성이 필요하다.
- Browser TLS 경고를 앱이 대신 승인하지 않는다.
- 자동 정적 호스트 배포와 자동 롤백은 포함되지 않는다.
- token의 장기 브라우저 저장을 보장하지 않는다.
- 호환 protocol major가 없으면 discovery와 제어가 실패한다.
