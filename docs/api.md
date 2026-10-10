# API de Rampa (v1)

Contrato entre el plano de control y sus clientes: la web, el CLI y los workflows de CI. Todo lo que hace la web se puede hacer con `curl`. Es la referencia que implementan `internal/api` (servidor), `web/` (cliente) e `internal/cli` (cliente).

## Convenciones

- Base: `https://<api>/v1`. JSON en ambos sentidos. `snake_case`. Fechas RFC 3339 en UTC. Ids UUID v4 como strings.
- En las rutas, `{org}` es el **slug** de la organización, `{app}` el **nombre** de la application y `{scope}` el nombre del scope. Los deployments, clusters y cuentas van por id.
- Errores: código HTTP adecuado y cuerpo `{"error": {"code": "not_found", "message": "..."}}`. Códigos: `unauthorized`, `forbidden`, `not_found`, `invalid`, `conflict`, `rate_limited`, `not_implemented`, `internal`.
- Validación de `rampa.yaml`: cuando falla, `invalid` con `"details": ["spec.type ...", ...]`.
- Listados devuelven `{"items": [...]}`. Sin paginación en la Fase A salvo eventos (`?limit=&before=`).
- Toda tabla y todo query filtra por `org_id`. Un usuario solo ve organizaciones donde es miembro.
- Idioma: los mensajes (`error.message`, `details`, textos de instrucciones) salen en el idioma de `Accept-Language` (`es` por defecto, `en`). Los `code` son estables y no dependen del idioma. `GET /v1/me` devuelve `locale` y `PATCH /v1/me` `{"locale": "en"}` lo guarda; si está seteado gana sobre el header.

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

- `GET /v1/orgs/{org}/providers` → `{"items": [{"kind": "aws", "display_name": "Amazon Web Services", "available": true, "auth": "federated|token", "terminology": {"account": {"es": "cuenta de AWS", "en": "AWS account"}, "cluster": {"es": "cluster EKS", "en": "EKS cluster"}, "vm": {"es": "instancia EC2", "en": "EC2 instance"}, "registry": {"es": "ECR", "en": "ECR"}, "secret_store": {"es": "Secrets Manager", "en": "Secrets Manager"}, "region": {"es": "región", "en": "region"}}}]}`. Salen todos los providers conocidos (aws, gcp, azure, digitalocean, oci, ovh); `available` dice si está implementado; `auth` dice si la cuenta se conecta por federación (sin secretos) o con un token del cliente. La web usa `terminology` en todos sus textos: en DigitalOcean dice Droplet y DOKS, en Azure máquina virtual y AKS.
- `POST /v1/orgs/{org}/cloud-accounts` `{"provider": "aws", "name": "acme-prod", "region": "us-east-1"}` → `CloudAccount` en `status: "pending"` con `instructions` (ver `provider.Instructions`): la API genera el `external_id`, lo guarda en `settings`, y arma el link de CloudFormation. El cliente lo ejecuta y vuelve con el ARN del rol.
- `PATCH /v1/orgs/{org}/cloud-accounts/{id}` `{"external_ref": "arn:aws:iam::123:role/RampaConnect", "settings": {...}}` → completa lo que pidió `instructions.fields`.
- `POST /v1/orgs/{org}/cloud-accounts/{id}/verify` → corre `Provider.Verify`. `status` pasa a `connected` o `failed`; devuelve `CloudAccount` con `verification`.
- `GET /v1/orgs/{org}/cloud-accounts`, `GET .../{id}`, `DELETE .../{id}` (falla con `conflict` si tiene clusters).
- `GET /public/cloudformation/rampa-connect.yaml` → plantilla de CloudFormation para el rol de AWS. Pública, sin auth, la sirve la API desde `pkg/provider/aws`. La consola de CloudFormation solo acepta plantillas alojadas en S3, así que el link de quick-create (`instructions.url`) existe únicamente si `RAMPA_AWS_TEMPLATE_URL` apunta a una copia de esta plantilla en S3; sin esa variable `url` viene vacío y `instructions.command` descarga la plantilla desde esta ruta con `curl` y crea el stack con `--template-body file://rampa-connect.yaml`.

`CloudAccount`: `{"id", "provider", "name", "region", "external_ref", "settings", "status", "verification": {"ok", "identity", "message"}|null, "instructions": Instructions|null, "has_credentials": false, "created_at"}`. `settings` nunca contiene credenciales. `instructions` viene en todas las respuestas (no solo en el `POST`) y es `null` únicamente si el provider no está registrado en esa instalación; se arma con la `region` de la cuenta.

**Providers con `auth: token`** (DigitalOcean, OCI, OVH): `instructions.fields` incluye campos con `"secret": true` (por ejemplo `api_token`). Al hacer `PATCH` con esos campos (en `"credentials": {...}`, o dentro de `settings`, de donde se quitan) la API los cifra con AES-256-GCM (clave `RAMPA_STATE_KEY`, dato autenticado el id de la cuenta) y los guarda en `credentials_ciphertext`; nunca vuelven en ninguna respuesta, `has_credentials` pasa a `true`, y `verify` los usa para llamar a la nube. Volver a mandarlos los rota. `DELETE` de la cuenta borra el cifrado. Sin `RAMPA_STATE_KEY` estos providers responden `501`.

`Instructions`: `{"title", "description", "url", "command", "fields": [{"name", "label", "placeholder", "help", "required"}]}`. `fields[].name` es `external_ref` o una clave de `settings`.

## Clusters y agente

- `POST /v1/orgs/{org}/clusters` `{"name": "eks-nonprod", "env": "nonprod", "cloud_account_id": "...", "domain": "apps.dev.acme.com", "ingress_class": "nginx", "secret_store": "aws-secrets-manager"}` → `Cluster` con `"token": "rampa_cluster_..."` **una sola vez** e `"install_command"`: el `helm install` listo para pegar. Usa las claves de `charts/rampa-agent/values.yaml`: `helm upgrade --install rampa-agent <RAMPA_CHART_REPO> --namespace rampa-system --create-namespace --set api.url=<RAMPA_PUBLIC_URL> --set cluster.token=<token> --set-string image.repository=<repo> --set-string image.tag=<tag>` (repo y tag salen de `RAMPA_AGENT_IMAGE`; `image.*` va con `--set-string` para que un tag numérico no se interprete como entero).
- `GET /v1/orgs/{org}/clusters`, `GET .../{id}`, `DELETE .../{id}` (falla con `conflict` si tiene scopes). Si el agente está conectado, se le cierra la conexión WebSocket.
- `POST /v1/orgs/{org}/clusters/{id}/rotate-token` → nuevo token, el anterior deja de valer. La conexión viva del agente, autenticada con el token viejo, se cierra en el acto (close `1008`); el agente reconecta con el nuevo.
- `env` es `nonprod` o `prod`. Un scope `prod` solo puede vivir en un cluster `prod`; el resto solo en `nonprod`. Si la org tiene un solo cluster de cada env, es el default al desplegar.

`Cluster`: `{"id", "name", "kind": "kubernetes|vm", "env", "cloud_account_id", "domain", "ingress_class", "secret_store", "status": "offline|online|failed", "agent_version", "kubernetes_version", "agent_role_arn", "infra_run_id", "last_seen_at", "created_at"}`. `failed` es un cluster cuyo run de infraestructura falló; `infra_run_id` es `""` si no lo creó Rampa.

`kind` es `kubernetes` (cluster administrado o registrado) o `vm` (una máquina de un solo nodo con k3s creada por Rampa). `secret_store` acepta el nombre de un ClusterSecretStore de External Secrets o el valor especial `kubernetes`: en ese caso el chart no usa ExternalSecret y el agente mantiene el Secret `<app>-params` con lo que le manda la API.

- `POST /v1/orgs/{org}/clusters/{id}/bootstrap-token` (admin) → 201 `{"bootstrap_token": "rampa_bootstrap_...", "expires_at", "cluster_id"}`. Es de un solo uso y vence en 2 horas. Sirve para instalar el agente donde el token real no debe quedar escrito (cloud-init de una VM, un script de instalación). El chart lo acepta en `cluster.bootstrapToken`.
- `POST /v1/agent/bootstrap` `{"bootstrap_token": "..."}` sin sesión → `{"cluster_token": "rampa_cluster_...", "cluster_id", "api_url"}`. Marca el bootstrap como usado y rota el token del cluster (cierra la conexión de un agente anterior; evento `cluster.token_rotated` con `payload.via = "bootstrap"`). Un segundo intento responde `401`; más de 10 intentos por minuto por IP o por token, `429` con código `rate_limited`. El agente guarda el token en el Secret `rampa-agent-token` de su namespace y arranca normal.
- `GET /public/charts/rampa-agent.tgz` → el chart del agente empaquetado, sin auth, para que un cloud-init lo instale con `helm install rampa-agent <url>`.
- `GET /v1/orgs/{org}/clusters/{id}/install-command` (admin) → `{"install_command", "expires_at"}` regenerado con un bootstrap token nuevo, sin rotar el token del cluster.

**Secretos de aplicación:** `PUT /v1/orgs/{org}/apps/{app}/scopes/{scope}/secrets` `{"values": {"API_KEY": "..."}}` (developer; prod requiere admin) → `202 {"keys": ["API_KEY"]}`. La API no persiste los valores: arma un comando `write_secret` al agente del cluster del scope con `target: kubernetes` (Secret `<app>-params` en el namespace del scope) o `target: cloud` (el agente escribe `rampa/<app>/<scope>/<NOMBRE>` en el gestor de secretos con su identidad de workload) según `secret_store` del cluster. El evento `scope.secrets_written` lleva solo las claves. `GET .../secrets` → `{"keys": [...]}` conocidas (sin valores). El payload de `agent_commands` para `write_secret` se guarda sin `values` (se sustituye por las claves) y el comando se entrega solo por la conexión viva; si el agente está offline responde `409`.

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

`Scope`: `{"name", "compute": "kubernetes|serverless", "ephemeral", "declared", "cluster_id", "namespace": "checkout-api-prod", "url": "https://checkout.acme.com", "status": "unbound|idle|deploying|healthy|degraded|failed|deleting", "current_deployment": Deployment|null, "size", "replicas", "strategy", "ttl_expires_at"}`.

## Applications sin YAML: spec estructurado

La web crea y edita aplicaciones con formularios. El formulario produce un `ApplicationSpec` en JSON; la API lo convierte al `rampa.yaml` canónico con `pkg/manifest`, lo valida igual que un manifest pegado, y lo guarda. El `rampa.yaml` sigue siendo la fuente de verdad: el spec es solo otra forma de escribirlo, y siempre se puede pasar de uno a otro sin pérdida.

`ApplicationSpec`:

```json
{
  "name": "checkout-api",
  "team": "payments",
  "tier": 2,
  "description": "API de checkout",
  "type": "api",
  "runtime": "go",
  "port": 8080,
  "healthcheck": "/health",
  "schedule": "",
  "scopes": [
    {"name": "dev", "size": "S", "replicas": 1, "strategy": "rolling", "domain": "", "autoscaling": null, "ttl": ""},
    {"name": "prod", "size": "M", "replicas": 3, "strategy": "canary", "domain": "checkout.acme.com",
     "autoscaling": {"min": 3, "max": 10, "cpu": 70}, "ttl": ""},
    {"name": "pr", "size": "S", "replicas": 1, "strategy": "rolling", "domain": "", "autoscaling": null, "ttl": "72h"}
  ],
  "parameters": [
    {"name": "LOG_LEVEL", "source": "value", "value": "info", "scopes": []},
    {"name": "LOG_LEVEL", "source": "value", "value": "debug", "scopes": ["dev", "pr"]},
    {"name": "API_KEY", "source": "secret", "scopes": []},
    {"name": "DB_URL", "source": "service", "service": "checkout-db", "key": "url", "scopes": []}
  ],
  "services": [
    {"name": "checkout-db", "type": "postgres", "size": "S", "scopes": ["prod"]}
  ]
}
```

Reglas: `source` es `value`, `secret` o `service` y determina qué otros campos aplican (`value`; ninguno; `service` y `key`). `scopes` vacío significa todos. El scope `pr` es la plantilla de efímeros. Los defaults son los de `pkg/manifest` (tier 2, size S, replicas 1, strategy rolling, port 8080, healthcheck `/health`, ttl `72h`).

- `POST /v1/orgs/{org}/apps/preview` `{"spec": ApplicationSpec}` → `{"manifest": "<yaml>", "valid": true, "details": []}`. No guarda nada. Sirve para mostrar el YAML resultante y los errores mientras el usuario completa el formulario. Con errores responde 200 con `valid: false` y `details`.
- `POST /v1/orgs/{org}/apps/from-spec` `{"spec": ApplicationSpec, "repo_url": "..."}` → `Application` (201). Mismas validaciones y errores que `POST /apps`.
- `GET /v1/orgs/{org}/apps/{app}/spec` → `{"spec": ApplicationSpec, "manifest": "<yaml>"}`. Si el manifest fue pegado a mano, el spec se deriva de él.
- `PUT /v1/orgs/{org}/apps/{app}/spec` `{"spec": ApplicationSpec}` → `Application`. El nombre no puede cambiar.
- `GET /v1/orgs/{org}/apps/{app}/scaffold.zip?github_org=<org de GitHub>` → zip con el proyecto inicial del golden path para el `type` y `runtime` de la app (hoy solo `api-go`), con el `rampa.yaml` real de la app y los workflows apuntando a `<github_org>/rampa`. Si no hay golden path para esa combinación, `invalid` con el mensaje de qué combinaciones existen. `github_org` cae a `settings.github_org` de la org y después a `CHANGEME`.

`OrgSettings` suma `github_org` (string, opcional): la organización de GitHub donde vive el fork o copia de `rampa` que ejecuta los workflows reusables. Se cambia con `PATCH /v1/orgs/{org}`.

## Onboarding guiado

- `GET /v1/orgs/{org}/onboarding` → estado de los pasos para levantar infraestructura desde la web, sin escribir YAML:

```json
{
  "steps": [
    {"key": "cloud",   "title": "Conectar una cuenta de nube",  "done": true,  "detail": "acme-nonprod (aws, connected)"},
    {"key": "cluster", "title": "Registrar o crear un cluster", "done": true,  "detail": "eks-nonprod"},
    {"key": "agent",   "title": "Instalar el agente",           "done": false, "detail": "eks-nonprod está offline"},
    {"key": "app",     "title": "Crear la primera aplicación",  "done": false, "detail": ""},
    {"key": "deploy",  "title": "Hacer el primer deploy",       "done": false, "detail": ""}
  ],
  "next": "agent",
  "complete": false
}
```

`cloud` está hecho si hay al menos una cuenta `connected`. `cluster` si hay al menos un cluster. `agent` si al menos un cluster está `online`. `app` si hay una application. `deploy` si algún deployment llegó a `healthy`.

## Infraestructura con clicks: OpenTofu ejecutado por Rampa

Una vez conectada la cuenta, Rampa crea infraestructura en la nube del cliente sin que escriba nada. La API genera un root module de OpenTofu que invoca los módulos de `terraform/modules/<provider>/...` (embebidos en el binario), asume la identidad de la cuenta conectada y corre `tofu init`, `plan` y `apply`. Es el mismo mecanismo para todos los proveedores: cambia el módulo, no el flujo. El estado se guarda cifrado en Postgres a través del backend `http` de OpenTofu. Cada ejecución es un `InfraRun` con log en vivo.

El primer caso es el cluster: el módulo `aws/eks-cluster` crea VPC, cluster EKS con Auto Mode, proveedor OIDC, rol IAM del agente e **instala el agente con Helm en el mismo apply**, con el token que la API generó al registrar el cluster. Al terminar, el cluster aparece online en Rampa sin que el cliente haya tocado una terminal.

- `POST /v1/orgs/{org}/infra/clusters` `{"cloud_account_id": "...", "name": "rampa-nonprod", "env": "nonprod", "kubernetes_version": "1.33", "domain": "apps.dev.acme.com", "ingress_class": "nginx", "admin_principal_arns": ["arn:aws:iam::123:user/juan"]}` → 202 `InfraRun`. Requiere `admin` y una cuenta `connected`. La API: (1) crea el `Cluster` en Rampa (offline, con su token) y lo enlaza al run; (2) encola el run con los inputs del módulo, incluidos `api_url` y el token del agente como variable sensible; (3) al terminar con éxito guarda los outputs en el run y `agent_role_arn` en el cluster; si falla, el cluster queda con `status: "failed"` y `infra_run_id` para reintentar o borrar. `admin_principal_arns` son las identidades IAM del cliente que van a poder usar `kubectl`; opcional.
- `GET /v1/orgs/{org}/infra/runs?limit=20` → `{"items": [InfraRun]}`. `GET /v1/orgs/{org}/infra/runs/{id}` → `InfraRun`.
- `GET /v1/orgs/{org}/infra/runs/{id}/log` → `text/plain` con el log completo. Con `?stream=1` (EventSource no puede mandar headers) responde Server-Sent Events: primero las líneas ya guardadas, después las nuevas en vivo (`event: line`, `data: <texto>`), `event: status` con `data: <estado>` en cada cambio, y cierra con `event: done` cuando el run termina.
- `POST /v1/orgs/{org}/infra/runs/{id}/destroy` (admin) → encola `tofu destroy` con los mismos inputs; al terminar borra el cluster de Rampa si no tiene scopes. `POST .../retry` → vuelve a encolar un run `failed`.
- `GET /v1/orgs/{org}/infra/modules` → `{"items": [{"provider": "aws", "kind": "cluster", "module": "aws/eks-cluster", "available": true, "secret_store": "aws-secrets-manager", "inputs": [{"name": "kubernetes_version", "type": "string|list|bool|number", "label": "...", "help": "...", "default": "1.33", "options": ["1.31","1.32","1.33","1.34"], "required": false}]}, {"provider": "gcp", "kind": "cluster", "available": false}, ...]}`. Los inputs se validan al crear el run (tipo, opciones, requeridos, formatos como CIDR o ARN; una clave desconocida es `invalid` con `inputs.<nombre>`). La web arma el formulario con esto. La respuesta incluye `"runner_ready": bool` y, si es `false`, `"runner_error"` con qué falta (tofu en el PATH o `RAMPA_STATE_KEY`).

`InfraRun`: `{"id", "kind": "cluster", "provider": "aws", "module": "aws/eks-cluster", "cloud_account_id", "cluster_id", "name", "operation": "apply|destroy", "attempt": 1, "status", "inputs": {}, "outputs": {}, "error": "", "log_tail": ["..."], "requested_by": User, "created_at", "started_at", "finished_at"}`. Destroy y retry reencolan la misma fila (cambia `operation` y sube `attempt`) para que el estado de OpenTofu sea el mismo. Estados: `queued → initializing → planning → applying → succeeded | failed`, y `destroying → destroyed | failed`. `inputs` nunca incluye valores sensibles (el token del agente se inyecta en el momento de correr y no se persiste en el run).

**Runner.** Un worker en la API toma runs `queued` de a uno por cuenta de nube (lock en la tabla), escribe en un directorio temporal el root module y los módulos embebidos, exporta las credenciales temporales que obtuvo asumiendo el rol de la cuenta (nunca se escriben a disco ni al log), configura el backend `http` y ejecuta el binario `tofu`. El log se guarda por líneas en `infra_run_logs` y se emite como eventos `infra_run.line` para el stream. Timeout por defecto 40 minutos.

**Backend de estado.** `GET|POST|DELETE /v1/tfstate/{run_id}` más `LOCK` y `UNLOCK`, autenticados con `Authorization: Bearer <token del run>` que la API genera por run y solo conoce el runner. El estado se cifra con AES-256-GCM con `RAMPA_STATE_KEY` antes de guardarse en `infra_states`. Un run solo puede leer su propio estado.

**Permisos en la nube.** Crear clusters requiere más permisos que conectar la cuenta. En AWS, el stack `RampaConnect` tiene el parámetro `ManageInfrastructure` (default `true`) que agrega la política de administración de infraestructura (VPC, EKS, IAM acotado a roles `rampa-*` y `<cluster>-*`, RDS, ElastiCache, SQS, S3 y DynamoDB acotados a `rampa-*`). Si la cuenta se conectó sin ese permiso, el run falla en `planning` con el error de AWS y el mensaje sugiere actualizar el stack.

Variables del servidor: `RAMPA_TOFU_BIN` (default `tofu`, buscado en `PATH`; si no está, los endpoints de infra responden `501 not_implemented` con cómo instalarlo), `RAMPA_STATE_KEY` (32 bytes en base64 o hex; requerido para infra), `RAMPA_INFRA_WORKDIR` (default el temp del sistema), `RAMPA_INFRA_TIMEOUT` (default `40m`), `RAMPA_INFRA_CONCURRENCY` (default `2`).

Módulos por proveedor y tipo. `kind` es `cluster` (Kubernetes administrado) o `vm` (una máquina con k3s, que Rampa registra como `Cluster` con `kind: vm`):

| Módulo | kind | Disponible | Qué crea |
|---|---|---|---|
| `aws/eks-cluster` | cluster | sí | VPC, EKS Auto Mode, IRSA, agente |
| `aws/ec2-k3s` | vm | sí | Instancia EC2 en la VPC default con Elastic IP, security group (80, 443, 22 opcional), instance profile para ECR y Secrets Manager, k3s con cifrado de secretos, agente por bootstrap token, cert-manager opcional |
| `digitalocean/droplet-k3s` | vm | sí | Droplet con IP reservada, firewall, k3s, agente por bootstrap token, DOCR configurado para pull, registro DNS comodín opcional, cert-manager opcional |
| `digitalocean/doks-cluster` | cluster | sí | Cluster DOKS con node pool, DOCR integrado, agente por bootstrap token |
| `gcp/gke-cluster`, `azure/aks-cluster`, `oci/oke-cluster`, `oci/compute-k3s`, `ovh/kube-cluster` | | no | Ola dos y tres |

`POST /v1/orgs/{org}/infra/clusters` acepta `"module"` explícito (default: el módulo `cluster` del provider de la cuenta) y los inputs del módulo en `"inputs"`. `domain` es obligatorio para módulos `cluster`; para módulos `vm` es opcional: si viene vacío, al terminar el run la API fija el dominio del cluster en `<ip-con-guiones>.sslip.io` (por ejemplo `1-2-3-4.sslip.io`), que resuelve a la IP pública de la máquina sin configurar DNS, y lo informa en `cluster.infra_ready` (`payload.domain`). `ingress_class` por defecto es `nginx`; en máquinas con k3s conviene `traefik`, que k3s trae instalado. Los `label` y `help` de los inputs de `GET /infra/modules` salen en el idioma del request. Para módulos `vm` el cluster queda con `kind: vm` y `secret_store` según el provider (`kubernetes` en DigitalOcean y OVH, `aws-secrets-manager` en `aws/ec2-k3s`). El runner pasa al bloque `module` solo las variables que el módulo declara en sus `.tf` (y reexporta solo sus outputs), así que un módulo nuevo se suma sin tocar la API. Genera un `bootstrap_token` (un solo uso) y `agent_chart_url` = `<RAMPA_PUBLIC_URL>/public/charts/rampa-agent.tgz`; el módulo instala el agente con `cluster.bootstrapToken`.

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

Kinds (lista cerrada; la web registra un listener por cada uno, así que un kind nuevo se agrega acá y en `web/src/api/types.ts`):, `cluster.infra_ready`, `infra_run.status`. Además `infra_run.line` se emite solo en vivo (no se persiste): son las líneas del log de un run.

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
| `RAMPA_TOFU_BIN` | `tofu` | Binario de OpenTofu para los runs de infraestructura |
| `RAMPA_STATE_KEY` | vacío | Clave AES-256 (32 bytes, base64 o hex) para cifrar el estado de OpenTofu. Sin ella, infra deshabilitada |
| `RAMPA_INFRA_WORKDIR` | temp del sistema | Directorio de trabajo de los runs |
| `RAMPA_INFRA_TIMEOUT` | `40m` | Tiempo máximo de un run |
| `RAMPA_INFRA_CONCURRENCY` | `2` | Runs simultáneos |
| `RAMPA_TRUSTED_PROXIES` | vacío | CIDRs (o IPs) de los proxies inversos delante de la API, separados por coma (por ejemplo `10.0.0.0/8,192.168.1.7`). Solo cuando la conexión viene de uno de ellos se honran `X-Forwarded-For` (el último salto que no es de confianza) y `X-Real-IP` para resolver la IP del cliente; desde cualquier otra dirección esos headers se ignoran. Vacío: la IP del cliente es siempre la del socket. Es la IP que usa el límite de `POST /v1/agent/bootstrap`, así que detrás de un proxy hay que configurarla o todos los agentes comparten un solo bucket |
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
