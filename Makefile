.PHONY: verify web-verify go-verify build web-build server-build server-build-only browser-smoke browser-smoke-only desktop-verify desktop-native-windows desktop-native-dependencies desktop-native-decoder desktop-native-decoder-check desktop-package package package-verify windows-server-package clean

VERSION := $(shell cat apps/server/VERSION)
SOURCE_REVISION ?= $(shell git rev-parse --verify HEAD)
SOURCE_DATE_EPOCH ?= $(shell git show -s --format=%ct $(SOURCE_REVISION))
WINDOWS_SERVER_BINARY ?= apps/server/dist/jastreamer-server.exe
NATIVE_AUDIO_DIR := apps/desktop/native/audio
NATIVE_FFMPEG_PREFIX ?= $(NATIVE_AUDIO_DIR)/deps/install/ffmpeg
NATIVE_AUDIO_BUILD_DIR ?= $(NATIVE_AUDIO_DIR)/build
NATIVE_JOBS ?= 4


verify: web-build
	$(MAKE) go-verify

web-verify: web-build

go-verify:
	cd apps/server && go test ./... -count=1
	cd apps/server && go vet ./...

build: server-build

web-build:
	cd apps/control && npm ci && npm run build

server-build: web-build
	$(MAKE) server-build-only
server-build-only:
	mkdir -p apps/server/dist
	cd apps/server && CGO_ENABLED=0 go build -trimpath \
		-ldflags='-s -w -X main.productVersion=$(VERSION) -X main.sourceRevision=$(SOURCE_REVISION)' \
		-o dist/jastreamer-server ./cmd/jastreamer-server

browser-smoke: build
	$(MAKE) browser-smoke-only
browser-smoke-only:
	cd apps/control && ./node_modules/.bin/playwright test --config ../../tooling/qa/playwright.config.mjs

desktop-verify:
	cd apps/desktop && npm ci && npm test
desktop-native-windows:
	cd apps/desktop && SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH) JASTREAMER_SOURCE_REVISION=$(SOURCE_REVISION) npm run build:native:win

desktop-native-dependencies:
	node $(NATIVE_AUDIO_DIR)/scripts/fetch-native-deps.mjs
	SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH) JOBS=$(NATIVE_JOBS) JASTREAMER_FFMPEG_TOOLCHAIN=native \
		$(NATIVE_AUDIO_DIR)/scripts/build-ffmpeg.sh \
		$(NATIVE_AUDIO_DIR)/deps/src/ffmpeg-8.1.2 \
		$(NATIVE_FFMPEG_PREFIX)

desktop-native-decoder: desktop-native-dependencies
	cmake -S $(NATIVE_AUDIO_DIR) -B $(NATIVE_AUDIO_BUILD_DIR) \
		-DCMAKE_BUILD_TYPE=Release \
		-DJASTREAMER_FFMPEG_ROOT=$(abspath $(NATIVE_FFMPEG_PREFIX)) \
		-DBUILD_TESTING=ON
	cmake --build $(NATIVE_AUDIO_BUILD_DIR) --parallel $(NATIVE_JOBS) \
		--target jastreamer-decoder-smoke jastreamer-decoder-behavior

desktop-native-decoder-check: desktop-native-decoder
	$(NATIVE_AUDIO_BUILD_DIR)/jastreamer-decoder-behavior


desktop-package:
	cd apps/desktop && npm ci && \
		SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH) JASTREAMER_SOURCE_REVISION=$(SOURCE_REVISION) npm run package:win && \
		SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH) JASTREAMER_SOURCE_REVISION=$(SOURCE_REVISION) npm run verify:package

package:
	SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH) JASTREAMER_SOURCE_REVISION=$(SOURCE_REVISION) packaging/server/release.sh dist/release
package-verify:
	packaging/server/verify.sh dist/release

windows-server-package:
	SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH) python3 packaging/server/windows_package.py package \
		--binary "$(WINDOWS_SERVER_BINARY)" \
		--output-dir dist/server-windows-x64 \
		--source-revision "$(SOURCE_REVISION)"

clean:
	rm -rf apps/server/dist apps/server/web/ui/dist dist/release dist/server-windows-x64 \
		$(NATIVE_AUDIO_DIR)/build $(NATIVE_AUDIO_DIR)/deps $(NATIVE_AUDIO_DIR)/dist
