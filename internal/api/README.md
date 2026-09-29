# internal/api: el plano de control

Implementación de `docs/api.md` para la Fase A. Un binario (`cmd/rampa-api`) que expone la API REST, el stream SSE de eventos, el hub WebSocket de agentes y sirve la SPA.

## Correr en desarrollo

```sh
export PATH="$HOME/.local/bin:$PATH"   # go 1.27
go run ./cmd/rampa-api serve --dev
```

`--dev` hace tres cosas:

1. Arranca un Postgres embebido ([fergusstrange/embedded-postgres](https://github.com/fergusstrange/embedded-postgres)) con datos en `~/.local/share/rampa/pg` y puerto `54329`. La primera vez descarga los binarios (~30 MB) a `~/.embedded-postgres-go`.
2. Setea `RAMPA_DEV_LOGIN=true`, así `POST /v1/auth/dev-login {"email": "..."}` abre sesión sin IdP.
3. Setea un `RAMPA_SESSION_SECRET` fijo de desarrollo para que las sesiones sobrevivan reinicios.

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

## Tests

```sh
go test ./internal/api/...            # todo
go test ./internal/api/server/        # integración con Postgres embebido
RAMPA_TEST_SKIP_DB=1 go test ./internal/api/...   # sin base: salta la integración
```

- **Unitarios sin base**: `config`, `auth` (cookie HMAC, tokens, roles), `events` (fanout), `deploy` (render del chart con `examples/checkout-api/rampa.yaml`: dev tiene Deployment, prod tiene Rollout y HPA) y `hub` (agente falso por WebSocket con `httptest`: welcome, comando, ack, event, result, reenvío al reconectar, watchdog de heartbeats).
- **Integración** (`server`): levanta un Postgres embebido en un puerto libre y un directorio temporal, una sola vez por proceso, con una base nueva por test. Corre las migraciones y ejercita el flujo completo por HTTP: dev-login, org, cuenta de nube con un provider falso registrado con `provider.Register`, verify, cluster, importar la app de ejemplo, atar el scope, pedir el deployment (queda `queued` y hay un `agent_command`), un agente falso lo lleva a `healthy`, aprobación de prod, cancelación, tokens y roles. Además, `TestTenantIsolation` prueba que un usuario de la org A recibe 404 en todo lo de la org B, y `TestEventStream` verifica el SSE. Si los binarios de Postgres no se pueden descargar o `RAMPA_TEST_SKIP_DB=1`, se salta con un mensaje claro.

## Layout

| Paquete | Qué hace |
|---|---|
| `config` | Carga y valida las variables `RAMPA_*` con defaults. `Load(getenv)` para testear sin tocar el entorno. |
| `store` | Pool de pgx, migraciones SQL embebidas (`migrations/0001_init.sql`) con runner propio (`schema_migrations` y advisory lock), modelos y repositorios por entidad. Todos los métodos reciben `org_id` salvo `users`, la búsqueda de tokens por hash y la de clusters por hash de token. `WithTx` ata un `*Store` a una transacción. |
| `auth` | Cookie `rampa_session` firmada con HMAC-SHA256, tokens `rampa_<hex>` y `rampa_cluster_<hex>` guardados como SHA-256, OIDC opcional con go-oidc (discovery perezoso), middlewares `RequireAuth`, `RequireOrg` (membresía o token de la org) y `RequireRole`. |
| `events` | Persistencia (vía `store`) y fanout en memoria para SSE, con filtros por `subject_type` y `subject_id`. |
| `deploy` | Pipeline de render: `render.Build` con los datos del cluster, chart `charts.RampaApp` cargado con el loader de Helm desde el `embed.FS`, `chartutil.ToRenderValues` + `engine.Render`, split de documentos, YAML a JSON, y armado del `protocol.DeployCommand` con labels y timeout. |
| `hub` | Hub de agentes por WebSocket (`coder/websocket`). Protocolo exacto de `pkg/protocol`. `Backend` es la interfaz de persistencia; `StoreBackend` la implementa con `store` y `events`. |
| `httpapi` | Router chi con todas las rutas de `docs/api.md`, handlers, helpers JSON y errores `{"error":{"code","message","details"}}`. |
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

## Fuera de alcance de la Fase A (documentado, no implementado)

- Expiración automática de scopes efímeros por TTL (`ttl_expires_at` se calcula y guarda; falta el barrido).
- `logs` y `status` del agente (`protocol.KindLog` se acepta y se ignora).
- Tablas `parameters` y `services` existen en la migración pero no tienen endpoints.
- Fanout de eventos entre réplicas.
- Paginación en listados (salvo eventos, con `limit` y `before`).
