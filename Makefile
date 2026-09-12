.PHONY: verify web-verify go-verify build web-build server-build server-build-only browser-smoke browser-smoke-only desktop-verify desktop-package package package-verify clean

VERSION := $(shell cat apps/server/VERSION)
SOURCE_REVISION ?= $(shell git rev-parse --verify HEAD)
SOURCE_DATE_EPOCH ?= $(shell git show -s --format=%ct $(SOURCE_REVISION))

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

desktop-package:
	cd apps/desktop && npm ci && npm run package:win && npm run verify:package

package:
	SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH) JASTREAMER_SOURCE_REVISION=$(SOURCE_REVISION) packaging/server/release.sh dist/release
package-verify:
	packaging/server/verify.sh dist/release

clean:
	rm -rf apps/server/dist apps/server/web/ui/dist dist/release
