import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useParams } from "react-router-dom";
import { CheckCircle2, Circle } from "lucide-react";
import { useApps, useClusters, useOnboarding } from "../api/hooks";
import { useOrgRole } from "../auth/AuthContext";
import type { Cluster, OnboardingStep } from "../api/types";
import { PageHeader } from "../components/ui/PageHeader";
import { Card, CardBody } from "../components/ui/Card";
import { LinkButton } from "../components/ui/LinkButton";
import { Badge } from "../components/ui/Badge";
import { ErrorState, LoadingState, Notice } from "../components/ui/States";
import { InstallCommandPanel } from "../components/InstallCommandPanel";
import { cn } from "../lib/cn";
import { stepTitle } from "../lib/onboarding";

export function OnboardingPage() {
  const { t } = useTranslation();
  const { org = "" } = useParams();
  const onboardingQ = useOnboarding(org);
  const clustersQ = useClusters(org);
  const appsQ = useApps(org);
  const { isAdmin } = useOrgRole(org);

  if (onboardingQ.isPending) return <LoadingState label={t("onboarding.loading")} />;
  if (onboardingQ.isError) return <ErrorState title={t("onboarding.loadError")} error={onboardingQ.error} onRetry={() => void onboardingQ.refetch()} />;

  const { steps, next, complete } = onboardingQ.data;
  const done = steps.filter((s) => s.done).length;
  const offline = (clustersQ.data ?? []).find((c) => c.status === "offline");
  const firstApp = appsQ.data?.[0];

  const actions = (s: OnboardingStep): ReactNode => {
    switch (s.key) {
      case "cloud":
        return <LinkButton variant={s.done ? "secondary" : "primary"} to={`/o/${org}/cloud-accounts?connect=1`}>{s.done ? t("onboarding.connectAnother") : t("onboarding.connect")}</LinkButton>;
      case "cluster":
        return (
          <>
            <LinkButton variant={s.done ? "secondary" : "primary"} to={`/o/${org}/infra/new-cluster`}>
              {t("onboarding.createCluster")}
            </LinkButton>
            <LinkButton variant="secondary" to={`/o/${org}/clusters?new=1`}>
              {t("onboarding.haveCluster")}
            </LinkButton>
          </>
        );
      case "agent":
        return <AgentHelp org={org} done={s.done} cluster={offline} loading={clustersQ.isPending} canView={isAdmin} />;
      case "app":
        return <LinkButton variant={s.done ? "secondary" : "primary"} to={`/o/${org}/apps/new`}>{s.done ? t("onboarding.createAnotherApp") : t("onboarding.createFirstApp")}</LinkButton>;
      case "deploy":
        return firstApp ? (
          <LinkButton variant={s.done ? "secondary" : "primary"} to={`/o/${org}/apps/${firstApp.name}`}>
            {s.done ? t("onboarding.viewApp") : t("onboarding.firstDeploy")}
          </LinkButton>
        ) : (
          <span className="text-xs text-slate-500 dark:text-slate-400">{t("onboarding.createAppFirst")}</span>
        );
      default:
        return null;
    }
  };

  return (
    <>
      <PageHeader
        title={t("nav.getStarted")}
        description={complete ? t("onboarding.allDone") : t("onboarding.progress", { done, total: steps.length })}
      />
      {complete && <Notice tone="green">{t("onboarding.completeNotice")}</Notice>}
      <ol className="mt-4 space-y-3">
        {steps.map((s, i) => (
          <li key={s.key}>
            <Card className={cn(s.key === next && !complete && "ring-2 ring-slate-900 dark:ring-slate-200")}>
              <CardBody className="flex flex-wrap items-start gap-4">
                <div className="mt-0.5" aria-hidden>
                  {s.done ? <CheckCircle2 className="size-6 text-green-600 dark:text-green-400" /> : <Circle className="size-6 text-slate-300 dark:text-slate-600" />}
                </div>
                <div className="min-w-0 flex-1 space-y-2">
                  <div className="flex flex-wrap items-center gap-2">
                    <h2 className="text-sm font-semibold text-slate-900 dark:text-slate-100">
                      {i + 1}. {stepTitle(t, s.key, s.title)}
                    </h2>
                    <Badge tone={s.done ? "green" : s.key === next ? "amber" : "gray"}>{s.done ? t("onboarding.done") : s.key === next ? t("onboarding.next") : t("onboarding.pending")}</Badge>
                  </div>
                  {s.detail && <p className="text-xs text-slate-500 dark:text-slate-400">{s.detail}</p>}
                  <div className="flex flex-wrap items-start gap-2">{actions(s)}</div>
                </div>
              </CardBody>
            </Card>
          </li>
        ))}
      </ol>
    </>
  );
}

function AgentHelp({ org, done, cluster, loading, canView }: { org: string; done: boolean; cluster: Cluster | undefined; loading: boolean; canView: boolean }) {
  const { t } = useTranslation();
  return (
    <div className="w-full space-y-3">
      <p className="text-xs text-slate-600 dark:text-slate-300">{t("onboarding.agentHelp")}</p>
      {done && !cluster && <p className="text-xs text-green-700 dark:text-green-400">{t("onboarding.clusterOnline")}</p>}
      {!loading && cluster && (
        <div className="space-y-2 rounded-md border border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-slate-800/60 p-3">
          <p className="text-xs font-medium text-slate-700 dark:text-slate-200">
            {cluster.infra_run_id ? t("onboarding.offlineCreatedByRampa", { name: cluster.name }) : t("onboarding.offline", { name: cluster.name })}
          </p>
          {cluster.infra_run_id && (
            <LinkButton size="sm" variant="secondary" to={`/o/${org}/infra/runs/${cluster.infra_run_id}`}>
              {t("onboarding.viewRun")}
            </LinkButton>
          )}
          <InstallCommandPanel org={org} cluster={cluster} canView={canView} />
        </div>
      )}
      {!loading && !cluster && !done && <p className="text-xs text-slate-500 dark:text-slate-400">{t("onboarding.noOfflineCluster")}</p>}
    </div>
  );
}
