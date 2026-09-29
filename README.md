# Rampa

**La plataforma interna de desarrollo que tu equipo no tiene que construir.** Conectás tu nube, creás un cluster con un click, describís tu aplicación en un formulario y desplegás. La infraestructura queda en tu cuenta. El agente que la opera es abierto. Nada sensible pasa por nosotros.

Rampa toma el modelo que Mercado Libre construyó para sí misma con Fury, cinco conceptos y nada más, y lo convierte en un producto para empresas de 10 a 300 developers que corren en Kubernetes en AWS, GCP o Azure y no tienen un equipo de plataforma.

```
                      web (React)          CLI y CI (rampa deploy)
                            │                      │
                            ▼                      ▼
   formularios  ──►   API rampa-api   ◄──  rampa.yaml en el repo
   sin YAML           │ Postgres │
                      │ runner   │ ── tofu apply ──►  nube del cliente: VPC, EKS, IRSA, agente
                      │ OpenTofu │
                      └────┬─────┘
                           │ WebSocket saliente (el agente llama a la API)
                           ▼
              agente rampa-agent en el cluster del cliente
                           │ server-side apply
                           ▼
              namespaces <app>-<scope>: dev, staging, prod, pr-<n>
```

## Qué hace

- **Infraestructura con clicks.** Conectás una cuenta de AWS con un stack de CloudFormation que crea un rol asumible, sin access keys. Desde ahí, Rampa crea por vos un cluster EKS con Auto Mode, red, IRSA y el agente ya instalado, ejecutando OpenTofu con tu identidad. El mismo mecanismo va a servir para GKE, AKS y las demás nubes: un módulo por proveedor.
- **Aplicaciones sin YAML.** Un asistente de cinco pasos arma la aplicación: básicos, scopes, parameters, services, revisión. El resultado es un `rampa.yaml` que podés descargar dentro de un proyecto inicial listo para hacer push. Si preferís escribirlo a mano, es el mismo archivo.
- **Deploys que se ven.** Cada deploy es una fila con quién, qué imagen y cómo terminó, con el rollout en vivo. Producción puede exigir aprobación. Cada pull request tiene su entorno efímero con URL, y se destruye al cerrarlo.
- **Seguridad por diseño.** Credenciales de nube por federación, secretos de aplicación en el gestor de secretos de tu nube, agente con conexión saliente y RBAC documentado permiso por permiso, estado de OpenTofu cifrado, aislamiento por organización en cada tabla y cada query.

## Empezar

- **Como usuario**, con clicks y en menos de una hora: [`docs/empezar.md`](docs/empezar.md).
- **Como developer** que prefiere la terminal: [`docs/cli.md`](docs/cli.md) y el `rampa.yaml` en [`docs/modelo.md`](docs/modelo.md).
- **Como quien instala el agente** en un cluster existente: [`docs/instalar-agente.md`](docs/instalar-agente.md).

## Correr Rampa en tu máquina

Requisitos: Go 1.27, Node 22 con npm, Helm 3.12 o superior, y OpenTofu 1.12 si querés probar el runner de infraestructura. No hace falta Postgres ni un cluster para la mayoría de las cosas.

```bash
make dev-api     # API en :8080 con Postgres embebido y dev-login habilitado
make dev-web     # web en :5173, proxea /v1 a la API
```

Entrá en http://localhost:5173, escribí cualquier email, creá una organización y seguí el onboarding. Sin cuenta de AWS ni cluster podés recorrer todo el producto: conectar una nube queda en `pending`, un cluster registrado queda `offline`, y un deploy queda `queued` esperando a un agente. Para ver un deploy llegar a `healthy` hace falta un cluster con el agente: un kind local sirve, con `RAMPA_KUBECONFIG` y `RAMPA_API_URL=http://localhost:8080`.

Para que el runner de infraestructura funcione de verdad hacen falta `tofu` en el PATH, `RAMPA_STATE_KEY` (32 bytes en base64) y `RAMPA_AWS_TRUSTED_PRINCIPAL_ARN` con la identidad IAM con la que corre la API. Las variables están todas en [`docs/api.md`](docs/api.md).

```bash
make build            # los tres binarios a ./bin
make test             # go test ./... (RAMPA_TEST_SKIP_DB=1 salta la integración con Postgres)
make lint             # gofmt, go vet, helm lint, typecheck de la web, tofu validate, cfn-lint, actionlint
make docker           # imágenes rampa-api (con la web y tofu adentro) y rampa-agent
make help             # todos los targets
```

## Qué hay en este repo

| Carpeta | Qué es |
|---|---|
| `cmd/rampa-api/` | El plano de control: API REST, SSE, hub de agentes, runner de OpenTofu, sirve la web. `serve --dev` levanta un Postgres embebido. |
| `cmd/rampa-agent/` | El agente que corre en el cluster del cliente. Conexión saliente, server-side apply, prune, rollout, logs. Distroless, nonroot. |
| `cmd/rampa/` | El CLI: `init`, `validate`, `render`, `login`, `apps import`, `deploy`, `undeploy`, `scope`. Los golden paths viven en `cmd/rampa/templates/`. |
| `internal/api/` | Implementación de la API: `config`, `store` (pgx y migraciones), `auth`, `httpapi`, `deploy` (render del chart), `hub` (WebSocket), `infra` (runner y backend de estado), `events`, `server`. |
| `internal/agent/` | Implementación del agente: `client`, `kube`, `executor`, `health`. |
| `internal/cli/` | Implementación del CLI y su cliente HTTP. |
| `pkg/` | Compartidos: `manifest` (el `rampa.yaml` y su spec estructurado), `render`, `protocol` (API-agente), `provider` (AWS, GCP, Azure), `scaffold`. |
| `charts/rampa-app/` | El chart único con el que se despliega toda aplicación. Embebido en la API. |
| `charts/rampa-agent/` | El chart que instala el agente. Embebido en la API para que el runner lo instale al crear clusters. |
| `terraform/modules/<nube>/` | Módulos OpenTofu: `aws/eks-cluster` (cluster listo con agente), `aws/postgres` (primer service). Embebidos en la API. |
| `web/` | El panel: React 19, TypeScript, Vite, Tailwind, TanStack Query. Onboarding, asistente de apps, infraestructura, deploys en vivo. |
| `docs/` | Toda la documentación. Índice abajo. |
| `.github/workflows/` | La CI de este repo y los workflows reusables que las apps de los clientes llaman para construir y desplegar. |
| `examples/` | `checkout-api`, la app de referencia. |

## Documentación

| Documento | Para qué |
|---|---|
| [`docs/empezar.md`](docs/empezar.md) | Guía de una hora para el usuario final, con clicks. |
| [`docs/modelo.md`](docs/modelo.md) | El contrato con el developer: application, scope, deployment, parameters, services, y el `rampa.yaml`. |
| [`docs/api.md`](docs/api.md) | La API v1 completa, la referencia que implementan API, web y CLI. Incluye variables de configuración. |
| [`docs/arquitectura-producto.md`](docs/arquitectura-producto.md) | Cómo está hecho: plano de control, agente, runner, providers, modelo de datos, flujos, roadmap. |
| [`docs/adr/`](docs/adr/) | Las decisiones y por qué: fundaciones, producto SaaS, infraestructura con clicks vía OpenTofu. |
| [`docs/instalar-agente.md`](docs/instalar-agente.md) | Instalar el agente en un cluster existente y darle identidad de nube. Permisos justificados uno a uno. |
| [`docs/cli.md`](docs/cli.md) | Referencia del CLI y uso desde CI. |
| [`docs/convenciones.md`](docs/convenciones.md) | Nombres, labels, rutas de secretos. |
| [`docs/modelo-de-negocio.md`](docs/modelo-de-negocio.md) | A quién le vendemos, cuánto cobramos y por qué, competencia, riesgos, métricas. |
| [`docs/plan.md`](docs/plan.md) | El plan original de la plataforma interna, del que nació el producto. |

## Estado

Fase A del producto, ampliada con infraestructura con clicks: API, agente, web con onboarding y asistente sin YAML, runner de OpenTofu con el módulo `aws/eks-cluster`, CLI contra la API, provider AWS completo, charts de app y agente. GCP y Azure implementan la interfaz y figuran como próximamente. Todo verificado con tests, lint estricto y CI; el agente y el runner todavía no se ejercitaron contra un cluster y una cuenta de AWS reales. Ver el roadmap en [`docs/arquitectura-producto.md`](docs/arquitectura-producto.md).

## Licencia

Por definir antes del lanzamiento público. La intención: agente, charts y módulos abiertos; plano de control con licencia a decidir (ver `docs/modelo-de-negocio.md`).
