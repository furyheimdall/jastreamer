# Task19 installed-product qualification

> 이 문서는 구현된 qualification 경계와 현재 pending 상태를 설명한다. workflow dispatch,
> native 실행, 보호 environment 설정 또는 publication이 완료되었다는 기록이 아니다.

## 하나의 authoritative flow

Task19 runner preflight와 installed-product 실행의 유일한 workflow는
`.github/workflows/task19-installed-qualification.yml`이다. 별도 preflight workflow는 없다.
preflight script `tooling/qa/task19/runner-preflight.ps1`는 이 workflow의 보호된 Windows job
안에서만 Android tools 설치 뒤 실행되고, 생성한 `task19-runner-preflight.json`은 같은 job의
`installed-runner.mjs --execute` 입력으로 바로 소비된다.

선행 provider는 `.github/workflows/product-qualification-dispatch.yml`이다. 보호된 default
branch의 수동 `version` 입력을 승인한 뒤 Server와 Control reusable staging workflow를 각각
한 번만 GitHub scheduler로 실행한다. reducer는 `needs.server.result`와
`needs.control.result`를 terminal-state authority로 사용하며 retry dispatch는 0이다. 이
workflow를 실행하는 shell 명령은 문서화하지 않는다. 현재 checkout에서 dispatch가
실행되었거나 `product-qualification-dispatch`, `task19-qualification`,
`task19-evidence-signing`, `product-promotion` environment가 구성되었다고 간주하지 않는다.

operator가 GitHub UI에서 선행 provider를 요청할 때 `version`과 exact Renderer provider tuple 외에
이미 완료된 K17 provider tuple 전체를 입력해야 한다. K17 tuple은 repository/workflow/event/run ID/
attempt/current SHA/conclusion과 artifact ID/name/digest/size/created/expires를 포함하며, 보호된
`product-qualification-dispatch` environment admission이 이를 repository
`furyheimdall/jastreamer`, `.github/workflows/server-release.yml`, `workflow_dispatch`, terminal
`success`, current protected SHA에 고정한다. qualification 자신의 run ID는 명시적으로 거부된다.
installed qualification의 필수 입력 `provider_run_id`와 `provider_run_attempt`는 그 뒤 완료된 exact
`product-qualification-dispatch.yml` attempt를 가리킨다.

## Exact candidate closure와 provider provenance

Authoritative reducer는 현재 run에서 provider가 관찰한 네 archive, 즉 Server/Control 각각의
`publication-stage`와 `staging-binding`을 요구한다. Actions API를 끝까지 pagination하고 exact
run ID/attempt/repository/head SHA, artifact ID/name/digest/size, 생성/만료 시각과 unexpired 상태를
검증한다. archive의 byte size와 SHA-256을 다시 계산하고 strict ZIP parser로 local/central
header, CRC, entry boundary와 allowlisted inventory를 검증한다. child output은 claim일 뿐이며
provider observation 및 `needs` result와 모두 일치해야만 reducer가 `satisfied`가 된다.

`tooling/qa/task19/task19-candidate-producer.mjs`는 그 reducer와 exact bytes에서 하나의
`task19_exact_candidate_closure` v2만 만든다. 입력 closure는 다음을 모두 포함한다.

- observed Server Linux amd64 DEB와 Server content manifest
- observed Control Web ZIP, signed Windows MSIX, universal APK와 Control content manifest
- exact Renderer Windows MSI와 Renderer content manifest
- 같은 source revision과 authoritative reducer digest에 묶인 `qualified` physical K17 및
  native WASAPI receipts
- authoritative staged manifest와 repository-owned scenario-driver digest

K17 input에는 결론 순환이 없는 provider route가 있다. 별도 선행 `server-release.yml`
`workflow_dispatch` run이 Server staging과 보호된 physical branch를 끝까지 실행하고 exact artifact
name `k17-qualification`을 업로드한 뒤 terminal `success`로 완료된다. 그 다음에만 parent workflow의
`task19-k17-provider-cli.ts`가 protected input authorization과 Actions API를 각각 사용해 exact
run/attempt/repository/workflow/event/current SHA/conclusion 및 artifact ID/name/digest/size/created/
expires를 다시 인증한다. metadata 인증이 끝나기 전에는 archive를 다운로드하지 않으며, qualification
자신의 run ID, stale/wrong attempt 또는 tuple drift는 exit 77로 거부된다. 인증된 단일-file archive만
private input root에 atomic rename되고 authorization receipt가 exact tuple을 보존한다. candidate
producer는 이 observed input의 `qualified`, source revision, authoritative candidate digest binding이
모두 맞을 때만 소비하며 pending branch를 qualified로 승격하지 않는다.

Renderer MSI와 native WASAPI도 all-or-nothing으로 materialize된다. observer는 두 exact artifact의
metadata와 모든 archive size/digest/inventory를 먼저 검증하고 collision을 거부한 뒤, final output과
분리된 private temporary directory에 전체 entry를 exclusive-write한다. 모든 검증/write가 끝나야
한 번의 rename으로 final `task19-physical` root가 나타난다. Renderer가 유효해도 이후 WASAPI
provider 검증이 실패하면 private staging을 제거하고 final candidate bytes는 0이다.

입력이 없거나 drift하면 status artifact는 `denied`, `promotable: false`이고 closure artifact는
업로드되지 않는다. 성공 status도 `promotable: false`이며 installed qualification의 입력일
뿐 publication approval이 아니다.

Installed workflow의 observer는 exact artifact name
`task19-exact-candidates-<provider run ID>-<provider run attempt>` 하나만 허용한다. GitHub API로
인증한 provider run의 `conclusion`은 정확히 `success`여야 한다. 다운로드한 archive는 provider가
보고한 `size_in_bytes`와 digest에 일치할 때만 retained되며, closure에는 다음 authenticated provider
provenance가 삽입된다: `repository`, `workflowPath`, `eventName`, `runId`, `runAttempt`, `headSha`,
`artifactId`, `artifactName`, `artifactDigest`, `archiveSha256`, `size`, `createdAt`, `expiresAt`,
`observedAt`. 이 `size`는 retained archive bytes에 결합되고 installed policy가 positive safe integer로
다시 검증한다. workflow path는 `.github/workflows/product-qualification-dispatch.yml`, event는
`workflow_dispatch`여야 한다. 다른 Task19 candidate artifact, unsuccessful provider, duplicate,
expiry, inventory 또는 archive/reference size/SHA-256 mismatch는 fail closed다.

## Repository trust, preflight와 authorization

Execution policy와 trust roots는 repository-owned
`tooling/qa/task19/installed-runner-policy.mjs` 및
`tooling/qa/task19/task19-production-trust-v1.json`이다. candidate producer와 protected runner는
repository-owned `tooling/qa/task19/scenario-driver.ps1`와 `scenario-runtime.mjs`의 pinned SHA-256을
다시 계산한다. runtime은 harness, immutable 30-scenario contract, operation/inventory/process adapter
각각의 경로와 digest도 독립적으로 검증한다. local plan 실행은 production readiness와 authorization을
우회할 수 없으므로 native evidence가 될 수 없다.

Workflow는 Android platform-tools 37.0.1과 build-tools 35.0.0을 Google의 exact URL에서
받되 archive size와 SHA-256을 trust file에 고정한다. `adb.exe`, `apksigner.bat`, build-tools
`source.properties`와 tool version command가 모두 통과해야 한다. preflight는 Windows/X64와
정확히 한 대의 `device` 상태 Android 장치를 요구한다. unauthorized device는 최대 120초간
`adb wait-for-device`를 기다린 뒤 다시 정확히 한 대인지 확인한다. receipt에는 raw serial이
아니라 serial SHA-256, adb executable SHA-256, adb version-output SHA-256과
`publicationWrites: 0`만 기록한다.

`authorize` job은 보호된 `task19-qualification` environment에서 15분 이내의 Ed25519 receipt를
서명한다. receipt는 repository/workflow/event/environment, installed workflow run ID/attempt/head
SHA, provider run ID/attempt, physical authorization token SHA-256과 authorized device serial
SHA-256에 묶인다. private key와 raw token은 Windows runner에 전달되지 않고 서명된 receipt와
signature만 전달된다. Windows runner는 repository public key
`tooling/qa/task19/task19-authorization-public.pem`, pinned key ID, pinned physical-token hash,
preflight device hash를 모두 확인한다. 이 authorization receipt는 단독 실행 권한이 아니다. production
status/ready, MSIX/APK lineage, K17/WASAPI/staged-manifest/driver roots, authorized device binding,
configured harness 및 scenario-contract/adapter 경로와 pinned digest, plan의 candidate/provider
run/attempt/head SHA binding이 모두 일치할 때만 protected execution을 enable한다. 하나라도 없거나
다르면 authorization이 유효해도 product command 0으로 거부한다.

현재 production trust는 `status: pending_external_roots`, `qualification.ready: false`다.
`physicalAuthorizationSha256`, MSIX certificate SHA-256, APK lineage SHA-256, K17 root, WASAPI
root와 harness signing trust가 아직 `null`이며 production invocation은 기본적으로 이 repository
production trust file을 읽고 실행을 거부한다. scenario provisioner 자체는 repository-owned production
module로 구현되어 있고 별도 test injection 없이 operation adapter의 default다. `receiptTemplate`은
trust 또는 실행 계약에 존재하지 않는다. receipt 본문은 authenticated candidate/source/contract/peer
roots와 실제 scenario 관찰에서만 만들어진다. 테스트 전용
`TASK19_TRUST_HARNESS=repository-owned-synthetic-v1`은
별도 repository fixture public/private authorization roots만 검증하는 synthetic test surface다.
그 fixture도 production certificate/lineage/K17/WASAPI/harness roots는 `null`, `ready: false`이므로
product command 0으로 default-deny되고 production root나 native evidence를 대신할 수 없다. 따라서
local scheduling/default-deny evidence는 six-run plan shape와 product command 0만 증명하며 native
qualification을 증명하지 않는다. `--dry-run` 성공, fixture, emulator, Linux run 또는 pending
receipt를 native pass로 해석하지 않는다.

## Native execution, signer isolation과 cleanup

모든 production root가 준비된 경우에만 `[self-hosted, Windows, X64, task19-protected]` job이
closure를 private Windows snapshot으로 만들고 다음 여섯 run을 실행할 수 있다:
Web/Windows/Android 각각의 `server_first`와 `control_first`. snapshot 보안 계약은 Windows
owner/private DACL이다. adapter가 current Windows identity에서 runner SID를 독립적으로 먼저
조회하고, ACL 적용 시 identity drift를 다시 거부한다. 적용 뒤 조회한 ACL owner는 그 pre-observed
runner SID와 정확히 비교하므로 ACL이 보고한 동적 owner 값을 trusted principal로 채택하지 않는다.
ACL inheritance를 차단한 뒤 그 runner SID, LocalSystem(`S-1-5-18`),
Administrators(`S-1-5-32-544`)의 non-inherited FullControl allow rule만 허용하며 owner/protected/rules를
다시 검증한다. 각 snapshot file은 write 직후 size/SHA-256과 reparse-point 부재를 확인한다.

실행은 repository의 operational lifecycle adapter `protected-lifecycle.mjs`와 Windows executor
`protected-runner.ps1`를 통한다. operational `task19-installed-scenario-driver-v1` contract에서
hashed repository `scenario-driver.ps1`가 separately hashed repository
`scenario-runtime.mjs`를 실행하고, runtime은 production trust에서 별도 구성된 harness path/contract와
pinned harness digest를 요구한다. Operation adapter는 Server가 실제로 mount한 authenticated
`/api/v1/discovery`, catalog/config/queue/transport/event routes만 사용한다. Web은 staged ZIP의 Flutter `main.dart.js`와 필수 assets를 task-owned TLS 1.3 static origin에서
정확히 제공하고 explicit listen readiness 뒤 run당 하나의 Playwright session으로 조작한다. `file://`은
사용하지 않는다. production trust에는 TLS private key 또는 repository-known certificate digest가 없다.
Digest-pinned generator만 authorization 뒤 protected snapshot 안에 run-ephemeral key/certificate를 만들고,
owner-only mode 또는 runner/System/Administrators private DACL을 적용한다. immutable run evidence는 raw
host 대신 allowlisted loopback host class, ephemeral port, certificate SHA-256, SPKI SHA-256과 exact-SPKI
browser trust mode를 함께 묶는다. Playwright/Chromium은 그 SPKI만 trust하며 global
`ignoreHTTPSErrors`를 사용하지 않는다. primary/rotation key와 certificate는 success/failure 모든 cleanup
경로에서 삭제된다. repository fixture key는 hostile unit test 입력일 뿐 production trust, protected runner,
release workflow에서 참조할 수 없다. Windows MSIX는 `shell:AppsFolder` activation 뒤 launched process의
`MainWindowHandle`에 한정된 Windows UI Automation으로, Android APK는 preflight에서 승인된 exact
serial의 `io.jastreamer.control/.MainActivity` foreground state와 `adb -s <serial> uiautomator`로 조작한다.
Renderer는 installed executable의 실제 CLI와 Server renderer session을 사용하며 native WASAPI
binding은 exact physical WASAPI receipt에 계속 묶인다. `/api/v1/qa/task19/*` protocol은 없다. trusted plan의 authenticated candidate/provider/source/contract/peer roots를 사용해
Web/Windows/Android x `server_first`/`control_first` 순서의 정확한 여섯 run, 각 run의 before/allocated/after inventory와 cleanup, 30개 scenario, performance evidence,
7개 nonzero negative probe를 수행하고 transcript/result를 artifact set과 결합한다. 다른 driver contract, runtime/harness/scenario-contract/adapter digest drift, run ordering 또는 result
binding drift는 product evidence로 승인되지 않는다. 실행 전 MSIX Authenticode와 certificate lineage, APK signature와
lineage, repository driver/runtime digest를 확인한다.

Protected runner는 시작 전에 다음 product state가 모두 없어야 한다: Control MSIX package,
Android `io.jastreamer.control`, Renderer MSI product code, WSL의 `jastreamer-server` Debian
package와 `jastreamer-server`, `jastreamer-control`, `jastreamer-renderer` process. Protected runner의
정상 경로는 candidate/signature/digest를 검증하고 scenario driver를 감시할 뿐 package를 설치하지
않는다. 여섯 run 각각에서 scenario runtime의 digest-pinned process adapter 하나만 WSL DEB,
해당 platform Control candidate와 Renderer MSI를 install하고, Server → Renderer → Control 또는
Control → Server → Renderer 순서로 실제 product surface를 시작한다.

각 run에서 하나의 scenario/process runtime이 normal lifecycle owner다. 마지막 qualified run은 30개 scenario 뒤에도 종료하지 않고 실제 8개 zone x 10,000-entry queue와 100,000-track catalog를 provision한 상태에서 performance 및 7개 probe를 관찰한 다음 종료한다. 따라서 종료된 session이나 고정 loopback fallback은 performance/probe evidence가 될 수 없다. Web은 run당 하나의
Chromium/browser-context/session을 pairing 뒤 30 scenario 동안 유지하고, Windows UIA와 Android
adb/uiautomator도 같은 run-scoped pairing state를 명시적으로 유지한다. 각 scenario는 immutable
contract의 exact selector/gesture 또는 method/route/body, status/code, named state/revision relation과
correlated event kind/resource/revision을 충족해야 한다. WebSocket initial snapshot은 별도 before
observation으로 기록되며 mutation 뒤 실제 관찰한 첫 matching invalidation만 event evidence가 된다.
initial/unrelated/stale/equal-revision event, `OBSERVED`, generic click/GET 또는 timestamp-only capture
차이는 거부된다. repository-owned digest-pinned scenario provisioner는 catalog root와 deterministic 100,000-track fixture,
zones, renderer assignment, 10,000-entry queues, stale revisions, revoked credentials, certificate rotation,
event gap, renderer disconnect, unavailable head 및 durable mutation 뒤 interruption/restart를 실제
Server API, WSL process와 native device boundary로 provision한다. event gap은 native capture proxy가 실제 다음 WebSocket invalidation을 누락시켜 만들고, renderer disconnect는 exact Renderer executable에 대한 run-owned outbound firewall rule을 추가한 뒤 `renderers` invalidation을 기다리며 cleanup에서 그 rule을 제거한다. API mutation에는 실제 ETag,
`If-Match`, `Idempotency-Key`를 사용한다. 각 prerequisite는 run lifecycle ownership handle에 묶이고
scenario lease 해제 뒤 run 종료에서 역순 cleanup된다. Server `/healthz`와 authenticated `/api/v1/config`
readiness가 끝나기 전에는 provision하지 않는다.

각 run의 성공과 실패 모두에서 같은 process adapter가 owned process tree를 종료한 뒤 APK,
exact MSIX package, Renderer MSI와 WSL package를 uninstall하고 post-inventory가 before inventory와
같음을 확인한다. Protected runner의 `finally`는 runtime 실패나 watchdog 종료 뒤 남은 package만
조건부로 제거하는 fail-safe이며 정상 uninstall owner가 아니다. 그 뒤 Android package, MSIX,
MSI product code, WSL package와 product process의 absence를 다시 확인하고
`cleanup-evidence.json`의 SHA-256을 execution result에 묶는다. workflow의 outer `always()` cleanup도
authorization files와 pinned Android tool directory를 제거하고 product process 및 Control MSIX
absence를 확인한다.

정확한 phase budget은 setup 20분, scenario driver 85분, cleanup 10분이며 watchdog margin은
1분이다. lifecycle adapter는 exact `[TASK19_PHASE]setup|driver|cleanup|done` 전환에 맞춰 watchdog을
다시 건다. 각 watchdog은 해당 phase budget에 margin만 더하므로 setup 21분, driver 86분,
cleanup 11분이다. active phase가 이 경계를 넘으면 exact protected process tree를 종료한다. 전체
Windows qualification workflow job
`timeout-minutes`는 130분이다.

모든 candidate package는 설치 직전에 size/SHA-256/reparse-point를 다시 검증한다. setup이 끝난
직후이자 driver launch 직전에는 execution plan SHA-256과 repository-trusted scenario driver
SHA-256을 즉시 다시 계산한다. drift는 이후 package 또는 driver command를 실행하기 전에
fail closed하며, 성공/실패 모두에서 cleanup과 post-state absence 검증을 수행한다.

Windows candidate host에는 Task19 evidence-signing private key가 없다. cleanup과 unsigned artifact
upload가 성공한 뒤 별도 Ubuntu job이 보호된 `task19-evidence-signing` environment에서 exact
unsigned artifact ID를 내려받아 evidence references를 서명하고 temporary key를 삭제한다. 그
job은 repository production harness trust로 signed execution을 다시 검증한 뒤에만
`task19-installed-receipt-<run ID>-<run attempt>`를 업로드한다.

## 현재 exact local candidate

현재 local candidate 문서를 native plan 입력으로 확인하는 명령은 다음과 같다.

```sh
node tooling/qa/task19/installed-runner.mjs \
  --candidates .omo/evidence/functional-jastreamer-products/final/stage-exact-server-control-candidates.json \
  --dry-run
```

이 명령은 exit 77, `SIGNED_MSIX_REQUIRED`, `productCommandsExecuted: 0`, `externalWrites: 0`으로
종료한다. local candidate에는 exact signed MSIX가 없으므로 scheduling plan조차 native
qualification으로 승격하지 않고 어떤 설치/제품 명령이나 외부 write도 수행하지 않는다.

## 현재 pending publication gates

다음 항목 중 하나라도 없으면 product gate와 publication은 반드시 불가능하다.

- production certificate lineage에 맞는 signed Control MSIX와 native install/uninstall evidence
- production Android signing lineage와 authorized physical device에서의 install/run/uninstall
- physical FiiO K17 qualification receipt
- native WASAPI qualification receipt와 numeric capture
- exact closure에 대한 native six-run Task19 signed receipt
- physical authorization token root, device binding와 protected Task19 authorization
- production harness signing trust 및 isolated signer environment
- protected production artifact-signing과 `product-promotion` authorization

현재 위 protected roots, runtime configuration과 native receipts는 pending/default-denied다. 유효한
authorization이 발급되었다는 주장도 없고 native run은 수행되지 않았다. 이 문서 작업은 workflow
dispatch, native installed-product/device command, tag, release, package promotion 또는 publication을
수행하지 않는다.

## 검증된 local help surface

설치나 publication 없이 runner CLI mode를 확인하는 명령은 다음 하나다.

```sh
node tooling/qa/task19/installed-runner.mjs --help
```

출력에는 `--dry-run`, `--execute`, `--output`, `--preflight`, `--authorization`,
`--authorization-signature`, `--validate-execution`이 포함된다. `--execute`는 operator용 local
우회가 아니며 signed physical authorization과 protected Windows runner policy 없이는
`awaiting_external_authorization`으로 거부된다.
