# 릴리즈 대상과 운영 절차

> 현재 GitHub Release와 GHCR 이미지는 아직 공개되지 않았다. 이 문서는
> 릴리즈를 실행하기 전에 필요한 저장소 설정과 검증 절차를 정리한다.

## 릴리즈 대상

현재 세 컴포넌트의 버전 파일은 모두 `0.1.0`이다.

| 컴포넌트 | 태그 | 워크플로우 | 공개 패키지 |
| --- | --- | --- | --- |
| Server | `server-v0.1.0` | `server-release.yml` | EXE, MSI, DEB/RPM amd64·arm64, OCI archive, GHCR image |
| Control | `control-v0.1.0` | `control-release.yml` | Web ZIP, Windows MSIX, universal APK |
| Renderer | `renderer-v0.1.0` | `renderer-release.yml` | Windows amd64 MSI, diagnostic ZIP |

컴포넌트 버전과 태그는 독립적이다. 한 컴포넌트 태그가 다른 컴포넌트
릴리스를 실행하지 않는다.

## 현재 검증 범위

- Server: Linux 호스트의 실제 no-publish dry-run에서 native package와
  amd64/arm64 OCI 생성 완료.
- Control: Linux/amd64 Flutter 컨테이너를 arm64 호스트에서 QEMU로 실행해
  Web/APK no-publish dry-run 검증. Windows MSIX의 최종 서명과 설치는
  `windows-2025` 보호 작업이 authoritative하다.
- Renderer: Linux에서는 가짜 Windows 파일을 만들지 않고
  `WINDOWS_RUNNER_REQUIRED`(69)로 종료한다. 실제 MSI/ZIP, 서명, 설치,
  `--help`, 제거는 `windows-2025` 보호 작업에서 검증한다.

## GitHub Environments와 secrets

다음 Environments를 만든다.

```text
server-signing
server-release
control-android-signing
control-windows-signing
control-release
renderer-signing
renderer-release
```

릴리스 승인자를 사용하는 경우 `*-release`에 required reviewer를
설정한다. signing environment와 release environment를 분리하여 private
key가 promotion job에 전달되지 않게 한다.

### Server

`server-signing`:

```text
SERVER_WINDOWS_SIGNING_PFX_B64
SERVER_WINDOWS_SIGNING_PFX_PASSWORD
```

### Control Android

`control-android-signing`:

```text
CONTROL_ANDROID_JKS_B64
CONTROL_ANDROID_STORE_PASSWORD
CONTROL_ANDROID_KEY_ALIAS
CONTROL_ANDROID_KEY_PASSWORD
CONTROL_ANDROID_CERT_SHA256
```

### Control Windows

`control-windows-signing`:

```text
CONTROL_WINDOWS_PFX_B64
CONTROL_WINDOWS_PFX_PASSWORD
```

### Renderer

`renderer-signing`:

```text
RENDERER_WINDOWS_SIGNING_PFX_B64
RENDERER_WINDOWS_SIGNING_PFX_PASSWORD
```

PFX/JKS 원문, 비밀번호, private key를 저장소, Actions artifact, 로그에
넣지 않는다. Base64는 암호화가 아니며 GitHub Environment secret의 전달
형식일 뿐이다.

## 태그 전 검사

```sh
cd apps/server
go test -race -shuffle=on -count=1 ./...
go vet ./...

cd ../renderer
cargo fmt --check
cargo clippy --locked --all-targets --all-features -- -D warnings
cargo test --locked

cd ../control
flutter analyze
flutter test
```

루트에서:

```sh
bun test packaging/server/tests packaging/control/tests \
  packaging/renderer/test tooling/release tooling/isolation tooling/container
./tooling/componentctl verify-boundaries --all
node tooling/docs/verify.mjs
```

각 `VERSION`과 태그가 정확히 일치하는지 확인한다.

## 로컬 no-publish rehearsal

```sh
./tooling/componentctl release dry-run \
  --component server \
  --tag server-v0.1.0 \
  --no-publish \
  --output /tmp/jastreamer-server-release

./tooling/componentctl release dry-run \
  --component control \
  --tag control-v0.1.0 \
  --no-publish \
  --scenario android-in-place-upgrade \
  --output /tmp/jastreamer-control-release
```

Renderer happy path는 Windows와 signing input이 필요하다. Linux에서 다음
명령은 `WINDOWS_RUNNER_REQUIRED`와 종료 코드 69를 반환해야 한다.

```sh
./tooling/componentctl release dry-run \
  --component renderer \
  --tag renderer-v0.1.0 \
  --no-publish \
  --scenario clean-windows-vm \
  --output /tmp/jastreamer-renderer-release
```

## 태그와 릴리즈 실행

모든 환경과 secret을 설정하고 `main` 검증이 끝난 뒤 필요한 컴포넌트
태그만 생성한다.

```sh
git tag -a server-v0.1.0 -m "server v0.1.0"
git push origin server-v0.1.0
```

Control과 Renderer도 같은 방식으로 각각 `control-v0.1.0`,
`renderer-v0.1.0`을 사용한다. 워크플로우는 기존 Release나 GHCR 태그를
덮어쓰지 않으며 promotion 실패 시 자신이 만든 draft를 정리한다.

## 공개 결과 확인

```sh
gh repo view furyheimdall/jastreamer
gh run list --repo furyheimdall/jastreamer --limit 20

gh release view server-v0.1.0 --repo furyheimdall/jastreamer
gh release view control-v0.1.0 --repo furyheimdall/jastreamer
gh release view renderer-v0.1.0 --repo furyheimdall/jastreamer

docker buildx imagetools inspect \
  ghcr.io/furyheimdall/jastreamer-server:0.1.0
```

Server OCI index에는 정확히 `linux/amd64`, `linux/arm64`만 있어야 한다.
각 Release의 파일명, `SHA256SUMS`, SBOM, provenance, 인증서 지문을
워크플로우 stage 기록과 비교한다.

## 릴리즈하지 말아야 하는 상태

- `VERSION`과 태그가 다름
- signing secret이 없거나 공개 인증서 지문과 PFX/JKS가 다름
- Control AAB 또는 private key가 public stage에 존재함
- Renderer를 Linux placeholder로 대체함
- 기존 Release/GHCR 태그가 이미 존재함
- Windows native 설치·실행·제거 검증이 실패함
- `.omo` 작업 상태나 signing material이 Git index에 포함됨
