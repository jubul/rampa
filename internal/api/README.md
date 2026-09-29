# internal/api: el plano de control

Implementación de `docs/api.md` para la Fase A. Un binario (`cmd/rampa-api`) que expone la API REST, el stream SSE de eventos, el hub WebSocket de agentes y sirve la SPA.

## Correr en desarrollo

```sh
export PATH="$HOME/.local/bin:$PATH"   # go 1.27
go run ./cmd/rampa-api serve --dev
```

`--dev` hace cuatro cosas:

1. Arranca un Postgres embebido ([fergusstrange/embedded-postgres](https://github.com/fergusstrange/embedded-postgres)) con datos en `~/.local/share/rampa/pg` y puerto `54329`. La primera vez descarga los binarios (~30 MB) a `~/.embedded-postgres-go`.
2. Setea `RAMPA_DEV_LOGIN=true`, así `POST /v1/auth/dev-login {"email": "..."}` abre sesión sin IdP.
3. Setea un `RAMPA_SESSION_SECRET` fijo de desarrollo para que las sesiones sobrevivan reinicios.
4. Setea una `RAMPA_STATE_KEY` fija de desarrollo, así el runner de OpenTofu queda habilitado si hay `tofu` en el PATH (ver más abajo). Nunca uses esa clave contra una cuenta real: es pública.

Cualquier variable `RAMPA_*` que ya esté en el entorno gana sobre los defaults de `--dev`. Sin `--dev`, hacen falta como mínimo `RAMPA_DATABASE_URL` y `RAMPA_SESSION_SECRET` (32+ bytes). La tabla completa está en `docs/api.md`.

Un flujo mínimo con `curl`:

```sh
API=http://localhost:8080
curl -c c.txt -X POST $API/v1/auth/dev-login -d '{"email":"vos@acme.com"}'
curl -b c.txt -X POST $API/v1/orgs -d '{"slug":"acme","name":"Acme"}'
curl -b c.txt -X POST $API/v1/orgs/acme/cloud-accounts -d '{"provider":"aws","name":"acme-nonprod","region":"us-east-1"}'
# ... PATCH con el ARN, POST .../verify, POST /clusters, POST /apps con el rampa.yaml, POST .../deployments
```

Otros subcomandos: `rampa-api migrate` aplica las migraciones pendientes y termina (también acepta `--dev`). `serve` corre las migraciones al arrancar.

### Infraestructura con OpenTofu en local

Los endpoints `/infra` (crear un cluster EKS con clicks) responden `501 not_implemented` hasta que el runner esté listo. Hacen falta dos cosas además de `--dev`:

```sh
export PATH="$HOME/.local/bin:$PATH"                 # tofu 1.12 está ahí; RAMPA_TOFU_BIN=tofu lo busca en el PATH
export RAMPA_STATE_KEY="$(openssl rand -base64 32)"  # clave AES-256 del estado; --dev pone una fija si no la definís
export RAMPA_AWS_TRUSTED_PRINCIPAL_ARN=arn:aws:iam::<cuenta de Rampa>:user/rampa-dev   # identidad con la que corre la API
go run ./cmd/rampa-api serve --dev
```

`GET /v1/orgs/{org}/infra/modules` dice en `runner_ready` si el runner puede ejecutar y en `runner_error` qué falta. El flujo completo:

```sh
# cuenta connected (ver arriba) y después:
curl -b c.txt -X POST $API/v1/orgs/acme/infra/clusters -d '{"cloud_account_id":"...","name":"rampa-nonprod","env":"nonprod","domain":"apps.dev.acme.com","kubernetes_version":"1.33","admin_principal_arns":["arn:aws:iam::123456789012:user/juan"]}'
curl -b c.txt $API/v1/orgs/acme/infra/runs/<id>/log?stream=1     # SSE: line, status, done
curl -b c.txt -X POST $API/v1/orgs/acme/infra/runs/<id>/destroy
```

El runner escribe cada run en `RAMPA_INFRA_WORKDIR` (default el temp del sistema): `main.tf` generado, `rampa.auto.tfvars.json` con los inputs no sensibles, `modules/aws/eks-cluster` desde `terraform/modules` y `charts/rampa-agent` desde `charts/`. Comparte un cache de providers en `<workdir>/rampa-tofu-plugin-cache` para no bajar el provider de AWS en cada apply. El directorio del run se borra al terminar. Variables: `RAMPA_TOFU_BIN`, `RAMPA_STATE_KEY`, `RAMPA_INFRA_WORKDIR`, `RAMPA_INFRA_TIMEOUT` (40m), `RAMPA_INFRA_CONCURRENCY` (2); la tabla completa está en `docs/api.md`.

El backend `http` de estado que usa `tofu` es la propia API (`/v1/tfstate/{run}`), así que `RAMPA_PUBLIC_URL` tiene que ser alcanzable desde donde corre el runner (en `--dev`, `http://localhost:8080` alcanza). Para probar sin AWS, `internal/api/infra/testdata/fake-tofu.sh` es un `tofu` de mentira que imprime líneas, guarda un estado por el backend http y devuelve outputs fijos: `RAMPA_TOFU_BIN=$PWD/internal/api/infra/testdata/fake-tofu.sh`.

## Tests

```sh
go test ./internal/api/...            # todo
go test ./internal/api/server/        # integración con Postgres embebido
RAMPA_TEST_SKIP_DB=1 go test ./internal/api/...   # sin base: salta la integración
```

- **Unitarios sin base**: `config`, `auth` (cookie HMAC, tokens, roles), `events` (fanout y `Broadcast`), `deploy` (render del chart con `examples/checkout-api/rampa.yaml`: dev tiene Deployment, prod tiene Rollout y HPA), `hub` (agente falso por WebSocket con `httptest`: welcome, comando, ack, event, result, reenvío al reconectar, watchdog de heartbeats) y en `infra` el cifrado, el registro de módulos, el root module generado (si hay `tofu` en el PATH, `tofu fmt` lo valida) y la escritura del workdir.
- **Con base** (`infra`, vía `store/storetest`): el backend http de estado (401 sin token o con token de otro run, 404 sin estado, POST/GET ida y vuelta, Basic y Bearer, LOCK/UNLOCK, 423 con otro ID, 409 en POST con ID incorrecto, la fila cifrada no contiene el texto plano) y el runner con el `tofu` falso de `testdata`: estados en orden, log por líneas y en vivo, outputs sin los sensibles, cluster con `agent_role_arn`, token del cluster rotado, credenciales ausentes del log y de los eventos, destroy que borra el cluster, fallo con pista de permisos, retry, recuperación de runs huérfanos y un run por cuenta de nube. Corren con `-race`.
- **Integración** (`server`): levanta un Postgres embebido en un puerto libre y un directorio temporal, una sola vez por proceso, con una base nueva por test. Corre las migraciones y ejercita el flujo completo por HTTP: dev-login, org, cuenta de nube con un provider falso registrado con `provider.Register`, verify, cluster, importar la app de ejemplo, atar el scope, pedir el deployment (queda `queued` y hay un `agent_command`), un agente falso lo lleva a `healthy`, aprobación de prod, cancelación, tokens y roles. Además, `TestTenantIsolation` prueba que un usuario de la org A recibe 404 en todo lo de la org B, y `TestEventStream` verifica el SSE. `features_test.go` cubre el spec (preview con `valid: false`, from-spec, GET y PUT spec), el `scaffold.zip` (lista de archivos, `rampa.yaml` igual al manifest, org de GitHub por query, settings o `CHANGEME`), el onboarding en cada estado y el flujo de infraestructura por HTTP con el `tofu` falso (validaciones, 202, SSE del log, outputs, `install_command` con IRSA, retry, destroy, 501 sin clave o sin `tofu`). Si los binarios de Postgres no se pueden descargar o `RAMPA_TEST_SKIP_DB=1`, se salta con un mensaje claro.

## Layout

| Paquete | Qué hace |
|---|---|
| `config` | Carga y valida las variables `RAMPA_*` con defaults. `Load(getenv)` para testear sin tocar el entorno. |
| `store` | Pool de pgx, migraciones SQL embebidas (`migrations/0001_init.sql`, `0002_infra.sql`) con runner propio (`schema_migrations` y advisory lock), modelos y repositorios por entidad. Todos los métodos reciben `org_id` salvo `users`, la búsqueda de tokens por hash, la de clusters por hash de token, la de runs por hash del token de estado y las operaciones sobre `infra_states` (que van por `run_id`, ya autenticado). `WithTx` ata un `*Store` a una transacción. `store/storetest` levanta el Postgres embebido para los tests de otros paquetes. |
| `auth` | Cookie `rampa_session` firmada con HMAC-SHA256, tokens `rampa_<hex>` y `rampa_cluster_<hex>` guardados como SHA-256, OIDC opcional con go-oidc (discovery perezoso), middlewares `RequireAuth`, `RequireOrg` (membresía o token de la org) y `RequireRole`. |
| `events` | Persistencia (vía `store`) y fanout en memoria para SSE, con filtros por `subject_type` y `subject_id`. |
| `deploy` | Pipeline de render: `render.Build` con los datos del cluster, chart `charts.RampaApp` cargado con el loader de Helm desde el `embed.FS`, `chartutil.ToRenderValues` + `engine.Render`, split de documentos, YAML a JSON, y armado del `protocol.DeployCommand` con labels y timeout. |
| `hub` | Hub de agentes por WebSocket (`coder/websocket`). Protocolo exacto de `pkg/protocol`. `Backend` es la interfaz de persistencia; `StoreBackend` la implementa con `store` y `events`. |
| `infra` | Infraestructura con clicks: registro de módulos (`aws/eks-cluster` disponible; gcp, azure, digitalocean y oci listados como no disponibles), generador del root module, escritura del workdir (módulos de `terraform/` y chart de `charts/` embebidos), worker que toma runs de la cola (uno por cuenta de nube, `RAMPA_INFRA_CONCURRENCY` a la vez), runner que asume el rol de la cuenta y ejecuta `tofu init/plan/apply` o `destroy` con log en vivo, y el backend http de estado cifrado (`/v1/tfstate/{run}`). |
| `httpapi` | Router chi con todas las rutas de `docs/api.md`, handlers, helpers JSON y errores `{"error":{"code","message","details"}}`. Incluye el spec estructurado (`/apps/preview`, `/apps/from-spec`, `/apps/{app}/spec`), el `scaffold.zip` (con los golden paths de `cmd/rampa/templates`, compartidos con el CLI), el onboarding y los endpoints `/infra`. |
| `server` | Cablea todo: abre la base, migra, marca clusters offline, arma bus, hub, pipeline y API; sirve la SPA desde `RAMPA_WEB_DIR` con fallback a `index.html`, `GET /public/cloudformation/rampa-connect.yaml` desde `pkg/provider/aws`, `/healthz`, `/readyz`, `/version`. |

## Decisiones

- **El render se hace antes de persistir.** `POST .../deployments` arma values, renderiza el chart y solo entonces crea el deployment. Si el chart falla, la respuesta es `invalid` y no queda basura. Los `values` se guardan en la fila (jsonb) y el approve re-renderiza desde ahí, así lo aprobado es exactamente lo que se pidió aunque el manifest haya cambiado en el medio.
- **El comando se persiste en `agent_commands` dentro de la misma transacción que el deployment** y después se avisa al hub (`Notify`). Si el agente no está conectado, el comando espera en la cola y se manda al reconectar (queued, y sent sin ack). El `id` del `agent_command` es el `id` del `protocol.Command`.
- **Estados del deployment.** `awaiting_approval | queued | applying | progressing | healthy | failed | cancelled`. `applying`, `progressing`, `healthy` y `failed` los pone el agente vía `event` y `result`; `timeout` y `rejected` se mapean a `failed` con mensaje. Un `result` repetido tras una reconexión se ignora. Un deployment cancelado no vuelve a cambiar aunque el agente reporte.
- **Estados del scope.** `unbound` (declarado sin fila o sin cluster), `idle`, `deploying`, `healthy`, `failed`, `deleting`. Tras un undeploy exitoso los efímeros se borran y los permanentes vuelven a `idle`.
- **Un agente solo afecta su cluster.** Cada update de `agent_commands` filtra por `org_id` y `cluster_id` de la conexión; un `ack`/`event`/`result` sobre un comando ajeno no encuentra fila y se descarta.
- **Online/offline.** `online` al `hello`; `offline` al cerrar la conexión o tras 3 heartbeats sin noticias (watchdog). Al arrancar la API todos los clusters se marcan `offline` porque ninguna conexión sobrevive al reinicio. Una segunda conexión del mismo cluster desplaza a la anterior.
- **404 para quien no es miembro.** Una org donde el principal no tiene membresía (o un token de otra org) devuelve `not_found`, no `forbidden`: la org no existe para él. Los roles insuficientes dentro de la org sí dan `forbidden`.
- **Roles.** Owner > admin > developer > viewer. Prod (desplegar, aprobar, cancelar, borrar scopes permanentes) requiere admin; el resto, developer. Nadie otorga un rol mayor al propio ni toca a alguien con rol superior (un admin no degrada ni saca a un owner), un token no puede ser owner y una org no puede quedar sin owner.
- **Los tokens siguen a su dueño.** El rol efectivo de un token es el menor entre su rol y la membresía actual de quien lo creó (`Authenticator.EffectiveRole`). Sacar a un miembro borra sus tokens en la org en la misma transacción; bajarle el rol acota los tokens con rol superior. El SSE re-chequea el acceso en cada ping.
- **Cerrar un comando es un solo camino.** `StoreBackend.finishCommand` cierra comando, deployment y scope en una transacción y emite después del commit. Lo usan el `result` del agente, la reconciliación del `hello` (los `acked` que no están en `in_flight` se dan por perdidos) y el reaper de `server` (cada minuto, `sent`/`acked` más viejos que `timeout_seconds` + 2 min). `approve` y `cancel` usan transiciones condicionales (`TransitionDeployment`), y `cancel` responde 409 si el agente ya hizo `ack`. Un scope en `deleting` no se pisa con estados de deploy (`SetScopeDeployStatus`).
- **rotate-token y borrar cluster cortan la conexión viva del agente** (`Hub.Disconnect`, close 1008).
- **`PATCH /v1/orgs/{org}`** (admin) para `name` y `settings` es una ruta extra respecto de `docs/api.md`: sin ella `require_approval_prod` no se podría cambiar.
- **`manifest` se guarda como texto.** La arquitectura dice jsonb, pero la API devuelve el YAML original y el CLI lo reimporta; conviene conservarlo tal cual. `manifest_sha` es SHA-256 del texto.
- **`api_tokens.role`** en vez de `scopes`: el contrato de la API asigna un rol al token.
- **El fanout de eventos es por proceso.** Con varias réplicas de la API, un cliente SSE solo ve los eventos que emite su réplica; los eventos persistidos siguen completos en `GET /events`. Para multi-réplica hace falta `LISTEN/NOTIFY` de Postgres (fuera de la Fase A).
- **`RAMPA_AGENT_IMAGE`** va al `install_command` como `--set-string image.repository=<repo> --set-string image.tag=<tag>`, las claves de `charts/rampa-agent/values.yaml` (`--set-string` para que un tag numérico siga siendo string y pase `values.schema.json`). `RAMPA_CHART_REPO` es el chart OCI y `RAMPA_PUBLIC_URL` va en `api.url`.
- **CORS** se habilita solo con `RAMPA_DEV_LOGIN=true`, para orígenes `http://localhost:*` (el dev server de Vite). En producción la SPA se sirve desde la misma API.
- **El spec es otra forma de escribir el manifest.** `POST /apps/from-spec` y `PUT /apps/{app}/spec` pasan por `manifest.FromSpec` + `MarshalManifest` y después por exactamente el mismo camino que un manifest pegado (`createApplication`, `replaceManifest`). Lo que se guarda es el YAML canónico con la cabecera "Generado por Rampa desde la web"; `GET /spec` deriva el spec del manifest guardado, sea pegado o generado. `preview` responde 200 con `valid: false` y los `details` de `FromSpec` (source inválido, scope repetido) más los de `Validate`.
- **El `scaffold.zip` lleva el `rampa.yaml` real** (`MarshalManifest` del manifest guardado, no el texto pegado) y una carpeta raíz con el nombre de la app. La org de GitHub sale de `?github_org`, después de `settings.github_org` y por último `CHANGEME`.
- **Un run de infraestructura es una fila que cambia de operación.** `destroy` y `retry` reencolan el mismo `infra_runs` (con `operation` apply o destroy) en vez de crear otro, así el estado de OpenTofu (`infra_states`, clave `run_id`) sigue siendo el mismo. El log se conserva entre intentos (`attempt` y una línea `== rampa: ... (intento N) ==` los separa). `destroying` cubre tanto "en cola para destruir" como "destruyendo"; `locked_at` distingue si algún worker lo tomó.
- **Nada sensible se persiste del run.** `inputs` no lleva el token del agente: al ejecutar, el runner **rota el token del cluster**, guarda solo el hash y pasa el texto plano por `TF_VAR_agent_token` al subproceso. El token del backend de estado también se genera por ejecución y solo se guarda su SHA-256 (`state_token_hash`). Las credenciales de la cuenta (`provider.CredentialsProvider`, AssumeRole de una hora con external ID y sesión `rampa-infra-<run>`) van únicamente al entorno del subproceso, que arranca con un entorno mínimo (PATH, HOME en el workdir, `TF_*`, `AWS_*`) y no hereda el de la API. `tofu output -json` no va al log y los outputs marcados `sensitive` se descartan.
- **Seguridad del backend de estado.** `/v1/tfstate/{run}` está fuera del grupo de sesión: autentica con el token del run (`Authorization: Bearer`, o Basic con el token como contraseña, que es lo que manda `tofu` con `TF_HTTP_USERNAME`/`TF_HTTP_PASSWORD`), busca el run por el hash y compara en tiempo constante que el id de la ruta sea el suyo: un run solo lee su estado. El estado se cifra con AES-256-GCM (`RAMPA_STATE_KEY`, 32 bytes en base64 o hex), nonce aleatorio por escritura y el id del run como dato autenticado, así un ciphertext no se puede mover de un run a otro. Si se pierde la clave, el estado es irrecuperable: hay que importar los recursos de nuevo. Locks con la semántica del backend http: `LOCK` devuelve 423 con el lock vigente si lo tiene otro ID (200 si es el mismo), `POST` exige `?ID=` del lock cuando está tomado (409 si no coincide) y `UNLOCK` con otro ID es 409.
- **Log en vivo sin inflar `events`.** Cada línea se guarda en `infra_run_logs` y se emite con `Bus.Broadcast` como `infra_run.line` (fanout en memoria, no se persiste). Las transiciones sí se persisten como `infra_run.status`. El stream `GET .../log?stream=1` se suscribe primero, manda lo guardado y deduplica por `seq`; con el estado terminal vacía lo que faltaba y cierra con `event: done`.
- **Recuperación al arrancar.** El worker marca `failed` (con `locked_at` liberado y el cluster en `failed`) los runs que quedaron tomados por un proceso anterior. Con varias réplicas de la API esto pisaría runs vivos de otra réplica; hoy el runner asume una sola réplica ejecutando infra (o `RAMPA_INFRA_CONCURRENCY` mayor en una sola).
- **`clusters.status` admite `failed`** para clusters cuyo run falló: quedan con `infra_run_id` para reintentar o borrar, y el arranque de la API no los pisa a `offline`. `POST /clusters` acepta `agent_role_arn` y el `install_command` agrega la anotación IRSA del ServiceAccount cuando lo hay.
- **Los golden paths viven en `cmd/rampa/templates`** (paquete `templates`, `embed.FS`) y los importan el CLI y la API, así el zip y `rampa init` generan lo mismo.

## Fuera de alcance de la Fase A (documentado, no implementado)

- Expiración automática de scopes efímeros por TTL (`ttl_expires_at` se calcula y guarda; falta el barrido).
- `logs` y `status` del agente (`protocol.KindLog` se acepta y se ignora).
- Tablas `parameters` y `services` existen en la migración pero no tienen endpoints.
- Fanout de eventos entre réplicas (también afecta al log en vivo de los runs de infraestructura: solo lo ve la réplica que ejecuta).
- Paginación en listados (salvo eventos y runs de infraestructura, con `limit`).
- Módulos de infraestructura para gcp, azure, digitalocean y oci: figuran en `/infra/modules` con `available: false`.
- Al rotar el token del cluster durante un apply, la conexión viva de un agente anterior (si la hubiera) no se corta; el helm upgrade reinstala el Secret y el agente reconecta con el nuevo.
