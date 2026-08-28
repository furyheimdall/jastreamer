# Windows Control 설치와 운영

> Windows MSIX는 아직 공개 출시되지 않았다. candidate stage와 native 설치 검사는
> publication이 아니며, Todo 19/21 native qualification은 현재 pending이다.

## candidate 확인, trust와 설치

같은 candidate set의 MSIX, `SHA256SUMS`, `control-windows.cer`,
`Windows-CERT-SHA256.txt`를 사용한다.

```powershell
Get-FileHash .\jastreamer-control_<버전>_windows.msix -Algorithm SHA256
certutil -dump .\control-windows.cer
```

두 digest를 독립 배포 기록과 비교한다. 자체 서명 `CN=jastreamer`는 Public Trust나
SmartScreen 평판이 아니다. 확인한 certificate만 관리자 PowerShell에서 trust한다.

```powershell
Import-Certificate -FilePath .\control-windows.cer `
  -CertStoreLocation Cert:\LocalMachine\TrustedPeople
Add-AppxPackage .\jastreamer-control_<버전>_windows.msix
Get-AppxPackage -Name io.jastreamer.control
```

불일치나 install warning을 우회하지 않는다. certificate 교체도 자동 신뢰하지 않는다.

## pairing과 secure token

1. Server HTTPS origin과 별도 경로로 받은 certificate SHA-256을 입력한다.
2. **Discover Server** 뒤 identity와 protocol major 3 capabilities를 확인한다.
3. 관리자가 `/pair/`에서 controller 역할 one-time code를 만든다.
4. code를 소비해 한 번 표시되는 token을 **Complete pairing return**에 입력한다.
5. pairing 완료 후 앱을 재시작해 reconnect 상태를 확인한다.

Windows Control은 token record를 현재 Windows 사용자 범위 DPAPI로 암호화하고 app/package
identity를 entropy로 묶는다. 유효 token은 같은 사용자와 signer/application identity의
upgrade 뒤 유지된다. 다른 사용자, 다른 app identity, 복사된/corrupt blob은 사용할 수
없고 삭제된다. token을 command line, URL, log, screenshot에 넣지 않는다.

## browse, queue, transport와 truth

Browse/Search는 Server catalog의 title/artist/album/album artist 결과를 사용한다. track을
explicit queue에 Add하고 Remove, Earlier/Later reorder, Clear, Retry blocked, Skip blocked를
사용할 수 있다. active row나 stale revision 실패에는 자동 retry하지 않는다.

모든 transport action은 Previous, Play/Start, Pause, Resume, Next/Skip, Stop, Seek다.
Start는 assigned online Renderer가 필요하고 enqueue만으로 시작하지 않는다. Stop은 queue를
자동 진행하지 않는다. unsupported seek, offline Renderer, unavailable explicit head는
Server의 typed failure로 남는다.

**Server intent**, **Renderer observed**, **pending command**를 각각 확인한다. `202`는 물리
작업 접수이지 재생 성공이 아니다. event gap이나 reconnect 뒤 Control은 authoritative REST
state를 full resync한다. automatic preview와 Server decision은 read-only이고 Control이
next track을 계산하지 않는다.

복구:

- `TOKEN_REVOKED`: **Clear & pair again**으로 DPAPI record를 지우고 새 code를 사용한다.
- certificate mismatch/change: 연결을 중지하고 identity를 독립 확인한 뒤 다시 pairing한다.
- stale revision: **Refresh Server truth** 후 보존된 intent를 검토해 수동 재제출한다.
- Renderer offline/command failure: Server assignment와 observed state를 확인한 뒤 재시도한다.
- blocked track: 원인 복구 후 Retry blocked 또는 Skip blocked를 명시한다.

## update, rollback, 제거와 trust removal

새 MSIX의 digest, publisher, application ID, signer lineage를 확인한 뒤
`Add-AppxPackage`로 in-place update한다. signer/application identity가 다르면 token 유지나
upgrade를 기대하지 않는다. rollback MSIX가 허용되지 않으면 제거/재설치 전에 Server에서
credential을 철회하고 새 pairing 계획을 세운다. 앱 rollback은 Server state를 되돌리지
않는다.

```powershell
Get-AppxPackage -Name io.jastreamer.control | Remove-AppxPackage
```

제거 전 또는 즉시 뒤 Server `/admin/`에서 해당 device를 철회한다. 그 다음
`Windows-CERT-SHA256.txt`와 일치하는 certificate만 삭제한다.

```powershell
Get-ChildItem Cert:\LocalMachine\TrustedPeople
Remove-Item 'Cert:\LocalMachine\TrustedPeople\<확인한-THUMBPRINT>'
```

관련 없는 certificate를 삭제하지 않는다.
