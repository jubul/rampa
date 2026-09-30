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
npm test             # vitest: cliente HTTP, filtros de eventos, formato, slug, catálogos de idioma, terminología, tema
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
| `/o/:org/cloud-accounts` | Lista de cuentas y asistente de conexión en tres pasos: proveedor (los no disponibles salen como "Próximamente" con su terminología), instrucciones con link o comando y campos a completar (PATCH), verificación con resultado. En proveedores con `auth: token` (DigitalOcean, OCI, OVH) los campos `secret` se piden como password, se mandan en el PATCH y no se vuelven a mostrar; la cuenta muestra el badge "Credenciales guardadas" (`has_credentials`) y un botón para rotarlas (volver a pegar el token). Un 501 al crear muestra el mensaje de la API. |
| `/o/:org/clusters` | Lista con badge de tipo (`kubernetes` o `vm`, con el término del proveedor: "cluster EKS", "Droplet"), estado online/offline, versión de agente y de Kubernetes. Crear con un click lleva a `/infra/new-cluster`; los creados por Rampa enlazan a su run. Registrar cluster existente muestra el token y el `install_command` una sola vez. "Comando de instalación" usa `GET /clusters/{id}/install-command` (un bootstrap token nuevo, de un solo uso, vence en 2 horas; no rota el token del cluster). Rotar token y borrar. |
| `/o/:org/apps` | Catálogo. Importar pegando el `rampa.yaml` y la URL del repo; los `details` de validación se muestran en el formulario. |
| `/o/:org/empezar` | Onboarding: los cinco pasos de `GET /onboarding` como checklist, cada uno con el botón que lleva a hacerlo (conectar nube, crear o registrar cluster, instalar agente, crear app, primer deploy). Se refresca con eventos SSE. El resumen muestra la tarjeta "Te faltan N pasos" mientras no esté completo. |
| `/o/:org/apps/new` | Wizard de aplicación sin YAML (con selector de compute por scope): Básicos, Scopes, Parameters, Services y Revisión (preview con debounce contra `POST /apps/preview`, YAML de solo lectura, errores por paso). Crear hace `POST /apps/from-spec`. |
| `/o/:org/apps/:app/edit` | El mismo wizard precargado con `GET /apps/{app}/spec`, nombre bloqueado, guarda con `PUT /apps/{app}/spec`. |
| `/o/:org/infra` | Ejecuciones de infraestructura (runs) con estado, nombre, módulo, cuenta, quién y cuándo. |
| `/o/:org/infra/new-cluster` | Se elige "Cluster administrado" o "Máquina con k3s" (`kind` cluster o vm de `GET /infra/modules`), la cuenta conectada, y el formulario se arma con los `inputs` del módulo disponible de ese kind para el proveedor de la cuenta (`options` como select, `default` como valor inicial, listas como campo multilínea, números y booleanos; los secretos no aplican). En vm avisa: un solo nodo, sin alta disponibilidad, y el costo aproximado del tamaño elegido si el módulo lo trae en su descripción. Aviso de costo y de tiempo, se deshabilita si `runner_ready` es false. Envía `POST /infra/clusters` con `module` e `inputs` y navega al run. |
| `/o/:org/infra/runs/:id` | Cabecera, log en vivo (SSE `?stream=1`) con autoscroll y pausa, outputs y enlace al cluster al terminar, error, Reintentar y Destruir (con confirmación escribiendo el nombre) si falló. |
| `/o/:org/apps/:app` | Detalle: metadata, scopes con estado, URL, imagen actual y cluster; Desplegar (diálogo con imagen y confirmación), Atar cluster si está sin atar, Eliminar scope; historial de deployments con Aprobar/Cancelar cuando corresponde; tarjeta "Secretos" por scope (lista las claves de `GET .../secrets`, formulario para agregar o reemplazar valores con `PUT .../secrets`, nunca muestra valores, explica el 409 de agente offline; prod solo para admins); badge de `compute` por scope (kubernetes o serverless; Desplegar en serverless avisa que llega en la ola dos); timeline de eventos en vivo filtrado por la app; editar manifest; pestaña YAML de solo lectura; botón Editar (wizard); Descargar proyecto inicial (`scaffold.zip` con la organización de GitHub); eliminar app. |
| `/o/:org/settings` | Datos de la org (incluida la organización de GitHub, `settings.github_org`), miembros (agregar por email con rol, cambiar rol, quitar) y tokens de API (crear mostrando el secreto una sola vez, borrar). |

## Estructura

```
src/
  main.tsx               QueryClient y montaje
  App.tsx                Router (react-router v7, createBrowserRouter)
  index.css              @import "tailwindcss", variante dark por clase y tema (neutros slate + acento verde)
  i18n/                  react-i18next: index.ts (detección, setLocale), es.json y en.json (catálogos), test de paridad
  theme/                 Tema claro / oscuro / sistema: theme.ts (lógica pura), ThemeProvider
  hooks/useTerms.ts      Terminología por proveedor (GET /providers) en el idioma activo
  api/
    types.ts             Tipos de todas las entidades de docs/api.md
    client.ts            fetch tipado, ApiError, helpers por recurso, openEvents (SSE)
    hooks.ts             Hooks de TanStack Query por recurso y claves centralizadas
  auth/AuthContext.tsx   RequireAuth (GET /v1/me, 401 -> /login), useAuth, useOrgRole
  hooks/useOrgEvents.tsx OrgEventsProvider: un stream SSE por org, buffer de eventos, invalidación
  layout/AppLayout.tsx   Barra lateral, selector de org, usuario, logout, estado del stream
  pages/                 Una pantalla por ruta
  components/ui/         Button, Input, Textarea, Select, Field, Card, Badge, Dialog, Table, CopyField, States
  components/            StatusBadge, EventList, Logo, PreferencesControls (idioma y tema), ClusterKindBadge, InstallCommandPanel
  components/appwizard/  Wizard de aplicación (AppWizard y los pasos)
  lib/                   format, status, events, slug, cn, spec (spec por defecto, validaciones, normalización), infra (estados de runs, parser del log, inputs de módulo)
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

## Idiomas

La web está en español (default) y en inglés con `react-i18next`. Los catálogos son `src/i18n/es.json` y `src/i18n/en.json`; toda clave nueva va en los dos (un test verifica que tengan exactamente las mismas claves, que ninguna esté vacía, que usen las mismas variables `{{x}}` y que toda `t("clave")` literal del código exista). Español rioplatense neutro y sin rayas largas; inglés natural, no traducción literal.

- **Detección:** preferencia del usuario (`GET /me` campo `locale`, que se recuerda en `localStorage` como `rampa.locale` para no parpadear) > idioma del navegador > `es`.
- **Selector** en el header: cambia al instante y guarda con `PATCH /v1/me {"locale"}`. En `/login` solo cambia el idioma local.
- **API:** todas las llamadas mandan `Accept-Language` con el idioma activo, así que los mensajes de error y las instrucciones de las cuentas vienen en ese idioma.
- **Fechas y tiempos relativos** usan `Intl` según el idioma (`es-AR` o `en-US`).
- Los helpers que no son componentes (estados, validaciones, formatos) leen el idioma con `i18n.t`; al cambiar de idioma se remonta el árbol del router para que todo se vuelva a renderizar.

## Tema

Claro, oscuro o sistema, con selector en el header y persistencia en `localStorage` (`rampa.theme`). Tailwind 4 con `@custom-variant dark (&:where(.dark, .dark *))`: el tema aplica la clase `dark` en `<html>` (y `color-scheme`), respeta `prefers-color-scheme` en modo sistema y un script inline en `index.html` lo aplica antes de pintar para que no haya flash. Todos los componentes y páginas llevan variantes `dark:` (fondos slate 900/950, no negro puro; el acento verde se usa en 500 sobre oscuro; los badges de estado cambian a fondos translúcidos con texto claro y anillo, así se distinguen en ambos temas). Si cambiás la clave o la lógica del tema, cambiala también en el script de `index.html`.

## Logo

`components/Logo.tsx` muestra la marca de texto "Rampa" con el acento verde. Para usar el logo de la marca, dejá estos archivos en `web/public/`:

- `logo.svg`: para fondos claros.
- `logo-dark.svg`: para fondos oscuros.

El componente prueba cada archivo con `new Image()` (la SPA responde `index.html` a rutas inexistentes, así que un `fetch` daría un falso positivo) y usa el logo si carga; si falta alguno de los dos, en ese tema cae a la marca de texto. Con que exista solo `logo.svg`, el tema claro usa el logo y el oscuro la marca de texto. Alto recomendado: 24 px en el header y 36 px en el login.

## Terminología por proveedor

`GET /providers` devuelve, por cada nube, cómo llama a sus cosas (`terminology`: cuenta, cluster, vm, registry, gestor de secretos, región, con `es` y `en`). La web las usa en todos los textos que las nombran: en DigitalOcean "Crear Droplet con k3s" y "cluster DOKS", en AWS "Crear instancia EC2 con k3s" y "cluster EKS", en Azure "máquina virtual". `useTerms(org)` devuelve los términos de un proveedor en el idioma activo y `useClusterTerms(org)` los del proveedor de la cuenta de un cluster; `lib/terminology.ts` tiene la lógica pura. Sin proveedor en contexto (por ejemplo en los títulos de listas que mezclan nubes) se usan términos genéricos del catálogo (`generic.*`). Los textos del catálogo reciben el término como variable (`{{cluster}}`, `{{vm}}`...) y `cap()` lo capitaliza cuando va al principio de una frase.

## Sin YAML, infraestructura con clicks

- **Un solo objeto `ApplicationSpec`** es el estado del wizard. Antes de enviar, `normalizeSpec` vacía lo que no aplica al tipo (puerto, healthcheck, schedule) o al origen del parameter, y descarta scopes inexistentes en `scopes` de parameters y services. Los errores de la API (`details`) se asignan a un paso por palabra clave (`stepForDetail`).
- **Preview con debounce** (500 ms) sobre el spec normalizado; Crear se habilita solo con preview válido y sin cambios pendientes.
- **Log de un run:** `openInfraLog` abre `EventSource` con `?stream=1`, escucha `line`, `status` y `done`, reintenta con backoff si el stream queda cerrado y no reabre después de `done`. Cada apertura reenvía lo guardado, así que el buffer se descarta al abrir.
- **install_command:** `InstallCommandPanel` lo pide con `GET /clusters/{id}/install-command`, que regenera un bootstrap token de un solo uso (vence en 2 horas) sin rotar el token del cluster. El comando que devuelven crear y rotar sigue mostrándose una sola vez.
- **Enlaces profundos:** `/cloud-accounts?connect=1` abre el asistente y `/clusters?new=1` abre el registro de cluster existente.

## Fuera de alcance de la Fase A

- Cambiar `settings.require_approval_prod` desde la web: no hay endpoint en `docs/api.md`.
- Paginación de eventos con `before` (la UI muestra los últimos 100 más los que llegan en vivo).
- Tests de componentes con DOM: los tests actuales cubren la lógica pura (cliente HTTP, SSE, filtros, formato).
