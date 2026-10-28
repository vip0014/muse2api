.PHONY: dev build test race lint init-data backup docker

dev:
	go run ./cmd/muse2api

web-dev:
	cd web && npm run dev

build:
	cd web && npm ci && npm run build
	CGO_ENABLED=0 go build -trimpath -o bin/muse2api ./cmd/muse2api

test:
	go test ./internal/... ./cmd/...
	cd web && npm run typecheck && npm run lint && npm run test

race:
	go test -race ./internal/... ./cmd/...

lint:
	go vet ./internal/... ./cmd/...
	cd web && npm run lint

init-data:
	sh deploy/init-data.sh

backup:
	sh deploy/backup.sh

docker:
	docker compose up -d --build
