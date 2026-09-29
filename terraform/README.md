# Services: módulos OpenTofu

Un service es una dependencia gestionada de una application: `postgres`, `redis`, `sqs`, `s3`, `dynamodb`. Se declara en el `rampa.yaml` y se provisiona con estos módulos, que corre el agente dentro del cluster del cliente (Fase C) con la identidad nativa de la nube.

## Estructura por provider

```
terraform/
  modules/
    aws/
      postgres/      # RDS PostgreSQL (existe)
      redis/         # ElastiCache (pendiente)
      sqs/           # (pendiente)
      s3/            # (pendiente)
      dynamodb/      # (pendiente)
    gcp/             # (pendiente, llega con GCP en la Fase C)
    azure/           # (pendiente, llega con Azure en la Fase D)
```

`pkg/provider` resuelve `ServiceModule(kind)` a `terraform/modules/<provider>/<kind>` y dice si el módulo existe. Hoy solo `aws/postgres` devuelve `ok`.

## Mapeo de services por nube

Sacado de `docs/arquitectura-producto.md`, sección 6. El tipo del `rampa.yaml` es el mismo en las tres nubes; cambia el recurso que hay detrás.

| Tipo en `rampa.yaml` | AWS | GCP | Azure |
|---|---|---|---|
| `postgres` | RDS PostgreSQL | Cloud SQL for PostgreSQL | Azure Database for PostgreSQL |
| `redis` | ElastiCache | Memorystore | Azure Cache for Redis |
| `sqs` | SQS | Pub/Sub | Service Bus |
| `s3` | S3 | Cloud Storage (GCS) | Blob Storage |
| `dynamodb` | DynamoDB | Firestore | Cosmos DB |

Y lo que rodea a los services en cada nube:

| | AWS | GCP | Azure |
|---|---|---|---|
| Secret manager donde queda la credencial | Secrets Manager | Secret Manager | Key Vault |
| Identidad con la que corre el módulo | IRSA del agente | Workload Identity | Workload Identity (Entra ID) |
| Estado de OpenTofu | bucket S3 del cliente | bucket GCS del cliente | Blob del cliente |

## El contrato

Todo módulo de service cumple lo mismo, en cualquier nube, y es lo único que el chart y la API asumen:

1. Recibe `app`, `service`, `scope`, `team` y `size` (S, M, L, XL). El resto son detalles de red y del tipo.
2. Nombra el recurso `rampa-<app>-<service>-<scope>`.
3. Etiqueta todo con `rampa:app`, `rampa:service`, `rampa:scope`, `rampa:team` (labels equivalentes en GCP y Azure).
4. Deja las credenciales en el secret manager de la nube en `rampa/services/<app>/<service>/<scope>` como JSON. Las claves por tipo están en `docs/convenciones.md`.
5. En `prod`: alta disponibilidad, protección contra borrado y snapshot final. En el resto: barato y descartable.

El chart, vía External Secrets y el `ClusterSecretStore` que arma el provider, lee ese JSON y lo inyecta como parameters. Ningún secreto pasa por el plano de control.

## Estado actual

Existe `aws/postgres` como referencia del contrato. Se invoca a mano desde un root module propio hasta que el agente lo corra como Job (Fase C).

```hcl
module "checkout_db_prod" {
  source = "git::https://github.com/CHANGEME/rampa.git//terraform/modules/aws/postgres?ref=main"

  app     = "checkout-api"
  service = "checkout-db"
  scope   = "prod"
  team    = "payments"
  size    = "M"

  vpc_id                     = var.vpc_id
  subnet_ids                 = var.private_subnet_ids
  allowed_security_group_ids = [var.eks_node_security_group_id]
}
```

Validación local de un módulo:

```sh
cd terraform/modules/aws/postgres
tofu init -backend=false && tofu validate && tofu fmt -check
rm -rf .terraform .terraform.lock.hcl
```
