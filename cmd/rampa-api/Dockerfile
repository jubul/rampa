# Imagen del plano de control de Rampa: la API, la SPA compilada y el binario de
# OpenTofu para los runs de infraestructura.
# Se construye desde la raíz del repo:
#   docker build -f cmd/rampa-api/Dockerfile -t ghcr.io/CHANGEME/rampa-api:dev --build-arg VERSION=dev .
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27 AS build
WORKDIR /src
ARG VERSION=dev
ENV CGO_ENABLED=0 GOFLAGS=-trimpath
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/rampa-api ./cmd/rampa-api

# OpenTofu: release oficial de GitHub para la arquitectura de la imagen,
# versión pinneada y checksum verificado contra el SHA256SUMS del release.
# Es un binario estático, así que va tal cual a la imagen distroless.
FROM debian:bookworm-slim AS tofu
ARG TOFU_VERSION=1.12.6
ARG TARGETARCH
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl unzip && rm -rf /var/lib/apt/lists/*
RUN set -eux; \
    arch="${TARGETARCH:-amd64}"; \
    base="https://github.com/opentofu/opentofu/releases/download/v${TOFU_VERSION}"; \
    file="tofu_${TOFU_VERSION}_linux_${arch}.zip"; \
    curl -fsSL -o "/tmp/${file}" "${base}/${file}"; \
    curl -fsSL -o /tmp/SHA256SUMS "${base}/tofu_${TOFU_VERSION}_SHA256SUMS"; \
    cd /tmp && grep " ${file}\$" SHA256SUMS | sha256sum -c -; \
    unzip -o "/tmp/${file}" tofu -d /out; \
    chmod 0755 /out/tofu

# Sin shell, sin root, sin paquetes: el binario, los certificados de CA, la web y tofu.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/rampa-api /rampa-api
COPY --from=web /src/web/dist /web
COPY --from=tofu /out/tofu /usr/local/bin/tofu
# RAMPA_TOFU_BIN default "tofu" lo encuentra en el PATH de la imagen (/usr/local/bin).
# Los runs escriben en RAMPA_INFRA_WORKDIR (default /tmp) y cachean providers ahí.
ENV RAMPA_LISTEN=:8080 RAMPA_WEB_DIR=/web RAMPA_INFRA_WORKDIR=/tmp
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/rampa-api"]
CMD ["serve"]
