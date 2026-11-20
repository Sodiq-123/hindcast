.PHONY: dev backend frontend build run test

# Backend API on :8080 and Vite dev server on :5173 (open http://localhost:5173)
dev:
	@echo "Starting backend (:8080) and frontend (:5173)…"
	@(cd backend && go run ./cmd/server) & (cd frontend && npm run dev)

backend:
	cd backend && go run ./cmd/server

frontend:
	cd frontend && npm run dev

# Single binary that also serves the built UI: http://localhost:8080
build:
	cd frontend && npm install && npm run build
	cd backend && go build -o bin/market-oracle ./cmd/server

run: build
	cd backend && ./bin/market-oracle

test:
	cd backend && go vet ./... && go test -p 1 ./...
	cd frontend && npx tsc -b

# Quick terminal analysis, e.g. make analyze SYM=NVDA
analyze:
	cd backend && go run ./cmd/server -analyze $(SYM)
