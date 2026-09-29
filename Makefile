# Makefile de Rampa. Requiere go 1.27, helm 3.12+ (o 4), node 22 y npm en el PATH.
# En esta máquina: export PATH="$HOME/.local/bin:$PATH".

BIN       ?= $(HOME)/.local/bin
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS    = -ldflags "-X main.version=$(VERSION)"
CHART_APP  = charts/rampa-app
CHART_AGENT= charts/rampa-agent
EXAMPLE    = examples/checkout-api/rampa.yaml
IMAGE_REPO ?= ghcr.io/CHANGEME
SCOPES     = dev staging prod pr-1

.PHONY: lint-infra help build api agent cli web test test-integration lint dev-api dev-web render smoke install docker clean

help:             ## Lista los targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-18s %s\n", $$1, $$2}'

build: api agent cli ## Compila los tres binarios a ./bin

api:              ## Compila el plano de control a ./bin/rampa-api
	go build $(LDFLAGS) -o bin/rampa-api ./cmd/rampa-api

agent:            ## Compila el agente a ./bin/rampa-agent
	CGO_ENABLED=0 go build $(LDFLAGS) -o bin/rampa-agent ./cmd/rampa-agent

cli:              ## Compila el CLI a ./bin/rampa
	go build $(LDFLAGS) -o bin/rampa ./cmd/rampa

web:              ## Compila la SPA a web/dist (npm ci + build)
	cd web && npm ci && npm run build

test:             ## go test ./... (RAMPA_TEST_SKIP_DB=1 salta los tests con Postgres embebido)
	go test ./...

test-integration: ## Solo los tests de integración de la API (bajan un Postgres embebido la primera vez)
	go test -count=1 -race -timeout 600s ./internal/api/server/

lint: cli lint-infra ## gofmt, go vet, helm lint de ambos charts, typecheck de la web e infra
	test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt: archivos sin formatear"; exit 1; }
	go vet ./...
	@for scope in $(SCOPES); do \
	  ./bin/rampa render -f $(EXAMPLE) --scope $$scope --image sha-0000000 > /tmp/rampa-lint-$$scope.yaml 2>/dev/null; \
	  helm lint --strict $(CHART_APP) -f /tmp/rampa-lint-$$scope.yaml || exit 1; \
	done
	@for f in $(CHART_AGENT)/ci/*.yaml; do helm lint --strict $(CHART_AGENT) -f $$f || exit 1; done
	cd web && npm run typecheck

lint-infra:       ## tofu fmt y validate de los módulos, cfn-lint de las plantillas, actionlint de los workflows (si están instalados)
	@for m in terraform/modules/*/*/; do \
	  echo "== $$m"; \
	  tofu -chdir=$$m fmt -check -diff || exit 1; \
	  tofu -chdir=$$m init -backend=false -input=false >/dev/null || exit 1; \
	  tofu -chdir=$$m validate || exit 1; \
	  rm -rf $$m/.terraform $$m/.terraform.lock.hcl; \
	done
	@if command -v cfn-lint >/dev/null; then cfn-lint pkg/provider/aws/cloudformation/*.yaml && echo "cfn-lint ok"; else echo "cfn-lint no instalado, salto"; fi
	@if command -v actionlint >/dev/null; then actionlint -no-color -shellcheck= .github/workflows/*.yml && echo "actionlint ok"; else echo "actionlint no instalado, salto"; fi

dev-api:          ## Corre la API en modo dev (Postgres embebido, dev-login, :8080)
	go run ./cmd/rampa-api serve --dev

dev-web:          ## Corre el dev server de la web en :5173 (proxy /v1 a :8080)
	cd web && npm run dev

render: cli       ## Renderiza todos los scopes de la app de ejemplo con el chart rampa-app
	@for scope in $(SCOPES); do \
	  ./bin/rampa render -f $(EXAMPLE) --scope $$scope --image sha-0000000 > /tmp/rampa-values-$$scope.yaml 2>/dev/null; \
	  helm template checkout-api $(CHART_APP) -f /tmp/rampa-values-$$scope.yaml > /tmp/rampa-manifests-$$scope.yaml; \
	  echo "$$scope: $$(grep -c '^kind:' /tmp/rampa-manifests-$$scope.yaml) recursos -> /tmp/rampa-manifests-$$scope.yaml"; \
	done

smoke: cli        ## Genera una app desde el golden path y la compila
	rm -rf /tmp/rampa-smoke && mkdir -p /tmp/rampa-smoke
	./bin/rampa init --name smoke-api --team plataforma --github-org acme --dir /tmp/rampa-smoke
	cd /tmp/rampa-smoke/smoke-api && $(CURDIR)/bin/rampa validate && go vet ./... && go test ./...

install:          ## Instala el CLI en $(BIN)
	go build $(LDFLAGS) -o $(BIN)/rampa ./cmd/rampa

docker:           ## Construye las imágenes de la API (con la web adentro) y del agente
	docker build -f cmd/rampa-api/Dockerfile   --build-arg VERSION=$(VERSION) -t $(IMAGE_REPO)/rampa-api:$(VERSION) .
	docker build -f cmd/rampa-agent/Dockerfile --build-arg VERSION=$(VERSION) -t $(IMAGE_REPO)/rampa-agent:$(VERSION) .

clean:            ## Borra binarios y la build de la web
	rm -rf bin web/dist
