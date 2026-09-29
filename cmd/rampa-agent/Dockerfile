# Imagen del agente de Rampa. Se construye desde la raíz del repo:
#   docker build -f cmd/rampa-agent/Dockerfile -t ghcr.io/CHANGEME/rampa-agent:dev --build-arg VERSION=dev .
FROM golang:1.27 AS build
WORKDIR /src
ARG VERSION=dev
ENV CGO_ENABLED=0 GOFLAGS=-trimpath
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/rampa-agent ./cmd/rampa-agent

# Sin shell, sin root, sin paquetes: solo el binario y los certificados de CA.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/rampa-agent /rampa-agent
USER nonroot:nonroot
EXPOSE 8081
ENTRYPOINT ["/rampa-agent"]
