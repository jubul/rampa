# CLI `rampa`

Referencia del CLI. Es un cliente de la API (`docs/api.md`) que además funciona sin conexión para lo que no la necesita: `init`, `validate`, `render`, `scope url` y `scope list` en modo offline. El código vive en `cmd/rampa` (main y golden paths embebidos) e `internal/cli` (comandos, `config`, `apiclient`).

```bash
make install      # compila a ~/.local/bin/rampa
rampa --help
```

## Sesión y configuración

`rampa login` guarda la sesión en `~/.config/rampa/config.yaml` con permisos `0600`:

```yaml
api: https://api.rampa.dev
token: rampa_xxxxxxxx
org: acme
```

Las variables de entorno **ganan** sobre el archivo. Son las que se usan en CI, donde no hay login:

| Variable | Qué es |
|---|---|
| `RAMPA_API` | URL de la API, sin `/v1` (ej. `https://api.rampa.dev`) |
| `RAMPA_TOKEN` | Token de API (`rampa_...`), creado en la web o con `POST /v1/orgs/{org}/tokens` |
| `RAMPA_ORG` | Slug de la organización |
| `RAMPA_CONFIG` | Ruta alternativa al archivo de configuración (opcional) |
| `RAMPA_DEBUG` | `1` para ver logs de depuración en stderr |

Todo comando que habla con la API acepta `--org SLUG` para pisar la organización por ese comando. Si no hay sesión (ni archivo ni variables), los comandos que la necesitan fallan con un mensaje que explica `rampa login`.

Los requests normales tienen timeout de 30 segundos. El stream de eventos de `deploy --wait` lo corta el `--timeout`.

Los errores de la API se muestran con el `code` y el `message` del cuerpo `{"error": {...}}`, y los `details` de validación uno por línea:

```
error: creando el deployment de checkout-api/prod en acme: forbidden: desplegar a prod requiere rol admin
```

## Comandos

### `rampa login`

Valida el token con `GET /v1/me`, guarda la configuración e imprime el usuario y sus organizaciones con rol. Nunca imprime el token completo.

```bash
rampa login --api https://api.rampa.dev --token rampa_xxx
rampa login --api https://api.rampa.dev --token rampa_xxx --org acme
```

- Con `--org` verifica que el token tenga acceso a esa organización.
- Sin `--org`, si el token pertenece a una sola organización la deja como default; si tiene varias, lo avisa y hay que repetir con `--org` o pasarlo por comando.

### `rampa init`

Crea una application desde un golden path (código mínimo, Dockerfile, `rampa.yaml`, workflows de CI). No necesita sesión.

```bash
rampa init --name checkout-api --team payments
rampa init --name billing-api --team payments --type api --runtime go --port 9000
```

Flags: `--name`, `--team` (obligatorios), `--type`, `--runtime`, `--port`, `--dir`, `--github-org` (o `RAMPA_GITHUB_ORG`). Hoy el único golden path que existe es `api-go` (`--type api --runtime go`, los defaults); cualquier otra combinación falla con la lista de las disponibles.

### `rampa validate`

Valida el `rampa.yaml` con `pkg/manifest`. Exit 1 con la lista de errores si no cierra.

```bash
rampa validate
rampa validate -f examples/checkout-api/rampa.yaml
```

### `rampa render`

Genera los values del chart `rampa-app` para un scope. Sirve para inspeccionar lo que la API va a renderizar o para `helm template` a mano.

```bash
rampa render --scope dev
rampa render --scope prod --image sha-a1b2c3d --registry 123.dkr.ecr.us-east-1.amazonaws.com --domain apps.acme.com \
  | helm template checkout-api charts/rampa-app -f -
```

Flags: `-f`, `--scope`, `--image`, `--platform` (platform.yaml legado), `--registry`, `--domain`.

### `rampa apps import`

Importa el `rampa.yaml` en la organización. Primero lo valida localmente (no manda un manifest roto). Hace `POST /v1/orgs/{org}/apps`; si la application ya existe (`conflict`), hace `PUT /v1/orgs/{org}/apps/{app}/manifest` para reemplazar el manifest.

```bash
rampa apps import
rampa apps import -f rampa.yaml --repo https://github.com/acme/checkout-api
```

### `rampa scope list`

Sin sesión: lista lo declarado en el `rampa.yaml` (scope, namespace, size, replicas, strategy, URL calculada).

Con sesión: consulta `GET /v1/orgs/{org}/apps/{app}/scopes` y agrega las columnas **STATUS** e **IMAGEN** (del `current_deployment`), reemplaza la URL por la real del cluster y agrega los scopes efímeros vivos (`pr-<n>`) que el manifest no nombra. Si la API falla, o hay sesión pero no se resuelve la organización (falta `--org`, `RAMPA_ORG` o org en el login), avisa por stderr y muestra la tabla offline.

```bash
rampa scope list
rampa scope list --offline          # no tocar la API aunque haya sesión
rampa scope list --platform ../rampa-deploys/platform.yaml   # dominios del modo legado
```

```
SCOPE    NAMESPACE             SIZE  REPLICAS  STRATEGY  STATUS   IMAGEN                                                      URL
dev      checkout-api-dev      S     1         rolling   healthy  123.dkr.ecr.us-east-1.amazonaws.com/rampa/checkout-api:sha-0000000  https://checkout-api-dev.apps.acme.com
pr-<n>   checkout-api-pr-<n>   S     1         rolling   -        -                                                           (plantilla de efímeros, ttl 72h)
prod     checkout-api-prod     M     3         canary    unbound  -                                                           https://checkout.acme.com
pr-7     checkout-api-pr-7     S     1         rolling   deploying  ...                                                       https://checkout-api-pr-7.apps.acme.com
```

### `rampa scope url`

Imprime la URL calculada de un scope (offline) y la publica en `GITHUB_OUTPUT` como `url`.

```bash
rampa scope url --scope prod
```

### `rampa scope bind`

Ata un scope a un cluster: `PUT /v1/orgs/{org}/apps/{app}/scopes/{scope}` con `cluster_id`. Es opcional si la organización tiene un solo cluster del env correcto (`prod` para `prod`, `nonprod` para el resto).

```bash
rampa scope bind --scope prod --cluster 7d1c0b6e-...
rampa scope bind --app checkout-api --scope dev --cluster 7d1c0b6e-...
```

### `rampa deploy`

Pide un deployment: `POST /v1/orgs/{org}/apps/{app}/scopes/{scope}/deployments` con `{"image": "...", "message": "..."}`. Imprime el id, el estado inicial, la imagen resuelta, el namespace y la URL del scope.

```bash
rampa deploy --scope dev --image sha-a1b2c3d
rampa deploy --scope prod --image sha-a1b2c3d --wait --timeout 20m
rampa deploy --app checkout-api --scope pr-42 --image 123.dkr.ecr.us-east-1.amazonaws.com/rampa/checkout-api:sha-a1b2c3d --wait
```

| Flag | Qué hace |
|---|---|
| `--scope`, `-s` | Scope destino. Obligatorio. |
| `--image` | Tag (`sha-a1b2c3d`) o referencia completa. Obligatorio. |
| `--app` | Nombre de la app. Default: `metadata.name` del `rampa.yaml` (`-f`). |
| `--message`, `-m` | Mensaje del deployment (CI manda el link al commit). |
| `--wait` | Sigue `GET /v1/orgs/{org}/events/stream?subject_type=deployment&subject_id=<id>` e imprime cada evento. Termina con exit 0 en `deployment.healthy` y exit 1 en `deployment.failed` o `deployment.cancelled`. |
| `--timeout` | Cuánto esperar con `--wait`. Default `15m`. Si vence, exit 1 con el estado actual. |
| `--org` | Organización. |
| `--deploys-repo` | **DEPRECATED.** Modo GitOps legado: escribe `apps/<app>/<scope>/values.yaml` en un checkout de `rampa-deploys` y no habla con la API. Avisa por stderr. |
| `--platform` | **DEPRECATED.** Solo con `--deploys-repo`. |

Cómo funciona `--wait`: antes de abrir el stream consulta el deployment por `GET` (si ya terminó, no hace falta el stream). Mientras el stream está abierto, cada 20 segundos vuelve a consultar por `GET`, así no se pierde un final que la API emitió entre el `POST` y la conexión. Si el servidor cierra el stream sin evento terminal, reconecta hasta agotar el timeout. Ante un error de transporte o un 5xx del stream (reinicio de la API, 502 del ingress) también reconecta, con backoff de 1s a 30s, y consulta el deployment por `GET` antes de cada reintento. Solo aborta con 401, 403 o 404. Si vence el timeout, el estado que se informa (y el output `status`) sale de un `GET` final, no del `POST` inicial.

Si el scope es `prod` y la organización exige aprobación, el deployment queda en `awaiting_approval`; `--wait` sigue esperando a que un admin lo apruebe (desde la web o con `POST /v1/orgs/{org}/deployments/{id}/approve`).

Outputs en `GITHUB_OUTPUT` (si la variable existe): `deployment_id`, `status`, `namespace`, `url`, `image`.

### `rampa undeploy`

Elimina un scope: `DELETE /v1/orgs/{org}/apps/{app}/scopes/{scope}`. La API encola un `undeploy` al agente, que borra el namespace `<app>-<scope>`. Si la API responde `not_found`, el comando termina bien (idempotente, útil al cerrar PRs).

```bash
rampa undeploy --scope pr-142
rampa undeploy --app checkout-api --scope pr-142
rampa undeploy --scope staging --force       # scopes permanentes: --force acá y rol admin en la API
```

`--deploys-repo` mantiene el modo legado (borra el directorio en `rampa-deploys`), marcado deprecated.

Outputs en `GITHUB_OUTPUT`: `namespace`, `deleted` (`true` | `false`).

## Uso desde CI

Los workflows reusables de este repo ya lo hacen. Una app generada con `rampa init` los llama con `secrets: inherit`:

```yaml
deploy-dev:
  uses: acme/rampa/.github/workflows/reusable-deploy.yml@main
  with:
    app: checkout-api
    scope: dev
    image: ${{ needs.build.outputs.image }}
  secrets: inherit
```

Lo que hay que configurar una vez en la organización de GitHub (o en cada repo):

| Dónde | Nombre | Valor |
|---|---|---|
| Variables | `RAMPA_API_URL` | `https://api.rampa.dev` |
| Variables | `RAMPA_ORG` | slug de la organización en Rampa |
| Secrets | `RAMPA_TOKEN` | token de API con rol `developer` (o `admin` si el repo despliega a `prod`) |
| Secrets | `RAMPA_PLATFORM_TOKEN` (opcional) | token con lectura sobre el repo de la plataforma. Necesario si `rampa` es privado y está en otra organización que la app; sin él el checkout usa `github.token` |

`reusable-deploy.yml` hace checkout de la app y de la plataforma, compila el CLI con `go build ./cmd/rampa`, y corre:

```bash
RAMPA_API=$RAMPA_API_URL RAMPA_ORG=$RAMPA_ORG RAMPA_TOKEN=$RAMPA_TOKEN \
rampa deploy --app "$APP" --scope "$SCOPE" --image "$IMAGE" --wait --timeout 15m -m "<link al commit>"
```

Para scopes `pr-*` comenta la URL en el PR de la app. Inputs opcionales: `wait` (default `true`), `timeout` (default `15m`), `platform-repo`, `platform-ref`. Outputs: `url`, `namespace`, `deployment_id`, `status`.

`reusable-undeploy.yml` compila el CLI y corre `rampa undeploy --app --scope`. Lo usa el workflow `pr-cleanup.yml` de cada app al cerrar un PR.

Para correr el CLI a mano en cualquier otro CI, alcanza con exportar las tres variables y llamar a los comandos: no hace falta `rampa login`.

## Códigos de salida

| Código | Cuándo |
|---|---|
| 0 | El comando terminó bien. Con `deploy --wait`, el deployment quedó `healthy`. |
| 1 | Cualquier error: validación, API (`unauthorized`, `forbidden`, `not_found`, `invalid`, `conflict`), `deploy --wait` en `failed` o `cancelled`, o timeout. |

## Fuera de alcance por ahora

- `rampa logs` (tail de pods) y `rampa deployments list/approve`: la API los expone pero el CLI todavía no.
- `rampa undeploy --wait` (esperar a que el namespace desaparezca): la API no emite hoy un evento terminal de undeploy que el CLI pueda seguir.
- Rotación o borrado de tokens desde el CLI: se hace desde la web.
