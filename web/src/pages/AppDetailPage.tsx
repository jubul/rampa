import { safeHttpUrl } from "../lib/url";
import { useMemo, useState, type FormEvent } from "react";
import { Trans, useTranslation } from "react-i18next";
import { Link, useNavigate, useParams } from "react-router-dom";
import { ArrowLeft, Check, Download, ExternalLink, FileCode2, KeyRound, Link2, Pencil, Plus, Rocket, Trash2, X } from "lucide-react";
import {
  useApp,
  useAppDeployments,
  useApproveDeployment,
  useBindScope,
  useCancelDeployment,
  useClusters,
  useCreateDeployment,
  useEvents,
  useOrg,
  usePutScopeSecrets,
  useRemoveApp,
  useRemoveScope,
  useScopeSecretKeys,
  useScopes,
  useUpdateManifest,
} from "../api/hooks";
import { useOrgRole } from "../auth/AuthContext";
import { useLiveEvents } from "../hooks/useOrgEvents";
import { eventBelongsToApp, mergeEvents } from "../lib/events";
import type { Application, Cluster, Scope } from "../api/types";
import { errorMessage } from "../api/client";
import { PageHeader } from "../components/ui/PageHeader";
import { Card, CardBody, CardHeader } from "../components/ui/Card";
import { Button } from "../components/ui/Button";
import { DownloadLink, LinkButton } from "../components/ui/LinkButton";
import { CodeBlock } from "../components/ui/CodeBlock";
import { Input, Textarea } from "../components/ui/Input";
import { Select } from "../components/ui/Select";
import { Field } from "../components/ui/Field";
import { Badge } from "../components/ui/Badge";
import { ConfirmDialog, Dialog } from "../components/ui/Dialog";
import { EmptyState, ErrorState, InlineError, LoadingState, Notice } from "../components/ui/States";
import { Table, TBody, TD, TH, THead, TR } from "../components/ui/Table";
import { StatusBadge } from "../components/StatusBadge";
import { EventList } from "../components/EventList";
import { TierBadge } from "./AppsPage";
import { scaffoldUrl } from "../api/client";
import { formatDateTime, formatRelative, shortId, shortImage } from "../lib/format";
import { useClusterTerms, useTerms } from "../hooks/useTerms";
import { ClusterKindBadge } from "../components/ClusterKindBadge";
import { isApiError } from "../api/client";
import { validateParamName } from "../lib/spec";
import { cap } from "../lib/terminology";

export function AppDetailPage() {
  const { t } = useTranslation();
  const { org = "", app = "" } = useParams();
  const navigate = useNavigate();
  const appQ = useApp(org, app);

  if (appQ.isPending) return <LoadingState label={t("appDetail.loading")} />;
  if (appQ.isError) {
    return (
      <>
        <BackLink org={org} />
        <ErrorState title={t("appDetail.loadError")} error={appQ.error} onRetry={() => void appQ.refetch()} />
      </>
    );
  }
  return <AppDetail org={org} application={appQ.data} onDeleted={() => navigate(`/o/${org}/apps`, { replace: true })} />;
}

function BackLink({ org }: { org: string }) {
  const { t } = useTranslation();
  return (
    <Link to={`/o/${org}/apps`} className="mb-3 inline-flex items-center gap-1 text-xs font-medium text-slate-500 dark:text-slate-400 hover:text-slate-900 dark:hover:text-slate-100">
      <ArrowLeft className="size-3.5" /> {t("nav.apps")}
    </Link>
  );
}

function AppDetail({ org, application, onDeleted }: { org: string; application: Application; onDeleted: () => void }) {
  const { t } = useTranslation();
  const clusterTerms = useClusterTerms(org);
  const generic = useTerms(org).terms();
  const app = application.name;
  const { isAdmin, isDeveloper } = useOrgRole(org);
  const orgQ = useOrg(org);
  const scopesQ = useScopes(org, app);
  const clustersQ = useClusters(org);
  const scopeNames = (scopesQ.data ?? []).map((s) => s.name);
  const historyQ = useAppDeployments(org, app, scopeNames);
  const eventsQ = useEvents(org, { limit: 100 });
  const live = useLiveEvents();

  const removeApp = useRemoveApp(org);
  const removeScope = useRemoveScope(org, app);
  const approve = useApproveDeployment(org);
  const cancel = useCancelDeployment(org);

  const [deployScope, setDeployScope] = useState<Scope | null>(null);
  const [bindScope, setBindScope] = useState<Scope | null>(null);
  const [scopeToDelete, setScopeToDelete] = useState<Scope | null>(null);
  const [manifestOpen, setManifestOpen] = useState(false);
  const [tab, setTab] = useState<"general" | "yaml">("general");
  const [deleteAppOpen, setDeleteAppOpen] = useState(false);
  const [actingOn, setActingOn] = useState<string | null>(null);

  const clusterById = useMemo(() => new Map((clustersQ.data ?? []).map((c) => [c.id, c])), [clustersQ.data]);

  // Ids de deployments conocidos, para filtrar eventos de la app.
  const deploymentIds = useMemo(() => {
    const ids = new Set<string>(historyQ.data.map((d) => d.id));
    for (const s of scopesQ.data ?? []) if (s.current_deployment) ids.add(s.current_deployment.id);
    return ids;
  }, [historyQ.data, scopesQ.data]);

  const appEvents = useMemo(
    () => mergeEvents(live.events, eventsQ.data, 200).filter((e) => eventBelongsToApp(e, application, deploymentIds)),
    [live.events, eventsQ.data, application, deploymentIds],
  );

  const requireApprovalProd = orgQ.data?.settings.require_approval_prod ?? true;

  return (
    <>
      <BackLink org={org} />
      <PageHeader
        title={
          <span className="flex flex-wrap items-center gap-2">
            {application.name}
            <TierBadge tier={application.tier} />
          </span>
        }
        description={application.description || t("appDetail.typeIn", { type: application.type, runtime: application.runtime })}
        actions={
          <>
            {isDeveloper ? (
              <LinkButton to={`/o/${org}/apps/${app}/edit`} icon={<Pencil className="size-4" />}>
                {t("appDetail.edit")}
              </LinkButton>
            ) : (
              <Button disabled title={t("common.needDeveloper")} icon={<Pencil className="size-4" />}>
                {t("appDetail.edit")}
              </Button>
            )}
            <Button variant="secondary" disabled={!isDeveloper} icon={<FileCode2 className="size-4" />} onClick={() => setManifestOpen(true)}>
              {t("appDetail.editYaml")}
            </Button>
            <Button variant="ghost" disabled={!isDeveloper} icon={<Trash2 className="size-4 text-red-600 dark:text-red-400" />} onClick={() => setDeleteAppOpen(true)}>
              {t("common.delete")}
            </Button>
          </>
        }
      />

      <Card>
        <CardBody>
          <dl className="grid gap-x-6 gap-y-3 text-sm sm:grid-cols-3 lg:grid-cols-6">
            <Meta label={t("apps.colTeam")} value={application.team} />
            <Meta label={t("apps.colType")} value={application.type} mono />
            <Meta label="Runtime" value={application.runtime || "-"} mono />
            <Meta label="Manifest" value={shortId(application.manifest_sha)} mono title={application.manifest_sha} />
            <Meta label={t("apps.colUpdated")} value={formatRelative(application.updated_at)} title={application.updated_at} />
            <div>
              <dt className="text-xs text-slate-500 dark:text-slate-400">{t("apps.colRepo")}</dt>
              <dd className="mt-0.5 truncate">
                {safeHttpUrl(application.repo_url) ? (
                  <a href={safeHttpUrl(application.repo_url)!} target="_blank" rel="noreferrer noopener" className="inline-flex items-center gap-1 text-slate-800 dark:text-slate-100 hover:underline">
                    <span className="truncate">{application.repo_url.replace(/^https?:\/\//, "")}</span>
                    <ExternalLink className="size-3 shrink-0" />
                  </a>
                ) : (
                  application.repo_url || "-"
                )}
              </dd>
            </div>
          </dl>
        </CardBody>
      </Card>

      <div className="mt-6 flex gap-1 border-b border-slate-200 dark:border-slate-800" role="tablist" aria-label={t("appDetail.sections")}>
        {([["general", t("appDetail.tabGeneral")], ["yaml", "YAML"]] as const).map(([key, label]) => (
          <button
            key={key}
            type="button"
            role="tab"
            aria-selected={tab === key}
            onClick={() => setTab(key)}
            className={`-mb-px border-b-2 px-4 py-2 text-sm font-medium ${tab === key ? "border-slate-900 text-slate-900 dark:text-slate-100" : "border-transparent text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-100"}`}
          >
            {label}
          </button>
        ))}
      </div>

      {tab === "yaml" && (
        <Card className="mt-6">
          <CardHeader title="rampa.yaml" description={t("appDetail.yamlDescription")} />
          <CardBody>{application.manifest ? <CodeBlock value={application.manifest} maxHeight="36rem" /> : <EmptyState title={t("appDetail.noManifest")} />}</CardBody>
        </Card>
      )}

      {tab === "general" && (
      <>
      <Card className="mt-6">
        <CardHeader title="Scopes" description={t("appDetail.scopesDescription", { cluster: generic.cluster })} />
        {scopesQ.isPending ? (
          <LoadingState />
        ) : scopesQ.isError ? (
          <ErrorState error={scopesQ.error} onRetry={() => void scopesQ.refetch()} />
        ) : scopesQ.data.length === 0 ? (
          <EmptyState title={t("appDetail.noScopes")} />
        ) : (
          <Table>
            <THead>
              <tr>
                <TH>Scope</TH>
                <TH>{t("appDetail.colCompute")}</TH>
                <TH>{t("dashboard.colStatus")}</TH>
                <TH>URL</TH>
                <TH>{t("appDetail.colImage")}</TH>
                <TH>{cap(generic.cluster)}</TH>
                <TH>{t("appDetail.colConfig")}</TH>
                <TH className="text-right">{t("clusters.colActions")}</TH>
              </tr>
            </THead>
            <TBody>
              {scopesQ.data.map((s) => {
                const cluster = s.cluster_id ? clusterById.get(s.cluster_id) : undefined;
                const canDeployHere = s.name === "prod" ? isAdmin : isDeveloper;
                const canDeleteHere = s.ephemeral ? isDeveloper : isAdmin;
                const busy = s.status === "deploying" || s.status === "deleting";
                return (
                  <TR key={s.name}>
                    <TD>
                      <div className="flex items-center gap-2 font-medium text-slate-900 dark:text-slate-100">
                        {s.name}
                        {s.ephemeral && <Badge tone="violet">{t("appDetail.ephemeral")}</Badge>}
                        {!s.declared && !s.ephemeral && <Badge tone="gray">{t("appDetail.undeclared")}</Badge>}
                      </div>
                      <div className="font-mono text-[11px] text-slate-500 dark:text-slate-400">{s.namespace}</div>
                      {s.ttl_expires_at && <div className="text-xs text-slate-500 dark:text-slate-400">{t("appDetail.expires", { when: formatRelative(s.ttl_expires_at) })}</div>}
                    </TD>
                    <TD>
                      <ComputeBadge compute={s.compute} />
                    </TD>
                    <TD>
                      <div className="flex flex-col gap-0.5">
                        <StatusBadge status={s.status} />
                        {s.current_deployment && s.current_deployment.status !== "healthy" && (
                          <span className="text-xs text-slate-500 dark:text-slate-400">{t("appDetail.deployStatus")} <StatusBadge status={s.current_deployment.status} /></span>
                        )}
                      </div>
                    </TD>
                    <TD>
                      {s.url ? (
                        <a href={s.url} target="_blank" rel="noreferrer noopener" className="inline-flex max-w-[14rem] items-center gap-1 text-xs text-slate-700 dark:text-slate-200 hover:underline">
                          <span className="truncate">{s.url.replace(/^https?:\/\//, "")}</span>
                          <ExternalLink className="size-3 shrink-0" />
                        </a>
                      ) : (
                        <span className="text-xs text-slate-500 dark:text-slate-400">-</span>
                      )}
                    </TD>
                    <TD className="font-mono text-xs" title={s.current_deployment?.image ?? ""}>
                      {shortImage(s.current_deployment?.image)}
                    </TD>
                    <TD>
                      {cluster ? (
                        <div className="flex flex-col items-start gap-1">
                          <span className="inline-flex items-center gap-1.5 text-xs">
                            <span className={`size-1.5 rounded-full ${cluster.status === "online" ? "bg-green-500" : "bg-slate-400"}`} aria-hidden />
                            {cluster.name}
                          </span>
                          <ClusterKindBadge cluster={cluster} terms={clusterTerms(cluster)} />
                        </div>
                      ) : s.cluster_id ? (
                        <span className="font-mono text-xs">{shortId(s.cluster_id)}</span>
                      ) : (
                        <span className="text-xs text-slate-500 dark:text-slate-400">{t("appDetail.unbound")}</span>
                      )}
                    </TD>
                    <TD className="text-xs text-slate-600 dark:text-slate-300">
                      {s.size} · {t("appDetail.replicas", { count: s.replicas })} · {s.strategy}
                    </TD>
                    <TD>
                      <div className="flex justify-end gap-1.5">
                        {!s.cluster_id && (
                          <Button size="sm" variant="secondary" disabled={!canDeployHere} title={!canDeployHere ? (s.name === "prod" ? t("appDetail.bindProdNeedsAdmin", { cluster: generic.cluster }) : t("common.needDeveloper")) : undefined} icon={<Link2 className="size-3.5" />} onClick={() => setBindScope(s)}>
                            {t("appDetail.bind", { cluster: generic.cluster })}
                          </Button>
                        )}
                        <Button
                          size="sm"
                          disabled={!canDeployHere || busy}
                          title={!canDeployHere ? (s.name === "prod" ? t("appDetail.deployProdNeedsAdmin") : t("common.needDeveloper")) : s.compute === "serverless" ? t("appDetail.serverlessTitle") : undefined}
                          icon={<Rocket className="size-3.5" />}
                          onClick={() => setDeployScope(s)}
                        >
                          {t("appDetail.deploy")}
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          disabled={!canDeleteHere || busy || s.status === "unbound" || s.status === "idle"}
                          title={!canDeleteHere ? t("appDetail.deleteScopeNeedsAdmin") : t("appDetail.deleteScopeHint")}
                          aria-label={t("appDetail.deleteScope")}
                          icon={<Trash2 className="size-3.5 text-red-600 dark:text-red-400" />}
                          onClick={() => setScopeToDelete(s)}
                        />
                      </div>
                    </TD>
                  </TR>
                );
              })}
            </TBody>
          </Table>
        )}
      </Card>

      <div className="mt-6 grid gap-6 lg:grid-cols-5">
        <Card className="lg:col-span-3">
          <CardHeader title={t("appDetail.history")} description={t("appDetail.historyDescription")} />
          {scopeNames.length > 0 && historyQ.isPending ? (
            <LoadingState />
          ) : historyQ.isError ? (
            <ErrorState error={historyQ.error} />
          ) : historyQ.data.length === 0 ? (
            <EmptyState title={t("dashboard.noDeployments")} description={t("appDetail.noDeploymentsHint")} />
          ) : (
            <Table>
              <THead>
                <tr>
                  <TH>{t("dashboard.colStatus")}</TH>
                  <TH>Scope</TH>
                  <TH>{t("dashboard.colImage")}</TH>
                  <TH>{t("infra.colWho")}</TH>
                  <TH>{t("dashboard.colWhen")}</TH>
                  <TH className="text-right">{t("clusters.colActions")}</TH>
                </tr>
              </THead>
              <TBody>
                {historyQ.data.map((d) => {
                  const canApprove = d.status === "awaiting_approval" && isAdmin;
                  const canCancel = (d.status === "awaiting_approval" || d.status === "queued") && (d.scope === "prod" ? isAdmin : isDeveloper);
                  const acting = actingOn === d.id;
                  return (
                    <TR key={d.id}>
                      <TD>
                        <div className="flex flex-col gap-0.5">
                          <StatusBadge status={d.status} />
                          {d.message && (
                            <span className="max-w-[16rem] truncate text-xs text-slate-500 dark:text-slate-400" title={d.message}>
                              {d.message}
                            </span>
                          )}
                          {d.workload && (
                            <span className="text-[11px] text-slate-500 dark:text-slate-400">
                              {t("appDetail.workloadReady", { kind: d.workload.kind, ready: d.workload.ready, desired: d.workload.desired })}
                            </span>
                          )}
                        </div>
                      </TD>
                      <TD>{d.scope}</TD>
                      <TD className="font-mono text-xs" title={d.image}>
                        {shortImage(d.image)}
                      </TD>
                      <TD>
                        <div className="text-xs">
                          <div className="text-slate-800 dark:text-slate-100">{d.requested_by?.name || d.requested_by?.email || "-"}</div>
                          {d.approved_by && <div className="text-slate-500 dark:text-slate-400">{t("appDetail.approvedBy", { who: d.approved_by.name || d.approved_by.email })}</div>}
                        </div>
                      </TD>
                      <TD className="text-xs text-slate-500 dark:text-slate-400" title={formatDateTime(d.created_at)}>
                        {formatRelative(d.created_at)}
                        {d.finished_at && <div>{t("infraRun.finished", { when: formatRelative(d.finished_at) })}</div>}
                      </TD>
                      <TD>
                        <div className="flex justify-end gap-1.5">
                          {d.status === "awaiting_approval" && (
                            <Button
                              size="sm"
                              disabled={!canApprove}
                              title={canApprove ? undefined : t("appDetail.approveNeedsAdmin")}
                              loading={acting && approve.isPending}
                              icon={<Check className="size-3.5" />}
                              onClick={() => {
                                setActingOn(d.id);
                                approve.mutate(d.id, { onSettled: () => setActingOn(null) });
                              }}
                            >
                              {t("appDetail.approve")}
                            </Button>
                          )}
                          {(d.status === "awaiting_approval" || d.status === "queued") && (
                            <Button
                              size="sm"
                              variant="secondary"
                              disabled={!canCancel}
                              loading={acting && cancel.isPending}
                              icon={<X className="size-3.5" />}
                              onClick={() => {
                                setActingOn(d.id);
                                cancel.mutate(d.id, { onSettled: () => setActingOn(null) });
                              }}
                            >
                              {t("common.cancel")}
                            </Button>
                          )}
                        </div>
                      </TD>
                    </TR>
                  );
                })}
              </TBody>
            </Table>
          )}
          {(approve.isError || cancel.isError) && (
            <div className="border-t border-slate-200 dark:border-slate-800 px-5 py-3">
              <InlineError error={approve.isError ? approve.error : cancel.error} />
            </div>
          )}
        </Card>

        <Card className="lg:col-span-2">
          <CardHeader title={t("appDetail.events")} description={live.state === "open" ? t("appDetail.eventsLive") : t("dashboard.activityWaiting")} />
          {eventsQ.isPending ? (
            <LoadingState />
          ) : eventsQ.isError ? (
            <ErrorState error={eventsQ.error} onRetry={() => void eventsQ.refetch()} />
          ) : (
            <div className="max-h-[70vh] overflow-y-auto">
              <EventList events={appEvents} emptyText={t("appDetail.noEvents")} />
            </div>
          )}
        </Card>
      </div>

      <div className="mt-6 grid gap-6 lg:grid-cols-2">
        {(scopesQ.data ?? []).map((s) => (
          <ScopeSecretsCard key={s.name} org={org} app={app} scope={s} cluster={s.cluster_id ? clusterById.get(s.cluster_id) : undefined} canWrite={s.name === "prod" ? isAdmin : isDeveloper} />
        ))}
      </div>

      <ScaffoldCard org={org} application={application} githubOrg={orgQ.data?.settings.github_org ?? ""} />
      </>
      )}

      <DeployDialog
        org={org}
        app={app}
        scope={deployScope}
        requireApprovalProd={requireApprovalProd}
        cluster={deployScope?.cluster_id ? clusterById.get(deployScope.cluster_id) : undefined}
        onClose={() => setDeployScope(null)}
      />

      <BindDialog org={org} app={app} scope={bindScope} clusters={clustersQ.data ?? []} onClose={() => setBindScope(null)} />

      <ConfirmDialog
        open={scopeToDelete !== null}
        onClose={() => {
          setScopeToDelete(null);
          removeScope.reset();
        }}
        title={scopeToDelete?.ephemeral ? t("appDetail.deleteEphemeral") : t("appDetail.deletePermanent")}
        description={
          scopeToDelete
            ? `${t("appDetail.deleteScopeBody", { namespace: scopeToDelete.namespace })}${scopeToDelete.ephemeral ? "" : ` ${t("appDetail.deleteScopePermanent", { scope: scopeToDelete.name })}`}`
            : undefined
        }
        confirmLabel={t("appDetail.deleteScope")}
        danger
        loading={removeScope.isPending}
        error={removeScope.isError ? errorMessage(removeScope.error) : null}
        onConfirm={() => {
          if (!scopeToDelete) return;
          removeScope.mutate(scopeToDelete.name, { onSuccess: () => setScopeToDelete(null) });
        }}
      />

      <ManifestDialog org={org} app={app} current={application.manifest} open={manifestOpen} onClose={() => setManifestOpen(false)} />

      <ConfirmDialog
        open={deleteAppOpen}
        onClose={() => {
          setDeleteAppOpen(false);
          removeApp.reset();
        }}
        title={t("appDetail.deleteApp")}
        description={t("appDetail.deleteAppDescription", { app })}
        confirmLabel={t("appDetail.deleteApp")}
        danger
        loading={removeApp.isPending}
        error={removeApp.isError ? errorMessage(removeApp.error) : null}
        onConfirm={() => removeApp.mutate(app, { onSuccess: onDeleted })}
      />
    </>
  );
}

function Meta({ label, value, mono = false, title }: { label: string; value: string; mono?: boolean; title?: string }) {
  return (
    <div>
      <dt className="text-xs text-slate-500 dark:text-slate-400">{label}</dt>
      <dd className={`mt-0.5 truncate text-slate-800 dark:text-slate-100 ${mono ? "font-mono text-xs" : ""}`} title={title ?? value}>
        {value}
      </dd>
    </div>
  );
}

function DeployDialog({
  org,
  app,
  scope,
  requireApprovalProd,
  cluster,
  onClose,
}: {
  org: string;
  app: string;
  scope: Scope | null;
  requireApprovalProd: boolean;
  cluster: Cluster | undefined;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  return (
    <Dialog open={scope !== null} onClose={onClose} title={scope ? t("appDetail.deployTitle", { app, scope: scope.name }) : t("appDetail.deploy")}>
      {scope && <DeployForm key={scope.name} org={org} app={app} scope={scope} requireApprovalProd={requireApprovalProd} cluster={cluster} onClose={onClose} />}
    </Dialog>
  );
}

function DeployForm({
  org,
  app,
  scope,
  requireApprovalProd,
  cluster,
  onClose,
}: {
  org: string;
  app: string;
  scope: Scope;
  requireApprovalProd: boolean;
  cluster: Cluster | undefined;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const ct = useClusterTerms(org)(cluster);
  const registryTerm = ct.registry;
  const clusterTerm = cluster?.kind === "vm" ? ct.vm : ct.cluster;
  const create = useCreateDeployment(org, app);
  const [image, setImage] = useState(scope.current_deployment?.image ?? "");
  const [message, setMessage] = useState("");
  const [confirm, setConfirm] = useState(false);
  const isProd = scope.name === "prod";

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const img = image.trim();
    if (!img) return;
    if (!confirm) {
      setConfirm(true);
      return;
    }
    const body = message.trim() ? { image: img, message: message.trim() } : { image: img };
    create.mutate({ scope: scope.name, body }, { onSuccess: onClose });
  };

  return (
    <form onSubmit={submit} className="space-y-4">
      <Field label={t("dashboard.colImage")} required help={t("appDetail.imageHelp", { registry: registryTerm })}>
        {(id) => <Input id={id} required autoFocus spellCheck={false} placeholder="sha-a1b2c3d" value={image} onChange={(e) => { setImage(e.target.value); setConfirm(false); }} className="font-mono text-xs" />}
      </Field>
      <Field label={t("appDetail.message")} help={t("appDetail.messageHelp")}>
        {(id) => <Input id={id} placeholder={t("appDetail.messagePlaceholder")} value={message} onChange={(e) => setMessage(e.target.value)} />}
      </Field>
      <div className="rounded-md border border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-slate-800/60 p-3 text-xs text-slate-600 dark:text-slate-300">
        <div>
          {t("appDetail.target")} <span className="font-medium text-slate-900 dark:text-slate-100">{scope.namespace}</span>
          {cluster ? ` ${t("appDetail.inCluster", { name: cluster.name, status: cluster.status === "online" ? t("status.online").toLowerCase() : t("status.offline").toLowerCase() })}` : scope.cluster_id ? "" : ` ${t("appDetail.noCluster", { cluster: clusterTerm })}`}
        </div>
        <div className="mt-0.5">
          {t("appDetail.strategyLine", { strategy: scope.strategy, size: scope.size, replicas: t("appDetail.replicas", { count: scope.replicas }) })}
        </div>
      </div>
      {scope.compute === "serverless" && (
        <Notice tone="amber">
          <strong>{t("appDetail.serverlessTitle")}</strong> {t("appDetail.serverlessBody")}
        </Notice>
      )}
      {isProd && requireApprovalProd && <Notice tone="blue">{t("appDetail.prodApproval")}</Notice>}
      {cluster && cluster.status === "offline" && <Notice tone="amber">{t("appDetail.agentOffline", { cluster: clusterTerm })}</Notice>}
      {confirm && (
        <Notice tone={isProd ? "amber" : "green"}>
          <Trans i18nKey="appDetail.confirmDeploy" values={{ image: shortImage(image.trim()), scope: scope.name }} components={{ code: <code className="font-mono" />, strong: <strong /> }} />
        </Notice>
      )}
      {create.isError && (isApiError(create.error) && create.error.status === 501 ? <Notice tone="amber">{t("appDetail.serverlessBody")} <span className="block text-xs">{create.error.message}</span></Notice> : <InlineError error={create.error} />)}
      <div className="flex justify-end gap-2">
        <Button variant="secondary" onClick={onClose}>
          {t("common.cancel")}
        </Button>
        <Button type="submit" loading={create.isPending} icon={<Rocket className="size-4" />}>
          {confirm ? t("appDetail.confirmDeployButton") : t("appDetail.deploy")}
        </Button>
      </div>
    </form>
  );
}

function BindDialog({ org, app, scope, clusters, onClose }: { org: string; app: string; scope: Scope | null; clusters: Cluster[]; onClose: () => void }) {
  const { t } = useTranslation();
  const generic = useTerms(org).terms();
  const bind = useBindScope(org, app);
  const [clusterId, setClusterId] = useState("");
  const wantedEnv = scope?.name === "prod" ? "prod" : "nonprod";
  const options = clusters.filter((c) => c.env === wantedEnv).map((c) => ({ value: c.id, label: `${c.name} (${c.domain})` }));

  return (
    <Dialog
      open={scope !== null}
      onClose={() => {
        setClusterId("");
        bind.reset();
        onClose();
      }}
      title={scope ? t("appDetail.bindTitle", { scope: scope.name, cluster: generic.cluster }) : t("appDetail.bind", { cluster: generic.cluster })}
      description={t("appDetail.bindDescription", { env: wantedEnv === "prod" ? t("env.prod") : t("env.nonprod") })}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            disabled={!clusterId}
            loading={bind.isPending}
            onClick={() => {
              if (!scope || !clusterId) return;
              bind.mutate({ scope: scope.name, clusterId }, { onSuccess: () => { setClusterId(""); onClose(); } });
            }}
          >
            {t("appDetail.bindConfirm")}
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        {options.length === 0 ? (
          <Notice tone="amber">
            {t("appDetail.noClustersOfEnv", { cluster: generic.cluster, env: wantedEnv === "prod" ? t("env.prod") : t("env.nonprod") })}{" "}
            <Link to={`/o/${org}/clusters`} className="font-medium underline">
              {t("appDetail.createOne")}
            </Link>
          </Notice>
        ) : (
          <Field label={cap(generic.cluster)} required>
            {(id) => <Select id={id} value={clusterId} onChange={(e) => setClusterId(e.target.value)} placeholder={t("appDetail.pickCluster", { cluster: generic.cluster })} options={options} />}
          </Field>
        )}
        {bind.isError && <InlineError error={bind.error} />}
      </div>
    </Dialog>
  );
}

function ManifestDialog({ org, app, current, open, onClose }: { org: string; app: string; current: string; open: boolean; onClose: () => void }) {
  const { t } = useTranslation();
  return (
    <Dialog open={open} onClose={onClose} title={t("appDetail.manifestTitle")} description={t("appDetail.manifestDescription")} size="lg">
      {open && <ManifestForm org={org} app={app} current={current} onClose={onClose} />}
    </Dialog>
  );
}

function ManifestForm({ org, app, current, onClose }: { org: string; app: string; current: string; onClose: () => void }) {
  const { t } = useTranslation();
  const update = useUpdateManifest(org, app);
  const [manifest, setManifest] = useState(current);
  const dirty = manifest !== current;
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (!dirty) return;
        update.mutate(manifest, { onSuccess: onClose });
      }}
      className="space-y-4"
    >
      <Textarea rows={22} spellCheck={false} value={manifest} onChange={(e) => setManifest(e.target.value)} className="font-mono text-xs" aria-label="rampa.yaml" />
      {update.isError && <InlineError error={update.error} />}
      <div className="flex justify-end gap-2">
        <Button variant="secondary" onClick={onClose}>
          {t("common.close")}
        </Button>
        <Button type="submit" disabled={!dirty} loading={update.isPending}>
          {t("appDetail.saveManifest")}
        </Button>
      </div>
    </form>
  );
}

function ScaffoldCard({ org, application, githubOrg }: { org: string; application: Application; githubOrg: string }) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState<string | null>(null);
  const value = draft ?? githubOrg;
  const supported = application.type === "api" && application.runtime === "go";
  return (
    <Card className="mt-6">
      <CardHeader title={t("appDetail.scaffold")} description={t("appDetail.scaffoldDescription")} />
      <CardBody className="space-y-4">
        <div className="flex flex-wrap items-end gap-3">
          <Field label={t("settings.githubOrg")} help={t("appDetail.githubOrgHelp")}>
            {(id) => <Input id={id} className="w-64" value={value} placeholder="acme" onChange={(e) => setDraft(e.target.value)} />}
          </Field>
          <div className="pb-6">
            <DownloadLink href={scaffoldUrl(org, application.name, value)} icon={<Download className="size-4" />}>
              {t("appDetail.downloadScaffold")}
            </DownloadLink>
          </div>
        </div>
        {!supported && (
          <Notice tone="amber">{t("appDetail.scaffoldUnsupported", { type: application.type, runtime: application.runtime || t("appDetail.noRuntime") })}</Notice>
        )}
        <ol className="list-inside list-decimal space-y-1 text-sm text-slate-700 dark:text-slate-200">
          <li>{t("appDetail.scaffoldStep1")}</li>
          <li>{t("appDetail.scaffoldStep2")}</li>
          <li>{t("appDetail.scaffoldStep3")}</li>
        </ol>
      </CardBody>
    </Card>
  );
}

function ComputeBadge({ compute }: { compute: Scope["compute"] }) {
  const { t } = useTranslation();
  const c = compute ?? "kubernetes";
  return <Badge tone={c === "serverless" ? "violet" : "blue"}>{t(`appDetail.compute.${c}`)}</Badge>;
}

interface SecretRow {
  key: string;
  value: string;
}

/**
 * Secretos de un scope. La API nunca devuelve valores: solo lista las claves
 * conocidas. El formulario agrega o reemplaza valores con PUT .../secrets.
 * Los valores viajan a la API y de ahí al agente; no se muestran ni se guardan acá.
 */
function ScopeSecretsCard({ org, app, scope, cluster, canWrite }: { org: string; app: string; scope: Scope; cluster: Cluster | undefined; canWrite: boolean }) {
  const { t } = useTranslation();
  const ct = useClusterTerms(org)(cluster);
  const keysQ = useScopeSecretKeys(org, app, scope.name);
  const put = usePutScopeSecrets(org, app, scope.name);
  const [rows, setRows] = useState<SecretRow[]>([{ key: "", value: "" }]);
  const [touched, setTouched] = useState(false);
  const [sent, setSent] = useState<string[] | null>(null);

  const setRow = (i: number, patch: Partial<SecretRow>) => {
    setSent(null);
    setRows((cur) => cur.map((r, idx) => (idx === i ? { ...r, ...patch } : r)));
  };
  const filled = rows.filter((r) => r.key !== "" || r.value !== "");
  const rowErrors = rows.map((r, i) => {
    if (r.key === "" && r.value === "") return null;
    const keyErr = validateParamName(r.key);
    if (keyErr) return keyErr;
    if (r.value === "") return t("secrets.valueRequired");
    if (rows.findIndex((o) => o.key === r.key) !== i) return t("secrets.duplicateKey");
    return null;
  });
  const valid = filled.length > 0 && rowErrors.every((e) => e === null);
  const known = keysQ.data ?? [];

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (!canWrite || !valid) return;
    const values: Record<string, string> = {};
    for (const r of filled) values[r.key] = r.value;
    put.mutate(values, {
      onSuccess: (res) => {
        setSent(res?.keys ?? Object.keys(values));
        setRows([{ key: "", value: "" }]);
        setTouched(false);
      },
    });
  };

  const description =
    cluster?.secret_store === "kubernetes"
      ? t("secrets.descriptionKubernetes", { app })
      : cluster
        ? t("secrets.descriptionCloud", { store: ct.secretStore })
        : t("secrets.descriptionGeneric");

  return (
    <Card>
      <CardHeader
        title={t("secrets.title", { scope: scope.name })}
        description={description}
        actions={scope.name === "prod" ? <Badge tone="blue">{t("secrets.adminsOnly")}</Badge> : undefined}
      />
      <CardBody className="space-y-4">
        <div>
          <p className="mb-1.5 text-xs font-medium text-slate-600 dark:text-slate-300">{t("secrets.knownKeys")}</p>
          {keysQ.isPending ? (
            <LoadingState />
          ) : keysQ.isError ? (
            <InlineError error={keysQ.error} />
          ) : known.length === 0 ? (
            <p className="text-xs text-slate-500 dark:text-slate-400">{t("secrets.noKeys")}</p>
          ) : (
            <ul className="flex flex-wrap gap-1.5">
              {known.map((k) => (
                <li key={k}>
                  <Badge tone="gray">
                    <KeyRound className="size-3" aria-hidden />
                    <span className="font-mono">{k}</span>
                  </Badge>
                </li>
              ))}
            </ul>
          )}
          <p className="mt-1.5 text-xs text-slate-500 dark:text-slate-400">{t("secrets.neverShown")}</p>
        </div>

        {!canWrite && <Notice tone="blue">{scope.name === "prod" ? t("secrets.prodNeedsAdmin") : t("common.needDeveloper")}</Notice>}

        <form onSubmit={submit} className="space-y-3" autoComplete="off">
          {rows.map((r, i) => (
            <div key={i} className="grid gap-2 sm:grid-cols-5">
              <div className="sm:col-span-2">
                <Input
                  aria-label={t("secrets.keyLabel")}
                  list={`secret-keys-${scope.name}`}
                  placeholder="API_KEY"
                  spellCheck={false}
                  autoComplete="off"
                  disabled={!canWrite}
                  aria-invalid={touched && rowErrors[i] !== null}
                  className="font-mono text-xs"
                  value={r.key}
                  onChange={(e) => setRow(i, { key: e.target.value.toUpperCase() })}
                />
              </div>
              <div className="sm:col-span-3">
                <Input
                  type="password"
                  aria-label={t("secrets.valueLabel")}
                  placeholder={t("secrets.valuePlaceholder")}
                  autoComplete="new-password"
                  disabled={!canWrite}
                  aria-invalid={touched && rowErrors[i] !== null}
                  className="font-mono text-xs"
                  value={r.value}
                  onChange={(e) => setRow(i, { value: e.target.value })}
                />
              </div>
              {touched && rowErrors[i] && <p className="text-xs text-red-600 dark:text-red-400 sm:col-span-5">{rowErrors[i]}</p>}
            </div>
          ))}
          <datalist id={`secret-keys-${scope.name}`}>
            {known.map((k) => (
              <option key={k} value={k} />
            ))}
          </datalist>
          <div className="flex flex-wrap items-center justify-between gap-2">
            <Button variant="ghost" size="sm" disabled={!canWrite} icon={<Plus className="size-3.5" />} onClick={() => setRows((cur) => [...cur, { key: "", value: "" }])}>
              {t("secrets.addAnother")}
            </Button>
            <Button type="submit" size="sm" disabled={!canWrite} loading={put.isPending} icon={<KeyRound className="size-3.5" />}>
              {t("secrets.save")}
            </Button>
          </div>
          <p className="text-xs text-slate-500 dark:text-slate-400">{t("secrets.replaceHint")}</p>
        </form>

        {sent && (
          <Notice tone="green">{t("secrets.sent", { count: sent.length, keys: sent.join(", ") })}</Notice>
        )}
        {put.isError &&
          (isApiError(put.error) && put.error.isConflict ? (
            <Notice tone="amber">
              <p className="font-medium">{t("secrets.agentOfflineTitle", { cluster: ct.cluster })}</p>
              <p className="mt-0.5">{t("secrets.agentOfflineBody")}</p>
            </Notice>
          ) : (
            <InlineError error={put.error} />
          ))}
      </CardBody>
    </Card>
  );
}
