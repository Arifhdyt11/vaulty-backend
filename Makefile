.PHONY: tunnel dev-deps dev-deps-down migrate migrate-status api worker build test vet fmt verify openapi

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

tunnel:            ## SSH tunnel ke Hermes (:8642) dan 9router (:20128) di sipantas; menyambung ulang otomatis
	@while true; do \
	  echo "tunnel sipantas: menyambung..."; \
	  ssh -N -o ExitOnForwardFailure=yes -o ServerAliveInterval=20 -o ServerAliveCountMax=3 \
	    -L 8642:127.0.0.1:8642 -L 20128:127.0.0.1:20128 sipantas; \
	  echo "tunnel putus, coba lagi dalam 3 detik"; sleep 3; \
	done
