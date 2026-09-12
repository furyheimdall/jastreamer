# jastreamer

<img src="assets/jastreamer.svg" width="80" height="80" alt="jastreamer 로고" />

jastreamer 0.2.0은 신뢰하는 사설 LAN에서 사용하는 자체 호스팅 음악 서버입니다. Linux Server 하나가 관리자가 승인한 로컬 음악을 색인하고 Web 화면을 제공하며, 대기열과 플레이리스트를 SQLite에 보관하고 선택한 네트워크 출력 하나로 오디오를 전송합니다. UPnP/DLNA는 기본으로 제공되며 지원되는 Linux 컨테이너 플랫폼에서는 AirPlay 전송도 사용할 수 있습니다.

화면 기본 언어는 영어이며 한국어도 지원합니다. 메뉴 이름은 두 언어 모두 **Settings**로 표시합니다. 이 화면 안의 **Language / 언어**에서 언어를 선택할 수 있습니다.

- [한국어 사용자 안내서](INSTRUCTION.ko.md)
- [English README](README.md)
- [English user guide](INSTRUCTION.md)

## 주요 기능

- 원본 보관함을 수정하지 않고 FLAC, MP3, WAV/WAVE, Ogg/Vorbis, Opus, M4A 파일 스캔
- 곡, 앨범, 아티스트, 장르, 폴더별 탐색과 검색, 내장 앨범 아트 표시
- 저장된 태그와 확인된 오디오·파일 정보를 필요할 때 표시
- 플레이리스트와 중복 곡을 보존하는 서버 전역 대기열 유지
- 하단 플레이 바의 앨범 아트는 대기열로 이동; 보관함 `(i)`와 대기열 앨범 아트는 곡 정보 표시; 대기열 앨범 아트에 마우스를 올리면 큰 `(i)` 표시; 삼각형 재생 버튼만 해당 곡 재생
- 선택 출력이 지원할 때 재생, 일시 정지, 정지, 이전 곡, 다음 곡, 탐색 제공
- LAN의 UPnP/DLNA 및 AirPlay 출력 검색
- 수신기가 요구할 때 AirPlay PIN·암호 인증
- 최초 계정 생성, 비밀번호 로그인·변경·복구와 로그인 상태 유지
- 기본 사설 LAN HTTP와 운영자가 제공한 PEM 인증서·키를 사용하는 선택적 내장 HTTPS

선택 사항인 Windows 10/11 x64 무설치 앱은 서버 접속용입니다. jastreamer 서버를 찾거나 HTTP(S) 주소를 입력받아 서버가 제공하는 웹 화면을 표시합니다. Windows용 서버나 로컬 오디오 플레이어는 아닙니다.

## 배포 방식

서버는 `amd64` 또는 `arm64`용 Linux 컨테이너로 배포합니다. 웹 화면, 오디오 전용 FFmpeg 8.1.2 실행 파일과 pyatv 0.18.0 AirPlay 송신 프로그램이 포함됩니다. 제공된 Compose 파일로 Synology Container Manager에 설치할 수 있습니다. 0.2 배포는 비공개입니다. 전달받은 검증된 패키지나 승인된 비공개 레지스트리의 정확한 이미지 다이제스트만 사용하세요. 공개 이미지나 다운로드는 제공하지 않습니다.

저장소 세 종류를 분리하세요.

- **config:** 쓰기 가능한 `server.json`과 선택적 HTTPS PEM 파일
- **data:** 쓰기 가능한 SQLite 데이터베이스, 앨범 아트 캐시와 AirPlay 상태
- **music:** 읽기 전용으로 연결하는 기존 음악 보관함

컨테이너를 교체할 때 config와 data를 보존해야 합니다. jastreamer를 위해 음악 보관함 전체의 소유권이나 권한을 재귀적으로 바꾸지 말고, 서버를 공용 인터넷에 직접 노출하지 마세요.

패키지 확인, Docker·Synology 설치, Windows 앱, 최초 사용, 언어 선택, 업데이트와 문제 해결은 [한국어 사용자 안내서](INSTRUCTION.ko.md)를 참고하세요.

## 호환성 범위

자동 검색에는 멀티캐스트와 서버·앱·출력 사이의 올바른 LAN 경로가 필요합니다. 출력 기능은 기기마다 다릅니다. 일부 수신기는 일시 정지나 탐색을 제공하지 않거나 특정 형식을 거부하거나 인증을 요구할 수 있습니다. 한정된 검증 결과가 모든 수신기나 장시간 재생의 품질을 보장하지는 않습니다. 실제 사용할 장비에서 검색, 인증, 제어, 대기열 진행과 소리를 확인하세요.

## 라이선스

jastreamer는 [Apache License 2.0](LICENSE)으로 배포됩니다. 패키지에 포함된 제3자 구성 요소에는 각자의 라이선스가 적용됩니다. 전달받은 서버 패키지의 `THIRD-PARTY-NOTICES.txt` 또는 컨테이너의 `/usr/share/jastreamer/THIRD-PARTY-NOTICES.txt`를 확인하세요.
