# jastreamer

<img src="assets/jastreamer.svg" width="80" height="80" alt="jastreamer 로고" />

jastreamer 0.2.0은 신뢰하는 사설 LAN에서 사용하는 자체 호스팅 음악 서버입니다. Linux 또는 Windows Server가 관리자가 승인한 로컬 음악을 색인하고 Web 화면을 제공하며, 대기열과 플레이리스트를 SQLite에 보관하고 선택한 네트워크 출력 하나로 오디오를 전송합니다. UPnP/DLNA는 기본으로 제공되고 Google Cast는 두 플랫폼에서 선택적으로 켤 수 있습니다. AirPlay 전송은 지원되는 Linux 컨테이너 패키지에서만 사용할 수 있습니다.

영어를 기본으로 한국어 UI를 지원합니다.

- [한국어 사용자 안내서](INSTRUCTION.ko.md)
- [English README](README.md)
- [English user guide](INSTRUCTION.md)
- [복사해서 사용하는 에이전트 셋업 프롬프트](#ai-에이전트로-설치하기)

## 주요 기능

- 원본 보관함을 수정하지 않고 FLAC, MP3, WAV/WAVE, Ogg/Vorbis, Opus, M4A 파일 스캔
- 곡, 앨범, 아티스트, 장르, 폴더별 탐색과 검색, 내장 앨범 아트 표시
- 저장된 태그와 확인된 오디오·파일 정보를 필요할 때 표시
- 플레이리스트와 중복 곡을 보존하는 서버 전역 대기열 유지
- 호환되는 네트워크 출력의 재생과 탐색 제어
- LAN의 UPnP/DLNA, 선택적 Google Cast 및 AirPlay 출력 검색
- 수신기가 요구할 때 AirPlay PIN·암호 인증
- 최초 계정 생성, 비밀번호 로그인·변경·복구와 로그인 상태 유지
- 기본 사설 LAN HTTP와 운영자가 제공한 PEM 인증서·키를 사용하는 선택적 내장 HTTPS

선택 사항인 데스크톱 앱은 Windows 10/11 x64 무설치 ZIP 또는 Linux amd64 DEB로 제공되는 서버 접속용 앱입니다. jastreamer 서버를 찾거나 HTTP(S) 주소를 입력받아 서버의 Web 화면을 표시합니다. 두 패키지 모두 서버나 로컬 오디오 플레이어가 아니며, Linux arm64 클라이언트는 브라우저를 사용할 수 있습니다.

## 배포 방식

Server 배포 대상은 서로 구분됩니다. Linux `amd64`·`arm64` 컨테이너에는 내장 Web 화면, 오디오 전용 FFmpeg 8.1.2와 pyatv 0.18.0 AirPlay 송신 프로그램이 포함됩니다. 네이티브 Windows x64는 미서명 무설치 `jastreamer-server_0.2.0_windows-x64.zip`이며, 내장 Web 화면, UPnP/DLNA와 선택적 Google Cast를 제공하지만 AirPlay 송신 프로그램, Renderer 또는 FFmpeg 변환기는 포함하지 않습니다. Google Cast 자체에는 어느 플랫폼에서도 Chrome이나 Python helper가 필요하지 않습니다. Synology Container Manager에서는 제공된 Linux Compose 파일을 사용합니다. [GitHub Releases](https://github.com/furyheimdall/jastreamer/releases)에 게시된 프리뷰를 선택하고, Linux는 정확한 이미지 다이제스트를, Windows Server는 검증된 ZIP과 체크섬을 사용하며 대상별 manifest와 네이티브 검증 receipt를 확인하세요. 공개 프리뷰는 레지스트리 로그인이 필요하지 않지만 정식 production 검증이나 서명을 마친 릴리즈가 아닙니다. 명시된 검증 범위를 확인하고 다운로드한 파일을 릴리즈 체크섬과 비교하세요. 가변 `latest` 태그는 사용하지 마세요.

저장소 세 종류를 분리하세요.

- **config:** 쓰기 가능한 `server.json`과 선택적 HTTPS PEM 파일
- **data:** 쓰기 가능한 SQLite 데이터베이스, 앨범 아트 캐시와 AirPlay 상태
- **music:** 읽기 전용으로 연결하는 기존 음악 보관함

어느 Server 대상을 교체하든 config와 data를 보존해야 합니다. jastreamer를 위해 음악 보관함 전체의 소유권이나 권한을 재귀적으로 바꾸지 말고, 서버를 공용 인터넷에 직접 노출하지 마세요.

패키지 확인, Docker·Synology 설치, 선택적 데스크톱 앱, 최초 사용, 언어 선택, 업데이트와 문제 해결은 [한국어 사용자 안내서](INSTRUCTION.ko.md)를 참고하세요.

## 선택 사항인 Google Cast

Google Cast는 기본적으로 꺼져 있으며 이전 `server.json`에 `cast.enabled`가 없을 때도 `false`로 처리됩니다. **Settings**에서 **Google Cast 출력**을 켜고 저장한 뒤 Server를 재시작하세요. 수신기가 보인다는 이유만으로 임의로 켜면 안 됩니다. 검색에는 선택한 `network.interfaces`의 mDNS UDP 5353이 필요합니다. Server에서 수신기가 mDNS로 광고한 Cast TCP 포트에 연결할 수 있어야 하고, 수신기에서 Server의 `media.base_url` HTTP(S) 미디어 URL에 접근할 수 있어야 합니다. 자동 미디어 출처 선택은 수신기를 찾은 인터페이스를 따르며, 라우팅 때문에 필요한 경우에만 수신기가 접근할 수 있는 출처를 명시하세요.

Cast 원본 직접 스트리밍은 검사된 codec·sample rate·channel 메타데이터와 해당되는 경우 bit depth를 보수적으로 판단합니다. 모든 직접 전송 원본은 일치하는 codec이 확인되고 sample rate가 양수이며 mono 또는 stereo여야 합니다. FLAC은 96 kHz 및 1–24-bit까지, MP3·Ogg/Vorbis·Ogg/Opus·M4A/AAC는 48 kHz까지, LPCM WAV는 48 kHz 및 1–16-bit까지 허용합니다. 그 밖의 형식이나 확인되지 않은 원본은 미디어 변환을 켜고 FFmpeg를 설정해야 하며, 변환 결과는 탐색할 수 없는 44.1 kHz stereo 16-bit WAV 스트림입니다. 원본 파일은 변경하지 않습니다.

Cast도 다른 출력과 같은 Server 대기열 하나를 사용합니다. Cast LOAD의 autoplay는 사용하지 않고 Play를 명시적으로 전송합니다. 수신기 그룹과 gapless 재생은 지원하지 않으며 직접 스트리밍이나 변환 모두 bit-perfect를 보장하지 않습니다. 일시 정지·탐색·형식 등 기능은 수신기에 따라 다릅니다. Cast 제어는 지속 TLS 연결을 사용하고 jastreamer가 소유한 애플리케이션·미디어 세션에서 명시적인 `FINISHED` 상태를 받은 경우에만 대기열을 진행합니다.

## AI 에이전트로 설치하기

아래 프롬프트 전체를 복사해 이 저장소를 읽고, 허가받은 서버나 NAS에서 작업할 수 있는 코딩 에이전트에게 전달하세요. 미리 양식을 채울 필요는 없습니다. 에이전트가 부족한 정보를 질문하고 낯선 선택지를 설명하도록 구성했습니다. 서버에 직접 접속할 수 없는 에이전트라면 사용자가 실행할 명령과 결과 확인 방법을 제공해야 하며, 설치했다고 주장하면 안 됩니다.

SSH 비밀번호, 개인 키, 레지스트리 토큰, jastreamer 비밀번호는 프롬프트에 넣지 마세요. 기존 SSH 프로필·키 에이전트나 비공개 대화형 자격 증명 입력을 사용하세요. 설치를 승인하기 전에 제안된 경로와 서비스 변경 내용을 확인하세요.

<details>
<summary>셋업 프롬프트 펼쳐서 복사하기</summary>

```text
내 서버에 jastreamer Server와 Web Control을 설치하도록 도와줘.
https://github.com/furyheimdall/jastreamer 를 기준으로, 전달받은
이미지와 일치하는 리비전의 README.ko.md, INSTRUCTION.ko.md,
deploy/docker/server/compose.synology.yaml,
packaging/server/server.json을 먼저 읽어줘.
Web 화면은 Server에 내장되어 있어. 이 Linux/NAS 절차에서는 별도 Web
컨테이너나 네이티브 Windows Server 패키지 대신 Linux 컨테이너 하나를
구성해. 선택한 아키텍처의 FFmpeg, pyatv와 Python 실행 환경을 포함한
전체 Linux 이미지를 사용해. Go 실행 파일만 배포하거나 이 미디어
의존성을 호스트에 별도로 설치하지 마. Google Cast 자체에는 Chrome이나
Python helper가 필요 없어.
일반적인 조언만 하고 끝내거나 설정 파일 작성을 내게 맡기지 말고
설치를 끝까지 함께 진행해줘. 처음에는 서버 접속 정보와 음악 경로를
확인하고, 기술 정보는 서버를 조사해 채워줘. 나머지는 안전한 기본값을
제안하고, 취향이나 접근·데이터·서비스 중단에 영향을 주는 결정만
물어봐. 단계마다 지금 무엇을 하는지 쉽게 설명해줘.

1. 변경 전에 이 서버에 필요한 정보를 수집해줘.
   내가 이미 답했거나 허가된 읽기 전용 확인으로 알 수 있는 내용은
   다시 묻지 말고, 모르는 정보는 관련 질문끼리 묶어 물어봐.
   낯선 항목은 의미와 안전한 기본 선택지를 설명해줘.
   - 서버/NAS 주소, 기존 SSH 프로필 또는 접속 사용자와 포트,
     허용된 접속 방법, Linux/DSM 버전, CPU 아키텍처,
     Docker/Container Manager와 Compose 설치 여부.
   - 브라우저와 오디오 출력에서 접근할 수 있는 서버 LAN 주소,
     원하는 HTTP(S) 포트와 충돌 여부, 사용할 UPnP/DLNA·선택적
     Google Cast·AirPlay 출력. HTTPS가 필요하면 인증서·키 파일의
     위치를 묻되 개인 키 내용은 대화에 붙여 넣으라고 하지 마.
   - 내 PC가 아닌 서버에 실제로 존재하는 음악 폴더들의 절대 경로.
     네트워크 마운트를 포함해 경로가 존재하는지, UID/GID
     10001:10001이 파일을 읽고 디렉터리를 통과할 수 있는지 확인해.
   - 서로 분리된 영구 project/config/data 경로, 여유 공간,
     기존 jastreamer 설치·재생 여부, 보존할 계정·대기열·
     플레이리스트·자격 증명과 백업.
   - https://github.com/furyheimdall/jastreamer/releases 의 대상 프리뷰.
     릴리즈 메타데이터에서 ghcr.io/furyheimdall/jastreamer-server의
     정확한 다이제스트, 체크섬과 소스 리비전을 확인해.
     공개 이미지는 레지스트리 비밀번호가 필요 없어. 내가 별도로 전달받은
     오프라인·비공개 패키지를 선택한 경우에만 그 위치를 물어봐.
     게시된 패키지나 검증 근거를 확보하지 못하면 무엇이 필요한지
     설명하고, 이미지 URL·다이제스트를 지어내거나 가변 태그를
     사용하거나 임의의 다른 빌드로 대체하지 마.
   비밀정보를 대화·생성 파일·Git·로그에 기록하지 말고 기존의
   안전한 인증 수단이나 사용자의 비공개 직접 입력을 사용해.

2. 먼저 조사하고 구체적인 설치 계획을 보여준 뒤 승인받아줘.
   정확한 이미지·아키텍처, 프로젝트·백업 위치, 호스트와 컨테이너
   경로 대응, 리스너·LAN 접속 URL, 필요한 권한·네트워크 접근,
   서비스 중단 여부를 정리해. Linux amd64 또는 arm64만 지원해.
   서버 이름, 보관함 경로, HTTP/HTTPS, 선택적 Google Cast와 AirPlay
   사용 여부, LAN 인터페이스·접근 제한, media base URL·트랜스코딩,
   선호 UI 언어까지 전체 설정을 한 번에 제안해. 내가 명시적으로
   선택하지 않으면 Google Cast를 켜지 말고, 요구사항이나 확인한
   네트워크 때문에 바꿔야 하는 경우가 아니면 나머지 문서 기본값을
   유지해. 기본값과 다른 설정은 이유를 설명해.
   JSON 항목의 의미를 내가 알아야만 답할 수 있는 식으로 묻지 마.
   실제 경로를 확인해 config/data/music가 서로 겹치거나 중첩되지
   않게 하고, 심볼릭 링크로 상태 데이터가 음악으로 노출되는 경우도
   거부해. 음악 경로가 없다고 빈 폴더를 대신 만들어 진행하지 마.
   Docker 설치, 호스트 권한·방화벽 변경, 기존 서비스 중지·교체는
   영향을 설명하고 별도로 승인받아.

3. 승인 후 저장소의 Compose 템플릿을 바탕으로 영구 설정을 준비해줘.
   JASTREAMER_SERVER_IMAGE, JASTREAMER_CONFIG_PATH,
   JASTREAMER_DATA_PATH, JASTREAMER_MUSIC_PATH에 실제 값을 지정해.
   완성된 Compose 파일, 영구 환경 변수와 server.json은 직접
   작성하고, 경로의 공백·특수문자를 안전하게 처리해.
   자리표시자를 남기거나 임시 셸의 export에만 의존하지 마.
   직접 접속할 수 없다면 확인된 실제 경로를 반영한 완성 파일과
   실행 명령을 제공하고, 내가 전달한 결과를 확인한 뒤 다음 단계로 가.
   가져오기 전에 패키지 무결성과 대상 아키텍처를 확인하고,
   OCI는 안내서대로 변환해. OCI 아카이브를 docker load에 바로
   넣지 마. 검증한 레지스트리 다이제스트나 로컬 이미지 ID를 기록해.
   UID/GID 10001:10001, 읽기 전용 rootfs, capability 제거,
   no-new-privileges, tmpfs와 host networking을 유지해.
   브리지 포트 매핑을 추가하지 말고 server.json에서 리스너를
   설정하고 포트 충돌을 확인해.
   config는 /etc/jastreamer에 쓰기 가능, data는 /var/lib/jastreamer에
   쓰기 가능, 기존 음악은 /music에 읽기 전용으로 연결해.
   data_dir와 library_roots는 이 컨테이너 내부 경로에 맞추고,
   패키지의 FFmpeg·AirPlay helper 경로를 유지해. Google Cast에는
   별도의 Chrome이나 Python helper가 필요 없어.
   승인된 음악 경로가 여러 개라면 각각 읽기 전용 마운트와
   library_roots 항목을 추가하고, 더 넓은 상위 폴더를 노출하지 마.
   privileged 모드, chmod 777, 음악 폴더의 재귀적 chown/chmod는
   사용하지 마. 방화벽을 끄거나 서버를 공용 인터넷에 노출하지 마.
   기존 config/data를 덮어쓰거나 계정을 초기화하지 마.
   업데이트라면 재생·서비스 중지 허가를 받고, 서비스가 정지한
   상태에서 config/data 전체를 일관되게 백업한 뒤 진행해.
   롤백을 위해 이전 이미지와 그에 대응하는 백업을 보관해.
   시작 전에 docker compose config와 준비한 마운트를 사용하는
   이미지의 --check-config 명령으로 구성을 검증해.

4. 승인된 구성을 시작하고 실제 결과를 확인해줘.
   컨테이너 상태·로그, /healthz와 Web 화면을 확인하되,
   localhost뿐 아니라 실제 클라이언트에서도 접근되는지 확인해.
   실행 UID, 보안 옵션, 음악의 읽기 전용 연결, config/data 쓰기
   권한과 보관함 마운트를 확인해.
   최초 관리자 계정은 내가 브라우저에서 비공개로 만들도록 안내하고,
   기존 계정이 있으면 초기화 대신 로그인하도록 해.
   내가 비공개로 로그인한 뒤 허가된 브라우저 세션에서 합의한
   UI 언어와 나머지 설정을 적용하고 보관함 스캔을 시작해 완료
   결과까지 확인해. 브라우저를 조작할 수 없다면 남은 조작을
   정확히 안내하고 결과를 확인해. 내가 고른 출력만 재생 정지
   상태에서 선택하고, 임의로 출력을 고르거나 페어링하지 마.
   보관함 내용과 출력 검색은 확인하되 자동으로 재생을 시작하지 마.
   출력 페어링이나 재생 명령은 먼저 허락받아. 컨테이너 정상 상태나
   출력 검색 성공만으로 실제 소리가 난다고 주장하지 마.

5. 접속 URL, 정확한 이미지 식별자, 저장한 설정과 데이터 위치,
   실행 명령, 검증 결과, 백업·롤백 방법을 비밀정보 없이 정리해줘.
   완료한 작업과 내가 직접 확인해야 할 클라이언트 네트워크·
   실제 오디오 검증을 구분하고, 실패해도 기존 음악과 상태를 보존해.
   마지막에는 "이제 접속해서 테스트해 보세요"라는 짧은 안내를 붙여줘.
   실제로 확인한 LAN 접속 URL을 프로토콜·포트까지 포함한 클릭 가능한
   링크로 제공해. 예시 주소, localhost, 0.0.0.0을 사용자가 접속할
   주소로 남기지 마. 같은 LAN의 브라우저에서 열어 로그인하도록 하고,
   선택 사항인 데스크톱 앱도 같은 URL로 접속할 수 있다고 알려줘.
   음악 목록에 내 곡이 보이는지 확인한 뒤, 정지 상태에서 원하는 출력을
   선택하고 곡 하나를 직접 재생해 실제 소리가 나는지 테스트하도록
   안내해. 지원되는 경우 일시 정지·탐색을 확인하고 마지막에 정지하는
   순서도 제안해. 이는 사용자가 직접 하는 테스트이며 자동 재생 허가는
   아니야. 각 단계의 기대 결과와 함께, 실패하면 비밀번호 등 비밀정보
   없이 어느 단계에서 어떤 오류가 났는지 알려 달라고 안내해.
   확보하지 못한 전제조건이나 내가 직접 해야 하는 단계가 남았다면
   설치 완료라고 하지 말고 정확히 무엇이 필요한지 알려줘.
```

</details>

이 프롬프트는 설치 절차 안내이며 무인 설치 프로그램이나 기존 운영 환경을 검토 없이 변경해도 된다는 허가는 아닙니다. 패키지 취급과 설정의 기준은 [사용자 안내서](INSTRUCTION.ko.md)입니다.

## 업데이트

Linux Server는 config·data 마운트를 보존하면서 컨테이너 이미지를 교체해 업데이트합니다. 네이티브 Windows Server는 새 ZIP을 검증해 별도 폴더에 푼 다음 기존 `server.json`, `data`, `music`을 보존하면서 기존 폴더의 패키지 소유 파일만 교체합니다. 어느 쪽도 앱을 다시 설치하거나 데이터를 초기화하지 않습니다. Server가 제공하는 Web 화면과 내장된 선택적 Google Cast 기능은 두 대상에서 함께 갱신되지만 FFmpeg와 AirPlay는 지원되는 Linux 컨테이너에만 포함됩니다. 선택 사항인 데스크톱 앱 실행 파일은 별도의 ZIP 또는 DEB로 업데이트합니다.

현재 앱 내 새 버전 확인이나 자동 업데이트 기능은 없습니다. 정확한 검증 대상과 체크섬을 다운로드한 뒤 재생과 Server를 멈추고 영구 상태를 백업한 다음 교체하세요. 검증 후 기존 서버 주소로 접속해 Web 화면을 새로고침하면 됩니다. 재생은 자동으로 재개되지 않습니다.

Linux 컨테이너의 실제 절차와 업데이트 후 확인 사항은 [백업과 업데이트](INSTRUCTION.ko.md#6-백업과-업데이트)를, Windows의 최초 설치와 업데이트는 [네이티브 Windows x64 무설치 Server](INSTRUCTION.ko.md#네이티브-windows-x64-무설치-server)를 참고하세요. 레지스트리 배포는 Linux 이미지에 적용되며 오프라인 패키지도 대안으로 사용할 수 있습니다.

## 호환성 범위

자동 검색에는 멀티캐스트와 Server·앱·출력 사이의 올바른 LAN 경로가 필요합니다. UPnP는 SSDP UDP 1900을 사용하고 Google Cast와 Linux AirPlay는 mDNS UDP 5353을 사용합니다. 출력 기능은 기기마다 다르며 일부 수신기는 일시 정지나 탐색을 제공하지 않거나 특정 형식을 거부하거나 인증을 요구할 수 있습니다. Google Cast는 Linux와 네이티브 Windows에서 사용할 수 있지만 기본값은 꺼짐이며 AirPlay는 Linux 전용입니다. 실제 사용할 장비에서 검색, 제어, 대기열 진행과 소리를 확인하세요.

## 라이선스

jastreamer는 [Apache License 2.0](LICENSE)으로 배포됩니다. 패키지에 포함된 제3자 구성 요소에는 각자의 라이선스가 적용됩니다. 전달받은 서버 패키지의 `THIRD-PARTY-NOTICES.txt` 또는 컨테이너의 `/usr/share/jastreamer/THIRD-PARTY-NOTICES.txt`를 확인하세요.
