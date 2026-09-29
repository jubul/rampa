# Agente de Rampa (`cmd/rampa-agent`, `internal/agent`)

El agente corre en el cluster del cliente y es el brazo del plano de control. Abre una conexión WebSocket **saliente** a la API, recibe comandos ya renderizados (objetos de Kubernetes en JSON) y los ejecuta con la identidad de su ServiceAccount. No conoce el chart ni el `rampa.yaml`: es un aplicador con memoria corta y sin puertos abiertos hacia afuera.

Implementa, del lado agente, el protocolo de `pkg/protocol` tal cual está definido.

## Paquetes

| Paquete | Qué hace |
|---|---|
| `internal/agent/client` | Conexión a `RAMPA_API_URL + /v1/agent/connect` con `Authorization: Bearer <token>`. Manda `hello`, espera `welcome`, manda `heartbeat` cada `heartbeat_seconds` (más un ping de WebSocket para detectar conexiones muertas). Cada `command` recibe `ack` inmediato y se ejecuta en su propia goroutine: **un comando a la vez por namespace** (deploy y undeploy), en paralelo entre namespaces; `status` y `logs` son de solo lectura y no esperan. Reconecta para siempre con backoff exponencial con jitter entre 1s y 60s. Un comando desconocido o con payload incompleto recibe `result` con `rejected`. Si la conexión se cae a mitad de un deploy, el deploy sigue y el `result` se guarda y se entrega al reconectar (cola acotada a 1000). Al reconectar, el `hello` lleva en `in_flight` los ids de los comandos que siguen ejecutándose. Comandos repetidos mientras siguen en curso reciben `ack` pero no se ejecutan dos veces; comandos repetidos que ya terminaron reciben otra vez su `result` sin ejecutarse (registro acotado a 1000 entradas o 1 hora). Al cancelar el contexto de `Run` entra en apagado: ver [Apagado](#apagado). |
| `internal/agent/kube` | Cliente de Kubernetes con config in-cluster o `RAMPA_KUBECONFIG`. Usa `client-go/dynamic` más un `RESTMapper` con discovery cacheado. Operaciones: `Deploy`, `Undeploy`, `Status`, `Logs`. |
| `internal/agent/executor` | Traduce `protocol.Command` a operaciones de `kube` y arma el `Result` (`succeeded`, `failed`, `timeout`, `rejected`). |
| `internal/agent/health` | HTTP en `:8081` (configurable con `RAMPA_HEALTH_ADDR`): `/healthz` responde 200 mientras el proceso viva; `/readyz` responde 200 solo con sesión aceptada por la API (después del `welcome`). El chart `charts/rampa-agent` usa estos endpoints como probes. |
| `cmd/rampa-agent` | `main.go`: config por variables de entorno, `slog` en JSON, apagado limpio con SIGINT/SIGTERM (espera hasta `RAMPA_SHUTDOWN_GRACE`, default 30s, a los comandos en curso y entrega sus results antes de cerrar). `Dockerfile` multi-stage a `gcr.io/distroless/static-debian12:nonroot`. |

## Qué hace cada comando

### `deploy`

0. **Guardas**: se rechaza desplegar en namespaces del sistema (`default`, `kube-*`, `rampa-system`).
1. **Namespace**: si no existe lo crea con los `labels` del comando; si existe, actualiza los labels. Se agregan siempre `rampa.dev/app=<app>` y `rampa.dev/deployment=<deployment_id>` aunque la API no los mande, porque el prune depende de ellos.
2. **Apply**: por cada objeto de `objects` agrega los labels a `metadata.labels` (solo metadata de primer nivel; nunca toca `spec.selector` ni `spec.template`), lo fuerza al namespace del comando y lo aplica con **server-side apply** (`Patch` con `application/apply-patch+yaml`, field manager `rampa`, `force: true`). Objetos de cluster (sin namespace) se rechazan. Se emite un `event` `Applied` por objeto.
3. **Prune**: para cada kind gestionado lista en el namespace con `rampa.dev/app=<app>` y borra los que tienen `rampa.dev/deployment` distinto del actual. Emite `Pruned` por objeto. Kinds gestionados: Deployment, Rollout (argoproj.io/v1alpha1), CronJob, Service, Ingress, HorizontalPodAutoscaler (autoscaling/v2), PodDisruptionBudget, ServiceAccount, ExternalSecret (external-secrets.io/v1), ResourceQuota. Si el cluster no tiene la CRD, ese kind se saltea en prune; si un objeto a aplicar es de un kind que falta, el deploy falla con un mensaje claro ("el cluster no tiene instalado Argo Rollouts, necesario para strategy canary o bluegreen").
4. **Espera**: observa el workload llamado `<app>` (Deployment, Rollout o CronJob, el que exista) cada 2s hasta `healthy`, `failed` o `timeout_seconds` (default 10m).
   - Deployment sano: `status.observedGeneration >= metadata.generation` y `updatedReplicas`, `readyReplicas` y `availableReplicas` iguales a `spec.replicas` (si no está, por autoscaling, se usa `status.replicas`).
   - Rollout: solo se mira `status.phase` cuando `status.observedGeneration >= metadata.generation` (Argo Rollouts lo guarda como string). Reconciliado, `Healthy` es sano y `Degraded` es fallido; mientras el controller no reconcilió la generación nueva, es `progressing` aunque el `phase` viejo diga `Healthy`.
   - CronJob sano en cuanto se aplicó.
   - Mientras espera revisa los pods del `spec.selector` **de la revisión actual**. Para Deployment busca el ReplicaSet cuya annotation `deployment.kubernetes.io/revision` coincide con la del Deployment y filtra por su label `pod-template-hash`; para Rollout usa `status.currentPodHash` (label `rollouts-pod-template-hash`). Si todavía no se puede saber la revisión (el controller no reconcilió o no creó el ReplicaSet nuevo) no se cuentan problemas: los pods de la revisión anterior en `CrashLoopBackOff` son justamente lo que el deploy nuevo viene a arreglar y no deben hacerlo fallar. Si no se pueden listar ReplicaSets (por ejemplo por RBAC), se consideran solo los pods creados desde el inicio del deploy. Si hay contenedores en `ImagePullBackOff`, `ErrImagePull`, `CrashLoopBackOff`, `CreateContainerConfigError` u `OOMKilled` repetido (2 o más reinicios), manda `event` `Unhealthy` con la razón y, tras **3 observaciones consecutivas** con problemas, falla antes del timeout.
   - Un error leyendo el workload (503 del API server, timeout de red) se loguea y se reintenta en el próximo tick; el deploy falla solo tras **5 errores consecutivos**.
   - Eventos: `Applied`, `Pruned`, `Progressing` (con `ready/desired`, solo cuando cambia), `Unhealthy`, y uno final `Healthy` o `failed` con la razón.
5. **Result**: `succeeded` con `WorkloadStatus`; `timeout` si venció el plazo (con el último estado observado); `failed` con el motivo.

### `undeploy`

Borra el namespace (propagación en background). Con `wait: true` espera hasta 5 minutos a que desaparezca. Como es destructivo, el agente se niega a borrar `default`, cualquier `kube-*` y `rampa-system`, exige que `namespace == <app>-<scope>` (la convención de `docs/convenciones.md`) y que el namespace tenga la label `rampa.dev/app=<app>`: solo borra lo que él mismo creó para esa app. Si el namespace ya no existía, es `succeeded`.

### `status`

Devuelve el `WorkloadStatus` actual: `healthy`, `progressing` o `failed` (Rollout degradado o pods en estados fatales).

### `logs`

Pods con `app.kubernetes.io/name=<app>`, contenedor `app` por defecto, `tail_lines`, y con `follow` sigue hasta `duration_seconds` (default 60s). Manda `log` en tandas de hasta 64 líneas o cada 200ms, y termina siempre con un chunk con `eof: true`. Si la conexión con la API se cae, el streaming se corta (no tiene sentido seguir leyendo).

## Apagado

Con SIGINT o SIGTERM (drain de nodo, `helm upgrade` del chart, OOM del nodo) el agente no aborta lo que está haciendo: un deploy a medias dejaría objetos aplicados sin prune y el comando quedaría `acked` sin `result` en la API.

1. Deja de aceptar comandos nuevos: los que lleguen no reciben `ack` y la API los reenvía al agente que conecte después.
2. Mantiene la sesión abierta (y reconecta si se cae) para entregar los `result` de los comandos en curso. El contexto de los ejecutores **no** es el de la señal: siguen corriendo con normalidad.
3. Espera hasta `RAMPA_SHUTDOWN_GRACE` (default 30s) a que terminen y a que sus `result` salgan por la conexión (cola de salida vacía).
4. Si vence el grace, manda `result` `failed` con el mensaje `el agente se apagó durante el comando` por cada comando que siga en curso, cancela los ejecutores y espera unos segundos más a que terminen. El `result` tardío del ejecutor se descarta: un comando termina una sola vez.
5. Recién entonces cierra la conexión y el proceso sale.

El `terminationGracePeriodSeconds` del pod tiene que ser mayor que `RAMPA_SHUTDOWN_GRACE` más unos segundos; si Kubernetes mata el proceso antes, los `result` se pierden y la API los detecta por el `in_flight` del próximo `hello`.

## Permisos que necesita

El ClusterRole está en `charts/rampa-agent/templates/clusterrole.yaml`. Lo que el código usa, verbo por verbo:

| Recurso | Verbos | Para qué |
|---|---|---|
| `namespaces` | get, create, update, delete | Crear el namespace del scope con labels, actualizar labels, borrarlo en undeploy y esperar a que desaparezca. |
| `deployments` (apps), `rollouts` (argoproj.io), `cronjobs` (batch) | get, list, patch, delete | Server-side apply, observar el workload y prune. |
| `replicasets` (apps) | list | Encontrar el ReplicaSet de la revisión actual del Deployment para mirar solo sus pods. Si falta el permiso el agente lo loguea y usa la fecha de creación de los pods como aproximación. |
| `services`, `serviceaccounts`, `resourcequotas` | get, list, patch, delete | Apply y prune. |
| `ingresses` (networking.k8s.io), `horizontalpodautoscalers` (autoscaling), `poddisruptionbudgets` (policy), `externalsecrets` (external-secrets.io) | get, list, patch, delete | Apply y prune. |
| `pods` | list | Detectar pods en `CrashLoopBackOff`, `ImagePullBackOff`, etc. y elegir los pods de `logs`. |
| `pods/log` | get | Comando `logs`. |
| `nodes` | list | Contar nodos para el `heartbeat`. Es informativo: si falta el permiso el agente lo loguea en debug y manda `nodes: 0`. |
| discovery (`/version`, `/apis`) | | Versión de Kubernetes para `hello` y `RESTMapper`. Lo tiene cualquier ServiceAccount autenticado (`system:discovery`). |

Server-side apply requiere el verbo `patch`; `create` no hace falta para los objetos aplicados porque el apply crea si no existe.

## Correr fuera del cluster

Sirve para desarrollar contra una API local y un cluster de prueba (kind, minikube, o uno real con tu kubeconfig).

```sh
export PATH="$HOME/.local/bin:$PATH"          # go 1.27
export RAMPA_API_URL=http://localhost:8080     # la API local; http se convierte en ws
export RAMPA_CLUSTER_TOKEN=rampa_cluster_...   # lo devuelve POST /v1/orgs/{org}/clusters una sola vez
export RAMPA_KUBECONFIG=$HOME/.kube/config     # sin esto intenta la config in-cluster y falla
export RAMPA_LOG_LEVEL=debug
go run ./cmd/rampa-agent
```

En otra terminal: `curl -i localhost:8081/readyz` debe dar 200 en cuanto la API acepte el `hello`. Si la API rechaza el token, el agente lo loguea ("la API rechazó el token del cluster (HTTP 401)") y sigue reintentando: es lo que quiere el protocolo, así rotar el token no requiere reiniciar el pod.

Variables:

| Variable | Default | Qué es |
|---|---|---|
| `RAMPA_API_URL` | requerido | `https://api.rampa.dev` (acepta http, https, ws, wss) |
| `RAMPA_CLUSTER_TOKEN` | requerido | Token del cluster. Nunca se loguea. |
| `RAMPA_KUBECONFIG` | vacío | Ruta a un kubeconfig. Vacío = in-cluster. |
| `RAMPA_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `RAMPA_HEALTH_ADDR` | `:8081` | Dirección de `/healthz` y `/readyz`. Es la que setea el chart. |
| `RAMPA_SHUTDOWN_GRACE` | `30s` | Cuánto esperar a los comandos en curso al recibir SIGTERM antes de darlos por fallidos (formato de `time.ParseDuration`: `30s`, `2m`). |

Imagen:

```sh
docker build -f cmd/rampa-agent/Dockerfile --build-arg VERSION=$(git describe --tags --always) -t ghcr.io/CHANGEME/rampa-agent:dev .
```

## Tests

```sh
go test ./internal/agent/... ./cmd/rampa-agent/
```

- `kube`: `dynamic/fake` con un `RESTMapper` fijo y `kubernetes/fake` para pods. El tracker fake no sabe crear objetos con apply, así que un reactor intercepta el `patch`, verifica que sea `apply` con field manager `rampa` y `force`, registra el objeto y hace upsert conservando `status` y `generation`. Cubre: labels y SSA en dos objetos, prune que borra lo viejo y respeta lo nuevo y lo de otras apps, kinds sin CRD (salteados en prune, error claro al aplicar), espera hasta sano con eventos `Progressing`, fallo rápido con pods en `CrashLoopBackOff` de la revisión actual, pods viejos en `CrashLoopBackOff` que no fallan el deploy nuevo (con ReplicaSet, sin ReplicaSet todavía y sin permiso para listarlos), Rollout con `observedGeneration` atrasada, errores transitorios de lectura tolerados y persistentes que fallan, timeout, CronJob, status, undeploy con espera y rechazos (namespaces del sistema y sin label `rampa.dev/app`), deploy rechazado en namespaces del sistema, logs con `eof` y follow con duración.
- `client`: un `httptest.Server` con `coder/websocket` hace de API. Cubre: hello y welcome, `ack -> event -> event -> result` en orden para un deploy con ejecutor falso, heartbeat, reconexión cuando la API cierra, reintentos con 401, `rejected` para comandos desconocidos, serialización por namespace y paralelismo entre namespaces, entrega del `result` por la segunda conexión, apagado que entrega el `result` de un comando en curso antes de cerrar y no acepta comandos nuevos, grace vencido con `result` `failed` y descarte del result tardío, `in_flight` en el hello de la reconexión, comando repetido ya terminado que reenvía su `result` sin ejecutarse, cota del registro de terminados, encolado bajo el mutex de escritura, cotas del backoff.
- `executor`, `health`, `cmd/rampa-agent`: mapeo de errores a `Result`, probes, parseo de config.

## Decisiones

- **El deploy no muere con la sesión ni con la señal.** El contexto de ejecución es propio del cliente, no el de la conexión ni el de SIGTERM. Si la API se reinicia a mitad de un rollout, el agente sigue observando y entrega el `result` al reconectar; si el pod del agente recibe SIGTERM, espera el grace con la sesión abierta. La alternativa (cancelar y que la API reintente) generaría deploys duplicados sobre un cluster que ya está a mitad de camino.
- **Un comando termina una sola vez.** El registro de comandos terminados (acotado) permite responder a un reenvío de la API con el mismo `result` y descartar el `result` tardío de un ejecutor que fue dado por fallido en el apagado.
- **Solo cuentan los pods de la revisión actual.** Contar pods viejos rotos haría fallar justo el deploy que los arregla. Si no se sabe la revisión, se espera en vez de fallar: preferimos un timeout tarde a un `failed` falso.
- **Serialización por namespace, no global.** Dos deploys al mismo scope en fila se ejecutan en orden; scopes distintos van en paralelo. `status` y `logs` no esperan porque solo leen.
- **Prune estricto.** Si no se puede listar o borrar algo que sobra, el deploy falla: preferimos que la API lo vea a dejar objetos huérfanos en silencio.
- **`rampa.dev/deployment` se garantiza.** Aunque la API se olvide de mandarlo en `labels`, el agente lo agrega: sin él, el prune borraría lo recién aplicado.
- **Guardas en undeploy y deploy.** Borrar un namespace es lo más destructivo que hace el agente. Se exige `namespace == <app>-<scope>`, la label `rampa.dev/app=<app>` en el namespace y se protegen los namespaces del sistema (`default`, `kube-*`, `rampa-system`), que tampoco se aceptan como destino de un deploy. El costo es que si algún día la API nombra namespaces de otra forma, hay que tocar `kube/undeploy.go`.
- **Límite de lectura de 64 MiB por mensaje.** El default de la librería es 32 KiB y un deploy trae todos los objetos renderizados.
- **Un solo escritor.** Todas las escrituras al WebSocket pasan por un mutex; los eventos son best effort (se descartan sin conexión), los `result` se encolan y los `log` cortan el streaming.
- **Sin dependencias nuevas.** Solo `coder/websocket`, `google/uuid` y `client-go`, que ya estaban en `go.mod`.

## Fuera de alcance (Fase A)

- Comando `provision` (OpenTofu como Job): el agente lo responde con `rejected` y la API debe tratarlo como no soportado hasta la Fase C.
- Instalar External Secrets Operator o Argo Rollouts: el agente solo detecta que faltan y lo dice en el `result`.
- Reenviar eventos de Kubernetes (`kubectl get events`) al plano de control: hoy se infiere el problema desde el estado de los pods, que cubre los casos frecuentes. Los eventos de `FailedScheduling` (falta de capacidad) quedan para más adelante; mientras tanto se ven como `Progressing` hasta el timeout.
- Métricas Prometheus del agente.
