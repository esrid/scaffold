.PHONY: all build build-fe run clean

all: build

build: build-fe
	@mkdir -p bin
	go build -o bin/server main.go

build-fe:
	@mkdir -p web/dist
	npx -y esbuild web/src/index.ts --bundle --minify --sourcemap --outfile=web/dist/index.js
	npx -y esbuild web/src/style.css --minify --outfile=web/dist/style.css
	@cp web/src/index.html web/dist/index.html
	@cp web/src/robots.txt web/dist/robots.txt
	@cp web/src/sitemap.xml web/dist/sitemap.xml

run: build-fe
	go run main.go

clean:
	rm -rf web/dist bin
