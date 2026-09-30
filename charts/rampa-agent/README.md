# rampa-agent

Chart Helm del agente de Rampa. Es lo primero que un cliente instala en su cluster: un Deployment de una réplica que abre una conexión saliente al plano de control y aplica lo que la API le manda. La guía completa para el cliente está en [docs/instalar-agente.md](../../docs/instalar-agente.md).

## Qué instala

| Recurso | Condición | Para qué |
|---|---|---|
| ServiceAccount | `serviceAccount.create` | Identidad del agente. Acá van las anotaciones de IRSA / Workload Identity. |
| Secret `<release>-token` | `cluster.token` | El token del cluster, clave `token`. |
| Secret `<release>-bootstrap` | `cluster.bootstrapToken` | El bootstrap token de un solo uso, clave `bootstrap_token`. El agente lo canjea al arrancar y guarda el token real en el Secret `rampa-agent-token`. |
| Role + RoleBinding `<release>-token` | `rbac.create` y `cluster.bootstrapToken` | En el namespace del release: `get, list, update, patch` sobre `secrets` acotado por `resourceNames` a `rampa-agent-token`, más `create` sin acotar (RBAC no aplica `resourceNames` a `create`). |
| ClusterRole + ClusterRoleBinding | `rbac.create` | Permisos del agente, un comentario por bloque en `templates/clusterrole.yaml`. Con `rbac.appSecrets` (default `true`) incluye `get, create, update, patch` sobre `secrets` para el Secret `<app>-params` de los clusters con `secret_store: kubernetes`. |
| Deployment | siempre | 1 réplica, strategy Recreate, securityContext estricto, probes en `:8081/healthz` y `/readyz`. Inyecta `RAMPA_NAMESPACE` por downward API y `RAMPA_CLUSTER_TOKEN` o `RAMPA_BOOTSTRAP_TOKEN` según la fuente del token. |
| ClusterSecretStore | `secretStore.enabled` | Hacia Secrets Manager, GCP Secret Manager o Key Vault, con auth por `serviceAccountRef`. Requiere External Secrets Operator. |

## Instalar

```bash
helm upgrade --install rampa-agent oci://ghcr.io/CHANGEME/charts/rampa-agent \
  --namespace rampa-system --create-namespace \
  --set api.url=https://api.rampa.dev \
  --set cluster.token=rampa_cluster_...
```

`api.url` y **exactamente uno** de `cluster.token`, `cluster.existingSecret` o `cluster.bootstrapToken` son obligatorios; `values.schema.json` (`oneOf`) y los templates lo exigen con mensaje claro.

### Con bootstrap token

Para cloud-init de VMs, scripts de instalación y módulos de OpenTofu, donde el token real no debe quedar escrito:

```bash
helm upgrade --install rampa-agent oci://ghcr.io/CHANGEME/charts/rampa-agent \
  --namespace rampa-system --create-namespace \
  --set api.url=https://api.rampa.dev \
  --set-string cluster.bootstrapToken=rampa_bootstrap_...
```

Flujo: el chart guarda el bootstrap token en el Secret `<release>-bootstrap` y lo inyecta como `RAMPA_BOOTSTRAP_TOKEN`. El agente, al arrancar, si no tiene `RAMPA_CLUSTER_TOKEN` lee el Secret `rampa-agent-token` de su namespace; si no existe, hace `POST <api.url>/v1/agent/bootstrap` (con reintentos hasta 10 minutos, por si la API todavía no responde), guarda el `cluster_token` en ese Secret y arranca. Un segundo canje devuelve `401`: el agente lo loguea y se queda leyendo el Secret, por si otro proceso lo canjeó. `NOTES.txt` explica qué hacer si el bootstrap venció, cómo borrar el Secret de bootstrap después y cómo pasar a `cluster.existingSecret=rampa-agent-token` como estado estable.

## Probar el chart

```bash
export PATH="$HOME/.local/bin:$PATH"
for f in charts/rampa-agent/ci/*.yaml; do
  helm lint --strict charts/rampa-agent -f "$f"
  helm template rampa-agent charts/rampa-agent -n rampa-system -f "$f" > /dev/null
done
```

Los values de `ci/` cubren: mínimo, `existingSecret` + IRSA, `bootstrapToken`, `secretStore` para aws, gcp y azure, y `rbac.create=false`.

## Decisiones

- **Una réplica, Recreate.** El plano de control identifica al cluster por su token; dos agentes con el mismo token se pisarían. El schema rechaza `replicaCount != 1`.
- **El token nunca va en args ni en anotaciones legibles.** Solo en el Secret y como `secretKeyRef`. Las anotaciones `rampa.dev/token-hash` y `rampa.dev/bootstrap-hash` del pod son hashes truncados: sirven para reiniciar el pod al rotar.
- **Exactamente una fuente de token.** `token`, `existingSecret` y `bootstrapToken` son excluyentes para que no haya dudas de cuál manda: el agente tiene su propio orden de prioridad (env, Secret, bootstrap) y mezclar fuentes en el chart lo volvería opaco.
- **El Secret del bootstrap es `optional` en el pod.** Después del primer arranque el bootstrap ya se canjeó y el Secret se puede borrar sin que el pod deje de arrancar.
- **Role acotado por `resourceNames` en el namespace del release.** El agente solo puede leer y actualizar `rampa-agent-token`. `create` va en una regla aparte sin `resourceNames` porque RBAC autoriza el `create` antes de conocer el nombre del objeto; sigue limitado al namespace del agente. El Role solo se renderiza con `bootstrapToken`: con `token` o `existingSecret` el agente recibe el token por variable de entorno y no toca Secrets de su namespace.
- **`secrets` en el ClusterRole, sin `list`, `watch` ni `delete`.** El comando `write_secret` con `target: kubernetes` crea o actualiza `<app>-params` en el namespace del scope. El agente exige la label `rampa.dev/app` en el namespace, se niega a tocar namespaces del sistema y no pisa Secrets administrados por otro. `rbac.appSecrets=false` quita la regla en clusters que usan ESO contra un gestor de nube.
- **`automountServiceAccountToken: false` en el SA y `true` en el pod.** El agente necesita in-cluster config; otros pods que usen el mismo SA por error no.
- **`/tmp` en emptyDir** porque el rootfs es de solo lectura.
- **`healthPort`** existe para el caso en que el agente lo cambie por env (`RAMPA_HEALTH_ADDR`); el default 8081 es el que expone el agente.

## Fuera de alcance

- Instalar External Secrets Operator o Argo Rollouts: son prerequisitos del cliente.
- NetworkPolicy de egreso: depende del CNI del cliente. La guía documenta qué salida necesita el agente.
- PodMonitor / métricas: el agente todavía no expone `/metrics`.
