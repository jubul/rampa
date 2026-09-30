variable "name" {
  description = "Nombre del cluster. Es el que se registra en Rampa."
  type        = string
  validation {
    condition     = can(regex("^[a-zA-Z][a-zA-Z0-9-_]{0,99}$", var.name))
    error_message = "El nombre empieza con letra y usa letras, números, guion y guion bajo (máximo 100)."
  }
}

variable "env" {
  description = "Entorno en Rampa: nonprod recibe dev, staging y efímeros; prod solo prod."
  type        = string
  default     = "nonprod"
  validation {
    condition     = contains(["nonprod", "prod"], var.env)
    error_message = "env debe ser nonprod o prod."
  }
}

variable "kubernetes_version" {
  description = "Versión de Kubernetes. EKS Auto Mode requiere 1.29 o superior."
  type        = string
  default     = "1.33"
}

variable "vpc_cidr" {
  description = "Rango de la VPC. Se parte en dos subredes públicas y dos privadas."
  type        = string
  default     = "10.42.0.0/16"
}

variable "admin_principal_arns" {
  description = "Identidades IAM del cliente que van a poder administrar el cluster con kubectl."
  type        = list(string)
  default     = []
}

variable "install_agent" {
  description = "Instalar el agente de Rampa con Helm en el mismo apply."
  type        = bool
  default     = true
}

variable "api_url" {
  description = "URL pública del plano de control de Rampa, para el agente."
  type        = string
  default     = ""
}

variable "bootstrap_token" {
  description = "Bootstrap token de un solo uso generado por Rampa. Tiene prioridad sobre agent_token."
  type        = string
  default     = ""
  sensitive   = true
}

variable "agent_chart_url" {
  description = "URL del chart rampa-agent empaquetado (la API lo sirve en /public/charts/rampa-agent.tgz). Se usa si no hay agent_chart_oci ni agent_chart_path."
  type        = string
  default     = ""
}

variable "agent_token" {
  description = "Token del cluster generado por Rampa. Solo se usa para crear el Secret del agente."
  type        = string
  default     = ""
  sensitive   = true
}

variable "agent_chart_path" {
  description = "Ruta local al chart rampa-agent (el runner lo escribe junto a los módulos). Alternativa: agent_chart_oci."
  type        = string
  default     = ""
}

variable "agent_chart_oci" {
  description = "Referencia OCI del chart rampa-agent, por ejemplo oci://ghcr.io/acme/charts/rampa-agent."
  type        = string
  default     = ""
}

variable "agent_chart_version" {
  description = "Versión del chart cuando se usa agent_chart_oci."
  type        = string
  default     = ""
}

variable "agent_image_repository" {
  description = "Imagen del agente."
  type        = string
  default     = ""
}

variable "agent_image_tag" {
  type    = string
  default = ""
}

variable "tags" {
  description = "Tags adicionales para todos los recursos."
  type        = map(string)
  default     = {}
}

variable "public_access_cidrs" {
  description = "CIDRs que pueden llegar al endpoint publico de la API de Kubernetes. Acotalo a tus redes; el agente y el runner de Rampa lo necesitan (el runner desde la IP de la API de Rampa)."
  type        = list(string)
  default     = ["0.0.0.0/0"]
}
