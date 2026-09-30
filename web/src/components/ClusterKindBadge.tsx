import { Badge } from "./ui/Badge";
import type { Cluster } from "../api/types";
import type { Terms } from "../lib/terminology";

/**
 * Tipo de cluster con el término del proveedor: un cluster de Kubernetes se
 * llama "cluster EKS" o "cluster DOKS"; una máquina con k3s, "instancia EC2" o
 * "Droplet". Sin proveedor en contexto usa los términos genéricos.
 */
export function ClusterKindBadge({ cluster, terms }: { cluster: Pick<Cluster, "kind">; terms: Terms }) {
  const vm = cluster.kind === "vm";
  return <Badge tone={vm ? "violet" : "blue"}>{vm ? terms.vm : terms.cluster}</Badge>;
}
