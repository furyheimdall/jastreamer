# Windows foreground Renderer TEST HARNESS

> **CI/test-only. 공개 제품이 아니며 공개 release가 없다.** Renderer workflow가 만든
> candidate는 native audio qualification의 test peer로만 보관한다. 일반 사용자 설치,
> production 운영, service/tray/autostart 용도를 지원하지 않는다.

## 범위와 qualification 상태

이 harness는 Windows amd64 foreground process에서 Renderer protocol major 3(major 2
compatibility), authenticated media fetch, FLAC/MP3/Vorbis/Opus/WAV decode,
play/pause/resume/stop/seek와 WASAPI를 시험한다. Linux fixture backend, `--help`, package
생성, screenshot은 real audio evidence가 아니다.

native gate에는 `[self-hosted, windows, x64, jastreamer-audio]` runner, 보호된 endpoint ID,
exact candidate digest, WASAPI loopback numeric capture가 필요하다. 현재 gate는
`awaiting_external_authorization`; 따라서 implementation-ready일 뿐 release-ready가 아니다.
Renderer asset은 Todo 22 이후에도 공개하지 않는다.

## 승인된 runner에서 candidate 준비

CI에서 보존한 diagnostic ZIP과 `SHA256SUMS`, provenance, SBOM, certificate record의 exact
binding을 확인한다. 임의 local rebuild나 MSI를 product로 설치하지 않는다.

```powershell
Get-FileHash .\jastreamer-renderer_<버전>_windows_amd64_diagnostic.zip `
  -Algorithm SHA256
Expand-Archive `
  .\jastreamer-renderer_<버전>_windows_amd64_diagnostic.zip `
  -DestinationPath .\renderer-test-harness -Force
$renderer = Resolve-Path .\renderer-test-harness\jastreamer-renderer.exe
& $renderer --version
& $renderer --protocol
```

`--protocol`은 `3 (compatible with 2)`를 출력한다. 이 status command는 audio qualification을
대신하지 않는다. certificate trust가 runner policy상 필요하면 exact candidate fingerprint를
검증한 certificate만 temporary trust store에 넣고 cleanup에서 제거한다.

## foreground 실행

Server admin이 renderer 역할 one-time code로 test identity를 pair한다. bearer를 command
line이나 environment에 넣지 않고 `--token-stdin` prompt로 전달한다. origin, Server
certificate SHA-256, paired Renderer ID, 실제 endpoint ID, shared mode, run별 state directory를
모두 명시한다.

```powershell
$state = Join-Path $env:TEMP ("jastreamer-renderer-" + [guid]::NewGuid())
& $renderer `
  --server-origin https://<server>:8443 `
  --server-fingerprint <64-hex-sha256> `
  --renderer-id <paired-renderer-id> `
  --output-device <authorized-endpoint-id> `
  --share-mode shared `
  --state-directory $state `
  --token-stdin
```

한 state directory에는 process 하나만 사용한다. bearer는 저장하지 않는다. journal에는
accepted command payload와 durable result/tombstone만 남고 duplicate command는 재실행하지
않는다. Ctrl+C로 session, decoder, WASAPI stream과 lock을 정리한다.

Server는 compatible original representation을 먼저 보내며 Range를 사용할 수 있다. L16은
Server의 외부 FFmpeg probe와 sink capability가 모두 맞을 때만 fallback이다. harness가
next track을 고르지 않고 Server command만 실행한다. play success는 running WASAPI clock에
첫 frame이 들어간 뒤이며 natural end는 decoder EOF와 buffer drain 뒤다.

## 실패 상태

- wrong fingerprint: 연결하지 않는다. 새 certificate를 자동 trust하지 않는다.
- revoked/wrong token: session이 `TOKEN_REVOKED`/unauthorized로 닫힌다. 새 renderer code로
  pair하고 기존 device는 철회한다.
- no/busy/removed endpoint: `OUTPUT_UNAVAILABLE` 또는 failed observed state다. endpoint를
  복구하고 Server truth 확인 뒤 명시적으로 다시 시작한다.
- unsupported/corrupt/truncated media: command failure이며 queue 성공을 꾸미지 않는다.
- disconnect/restart ambiguity: durable journal/result replay로 reconcile하며 알 수 없으면
  suspended 상태를 유지한다.
- missing native gate: pending receipt, audio mutation 0, publication denied가 정상이다.

## test cleanup과 trust removal

Ctrl+C 뒤 process가 종료된 것을 확인하고 test state/capture/temp directory를 삭제한다.
Server `/admin/`에서 paired Renderer device를 철회한다. temporary certificate를 넣었다면
candidate fingerprint와 일치하는 항목만 제거한다.

```powershell
Get-Process jastreamer-renderer -ErrorAction SilentlyContinue
Remove-Item -LiteralPath $state -Recurse -Force
Get-ChildItem Cert:\LocalMachine\TrustedPeople
Remove-Item 'Cert:\LocalMachine\TrustedPeople\<확인한-THUMBPRINT>'
```

승인된 runner의 `tooling/qa/windows-audio/provision.ps1`가 exact artifacts와 endpoint를
검증하고 process/capture cleanup receipt를 만든다. 일반 workstation에서 이 절차를
실행하거나 수동 청취로 qualified 표시하지 않는다.
