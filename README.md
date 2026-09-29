# Rampa

Plataforma interna de desarrollo como producto. Un dev describe su aplicación en un `rampa.yaml`, la importa en Rampa, y el plano de control se encarga de renderizar, desplegar y crear entornos en el cluster del cliente. La infraestructura debajo (EKS, GKE, AKS, registries, secret managers) es un detalle que el dev no toca.

```
rampa.yaml  ->  API (rampa-api)  ->  agente (rampa-agent, en el cluster)  ->  Kubernetes
                   ^        ^
        web (React)   CLI y CI (rampa deploy)
```

La API guarda el estado en Postgres, renderiza el chart `rampa-app` con los values del scope y le manda al agente los objetos ya listos por WebSocket. El agente los aplica con server-side apply, observa el rollout y reporta. La web y el CLI ven todo en vivo por SSE.

## Qué hay en este repo

| Carpeta | Qué es |
|---|---|
| `docs/` | El contrato: [`docs/api.md`](docs/api.md) (API v1, la referencia que implementan API, web y CLI), [`docs/modelo.md`](docs/modelo.md) (el `rampa.yaml`), [`docs/arquitectura-producto.md`](docs/arquitectura-producto.md), [`docs/cli.md`](docs/cli.md), [`docs/instalar-agente.md`](docs/instalar-agente.md), convenciones, ADRs y el plan. |
| `cmd/rampa-api/` | El plano de control: API REST, SSE, hub de agentes, sirve la SPA. `serve --dev` levanta un Postgres embebido. Tiene `Dockerfile`. |
| `cmd/rampa-agent/` | El agente que corre en el cluster del cliente y aplica lo que la API decide. Tiene `Dockerfile` (distroless, nonroot). |
| `cmd/rampa/` | El CLI `rampa`: `init`, `validate`, `render`, `login`, `apps import`, `deploy`, `undeploy`, `scope`. Los golden paths viven en `cmd/rampa/templates/`. |
| `internal/api/` | Implementación de la API: `config`, `store` (pgx + migraciones), `auth`, `httpapi`, `deploy` (pipeline de render), `hub` (WebSocket), `events`, `server`. |
| `internal/agent/` | Implementación del agente: `client` (WebSocket y reconexión), `kube` (SSA, prune, salud, undeploy, logs), `executor`, `health`. |
| `internal/cli/` | Implementación del CLI y su cliente HTTP (`apiclient`). |
| `pkg/` | Paquetes compartidos: `manifest` (rampa.yaml), `render` (values del chart), `protocol` (mensajes API-agente), `provider` (AWS, GCP, Azure), `scaffold`. |
| `charts/rampa-app/` | El Helm chart único con el que se despliega toda aplicación. La API lo embebe (`charts/embed.go`). |
| `charts/rampa-agent/` | El chart que instala el agente en el cluster del cliente. La web genera el `helm install` al crear un cluster. |
| `web/` | El panel web: React 19, TypeScript, Vite, Tailwind, TanStack Query. |
| `terraform/modules/<provider>/` | Módulos OpenTofu de services (Postgres, etc.), por nube. |
| `.github/workflows/` | `ci.yml` es la CI de este repo. `reusable-build.yml`, `reusable-deploy.yml` y `reusable-undeploy.yml` son los workflows que las apps llaman. |
| `examples/` | `checkout-api`, una app de ejemplo con su `rampa.yaml`. |

## Correr Rampa local en 5 minutos

Requisitos: go 1.27, node 22 y npm, helm 3.12 o superior (en esta máquina `export PATH="$HOME/.local/bin:$PATH"`). No hace falta Postgres ni un cluster.

1. **La API**, en una terminal:

   ```bash
   make dev-api
   ```

   `serve --dev` descarga la primera vez un Postgres embebido (~30 MB, cacheado en `~/.embedded-postgres-go`), lo arranca en el puerto 54329 con datos en `~/.local/share/rampa/pg`, corre las migraciones, habilita `POST /v1/auth/dev-login` y escucha en `http://localhost:8080`. Probá `curl localhost:8080/readyz`.

2. **La web**, en otra terminal:

   ```bash
   make dev-web        # la primera vez: cd web && npm ci
   ```

   Vite sirve en `http://localhost:5173` y proxea `/v1` y `/public` a la API.

3. **Entrá** en http://localhost:5173. En la pantalla de login escribí cualquier email (dev-login crea el usuario) y creá una organización con su slug.

4. **Qué se puede hacer sin cluster:**
   - Importar una app pegando `examples/checkout-api/rampa.yaml`; la API la valida con `pkg/manifest` y muestra los scopes declarados.
   - Crear una cuenta de nube AWS: la API genera el `external_id` y las instrucciones con el link de CloudFormation (la plantilla se sirve en `/public/cloudformation/rampa-connect.yaml`). La verificación real necesita `RAMPA_AWS_TRUSTED_PRINCIPAL_ARN` y una cuenta de AWS; sin eso queda en `pending` o `failed`.
   - Crear un cluster: recibís el token y el `helm install` listo para pegar. Queda `offline` hasta que un agente se conecte.
   - Pedir un deployment: la API renderiza el chart, lo deja `queued` y encola el comando en `agent_commands`. Con `require_approval_prod`, prod queda en `awaiting_approval` y podés aprobarlo o cancelarlo.
   - Ver todo en vivo en la actividad (SSE), administrar miembros y crear tokens de API para el CLI (`rampa login --api http://localhost:8080 --token rampa_...`).

   Para ver un deploy llegar a `healthy` hace falta un cluster con el agente instalado (`docs/instalar-agente.md`); para probarlo local sirve un kind con `RAMPA_KUBECONFIG` apuntando al kubeconfig y `RAMPA_API_URL=http://localhost:8080`.

## Otros targets

```bash
make build            # los tres binarios a ./bin
make test             # go test ./... (RAMPA_TEST_SKIP_DB=1 salta la integración con Postgres)
make lint             # gofmt, go vet, helm lint de ambos charts, typecheck de la web
make render           # renderiza la app de ejemplo en todos los scopes
make docker           # imágenes rampa-api (con la web adentro) y rampa-agent
make help             # lista completa
```

## Estado

Fase A del producto: API, agente, web, CLI contra la API, provider AWS y chart del agente. GCP y Azure se listan como "próximamente". Ver [`docs/arquitectura-producto.md`](docs/arquitectura-producto.md) y [`docs/adr/0002-producto-saas.md`](docs/adr/0002-producto-saas.md).
