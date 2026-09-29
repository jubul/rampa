import { Link, useParams } from "react-router-dom";
import { Layers, Plus } from "lucide-react";
import { useCloudAccounts, useInfraRuns } from "../api/hooks";
import { PageHeader } from "../components/ui/PageHeader";
import { Card } from "../components/ui/Card";
import { LinkButton } from "../components/ui/LinkButton";
import { EmptyState, ErrorState, LoadingState } from "../components/ui/States";
import { Table, TBody, TD, TH, THead, TR } from "../components/ui/Table";
import { RunStatusBadge } from "../components/RunStatusBadge";
import { formatDateTime, formatRelative } from "../lib/format";

export function InfraPage() {
  const { org = "" } = useParams();
  const runsQ = useInfraRuns(org);
  const accountsQ = useCloudAccounts(org);
  const accountName = (id: string) => accountsQ.data?.find((a) => a.id === id)?.name ?? id.slice(0, 8);

  const newButton = (
    <LinkButton to={`/o/${org}/infra/new-cluster`} icon={<Plus className="size-4" />}>
      Crear cluster
    </LinkButton>
  );

  return (
    <>
      <PageHeader
        title="Infraestructura"
        description="Rampa crea la infraestructura en tu nube con OpenTofu y guarda el registro de cada ejecución."
        actions={newButton}
      />
      <Card>
        {runsQ.isPending ? (
          <LoadingState />
        ) : runsQ.isError ? (
          <ErrorState error={runsQ.error} onRetry={() => void runsQ.refetch()} />
        ) : runsQ.data.length === 0 ? (
          <EmptyState
            icon={<Layers className="size-8" />}
            title="Todavía no hay ejecuciones"
            description="Creá un cluster con un click: Rampa levanta la red, el cluster y el agente en tu cuenta de nube."
            action={newButton}
          />
        ) : (
          <Table>
            <THead>
              <tr>
                <TH>Estado</TH>
                <TH>Nombre</TH>
                <TH>Módulo</TH>
                <TH>Cuenta</TH>
                <TH>Quién</TH>
                <TH>Cuándo</TH>
              </tr>
            </THead>
            <TBody>
              {runsQ.data.map((r) => (
                <TR key={r.id}>
                  <TD>
                    <RunStatusBadge status={r.status} />
                  </TD>
                  <TD>
                    <Link to={`/o/${org}/infra/runs/${r.id}`} className="font-medium text-slate-900 hover:underline">
                      {r.name}
                    </Link>
                  </TD>
                  <TD className="font-mono text-xs">{r.module}</TD>
                  <TD>{accountName(r.cloud_account_id)}</TD>
                  <TD className="text-xs">{r.requested_by?.name || r.requested_by?.email || "-"}</TD>
                  <TD className="text-xs text-slate-500" title={formatDateTime(r.created_at)}>
                    {formatRelative(r.created_at)}
                  </TD>
                </TR>
              ))}
            </TBody>
          </Table>
        )}
      </Card>
    </>
  );
}
