# API de Rampa (v1)

Contrato entre el plano de control y sus clientes: la web, el CLI y los workflows de CI. Todo lo que hace la web se puede hacer con `curl`. Es la referencia que implementan `internal/api` (servidor), `web/` (cliente) e `internal/cli` (cliente).

## Convenciones

- Base: `https://<api>/v1`. JSON en ambos sentidos. `snake_case`. Fechas RFC 3339 en UTC. Ids UUID v4 como strings.
- En las rutas, `{org}` es el **slug** de la organización, `{app}` el **nombre** de la application y `{scope}` el nombre del scope. Los deployments, clusters y cuentas van por id.
- Errores: código HTTP adecuado y cuerpo `{"error": {"code": "not_found", "message": "..."}}`. Códigos: `unauthorized`, `forbidden`, `not_found`, `invalid`, `conflict`, `internal`.
- Validación de `rampa.yaml`: cuando falla, `invalid` con `"details": ["spec.type ...", ...]`.
- Listados devuelven `{"items": [...]}`. Sin paginación en la Fase A salvo eventos (`?limit=&before=`).
- Toda tabla y todo query filtra por `org_id`. Un usuario solo ve organizaciones donde es miembro.

## Autenticación

Dos mecanismos, ambos resuelven a un usuario (o a un token) y a un rol por organización.

1. **Sesión** (web): cookie `rampa_session`, HttpOnly, SameSite=Lax, firmada con `RAMPA_SESSION_SECRET`.
   - `POST /v1/auth/dev-login` `{"email": "..."}` → crea el usuario si no existe y abre sesión. **Solo si `RAMPA_DEV_LOGIN=true`.** Devuelve `Me`.
   - `GET /v1/auth/oidc/start` → redirige al IdP si `RAMPA_OIDC_ISSUER`, `RAMPA_OIDC_CLIENT_ID` y `RAMPA_OIDC_CLIENT_SECRET` están configurados. `GET /v1/auth/oidc/callback` cierra el flujo y redirige a `/`.
   - `POST /v1/auth/logout`.
2. **Token de API** (CLI, CI): header `Authorization: Bearer rampa_<token>`. El token pertenece a una organización y tiene un rol. Se guarda hasheado (SHA-256).

Roles por organización, de mayor a menor: `owner`, `admin`, `developer`, `viewer`.

| Acción | Rol mínimo |
|---|---|
| Ver todo | viewer |
| Importar apps, desplegar a scopes no prod, borrar scopes efímeros | developer |
| Desplegar a prod, aprobar, borrar scopes permanentes | admin |
| Nubes, clusters, miembros, tokens | admin |
| Borrar la organización | owner |

`GET /v1/me` → `Me`:
```json
{
  "user": {"id": "...", "email": "juan@acme.com", "name": "Juan"},
  "memberships": [{"org": {"id": "...", "slug": "acme", "name": "Acme"}, "role": "owner"}]
}
```

## Organizaciones

- `GET /v1/orgs` → `{"items": [Org]}` las del usuario.
- `POST /v1/orgs` `{"slug": "acme", "name": "Acme"}` → `Org`. El creador queda `owner`. Slug: `^[a-z0-9]([a-z0-9-]{1,38}[a-z0-9])?$`.
- `GET /v1/orgs/{org}` → `Org`.
- `PATCH /v1/orgs/{org}` `{"name": "...", "settings": {"require_approval_prod": false}}` (admin) → `Org`. Los dos campos son opcionales; `settings` reemplaza el objeto completo.
- `DELETE /v1/orgs/{org}` (owner).
- `GET /v1/orgs/{org}/members` → `{"items": [{"user": User, "role": "admin"}]}`.
- `POST /v1/orgs/{org}/members` `{"email": "...", "role": "developer"}` → agrega o cambia rol (crea el usuario si no existe).
- `DELETE /v1/orgs/{org}/members/{user_id}`.
- Tokens: `GET /v1/orgs/{org}/tokens` → `{"items": [Token]}` sin el secreto. `POST /v1/orgs/{org}/tokens` `{"name": "ci-checkout", "role": "developer", "expires_in_days": 365}` → `Token` con `"token": "rampa_..."` **una sola vez**. `DELETE /v1/orgs/{org}/tokens/{id}`.

`Org`: `{"id", "slug", "name", "created_at", "settings": {"require_approval_prod": true}}`.

`Token`: `{"id", "name", "role", "expires_at": "...|null", "last_used_at": "...|null", "created_at"}`, más `"token"` solo en la respuesta del `POST`.

Un token nunca otorga más que su dueño hoy: el rol efectivo en cada request es el **menor** entre el `role` del token y el rol de la membresía actual de quien lo creó. Si a esa persona la sacan de la org (`DELETE /members/{user_id}`), sus tokens en la org se borran en la misma transacción y dejan de autenticar; si le bajan el rol (`POST /members`), los tokens que tenían un rol superior bajan al nuevo. El stream SSE (`/events/stream`) re-chequea membresía y existencia del token en cada ping y se cierra si el acceso se revocó.

`POST /v1/orgs/{org}/members` exige que el actor tenga rol mayor o igual al rol **actual** del objetivo, además del rol nuevo: un admin no puede degradar a un owner (responde `forbidden`), igual que no puede sacarlo.

## Providers y cuentas de nube

- `GET /v1/orgs/{org}/providers` → `{"items": [{"kind": "aws", "display_name": "Amazon Web Services", "available": true}]}`. Salen los tres; `available` dice si está implementado.
- `POST /v1/orgs/{org}/cloud-accounts` `{"provider": "aws", "name": "acme-prod", "region": "us-east-1"}` → `CloudAccount` en `status: "pending"` con `instructions` (ver `provider.Instructions`): la API genera el `external_id`, lo guarda en `settings`, y arma el link de CloudFormation. El cliente lo ejecuta y vuelve con el ARN del rol.
- `PATCH /v1/orgs/{org}/cloud-accounts/{id}` `{"external_ref": "arn:aws:iam::123:role/RampaConnect", "settings": {...}}` → completa lo que pidió `instructions.fields`.
- `POST /v1/orgs/{org}/cloud-accounts/{id}/verify` → corre `Provider.Verify`. `status` pasa a `connected` o `failed`; devuelve `CloudAccount` con `verification`.
- `GET /v1/orgs/{org}/cloud-accounts`, `GET .../{id}`, `DELETE .../{id}` (falla con `conflict` si tiene clusters).
- `GET /public/cloudformation/rampa-connect.yaml` → plantilla de CloudFormation para el rol de AWS. Pública, sin auth, la sirve la API desde `pkg/provider/aws`. La consola de CloudFormation solo acepta plantillas alojadas en S3, así que el link de quick-create (`instructions.url`) existe únicamente si `RAMPA_AWS_TEMPLATE_URL` apunta a una copia de esta plantilla en S3; sin esa variable `url` viene vacío y `instructions.command` descarga la plantilla desde esta ruta con `curl` y crea el stack con `--template-body file://rampa-connect.yaml`.

`CloudAccount`: `{"id", "provider", "name", "region", "external_ref", "settings", "status", "verification": {"ok", "identity", "message"}|null, "instructions": Instructions|null, "created_at"}`. `settings` nunca contiene credenciales. `instructions` viene en todas las respuestas (no solo en el `POST`) y es `null` únicamente si el provider no está registrado en esa instalación; se arma con la `region` de la cuenta.

`Instructions`: `{"title", "description", "url", "command", "fields": [{"name", "label", "placeholder", "help", "required"}]}`. `fields[].name` es `external_ref` o una clave de `settings`.

## Clusters y agente

- `POST /v1/orgs/{org}/clusters` `{"name": "eks-nonprod", "env": "nonprod", "cloud_account_id": "...", "domain": "apps.dev.acme.com", "ingress_class": "nginx", "secret_store": "aws-secrets-manager"}` → `Cluster` con `"token": "rampa_cluster_..."` **una sola vez** e `"install_command"`: el `helm install` listo para pegar. Usa las claves de `charts/rampa-agent/values.yaml`: `helm upgrade --install rampa-agent <RAMPA_CHART_REPO> --namespace rampa-system --create-namespace --set api.url=<RAMPA_PUBLIC_URL> --set cluster.token=<token> --set-string image.repository=<repo> --set-string image.tag=<tag>` (repo y tag salen de `RAMPA_AGENT_IMAGE`; `image.*` va con `--set-string` para que un tag numérico no se interprete como entero).
- `GET /v1/orgs/{org}/clusters`, `GET .../{id}`, `DELETE .../{id}` (falla con `conflict` si tiene scopes). Si el agente está conectado, se le cierra la conexión WebSocket.
- `POST /v1/orgs/{org}/clusters/{id}/rotate-token` → nuevo token, el anterior deja de valer. La conexión viva del agente, autenticada con el token viejo, se cierra en el acto (close `1008`); el agente reconecta con el nuevo.
- `env` es `nonprod` o `prod`. Un scope `prod` solo puede vivir en un cluster `prod`; el resto solo en `nonprod`. Si la org tiene un solo cluster de cada env, es el default al desplegar.

`Cluster`: `{"id", "name", "env", "cloud_account_id", "domain", "ingress_class", "secret_store", "status": "offline|online", "agent_version", "kubernetes_version", "last_seen_at", "created_at"}`.

**Conexión del agente:** `GET /v1/agent/connect` con `Authorization: Bearer <token del cluster>` y upgrade a WebSocket. Mensajes según `pkg/protocol`. La API:

- Marca el cluster `online` al recibir `hello` y `offline` cuando cae la conexión o pasan 3 heartbeats sin noticias.
- Guarda cada comando en `agent_commands` antes de mandarlo. Al reconectar el agente, reenvía los `queued` y los `sent` sin `ack`.
- Con el `result` de un `deploy`, actualiza comando, deployment y scope en una sola transacción y después emite el evento. Con cada `event`, emite un evento de la org. Un `result` o `ack` que falla contra la base se reintenta con backoff acotado antes de descartarse (queda en el log con `command_id`).
- Al recibir `hello`, compara los comandos `acked` del cluster con `hello.in_flight`: los que el agente ya no tiene en curso se cierran como `failed` (deployment `failed` con mensaje `"el agente se reinició y el resultado se perdió"`, scope actualizado, evento `deployment.failed`).
- Un reaper corre cada minuto y cierra como `failed` los comandos `sent` o `acked` cuya antigüedad supera `timeout_seconds` del payload (o `RAMPA_DEPLOY_TIMEOUT` si el comando no lo trae) más 2 minutos, para que ningún deployment quede en `applying`/`progressing` para siempre.
- Un scope en `deleting` no cambia con `event` ni `result` de un deploy: el undeploy encolado manda.

## Applications

- `POST /v1/orgs/{org}/apps` `{"manifest": "<contenido del rampa.yaml>", "repo_url": "https://github.com/acme/checkout-api"}` → `Application`. Valida con `pkg/manifest`; `conflict` si el nombre ya existe en la org. `repo_url` es opcional y, si viene, tiene que ser una URL `http(s)` absoluta (`invalid` si no).
- `PUT /v1/orgs/{org}/apps/{app}/manifest` `{"manifest": "..."}` → reemplaza el manifest (el nombre no puede cambiar).
- `GET /v1/orgs/{org}/apps` → `{"items": [Application]}`. `GET .../{app}`. `DELETE .../{app}` (falla con `conflict` si tiene scopes desplegados; el chequeo y el borrado son una sola transacción).

`Application`: `{"id", "name", "team", "tier", "description", "type", "runtime", "repo_url", "manifest": "<yaml>", "manifest_sha": "...", "scopes_declared": ["dev", "prod", "pr"], "created_at", "updated_at"}`.

## Scopes

- `GET /v1/orgs/{org}/apps/{app}/scopes` → `{"items": [Scope]}`: los declarados en el manifest más los efímeros vivos, con su estado en runtime.
- `PUT /v1/orgs/{org}/apps/{app}/scopes/{scope}` `{"cluster_id": "..."}` → `200` con el `Scope` atado al cluster. `cluster_id` es opcional si la org tiene un solo cluster del env correcto. `conflict` si el scope está desplegado en otro cluster.
- `DELETE /v1/orgs/{org}/apps/{app}/scopes/{scope}` → `202` con el `Scope` en `status: "deleting"`; encola `undeploy` y emite `scope.delete_requested`. Cuando el agente confirma, `scope.deleted`: los efímeros desaparecen y los permanentes vuelven a `idle`. Scopes permanentes requieren `admin`.

`Scope`: `{"name", "ephemeral", "declared", "cluster_id", "namespace": "checkout-api-prod", "url": "https://checkout.acme.com", "status": "unbound|idle|deploying|healthy|degraded|failed|deleting", "current_deployment": Deployment|null, "size", "replicas", "strategy", "ttl_expires_at"}`.

## Deployments

- `POST /v1/orgs/{org}/apps/{app}/scopes/{scope}/deployments` `{"image": "sha-a1b2c3d", "message": "opcional"}` → `Deployment`. `image` puede ser un tag o una referencia completa; un tag (sin `/`) tiene que cumplir `^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$` o la respuesta es `invalid`. `conflict` si el scope está en `deleting`. Flujo:
  1. Resuelve el scope (`pkg/manifest.ResolveScope`), el cluster y la cuenta de nube.
  2. Renderiza values con `pkg/render.Build` usando `Options{Image, Registry: provider.Registry(cuenta), Cluster: &render.Cluster{Domain, IngressClass, SecretStore}}`.
  3. Renderiza el chart `charts.RampaApp` con esos values (Helm SDK, `engine.Render`), separa documentos, convierte a JSON, descarta vacíos.
  4. Si el scope es `prod` y `settings.require_approval_prod`, status `awaiting_approval` (evento `deployment.awaiting_approval`). Si no, `queued` y encola un `deploy` al agente.
- `POST /v1/orgs/{org}/deployments/{id}/approve` (admin) → pasa de `awaiting_approval` a `queued` y encola. La transición es condicional dentro de la transacción: un segundo approve (o uno que llega después de un cancel) responde `conflict` con el estado actual. También `conflict` si el scope se ató a otro cluster mientras esperaba aprobación: hay que cancelar y pedir el deploy de nuevo.
- `POST /v1/orgs/{org}/deployments/{id}/cancel` → solo si `awaiting_approval` o `queued`; en cualquier otro estado, `conflict`. Si el deployment está `queued` pero el agente ya hizo `ack` del comando, también `conflict`: el deploy está corriendo en el cluster y hay que esperar su `result`. Al cancelar, el scope vuelve a su último deployment `healthy` (o queda `idle` sin deployment actual si no hay ninguno).
- `GET /v1/orgs/{org}/apps/{app}/scopes/{scope}/deployments?limit=20` → `{"items": [Deployment]}` del más nuevo al más viejo.
- `GET /v1/orgs/{org}/deployments/{id}` → `Deployment`.

`Deployment`: `{"id", "app", "scope", "cluster_id", "image": "<referencia completa>", "status", "message", "requested_by": User, "approved_by": User|null, "workload": WorkloadStatus|null, "created_at", "started_at", "finished_at"}`.

Estados: `awaiting_approval → queued → applying → progressing → healthy | failed`, y `cancelled` desde los dos primeros. `applying`, `progressing`, `healthy` y `failed` los pone el agente vía `event` y `result`. `timeout` del agente se mapea a `failed` con mensaje.

## Eventos

- `GET /v1/orgs/{org}/events?subject_type=deployment&subject_id=...&limit=50&before=<created_at>` → `{"items": [Event]}`.
- `GET /v1/orgs/{org}/events/stream?subject_type=&subject_id=` → **Server-Sent Events**. Cada evento: `event: <kind>` y `data: <Event en JSON>`. Manda `: ping` cada 25 segundos. Sin filtro manda todos los de la org.

`Event`: `{"id", "kind", "actor": {"type": "user|token|agent|system", "id", "name"}, "subject_type": "org|cloud_account|cluster|application|scope|deployment", "subject_id", "message", "payload": {}, "created_at"}`.

Kinds (lista cerrada; la web registra un listener por cada uno, así que un kind nuevo se agrega acá y en `web/src/api/types.ts`):

| Sujeto | Kinds |
|---|---|
| `org` | `org.created`, `org.updated`, `org.member_set`, `org.member_removed`, `org.token_created`, `org.token_deleted` |
| `cloud_account` | `cloud_account.created`, `cloud_account.updated`, `cloud_account.verified`, `cloud_account.failed`, `cloud_account.deleted` |
| `cluster` | `cluster.created`, `cluster.online`, `cluster.offline`, `cluster.token_rotated`, `cluster.deleted`, `cluster.command_progress` |
| `application` | `application.imported`, `application.updated`, `application.deleted` |
| `scope` | `scope.bound`, `scope.delete_requested`, `scope.progress`, `scope.deleted`, `scope.failed` |
| `deployment` | `deployment.requested`, `deployment.awaiting_approval`, `deployment.approved`, `deployment.cancelled`, `deployment.queued`, `deployment.applying`, `deployment.progress`, `deployment.progressing`, `deployment.healthy`, `deployment.failed` |

Los eventos de `scope` y `deployment` llevan siempre `payload.app` y `payload.scope` (nombres), para que los clientes sepan qué pantalla refrescar. `deployment.progress` es un hito del agente que no cambia el estado (por ejemplo `Applied` o `Pruned`); los `deployment.<estado>` sí lo cambian.

## Salud

- `GET /healthz` → 200 siempre que el proceso viva. `GET /readyz` → 200 si la base responde.
- `GET /version` → `{"version": "...", "protocol": 1}`.

## Configuración del servidor

| Variable | Default | Qué es |
|---|---|---|
| `RAMPA_LISTEN` | `:8080` | Dirección de escucha |
| `RAMPA_DATABASE_URL` | requerido | Postgres, formato `postgres://user:pass@host/db` |
| `RAMPA_PUBLIC_URL` | `http://localhost:8080` | URL pública de la API (links, callbacks, install command) |
| `RAMPA_SESSION_SECRET` | requerido salvo dev | 32+ bytes para firmar cookies |
| `RAMPA_DEV_LOGIN` | `false` | Habilita `POST /v1/auth/dev-login` |
| `RAMPA_OIDC_ISSUER`, `RAMPA_OIDC_CLIENT_ID`, `RAMPA_OIDC_CLIENT_SECRET` | vacíos | Login OIDC |
| `RAMPA_WEB_DIR` | vacío | Si se setea, sirve la SPA desde ahí con fallback a `index.html` |
| `RAMPA_AGENT_IMAGE` | `ghcr.io/CHANGEME/rampa-agent:latest` | Imagen que va en el `install_command` |
| `RAMPA_CHART_REPO` | `oci://ghcr.io/CHANGEME/charts/rampa-agent` | Chart que va en el `install_command` |
| `RAMPA_DEPLOY_TIMEOUT` | `10m` | Cuánto espera el agente a que un deploy esté sano. También es el timeout que asume el reaper para comandos sin `timeout_seconds` |
| `RAMPA_AWS_TRUSTED_PRINCIPAL_ARN` | vacío | Identidad IAM del plano de control (rol o usuario) que asume los roles de los clientes. Sin esto `verify` de cuentas AWS falla con un mensaje claro |
| `RAMPA_AWS_DEFAULT_REGION` | `us-east-1` | Región para el link de CloudFormation y el cliente STS base cuando la cuenta no trae una |
| `RAMPA_AWS_TEMPLATE_URL` | vacío | URL en S3 (o CloudFront delante de S3) de una copia de `rampa-connect.yaml`. Si está, `instructions.url` es el link de quick-create de CloudFormation; si no, no hay link y `instructions.command` descarga la plantilla desde la API y usa `--template-body` |

Las migraciones corren al arrancar (`schema_migrations`). El binario acepta `rampa-api serve` (default) y `rampa-api migrate`.

## Configuración del agente

| Variable | Default | Qué es |
|---|---|---|
| `RAMPA_API_URL` | requerido | `https://api.rampa.dev` |
| `RAMPA_CLUSTER_TOKEN` | requerido | El token del cluster |
| `RAMPA_KUBECONFIG` | vacío | Para correr fuera del cluster; adentro usa in-cluster |
| `RAMPA_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `RAMPA_HEALTH_ADDR` | `:8081` | Dónde sirve `GET /healthz` (proceso vivo) y `GET /readyz` (200 solo con la sesión WebSocket establecida). El chart `rampa-agent` la deriva de `healthPort` y apunta ahí sus probes. |

## CLI contra la API

- `rampa login --api https://api.rampa.dev --token rampa_...` guarda `~/.config/rampa/config.yaml` (`api`, `token`, `org`). También `RAMPA_API`, `RAMPA_TOKEN`, `RAMPA_ORG` como variables de entorno, que ganan.
- `rampa apps import [-f rampa.yaml] [--repo URL]` → `POST /apps` o `PUT /manifest` si ya existe.
- `rampa deploy --scope prod --image sha-... [--wait]` → `POST .../deployments`; con `--wait` sigue el stream de eventos hasta `healthy` o `failed` y sale con código 1 si falla.
- `rampa undeploy --scope pr-42` → `DELETE .../scopes/pr-42`.
- `rampa scope list` muestra además `status` y `url` reales si hay sesión.
- `render`, `validate` e `init` siguen funcionando sin conexión.
