import { safeHttpUrl } from "../lib/url";
import { useMemo, useState, type FormEvent } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { ArrowLeft, Check, Download, ExternalLink, FileCode2, Link2, Pencil, Rocket, Trash2, X } from "lucide-react";
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
  useRemoveApp,
  useRemoveScope,
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

export function AppDetailPage() {
  const { org = "", app = "" } = useParams();
  const navigate = useNavigate();
  const appQ = useApp(org, app);

  if (appQ.isPending) return <LoadingState label="Cargando aplicación" />;
  if (appQ.isError) {
    return (
      <>
        <BackLink org={org} />
        <ErrorState title="No se pudo cargar la aplicación" error={appQ.error} onRetry={() => void appQ.refetch()} />
      </>
    );
  }
  return <AppDetail org={org} application={appQ.data} onDeleted={() => navigate(`/o/${org}/apps`, { replace: true })} />;
}

function BackLink({ org }: { org: string }) {
  return (
    <Link to={`/o/${org}/apps`} className="mb-3 inline-flex items-center gap-1 text-xs font-medium text-slate-500 hover:text-slate-900">
      <ArrowLeft className="size-3.5" /> Aplicaciones
    </Link>
  );
}

function AppDetail({ org, application, onDeleted }: { org: string; application: Application; onDeleted: () => void }) {
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
        description={application.description || `${application.type} en ${application.runtime}`}
        actions={
          <>
            {isDeveloper ? (
              <LinkButton to={`/o/${org}/apps/${app}/edit`} icon={<Pencil className="size-4" />}>
                Editar
              </LinkButton>
            ) : (
              <Button disabled title="Necesitás rol developer" icon={<Pencil className="size-4" />}>
                Editar
              </Button>
            )}
            <Button variant="secondary" disabled={!isDeveloper} icon={<FileCode2 className="size-4" />} onClick={() => setManifestOpen(true)}>
              Editar YAML
            </Button>
            <Button variant="ghost" disabled={!isDeveloper} icon={<Trash2 className="size-4 text-red-600" />} onClick={() => setDeleteAppOpen(true)}>
              Eliminar
            </Button>
          </>
        }
      />

      <Card>
        <CardBody>
          <dl className="grid gap-x-6 gap-y-3 text-sm sm:grid-cols-3 lg:grid-cols-6">
            <Meta label="Equipo" value={application.team} />
            <Meta label="Tipo" value={application.type} mono />
            <Meta label="Runtime" value={application.runtime || "-"} mono />
            <Meta label="Manifest" value={shortId(application.manifest_sha)} mono title={application.manifest_sha} />
            <Meta label="Actualizada" value={formatRelative(application.updated_at)} title={application.updated_at} />
            <div>
              <dt className="text-xs text-slate-500">Repositorio</dt>
              <dd className="mt-0.5 truncate">
                {safeHttpUrl(application.repo_url) ? (
                  <a href={safeHttpUrl(application.repo_url)!} target="_blank" rel="noreferrer noopener" className="inline-flex items-center gap-1 text-slate-800 hover:underline">
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

      <div className="mt-6 flex gap-1 border-b border-slate-200" role="tablist" aria-label="Secciones de la aplicación">
        {([["general", "General"], ["yaml", "YAML"]] as const).map(([key, label]) => (
          <button
            key={key}
            type="button"
            role="tab"
            aria-selected={tab === key}
            onClick={() => setTab(key)}
            className={`-mb-px border-b-2 px-4 py-2 text-sm font-medium ${tab === key ? "border-slate-900 text-slate-900" : "border-transparent text-slate-500 hover:text-slate-800"}`}
          >
            {label}
          </button>
        ))}
      </div>

      {tab === "yaml" && (
        <Card className="mt-6">
          <CardHeader title="rampa.yaml" description="El manifest guardado, en solo lectura. Para cambiarlo usá Editar (asistente) o Editar YAML." />
          <CardBody>{application.manifest ? <CodeBlock value={application.manifest} maxHeight="36rem" /> : <EmptyState title="Sin manifest" />}</CardBody>
        </Card>
      )}

      {tab === "general" && (
      <>
      <Card className="mt-6">
        <CardHeader title="Scopes" description="Cada scope es un namespace propio en un cluster." />
        {scopesQ.isPending ? (
          <LoadingState />
        ) : scopesQ.isError ? (
          <ErrorState error={scopesQ.error} onRetry={() => void scopesQ.refetch()} />
        ) : scopesQ.data.length === 0 ? (
          <EmptyState title="El manifest no declara scopes" />
        ) : (
          <Table>
            <THead>
              <tr>
                <TH>Scope</TH>
                <TH>Estado</TH>
                <TH>URL</TH>
                <TH>Imagen actual</TH>
                <TH>Cluster</TH>
                <TH>Config</TH>
                <TH className="text-right">Acciones</TH>
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
                      <div className="flex items-center gap-2 font-medium text-slate-900">
                        {s.name}
                        {s.ephemeral && <Badge tone="violet">efímero</Badge>}
                        {!s.declared && !s.ephemeral && <Badge tone="gray">no declarado</Badge>}
                      </div>
                      <div className="font-mono text-[11px] text-slate-500">{s.namespace}</div>
                      {s.ttl_expires_at && <div className="text-xs text-slate-500">expira {formatRelative(s.ttl_expires_at)}</div>}
                    </TD>
                    <TD>
                      <div className="flex flex-col gap-0.5">
                        <StatusBadge status={s.status} />
                        {s.current_deployment && s.current_deployment.status !== "healthy" && (
                          <span className="text-xs text-slate-500">deploy: <StatusBadge status={s.current_deployment.status} /></span>
                        )}
                      </div>
                    </TD>
                    <TD>
                      {s.url ? (
                        <a href={s.url} target="_blank" rel="noreferrer noopener" className="inline-flex max-w-[14rem] items-center gap-1 text-xs text-slate-700 hover:underline">
                          <span className="truncate">{s.url.replace(/^https?:\/\//, "")}</span>
                          <ExternalLink className="size-3 shrink-0" />
                        </a>
                      ) : (
                        <span className="text-xs text-slate-400">-</span>
                      )}
                    </TD>
                    <TD className="font-mono text-xs" title={s.current_deployment?.image ?? ""}>
                      {shortImage(s.current_deployment?.image)}
                    </TD>
                    <TD>
                      {cluster ? (
                        <span className="inline-flex items-center gap-1.5 text-xs">
                          <span className={`size-1.5 rounded-full ${cluster.status === "online" ? "bg-green-500" : "bg-slate-400"}`} aria-hidden />
                          {cluster.name}
                        </span>
                      ) : s.cluster_id ? (
                        <span className="font-mono text-xs">{shortId(s.cluster_id)}</span>
                      ) : (
                        <span className="text-xs text-slate-400">sin atar</span>
                      )}
                    </TD>
                    <TD className="text-xs text-slate-600">
                      {s.size} · {s.replicas} réplica{s.replicas === 1 ? "" : "s"} · {s.strategy}
                    </TD>
                    <TD>
                      <div className="flex justify-end gap-1.5">
                        {!s.cluster_id && (
                          <Button size="sm" variant="secondary" disabled={!canDeployHere} title={!canDeployHere ? (s.name === "prod" ? "Atar cluster a prod requiere admin" : "Necesitás rol developer") : undefined} icon={<Link2 className="size-3.5" />} onClick={() => setBindScope(s)}>
                            Atar cluster
                          </Button>
                        )}
                        <Button
                          size="sm"
                          disabled={!canDeployHere || busy}
                          title={!canDeployHere ? (s.name === "prod" ? "Desplegar a prod requiere admin" : "Necesitás rol developer") : undefined}
                          icon={<Rocket className="size-3.5" />}
                          onClick={() => setDeployScope(s)}
                        >
                          Desplegar
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          disabled={!canDeleteHere || busy || s.status === "unbound" || s.status === "idle"}
                          title={!canDeleteHere ? "Eliminar un scope permanente requiere admin" : "Eliminar el scope (borra el namespace)"}
                          aria-label="Eliminar scope"
                          icon={<Trash2 className="size-3.5 text-red-600" />}
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
          <CardHeader title="Historial de deployments" description="Todos los scopes, del más nuevo al más viejo." />
          {scopeNames.length > 0 && historyQ.isPending ? (
            <LoadingState />
          ) : historyQ.isError ? (
            <ErrorState error={historyQ.error} />
          ) : historyQ.data.length === 0 ? (
            <EmptyState title="Todavía no hay deployments" description="Usá Desplegar en un scope para empezar." />
          ) : (
            <Table>
              <THead>
                <tr>
                  <TH>Estado</TH>
                  <TH>Scope</TH>
                  <TH>Imagen</TH>
                  <TH>Quién</TH>
                  <TH>Cuándo</TH>
                  <TH className="text-right">Acciones</TH>
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
                            <span className="max-w-[16rem] truncate text-xs text-slate-500" title={d.message}>
                              {d.message}
                            </span>
                          )}
                          {d.workload && (
                            <span className="text-[11px] text-slate-500">
                              {d.workload.kind}: {d.workload.ready}/{d.workload.desired} listos
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
                          <div className="text-slate-800">{d.requested_by?.name || d.requested_by?.email || "-"}</div>
                          {d.approved_by && <div className="text-slate-500">aprobó {d.approved_by.name || d.approved_by.email}</div>}
                        </div>
                      </TD>
                      <TD className="text-xs text-slate-500" title={formatDateTime(d.created_at)}>
                        {formatRelative(d.created_at)}
                        {d.finished_at && <div>terminó {formatRelative(d.finished_at)}</div>}
                      </TD>
                      <TD>
                        <div className="flex justify-end gap-1.5">
                          {d.status === "awaiting_approval" && (
                            <Button
                              size="sm"
                              disabled={!canApprove}
                              title={canApprove ? undefined : "Aprobar requiere admin"}
                              loading={acting && approve.isPending}
                              icon={<Check className="size-3.5" />}
                              onClick={() => {
                                setActingOn(d.id);
                                approve.mutate(d.id, { onSettled: () => setActingOn(null) });
                              }}
                            >
                              Aprobar
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
                              Cancelar
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
            <div className="border-t border-slate-200 px-5 py-3">
              <InlineError error={approve.isError ? approve.error : cancel.error} />
            </div>
          )}
        </Card>

        <Card className="lg:col-span-2">
          <CardHeader title="Eventos" description={live.state === "open" ? "En vivo, filtrados por esta app" : "Esperando conexión en vivo"} />
          {eventsQ.isPending ? (
            <LoadingState />
          ) : eventsQ.isError ? (
            <ErrorState error={eventsQ.error} onRetry={() => void eventsQ.refetch()} />
          ) : (
            <div className="max-h-[70vh] overflow-y-auto">
              <EventList events={appEvents} emptyText="Sin eventos para esta aplicación" />
            </div>
          )}
        </Card>
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
        title={scopeToDelete?.ephemeral ? "Eliminar scope efímero" : "Eliminar scope permanente"}
        description={
          scopeToDelete
            ? `Se va a borrar el namespace ${scopeToDelete.namespace} con todo lo que tiene. ${scopeToDelete.ephemeral ? "" : "Es un scope permanente: la app deja de estar disponible en " + scopeToDelete.name + "."}`
            : undefined
        }
        confirmLabel="Eliminar scope"
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
        title="Eliminar aplicación"
        description={`Se va a quitar "${app}" del catálogo. Si tiene scopes desplegados la API lo va a rechazar.`}
        confirmLabel="Eliminar aplicación"
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
      <dt className="text-xs text-slate-500">{label}</dt>
      <dd className={`mt-0.5 truncate text-slate-800 ${mono ? "font-mono text-xs" : ""}`} title={title ?? value}>
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
  return (
    <Dialog open={scope !== null} onClose={onClose} title={scope ? `Desplegar ${app} a ${scope.name}` : "Desplegar"}>
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
      <Field label="Imagen" required help="Un tag (sha-a1b2c3d) o la referencia completa del registry.">
        {(id) => <Input id={id} required autoFocus spellCheck={false} placeholder="sha-a1b2c3d" value={image} onChange={(e) => { setImage(e.target.value); setConfirm(false); }} className="font-mono text-xs" />}
      </Field>
      <Field label="Mensaje" help="Opcional. Queda en el historial.">
        {(id) => <Input id={id} placeholder="Hotfix del checkout" value={message} onChange={(e) => setMessage(e.target.value)} />}
      </Field>
      <div className="rounded-md border border-slate-200 bg-slate-50 p-3 text-xs text-slate-600">
        <div>
          Destino: <span className="font-medium text-slate-900">{scope.namespace}</span>
          {cluster ? ` en ${cluster.name} (${cluster.status === "online" ? "en línea" : "desconectado"})` : scope.cluster_id ? "" : " (sin cluster: la API intenta elegir el único del entorno)"}
        </div>
        <div className="mt-0.5">
          Estrategia {scope.strategy} · {scope.size} · {scope.replicas} réplica{scope.replicas === 1 ? "" : "s"}
        </div>
      </div>
      {isProd && requireApprovalProd && <Notice tone="blue">Es producción: el deployment va a quedar esperando la aprobación de un admin.</Notice>}
      {cluster && cluster.status === "offline" && <Notice tone="amber">El agente del cluster está desconectado. El comando queda encolado hasta que vuelva.</Notice>}
      {confirm && (
        <Notice tone={isProd ? "amber" : "green"}>
          Vas a desplegar <code className="font-mono">{shortImage(image.trim())}</code> a <strong>{scope.name}</strong>. Confirmá para continuar.
        </Notice>
      )}
      {create.isError && <InlineError error={create.error} />}
      <div className="flex justify-end gap-2">
        <Button variant="secondary" onClick={onClose}>
          Cancelar
        </Button>
        <Button type="submit" loading={create.isPending} icon={<Rocket className="size-4" />}>
          {confirm ? "Confirmar deploy" : "Desplegar"}
        </Button>
      </div>
    </form>
  );
}

function BindDialog({ org, app, scope, clusters, onClose }: { org: string; app: string; scope: Scope | null; clusters: Cluster[]; onClose: () => void }) {
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
      title={scope ? `Atar ${scope.name} a un cluster` : "Atar a un cluster"}
      description={`Solo se listan clusters ${wantedEnv === "prod" ? "prod" : "no prod"}.`}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Cancelar
          </Button>
          <Button
            disabled={!clusterId}
            loading={bind.isPending}
            onClick={() => {
              if (!scope || !clusterId) return;
              bind.mutate({ scope: scope.name, clusterId }, { onSuccess: () => { setClusterId(""); onClose(); } });
            }}
          >
            Atar
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        {options.length === 0 ? (
          <Notice tone="amber">
            No hay clusters {wantedEnv === "prod" ? "prod" : "no prod"}.{" "}
            <Link to={`/o/${org}/clusters`} className="font-medium underline">
              Creá uno.
            </Link>
          </Notice>
        ) : (
          <Field label="Cluster" required>
            {(id) => <Select id={id} value={clusterId} onChange={(e) => setClusterId(e.target.value)} placeholder="Elegí un cluster" options={options} />}
          </Field>
        )}
        {bind.isError && <InlineError error={bind.error} />}
      </div>
    </Dialog>
  );
}

function ManifestDialog({ org, app, current, open, onClose }: { org: string; app: string; current: string; open: boolean; onClose: () => void }) {
  return (
    <Dialog open={open} onClose={onClose} title="Manifest de la aplicación" description="Reemplazá el rampa.yaml. El nombre no puede cambiar." size="lg">
      {open && <ManifestForm org={org} app={app} current={current} onClose={onClose} />}
    </Dialog>
  );
}

function ManifestForm({ org, app, current, onClose }: { org: string; app: string; current: string; onClose: () => void }) {
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
          Cerrar
        </Button>
        <Button type="submit" disabled={!dirty} loading={update.isPending}>
          Guardar manifest
        </Button>
      </div>
    </form>
  );
}

function ScaffoldCard({ org, application, githubOrg }: { org: string; application: Application; githubOrg: string }) {
  const [draft, setDraft] = useState<string | null>(null);
  const value = draft ?? githubOrg;
  const supported = application.type === "api" && application.runtime === "go";
  return (
    <Card className="mt-6">
      <CardHeader title="Proyecto inicial" description="Código de partida del golden path, con el rampa.yaml real de esta app y los workflows de CI." />
      <CardBody className="space-y-4">
        <div className="flex flex-wrap items-end gap-3">
          <Field label="Organización de GitHub" help="Donde vive tu copia de rampa. Si la dejás vacía se usa la de la organización, o CHANGEME.">
            {(id) => <Input id={id} className="w-64" value={value} placeholder="acme" onChange={(e) => setDraft(e.target.value)} />}
          </Field>
          <div className="pb-6">
            <DownloadLink href={scaffoldUrl(org, application.name, value)} icon={<Download className="size-4" />}>
              Descargar proyecto inicial
            </DownloadLink>
          </div>
        </div>
        {!supported && (
          <Notice tone="amber">Hoy el proyecto inicial existe solo para apps de tipo api con runtime go. Esta app es {application.type} en {application.runtime || "sin runtime"}, así que la descarga puede fallar.</Notice>
        )}
        <ol className="list-inside list-decimal space-y-1 text-sm text-slate-700">
          <li>Descomprimí el zip.</li>
          <li>Creá un repositorio en GitHub con el código descomprimido y subilo.</li>
          <li>Hacé push a main: el workflow construye la imagen y despliega a dev.</li>
        </ol>
      </CardBody>
    </Card>
  );
}
