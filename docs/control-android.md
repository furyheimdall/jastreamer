# Android Control 설치와 운영

> universal APK는 아직 공개 출시되지 않았다. Android native install/upgrade와 installed
> product qualification이 완료되기 전에는 publication-ready가 아니다.

## candidate 확인과 설치

candidate 이름은 `jastreamer-control_<버전>_android_universal.apk`, package ID는
`io.jastreamer.control`이다. 같은 candidate set의 `SHA256SUMS`와
`Android-CERT-SHA256.txt`를 사용한다.

```sh
apksigner verify --verbose --print-certs \
  jastreamer-control_<버전>_android_universal.apk
unzip -Z1 jastreamer-control_<버전>_android_universal.apk \
  | grep -E '^lib/(armeabi-v7a|arm64-v8a|x86_64)/libapp\.so$' | sort -u
adb install jastreamer-control_<버전>_android_universal.apk
```

signer digest가 다르거나 `armeabi-v7a`, `arm64-v8a`, `x86_64` 중 하나가 없으면 설치하지
않는다. sideload 권한은 설치 뒤 다시 끈다. AAB와 keystore는 public asset이 아니다.

## pairing과 secure token

1. Server HTTPS origin과 독립 확인한 certificate SHA-256을 입력한다.
2. discovery 결과와 protocol major 3 capabilities를 확인한다.
3. 관리자가 `/pair/`에서 controller 역할 one-time code를 만든다.
4. code를 소비하고 한 번 표시되는 token을 Control에 입력한다.
5. 앱을 재시작해 reconnect를 확인한다.

Android Control은 Android Keystore AES-GCM key로 token record를 암호화하고
`noBackupFilesDir`에 둔다. 정상 same-signer update와 app restart에서는 유지된다. Android
backup/restore, 다른 app, invalidated key, copied/corrupt record는 fail closed하고 record를
삭제하므로 다시 pairing한다. token을 URL, clipboard history, log, screenshot에 보존하지
않는다.

## browse, queue, transport와 상태

Browse/Search는 Server catalog의 title/artist/album/album artist 결과를 사용한다. explicit
queue에서 Add, Remove, Earlier/Later reorder, Clear, Retry blocked, Skip blocked를 수행한다.
automatic preview는 queue가 아니며 read-only다.

transport는 Previous, Play/Start, Pause, Resume, Next/Skip, Stop, Seek를 모두 제공한다.
enqueue는 autoplay가 아니고 Start에는 assigned online Renderer가 필요하다. Stop은 자동
advance하지 않는다. unsupported seek와 offline Renderer는 typed failure이며 Control이
로컬 성공으로 바꾸지 않는다.

화면의 Server intent, Renderer observed state, pending command는 분리되어 있다. `202`는
physical confirmation이 아니다. invalidation sequence/epoch gap이나 앱 resume/reconnect
뒤 authoritative REST state를 다시 읽는다. Control은 queue authority나 next-track 선택을
소유하지 않는다.

복구:

- `TOKEN_REVOKED`: secure record를 지우고 새 code로 pairing한다.
- certificate identity change: 자동 신뢰하지 말고 Server identity를 별도 검증한다.
- stale revision/conflict: 자동 retry 없이 refresh 후 intent를 다시 확인한다.
- offline Renderer: Server assignment/status를 복구한 뒤 명시적으로 retry한다.
- blocked track: 원인을 고친 뒤 Retry blocked 또는 Skip blocked를 선택한다.
- restored/corrupt credential: 기존 token 재사용을 시도하지 말고 새 pairing을 한다.

## update, rollback, 제거

same package ID와 same signer APK만 in-place update한다.

```sh
adb install -r jastreamer-control_<새버전>_android_universal.apk
```

`INSTALL_FAILED_UPDATE_INCOMPATIBLE`를 기존 앱 삭제로 즉시 우회하지 말고 source와 signer를
조사한다. rollback은 Android version policy가 거부할 수 있다. 제거/재설치가 필요하면
Server에서 device를 먼저 철회한다. app rollback은 Server queue/catalog를 되돌리지 않는다.

```sh
adb uninstall io.jastreamer.control
```

제거와 app-data clear는 secure token을 제거한다. Server-side device 철회까지 해야 trust
removal이 완료된다.
