.PHONY: build run run-tui once build-arm clean test

BIN = bin/grafana-light
ARM_BIN = bin/grafana-light-arm7

build:
	go build -o $(BIN) ./cmd/server

run: build
	./$(BIN) -config configs/example.yaml

run-tui: build
	./$(BIN) -mode tui -config configs/example.yaml

once: build
	./$(BIN) -mode once -config configs/example.yaml

# Raspberry Pi 3B (ARMv7) 向けクロスコンパイル
build-arm:
	GOOS=linux GOARCH=arm GOARM=7 go build -ldflags="-s -w" -o $(ARM_BIN) ./cmd/server

test:
	go test ./...

clean:
	rm -rf bin/
