# Candidate와 publication gate 운영

> 현재 Server/Control 공개 release와 GHCR promotion은 준비 완료로 승인되지 않았다.
> Renderer는 qualification용 CI artifact일 뿐 공개 대상이 아니다. tag나 workflow 존재는
> publication evidence가 아니다.

## 대상

- Server: staged native packages와 `linux/amd64`, `linux/arm64` OCI candidate
- Control: staged Web ZIP, Windows MSIX, universal Android APK candidate
- Renderer: Windows diagnostic candidate를 K17/WASAPI test peer로만 보존; public asset 0

각 component tag/version은 독립적이지만 publication은 exact staged artifact set과 peer
contract digest를 하나의 product receipt로 묶는다. qualification 뒤 rebuild하지 않는다.

## gate 의미

| receipt key | 증명 |
| --- | --- |
| `candidate` | source/contract에 묶인 exact staged artifact digest |
| `server_control_e2e` | exact installed Server와 Web/Windows/Android Control workflow |
| `k17` | 승인된 물리 FiiO K17 V261+의 protocol/media/audio 결과 |
| `wasapi` | 승인된 native Windows runner의 exact Renderer와 loopback numeric capture |
| `ffmpeg` | 관리자 제공 외부 executable digest, decoder, `pcm_s16be` probe |
| `external_authorization_pending` | K17/WASAPI 실행 권한 없음, network/audio mutation 0, publication 불가 |
| `cleanup` | process/resource/temp 정리와 external write 0 |

`physical` gate는 실제 device/runner receipt다. emulator나 fixture는 구현 검증일 뿐이다.
`runner` gate는 required labels, protected endpoint/device identity, candidate digest가 모두
일치하는 실행이다. 현재 K17 authorization과 Windows native qualification이 pending이므로
pending receipt는 안전한 대기 상태만 증명하며 release approval이 아니다.

## Todo 22 exact-byte product gate

Server/Control promotion verifier는 서명된 `product_promotion_gate` v1 receipt와 그 receipt가
가리키는 안정적인(non-symlink) staged file descriptor의 실제 SHA-256을 다시 계산한다. gate는
Server 7개 native/OCI artifact, Control 3개 artifact, CI-only Renderer peer, source dirty
snapshot, contract/peer set, Task19/K17/WASAPI qualification, artifact별 SBOM/provenance/signing
record, cleanup inventory와 append-only attempted-operation ledger를 한 번에 묶는다. OCI index는
정확히 `linux/amd64`, `linux/arm64`와 두 attached attestation을 가져야 한다.

현재 checkout에는 signed Task19 Windows/production-lineage physical Android, physical K17,
native WASAPI, Server OCI와
Windows native candidate가 없으므로 실제 candidate는 `QUALIFICATION_PENDING`/artifact pending으로
거부된다. pending을 채우기 위한 placeholder나 rebuild는 금지된다. workflow dispatch input은
receipt나 authorization을 받을 수 없고 candidate staging만 수행한다.

verifier의 synthetic fixture는 production qualification이 아니다. fixture trust store는
`profile=fixture`에서만 허용되며 production invocation은 저장소의 versioned trust root와 key
rotation epoch만 사용한다. 현재 simulator CLI는 operator runbook surface로 검증되지 않았으므로
여기서는 실행 명령을 제공하지 않는다.

staging 실패 cleanup은 해당 run의 stage directory만 제거한다. verifier는 pre/post
process/container/temp/listener inventory가 같고 externally-observed mutation ledger가 비어
있는지 확인한다. 이전 tag/release/image 삭제나 재조회는 cleanup이 아니다.

## local 검증

```sh
bun test contracts/tests tooling/compatibility tooling/docs tooling/qa \
  packaging/server packaging/control packaging/renderer tooling/release
bun tooling/docs/verify.mjs \
  --claims docs/claims.json \
  --receipt-schema tooling/qa/product-receipt.schema.json
./tooling/componentctl verify-boundaries --all
```

Server Go race tests, Control analyze/tests, Renderer fmt/clippy/tests도 component별로 실행한다.
Renderer Linux run은 real WASAPI qualification이 아니다.

candidate rehearsal은 `--no-publish`만 사용한다. output은 run별 temporary directory로 둔다.

```sh
out="$(mktemp -d)"
./tooling/componentctl release dry-run --component server \
  --tag server-v0.1.0 --no-publish --output "$out/server"
./tooling/componentctl release dry-run --component control \
  --tag control-v0.1.0 --no-publish --scenario android-in-place-upgrade \
  --output "$out/control"
rm -rf "$out"
```

Renderer Windows candidate와 native audio는 authorized workflow runner에서만 수행한다.
Linux에서 `WINDOWS_RUNNER_REQUIRED`는 안전한 pending 결과다.

## default-deny와 금지 상태

promotion은 missing, stale, mismatched, mock-only, emulator-only, K17-missing,
WASAPI-missing, Task19-missing, bad source/peer/contract/signature/SBOM/provenance/signing,
extra/floating/rebuilt artifact, cleanup-incomplete receipt를 독립적으로 거부하고 external write
0을 유지해야 한다.
다음 상태에서 tag/push/publish하지 않는다.

- signed MSIX, production Android lineage/device run 또는 native Task19 six-run evidence 없음
- physical K17 V261+ 또는 native WASAPI receipt 없음
- pending receipt를 qualified receipt로 해석함
- artifact/source/contract/peer digest 하나라도 다름
- FFmpeg를 bundle하거나 PATH/download로 취득함
- OCI platform이 정확히 amd64/arm64가 아님
- Renderer asset이 public allowlist에 들어감
- signing material, raw token/device identity, `.omo` 작업 state가 artifact에 들어감

tag workflow의 product handoff는 protected `product-promotion` environment가 고정한 upstream
qualification run/artifact ID, artifact digest와 gate SHA-256으로 qualified bundle을 읽고 다시
검증한다. `workflow_dispatch`, branch, PR은 final job에 도달하지 않는다. 완전한 production-trust
receipt만 별도 dispatch run에서 provider가 관찰한 exact Server/Control attempt-qualified publication-stage artifact ID를 선택한다.
final Server job만 `contents: write`, `packages: write`를 가지며 draft GitHub Release와 run-scoped
staging OCI reference를 거쳐 immutable version tag를 만든다. Control final job은 `contents: write`만
가지며 Web ZIP, Windows MSIX, universal APK 세 파일만 올린다. Renderer public asset/job은 없다.
모든 provider command 직전에 gate/manifest/selected bytes를 stable rehash한다. GitHub upload와 OCI
copy는 mutable stage pathname을 다시 열지 않고 private read-only approved snapshot만 사용하며, publish
직전에 GitHub가 관찰한 asset별 size/SHA-256을 다시 대조한다. provider child environment는 명시적인
runtime allowlist만 사용하고 receipt HMAC key를 전달하지 않는다. rebuild나 floating tag를 사용하지
않는다. 현재 production artifact-signing/qualification trust와 physical K17/native WASAPI evidence는
미완성이므로 이 positive path는 계속 fail-closed 상태다.

## Task19 preflight와 installed qualification

Task19의 authoritative runner preflight는 별도 workflow가 아니라
`.github/workflows/task19-installed-qualification.yml`의 보호된 Windows job 안에서 실행된다.
선행 `.github/workflows/product-qualification-dispatch.yml`은 `version`과 current revision에
묶인 exact Renderer provider tuple 외에 이미 완료된 K17 provider의 repository/workflow/event/run ID/
attempt/current SHA/conclusion/artifact ID/name/digest/size/created/expires tuple을 필수로 받는다.
K17 producer는 별도 `server-release.yml` protected `workflow_dispatch` run에서 완료되어야 하며,
qualification 자신의 run ID는 admission과 observer에서 거부된다. protected environment admission이
current SHA와 caller identity를 독립적으로 확인한 뒤 read-only observer가 Actions API의 terminal
`success` run과 exact artifact metadata를 다시 인증한다. metadata가 일치하기 전에는 archive를
다운로드하거나 물리 device command를 실행하지 않는다. Renderer/WASAPI와 K17이 모두 인증되어야
Server/Control provider bytes와 exact closure 하나를 만든다. Renderer/WASAPI는 두 provider archive를 모두 검증한 뒤 private staging root 전체를 한
번에 rename하므로 later provider failure 시 final candidate bytes는 0이다. 그 뒤 installed workflow가
exact provider run ID/attempt를 관찰해 preflight와 native execution을 이어간다. candidate closure,
authenticated provider provenance와 bound archive size, repository trust roots, pinned Android tools,
physical authorization, signer isolation, timeout과 MSIX/APK/MSI/WSL cleanup의 정확한 운영 계약은
[Task19 installed-product qualification](task19-installed-qualification.md)을 따른다.

현재 exact local candidate는 installed runner에서 exit 77 `SIGNED_MSIX_REQUIRED`, product command
0, external write 0으로 거부된다. 별도 local scheduling fixture는 Web/Windows/Android x
`server_first`/`control_first` 여섯 run의 shape와 default-deny만 증명한다. synthetic repository test
roots는 production roots와 분리되어 있고, production trust의 physical authorization token hash,
MSIX certificate lineage, APK lineage, K17, WASAPI, harness signing trust와 native semantic provisioner
roots는 계속 `null`/default-denied다. `receiptTemplate`은 없다. immutable 30-scenario contract와
harness/operation/inventory/process adapter는 각각 digest-pinned되고, receipt는 authenticated roots와
실제 action/state/event 관찰에서만 생성된다. authorization은 이 모든 roots, device,
runtime/harness/scenario-contract/adapter, plan/candidate/provider binding이 준비된 경우에만 실행을
enable한다. 현재 native run은 없었고 어느 local 결과도 native
run이나 dispatch evidence가 아니다. signed MSIX, production Android
physical-device lineage/run, physical K17, native WASAPI, native six-run Task19 signed receipt와
protected production authorization이 모두 채워지기 전에는 publication이 불가능하다.

## GitHub-authoritative qualification orchestration

Protected `product-qualification-dispatch` is a read-only manual orchestrator admitted only from the protected default branch. It invokes Server and Control exactly once through local reusable workflow jobs (`jobs.server.uses` and `jobs.control.uses`); it never executes `gh workflow run`, the REST dispatch endpoint, or a client-side retry. GitHub's scheduler owns success, failure, cancelled, and skipped terminal state. The parent reducer uses only `needs.<job>.result` as result authority and always reports `retryDispatches: 0`.

`server-release.yml` and `control-release.yml` retain protected tag and explicit manual staging triggers and also expose strict `workflow_call` inputs and outputs. A checkout-free, permissionless authorization job validates invocation mode, caller run ID/attempt/ref/SHA, protected default branch, and exact `github.workflow_ref`; every checkout/build/sign/stage job depends on it. The called candidate path remains the standalone build/stage implementation. Signing stays in the existing protected environments. The parent observer has only `actions: read` and `contents: read`, and its token exists only in the read step. Parent reusable calls, reducer, and gate-input jobs explicitly require `needs.authorize.result == 'success'`; `always()` applies only after authorization so rejected admission schedules no checkout, setup, observer, reducer, or gate code.

Child outputs are claims, not provider observation. The parent derives four exact artifact names from its own run ID/attempt, verifies the current run/repository/SHA, completely paginates the no-cache Actions artifact API, and requires one fresh unexpired record per name. It downloads ZIP bytes by independently observed IDs, verifies provider/archive SHA-256 and size, and cross-checks local and central ZIP headers, flags, methods, names, sizes, descriptors, CRCs, and non-overlapping record boundaries. Deflate uses a strict `maxOutputLength` derived from remaining per-entry, cumulative, and actual compression-ratio budgets; ZIP64, encryption, unsafe paths, links, duplicates, bombs, trailing records, and inventory drift fail closed. The serialized CLI boundary retains only exact manifest-allowlisted files with their size/SHA-256 before the strict candidate-binding parser rehashes every selected publication file. Only after independent records, child outputs, staging JSON, embedded metadata, and bytes agree does it inject `calledJobResult` from `needs`. Two exact successes produce the sole product-gate input; every uncertainty produces denial. Product-gate verification and publication trace that reducer digest/result. Renderer is absent.

No qualification claim release, blocker, cursor, checkpoint, lease, request-authentication secret, scheduled reaper, state variable, provider subprocess, dispatch API call, cleanup delete, or storage GC remains. Qualification state-resource growth is therefore zero. Task19 installed-product evidence, physical K17, and native WASAPI remain mandatory signed product-gate qualifications; this orchestration does not weaken their default-deny behavior.

## 실패와 cleanup

부분 실패는 preflight에서 부재와 digest uniqueness를 증명한 현재 run 소유 draft, asset,
run-scoped staging OCI와 현재 run이 만든 final OCI reference만 보상한다. 이전 release, git tag,
package digest는 삭제하지 않는다. 실패 receipt는 signed product-gate key ID와 결합된 protected
HMAC key로 인증되며 selected/provider-observed asset size+digest, cleanup residual과 resource별
`absent`/`owned`/`indeterminate` 상태를 명시한다. provider write는 dispatch 전에 possibly-committed로
기록하고 오류 뒤 exact run marker/digest를 read-after-write로 조정한다. ownership을 증명할 수 없으면
삭제하지 않고 실패 receipt에 `indeterminate`로 남긴다. release operator는 receipt의 cleanup,
process/container inventory, temporary registry credential 제거를 확인한다.
