.PHONY: build test test-sandbox install clean fmt vet

BIN := ./bin/bunny

build:
	mkdir -p ./bin
	go build -o $(BIN) ./cmd/bunny

test:
	go test ./...

# Requires bwrap >= 0.11, pasta, nft, xdg-dbus-proxy, and user namespaces.
test-sandbox:
	BUNNY_REQUIRE_SANDBOX_TESTS=1 dbus-run-session -- go test -race -count=1 -tags sandbox_integration ./internal/runtime

# Mirrors install.sh and paths.Resolve: the binary lives in $BUNNY_HOME/bin,
# ~/.bunny/bin by default.
install: build
	@dir=$${BUNNY_HOME:+$$BUNNY_HOME/bin}; dir=$${dir:-$$HOME/.bunny/bin}; \
	mkdir -p "$$dir" && cp $(BIN) "$$dir/bunny" && echo "installed $$dir/bunny"

fmt:
	gofmt -w .

vet:
	go vet ./...

clean:
	rm -rf ./bin
