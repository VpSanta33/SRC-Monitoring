.PHONY: setup collect export build run test
setup:
	go mod download
	npm --prefix web ci
collect:
	go run ./cmd/collect
export:
	go run ./cmd/collect -offline
build:
	npm --prefix web run build
run:
	npm --prefix web run dev
test:
	node scripts/check-config.mjs
	go test -race ./...
	npm --prefix web test
	npm --prefix web run build
