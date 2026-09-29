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

Siempre: ServiceAccount (con anotación IRSA si `aws.roleArn`), ExternalSecret si hay `secrets`, ResourceQuota si el scope es efímero.

Probar localmente:

```bash
rampa render --file examples/checkout-api/rampa.yaml --scope prod --image 1.2.3 > /tmp/values.yaml
helm template checkout-api charts/rampa-app -f /tmp/values.yaml
```
