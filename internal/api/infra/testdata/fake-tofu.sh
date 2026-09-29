#!/usr/bin/env bash
# tofu falso para los tests del runner. Imita lo justo de OpenTofu:
#   - imprime líneas por stdout y stderr,
#   - en apply guarda un estado en el backend http usando TF_HTTP_* (LOCK, POST, UNLOCK)
#     con Basic auth, que es lo que hace el backend real con TF_HTTP_USERNAME/PASSWORD,
#   - en destroy borra el estado,
#   - responde a "output -json" con un JSON fijo.
# Nunca imprime el valor de las credenciales: los tests verifican que no aparezcan en el log.
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

case "$sub" in
  init)
    echo "Initializing the backend..."
    require_env TF_HTTP_ADDRESS TF_HTTP_LOCK_ADDRESS TF_HTTP_UNLOCK_ADDRESS TF_HTTP_PASSWORD TF_IN_AUTOMATION
    [ -f main.tf ] || { echo "fake-tofu: no hay main.tf en $(pwd)" >&2; exit 3; }
    [ -f "$tfvars" ] || { echo "fake-tofu: no hay $tfvars" >&2; exit 3; }
    [ -f modules/aws/eks-cluster/main.tf ] || { echo "fake-tofu: falta el módulo embebido" >&2; exit 3; }
    [ -f charts/rampa-agent/Chart.yaml ] || { echo "fake-tofu: falta el chart del agente" >&2; exit 3; }
    echo "OpenTofu has been successfully initialized!"
    ;;
  plan)
    require_env AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN AWS_REGION TF_VAR_agent_token
    echo "credenciales presentes: AWS_ACCESS_KEY_ID=${AWS_ACCESS_KEY_ID:+si} AWS_REGION=$AWS_REGION"
    echo "agent_chart_path: $(grep -o '"agent_chart_path": *"[^"]*"' "$tfvars")"
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
    state='{"version":4,"terraform_version":"1.12.0","serial":1,"lineage":"fake","outputs":{},"resources":[{"type":"aws_eks_cluster","name":"this"}]}'
    code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "${auth[@]}" -H 'Content-Type: application/json' -d "$state" "$TF_HTTP_ADDRESS?ID=fake-lock-1")
    [ "$code" = "200" ] || { echo "fake-tofu: POST devolvió $code" >&2; exit 1; }
    code=$(curl -sS -o /dev/null -w '%{http_code}' -X GET "${auth[@]}" "$TF_HTTP_ADDRESS")
    [ "$code" = "200" ] || { echo "fake-tofu: GET devolvió $code" >&2; exit 1; }
    code=$(curl -sS -o /dev/null -w '%{http_code}' -X UNLOCK "${auth[@]}" -d '{"ID":"fake-lock-1"}' "$TF_HTTP_UNLOCK_ADDRESS")
    [ "$code" = "200" ] || { echo "fake-tofu: UNLOCK devolvió $code" >&2; exit 1; }
    echo "module.cluster.aws_eks_cluster.this: Creating..."
    echo "module.cluster.helm_release.rampa_agent[0]: Creation complete"
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
  *)
    echo "fake-tofu: subcomando desconocido: $*" >&2
    exit 2
    ;;
esac
