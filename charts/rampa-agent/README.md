# rampa-agent

Chart Helm del agente de Rampa. Es lo primero que un cliente instala en su cluster: un Deployment de una réplica que abre una conexión saliente al plano de control y aplica lo que la API le manda. La guía completa para el cliente está en [docs/instalar-agente.md](../../docs/instalar-agente.md).

## Qué instala

| Recurso | Condición | Para qué |
|---|---|---|
| ServiceAccount | `serviceAccount.create` | Identidad del agente. Acá van las anotaciones de IRSA / Workload Identity. |
| Secret `<release>-token` | si no hay `cluster.existingSecret` | El token del cluster, clave `token`. |
| ClusterRole + ClusterRoleBinding | `rbac.create` | Permisos del agente, un comentario por bloque en `templates/clusterrole.yaml`. |
| Deployment | siempre | 1 réplica, strategy Recreate, securityContext estricto, probes en `:8081/healthz` y `/readyz`. |
| ClusterSecretStore | `secretStore.enabled` | Hacia Secrets Manager, GCP Secret Manager o Key Vault, con auth por `serviceAccountRef`. Requiere External Secrets Operator. |

## Instalar

```bash
helm upgrade --install rampa-agent oci://ghcr.io/CHANGEME/charts/rampa-agent \
  --namespace rampa-system --create-namespace \
  --set api.url=https://api.rampa.dev \
  --set cluster.token=rampa_cluster_...
```

`api.url` y uno de `cluster.token` o `cluster.existingSecret` son obligatorios; `values.schema.json` y los templates lo exigen con mensaje claro.

## Probar el chart

```bash
export PATH="$HOME/.local/bin:$PATH"
for f in charts/rampa-agent/ci/*.yaml; do
  helm lint --strict charts/rampa-agent -f "$f"
  helm template rampa-agent charts/rampa-agent -n rampa-system -f "$f" > /dev/null
done
```

Los values de `ci/` cubren: mínimo, `existingSecret` + IRSA, `secretStore` para aws, gcp y azure, y `rbac.create=false`.

## Decisiones

- **Una réplica, Recreate.** El plano de control identifica al cluster por su token; dos agentes con el mismo token se pisarían. El schema rechaza `replicaCount != 1`.
- **El token nunca va en args ni en anotaciones legibles.** Solo en el Secret y como `secretKeyRef`. La anotación `rampa.dev/token-hash` del pod es un hash truncado, sirve para reiniciar el pod al rotar.
- **`automountServiceAccountToken: false` en el SA y `true` en el pod.** El agente necesita in-cluster config; otros pods que usen el mismo SA por error no.
- **`/tmp` en emptyDir** porque el rootfs es de solo lectura.
- **`healthPort`** existe para el caso en que el agente lo cambie por env (`RAMPA_HEALTH_ADDR`); el default 8081 es el que expone el agente.

## Fuera de alcance

- Instalar External Secrets Operator o Argo Rollouts: son prerequisitos del cliente.
- NetworkPolicy de egreso: depende del CNI del cliente. La guía documenta qué salida necesita el agente.
- PodMonitor / métricas: el agente todavía no expone `/metrics`.
