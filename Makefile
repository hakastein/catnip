.PHONY: go build dist

TARGETS = linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

go:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	CGO_ENABLED=0 go vet ./...
	CGO_ENABLED=0 go test $(TESTFLAGS) ./...

build:
	CGO_ENABLED=0 go build -o bin/ .

# dist builds the release binaries dist/catnip-<os>-<arch>[.exe] and dist/sha256sums.txt.
dist:
	rm -rf dist && mkdir dist
	for t in $(TARGETS); do \
	  os=$${t%/*}; arch=$${t#*/}; ext=; [ $$os = windows ] && ext=.exe; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags=-s -o dist/catnip-$$os-$$arch$$ext . || exit 1; \
	done
	cd dist && sha256sum catnip-* > sha256sums.txt
