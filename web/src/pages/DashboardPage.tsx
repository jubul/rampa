import { useTranslation } from "react-i18next";
import { Link, useParams } from "react-router-dom";
import { ArrowRight, Boxes, ListChecks, Package, Rocket } from "lucide-react";
import { useApps, useClusters, useEvents, useOnboarding, useOrg, useScopesForApps } from "../api/hooks";
import { mergeEvents, useLiveEvents } from "../hooks/useOrgEvents";
import { PageHeader } from "../components/ui/PageHeader";
import { Card, CardHeader } from "../components/ui/Card";
import { EmptyState, ErrorState, LoadingState } from "../components/ui/States";
import { Table, TBody, TD, TH, THead, TR } from "../components/ui/Table";
import { StatusBadge } from "../components/StatusBadge";
import { EventList } from "../components/EventList";
import { Badge } from "../components/ui/Badge";
import { formatRelative, shortImage } from "../lib/format";
import { envLabel } from "../lib/status";
import { useClusterTerms } from "../hooks/useTerms";
import { stepTitle } from "../lib/onboarding";
import { ClusterKindBadge } from "../components/ClusterKindBadge";
import type { Deployment } from "../api/types";

export function DashboardPage() {
  const { t } = useTranslation();
  const { org = "" } = useParams();
  const clusterTerms = useClusterTerms(org);
  const orgQ = useOrg(org);
  const clustersQ = useClusters(org);
  const appsQ = useApps(org);
  const appNames = (appsQ.data ?? []).map((a) => a.name);
  const scopesQ = useScopesForApps(org, appNames);
  const eventsQ = useEvents(org, { limit: 50 });
  const onboardingQ = useOnboarding(org);
  const live = useLiveEvents();

  const recentDeployments: Deployment[] = scopesQ.data
    .map((s) => s.scope.current_deployment)
    .filter((d): d is Deployment => d !== null)
    .sort((a, b) => (a.created_at < b.created_at ? 1 : -1))
    .slice(0, 10);

  const online = (clustersQ.data ?? []).filter((c) => c.status === "online").length;

  return (
    <>
      <PageHeader
        title={orgQ.data?.name ?? org}
        description={
          orgQ.data
            ? `${orgQ.data.slug} · ${orgQ.data.settings.require_approval_prod ? t("dashboard.approvalRequired") : t("dashboard.approvalNotRequired")}`
            : undefined
        }
      />

      {onboardingQ.data && !onboardingQ.data.complete && <OnboardingCard org={org} missing={onboardingQ.data.steps.filter((s) => !s.done).length} nextTitle={stepTitle(t, onboardingQ.data.next)} />}

      <div className="grid gap-4 sm:grid-cols-3">
        <Stat icon={<Boxes className="size-4" />} label={t("dashboard.clustersOnline")} value={clustersQ.data ? `${online} / ${clustersQ.data.length}` : "-"} to={`/o/${org}/clusters`} />
        <Stat icon={<Package className="size-4" />} label={t("nav.apps")} value={appsQ.data ? String(appsQ.data.length) : "-"} to={`/o/${org}/apps`} />
        <Stat
          icon={<Rocket className="size-4" />}
          label={t("dashboard.healthyScopes")}
          value={
            appsQ.data && !scopesQ.isPending
              ? `${scopesQ.data.filter((s) => s.scope.status === "healthy").length} / ${scopesQ.data.length}`
              : "-"
          }
          to={`/o/${org}/apps`}
        />
      </div>

      <div className="mt-6 grid gap-6 lg:grid-cols-5">
        <div className="space-y-6 lg:col-span-3">
          <Card>
            <CardHeader
              title={t("nav.clusters")}
              actions={
                <Link to={`/o/${org}/clusters`} className="inline-flex items-center gap-1 text-xs font-medium text-slate-600 dark:text-slate-300 hover:text-slate-900 dark:hover:text-slate-100">
                  {t("dashboard.viewAll")} <ArrowRight className="size-3" />
                </Link>
              }
            />
            {clustersQ.isPending ? (
              <LoadingState />
            ) : clustersQ.isError ? (
              <ErrorState error={clustersQ.error} onRetry={() => void clustersQ.refetch()} />
            ) : clustersQ.data.length === 0 ? (
              <EmptyState
                title={t("dashboard.noClusters")}
                description={t("dashboard.noClustersDescription")}
                action={
                  <Link to={`/o/${org}/clusters`} className="text-sm font-medium text-slate-900 dark:text-slate-100 underline">
                    {t("dashboard.goToClusters")}
                  </Link>
                }
              />
            ) : (
              <Table>
                <THead>
                  <tr>
                    <TH>{t("dashboard.colName")}</TH>
                    <TH>{t("dashboard.colType")}</TH>
                    <TH>{t("dashboard.colEnv")}</TH>
                    <TH>{t("dashboard.colStatus")}</TH>
                    <TH>{t("dashboard.colLastSeen")}</TH>
                  </tr>
                </THead>
                <TBody>
                  {clustersQ.data.map((c) => (
                    <TR key={c.id}>
                      <TD className="font-medium text-slate-900 dark:text-slate-100">{c.name}</TD>
                      <TD>
                        <ClusterKindBadge cluster={c} terms={clusterTerms(c)} />
                      </TD>
                      <TD>
                        <Badge tone={c.env === "prod" ? "blue" : "gray"}>{envLabel(c.env)}</Badge>
                      </TD>
                      <TD>
                        <StatusBadge status={c.status} />
                      </TD>
                      <TD className="text-slate-500 dark:text-slate-400">{formatRelative(c.last_seen_at)}</TD>
                    </TR>
                  ))}
                </TBody>
              </Table>
            )}
          </Card>

          <Card>
            <CardHeader title={t("dashboard.recentDeployments")} description={t("dashboard.recentDeploymentsDescription")} />
            {appsQ.isPending || (appNames.length > 0 && scopesQ.isPending) ? (
              <LoadingState />
            ) : appsQ.isError ? (
              <ErrorState error={appsQ.error} onRetry={() => void appsQ.refetch()} />
            ) : scopesQ.isError ? (
              <ErrorState error={scopesQ.error} />
            ) : recentDeployments.length === 0 ? (
              <EmptyState title={t("dashboard.noDeployments")} description={t("dashboard.noDeploymentsDescription")} />
            ) : (
              <Table>
                <THead>
                  <tr>
                    <TH>{t("dashboard.colApp")}</TH>
                    <TH>{t("dashboard.colScope")}</TH>
                    <TH>{t("dashboard.colImage")}</TH>
                    <TH>{t("dashboard.colStatus")}</TH>
                    <TH>{t("dashboard.colWhen")}</TH>
                  </tr>
                </THead>
                <TBody>
                  {recentDeployments.map((d) => (
                    <TR key={d.id}>
                      <TD>
                        <Link to={`/o/${org}/apps/${d.app}`} className="font-medium text-slate-900 dark:text-slate-100 hover:underline">
                          {d.app}
                        </Link>
                      </TD>
                      <TD>{d.scope}</TD>
                      <TD className="font-mono text-xs" title={d.image}>
                        {shortImage(d.image)}
                      </TD>
                      <TD>
                        <StatusBadge status={d.status} />
                      </TD>
                      <TD className="text-slate-500 dark:text-slate-400" title={d.created_at}>
                        {formatRelative(d.created_at)}
                      </TD>
                    </TR>
                  ))}
                </TBody>
              </Table>
            )}
          </Card>
        </div>

        <Card className="lg:col-span-2">
          <CardHeader title={t("dashboard.activity")} description={live.state === "open" ? t("dashboard.activityLive") : t("dashboard.activityWaiting")} />
          {eventsQ.isPending ? (
            <LoadingState />
          ) : eventsQ.isError ? (
            <ErrorState error={eventsQ.error} onRetry={() => void eventsQ.refetch()} />
          ) : (
            <div className="max-h-[70vh] overflow-y-auto">
              <EventList events={mergeEvents(live.events, eventsQ.data, 60)} />
            </div>
          )}
        </Card>
      </div>
    </>
  );
}

function Stat({ icon, label, value, to }: { icon: React.ReactNode; label: string; value: string; to: string }) {
  return (
    <Link to={to} className="block rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 px-5 py-4 shadow-sm hover:border-slate-300 dark:hover:border-slate-700">
      <div className="flex items-center gap-2 text-xs font-medium text-slate-500 dark:text-slate-400">
        <span className="text-slate-500 dark:text-slate-400">{icon}</span>
        {label}
      </div>
      <div className="mt-1.5 text-2xl font-semibold tracking-tight text-slate-900 dark:text-slate-100">{value}</div>
    </Link>
  );
}

function OnboardingCard({ org, missing, nextTitle }: { org: string; missing: number; nextTitle?: string }) {
  const { t } = useTranslation();
  return (
    <Link
      to={`/o/${org}/empezar`}
      className="mb-4 flex items-center gap-3 rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 px-4 py-3 text-amber-900 dark:text-amber-200 hover:bg-amber-100 dark:hover:bg-amber-900/40"
    >
      <ListChecks className="size-5 shrink-0" aria-hidden />
      <div className="min-w-0 flex-1">
        <div className="text-sm font-semibold">
          {t("dashboard.missingSteps", { count: missing })}
        </div>
        <div className="truncate text-xs">{nextTitle ? t("dashboard.nextStep", { title: nextTitle }) : t("dashboard.finishSetup")}</div>
      </div>
      <span className="inline-flex items-center gap-1 text-xs font-medium">
        {t("nav.getStarted")} <ArrowRight className="size-3" />
      </span>
    </Link>
  );
}
