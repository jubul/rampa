output "cluster_name" {
  value = digitalocean_kubernetes_cluster.this.name
}

output "cluster_id" {
  value = digitalocean_kubernetes_cluster.this.id
}

output "endpoint" {
  value = digitalocean_kubernetes_cluster.this.endpoint
}

output "kubernetes_version" {
  value = digitalocean_kubernetes_cluster.this.version
}

output "agent_installed" {
  value = true
}

output "kubeconfig_command" {
  value = "doctl kubernetes cluster kubeconfig save ${digitalocean_kubernetes_cluster.this.id}"
}

output "registry" {
  value = var.registry_name != "" ? "registry.digitalocean.com/${var.registry_name}" : ""
}
