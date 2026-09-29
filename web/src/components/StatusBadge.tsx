import { Badge } from "./ui/Badge";
import { isDeploymentActive, labelForStatus, toneForStatus } from "../lib/status";

export function StatusBadge({ status }: { status: string | null | undefined }) {
  return (
    <Badge tone={toneForStatus(status)} pulse={status ? isDeploymentActive(status) || status === "deploying" : false}>
      {labelForStatus(status)}
    </Badge>
  );
}
