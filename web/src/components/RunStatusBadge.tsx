import { Badge } from "./ui/Badge";
import { isRunActive, labelForRun, toneForRun } from "../lib/infra";

export function RunStatusBadge({ status }: { status: string }) {
  return (
    <Badge tone={toneForRun(status)} pulse={isRunActive(status)}>
      {labelForRun(status)}
    </Badge>
  );
}
