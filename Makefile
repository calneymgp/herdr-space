GO := $(if $(wildcard .tools/go1.27.1/bin/go),$(CURDIR)/.tools/go1.27.1/bin/go,go)
export GOTOOLCHAIN := go1.27.1
export PATH := $(CURDIR)/.tools/go1.27.1/bin:$(PATH)

.PHONY: toolchain deps frontend build test verify dev clean browser-deps license-bundle license-check

toolchain:
	python3 scripts/install-toolchain.py

deps:
	$(GO) mod download
	cd web && npm ci

frontend:
	cd web && npm run build
	mkdir -p internal/webassets/dist
	find internal/webassets/dist -mindepth 1 ! -name .gitkeep -delete
	cp -a web/dist/. internal/webassets/dist/

build: frontend license-check
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -mod=readonly -trimpath -o bin/herdr-space ./cmd/herdr-space
	install -m 644 LICENSE NOTICE.md bin/
	install -m 644 third_party/THIRD_PARTY_LICENSES.md bin/

test:
	$(GO) test -mod=readonly -race ./...
	cd web && npm test

verify: test
	$(GO) vet -mod=readonly ./...
	$(MAKE) build

dev: build
	./bin/herdr-space serve --origin http://127.0.0.1:9380 --allow-insecure-local --data-dir .state/dev

clean:
	rm -rf bin web/dist
	find internal/webassets/dist -mindepth 1 ! -name .gitkeep -delete

browser-deps:
	npm install --prefix .tools/browser --no-audit --no-fund --save-exact @playwright/test@1.63.0

license-bundle:
	python3 scripts/generate-license-bundle.py --write

license-check:
	python3 scripts/generate-license-bundle.py --check
