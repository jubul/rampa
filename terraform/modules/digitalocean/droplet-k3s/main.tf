# Droplet de un nodo con k3s y el agente de Rampa. Para Rampa es un Cluster de
# kind vm. Expone solo 80 y 443 (y 22 si hay admin_cidrs). La API de k3s no se
# expone: el agente esta adentro.

locals {
  tags         = concat(["rampa", "rampa-cluster:${var.name}", "rampa-env:${var.env}"], var.tags)
  use_registry = var.registry_name != ""

  # Las credenciales de pull de DOCR vienen como config de Docker: auths[registry].auth
  # es base64("usuario:contraseña"). k3s las quiere separadas en registries.yaml.
  docr_auth = try(
    split(":", base64decode(jsondecode(digitalocean_container_registry_docker_credentials.pull[0].docker_credentials).auths["registry.digitalocean.com"].auth)),
    ["", ""],
  )
  registries_yaml = local.use_registry ? join("\n", [
    "configs:",
    "  \"registry.digitalocean.com\":",
    "    auth:",
    "      username: \"${local.docr_auth[0]}\"",
    "      password: \"${local.docr_auth[1]}\"",
  ]) : ""
}

resource "digitalocean_container_registry" "this" {
  count                  = local.use_registry ? 1 : 0
  name                   = var.registry_name
  subscription_tier_slug = "basic"
  region                 = var.region
}

# Credenciales de solo lectura para que k3s haga pull de DOCR.
resource "digitalocean_container_registry_docker_credentials" "pull" {
  count          = local.use_registry ? 1 : 0
  registry_name  = digitalocean_container_registry.this[0].name
  write          = false
  expiry_seconds = 31536000 # un año; se rota con un nuevo apply
}

resource "digitalocean_reserved_ip" "this" {
  region = var.region
}

# El firewall se asocia por tag y existe antes que el Droplet, asi la maquina
# nace protegida en vez de quedar unos segundos con todo abierto.
resource "digitalocean_tag" "firewall" {
  name = "rampa-fw-${var.name}"
}

resource "digitalocean_droplet" "this" {
  name       = var.name
  region     = var.region
  size       = var.size
  image      = var.image
  ssh_keys   = var.ssh_key_fingerprints
  monitoring = true
  ipv6       = false # sin IPv6: el firewall y el bloqueo de metadata estan pensados para IPv4
  tags       = concat(local.tags, [digitalocean_tag.firewall.id])

  user_data = templatefile("${path.module}/cloud-init.yaml.tftpl", {
    public_ip              = digitalocean_reserved_ip.this.ip_address
    k3s_version            = var.k3s_version
    helm_version           = var.helm_version
    api_url                = var.api_url
    bootstrap_token        = var.bootstrap_token
    agent_chart_url        = var.agent_chart_url
    agent_image_repository = var.agent_image_repository
    agent_image_tag        = var.agent_image_tag
    tls_email              = var.tls_email
    install_eso            = false
    secret_store_values    = ""
    registries_yaml        = local.registries_yaml
    agent_host_network     = false
    pod_cidr               = var.pod_cidr
    k3s_version_url        = replace(var.k3s_version, "+", "%2B")
    helm_sha256_amd64      = var.helm_sha256["amd64"]
    helm_sha256_arm64      = var.helm_sha256["arm64"]
    cert_manager_version   = var.cert_manager_version
    eso_chart_version      = ""
  })
}

resource "digitalocean_reserved_ip_assignment" "this" {
  ip_address = digitalocean_reserved_ip.this.ip_address
  droplet_id = digitalocean_droplet.this.id
}

resource "digitalocean_firewall" "this" {
  name = "${var.name}-rampa"
  tags = [digitalocean_tag.firewall.name]

  inbound_rule {
    protocol         = "tcp"
    port_range       = "80"
    source_addresses = ["0.0.0.0/0", "::/0"]
  }
  inbound_rule {
    protocol         = "tcp"
    port_range       = "443"
    source_addresses = ["0.0.0.0/0", "::/0"]
  }
  dynamic "inbound_rule" {
    for_each = length(var.admin_cidrs) > 0 ? [1] : []
    content {
      protocol         = "tcp"
      port_range       = "22"
      source_addresses = var.admin_cidrs
    }
  }
  outbound_rule {
    protocol              = "tcp"
    port_range            = "1-65535"
    destination_addresses = ["0.0.0.0/0", "::/0"]
  }
  outbound_rule {
    protocol              = "udp"
    port_range            = "1-65535"
    destination_addresses = ["0.0.0.0/0", "::/0"]
  }
  outbound_rule {
    protocol              = "icmp"
    destination_addresses = ["0.0.0.0/0", "::/0"]
  }
}

# Registro comodín para los scopes, si la zona vive en DigitalOcean.
resource "digitalocean_record" "wildcard" {
  count  = var.dns_zone != "" && var.domain != "" ? 1 : 0
  domain = var.dns_zone
  type   = "A"
  name   = var.domain == var.dns_zone ? "*" : "*.${trimsuffix(var.domain, ".${var.dns_zone}")}"
  value  = digitalocean_reserved_ip.this.ip_address
  ttl    = 300

  lifecycle {
    precondition {
      condition     = var.domain == var.dns_zone || endswith(var.domain, ".${var.dns_zone}")
      error_message = "domain tiene que ser igual a dns_zone o terminar en .<dns_zone>."
    }
  }
}
