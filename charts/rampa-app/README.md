# rampa-app

El chart único de Rampa. No se instala a mano: `rampa render` genera los values desde el `rampa.yaml` de la app y Argo CD lo aplica desde `rampa-deploys`.

Qué genera según `app.type` y `strategy`:

| type | strategy | Workload | Service | Ingress | HPA | PDB |
|---|---|---|---|---|---|---|
| api, static | rolling | Deployment | sí | sí | si autoscaling | si réplicas > 1 |
| api, static | canary, bluegreen | Rollout (Argo Rollouts) | sí (+preview en bluegreen) | sí | si autoscaling | si réplicas > 1 |
| worker | rolling | Deployment | no | no | si autoscaling | si réplicas > 1 |
| worker | canary, bluegreen | Rollout | no | no | si autoscaling | si réplicas > 1 |
| cron | | CronJob | no | no | no | no |

Siempre: ServiceAccount (con anotación IRSA si `aws.roleArn`), ResourceQuota si el scope es efímero.

## Parameters secretos (`secrets`)

Los `secrets` del render se inyectan al contenedor con `envFrom` desde el Secret `<app>-params`. De dónde sale ese Secret depende de `cluster.secretStore`, que la API completa con el `secret_store` del cluster:

| `cluster.secretStore` | ExternalSecret | `envFrom` | Quién mantiene `<app>-params` |
|---|---|---|---|
| nombre de un ClusterSecretStore (default `aws-secrets-manager`) | sí, `refreshInterval: 1h`, `creationPolicy: Owner` | `secretRef` obligatorio | External Secrets Operator, desde el gestor de secretos de la nube |
| `kubernetes` | no | `secretRef` con `optional: true` | El agente de Rampa, con el comando `write_secret` que la API manda cuando alguien carga secretos desde la web o la API |

Con `kubernetes` el pod arranca aunque el Secret todavía no exista (por ejemplo, el primer deploy antes de cargar secretos): las variables simplemente no están hasta que llegan. Es el modo de las VMs con k3s y de las nubes sin gestor de secretos (ADR 0004).

## Probar localmente

```bash
rampa render --file examples/checkout-api/rampa.yaml --scope prod --image 1.2.3 > /tmp/values.yaml
helm template checkout-api charts/rampa-app -f /tmp/values.yaml
# Sin ExternalSecret y con envFrom optional:
helm template checkout-api charts/rampa-app -f /tmp/values.yaml --set cluster.secretStore=kubernetes
```
