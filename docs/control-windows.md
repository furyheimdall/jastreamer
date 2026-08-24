# Windows Control 설치 및 사용

> 현재 공개 릴리스는 아직 생성되지 않았다. 아래 파일명은
> `control-vX.Y.Z` 워크플로우가 생성할 산출물 계약이다.

## 필요한 파일

같은 GitHub Release에서 다음 파일을 받는다.

```text
jastreamer-control_<버전>_windows.msix
control-windows.cer
Windows-CERT-SHA256.txt
SHA256SUMS
```

Control의 application ID는 `io.jastreamer.control`, MSIX publisher는
`CN=jastreamer`이다.

## 무결성과 인증서 확인

먼저 `SHA256SUMS`와 MSIX의 SHA-256을 비교한다.

```powershell
Get-FileHash .\jastreamer-control_<버전>_windows.msix -Algorithm SHA256
```

Control은 개인용 자체 서명 인증서를 사용한다. Windows Public Trust나
SmartScreen 평판이 없으므로 인증서를 직접 확인해야 한다.

```powershell
certutil -dump .\control-windows.cer
```

표시된 SHA-256 인증서 지문이 `Windows-CERT-SHA256.txt`와 정확히
일치해야 한다. 다르면 설치하지 않는다.

관리자 PowerShell에서 확인한 인증서를 `TrustedPeople`에 가져온다.

```powershell
Import-Certificate `
  -FilePath .\control-windows.cer `
  -CertStoreLocation Cert:\LocalMachine\TrustedPeople
```

## 설치와 제거

```powershell
Add-AppxPackage .\jastreamer-control_<버전>_windows.msix
```

설치 전 신뢰가 없으면 실패하는 것이 정상이다. 경고를 우회하지 말고
지문과 파일 digest를 다시 확인한다.

제거:

```powershell
Get-AppxPackage -Name io.jastreamer.control | Remove-AppxPackage
```

## 서버 찾기

1. **Server HTTPS address**에 주소를 입력한다.
   예: `https://music-server.local:8443`
2. Server 콘솔이나 관리자가 별도 경로로 제공한 HTTPS 인증서 SHA-256
   지문을 **Advertised SHA-256 fingerprint**에 입력한다.
3. **Discover Server**를 선택한다.
4. 결과의 이름, 주소, 지문을 다시 비교한다.

Windows Control은 인증서 DER의 SHA-256을 입력값과 직접 비교한다. 일반
인증서 chain이 통과하더라도 지문이 다르면 연결하지 않는다.

## Pairing

1. 발견한 Server에서 **Open pairing page**를 선택한다.
2. Server 관리자가 기본 5분 유효한 일회용 code를 생성한다.
3. 브라우저의 Server pairing 페이지에서 이 Windows 기기를 등록한다.
4. 한 번 표시되는 controller token을 복사한다.
5. Control의 **Complete pairing return**에 token을 입력한다.
6. **Complete pairing**을 선택한다.

token을 URL, 브라우저 기록, 로그, 화면 캡처에 넣지 않는다. 현재 네이티브
Control은 token을 메모리에 보관하므로 앱 재시작 후 재pairing이 필요할 수
있다.

## 사용

pairing 후 다음 영역을 사용할 수 있다.

- continuation policy: 정지, 앨범 이어듣기, 비슷한 음악
- artist/album cooldown과 세션 재정의
- catalog 인덱싱 및 분석 coverage
- explicit queue와 automatic next preview
- Server decision 설명
- **Refresh Server state**

정책 변경은 **Save policy**로 저장한다. Server revision이 먼저 바뀌어
stale 상태가 되면 최신 상태를 검토한 뒤 **Retry saved intent**를 사용한다.

## 업데이트

1. 새 MSIX와 `SHA256SUMS`를 확인한다.
2. 새 릴리스 인증서 지문을 확인한다.
3. 같은 application ID와 서명 계보인지 확인한다.
4. 새 MSIX를 `Add-AppxPackage`로 설치한다.
5. Server discovery와 pairing 상태를 확인한다.

자체 서명 인증서가 바뀌었다면 새 인증서를 자동으로 신뢰하지 않는다.
별도 경로로 새 지문을 검증한 후 가져온다.

## 인증서 신뢰 제거

Control을 먼저 제거한 다음, `Windows-CERT-SHA256.txt`와 일치하는
인증서만 삭제한다.

```powershell
Get-ChildItem Cert:\LocalMachine\TrustedPeople
Remove-Item "Cert:\LocalMachine\TrustedPeople\<확인한-THUMBPRINT>"
```

관련 없는 인증서를 삭제하지 않는다. 신뢰를 제거하면 재설치 전에 다시
인증서를 검증하고 가져와야 한다.

## 제한

- 인증서는 Windows Public Trust가 아니다.
- SmartScreen 평판을 제공하지 않는다.
- 서명 또는 Server TLS 검증을 비활성화하는 설치 방식은 지원하지 않는다.
- 앱 종료 또는 재설치 뒤 token 유지가 보장되지 않는다.
