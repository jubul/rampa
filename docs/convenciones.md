# Convenciones

Nombres y rutas que todas las piezas de Rampa comparten. Cambiar algo acá implica cambiar chart, CLI, workflows y Terraform a la vez.

## Nombres

| Cosa | Convención | Ejemplo |
|---|---|---|
| Application | `[a-z0-9-]`, máximo 40 caracteres | `checkout-api` |
| Scope permanente | `dev`, `staging`, `prod` | |
| Scope efímero | `pr-<número de PR>` | `pr-142` |
| Namespace | `<app>-<scope>` | `checkout-api-pr-142` |
| Recursos de Kubernetes | `<app>` dentro del namespace | Deployment `checkout-api` |
| Application de Argo CD | `<app>-<scope>` | `checkout-api-prod` |
| Repositorio ECR | `rampa/<app>` | `rampa/checkout-api` |
| Tag de imagen | `sha-<7 chars del commit>` | `sha-a1b2c3d` |
| URL por defecto | `<app>-<scope>.<dominio del cluster>` | `checkout-api-dev.apps.example.internal` |

## Labels de Kubernetes

Todos los recursos que genera el chart llevan:

```yaml
rampa.dev/app: checkout-api
rampa.dev/scope: prod
rampa.dev/team: payments
rampa.dev/tier: "2"
rampa.dev/ephemeral: "false"
app.kubernetes.io/name: checkout-api
app.kubernetes.io/instance: checkout-api-prod
app.kubernetes.io/managed-by: rampa
```

Las mismas claves se usan como tags de AWS en los services (`rampa:app`, `rampa:scope`, `rampa:team`) para costos.

## Secretos en AWS Secrets Manager

| Qué | Ruta | Contenido |
|---|---|---|
| Parameter secreto de la app | `rampa/<app>/<scope>/<NOMBRE>` | string plano |
| Credenciales de un service | `rampa/services/<app>/<service>/<scope>` | JSON con claves según el tipo |

Claves del JSON por tipo de service:

- `postgres`: `url`, `host`, `port`, `dbname`, `user`, `password`
- `redis`: `url`, `host`, `port`
- `sqs`: `url`, `arn`
- `s3`: `bucket`, `arn`
- `dynamodb`: `table`, `arn`

## `rampa-deploys`

```
apps/
  <app>/
    <scope>/
      values.yaml      # generado por `rampa render`, más la imagen. No se edita a mano.
argocd/
  applicationset-nonprod.yaml
  applicationset-prod.yaml
```

Reglas:

- `dev` y `pr-*`: el workflow commitea directo a `main`.
- `staging` y `prod`: el workflow abre un PR. Alguien del equipo owner aprueba.
- Borrar el directorio de un scope borra el scope. Argo CD tiene `prune` activado.

## Identidad AWS

- Las personas entran con SSO. No hay usuarios IAM.
- Cada app tiene un rol IAM `rampa-<app>-<scope>` asumido vía IRSA. El chart anota el ServiceAccount con el ARN si se le pasa `aws.roleArn`.
- GitHub Actions asume roles vía OIDC. No hay access keys en secrets de GitHub.
