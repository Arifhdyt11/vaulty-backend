.PHONY: dev-deps dev-deps-down migrate migrate-status api worker build test vet fmt verify openapi

dev-deps:          ## Postgres + Redis + MinIO (docker compose)
	docker compose up -d

dev-deps-down:
	docker compose down

migrate:
	go run ./cmd/vaulty migrate

migrate-status:
	go run ./cmd/vaulty migrate status

api:               ## Swagger: http://localhost:8080/api/docs
	go run ./cmd/vaulty api

worker:
	go run ./cmd/vaulty worker

build:
	mkdir -p bin && go build -o bin/vaulty ./cmd/vaulty

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

verify: fmt vet test

API_URL ?= http://localhost:8080

openapi:           ## simpan openapi.json (api harus jalan; API_URL=http://localhost:8081 make openapi)
	curl -sf $(API_URL)/api/v1/openapi.json -o openapi.json && echo "openapi.json diperbarui"
