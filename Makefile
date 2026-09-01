.PHONY: test test-race lint verify-model fetch-model package-model-linux package-model-macos build-linux build-macos desktop-build build-public-macos-arm64 release-public-macos-arm64 publish-public-macos-arm64 build-private-macos-arm64 build-universal-macos-arm64-local build-license-generator-windows tidy

GO ?= go
WAILS ?= $(HOME)/go/bin/wails
PUBLIC_VERSION ?=
LICENSE_PUBLIC_KEY ?=
APPCAST_URL ?=
REVOCATION_MANIFEST_URL ?=
REVOCATION_KEY_ID ?=
REVOCATION_PUBLIC_KEY ?=
REVOCATION_BUILD_METADATA := TCREVBUILD1|$(REVOCATION_MANIFEST_URL)|$(REVOCATION_KEY_ID)|$(REVOCATION_PUBLIC_KEY)
PUBLIC_BOOTSTRAP_BUNDLE_PATH ?=
PUBLIC_BOOTSTRAP_BUNDLE_SHA256 ?=
PUBLIC_BOOTSTRAP_SEED_EXPORT_FILE ?=
PUBLIC_BOOTSTRAP_SEED_PASSWORD_FILE ?=
TELEGRAM_COMPANION_RUNTIME_BUNDLE ?=
PUBLIC_BUILD_TAGS := desktop,public_macos_arm64
PUBLIC_MACOS_LDFLAGS := -s -w -X telegram-companion/internal/buildinfo.version=$(PUBLIC_VERSION) -X telegram-companion/internal/buildinfo.licensePublicKey=$(LICENSE_PUBLIC_KEY) -X telegram-companion/internal/buildinfo.appcastURL=$(APPCAST_URL) -X telegram-companion/internal/buildinfo.revocationManifestURL=$(REVOCATION_MANIFEST_URL) -X telegram-companion/internal/buildinfo.revocationKeyID=$(REVOCATION_KEY_ID) -X telegram-companion/internal/buildinfo.revocationPublicKey=$(REVOCATION_PUBLIC_KEY) -X telegram-companion/internal/buildinfo.revocationBuildMetadata=$(REVOCATION_BUILD_METADATA)
PRIVATE_RELEASE_ROOT ?= $(CURDIR)/build/release/macos-arm64-private
PRIVATE_DMG_PATH ?= $(PRIVATE_RELEASE_ROOT)/artifacts/Telegram-Companion-$(PUBLIC_VERSION)-arm64-private.dmg
PUBLIC_MACOS_ENV = GOOS=darwin GOARCH=arm64 MACOSX_DEPLOYMENT_TARGET=13.0 BUILD_TAGS=$(PUBLIC_BUILD_TAGS) GO_LDFLAGS='$(PUBLIC_MACOS_LDFLAGS)' RELEASE_CHANNEL=public WAILS_BIN='$(WAILS)' REVOCATION_MANIFEST_URL='$(REVOCATION_MANIFEST_URL)' REVOCATION_KEY_ID='$(REVOCATION_KEY_ID)' REVOCATION_PUBLIC_KEY='$(REVOCATION_PUBLIC_KEY)' PUBLIC_BOOTSTRAP_BUNDLE_PATH='$(PUBLIC_BOOTSTRAP_BUNDLE_PATH)' PUBLIC_BOOTSTRAP_BUNDLE_SHA256='$(PUBLIC_BOOTSTRAP_BUNDLE_SHA256)' PUBLIC_BOOTSTRAP_SEED_EXPORT_FILE='$(PUBLIC_BOOTSTRAP_SEED_EXPORT_FILE)' PUBLIC_BOOTSTRAP_SEED_PASSWORD_FILE='$(PUBLIC_BOOTSTRAP_SEED_PASSWORD_FILE)'
PRIVATE_MACOS_ENV = GOOS=darwin GOARCH=arm64 MACOSX_DEPLOYMENT_TARGET=13.0 BUILD_TAGS=$(PUBLIC_BUILD_TAGS) GO_LDFLAGS='$(PUBLIC_MACOS_LDFLAGS)' WAILS_BIN='$(WAILS)' REVOCATION_MANIFEST_URL='$(REVOCATION_MANIFEST_URL)' REVOCATION_KEY_ID='$(REVOCATION_KEY_ID)' REVOCATION_PUBLIC_KEY='$(REVOCATION_PUBLIC_KEY)' RELEASE_ROOT='$(PRIVATE_RELEASE_ROOT)' DMG_PATH='$(PRIVATE_DMG_PATH)' RELEASE_CHANNEL=local

export RELEASE_VERSION := $(PUBLIC_VERSION)
export LICENSE_PUBLIC_KEY
export APPCAST_URL
export REVOCATION_MANIFEST_URL
export REVOCATION_KEY_ID
export REVOCATION_PUBLIC_KEY

test:
	GOMAXPROCS=2 $(GO) test -p=1 ./...
	GOMAXPROCS=2 $(GO) test -p=1 -tags desktop ./...

test-race:
	GOMAXPROCS=2 $(GO) test -p=1 -race ./...

lint:
	GOMAXPROCS=1 golangci-lint run --concurrency 1 ./...

verify-model:
	$(GO) run ./scripts/fetch_model.go -verify-manifest-only

fetch-model:
	$(GO) run ./scripts/fetch_model.go
	$(GO) run ./scripts/fetch_model.go -verify-payload

package-model-linux:
	$(GO) run ./scripts/fetch_model.go -package-root build/bin

package-model-macos:
	$(GO) run ./scripts/fetch_model.go -package-root "build/bin/telegram-companion.app/Contents/Resources"

build-linux: fetch-model
	PATH=$$PATH:/usr/local/go/bin:$$HOME/go/bin $(WAILS) build -clean -tags desktop,webkit2_41
	$(MAKE) package-model-linux

build-macos: fetch-model
	test -n "$(TELEGRAM_COMPANION_RUNTIME_BUNDLE)"
	bash ./build/macos/generate_icon.sh
	PATH=$$PATH:/usr/local/go/bin:$$HOME/go/bin $(WAILS) build -clean -tags desktop
	$(MAKE) package-model-macos
	TELEGRAM_COMPANION_RUNTIME_BUNDLE='$(TELEGRAM_COMPANION_RUNTIME_BUNDLE)' bash ./scripts/package_macos_dev_runtime.sh

build-public-macos-arm64:
	$(PUBLIC_MACOS_ENV) bash ./scripts/release/macos-arm64/preflight.sh
	$(PUBLIC_MACOS_ENV) bash ./scripts/release/macos-arm64/build-stage.sh

release-public-macos-arm64: build-public-macos-arm64
	$(PUBLIC_MACOS_ENV) bash ./scripts/release/macos-arm64/preflight-public-release.sh
	$(PUBLIC_MACOS_ENV) bash ./scripts/release/macos-arm64/embed-public-bootstrap-bundle.sh
	$(PUBLIC_MACOS_ENV) bash ./scripts/release/macos-arm64/sign.sh
	$(PUBLIC_MACOS_ENV) bash ./scripts/release/macos-arm64/notarize.sh
	$(PUBLIC_MACOS_ENV) bash ./scripts/release/macos-arm64/appcast.sh
	$(PUBLIC_MACOS_ENV) bash ./scripts/release/macos-arm64/verify.sh

publish-public-macos-arm64:
	$(PUBLIC_MACOS_ENV) bash ./scripts/release/macos-arm64/preflight-public-publish.sh
	$(PUBLIC_MACOS_ENV) bash ./scripts/release/macos-arm64/verify.sh
	$(PUBLIC_MACOS_ENV) bash ./scripts/release/macos-arm64/publish.sh

build-private-macos-arm64:
	$(PRIVATE_MACOS_ENV) bash ./scripts/release/macos-arm64/preflight-private.sh
	$(PRIVATE_MACOS_ENV) bash ./scripts/release/macos-arm64/build-stage.sh
	$(PRIVATE_MACOS_ENV) bash ./scripts/release/macos-arm64/sign-adhoc.sh
	$(PRIVATE_MACOS_ENV) bash ./scripts/release/macos-arm64/package-private.sh
	$(PRIVATE_MACOS_ENV) bash ./scripts/release/macos-arm64/verify-private.sh

build-universal-macos-arm64-local:
	PUBLIC_VERSION='$(PUBLIC_VERSION)' PUBLIC_BOOTSTRAP_SEED_EXPORT_FILE='$(PUBLIC_BOOTSTRAP_SEED_EXPORT_FILE)' PUBLIC_BOOTSTRAP_SEED_PASSWORD_FILE='$(PUBLIC_BOOTSTRAP_SEED_PASSWORD_FILE)' bash ./scripts/release/macos-arm64/build-universal-local.sh

build-license-generator-windows:
	mkdir -p dist
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 $(GO) build -tags production -trimpath -ldflags '-s -w -H=windowsgui' -o dist/telegram-companion-license-generator.exe ./cmd/license-generator

desktop-build: build-linux

tidy:
	$(GO) mod tidy
