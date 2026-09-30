# Seguridad de Rampa

Qué protege Rampa, cómo, y qué asume del cliente. Es el documento para leer antes de instalar el agente o conectar una cuenta, y la lista que usa la revisión adversarial interna. Cuando algo acá no coincida con el código, gana este documento y se arregla el código.

## Modelo de amenazas, en corto

- **Un cliente no puede ver ni tocar nada de otro.** Toda tabla lleva `org_id`; todo query filtra por él; los recursos referenciados por id en un body se validan contra la misma organización; un token de API vale solo en su organización y con el menor rol entre el token y la membresía vigente del dueño.
- **El plano de control no guarda credenciales de nube de larga duración cuando la nube ofrece federación.** AWS, GCP y Azure se conectan por rol asumible con external ID o por credencial federada. DigitalOcean, OCI y OVH no ofrecen federación: su token se guarda cifrado (ver abajo) y es una excepción documentada.
- **Los secretos de aplicación no se persisten en el plano de control.** Van al gestor de secretos de la nube del cliente, o a Secrets de Kubernetes cifrados en reposo, escritos por el agente. El valor pasa por la API solo en tránsito, cifrado por TLS, y no queda en la base, en eventos ni en logs.
- **El agente solo habla hacia afuera.** No hay puertos abiertos hacia el cluster del cliente ni kubeconfigs en nuestras manos. El agente se autentica con un token por cluster, hasheado en la base y rotable.
- **Lo que Rampa ejecuta en la nube del cliente es auditable.** Los módulos de OpenTofu, el chart del agente y el código del agente son abiertos. El RBAC del agente está justificado permiso por permiso en `instalar-agente.md`.

## Identidad y acceso

| Qué | Cómo |
|---|---|
| Sesión web | Cookie HttpOnly, SameSite Lax, firmada con HMAC-SHA256 con `RAMPA_SESSION_SECRET` (32 bytes o más). `dev-login` solo existe con `RAMPA_DEV_LOGIN=true`. |
| Login corporativo | OIDC con `state` firmado. Código intercambiado del lado del servidor. |
| Tokens de API | `rampa_<aleatorio>`, guardados como SHA-256, con vencimiento opcional. Mostrados una sola vez. Se borran al expulsar al dueño de la organización y se acotan al bajarle el rol. |
| Roles | owner, admin, developer, viewer. Prod y las operaciones de infraestructura exigen admin. Nadie puede otorgar ni tocar un rol superior al propio. El último owner no puede irse. |
| Tokens de cluster | `rampa_cluster_<aleatorio>`, SHA-256 en la base, rotables desde la web; rotar corta la conexión viva del agente anterior. |
| Tokens de bootstrap | `rampa_bootstrap_<aleatorio>`, de un solo uso, vencen a las 2 horas, canjeables por un token de cluster en `POST /v1/agent/bootstrap`, con límite de intentos por IP (la IP real, salvo proxies declarados en `RAMPA_TRUSTED_PROXIES`) y por token. Rotar el token del cluster invalida los bootstraps pendientes. Son lo único que viaja en un cloud-init. |
| Stream de eventos | Re-chequea en cada latido que el usuario siga siendo miembro o que el token siga existiendo, y se corta si no. |

## Credenciales de nube

| Nube | Mecanismo | Qué guarda Rampa |
|---|---|---|
| AWS | Rol IAM asumible por la identidad del plano de control, con condición de external ID único por cuenta. Creado por el cliente con CloudFormation. | ARN del rol y external ID. Nada secreto. |
| GCP | Workload Identity Federation hacia nuestro emisor OIDC. | Ids del proyecto, pool y cuenta de servicio. |
| Azure | App registration con credencial federada. | Tenant, client id, suscripción. |
| DigitalOcean, OCI, OVH | Token de API del cliente con alcance mínimo. | El token cifrado con AES-256-GCM, clave `RAMPA_STATE_KEY`, dato autenticado el id de la cuenta. Nunca se devuelve por la API. Se rota volviéndolo a pegar; se borra con la cuenta. |

Las credenciales temporales que obtiene el plano de control para correr OpenTofu duran una hora, viajan al subproceso solo por variables de entorno, no tocan disco, y cada línea del log del run pasa por un redactor que reemplaza por `***` cualquier secreto conocido del run (credenciales de nube, tokens de bootstrap y de estado) antes de guardarse, emitirse o entrar en un mensaje de error.

## El agente

- Corre en `rampa-system` con un ServiceAccount propio. Sin privilegios de host, sin capacidades, sistema de archivos de solo lectura, usuario no root.
- Aplica solo lo que la API le manda para el namespace `<app>-<scope>` exacto; rechaza namespaces del sistema y namespaces existentes que no creó Rampa o que pertenecen a otra app; para borrar un namespace exige la label `rampa.dev/app` que él mismo puso. No pisa Secrets administrados por External Secrets.
- Su ClusterRole tiene solo los recursos y verbos que el código usa, sin `watch`, sin `events`, sin `jobs`. El permiso `get` sobre Secrets a nivel cluster existe solo con `rbac.appSecrets` (necesario para `secret_store: kubernetes`) y se puede apagar en clusters con gestor de secretos en la nube.
- Termina los comandos en curso al recibir SIGTERM antes de cerrar, para no dejar deploys a medias ni resultados perdidos.
- Su identidad de nube (IRSA, instance profile, Workload Identity) es la que escribe secretos en el gestor de la nube y hace pull del registry. Rampa nunca tiene esa identidad.

## Infraestructura creada por Rampa

- El runner de OpenTofu corre cada ejecución en un directorio temporal propio, con un entorno mínimo (no hereda las variables del plano de control), con concurrencia y timeout acotados. El estado se cifra con AES-256-GCM antes de guardarse y un run solo puede leer el suyo.
- Los permisos que Rampa pide en la nube para crear infraestructura son un parámetro del stack de conexión (`ManageInfrastructure`), acotados a recursos `rampa-*` y `<cluster>-*` donde la nube lo permite. Sin ese parámetro Rampa solo despliega en clusters existentes.
- Las máquinas con k3s exponen 80 y 443; 22 solo desde los CIDRs que el cliente indique, o nunca. La API de Kubernetes de la máquina no se expone. k3s corre con cifrado de secretos en reposo. SSH por contraseña deshabilitado. Actualizaciones de seguridad automáticas. En EC2, IMDSv2 obligatorio con un solo salto, disco cifrado, y SSM Session Manager opcional (`enable_ssm`), que es una vía de acceso para quien tenga `ssm:StartSession` en la cuenta.
- **Los pods no llegan al metadata del proveedor.** En una máquina con k3s, el servicio de metadata (169.254.169.254) entrega las credenciales de la identidad de la máquina, y en DigitalOcean también el user-data completo. Una regla de iptables (`rampa-imds-guard`, reafirmada cada 5 minutos) bloquea ese destino desde la red de pods. El agente y External Secrets corren con la red del host para poder usar la identidad de la máquina; ningún pod de aplicación puede.
- **Todo lo que el cloud-init instala tiene versión fija**: k3s (el instalador se baja del tag y verifica el sha256 del binario), Helm (checksum verificado), cert-manager y External Secrets (versión de chart fija). El chart del agente se baja de la API de Rampa por HTTPS.
- **Lo que queda en el user-data de una máquina** es legible por root en la máquina: el bootstrap token, que es de un solo uso y vence en 2 horas, y en DigitalOcean la credencial de solo lectura del registry (vence al año y se rota con un nuevo apply). Los pods no lo pueden leer por el bloqueo del metadata.
- **EKS:** la identidad con la que Rampa crea el cluster queda como administradora del mismo (es cómo instala y opera el agente). El endpoint público de la API de Kubernetes se puede acotar con `public_access_cidrs`.
- Un cluster creado por Rampa se destruye desde Rampa. Uno registrado a mano, no.

## Lo que asumimos del cliente

- Que el cluster que registra a mano tiene su propio control de acceso y cifrado de Secrets en reposo si va a usar `secret_store: kubernetes`.
- Que las identidades IAM que pasa como administradores de un cluster creado por Rampa son suyas.
- Que el token de API que pega para DigitalOcean, OCI u OVH tiene el alcance que pedimos y no más.

## Lo que todavía no está

- SOC 2 Type II y pentest externo: planeados para el segundo año.
- Firma de binarios e imágenes (cosign) y SBOM: antes del lanzamiento público.
- Auditoría exportable y retención configurable: Fase B.
- Alta disponibilidad multi-réplica del plano de control con eventos compartidos: Fase B.
- Un runtime de VM sin Kubernetes y serverless: ola dos.
