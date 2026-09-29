# Rampa Web

Panel web de Rampa: una SPA en React 19 + TypeScript + Vite 8 + Tailwind v4 que habla con la API REST descripta en `docs/api.md`. Todo lo que hace la web se puede hacer con `curl`; la web no tiene lógica propia de negocio, solo presenta y orquesta llamadas.

## Correr en desarrollo

Requisitos: Node 22 y la API corriendo en `:8080`.

```sh
# Terminal 1: la API en modo dev (habilita POST /v1/auth/dev-login)
export PATH="$HOME/.local/bin:$PATH"
RAMPA_DEV_LOGIN=true RAMPA_DATABASE_URL=postgres://... go run ./cmd/rampa-api serve --dev

# Terminal 2: la web
cd web
npm install
npm run dev          # http://localhost:5173
```

Vite proxea `/v1` y `/public` a `http://localhost:8080` (configurable con `RAMPA_API_URL`), así la cookie `rampa_session` es same-origin y no hay que pelear con CORS ni con `SameSite`. En `/login` podés entrar con cualquier email (dev-login) o con el botón OIDC, que navega a `/v1/auth/oidc/start`.

Otros comandos:

```sh
npm run typecheck    # tsc -b, estricto
npm test             # vitest: cliente HTTP, filtros de eventos, formato, slug
npm run build        # tsc -b && vite build -> dist/
npm run preview      # sirve dist/ localmente
```

Para producción, `dist/` se sirve desde la API con `RAMPA_WEB_DIR=web/dist` (fallback a `index.html` para las rutas del router) o desde un CDN delante de la misma API.

## Rutas

| Ruta | Qué muestra |
|---|---|
| `/login` | Dev-login por email y botón OIDC. Si ya hay sesión redirige a `/`. |
| `/` | Redirige a la primera organización del usuario o a `/orgs/new`. |
| `/orgs/new` | Alta de organización (slug derivado del nombre, validado con la regex de la API). |
| `/o/:org` | Resumen: clusters con estado, últimos deployments (el actual de cada scope), actividad en vivo. |
| `/o/:org/cloud-accounts` | Lista de cuentas y asistente de conexión en tres pasos: proveedor (los no disponibles salen como "Próximamente"), instrucciones con link o comando y campos a completar (PATCH), verificación con resultado. |
| `/o/:org/clusters` | Lista con estado online/offline, versión de agente y de Kubernetes. Crear cluster muestra el token y el `install_command` una sola vez, con copiar y advertencia. Rotar token y borrar. |
| `/o/:org/apps` | Catálogo. Importar pegando el `rampa.yaml` y la URL del repo; los `details` de validación se muestran en el formulario. |
| `/o/:org/apps/:app` | Detalle: metadata, scopes con estado, URL, imagen actual y cluster; Desplegar (diálogo con imagen y confirmación), Atar cluster si está sin atar, Eliminar scope; historial de deployments con Aprobar/Cancelar cuando corresponde; timeline de eventos en vivo filtrado por la app; editar manifest; eliminar app. |
| `/o/:org/settings` | Datos de la org, miembros (agregar por email con rol, cambiar rol, quitar) y tokens de API (crear mostrando el secreto una sola vez, borrar). |

## Estructura

```
src/
  main.tsx               QueryClient y montaje
  App.tsx                Router (react-router v7, createBrowserRouter)
  index.css              @import "tailwindcss" y tema (neutros slate + acento verde)
  api/
    types.ts             Tipos de todas las entidades de docs/api.md
    client.ts            fetch tipado, ApiError, helpers por recurso, openEvents (SSE)
    hooks.ts             Hooks de TanStack Query por recurso y claves centralizadas
  auth/AuthContext.tsx   RequireAuth (GET /v1/me, 401 -> /login), useAuth, useOrgRole
  hooks/useOrgEvents.tsx OrgEventsProvider: un stream SSE por org, buffer de eventos, invalidación
  layout/AppLayout.tsx   Barra lateral, selector de org, usuario, logout, estado del stream
  pages/                 Una pantalla por ruta
  components/ui/         Button, Input, Textarea, Select, Field, Card, Badge, Dialog, Table, CopyField, States
  components/            StatusBadge, EventList
  lib/                   format (fechas, imágenes), status (tonos y etiquetas), events (filtros), slug, cn
```

## Decisiones

- **Sin kits de UI.** Tailwind v4 y componentes propios chicos. `Dialog` usa el elemento `<dialog>` nativo (foco, Escape y backdrop gratis). Los diálogos que muestran un secreto son `persistent`: no se cierran con Escape ni clic afuera, y piden marcar "ya lo copié".
- **Un solo stream SSE por organización**, abierto en el layout. Cada evento se guarda en un buffer acotado (300) e invalida las queries que toca: `cluster.*` refresca clusters, `deployment.*` y `scope.*` refrescan scopes y deployments de la app que viene en `payload.app`, etc. Las pantallas combinan el histórico (`GET /events`) con el buffer en vivo sin duplicados (`mergeEvents`).
- **EventSource con listeners por kind.** El servidor manda `event: <kind>`, y `EventSource` solo entrega eventos con nombre a listeners registrados para ese nombre. `openEvents` registra uno por cada kind documentado (`EVENT_KINDS` en `types.ts`) más `message`. Si la API agrega kinds nuevos hay que sumarlos a esa lista.
- **Errores como `ApiError`** con `status`, `code`, `message` y `details`. Los `details` de validación del `rampa.yaml` se muestran tal cual en el formulario de importación. Errores de red y timeouts (30 s por request, 60 s para verificar cuentas) también se convierten en `ApiError`.
- **Permisos en la UI son solo una ayuda.** Los botones se deshabilitan según el rol del usuario en la org (`useOrgRole`), con un tooltip que explica el rol necesario. La autoridad la tiene la API: un 403 se muestra igual.
- **Estados de deployment con la paleta pedida:** healthy verde, failed rojo, progressing/applying/queued ámbar, awaiting_approval violeta, offline y cancelled gris. `StatusBadge` centraliza el mapeo.
- **Historial de deployments por app.** La API solo lista deployments por scope, así que el detalle de la app consulta cada scope en paralelo (`useQueries`) y combina. Con pocos scopes por app es barato y evita inventar un endpoint que no está en el contrato.
- **Eventos filtrados por app.** Los scopes no tienen id en la API, por lo que sus eventos se asocian a la app por `subject_id` (app o deployment conocido) o por `payload.app` / `payload.application_id`. Si el servidor no incluye el nombre de la app en el payload de eventos de scope, esos eventos no aparecen en la timeline de la app (sí en el resumen de la org).
- **Dashboard sin endpoint global de deployments.** "Últimos deployments" se arma con el `current_deployment` de cada scope de cada app. Es N+1 pero acotado para la Fase A.

## Fuera de alcance de la Fase A

- Parámetros y secretos desde la web, logs en vivo, services (Fases B y C).
- Cambiar `settings.require_approval_prod` desde la web: no hay endpoint en `docs/api.md`.
- Paginación de eventos con `before` (la UI muestra los últimos 100 más los que llegan en vivo).
- Tests de componentes con DOM: los tests actuales cubren la lógica pura (cliente HTTP, SSE, filtros, formato).
