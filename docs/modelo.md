# El modelo de Rampa

Este documento es el contrato entre Rampa y los developers. Todo lo demás (chart, CLI, workflows, Terraform) implementa esto. Si algo de la implementación contradice este documento, gana el documento y se arregla la implementación.

## Entidades

### Application

Una unidad deployable. Es 1:1 con un repositorio (o una carpeta de un monorepo). Tiene un owner, que es un equipo, y un `rampa.yaml` en la raíz que la describe.

### Scope

Una instancia corriendo de una application en un entorno. Cada scope es un namespace de Kubernetes propio, llamado `<app>-<scope>`, con su cuota, su red y sus secretos. Hay dos clases:

- **Permanentes:** `dev`, `staging`, `prod`. Se declaran en `rampa.yaml`.
- **Efímeros:** `pr-<número>`. Se crean al abrir un pull request y se destruyen al cerrarlo. Heredan su configuración de la sección `pr` del `rampa.yaml`.

`prod` corre en el cluster y la cuenta AWS de producción. El resto, en no-prod.

### Deployment

Llevar una imagen a un scope con una estrategia. Estrategias: `rolling` (default), `canary`, `bluegreen`. Cada deployment queda registrado en Rampa con quién lo pidió, con qué imagen y cómo terminó. Rollback es desplegar la imagen anterior.

### Parameters

Configuración por scope, inyectada como variables de entorno. Tres orígenes:

- **Valor plano** en el `rampa.yaml`. Puede tener override por scope; la definición más específica gana.
- **Secreto** en AWS Secrets Manager, en la ruta `rampa/<app>/<scope>/<NOMBRE>`. El chart genera un `ExternalSecret` que lo trae al namespace.
- **Service** linkeado: una clave dentro del secreto que el service dejó en `rampa/services/<app>/<service>/<scope>`.

### Service

Una dependencia de infraestructura gestionada: `postgres`, `redis`, `sqs`, `s3`, `dynamodb`. Se declara en `rampa.yaml`, se provisiona con Terraform, y deja sus credenciales en Secrets Manager. Linkear un service a un scope es declarar parameters que apuntan a él.

### Catalog

El conjunto de todos los `rampa.yaml`. Como cada app se describe a sí misma, el catálogo se deriva, no se mantiene a mano.

## `rampa.yaml` de referencia

```yaml
apiVersion: rampa.dev/v1
kind: Application
metadata:
  name: checkout-api          # dns-safe, único en la organización
  team: payments              # owner. Va a labels, costos y alertas
  tier: 2                     # 1 crítico, 2 importante, 3 interno
  description: API de checkout

spec:
  type: api                   # api | worker | cron | static
  runtime: go                 # go | node | python | static. Informativo: templates, dashboards
  port: 8080                  # solo api y static
  healthcheck: /health        # solo api. Default /health
  # schedule: "*/5 * * * *"   # solo cron

  scopes:
    dev:
      size: S
      replicas: 1
    staging:
      size: S
      replicas: 2
    prod:
      size: M
      replicas: 3
      strategy: canary
      domain: checkout.example.com   # opcional. Sin domain, la URL es <app>-<scope>.<dominio-del-cluster>
      autoscaling:
        min: 3
        max: 10
        cpu: 70
    pr:                       # plantilla de los scopes efímeros
      size: S
      replicas: 1
      ttl: 72h

  parameters:
    - name: LOG_LEVEL
      value: info
    - name: LOG_LEVEL
      value: debug
      scopes: [dev, pr]
    - name: API_KEY
      secret: true            # Secrets Manager: rampa/checkout-api/<scope>/API_KEY
    - name: DB_URL
      fromService: checkout-db
      key: url                # clave dentro del secreto del service

  services:
    - name: checkout-db
      type: postgres
      size: S
      scopes: [staging, prod] # opcional. Default: todos los permanentes. Los efímeros nunca reciben services gestionados
```

## Dos formas de escribir lo mismo

El `rampa.yaml` se puede escribir a mano en el repo o armar con formularios en la web. La web edita un spec estructurado y Rampa lo convierte al mismo `rampa.yaml`, sin pérdida en ninguna dirección. Lo que se crea con clicks se puede descargar como proyecto inicial con el YAML adentro. Lo que se escribe a mano se puede seguir editando con formularios.

## Tamaños

Los tamaños son remeras, no CPUs. El chart los traduce.

| Size | CPU request / limit | Memoria request / limit |
|---|---|---|
| S | 100m / 500m | 128Mi / 512Mi |
| M | 250m / 1000m | 256Mi / 1Gi |
| L | 500m / 2000m | 512Mi / 2Gi |
| XL | 1000m / 4000m | 1Gi / 4Gi |

## Golden path

1. `rampa init` crea el repo desde un template: código mínimo, Dockerfile, `rampa.yaml`, workflow de CI.
2. Push a `main`: el workflow construye la imagen, la escanea, la sube a ECR y llama a `rampa deploy --scope dev`.
3. Abrir un PR: se construye y despliega a `pr-<n>`. El bot comenta la URL. Cerrar el PR destruye el scope.
4. Promover a `staging` y `prod`: desde la web con el botón Desplegar, o con `rampa deploy --scope prod --image <tag>`. Si la organización exige aprobación para producción, otro admin la aprueba. El agente aplica la estrategia declarada y reporta el avance en vivo.
5. Rollback: desplegar la imagen anterior, desde la web o con `rampa deploy`.

## Lo que Rampa no hace (por ahora)

- No gestiona bases de datos compartidas entre apps. Un service pertenece a una app.
- No crea services para scopes efímeros. Los PR usan mocks, fixtures o una base en el cluster.
- No reemplaza Terraform para infraestructura que no sea un service de una app (redes, clusters, cuentas).
