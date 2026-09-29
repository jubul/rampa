# Modelo de negocio de Rampa

Versión 1, 2026-09-29. Es un documento de trabajo: los precios y los números son hipótesis para validar con los primeros clientes, no decisiones cerradas.

## En una frase

Rampa vende a empresas de 10 a 300 developers la experiencia de plataforma que Mercado Libre construyó para sí misma con Fury, sin que tengan que armar un equipo de plataforma: conectan su nube, crean un cluster y despliegan con clicks, y la infraestructura queda en su cuenta.

## El problema que cobramos por resolver

Una empresa que corre software en Kubernetes con 20 developers gasta entre 1 y 3 ingenieros senior en mantener pipelines, charts, entornos, secretos y bases de datos. Eso son entre USD 100.000 y USD 400.000 por año en LATAM y más en Europa o Estados Unidos, y el resultado suele ser una plataforma interna frágil que nadie quiere heredar. Las alternativas comerciales existentes están pensadas y cotizadas para empresas grandes, en inglés, y con modelos conceptuales pesados.

## Propuesta de valor

1. **Cinco conceptos y nada más.** Application, scope, deployment, parameters, services. Un developer productivo el primer día. Es el modelo de Fury y de Nullplatform, probado a escala de decenas de miles de servicios.
2. **Infraestructura con clicks, en la nube del cliente.** Conectar la cuenta, crear un cluster, pedir una base de datos: formularios, no YAML ni Terraform. Rampa ejecuta OpenTofu por el cliente y los recursos quedan en su cuenta, con su facturación y sus permisos. Nada de infraestructura compartida con otros clientes.
3. **Nada sensible pasa por nosotros.** Credenciales de nube por federación, secretos de aplicación en el gestor de secretos del cliente, agente con conexión saliente y código abierto. Es el argumento que cierra la venta con seguridad y compras.
4. **Un YAML por app cuando lo quieran.** Todo lo que se hace con clicks queda expresado en un `rampa.yaml` versionable. No hay lock-in del modelo: el manifest, el chart y los módulos son abiertos.
5. **Precio por developer, público y razonable.** El cliente sabe cuánto va a pagar antes de hablar con nadie.

## Segmentos, en orden de prioridad

| Segmento | Quién es | Por qué compra | Cómo llegamos |
|---|---|---|---|
| **Scale-ups sin equipo de plataforma** | 20 a 100 devs, Kubernetes en AWS, 30 a 200 microservicios, un DevOps agotado | Reemplaza 1 a 2 contrataciones y baja el tiempo de deploy de días a minutos | Free tier, contenido técnico, referidos |
| **Software factories y consultoras** | Operan la infraestructura de varios clientes finales | Una organización de Rampa por cliente final, mismo panel, menos horas de operación | Alianzas: ellos revenden e implementan |
| **Empresas medianas migrando a la nube o a Kubernetes** | 50 a 300 devs, equipo de infraestructura tradicional | Un camino estándar para que los equipos desplieguen sin abrir tickets | Venta directa, prueba de concepto de 30 días |
| **Empresas grandes que exigen self-hosted** | Más de 300 devs, compliance, aire aislado | El mismo binario en su propia nube, con soporte | Enterprise, más adelante |

El cliente cero es nuestro propio grupo de developers. Cada funcionalidad se usa internamente antes de venderse.

## Cómo cobramos

Métrica principal: **developer activo por mes** (una persona que hizo al menos un deploy o un cambio en el mes). Es la métrica que crece con el valor que entregamos, la que usan Port y GitHub, y la que un gerente puede aprobar sin calculadora. Clusters, apps, scopes y deployments son ilimitados en todos los planes, porque limitarlos castiga justo el uso que queremos.

| Plan | Precio (hipótesis) | Para quién | Incluye |
|---|---|---|---|
| **Free** | USD 0 | Probar y equipos chicos | Hasta 3 developers, 1 cluster, AWS, deploys, scopes efímeros, comunidad |
| **Team** | USD 29 por developer por mes, mínimo 5 | Scale-ups | Todo Free, clusters ilimitados, infraestructura con clicks (clusters y services), aprobaciones, auditoría 90 días, soporte por chat en horario laboral |
| **Business** | USD 59 por developer por mes, mínimo 10 | Empresas medianas y consultoras | Todo Team, SSO de la organización, RBAC fino, auditoría 1 año y exportable, múltiples organizaciones, GCP y Azure, soporte con SLA de 4 horas |
| **Enterprise** | A cotizar, desde USD 30.000 por año | Grandes y reguladas | Todo Business, self-hosted o región dedicada, SLA 99,9 %, soporte 24x7, contrato y DPA a medida |

Descuento anual del 20 %. Los runs de infraestructura y el almacenamiento de estado están incluidos: el cliente paga la nube; nosotros no revendemos cómputo, porque no queremos que nuestro margen dependa de su factura de AWS.

**Por qué no cobramos por cluster o por app.** Un cluster puede tener 3 o 300 servicios; cobrar por cluster incentiva clusters gigantes. Cobrar por app penaliza los microservicios, que es justo lo que la plataforma facilita. Por developer es predecible para el cliente y crece cuando el cliente crece.

## Unit economics

Costos de servir a un cliente:

- **Plano de control:** compartido. Un cliente de 30 developers agrega decenas de MB en Postgres y unas pocas conexiones WebSocket. Costo marginal cercano a cero.
- **Runs de infraestructura:** un run de cluster consume unos 20 minutos de CPU de un worker. A precio de nube, centavos.
- **Soporte:** el costo real. Se controla con documentación, onboarding guiado en el producto y el free tier con soporte de comunidad.

Costos fijos del primer año, en orden de magnitud:

| Rubro | Mensual (USD) |
|---|---|
| Infraestructura del plano de control (API redundante, Postgres gestionado, dominios, observabilidad) | 400 a 900 |
| Herramientas (GitHub, correo, facturación, monitoreo) | 200 a 400 |
| Equipo: 2 a 3 personas (producto y plataforma, backend, frontend o ventas según etapa) | según mercado |

Margen bruto objetivo: mayor al 85 %. Un cliente Team de 25 developers paga USD 725 por mes y cuesta servirlo menos de USD 50.

Ejemplo de escala a 12 meses del lanzamiento: 20 clientes Team con 25 developers de promedio son USD 14.500 de facturación mensual recurrente. Con 5 clientes Business de 60 developers, otros USD 17.700. El objetivo a 12 meses es USD 30.000 mensuales, que cubre un equipo de 3 personas en LATAM.

## Cómo llegamos al mercado

1. **Meses 0 a 3: cliente cero y design partners.** Nuestro equipo usa Rampa en producción. Se invita a 3 a 5 empresas conocidas a usarlo gratis por 6 meses a cambio de feedback semanal y un caso de éxito publicable. Criterio: Kubernetes en AWS, 15 a 60 developers, dolor real con deploys.
2. **Meses 3 a 6: lanzamiento con free tier.** Sitio, documentación, el onboarding de la web sin intervención humana. Contenido en español: charlas y artículos sobre cómo Fury cambió Mercado Libre y cómo se construyó Rampa. La ventaja es ser los que hablan el idioma y conocen el contexto.
3. **Meses 6 a 12: canales.** Consultoras cloud de LATAM como implementadores y revendedores, con 20 % de comisión recurrente. Listado en AWS Marketplace para facturar contra el compromiso de gasto de los clientes.
4. **Después:** GCP y Azure abren Europa y clientes multi-cloud. Self-hosted abre banca y gobierno.

## Diferenciación frente a la competencia

| | Rampa | Nullplatform | Humanitec | Port | Northflank |
|---|---|---|---|---|---|
| Modelo conceptual | 5 entidades, rampa.yaml | Similar, más extenso | Score, orquestador, complejo | Portal, sin ejecución | PaaS propio |
| Infraestructura del cliente | En su nube, creada con clicks vía OpenTofu | En su nube | En su nube, BYO Terraform | No aplica | En su nube o en la de ellos |
| Agente | Open source, saliente | Propietario | Propietario | No aplica | Propietario |
| Precio | Público, por developer, desde USD 29 | No público | Desde USD 1.979 por mes | Desde USD 30 por seat | Por uso |
| Idioma y soporte | Español y portugués como primera clase | Inglés y español | Inglés | Inglés | Inglés |
| Self-hosted | Sí (Enterprise) | No público | Sí | No | Sí |

La diferenciación no es tecnológica, es de foco: simplicidad radical, transparencia de precios y seguridad, e infraestructura creada por la plataforma sin que el cliente escriba Terraform. Eso último es lo que ninguno de los competidores hace con clicks hoy.

## Riesgos y cómo los mitigamos

- **Competidores con más recursos bajan precio o copian la simplicidad.** Mitigación: velocidad, foco regional, relación directa con los clientes, y que el modelo abierto haga que cambiar de proveedor sea fácil pero innecesario.
- **Confianza en el agente y en el runner de infraestructura.** Mitigación: código abierto, binarios firmados, permisos documentados uno por uno, estado cifrado, SOC 2 Type II en el segundo año.
- **Sensibilidad al precio en LATAM.** Mitigación: precio en dólares pero con descuentos regionales explícitos y un free tier generoso; el cliente compara contra un sueldo, no contra otra herramienta.
- **Dependencia de Kubernetes.** Es una decisión: el mercado objetivo ya está ahí. Un runtime sin Kubernetes (ECS, Cloud Run) se evalúa cuando un segmento lo pida.
- **Multi-cloud sin cuentas para probar.** Mitigación: GCP y Azure llegan con un design partner que las use, no antes.
- **Un fundador técnico haciendo ventas.** Mitigación: canal de consultoras desde el mes 6 y una primera contratación comercial cuando haya 10 clientes pagos.

## Métricas que miramos

| Métrica | Objetivo primer año |
|---|---|
| Tiempo desde registro hasta primer deploy sano | menos de 60 minutos sin ayuda |
| Organizaciones activas semanalmente | crecimiento del 10 % mensual |
| Deploys por developer por semana | más de 3 |
| Conversión de Free a pago | 8 % |
| Retención neta de ingresos | mayor al 110 % |
| Margen bruto | mayor al 85 % |
| Tickets de soporte por cliente por mes | menos de 2 |

## Lo que hay que decidir pronto

1. Nombre y dominio comercial definitivos. Rampa es el nombre de trabajo; hay que verificar marca y dominio.
2. Región y proveedor del plano de control (afecta residencia de datos y el argumento de venta en Europa).
3. Facturación: Stripe con precio por seat y facturación anual; en LATAM, sumar factura local vía un partner.
4. Licencia del código: abierto todo el agente y los charts; el plano de control con licencia source-available o abierto con oferta de nube. Decidir antes del lanzamiento público.
