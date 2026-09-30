import { useTranslation } from "react-i18next";
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
  const { t } = useTranslation();
  const { org = "" } = useParams();
  const runsQ = useInfraRuns(org);
  const accountsQ = useCloudAccounts(org);
  const accountName = (id: string) => accountsQ.data?.find((a) => a.id === id)?.name ?? id.slice(0, 8);

  const newButton = (
    <LinkButton to={`/o/${org}/infra/new-cluster`} icon={<Plus className="size-4" />}>
      {t("infra.create")}
    </LinkButton>
  );

  return (
    <>
      <PageHeader
        title={t("nav.infra")}
        description={t("infra.description")}
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
            title={t("infra.emptyTitle")}
            description={t("infra.emptyDescription")}
            action={newButton}
          />
        ) : (
          <Table>
            <THead>
              <tr>
                <TH>{t("infra.colStatus")}</TH>
                <TH>{t("infra.colName")}</TH>
                <TH>{t("infra.colModule")}</TH>
                <TH>{t("infra.colAccount")}</TH>
                <TH>{t("infra.colWho")}</TH>
                <TH>{t("infra.colWhen")}</TH>
              </tr>
            </THead>
            <TBody>
              {runsQ.data.map((r) => (
                <TR key={r.id}>
                  <TD>
                    <RunStatusBadge status={r.status} />
                  </TD>
                  <TD>
                    <Link to={`/o/${org}/infra/runs/${r.id}`} className="font-medium text-slate-900 dark:text-slate-100 hover:underline">
                      {r.name}
                    </Link>
                  </TD>
                  <TD className="font-mono text-xs">{r.module}</TD>
                  <TD>{accountName(r.cloud_account_id)}</TD>
                  <TD className="text-xs">{r.requested_by?.name || r.requested_by?.email || "-"}</TD>
                  <TD className="text-xs text-slate-500 dark:text-slate-400" title={formatDateTime(r.created_at)}>
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
