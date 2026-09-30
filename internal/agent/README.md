# Agente de Rampa (`cmd/rampa-agent`, `internal/agent`)

El agente corre en el cluster del cliente y es el brazo del plano de control. Abre una conexión WebSocket **saliente** a la API, recibe comandos ya renderizados (objetos de Kubernetes en JSON) y los ejecuta con la identidad de su ServiceAccount. No conoce el chart ni el `rampa.yaml`: es un aplicador con memoria corta y sin puertos abiertos hacia afuera.

Implementa, del lado agente, el protocolo de `pkg/protocol` tal cual está definido.

Al arrancar resuelve su token del cluster en este orden: `RAMPA_CLUSTER_TOKEN`; el Secret `<RAMPA_NAMESPACE>/rampa-agent-token`; el canje de `RAMPA_BOOTSTRAP_TOKEN` contra `POST /v1/agent/bootstrap` (ver [Bootstrap](#bootstrap-del-token)).

## Paquetes

| Paquete | Qué hace |
|---|---|
| `internal/agent/client` | Conexión a `RAMPA_API_URL + /v1/agent/connect` con `Authorization: Bearer <token>`. Manda `hello`, espera `welcome`, manda `heartbeat` cada `heartbeat_seconds` (más un ping de WebSocket para detectar conexiones muertas). Cada `command` recibe `ack` inmediato y se ejecuta en su propia goroutine: **un comando a la vez por namespace** (deploy y undeploy), en paralelo entre namespaces; `status` y `logs` son de solo lectura y no esperan. Reconecta para siempre con backoff exponencial con jitter entre 1s y 60s. Un comando desconocido o con payload incompleto recibe `result` con `rejected`. Si la conexión se cae a mitad de un deploy, el deploy sigue y el `result` se guarda y se entrega al reconectar (cola acotada a 1000). Al reconectar, el `hello` lleva en `in_flight` los ids de los comandos que siguen ejecutándose. Comandos repetidos mientras siguen en curso reciben `ack` pero no se ejecutan dos veces; comandos repetidos que ya terminaron reciben otra vez su `result` sin ejecutarse (registro acotado a 1000 entradas o 1 hora). Al cancelar el contexto de `Run` entra en apagado: ver [Apagado](#apagado). |
| `internal/agent/kube` | Cliente de Kubernetes con config in-cluster o `RAMPA_KUBECONFIG`. Usa `client-go/dynamic` más un `RESTMapper` con discovery cacheado. Operaciones: `Deploy`, `Undeploy`, `Status`, `Logs`, `WriteSecret` (Secret `<app>-params`, target `kubernetes`). |
| `internal/agent/cloud` | Escribe secretos de aplicación en el gestor de secretos de la nube con la identidad de workload del agente (target `cloud`). Hoy AWS Secrets Manager: `rampa/<app>/<scope>/<CLAVE>`, `CreateSecret` con tags `rampa:app` y `rampa:scope` o `PutSecretValue` si ya existe, cliente armado bajo demanda con la cadena de credenciales por defecto del SDK (IRSA, Pod Identity, instance profile) y región de `AWS_REGION` o IMDS. Una interfaz mínima (`SecretsManagerAPI`) permite mockearlo. |
| `internal/agent/bootstrap` | Resuelve el token del cluster al arrancar: env, Secret del namespace o canje del bootstrap token con reintentos y backoff (1s a 30s, hasta 10 minutos). Guarda el token canjeado en el Secret `rampa-agent-token`. Nunca loguea tokens. |
| `internal/agent/executor` | Traduce `protocol.Command` a operaciones de `kube` (y de `cloud` para `write_secret` con target `cloud`) y arma el `Result` (`succeeded`, `failed`, `timeout`, `rejected`). |
| `internal/agent/health` | HTTP en `:8081` (configurable con `RAMPA_HEALTH_ADDR`): `/healthz` responde 200 mientras el proceso viva; `/readyz` responde 200 solo con sesión aceptada por la API (después del `welcome`). El chart `charts/rampa-agent` usa estos endpoints como probes. |
| `cmd/rampa-agent` | `main.go`: config por variables de entorno, `slog` en JSON, servidor de salud arriba antes del bootstrap (para que `/healthz` responda mientras se espera a la API), apagado limpio con SIGINT/SIGTERM (espera hasta `RAMPA_SHUTDOWN_GRACE`, default 30s, a los comandos en curso y entrega sus results antes de cerrar). `Dockerfile` multi-stage a `gcr.io/distroless/static-debian12:nonroot`. |

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

### `write_secret`

Guarda parameters secretos de una app para un scope. Los valores viajan solo en este mensaje (TLS), no se persisten en el plano de control y **no aparecen en ningún log, evento ni result del agente**: el `result` y el evento `SecretWritten` llevan solo la lista de claves. Se serializa con los deploys del mismo namespace para que dos escrituras no se pisen. La capability `write_secret` va en el `hello`.

- **`target: kubernetes`** (clusters con `secret_store: kubernetes`): crea o actualiza el Secret `<app>-params` en `namespace`, tipo `Opaque`, con merge de claves (no borra las que no vienen) y labels `rampa.dev/app`, `rampa.dev/scope` y `app.kubernetes.io/managed-by=rampa`. Guardas: el namespace no puede ser del sistema (`default`, `kube-*`, `rampa-system`) y tiene que llevar la label `rampa.dev/app=<app>`, la misma exigencia que undeploy. Si el namespace todavía no existe y se llama `<app>-<scope>`, se crea con esas labels (cargar secretos antes del primer deploy es un flujo normal; el deploy después completa los labels). No toca Secrets de otro tipo ni administrados por otro (`managed-by` distinto de `rampa`, por ejemplo External Secrets). Conflictos de `resourceVersion` se reintentan. Las claves tienen que ser válidas para un Secret (`[-._a-zA-Z0-9]+`).
- **`target: cloud`** (clusters con gestor de secretos de nube): escribe cada clave en `rampa/<app>/<scope>/<CLAVE>` en AWS Secrets Manager con la identidad de workload del agente. Si la identidad no alcanza, el `result` dice qué acción (`secretsmanager:CreateSecret` o `PutSecretValue`) faltó sobre qué recurso; sin credenciales o sin región el mensaje dice cómo darlas (IRSA, Pod Identity, instance profile, `AWS_REGION` por `extraEnv`). GCP y Azure quedan pendientes: hoy el `result` es `failed` con un mensaje claro porque el único escritor es el de AWS.

## Bootstrap del token

`internal/agent/bootstrap.Resolve` corre después de conectar con Kubernetes y antes de abrir la sesión con la API:

1. Si hay `RAMPA_CLUSTER_TOKEN`, se usa y no se toca ningún Secret.
2. Si no, lee el Secret `<RAMPA_NAMESPACE>/rampa-agent-token`, clave `token`. Si existe, se usa. Si el API server responde `Forbidden`, falla en el acto con un mensaje que dice qué Role falta: sin permiso de lectura no tiene sentido seguir.
3. Si no existe y hay `RAMPA_BOOTSTRAP_TOKEN`, hace `POST <RAMPA_API_URL>/v1/agent/bootstrap {"bootstrap_token"}`. Con `200` guarda `cluster_token` en ese Secret (crea o actualiza, labels `app.kubernetes.io/managed-by=rampa`) y arranca. Con errores de red o `5xx` reintenta con backoff exponencial con jitter entre 1s y 30s hasta 10 minutos: en una VM el agente suele arrancar antes de que el plano de control esté listo. Con `401` el bootstrap ya se usó o venció: lo loguea y deja de canjear, pero sigue releyendo el Secret hasta el plazo, porque otro proceso (un pod anterior que murió justo después de canjear) pudo haberlo guardado.
4. Si canjeó pero no puede escribir el Secret por permisos, arranca igual con el token en memoria y lo loguea como error: el bootstrap token ya quedó consumido y crashear lo perdería. En el próximo reinicio hará falta otro bootstrap token o crear el Secret a mano.
5. Si no hay ninguna fuente, falla con un mensaje que nombra las tres.

El servidor de salud arranca antes del bootstrap: `/healthz` responde mientras se espera a la API y `/readyz` sigue en 503, así el `startupProbe` no mata el pod. Los tokens no se loguean nunca; los tests lo verifican sobre la salida del logger.

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
| `secrets` (ClusterRole, `rbac.appSecrets`) | get, create, update, patch | `write_secret` con target `kubernetes`: el Secret `<app>-params` en el namespace del scope. Sin `list`, `watch` ni `delete`. |
| `secrets` (Role en el namespace del agente, solo con `cluster.bootstrapToken`) | get, list, update, patch con `resourceNames: [rampa-agent-token]`; create sin `resourceNames` | Guardar y releer el token canjeado. RBAC no aplica `resourceNames` a `create`, por eso va aparte. Si falta `get`, el agente falla al arrancar con un mensaje que lo dice. |
| discovery (`/version`, `/apis`) | | Versión de Kubernetes para `hello` y `RESTMapper`. Lo tiene cualquier ServiceAccount autenticado (`system:discovery`). |

Server-side apply requiere el verbo `patch`; `create` no hace falta para los objetos aplicados porque el apply crea si no existe.

## Correr fuera del cluster

Sirve para desarrollar contra una API local y un cluster de prueba (kind, minikube, o uno real con tu kubeconfig).

```sh
export PATH="$HOME/.local/bin:$PATH"          # go 1.27
export RAMPA_API_URL=http://localhost:8080     # la API local; http se convierte en ws
export RAMPA_CLUSTER_TOKEN=rampa_cluster_...   # lo devuelve POST /v1/orgs/{org}/clusters una sola vez
# o, en vez del token: export RAMPA_BOOTSTRAP_TOKEN=rampa_bootstrap_... (se canjea y se guarda en el Secret rampa-system/rampa-agent-token del cluster del kubeconfig)
export RAMPA_KUBECONFIG=$HOME/.kube/config     # sin esto intenta la config in-cluster y falla
export RAMPA_LOG_LEVEL=debug
go run ./cmd/rampa-agent
```

En otra terminal: `curl -i localhost:8081/readyz` debe dar 200 en cuanto la API acepte el `hello`. Si la API rechaza el token, el agente lo loguea ("la API rechazó el token del cluster (HTTP 401)") y sigue reintentando: es lo que quiere el protocolo, así rotar el token no requiere reiniciar el pod.

Variables:

| Variable | Default | Qué es |
|---|---|---|
| `RAMPA_API_URL` | requerido | `https://api.rampa.dev` (acepta http, https, ws, wss) |
| `RAMPA_CLUSTER_TOKEN` | vacío | Token del cluster. Si está, manda. Nunca se loguea. |
| `RAMPA_BOOTSTRAP_TOKEN` | vacío | Token de bootstrap de un solo uso (chart: `cluster.bootstrapToken`). Se canjea solo si no hay `RAMPA_CLUSTER_TOKEN` ni Secret. Nunca se loguea. |
| `RAMPA_NAMESPACE` | `rampa-system` | Namespace donde vive el Secret `rampa-agent-token`. El chart lo inyecta por downward API. |
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
- `kube` (`secret_test.go`): `WriteSecret` con `kubernetes/fake`: crear, merge que conserva claves ajenas, guardas de namespace (otra app, sin label, sistema, sin convención), creación del namespace `<app>-<scope>` cuando no existe, rechazo de Secrets con `managed-by` ajeno o tipo no Opaque, reintento en conflicto, validación de claves; y que ningún error lleva valores.
- `cloud`: mock de `SecretsManagerAPI`: `CreateSecret` con tags para claves nuevas y `PutSecretValue` tras `ResourceExistsException`, `AccessDeniedException` con mensaje accionable, errores de credenciales y región, validación, cliente perezoso armado una sola vez, y que los logs no llevan valores.
- `bootstrap`: `httptest` más `kubernetes/fake`: prioridad env > Secret > bootstrap (sin llamadas de más), canje y persistencia (crear y actualizar), reintentos con `503`, `401` que espera a que el Secret aparezca y `401` que vence, API caída que vence, cancelación por contexto, `Forbidden` al leer (error claro) y al escribir (arranca igual), URL del canje, cotas del backoff, y que ningún token aparece en los logs.
- `executor`, `health`, `cmd/rampa-agent`: mapeo de errores a `Result` (incluido `write_secret` por target, sin `Cloud` configurado y con target desconocido, verificando que result y eventos no llevan valores), probes, parseo de config (sin token es válido; `RAMPA_NAMESPACE` y `RAMPA_BOOTSTRAP_TOKEN`).

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
- **Los valores de `write_secret` no salen del comando.** Ni `result`, ni `event`, ni logs los llevan: solo claves. Los mensajes de error de Kubernetes y de AWS no incluyen el contenido del Secret, y los tests lo verifican buscando los valores en todo lo que el agente emite.
- **`write_secret` con target `kubernetes` crea el namespace si falta.** La guarda de undeploy (label `rampa.dev/app`) se mantiene para namespaces existentes; si no existe y se llama `<app>-<scope>`, se crea con la label, porque cargar secretos antes del primer deploy es lo normal y el deploy después completa los labels. Un namespace que existe sin la label se rechaza igual que en undeploy.
- **El bootstrap token se canjea una sola vez y el token real vive en un Secret, no en el chart.** El cloud-init es legible por cualquier proceso de la VM. Tras un `401` el agente no insiste con el canje (no serviría) pero sí con el Secret, por si otro proceso lo canjeó. Si canjea y no puede guardar, arranca igual: perder el token por crashear sería peor que avisar en el log.
- **Fuente única de verdad del nombre del Secret.** `rampa-agent-token` está fijo en `bootstrap.SecretName`, en el `resourceNames` del Role del chart y en la doc. Cambiarlo es cambiar los tres.
- **Dependencias.** `coder/websocket`, `google/uuid`, `client-go`, `aws-sdk-go-v2` (`config` ya estaba; `service/secretsmanager` se agregó para `write_secret` con target `cloud`).

## Fuera de alcance (Fase A)

- Comando `provision` (OpenTofu como Job): el agente lo responde con `rejected` y la API debe tratarlo como no soportado hasta la Fase C.
- Instalar External Secrets Operator o Argo Rollouts: el agente solo detecta que faltan y lo dice en el `result`.
- Reenviar eventos de Kubernetes (`kubectl get events`) al plano de control: hoy se infiere el problema desde el estado de los pods, que cubre los casos frecuentes. Los eventos de `FailedScheduling` (falta de capacidad) quedan para más adelante; mientras tanto se ven como `Progressing` hasta el timeout.
- Métricas Prometheus del agente.
- `write_secret` con target `cloud` hacia GCP Secret Manager y Azure Key Vault: solo AWS por ahora; en esas nubes el `result` es `failed` con un mensaje claro.
- Reintentar el bootstrap cuando el token guardado en el Secret fue rotado en la web: hoy hay que borrar el Secret `rampa-agent-token` y hacer `helm upgrade` con un bootstrap token nuevo (NOTES.txt del chart lo explica).
