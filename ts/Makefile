.PHONY: all build test clean build-ts build-go test-ts test-go clean-ts clean-go embed publish-go tags-go tidy-go reset

all: build test

build: build-ts build-go

test: test-ts test-go

clean: clean-ts clean-go

# Embed yaml-grammar.jsonic into src/yaml.ts and ../go/yaml.go.
embed:
	node embed-grammar.js

# TypeScript
build-ts:
	npm run build

test-ts:
	npm test

clean-ts:
	rm -rf dist dist-test

# Go
build-go: embed
	cd ../go && go build ./...

test-go:
	cd ../go && go test ./...

clean-go:
	cd ../go && go clean -cache

# Publish Go module: make publish-go V=0.3.1
publish-go: test-go
	@test -n "$(V)" || (echo "Usage: make publish-go V=x.y.z" && exit 1)
	git tag go/v$(V)
	git push origin main go/v$(V)
	if command -v gh >/dev/null 2>&1; then gh release create go/v$(V) --title "go/v$(V)" --notes "Go module release v$(V)"; fi

tidy-go:
	cd ../go && go mod tidy

tags-go:
	git tag -l 'go/v*' --sort=-version:refname

reset:
	npm run reset
	cd ../go && go clean -cache
	cd ../go && go build ./...
	cd ../go && go test -v ./...
