# Plan: plataforma interna de desarrollo (IDP) estilo Fury

**Contexto:** grupo de 10 a 30 developers, infraestructura en AWS, Kubernetes (EKS) ya en uso.
**Dolores a resolver, en orden:** deploys self-service, entornos y scopes, datos como servicio, catálogo y estándares.
**Fecha:** 2026-09-29

---

## 1. Qué significa "algo similar a Fury" a esta escala

Fury es el resultado de más de diez años y de un equipo de plataforma de cientos de personas. Lo que conviene copiar no son las herramientas sino el **contrato con el developer**: un modelo de pocas entidades (application, scope, deployment, parameters, services) que esconde la infraestructura y es igual para todos los equipos.

Principios que guían este plan:

1. **El modelo es el producto.** Las herramientas debajo se pueden cambiar sin que el dev lo note.
2. **Golden path, no jaula.** Hay un camino fácil y estándar, y una salida de emergencia documentada.
3. **Self-service con guardrails.** El dev no abre tickets. La plataforma impone cuotas, políticas y ownership.
4. **Plataforma como producto.** Tiene owner, roadmap, métricas de adopción y usuarios a los que se les pregunta.
5. **Pilotos reales desde el día uno.** Se construye sobre 2 o 3 aplicaciones productivas, no sobre un demo.

---

## 2. Modelo conceptual (el contrato con el dev)

| Entidad | Qué es | Ejemplo |
|---|---|---|
| **Application** | Unidad deployable, 1:1 con un repo o carpeta de monorepo. Tiene owner (equipo), tecnología y template de origen. | `checkout-api` |
| **Scope** | Instancia corriendo de una application en un entorno. Tiene tamaño, réplicas, dominio y parámetros. Puede ser permanente (dev, staging, prod) o efímero (por PR). | `checkout-api / prod`, `checkout-api / pr-142` |
| **Deployment** | Llevar un build a un scope con una estrategia (rolling, blue/green, canary) y rollback. | build `a1b2c3` a `prod`, canary al 10% |
| **Parameters** | Configuración y secretos por scope, versionados, inyectados como variables de entorno o archivos. | `DB_URL`, `FEATURE_X=true` |
| **Service** | Dependencia de infraestructura gestionada (Postgres, Redis, SQS, S3, DynamoDB). Se crea y se "linkea" a un scope. El link inyecta credenciales. | `checkout-db` (RDS Postgres) linkeado a `checkout-api / prod` |
| **Catalog** | Inventario de applications con owner, tecnología, dependencias, scorecards y docs. | quién es dueño de `payments-worker` |

**Flujo del dev (golden path):**

1. Crea una application desde un template (API Go/Node/Python, worker, cron, frontend estático).
2. Push a `main`: CI construye la imagen y la despliega automáticamente al scope `dev`.
3. Abre un PR: se crea un scope efímero con URL propia. Se destruye al mergear.
4. Promueve a `staging` y `prod` con estrategia de deploy y aprobación.
5. Pide un service (Postgres) y lo linkea: las credenciales aparecen en parameters.
6. Logs, métricas y alertas están conectados por defecto.

---

## 3. Arquitectura de referencia (independiente de build vs buy)

```
┌──────────────────────────────────────────────────────────────────┐
│ Superficie dev:  CLI  ·  Portal/UI  ·  API  ·  integración GitHub │
└──────────────────────────────────────────────────────────────────┘
┌──────────────────────────────────────────────────────────────────┐
│ Orquestador: traduce el modelo (app / scope / service) a infra   │
│ Políticas, cuotas, ownership, aprobaciones, auditoría            │
└──────────────────────────────────────────────────────────────────┘
┌──────────────────────────────┐  ┌────────────────────────────────┐
│ Delivery                     │  │ Provisioning de services       │
│ CI build -> ECR              │  │ Terraform/OpenTofu o Crossplane│
│ GitOps deploy (Argo CD)      │  │ RDS, ElastiCache, SQS, S3...   │
└──────────────────────────────┘  └────────────────────────────────┘
┌──────────────────────────────────────────────────────────────────┐
│ Runtime: EKS (namespaces por equipo y entorno), ingress, DNS,    │
│ External Secrets <-> Secrets Manager, IRSA, observabilidad       │
└──────────────────────────────────────────────────────────────────┘
```

**Decisiones de fundación que aplican a cualquier opción:**

- **Cuentas AWS:** separar como mínimo `prod` de `no-prod`. Un cluster EKS por cuenta, namespaces por equipo.
- **Identidad:** SSO con IAM Identity Center para personas. IRSA o Pod Identity para apps. Cero credenciales estáticas.
- **Secretos:** AWS Secrets Manager como fuente de verdad, External Secrets Operator en el cluster.
- **Registry:** ECR, imágenes escaneadas y firmadas (cosign).
- **Ingress y DNS:** ALB o NGINX, external-dns, cert-manager, wildcard DNS para scopes efímeros.
- **Observabilidad:** Prometheus + Grafana + Loki, o Datadog si ya lo pagan. OpenTelemetry incluido en los templates.
- **Costos:** tags obligatorios (`app`, `scope`, `team`), Kubecost o CUR por namespace.

---

## 4. Comparación build vs buy

### Opción A: construir completo con open source

Backstage (portal, catálogo, templates) + Argo CD + Argo Rollouts + Crossplane o Terraform con tf-controller + ApplicationSets para entornos efímeros + External Secrets.

- **Tiempo:** 3 a 4 meses hasta deploys self-service. 6 a 9 meses hasta cubrir los cuatro dolores.
- **Esfuerzo:** 2 FTE durante 6 a 9 meses. Luego 1 a 1,5 FTE permanentes (upgrades de Backstage, plugins, pegamento).
- **Licencias:** $0.
- **A favor:** control total, sin vendor, comunidad grande.
- **En contra:** Backstage es un framework, no un producto. El costo real es el pegamento entre piezas. 2 FTE son entre el 7% y el 20% de un grupo de 10 a 30 devs.
- **Veredicto:** no recomendado a esta escala salvo que ya exista un equipo de plataforma.

### Opción B: construir liviano ("build light")

Sin Backstage. Argo CD + un Helm chart único + GitHub Actions reusables + módulos Terraform + un CLI fino (o scripts) + Port en tier gratuito (hasta 15 seats) como portal y catálogo.

- **Tiempo:** 4 a 6 semanas hasta deploys self-service. 4 a 5 meses hasta los cuatro dolores en versión MVP.
- **Esfuerzo:** 1 FTE durante 4 a 5 meses. Luego 0,3 a 0,5 FTE.
- **Licencias:** $0 hasta 15 seats en Port. $30 por seat por mes arriba de eso (facturación anual).
- **A favor:** barato, piezas conocidas y reemplazables, el equipo aprende el stack.
- **En contra:** entornos efímeros y datos como servicio requieren pegamento propio. La UX depende de cuánto CLI o portal se construya. Riesgo de scripts "copo de nieve" si no hay owner.

### Opción C: adoptar Nullplatform

El modelo es exactamente el de Fury. El plano de control es SaaS y un agent corre dentro de tu cuenta AWS, así que los deploys y la infra quedan de tu lado. Soporta EKS, GitHub y GitLab, Datadog, Prometheus y CloudWatch. Tiene un catálogo open source de services en OpenTofu (RDS Postgres, DynamoDB, Valkey/ElastiCache, S3, Postgres en Kubernetes, endpoint exposer) extensible con "service specifications" propias.

- **Tiempo:** 1 a 3 semanas para un piloto. 2 a 3 meses hasta los cuatro dolores.
- **Esfuerzo:** 0,5 FTE durante la adopción. Luego 0,2 a 0,3 FTE (service specs, políticas).
- **Licencias:** no publican precios. Hay que pedir cotización y compararla contra el costo de un FTE.
- **A favor:** la experiencia más cercana a Fury. Scopes, parameters, services, catálogo, aprobaciones y auditoría vienen resueltos. SOC 2 Type II.
- **En contra:** vendor mediano (es una apuesta). Lock-in sobre el modelo y el workflow, aunque la infra sea tuya. Precio opaco. Hay que verificar cómo conviven las apps existentes con sus charts propios, sidecars y CRDs.

### Opción D: híbrido con otros productos

Port (portal y acciones) + Humanitec (orquestador). Humanitec arranca alrededor de $1.979 por mes con 5 usuarios incluidos según referencias de terceros, lo cual es caro para 10 a 30 devs. Port solo es un portal sin capa de ejecución, así que igual se construye la opción B debajo.

- **Veredicto:** Port sí, como portal dentro de la opción B. Humanitec no, a esta escala.

### Tabla comparativa

| Criterio | A: build completo | B: build light | C: Nullplatform | D: Port + Humanitec |
|---|---|---|---|---|
| Tiempo a primer deploy self-service | 3 a 4 meses | 4 a 6 semanas | 1 a 3 semanas | 4 a 8 semanas |
| Tiempo a cubrir los 4 dolores (MVP) | 6 a 9 meses | 4 a 5 meses | 2 a 3 meses | 4 a 6 meses |
| Esfuerzo inicial | 2 FTE | 1 FTE | 0,5 FTE | 1 FTE |
| Esfuerzo continuo | 1 a 1,5 FTE | 0,3 a 0,5 FTE | 0,2 a 0,3 FTE | 0,5 FTE |
| Licencias por año (20 devs) | $0 | $0 a ~$7.200 (Port arriba de 15 seats) | a cotizar | ~$24.000+ Humanitec, más Port |
| Lock-in | bajo | bajo | medio (modelo y workflow) | medio |
| Fit con el modelo Fury | alto, si lo construís | medio | muy alto | medio |
| Riesgo principal | mantenimiento | pegamento sin owner | vendor y precio | costo y dos vendors |

### Recomendación

1. **Hacer la Fase 0 (fundaciones) ya, sin esperar la decisión.** Es necesaria para cualquier opción y no se desperdicia.
2. **En paralelo, correr un PoC de Nullplatform de 3 semanas** con 2 apps piloto, un service (RDS Postgres) y un scope efímero. Pedir cotización el primer día. Fijar los criterios de aceptación antes de empezar (checklist en la sección 8).
3. **Decidir en la semana 5 o 6:**
   - Si el PoC pasa y el costo anual está por debajo de la mitad del costo de un platform engineer, **ir por C (Nullplatform)**.
   - Si no, **ir por B (build light)** con Port en tier gratuito como portal.
   - **A** solo tiene sentido si ya existe un equipo de plataforma con capacidad ociosa.

**Por qué:** con 10 a 30 devs, cada FTE dedicado a plataforma es entre el 3% y el 10% de la capacidad del grupo. El camino más barato hacia la experiencia Fury es comprar el modelo y quedarse con la infra. El PoC con criterios previos protege contra comprar humo.

---

## 5. Roadmap por fases

Cada fase indica qué cambia según la opción elegida (B o C). Las semanas son orientativas para 1 persona dedicada más champions.

### Fase 0: fundaciones (semanas 1 a 4). Común a todas las opciones.

- **Inventario.** Lista de servicios con owner, repo, runtime, dependencias y cómo se deploya hoy. Salida: un `catalog.yaml` o una hoja.
- **Modelo.** Documento de 2 páginas con las entidades de la sección 2 y el golden path. Validarlo con 3 devs de equipos distintos.
- **Cuentas y cluster.** Separar prod de no-prod. Namespaces por equipo. RBAC vía SSO. IRSA para apps.
- **Build estándar.** Dockerfile base por lenguaje (o buildpacks). Pipeline reusable que construye, escanea y publica a ECR.
- **Chart único.** Un Helm chart (o base de Kustomize) que cubre el 90% de las apps: Deployment o Rollout, Service, Ingress, HPA, PDB, ServiceAccount, ExternalSecret. La app solo aporta un `values.yaml`.
- **Secretos.** External Secrets Operator apuntando a Secrets Manager.
- **Observabilidad.** Dashboards y alertas por defecto por app.
- **Pilotos.** Elegir 2 o 3 apps (una API, un worker, un frontend) y a sus owners como co-diseñadores.

**Listo cuando:** las apps piloto se construyen con el pipeline estándar y corren con el chart único en `dev`.

### Fase 1: deploys self-service (semanas 4 a 8)

- **B:** Argo CD con ApplicationSet por equipo. Repo `platform-deploys` con una carpeta por app y scope. Argo Rollouts para canary y blue/green. GitHub Action reusable "deploy" que actualiza la imagen y commitea directo en dev o abre PR en prod. Notificaciones a Slack. Rollback = revert.
- **C:** instalar el agent en EKS. Crear namespace y applications para los pilotos. Conectar GitHub. Definir scopes dev, staging y prod con sus estrategias.

**Listo cuando:** un dev hace push a `main` y llega a dev sin tocar infra. Promoción a prod con un comando o click. Rollback probado.

### Fase 2: entornos y scopes (semanas 8 a 12)

- **B:** ApplicationSet con generador de Pull Requests. Cada PR crea el namespace `app-pr-N` con Ingress en `pr-N.app.dev.tu-dominio`, TTL y limpieza al cerrar el PR. ResourceQuota por namespace. Datos efímeros con Postgres en cluster (CloudNativePG) con seed o snapshot. vCluster solo si hace falta aislamiento fuerte.
- **C:** scopes efímeros nativos ligados al PR. Parameters por dimensión.
- **Ambas:** límites de réplicas y recursos para efímeros. Apagado nocturno de no-prod.

**Listo cuando:** abrir un PR genera una URL que funciona en menos de 10 minutos. Cerrarlo limpia todo. El costo de los efímeros es visible.

### Fase 3: datos como servicio (semanas 12 a 18)

- **Catálogo inicial:** RDS Postgres, ElastiCache Valkey/Redis, SQS, S3, DynamoDB. Cada uno con módulo Terraform/OpenTofu, tamaños S/M/L, backups, red (subnets privadas, security groups), credenciales a Secrets Manager y tags.
- **B:** motor de ejecución. Terraform/OpenTofu con tf-controller o Atlantis si el equipo ya sabe Terraform. Crossplane solo si quieren todo declarativo en Kubernetes. El "link" es: el service escribe el secreto en Secrets Manager y el ExternalSecret del scope lo importa.
- **C:** usar las service specs del catálogo open source de Nullplatform (RDS Postgres, DynamoDB, Valkey, S3) y escribir las propias (SQS) en OpenTofu.

**Listo cuando:** un dev pide un Postgres desde el portal o CLI y en menos de 15 minutos tiene el `DB_URL` en su scope sin abrir ticket.

### Fase 4: catálogo y estándares (semanas 18 a 24)

- **Catálogo** con ownership, on-call, tier, dependencias y links a repo, dashboards y runbooks. Fuente: un `catalog.yaml` en cada repo, ingestado automáticamente.
- **Templates (golden paths):** `api-go`, `api-node`, `api-python`, `worker`, `cron`, `frontend-static`. Cada uno incluye Dockerfile, pipeline, values, OpenTelemetry, healthchecks y README.
- **Scorecards:** tiene owner, tiene alertas, imagen sin CVEs críticas, usa el chart estándar, tiene runbook.
- **B:** Port en tier gratuito (hasta 15 seats), o Backstage si prefieren open source puro. Acciones self-service (crear app, pedir service) desde Port disparando GitHub Actions.
- **C:** catálogo y templates nativos.

**Listo cuando:** crear un servicio nuevo desde template hasta producción toma menos de un día. El 100% de las apps tiene owner.

### Fase 5: plataforma como producto (continuo)

- **Métricas:** las cuatro DORA (frecuencia de deploy, lead time, MTTR, tasa de fallo de cambios), tiempo hasta el primer deploy de un dev nuevo, porcentaje de apps en el golden path, tickets a infra por mes.
- **Rituales:** office hours semanales, changelog de la plataforma, encuesta trimestral a los devs.
- **Deprecación** del camino viejo con fecha anunciada.

---

## 6. Equipo y esfuerzo

- **Owner de plataforma:** 1 persona senior a tiempo completo los primeros 6 meses (opción B) o a medio tiempo (opción C). Después, según la tabla comparativa.
- **Champions:** 1 dev por equipo con 10% a 20% de su tiempo. Co-diseñan, prueban y difunden.
- **Regla:** nadie hace la plataforma "en los ratos libres". Es la forma garantizada de que no exista.

---

## 7. Riesgos y anti-patrones

- **Empezar por el portal.** Un Backstage bonito sin capa de ejecución es un catálogo que nadie usa.
- **Abstraer de más temprano.** Fury tardó años. A 10 a 30 devs, un chart único más Argo CD ya es el 80% del valor.
- **Ignorar el día 2.** Upgrades de EKS, de Argo, de charts. On-call de la plataforma.
- **Entornos efímeros sin límites.** Costo y ruido.
- **Comprar sin PoC, o construir sin owner.**
- **Lock-in.** Con Nullplatform la infra es tuya pero el modelo y el workflow son de ellos. Exigir exportabilidad (manifests, módulos OpenTofu) en el contrato.

---

## 8. Próximos pasos (semana 1)

1. Inventario de apps y owners. Medio día con cada equipo.
2. Escribir el documento del modelo (sección 2) y validarlo con 3 devs.
3. Elegir 2 o 3 apps piloto y sus champions.
4. Pedir a Nullplatform cotización y acceso para un PoC de 3 semanas. Fijar los criterios de aceptación de abajo.
5. Crear el repo `platform` con: chart único, pipeline reusable, módulos Terraform base.

### Checklist de aceptación del PoC (aplica a Nullplatform y a build light)

- [ ] Push a `main` deploya a dev en menos de 10 minutos sin intervención.
- [ ] Promoción a prod con canary y rollback en menos de 2 minutos.
- [ ] Un PR crea un scope efímero con URL. Cerrarlo lo destruye.
- [ ] Pedir un RDS Postgres y linkearlo inyecta credenciales sin ticket.
- [ ] Parameters y secretos por scope, con auditoría.
- [ ] Logs y métricas de la app visibles sin configurar nada.
- [ ] Un dev nuevo hace su primer deploy en su primer día siguiendo solo la doc.
- [ ] Costo por app y scope visible.
- [ ] (Nullplatform) precio anual conocido y por debajo de la mitad del costo de un FTE.
- [ ] (Nullplatform) las apps existentes con charts propios pueden migrar o convivir.

---

## Apéndice A: mapa de conceptos

| Concepto | Fury | Nullplatform | Build light (B) |
|---|---|---|---|
| Application | Application | Application | Repo + `catalog.yaml` + Argo Application |
| Scope | Scope | Scope | Namespace + values por entorno (ApplicationSet) |
| Deployment | Deploy (blue/green, canary) | Deployment con estrategias | Argo CD + Argo Rollouts |
| Parameters | Config y secrets | Parameters + Dimensions | ExternalSecret + values |
| Services | Fury Data Services | Services + Service Specs (OpenTofu) | Módulos Terraform vía tf-controller/Atlantis, o Crossplane |
| Catalog | Consola de Fury | Catálogo nativo | Port o Backstage |
| Ownership | Team / Namespace | Account > Namespace | `catalog.yaml` + CODEOWNERS |
| CLI | fury CLI | `np` CLI | scripts o CLI fino |

## Apéndice B: fuentes consultadas (2026-09-29)

- Nullplatform docs, getting started: https://docs.nullplatform.com/docs/getting-started
- Nullplatform, providers soportados: https://docs.nullplatform.com/docs/providers/overview
- Nullplatform, catálogo open source de services: https://github.com/nullplatform/services
- Port, precios: https://www.port.io/pricing
- Referencia de precios de Humanitec (terceros, verificar): https://northflank.com/blog/top-internal-developer-portals
