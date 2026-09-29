# ADR 0003: infraestructura con clicks vía OpenTofu, y aplicaciones sin YAML

Fecha: 2026-09-29. Estado: aceptado. Extiende ADR 0002.

## Contexto

Queremos que un cliente levante infraestructura y cree aplicaciones desde la web, con clicks, sin escribir YAML ni Terraform. La primera idea fue una plantilla de CloudFormation para crear el cluster EKS con un click. Se descartó: CloudFormation solo sirve para AWS, y la meta es el mismo flujo en AWS, GCP, Azure, DigitalOcean, OCI y OVH.

## Decisiones

1. **OpenTofu ejecutado por Rampa para toda la infraestructura posterior a la conexión.** La API genera un root module, asume la identidad de la cuenta conectada y corre `tofu` en un worker. Un módulo por proveedor y tipo (`terraform/modules/aws/eks-cluster`, después `gcp/gke-cluster`, `azure/aks-cluster`, etc.). Los módulos van embebidos en el binario de la API y son abiertos: un cliente puede aplicarlos a mano.
2. **El estado de OpenTofu vive en Rampa, cifrado.** Backend `http` de OpenTofu contra la propia API, un estado por run, cifrado AES-256-GCM con una clave del servidor. Así no hace falta pedirle al cliente un bucket, y el estado es exportable si se va.
3. **El bootstrap de la conexión sigue siendo nativo de cada nube.** Antes de conectar no tenemos identidad en la cuenta del cliente, así que el primer paso lo ejecuta el cliente con sus credenciales: CloudFormation quick-create en AWS, Workload Identity Federation en GCP, App registration en Azure. Es el único punto donde se acepta algo no portable, y es un paso único.
4. **El módulo de cluster instala el agente en el mismo apply.** Con el token que la API generó al registrar el cluster y el rol IRSA que el módulo creó. El cliente termina con el cluster online en Rampa sin tocar una terminal.
5. **Aplicaciones sin YAML con un spec estructurado que es equivalente al manifest.** La web edita un `ApplicationSpec` en formularios; la API lo convierte al `rampa.yaml` canónico con el mismo paquete que valida los manifests pegados. Ida y vuelta sin pérdida. El YAML sigue siendo la fuente de verdad y se puede descargar con el proyecto inicial.
6. **Onboarding guiado en la web** como camino por defecto para una organización nueva: nube, cluster, agente, app, deploy.

## Consecuencias

- El plano de control corre procesos externos con credenciales temporales de clientes. Se aísla por run en directorios temporales, las credenciales van solo por entorno al subproceso, el log se filtra, y el runner tiene concurrencia y timeout acotados. Es el segundo componente más sensible después del agente.
- La cuenta conectada necesita permisos de administración de infraestructura. En AWS es un parámetro del stack de conexión (`ManageInfrastructure`), con una política acotada a recursos `rampa-*` donde la nube lo permite.
- Los módulos por nube son el trabajo repetitivo para sumar proveedores: interfaz `Provider` más un módulo `<nube>/<tipo>-cluster`. No hace falta tocar la API ni la web.
- Un cluster creado por Rampa se destruye con Rampa (`destroy` del run). Un cluster registrado a mano no.
