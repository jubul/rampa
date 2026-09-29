# Rampa como producto: arquitectura

Rampa deja de ser solo la plataforma interna de un equipo y pasa a ser un producto: un plano de control SaaS que gobierna aplicaciones desplegadas en la nube de cada cliente (AWS, GCP, Azure), con una web, un CLI y una API. Nuestro propio grupo de developers es el **cliente cero**: todo se prueba primero con nosotros.

Este documento fija la arquitectura. Las decisiones puntuales están en `adr/0002-producto-saas.md`. El modelo del developer (`rampa.yaml`, application, scope, deployment, parameters, services) no cambia: está en `modelo.md`.

## 1. Principios

1. **El agente solo habla hacia afuera.** El cliente instala un agente en su cluster con un `helm install`. El agente abre una conexión saliente al plano de control. No hay puertos abiertos ni credenciales de kubeconfig en nuestras manos.
2. **Las credenciales de nube y los secretos se quedan en la nube del cliente.** El plano de control nunca guarda access keys ni secretos de aplicaciones. Guarda referencias. Lo que necesita identidad de nube (aplicar, provisionar, leer secretos) lo hace el agente con la identidad nativa de la nube (IRSA, Workload Identity, Managed Identity).
3. **Kubernetes es el runtime común.** EKS, GKE y AKS se ven iguales desde el agente. Lo que difiere por nube (registry, secretos, identidad, services) se esconde detrás de una interfaz `Provider`.
4. **`rampa.yaml` sigue siendo la fuente de verdad de la app.** El plano de control lo importa desde el repo y lo valida con el mismo paquete que el CLI.
5. **API first.** La web y el CLI son clientes de la misma API. Nada se puede hacer desde la web que no se pueda hacer con `curl`.
6. **Multi-tenant desde el día uno.** Toda tabla tiene `org_id`. Todo handler filtra por organización. Un binario, muchos clientes. La opción self-hosted es el mismo binario con una sola organización.
7. **El chart único vive en la API, el agente es un aplicador.** `charts/rampa-app` va embebido en el binario de la API, que renderiza y manda al agente objetos de Kubernetes en JSON. El agente los aplica y observa. Cambiar el chart no requiere actualizar agentes.

## 2. Componentes

```
                 ┌───────────────────────── nuestra nube ─────────────────────────┐
                 │                                                                │
  Web (React) ───┤►  API (Go)  ◄──►  Postgres                                     │
  CLI (Go)    ───┤►    │  ▲                                                       │
  CI (Actions)───┤►    │  │ webhooks                                              │
                 │     │  └────────── GitHub App                                  │
                 │     │ agent hub (WebSocket)                                    │
                 └─────┼──────────────────────────────────────────────────────────┘
                       │  conexión saliente, TLS, token por cluster
        ┌──────────────┼───────── nube del cliente (AWS, GCP o Azure) ────────────┐
        │              ▼                                                          │
        │   Agente (Go) en el cluster: renderiza rampa-app, aplica con SSA,       │
        │   observa rollouts, corre provisioning de services (tofu) como Jobs     │
        │              │                                                          │
        │   Namespaces <app>-<scope>  ·  ESO ↔ Secret manager de la nube          │
        │   Registry de la nube  ·  Identidad de workload  ·  RDS/CloudSQL/...    │
        └─────────────────────────────────────────────────────────────────────────┘
```

| Componente | Qué hace | Dónde vive en el repo |
|---|---|---|
| **API** | Plano de control. Auth, organizaciones, cuentas de nube, clusters, catálogo, scopes, deployments, eventos, aprobaciones. Hub de agentes. | `cmd/rampa-api`, `internal/api` |
| **Agente** | Corre en el cluster del cliente. Ejecuta comandos del plano de control y reporta estado. | `cmd/rampa-agent`, `internal/agent`, `charts/rampa-agent` |
| **Web** | Panel: conectar nube, instalar agente, ver catálogo, desplegar, ver estado y eventos en vivo. | `web/` |
| **CLI** | Lo que ya existe. Pasa a hablar con la API (`rampa login`, `rampa deploy` llama a la API en vez de escribir un repo). | `cmd/rampa`, `internal/cli` |
| **Paquetes compartidos** | `manifest` (rampa.yaml), `render` (values), `protocol` (mensajes agente), `provider` (nubes). | `pkg/` |
| **Chart de app** | El chart único. Embebido en la API (`charts/embed.go`). | `charts/rampa-app` |
| **Services** | Módulos OpenTofu por tipo y por nube. Los corre el agente. | `terraform/modules/<provider>/<tipo>` |

## 3. Modelo de datos

Todo cuelga de `organizations`. Los nombres son tablas de Postgres.

| Tabla | Campos clave | Notas |
|---|---|---|
| `organizations` | id, slug, name, plan | El cliente. |
| `users` | id, email, name | Identidad global. |
| `memberships` | org_id, user_id, role | owner, admin, developer, viewer. |
| `api_tokens` | org_id, user_id, hash, scopes, expires_at | Para CLI y CI. Se guarda el hash. |
| `cloud_accounts` | org_id, provider, name, external_ref, status | AWS: role ARN + external ID. GCP: project + WIF pool. Azure: tenant + client ID federado. Nunca claves. |
| `clusters` | org_id, cloud_account_id, name, env (nonprod, prod), token_hash, agent_version, k8s_version, last_seen, status, domain, ingress_class, secret_store | Un agente por cluster. `domain` es el wildcard de los scopes. |
| `applications` | org_id, name, team, tier, repo_url, manifest (jsonb), manifest_sha | `manifest` es el `rampa.yaml` importado y validado. |
| `scopes` | org_id, application_id, name, cluster_id, ephemeral, status, current_deployment_id, ttl_expires_at | El scope vive en un cluster. `pr-<n>` tiene TTL. |
| `deployments` | org_id, scope_id, image, values (jsonb), status, requested_by, approval_id, started_at, finished_at, message | status: pending, approved, applying, progressing, healthy, failed, rolled_back. |
| `parameters` | org_id, application_id, scope, name, value | Solo valores planos. Los secretos viven en la nube del cliente; acá solo existe la referencia implícita por convención de ruta. |
| `services` | org_id, application_id, name, type, size, scopes, status, provider_state_ref | El state de tofu está en un bucket del cliente. |
| `approvals` | org_id, deployment_id, required_by_policy, approved_by, approved_at | Prod requiere aprobación por defecto. |
| `events` | org_id, actor, kind, subject_type, subject_id, payload, created_at | Auditoría y timeline. La web los recibe por SSE. |
| `agent_commands` | org_id, cluster_id, kind, payload, status, result, created_at, acked_at | Cola durable de comandos al agente. Sobrevive a reconexiones. |

## 4. Flujo de deploy

1. Alguien pide un deploy: la web, el CLI o el workflow de CI hacen `POST /v1/orgs/{org}/apps/{app}/scopes/{scope}/deployments` con la imagen.
2. La API valida el manifest, resuelve el scope, renderiza los values con `pkg/render` usando los datos del cluster (dominio, ingress class, secret store, registry del provider) y crea el `deployment` en `pending`. Si el scope es `prod` y la política lo exige, queda en espera de aprobación.
3. La API encola un `agent_command` de tipo `deploy` para el cluster del scope y lo empuja por la conexión del agente si está online.
4. La API renderiza el chart embebido con esos values y manda al agente los objetos en JSON. El agente crea el namespace `<app>-<scope>` si no existe, aplica todo con server-side apply (field manager `rampa`) y hace prune de lo que ya no está.
5. El agente observa el rollout y reporta `applying`, `progressing`, `healthy` o `failed`, con eventos de Kubernetes relevantes. La API actualiza el deployment y emite eventos. La web los ve en vivo.
6. `undeploy` de un scope es borrar el namespace. Los scopes efímeros expiran por TTL o al cerrar el PR.

Rollback es un deploy con la imagen anterior. El historial de deployments es el historial.

## 5. El agente

- **Conexión:** WebSocket saliente a `wss://api.rampa.dev/v1/agent/connect`, autenticado con el token del cluster. Reconexión con backoff exponencial. Heartbeat cada 30 segundos.
- **Protocolo:** mensajes JSON tipados en `pkg/protocol`. `hello`, `heartbeat`, `command`, `result`, `event`, `log`. Cada comando tiene un id y el agente confirma recepción, así la API puede reintentar.
- **Comandos:** `deploy`, `undeploy`, `status`, `logs` (tail de un pod), `provision` (corre un Job con OpenTofu para un service; Fase C).
- **Permisos:** ClusterRole con namespaces, workloads, Service, Ingress, HPA, PDB, ServiceAccount, ResourceQuota, ExternalSecret y Rollout. Es amplio a propósito: el agente es el brazo del plano de control. Los clientes lo revisan en el chart.
- **Lo que instala junto a él (opcional, marcado en el chart):** External Secrets Operator con un `ClusterSecretStore` hacia el secret manager de la nube, Argo Rollouts si se usan canary o bluegreen.
- **Identidad de nube:** el ServiceAccount del agente lleva la anotación de IRSA, Workload Identity o Managed Identity. Con eso lista repos del registry, escribe y lee secretos, y corre tofu.

## 6. Providers

```go
type Provider interface {
    Kind() Kind                                              // aws | gcp | azure
    Verify(ctx, CloudAccount) (Verification, error)          // ¿podemos asumir la identidad?
    Registry(CloudAccount) (string, error)                   // ECR | Artifact Registry | ACR
    SecretStoreSpec(CloudAccount, ClusterInfo) SecretStoreSpec // qué ClusterSecretStore instala el agente
    WorkloadIdentityAnnotations(CloudAccount, identity) map[string]string
    ConnectInstructions(ConnectRequest) Instructions         // el link o comando para que el cliente conecte la cuenta
    ServiceModule(kind string) (ModuleRef, bool)      // terraform/modules/aws/postgres, etc.
}
```

| | AWS (primero) | GCP | Azure |
|---|---|---|---|
| Conectar cuenta | Rol IAM con external ID, creado con un link de CloudFormation quick-create | Workload Identity Federation hacia nuestro OIDC | App registration con credencial federada |
| Cluster | EKS | GKE | AKS |
| Registry | ECR | Artifact Registry | ACR |
| Secretos | Secrets Manager | Secret Manager | Key Vault |
| Identidad de pods | IRSA / Pod Identity | Workload Identity | Workload Identity (AAD) |
| Services | RDS, ElastiCache, SQS, S3, DynamoDB | Cloud SQL, Memorystore, Pub/Sub, GCS, Firestore | Azure DB for PostgreSQL, Azure Cache, Service Bus, Blob, Cosmos |

La interfaz se implementa completa para AWS en la Fase A. GCP y Azure llegan con la interfaz ya estable y sin tocar el resto del sistema.

## 7. Build de imágenes e integración con Git

- **Fase A:** el build sigue en el CI del cliente con el workflow reusable que ya existe. Cambia el último paso: en vez de escribir en un repo, llama a la API con la imagen. Un token de API por repo o por organización.
- **Fase B:** GitHub App de Rampa. Webhooks de push y pull request crean y destruyen scopes efímeros, comentan la URL en el PR y publican checks. La API lee el `rampa.yaml` del repo directamente.
- **Después:** builds dentro del cluster del cliente (BuildKit) para quien no tenga CI, y soporte GitLab.

## 8. Seguridad y multi-tenancy

- `org_id` en todas las tablas y en todos los queries. Tests que lo verifican.
- Tokens de API y de agente guardados como hash. Rotación desde la web.
- Roles: `owner` (facturación y borrar la org), `admin` (nubes, clusters, miembros), `developer` (apps y deploys), `viewer`.
- Aprobación obligatoria para `prod` por defecto, configurable por organización.
- Auditoría completa en `events`. Exportable.
- El plano de control nunca ve secretos de aplicación: la web escribe un secreto mandando un comando al agente, que lo guarda en el secret manager del cliente.
- Cada agente solo puede reportar sobre su propio cluster. Cada comando lleva el `cluster_id` y se verifica contra la conexión.

## 9. Qué se reusa de la Fase 0 y qué cambia

| Pieza | Destino |
|---|---|
| `docs/modelo.md`, `rampa.yaml`, `pkg/manifest` | Se reusan tal cual. Son el corazón del producto. |
| `charts/rampa-app` | Se reusa. Pasa a viajar embebido en la API, que lo renderiza. |
| `pkg/render` | Se reusa. La API lo llama con los datos del cluster en vez de `platform.yaml`. |
| CLI | Se reusa. `deploy` y `undeploy` pasan a llamar a la API. `render`, `validate` e `init` quedan igual. |
| Workflows reusables | `build` igual. `deploy` y `undeploy` llaman a la API. |
| Módulos Terraform | Se reusan. Se reorganizan por provider: `terraform/modules/aws/postgres`. |
| `rampa-deploys` y Argo CD | Dejan de ser requisito. Quedan como "modo GitOps" opcional para clientes que lo exijan, más adelante. |

## 10. Roadmap del producto

| Fase | Semanas | Entregable | Listo cuando |
|---|---|---|---|
| **A: núcleo** | 1 a 6 | API con auth, orgs, cuentas AWS, clusters, apps, scopes, deployments, eventos. Agente que aplica y reporta. Web mínima: conectar AWS, instalar agente, importar app, desplegar, ver estado. | Nuestro equipo despliega a producción desde la web. |
| **B: el día a día** | 6 a 12 | GitHub App y scopes efímeros. Parameters y secretos desde la web. Aprobaciones de prod. Auditoría. CLI contra la API. Logs en vivo. | Un equipo externo lo usa sin hablar con nosotros. |
| **C: datos y costos** | 12 a 20 | Services con OpenTofu corrido por el agente: postgres, redis, s3, sqs. Costos por app y scope. GCP completo. | Un cliente pide un Postgres desde la web y lo tiene en 15 minutos. |
| **D: enterprise** | 20 en adelante | Azure. SSO SAML/OIDC de la org del cliente. RBAC fino. Modo self-hosted. Billing. | Se puede firmar un contrato con un área de compras. |

## 11. Riesgos

- **Alcance.** Es un producto, no un proyecto. Nullplatform, Humanitec y Northflank tienen años de ventaja. La diferenciación tiene que ser explícita: simplicidad del modelo, precio, foco regional, o algo que ellos no hagan. Definirla antes de la Fase B.
- **Confianza.** Le pedimos al cliente instalar un agente con permisos amplios en su cluster. Chart auditable, binario firmado, código del agente abierto.
- **Sin cuentas de GCP y Azure no hay forma de probar.** La interfaz se diseña ahora, pero la implementación espera a tener cuentas.
- **Operar el plano de control** es operar producción para terceros: on-call, backups de Postgres, SLA. Se dogfoodea: la API se despliega con Rampa.
