# Imagen del plano de control de Rampa: la API más la SPA compilada.
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

# Sin shell, sin root, sin paquetes: el binario, los certificados de CA y la web.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/rampa-api /rampa-api
COPY --from=web /src/web/dist /web
ENV RAMPA_LISTEN=:8080 RAMPA_WEB_DIR=/web
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/rampa-api"]
CMD ["serve"]
