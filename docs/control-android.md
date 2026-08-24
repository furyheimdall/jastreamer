# Android Control 설치 및 사용

> 현재 공개 릴리스는 아직 생성되지 않았다. 아래 파일명과 절차는
> `control-vX.Y.Z` 릴리스 워크플로우가 생성할 산출물 계약을 설명한다.

## 공개 산출물

Android 공개 산출물은 다음 universal APK 하나다.

```text
jastreamer-control_<버전>_android_universal.apk
```

APK는 `armeabi-v7a`, `arm64-v8a`, `x86_64`를 포함한다. AAB는 CI 내부
검증용이며 GitHub Release에 공개하지 않는다.

릴리스 태그, 버전 파일, Flutter 버전은 일치해야 한다.

```text
control-vX.Y.Z
apps/control/VERSION
apps/control/pubspec.yaml
```

## 다운로드 후 검증

APK와 함께 `Android-CERT-SHA256.txt`, `SHA256SUMS`를 같은 릴리스에서
받는다. Android SDK Build Tools의 `apksigner`로 서명 인증서를 확인한다.

```sh
apksigner verify --verbose --print-certs \
  jastreamer-control_<버전>_android_universal.apk
```

출력의 `Signer #1 certificate SHA-256 digest`가
`Android-CERT-SHA256.txt`와 같아야 한다. ABI도 확인할 수 있다.

```sh
unzip -Z1 jastreamer-control_<버전>_android_universal.apk \
  | grep -E '^lib/(armeabi-v7a|arm64-v8a|x86_64)/libapp\.so$' \
  | sort -u
```

다음 세 경로가 모두 출력되어야 한다.

```text
lib/armeabi-v7a/libapp.so
lib/arm64-v8a/libapp.so
lib/x86_64/libapp.so
```

서명 지문이 다르거나 ABI가 빠졌다면 설치하지 않는다.

## 설치

Android 설정에서 APK를 연 파일 관리자나 브라우저에만 "알 수 없는 앱
설치" 권한을 일시적으로 허용한다. ADB를 사용할 수도 있다.

```sh
adb install jastreamer-control_<버전>_android_universal.apk
```

패키지 ID는 다음과 같다.

```text
io.jastreamer.control
```

설치 후 필요하지 않은 sideload 권한은 다시 해제한다.

## 서버 찾기와 인증서 확인

Control의 **Find a jastreamer server** 화면에서 다음 값을 입력한다.

- **Server HTTPS address**: 예) `https://music-server.local:8443`
- **Advertised SHA-256 fingerprint**: Server 콘솔 또는 관리자가 별도
  경로로 제공한 HTTPS 인증서 지문

**Discover Server**를 누른 뒤 화면에 표시된 서버 주소와 지문을 다시
비교한다. Android Control은 서버 인증서 DER의 SHA-256 지문을 입력값과
비교하며, 값이 다르면 연결하지 않는다.

## Pairing

1. 발견된 서버에서 **Open pairing page**를 선택한다.
2. Server 관리자가 일회용 pairing code를 생성한다.
3. code가 만료되기 전에 서버 pairing 페이지에서 기기를 등록한다.
4. 서버가 발급한 controller token을 복사한다.
5. Control로 돌아와 **Complete pairing return**에 token을 입력한다.
6. **Complete pairing**을 선택한다.

pairing code는 한 번만 사용할 수 있고 기본 유효 시간은 5분이다.
controller token은 URL, 브라우저 기록, 로그, 화면 캡처에 넣지 않는다.
Control은 입력된 세션 token을 메모리에만 보관한다.

## 사용

pairing 후 Control에서 다음 기능을 사용할 수 있다.

- Server policy 조회, 변경, 저장
- catalog 인덱싱 및 분석 coverage 확인
- Server가 생성한 playback decision 확인
- explicit queue와 queue preview 확인
- Server 상태 새로 고침

Control은 추천이나 재생 결정을 자체 계산하지 않고 Server의 상태와
결정을 표시한다. 호환 가능한 protocol major가 없으면 Server와 Control의
버전을 확인한다.

## 업데이트

같은 signing key와 같은 application ID를 사용한 APK만 기존 설치 위에
업데이트할 수 있다.

```sh
adb install -r jastreamer-control_<새버전>_android_universal.apk
```

릴리스 CI는 이전 APK 설치 후 새 APK를 `adb install -r`로 적용하여
application UID, signing key, version code가 유지되는지 검사한다. 서명이
다른 APK는 `INSTALL_FAILED_UPDATE_INCOMPATIBLE`로 거부될 수 있다. 이 경우
출처와 지문을 먼저 확인하며, 단순히 기존 앱을 삭제해 우회하지 않는다.

## 제거

Android의 **설정 → 앱 → jastreamer Control → 제거**를 사용하거나 다음
명령을 실행한다.

```sh
adb uninstall io.jastreamer.control
```

앱 데이터를 삭제하면 pairing 정보도 사라질 수 있다. 재설치 후에는 기존
token 재사용을 가정하지 말고 새 pairing을 수행한다.

## 공개하지 않는 파일

최종 Control 릴리스에는 다음 파일이 들어가면 안 된다.

```text
*.aab
*.jks
*.keystore
*.pfx
*.p12
*.key
```

AAB와 signing material은 설치 파일이 아니며 공개 배포 대상도 아니다.
