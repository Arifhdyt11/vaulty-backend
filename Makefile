.PHONY: dev-deps migrate api worker bot sqlc test build tunnel

dev-deps: ## Jalankan Postgres (pgvector) untuk development
	docker compose -f docker-compose.dev.yml up -d

tunnel: ## SSH tunnel ke Hermes (:8642, chat) dan 9router (:20128, embedding) di server sipantas
	ssh -N -L 8642:127.0.0.1:8642 -L 20128:127.0.0.1:20128 sipantas

migrate: ## Jalankan migration (up)
	go run ./cmd/migrate up

api:
	go run ./cmd/api

worker:
	go run ./cmd/worker

bot: ## Bot Telegram (butuh TELEGRAM_BOT_TOKEN)
	go run ./cmd/bot

sqlc: ## Generate ulang kode query dari db/queries
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@latest generate

test:
	go test ./...

build:
	go build -o bin/ ./cmd/...
