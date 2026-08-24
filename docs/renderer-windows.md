# Windows Renderer 설치 및 사용

> 현재 Renderer는 workflow-ready 상태이며 공개 GitHub Release는 아직
> 생성되지 않았다. 실제 설치 전 해당 태그의 Release와 산출물이 존재하는지
> 확인한다.

## 지원 범위

- Windows amd64 (`x86_64-pc-windows-msvc`)
- WASAPI 출력
- Renderer protocol major 2와 호환 major 1
- 필수 capability `render`

Linux, macOS, ARM Windows, 일반 UPnP Renderer 동작은 이 패키지의 지원
범위가 아니다.

## 산출물 선택

```text
jastreamer-renderer_<버전>_windows_amd64.msi
jastreamer-renderer_<버전>_windows_amd64_diagnostic.zip
```

- **MSI**: 일반 설치용. 기본 경로는
  `C:\Program Files\jastreamer-renderer\jastreamer-renderer.exe`.
- **diagnostic ZIP**: 설치 없이 실행 파일을 확인하는 portable 진단용.
  시스템 설치나 registry 구성을 하지 않는다.

함께 제공되는 검증 파일:

```text
certificate.cer
certificate-fingerprint.txt
SHA256SUMS
SBOM.spdx.json
PROVENANCE.intoto.json
trust.md
remove-trust.md
protocol-range.json
```

## 파일과 인증서 검증

```powershell
Get-FileHash .\jastreamer-renderer_<버전>_windows_amd64.msi `
  -Algorithm SHA256
Get-FileHash .\jastreamer-renderer_<버전>_windows_amd64_diagnostic.zip `
  -Algorithm SHA256
certutil -dump .\certificate.cer
```

패키지 digest는 `SHA256SUMS`, 인증서 SHA-256 지문은
`certificate-fingerprint.txt`와 비교한다. 다르면 설치하지 않는다.

Renderer는 프로젝트 자체 서명 인증서를 사용한다. Windows Public Trust와
SmartScreen 평판을 제공하지 않는다.

확인한 인증서를 관리자 PowerShell에서 가져온다.

```powershell
Import-Certificate `
  -FilePath .\certificate.cer `
  -CertStoreLocation Cert:\LocalMachine\TrustedPeople
```

## MSI 설치

```powershell
$msi = Resolve-Path `
  .\jastreamer-renderer_<버전>_windows_amd64.msi
$process = Start-Process msiexec.exe `
  -ArgumentList @('/i', $msi, '/qn', '/norestart') `
  -Wait `
  -PassThru
if ($process.ExitCode -ne 0) {
  throw "MSI install failed: $($process.ExitCode)"
}
```

설치 확인:

```powershell
$renderer = "$env:ProgramFiles\jastreamer-renderer\jastreamer-renderer.exe"
& $renderer --help
& $renderer --version
& $renderer --revision
& $renderer --protocol
```

`--protocol`은 현재 major 2와 compatible major 1을 보고한다.

## Diagnostic ZIP

```powershell
Expand-Archive `
  .\jastreamer-renderer_<버전>_windows_amd64_diagnostic.zip `
  -DestinationPath .\renderer-diagnostic `
  -Force

Push-Location .\renderer-diagnostic
.\jastreamer-renderer.exe --help
.\jastreamer-renderer.exe --protocol
Pop-Location
```

MSI 설치가 정책상 차단되거나 실행 파일 자체를 분리해 확인할 때 사용한다.
영구 설치 대체물로 자동 등록되지는 않는다.

## 프로토콜 호환성

Renderer는 원격 peer와 공통으로 지원하는 가장 높은 major를 선택한다.

- peer 2: major 2
- peer 1: major 1
- 공통 major 없음: `UNSUPPORTED_PROTOCOL_MAJOR`, 종료 코드 78
- `render` capability 없음: 호환성 실패

호환성은 제품 SemVer가 아니라 protocol major와 capability로 판단한다.
`protocol-range.json`과 Server/Control의 호환 범위를 함께 확인한다.

CLI fixture 진단:

```powershell
.\jastreamer-renderer.exe `
  --compatibility-fixture .\fixture.json `
  --remote-majors 2,1 `
  --remote-capabilities render
```

알 수 없는 미래 명령은 wire value를 보존한 채 `unknown`으로 보고된다.

## 업데이트

1. 새 MSI/ZIP의 digest와 인증서 지문을 확인한다.
2. 새 인증서라면 별도 신뢰 경로로 검증한다.
3. 새 MSI를 설치한다.
4. `--version`, `--protocol`, `--help`를 실행한다.
5. Server와 `render` capability 협상이 성공하는지 확인한다.

인증서가 바뀌었다는 이유만으로 기존 설치를 삭제하거나 새 인증서를
자동 신뢰하지 않는다.

## 제거와 신뢰 철회

```powershell
$msi = Resolve-Path `
  .\jastreamer-renderer_<버전>_windows_amd64.msi
$process = Start-Process msiexec.exe `
  -ArgumentList @('/x', $msi, '/qn', '/norestart') `
  -Wait `
  -PassThru
if ($process.ExitCode -ne 0) {
  throw "MSI uninstall failed: $($process.ExitCode)"
}
```

설치 파일이 없어졌는지 확인한다.

```powershell
Test-Path "$env:ProgramFiles\jastreamer-renderer\jastreamer-renderer.exe"
```

결과는 `False`여야 한다. diagnostic ZIP은 압축 해제 디렉터리를 직접
삭제한다.

앱 제거 후 `certificate-fingerprint.txt`와 일치하는 인증서만
`Cert:\LocalMachine\TrustedPeople`에서 삭제한다.

```powershell
Get-ChildItem Cert:\LocalMachine\TrustedPeople
Remove-Item "Cert:\LocalMachine\TrustedPeople\<확인한-THUMBPRINT>"
```

## WASAPI 제한

- 프로토콜 협상 성공과 오디오 장치 초기화 성공은 별개다.
- 장치 점유, Windows 오디오 권한과 출력 설정에 영향을 받는다.
- Linux 테스트는 fake backend로 프로토콜을 검증하며 실제 WASAPI를
  실행하지 않는다.
- 동기화 그룹이나 generic UPnP gapless 동작을 주장하지 않는다.

## 릴리스 상태 확인

`renderer-vX.Y.Z` 태그는 Windows native build, EXE 선서명, MSI/ZIP
패키징, MSI 서명, 설치·`--help`·제거 smoke, allowlist, SBOM/provenance를
통과해야 한다.

워크플로우 파일이 존재하는 것만으로 게시 완료가 아니다. GitHub Release에
해당 태그와 두 패키지가 실제 공개되어야 **Published** 상태다.
