# Web Control 배포와 운영

> Control Web 공개 ZIP은 아직 출시되지 않았다. exact staged artifact와 product receipt가
> 모두 qualified되기 전에는 publication-ready가 아니다.

## 배포

candidate 이름은 `jastreamer-control_<버전>_web.zip`이다. ZIP을 HTTPS static host의
한 release directory에 풀고 원자적으로 활성 directory를 전환한다.

```sh
unzip -t jastreamer-control_<버전>_web.zip
mkdir -p /srv/www/jastreamer-control/<버전>
unzip jastreamer-control_<버전>_web.zip -d /srv/www/jastreamer-control/<버전>
```

root 배포가 기본이다. 하위 path는 Flutter base href와 host fallback을 함께 바꿔야 한다.
`index.html`, `flutter_bootstrap.js`, `main.dart.js`, `manifest.json`을 같은 release에서
제공하고 HTML/JS가 다른 version으로 섞이지 않게 한다.

Server의 `control_origins`에는 path나 trailing slash가 없는 정확한 HTTPS origin만 둔다.
wildcard와 `Access-Control-Allow-Origin: *`는 지원하지 않는다.

## TLS, pairing과 token

브라우저가 정상 TLS chain/hostname을 검증한다. Web 앱은 인증서 pinning이나 TLS 경고
우회를 하지 않는다. Server identity fingerprint는 독립 경로로 비교해 identity change를
발견하는 보조값이다. 새 인증서를 자동 신뢰하지 않는다.

1. Server `/pair/`를 정상 TLS로 연다.
2. 관리자가 controller 역할의 one-time code를 만든다.
3. Control에서 Server HTTPS origin과 별도로 확인한 fingerprint를 입력한다.
4. code를 소비해 한 번 표시되는 controller token을 Control에 붙여 넣는다.
5. discovery가 protocol major 3과 required capabilities를 선택하는지 확인한다.

Web token은 해당 tab/browser session의 `sessionStorage`에만 있고 `localStorage`, URL,
history에는 저장하지 않는다. tab/browser session 종료, site-data 삭제, identity change,
revocation 시 다시 pairing한다.

## browse, queue와 transport

**Browse catalog**에서 title/artist/album/album artist를 검색하고 Server가 `available`로
표시한 track만 explicit queue에 추가한다. stale catalog cursor는 전체 page를 새로
읽는다. Control이 file path나 availability를 만들지 않는다.

Explicit queue 작업은 add, remove, earlier/later reorder, clear, retry blocked, skip blocked다.
active entry 보호나 stale revision 오류가 나면 Server truth를 refresh하고 보존된 intent를
검토한 뒤 수동으로 다시 실행한다. automatic preview는 read-only이며 queue row가 아니다.

Transport 작업은 Previous, Play/Start, Pause, Resume, Next/Skip, Stop, Seek다. enqueue는
자동 시작하지 않는다. Start는 online assigned Renderer가 필요하다. Stop은 next로
진행하지 않고, Next는 current stop 확인 후 다음을 선택한다. Previous는 재생 5초 이후면
0으로 seek하고 그 전이면 retained history를 선택한다. 지원하지 않는 seek는 안전하게
실패한다.

## 상태 truth와 복구

화면의 **Server intent**, **Renderer observed**, **pending command**는 서로 다른 truth다.
HTTP `202`나 pending 표시는 실제 speaker 성공을 뜻하지 않는다. event는 invalidation일
뿐이며 Control은 matching resource를 REST로 다시 읽는다. event sequence/epoch gap,
overflow, reconnect에는 bounded full resync를 수행하고 stale 값을 사실로 표시하지 않는다.

- `TOKEN_REVOKED`: session token을 지우고 새 code로 pairing한다.
- certificate identity change: 중지하고 새 identity를 독립 검증한 뒤 pairing한다.
- stale revision/conflict: 자동 mutation retry 없이 Server truth를 refresh한다.
- offline Renderer: assignment/network/K17 status를 확인하고 online 뒤 명시적으로 retry한다.
- blocked track: 원인을 고친 뒤 Retry blocked 또는 Skip blocked를 선택한다.
- Server offline: reconnect 뒤 full state를 읽는다. Control이 next track을 계산하지 않는다.

## update, rollback과 제거

새 ZIP은 별도 directory에 검증·압축 해제하고 활성 pointer만 전환한다. rollback은 이전
검증 ZIP으로 pointer를 돌리되 현재 Server와 protocol major가 겹치는지 확인한다. Web
rollback은 Server DB, queue, device를 되돌리지 않는다.

제거할 때 Server admin에서 Web Control device를 철회하고 static files, service worker,
site data를 삭제한다. browser/OS trust store를 변경했다면 정확히 확인한 Server
certificate만 제거한다. 다른 site certificate를 삭제하지 않는다.
