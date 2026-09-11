BINARY_AGENT  := fleetpulse-agent
BINARY_SERVER := fleetpulse-server
VERSION       ?= dev
LDFLAGS       := -s -w -X main.version=$(VERSION)
BUF_VERSION   := v1.50.0

.PHONY: help tools proto build build-server build-linux build-windows test lint tidy \
        run run-server web-install web-build web-dev compose-up compose-down clean

help: ## Lista los objetivos disponibles
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

tools: ## Instala buf y los plugins de generacion en $(GOBIN)
	go install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

proto: ## Genera el codigo Go a partir de proto/
	buf lint
	buf generate

build: ## Compila el agente para el sistema actual
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY_AGENT) ./cmd/fleetpulse-agent

build-server: ## Compila el servidor para el sistema actual
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY_SERVER) ./cmd/fleetpulse-server

# El agente es un binario estatico sin cgo para que corra igual en Debian,
# Alpine o una Raspberry Pi sin arrastrar dependencias de glibc.
build-linux: ## Binarios de release del agente para linux amd64 y arm64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY_AGENT)-linux-amd64 ./cmd/fleetpulse-agent
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY_AGENT)-linux-arm64 ./cmd/fleetpulse-agent

build-windows: ## Binario de release del agente para windows amd64
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY_AGENT)-windows-amd64.exe ./cmd/fleetpulse-agent

test: ## Ejecuta los tests de Go (con -race donde hay cgo disponible)
	go test ./... -race -count=1

lint: ## Analisis estatico basico
	go vet ./...
	gofmt -l .

tidy: ## Sincroniza go.mod y go.sum
	go mod tidy

run: ## Agente: toma una sola muestra y la imprime
	go run ./cmd/fleetpulse-agent --once --log-level debug

run-server: ## Servidor: arranca en modo memoria (sin Postgres/Redis), para probar el panel
	go run ./cmd/fleetpulse-server --storage=memory --tokens=dev-token --log-level debug

web-install: ## Instala las dependencias del dashboard
	cd web && npm install

web-build: ## Compila el dashboard para produccion
	cd web && npm run build

web-dev: ## Arranca el dashboard en modo desarrollo
	cd web && npm run dev

compose-up: ## Levanta el stack completo de demostracion (TimescaleDB, Redis, servidor, dashboard, agente)
	docker compose up --build

compose-down: ## Para y elimina el stack de demostracion
	docker compose down -v

clean: ## Borra los artefactos de compilacion
	rm -rf bin dist
