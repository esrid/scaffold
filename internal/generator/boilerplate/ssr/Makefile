.PHONY: all build run clean proto generate

all: build

generate:
	templ generate

build: generate
	@mkdir -p bin
	go build -o bin/server main.go

run: generate
	go run main.go

clean:
	rm -rf bin

proto:
	buf generate --path internal/adapters/grpc/pb
