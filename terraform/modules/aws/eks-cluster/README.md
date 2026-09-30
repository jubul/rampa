# aws/eks-cluster

Cluster EKS listo para Rampa, creado por el runner de infraestructura o a mano con OpenTofu.

Crea: VPC en dos zonas (dos subredes públicas, dos privadas, un NAT gateway), cluster EKS con **Auto Mode** (AWS administra los nodos; no hay node groups), proveedor OIDC para IRSA, rol IAM del agente con permisos sobre `rampa/*` en Secrets Manager y lectura de ECR, access entries de administrador para las identidades que se pasen, y opcionalmente el agente de Rampa instalado con Helm en el mismo apply.

| Variable | Default | Qué es |
|---|---|---|
| `name` | requerido | Nombre del cluster |
| `env` | `nonprod` | `nonprod` o `prod` |
| `kubernetes_version` | `1.33` | Auto Mode requiere 1.29 o superior |
| `vpc_cidr` | `10.42.0.0/16` | Se parte en cuatro subredes /20 |
| `admin_principal_arns` | `[]` | Identidades IAM que van a poder usar `kubectl` |
| `public_access_cidrs` | `["0.0.0.0/0"]` | Quién llega al endpoint público de la API de Kubernetes. Acotalo; el runner de Rampa lo necesita desde la IP de la API |
| `install_agent` | `true` | Instalar el agente con Helm |
| `api_url`, `agent_token` | vacíos | Los pasa el runner de Rampa. `agent_token` es sensible |
| `agent_chart_path`, `agent_chart_oci` o `agent_chart_url` | vacíos | De dónde sale el chart `rampa-agent` (el runner pasa la URL pública de la API) |
| `bootstrap_token` | vacío | Token de un solo uso generado por Rampa; tiene prioridad sobre `agent_token` |

Salidas principales: `cluster_name`, `agent_role_arn`, `oidc_issuer`, `private_subnet_ids`, `kubeconfig_command`.

**Costo aproximado:** plano de control de EKS (unos USD 73 por mes), el NAT gateway (unos USD 32 por mes más tráfico) y los nodos que Auto Mode levante según la carga. Borrar el módulo borra todo.

**Permisos:** quien lo aplica necesita crear VPC, EKS, roles IAM y proveedores OIDC. En el stack `RampaConnect` eso es `ManageInfrastructure=true`.

Uso a mano:

```hcl
module "cluster" {
  source = "git::https://github.com/jubul/rampa.git//terraform/modules/aws/eks-cluster?ref=main"

  name                 = "rampa-nonprod"
  env                  = "nonprod"
  admin_principal_arns = ["arn:aws:iam::123456789012:user/juan"]
  install_agent        = true
  api_url              = "https://api.rampa.example.com"
  agent_token          = var.agent_token
  agent_chart_oci      = "oci://ghcr.io/jubul/charts/rampa-agent"
  agent_chart_version  = "0.1.0"
}
```


**Nota de acceso:** la identidad con la que Rampa aplica el módulo queda como administradora del cluster (`bootstrap_cluster_creator_admin_permissions`): es cómo instala y opera el agente. Si querés quitarla después, borrá su access entry desde la consola de EKS.
