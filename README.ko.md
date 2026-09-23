# jastreamer

[![CI](https://github.com/furyheimdall/jastreamer/actions/workflows/ci.yml/badge.svg?branch=main&event=push)](https://github.com/furyheimdall/jastreamer/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![TypeScript](https://img.shields.io/badge/TypeScript-3178C6?logo=typescript&logoColor=white)](https://www.typescriptlang.org/)
[![Python](https://img.shields.io/badge/Python-3776AB?logo=python&logoColor=white)](https://www.python.org/)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

<img src="assets/jastreamer.svg" width="80" height="80" alt="jastreamer 로고" />

jastreamer 0.2.0은 신뢰하는 사설 LAN에서 사용하는 자체 호스팅 음악 서버입니다. Linux 또는 Windows Server가 관리자가 승인한 로컬 음악을 색인하고 Web 화면을 제공하며, 좋아요·대기열·플레이리스트를 SQLite에 보관하고 선택한 네트워크 출력 또는 접속 중인 브라우저 하나로 오디오를 전송합니다. UPnP/DLNA는 기본으로 제공되고 Google Cast는 두 플랫폼에서 선택적으로 켤 수 있습니다. AirPlay 전송은 지원되는 Linux 컨테이너 패키지에서만 사용할 수 있습니다.

영어를 기본으로 한국어 UI를 지원합니다.

- [에이전트 설치·업데이트 안내](AGENTS.md)
- [한국어 설치 및 업데이트 안내서](INSTALL.ko.md)
- [한국어 사용자 안내서](INSTRUCTION.ko.md)
- [English README](README.md)
- [English installation guide](INSTALL.md)
- [English user guide](INSTRUCTION.md)

## 주요 기능

- 원본 보관함을 수정하지 않고 FLAC, MP3, WAV/WAVE, Ogg/Vorbis, Opus, M4A 파일 스캔
- 곡, 앨범, 아티스트, 장르, 폴더별 탐색과 검색, 내장 앨범 아트 표시
- 저장된 태그와 확인된 오디오·파일 정보를 필요할 때 표시
- 플레이리스트와 중복 곡을 보존하는 서버 전역 대기열 유지
- 곡 좋아요 저장과 재생 가능한 좋아요 곡 전체의 셔플 플레이리스트 생성
- 같은 Server 대기열을 사용하는 브라우저 오디오 **이 기기** 출력
- 호환되는 네트워크 출력의 재생과 탐색 제어
- LAN의 UPnP/DLNA, 선택적 Google Cast 및 Linux AirPlay 출력 검색
- 수신기가 요구할 때 AirPlay PIN·암호 인증
- 최초 계정 생성, 비밀번호 로그인·변경·복구와 로그인 상태 유지
- 기본 사설 LAN HTTP와 운영자가 제공한 PEM 인증서·키를 사용하는 선택적 내장 HTTPS
- iPhone·Android 휴대전화용 네 탭 화면, 간결하게 접히는 재생 제어와 선택 사항인 설치형 Web 앱(PWA)
- 확인된 Server 검색, 격리된 WebView 세션과 시스템 미디어 제어를 갖춘 Server 주도 Media3 로컬 재생을 제공하는 네이티브 Android 클라이언트
- 같은 Server Web 화면을 사용하는 네이티브 SwiftUI/WKWebView iOS 소스와 시뮬레이터 CI

휴대전화 화면은 iPhone과 Android 휴대전화 브라우저에서만 자동으로 사용되며 iPad와 다른 태블릿은 기존 화면을 유지합니다. PWA는 Server의 네트워크 클라이언트일 뿐 보관함 cache나 오프라인 재생을 제공하지 않습니다.

## 배포 방식

- **Linux Server:** 내장 Web 화면, Python 3.12, 오디오 전용 FFmpeg 8.1.2와 pyatv 0.18.0 AirPlay 송신 프로그램을 포함하는 `amd64`·`arm64` 컨테이너입니다. Synology Container Manager에서는 제공된 Compose 정의를 사용합니다.
- **네이티브 Windows Server:** 내장 Web 화면, UPnP/DLNA와 선택적 Google Cast를 제공하는 미서명 x64 무설치 ZIP입니다. AirPlay 송신 프로그램, 네이티브 오디오 엔진 또는 FFmpeg 변환기를 포함하지 않으며 Windows 서비스가 아닙니다.
- **선택 사항인 데스크톱 앱:** Windows 10/11 x64 무설치 ZIP 또는 Linux amd64 DEB로 제공됩니다. Server를 찾거나 HTTP(S) 주소를 입력받아 Server의 Web 화면을 표시합니다. Server나 독립 네이티브 오디오 엔진을 추가하지 않으며 로컬 출력은 공유 Web 화면을 사용합니다. ARM64 데스크톱 패키지는 없으며 Linux arm64 클라이언트는 브라우저를 사용할 수 있습니다.
- **Android 클라이언트:** Android 10 이상과 독립 프로필을 지원하는 최신 WebView가 필요한 Kotlin/WebView 앱입니다. Server의 휴대전화·태블릿 화면을 그대로 사용하며 Media3 포그라운드 서비스로 네이티브 로컬 재생을 제공합니다. [Android CI](https://github.com/furyheimdall/jastreamer/actions/workflows/android.yml)는 개발·테스트 APK와 미서명 release 빌드를 제공하며 production 서명이나 Play Store 배포는 아닙니다. [Android 설치](INSTALL.ko.md#android)를 참고하세요.
- **iOS 소스와 CI:** iOS/iPadOS 18.4 이상용 SwiftUI/WKWebView 앱입니다. [iOS CI](https://github.com/furyheimdall/jastreamer/actions/workflows/ios.yml)는 iPhone 시뮬레이터에서 실제 Web 화면을 검증하고 미서명 기기용 개발 번들과 시뮬레이터 전용 번들을 만듭니다. 설치 가능한 iPhone 패키지, TestFlight나 App Store 릴리즈는 제공하지 않습니다. [iOS 개발 범위](INSTALL.ko.md#ios)를 참고하세요.

Google Cast 자체에는 어느 Server 플랫폼에서도 Chrome이나 Python helper가 필요하지 않습니다. 전체 [GitHub Releases 목록](https://github.com/furyheimdall/jastreamer/releases)에서 올바르게 표시된 프리뷰까지 포함해 가장 최근의 호환되는 게시 Server 릴리즈를 선택하고 provenance와 패키지 checksum 또는 정확한 이미지 다이제스트를 검증하세요. 가변 `latest`를 사용하거나 `/releases/latest`가 프리뷰를 포함한다고 가정하지 마세요.

- **config:** 쓰기 가능한 `server.json`과 선택적 HTTPS PEM 파일
- **data:** 쓰기 가능한 SQLite 데이터베이스, 앨범 아트 cache와 AirPlay 상태
- **music:** 읽기 전용 기존 절대 음악 루트 또는 사용자가 생성·사용에 명시적으로 동의한 새 음악 루트

manifest에 샘플 묶음이 포함된 릴리즈는 기존 파일을 덮어쓰지 않고 `jastreamer-samples`에 MP3 세 개를 둘 수 있습니다. 어느 Server 대상을 교체하든 기존 배포 경로, config, data와 원본 음악을 보존하고 음악 보관함 전체의 소유권·권한을 재귀적으로 바꾸거나 Server를 공용 인터넷에 직접 노출하지 마세요.

수동 설정, 패키지 검증, Linux·Synology·Windows 설치, 샘플, PWA 설정, 업데이트·복구 절차는 [한국어 설치 및 업데이트 안내서](INSTALL.ko.md)를, 에이전트 작업 지침은 [AGENTS.md](AGENTS.md)를 참고하세요. 일상적인 사용과 문제 해결은 [한국어 사용자 안내서](INSTRUCTION.ko.md)를 따르세요.

## 선택 사항인 Google Cast

Google Cast는 기본적으로 꺼져 있으며 이전 `server.json`에 `cast.enabled`가 없을 때도 `false`로 처리됩니다. **Settings → 재생·출력**에서 **Google Cast 출력**을 켜고 저장한 뒤 Server를 재시작하세요. 수신기가 보인다는 이유만으로 임의로 켜면 안 됩니다. 검색에는 선택한 `network.interfaces`의 mDNS UDP 5353이 필요합니다. Server에서 수신기가 mDNS로 광고한 Cast TCP 포트에 연결할 수 있어야 하고, 수신기에서는 **재생 기기가 음원을 가져올 Server URL**에 접근할 수 있어야 합니다. 이 설정은 보통 비워 두어 자동 선택하게 하고 라우팅 때문에 필요할 때만 수신기가 접근할 수 있는 실제 Server 주소를 지정하세요.

Cast 원본 직접 스트리밍은 검사된 codec·sample rate·channel 메타데이터와 해당되는 경우 bit depth를 보수적으로 판단합니다. 모든 직접 전송 원본은 일치하는 codec이 확인되고 sample rate가 양수이며 mono 또는 stereo여야 합니다. FLAC은 96 kHz 및 1–24-bit까지, MP3·Ogg/Vorbis·Ogg/Opus·M4A/AAC는 48 kHz까지, LPCM WAV는 48 kHz 및 1–16-bit까지 허용합니다. 그 밖의 형식이나 확인되지 않은 원본은 미디어 변환을 켜고 FFmpeg를 설정해야 하며, 변환 결과는 탐색할 수 없는 44.1 kHz stereo 16-bit WAV 스트림입니다. 원본 파일은 변경하지 않습니다.

Cast도 다른 출력과 같은 Server 대기열 하나를 사용합니다. Cast LOAD의 autoplay는 사용하지 않고 Play를 명시적으로 전송합니다. 수신기 그룹과 gapless 재생은 지원하지 않으며 직접 스트리밍이나 변환 모두 bit-perfect를 보장하지 않습니다. 일시 정지·탐색·형식 등 기능은 수신기에 따라 다릅니다. Cast 제어는 지속 TLS 연결을 사용하고 jastreamer가 소유한 애플리케이션·미디어 세션에서 명시적인 `FINISHED` 상태를 받은 경우에만 대기열을 진행합니다.

## AI 에이전트로 설치하기

에이전트에게 다음 요청을 보내세요. 설치 전에는 제안한 경로와 서비스 변경을 직접 검토하고 비밀번호·개인 키·토큰을 프롬프트에 넣지 마세요.

> https://github.com/furyheimdall/jastreamer 에서 jastreamer를 설치하거나 업데이트하도록 도와줘.  
> 저장소 루트의 [AGENTS.md](AGENTS.md)를 먼저 읽고 따라줘.

## 업데이트

Linux Server는 config·data 마운트를 보존하면서 컨테이너 이미지를 교체해 업데이트합니다. 네이티브 Windows Server는 새 ZIP을 검증해 별도 폴더에 푼 다음 기존 `server.json`, `data`, `music`을 보존하면서 기존 폴더의 패키지 소유 파일만 교체합니다. 어느 쪽도 앱을 다시 설치하거나 데이터를 초기화하지 않습니다. Server가 제공하는 Web 화면과 내장된 선택적 Google Cast 기능은 두 대상에서 함께 갱신되지만 FFmpeg와 AirPlay는 지원되는 Linux 컨테이너에만 포함됩니다. 선택 사항인 데스크톱 앱 실행 파일은 별도의 ZIP 또는 DEB로 업데이트합니다.
Android wrapper는 같은 인증서로 서명한 호환 APK로 별도 업데이트하며 Server의 Web 화면 변경만으로 wrapper를 교체할 필요는 없습니다. 현재 CI APK는 개발 산출물이며 확립된 production 업데이트 경로가 아닙니다.

iOS wrapper는 현재 소스와 CI만 제공합니다. 기기용 서명·설치·배포와 production 업데이트 경로는 Server Web 화면 갱신과 별도입니다.

현재 앱 내 새 버전 확인이나 자동 업데이트 기능은 없습니다. 정확한 대상 패키지와 체크섬을 다운로드·검증한 뒤 재생과 Server를 멈추고, 기존 설정·데이터·음악 경로·마운트를 보존하며 패키지 소유 파일이나 이미지만 교체하세요. 검증 후 기존 서버 주소로 접속해 Web 화면을 새로고침하면 됩니다. 재생은 자동으로 재개되지 않습니다.

Linux 컨테이너의 실제 절차와 업데이트 후 확인 사항은 [업데이트](INSTALL.ko.md#upgrade)를, Windows의 최초 설치와 업데이트는 [네이티브 Windows x64 무설치 Server](INSTALL.ko.md#windows-server)를 참고하세요. 레지스트리 배포는 Linux 이미지에 적용되며 오프라인 패키지도 대안으로 사용할 수 있습니다.

## 호환성 범위

자동 검색에는 멀티캐스트와 Server·앱·출력 사이의 올바른 LAN 경로가 필요합니다. UPnP는 SSDP UDP 1900을 사용하고 Google Cast와 Linux AirPlay는 mDNS UDP 5353을 사용합니다. 출력 기능은 기기마다 다르며 일부 수신기는 일시 정지나 탐색을 제공하지 않거나 특정 형식을 거부하거나 인증을 요구할 수 있습니다. Google Cast는 Linux와 네이티브 Windows에서 사용할 수 있지만 기본값은 꺼짐이며 AirPlay는 Linux 전용입니다. 실제 사용할 장비에서 검색, 제어, 대기열 진행과 소리를 확인하세요. 검색 성공이나 정상적인 Server 상태만으로 실제 소리가 나는지는 입증되지 않습니다.

사설 LAN HTTP는 인증 정보나 오디오를 암호화하지 않습니다. 필요한 경우 운영자가 제공한 신뢰할 수 있는 PEM 인증서와 키로 Server의 내장 HTTPS 수신 주소를 사용하세요. 휴대전화 PWA는 localhost 개발 주소를 제외하면 신뢰할 수 있는 HTTPS가 필요합니다. 인증서 경고를 우회하거나 NAS를 공용 인터넷에 노출하지 마세요.

## 라이선스

jastreamer는 [Apache License 2.0](LICENSE)으로 배포됩니다. 패키지에 포함된 제3자 구성 요소에는 각자의 라이선스가 적용됩니다. 전달받은 서버 패키지의 `THIRD-PARTY-NOTICES.txt` 또는 컨테이너의 `/usr/share/jastreamer/THIRD-PARTY-NOTICES.txt`를 확인하세요.
