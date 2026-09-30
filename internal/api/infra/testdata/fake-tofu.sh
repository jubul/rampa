#!/usr/bin/env bash
# tofu falso para los tests del runner. Imita lo justo de OpenTofu:
#   - imprime líneas por stdout y stderr,
#   - en apply guarda un estado en el backend http usando TF_HTTP_* (LOCK, POST, UNLOCK)
#     con Basic auth, que es lo que hace el backend real con TF_HTTP_USERNAME/PASSWORD,
#   - en destroy borra el estado,
#   - responde a "output -json" con un JSON fijo según el módulo escrito en modules/.
# Nunca imprime el valor de las credenciales ni de los tokens: los tests verifican
# que no aparezcan en el log. Solo dice si están presentes.
# Si rampa.auto.tfvars.json trae "name": "leak-secrets", imprime secretos del entorno (para probar la redacción).
# Si rampa.auto.tfvars.json trae "name": "fail-plan", falla en plan con un error de permisos.
set -euo pipefail

sub="${1:-}"
tfvars="rampa.auto.tfvars.json"

require_env() {
  for v in "$@"; do
    if [ -z "${!v:-}" ]; then
      echo "fake-tofu: falta la variable $v" >&2
      exit 3
    fi
  done
}

# El runner escribe un solo módulo: modules/<provider>/<nombre>.
module_path() {
  local d
  for d in modules/*/*/; do
    if [ -f "$d/main.tf" ]; then
      echo "${d%/}"
      return
    fi
  done
  echo ""
}

mod="$(module_path)"
mod="${mod#modules/}"
provider="${mod%%/*}"

case "$sub" in
  init)
    echo "Initializing the backend..."
    require_env TF_HTTP_ADDRESS TF_HTTP_LOCK_ADDRESS TF_HTTP_UNLOCK_ADDRESS TF_HTTP_PASSWORD TF_IN_AUTOMATION
    [ -f main.tf ] || { echo "fake-tofu: no hay main.tf en $(pwd)" >&2; exit 3; }
    [ -f "$tfvars" ] || { echo "fake-tofu: no hay $tfvars" >&2; exit 3; }
    [ -n "$mod" ] || { echo "fake-tofu: falta el módulo embebido" >&2; exit 3; }
    [ -f charts/rampa-agent/Chart.yaml ] || { echo "fake-tofu: falta el chart del agente" >&2; exit 3; }
    echo "módulo: $mod"
    echo "OpenTofu has been successfully initialized!"
    ;;
  plan)
    require_env TF_VAR_bootstrap_token
    echo "bootstrap_token presente: si"
    case "$provider" in
      aws)
        require_env AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN AWS_REGION
        echo "credenciales presentes: AWS_ACCESS_KEY_ID=${AWS_ACCESS_KEY_ID:+si} AWS_REGION=$AWS_REGION"
        if [ "$mod" = "aws/eks-cluster" ]; then
          require_env TF_VAR_agent_token
          echo "agent_chart_path: $(grep -o '"agent_chart_path": *"[^"]*"' "$tfvars")"
        fi
        ;;
      digitalocean)
        require_env DIGITALOCEAN_TOKEN
        echo "credenciales presentes: DIGITALOCEAN_TOKEN=${DIGITALOCEAN_TOKEN:+si} AWS_ACCESS_KEY_ID=${AWS_ACCESS_KEY_ID:+si}${AWS_ACCESS_KEY_ID:-no} TF_VAR_agent_token=${TF_VAR_agent_token:+si}${TF_VAR_agent_token:-no}"
        echo "agent_chart_url: $(grep -o '"agent_chart_url": *"[^"]*"' "$tfvars")"
        ;;
    esac
    if grep -q '"leak-secrets"' "$tfvars"; then
      # Imita un provider o TF_LOG que imprime variables de entorno: el runner las tiene que redactar.
      echo "debug DIGITALOCEAN_TOKEN=${DIGITALOCEAN_TOKEN:-}"
      echo "debug TF_VAR_bootstrap_token=${TF_VAR_bootstrap_token:-}"
      echo "debug TF_HTTP_PASSWORD=${TF_HTTP_PASSWORD:-}"
      echo "Error: provider dijo AWS_SECRET_ACCESS_KEY=${AWS_SECRET_ACCESS_KEY:-} corto=x" >&2
      exit 1
    fi
    if grep -q '"fail-plan"' "$tfvars"; then
      echo "Error: creating EC2 VPC: UnauthorizedOperation: You are not authorized to perform this operation." >&2
      exit 1
    fi
    echo "Plan: 12 to add, 0 to change, 0 to destroy."
    ;;
  apply)
    require_env TF_HTTP_ADDRESS TF_HTTP_USERNAME TF_HTTP_PASSWORD
    auth=(-u "$TF_HTTP_USERNAME:$TF_HTTP_PASSWORD")
    lock='{"ID":"fake-lock-1","Operation":"OperationTypeApply","Who":"fake-tofu"}'
    code=$(curl -sS -o /dev/null -w '%{http_code}' -X LOCK "${auth[@]}" -d "$lock" "$TF_HTTP_LOCK_ADDRESS")
    [ "$code" = "200" ] || { echo "fake-tofu: LOCK devolvió $code" >&2; exit 1; }
    rtype="aws_eks_cluster"
    [ "$provider" = "digitalocean" ] && rtype="digitalocean_droplet"
    state='{"version":4,"terraform_version":"1.12.0","serial":1,"lineage":"fake","outputs":{},"resources":[{"type":"'"$rtype"'","name":"this"}]}'
    code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "${auth[@]}" -H 'Content-Type: application/json' -d "$state" "$TF_HTTP_ADDRESS?ID=fake-lock-1")
    [ "$code" = "200" ] || { echo "fake-tofu: POST devolvió $code" >&2; exit 1; }
    code=$(curl -sS -o /dev/null -w '%{http_code}' -X GET "${auth[@]}" "$TF_HTTP_ADDRESS")
    [ "$code" = "200" ] || { echo "fake-tofu: GET devolvió $code" >&2; exit 1; }
    code=$(curl -sS -o /dev/null -w '%{http_code}' -X UNLOCK "${auth[@]}" -d '{"ID":"fake-lock-1"}' "$TF_HTTP_UNLOCK_ADDRESS")
    [ "$code" = "200" ] || { echo "fake-tofu: UNLOCK devolvió $code" >&2; exit 1; }
    if [ "$provider" = "digitalocean" ]; then
      echo "module.vm.digitalocean_droplet.this: Creating..."
      echo "module.vm.digitalocean_reserved_ip.this: Creation complete"
    else
      echo "module.cluster.aws_eks_cluster.this: Creating..."
      echo "module.cluster.helm_release.rampa_agent[0]: Creation complete"
    fi
    echo "Apply complete! Resources: 12 added, 0 changed, 0 destroyed."
    ;;
  destroy)
    require_env TF_HTTP_ADDRESS TF_HTTP_USERNAME TF_HTTP_PASSWORD
    auth=(-u "$TF_HTTP_USERNAME:$TF_HTTP_PASSWORD")
    code=$(curl -sS -o /dev/null -w '%{http_code}' -X GET "${auth[@]}" "$TF_HTTP_ADDRESS")
    echo "estado previo: HTTP $code"
    code=$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE "${auth[@]}" "$TF_HTTP_ADDRESS")
    [ "$code" = "200" ] || { echo "fake-tofu: DELETE devolvió $code" >&2; exit 1; }
    echo "Destroy complete! Resources: 12 destroyed."
    ;;
  output)
    case "$mod" in
      digitalocean/droplet-k3s)
        cat <<'JSON'
{
  "cluster_name": {"sensitive": false, "type": "string", "value": "do-nonprod"},
  "agent_installed": {"sensitive": false, "type": "bool", "value": true},
  "public_ip": {"sensitive": false, "type": "string", "value": "203.0.113.10"},
  "droplet_id": {"sensitive": false, "type": "string", "value": "123456"},
  "ssh_hint": {"sensitive": false, "type": "string", "value": "ssh root@203.0.113.10   # log de instalación: /var/log/rampa-install.log"},
  "dns_hint": {"sensitive": false, "type": "string", "value": "Apuntá un registro A comodín *.apps.dev.acme.com a 203.0.113.10."},
  "secreto": {"sensitive": true, "type": "string", "value": "no-deberia-salir"}
}
JSON
        ;;
      digitalocean/doks-cluster)
        cat <<'JSON'
{
  "cluster_name": {"sensitive": false, "type": "string", "value": "doks-nonprod"},
  "cluster_id": {"sensitive": false, "type": "string", "value": "0f1e2d3c"},
  "agent_installed": {"sensitive": false, "type": "bool", "value": true},
  "kubeconfig_command": {"sensitive": false, "type": "string", "value": "doctl kubernetes cluster kubeconfig save 0f1e2d3c"}
}
JSON
        ;;
      aws/ec2-k3s)
        cat <<'JSON'
{
  "cluster_name": {"sensitive": false, "type": "string", "value": "ec2-nonprod"},
  "agent_installed": {"sensitive": false, "type": "bool", "value": true},
  "instance_id": {"sensitive": false, "type": "string", "value": "i-0123456789abcdef0"},
  "public_ip": {"sensitive": false, "type": "string", "value": "198.51.100.7"},
  "agent_role_arn": {"sensitive": false, "type": "string", "value": "arn:aws:iam::123456789012:role/ec2-nonprod-node"},
  "ssh_hint": {"sensitive": false, "type": "string", "value": "SSH cerrado."},
  "dns_hint": {"sensitive": false, "type": "string", "value": "Apuntá un registro A comodín a 198.51.100.7."}
}
JSON
        ;;
      *)
        cat <<'JSON'
{
  "agent_role_arn": {"sensitive": false, "type": "string", "value": "arn:aws:iam::123456789012:role/rampa-nonprod-rampa-agent"},
  "cluster_name": {"sensitive": false, "type": "string", "value": "rampa-nonprod"},
  "kubeconfig_command": {"sensitive": false, "type": "string", "value": "aws eks update-kubeconfig --region us-east-1 --name rampa-nonprod"},
  "private_subnet_ids": {"sensitive": false, "type": ["list", "string"], "value": ["subnet-a", "subnet-b"]},
  "secreto": {"sensitive": true, "type": "string", "value": "no-deberia-salir"}
}
JSON
        ;;
    esac
    ;;
  *)
    echo "fake-tofu: subcomando desconocido: $*" >&2
    exit 2
    ;;
esac
