.PHONY: build test install clean fmt vet

BIN := ./bin/bunny

build:
	mkdir -p ./bin
	go build -o $(BIN) ./cmd/bunny

test:
	go test ./...

install: build
	mkdir -p $${BUNNY_HOME:-$$HOME/.bunny}/bin
	cp $(BIN) $${BUNNY_HOME:-$$HOME/.bunny}/bin/bunny

fmt:
	gofmt -w .

vet:
	go vet ./...

clean:
	rm -rf ./bin
