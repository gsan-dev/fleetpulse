# Imagen del agente para correr contenerizado (uso principal: el stack de
# demostracion en docker-compose.yml). En un despliegue real el agente se
# instala como binario nativo con install/install.sh o install/install.ps1;
# esta imagen es la alternativa para quien prefiera desplegarlo como
# contenedor mas (montando /var/run/docker.sock de solo lectura).
#
#   docker build -f cmd/fleetpulse-agent/Dockerfile -t fleetpulse-agent .

FROM golang:1.26-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/fleetpulse-agent ./cmd/fleetpulse-agent

# No distroless aqui: el agente necesita resolver DNS y (opcionalmente)
# hablar con el socket de Docker montado por el host; alpine da un base
# minimo sin renunciar a esas utilidades basicas.
FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=build /out/fleetpulse-agent /usr/local/bin/fleetpulse-agent

ENTRYPOINT ["/usr/local/bin/fleetpulse-agent"]
