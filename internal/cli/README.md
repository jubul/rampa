# internal/cli

Comandos del CLI `rampa` (cobra). El binario está en `cmd/rampa`. Referencia de uso en `docs/cli.md`.

| Archivo | Qué hay |
|---|---|
| `root.go` | Árbol de comandos y `Execute`. |
| `session.go` | Carga de sesión (`config`), construcción del `apiclient`, resolución de app y org, logger `slog`. |
| `login.go` | `rampa login`: valida con `GET /v1/me` y guarda `~/.config/rampa/config.yaml`. |
| `apps.go` | `rampa apps import`: `POST /apps` con fallback a `PUT /manifest` en `conflict`. |
| `deploy.go` | `rampa deploy`: `POST .../deployments`, `--wait` con SSE + polling. Modo legado `--deploys-repo`. |
| `undeploy.go` | `rampa undeploy`: `DELETE .../scopes/{scope}`. Modo legado `--deploys-repo`. |
| `scope.go` | `scope list` (offline, o con STATUS e IMAGEN si hay sesión), `scope url`, `scope bind`. |
| `init.go`, `validate.go`, `render.go` | Offline, sin cambios de fondo. |
| `config/` | Archivo de sesión y variables `RAMPA_API`, `RAMPA_TOKEN`, `RAMPA_ORG`. |
| `apiclient/` | Cliente HTTP tipado (stdlib) para las rutas que usa el CLI, incluido el stream SSE. |

Tests: `cli_test.go` levanta un `httptest.Server` que implementa las rutas de `docs/api.md` y corre los comandos con `HOME` y variables aisladas.

```bash
export PATH="$HOME/.local/bin:$PATH"
go build ./cmd/rampa ./internal/cli/... && go vet ./internal/cli/... ./cmd/rampa/... && go test ./internal/cli/... ./cmd/rampa/...
```

Pendiente (ver también `docs/cli.md`): `rampa logs`, `rampa deployments list/approve`, `undeploy --wait`. El modo GitOps legado (`--deploys-repo`) se mantiene solo para transición y se va a quitar cuando ningún workflow lo use.
