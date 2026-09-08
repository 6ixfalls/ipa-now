.PHONY: setup dev dev-web fmt fmt-check lint test test-go test-web e2e frontend build check migrate test-device

setup:
	go mod download
	pnpm --dir web install --frozen-lockfile

dev:
	go run ./cmd/ipa-now

dev-web:
	pnpm --dir web run dev

fmt:
	gofmt -w cmd internal
	pnpm --dir web run format

fmt-check:
	@test -z "$$(gofmt -l cmd internal)" || (gofmt -l cmd internal; exit 1)
	pnpm --dir web run format:check

lint: fmt-check
	go vet ./...
	pnpm --dir web run lint
	pnpm --dir web run typecheck

test: test-go test-web

test-go:
	go test -race ./...

test-web:
	pnpm --dir web run test

e2e:
	go test -race ./internal/httpapi -run TestEndToEnd -count=1

frontend:
	pnpm --dir web run build

build: frontend
	mkdir -p bin
	go build -trimpath -o bin/ipa-now ./cmd/ipa-now

check: lint test e2e build

migrate:
	@echo 'Schema v2 is migrated transactionally on startup under the data-directory lock. Back up data while the service is stopped, then run make dev.'

test-device:
	go test -tags integration ./internal/worker -run '^TestRealDevice$$' -count=1 -v
