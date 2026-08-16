.PHONY: build test install clean fmt vet

BIN := ./bin/bunny

build:
	mkdir -p ./bin
	go build -o $(BIN) ./cmd/bunny

test:
	go test ./...

# Mirrors install.sh and paths.Resolve: BUNNY_HOME collapses everything under
# one root, otherwise shims and the binary live in ~/.local/bin.
install: build
	@dir=$${BUNNY_HOME:+$$BUNNY_HOME/bin}; dir=$${dir:-$$HOME/.local/bin}; \
	mkdir -p "$$dir" && cp $(BIN) "$$dir/bunny" && echo "installed $$dir/bunny"

fmt:
	gofmt -w .

vet:
	go vet ./...

clean:
	rm -rf ./bin
