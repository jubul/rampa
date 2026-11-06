# Cluster DOKS listo para Rampa, con el agente instalado por Helm en el mismo apply.

data "digitalocean_kubernetes_versions" "this" {}

locals {
  version = var.kubernetes_version != "" ? var.kubernetes_version : data.digitalocean_kubernetes_versions.this.latest_version
  tags    = concat(["rampa", "rampa-cluster:${var.name}", "rampa-env:${var.env}"], var.tags)
}

resource "digitalocean_container_registry" "this" {
  count                  = var.registry_name != "" ? 1 : 0
  name                   = var.registry_name
  subscription_tier_slug = "basic"
  region                 = var.region
}

resource "digitalocean_kubernetes_cluster" "this" {
  name                 = var.name
  region               = var.region
  version              = local.version
  auto_upgrade         = true
  surge_upgrade        = true
  registry_integration = var.registry_name != ""
  tags                 = local.tags

  maintenance_policy {
    start_time = "04:00"
    day        = "sunday"
  }

  node_pool {
    name       = "${var.name}-pool"
    size       = var.node_size
    node_count = var.node_count
    tags       = local.tags
  }

  depends_on = [digitalocean_container_registry.this]
}

provider "helm" {
  kubernetes = {
    host                   = digitalocean_kubernetes_cluster.this.endpoint
    token                  = digitalocean_kubernetes_cluster.this.kube_config[0].token
    cluster_ca_certificate = base64decode(digitalocean_kubernetes_cluster.this.kube_config[0].cluster_ca_certificate)
  }
}

resource "helm_release" "rampa_agent" {
  name             = "rampa-agent"
  namespace        = "rampa-system"
  create_namespace = true
  chart            = var.agent_chart_url
  # No se espera a que el agente quede listo: depende de que llegue a la API de
  # Rampa, y eso se ve en el estado online del cluster, no en el apply.
  wait             = false
  timeout          = 600

  set = concat(
    [{ name = "api.url", value = var.api_url }],
    var.agent_image_repository != "" ? [{ name = "image.repository", value = var.agent_image_repository }] : [],
    var.agent_image_tag != "" ? [{ name = "image.tag", value = var.agent_image_tag }] : [],
  )

  set_sensitive = [
    { name = "cluster.bootstrapToken", value = var.bootstrap_token },
  ]
}
