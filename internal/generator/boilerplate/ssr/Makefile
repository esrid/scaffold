.PHONY: all build run clean proto

all: build

build:
	@mkdir -p bin
	go build -o bin/server main.go

run:
	go run main.go

clean:
	rm -rf bin

proto:
	buf generate --path internal/adapters/grpc/pb
