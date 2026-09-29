# postgres

RDS PostgreSQL para una application. Cumple el contrato de `terraform/README.md`.

| size | instancia | storage inicial |
|---|---|---|
| S | db.t4g.micro | 20 GB |
| M | db.t4g.medium | 50 GB |
| L | db.r6g.large | 100 GB |
| XL | db.r6g.xlarge | 200 GB |

En `prod`: multi-AZ, 14 días de backups, protección contra borrado, snapshot final, Performance Insights. En `dev` y `staging`: single-AZ, 3 días, se destruye sin snapshot.

El secreto `rampa/services/<app>/<service>/<scope>` queda con las claves `url`, `host`, `port`, `dbname`, `user`, `password`.
