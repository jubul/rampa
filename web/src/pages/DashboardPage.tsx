import { Link, useParams } from "react-router-dom";
import { ArrowRight, Boxes, Package, Rocket } from "lucide-react";
import { useApps, useClusters, useEvents, useOrg, useScopesForApps } from "../api/hooks";
import { mergeEvents, useLiveEvents } from "../hooks/useOrgEvents";
import { PageHeader } from "../components/ui/PageHeader";
import { Card, CardHeader } from "../components/ui/Card";
import { EmptyState, ErrorState, LoadingState } from "../components/ui/States";
import { Table, TBody, TD, TH, THead, TR } from "../components/ui/Table";
import { StatusBadge } from "../components/StatusBadge";
import { EventList } from "../components/EventList";
import { Badge } from "../components/ui/Badge";
import { formatRelative, shortImage } from "../lib/format";
import { envLabels } from "../lib/status";
import type { Deployment } from "../api/types";

export function DashboardPage() {
  const { org = "" } = useParams();
  const orgQ = useOrg(org);
  const clustersQ = useClusters(org);
  const appsQ = useApps(org);
  const appNames = (appsQ.data ?? []).map((a) => a.name);
  const scopesQ = useScopesForApps(org, appNames);
  const eventsQ = useEvents(org, { limit: 50 });
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
            ? `${orgQ.data.slug} · aprobación en prod ${orgQ.data.settings.require_approval_prod ? "requerida" : "no requerida"}`
            : undefined
        }
      />

      <div className="grid gap-4 sm:grid-cols-3">
        <Stat icon={<Boxes className="size-4" />} label="Clusters en línea" value={clustersQ.data ? `${online} / ${clustersQ.data.length}` : "-"} to={`/o/${org}/clusters`} />
        <Stat icon={<Package className="size-4" />} label="Aplicaciones" value={appsQ.data ? String(appsQ.data.length) : "-"} to={`/o/${org}/apps`} />
        <Stat
          icon={<Rocket className="size-4" />}
          label="Scopes sanos"
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
              title="Clusters"
              actions={
                <Link to={`/o/${org}/clusters`} className="inline-flex items-center gap-1 text-xs font-medium text-slate-600 hover:text-slate-900">
                  Ver todos <ArrowRight className="size-3" />
                </Link>
              }
            />
            {clustersQ.isPending ? (
              <LoadingState />
            ) : clustersQ.isError ? (
              <ErrorState error={clustersQ.error} onRetry={() => void clustersQ.refetch()} />
            ) : clustersQ.data.length === 0 ? (
              <EmptyState
                title="No hay clusters"
                description="Conectá una cuenta de nube y creá un cluster para instalar el agente."
                action={
                  <Link to={`/o/${org}/clusters`} className="text-sm font-medium text-slate-900 underline">
                    Ir a clusters
                  </Link>
                }
              />
            ) : (
              <Table>
                <THead>
                  <tr>
                    <TH>Nombre</TH>
                    <TH>Entorno</TH>
                    <TH>Estado</TH>
                    <TH>Última señal</TH>
                  </tr>
                </THead>
                <TBody>
                  {clustersQ.data.map((c) => (
                    <TR key={c.id}>
                      <TD className="font-medium text-slate-900">{c.name}</TD>
                      <TD>
                        <Badge tone={c.env === "prod" ? "blue" : "gray"}>{envLabels[c.env] ?? c.env}</Badge>
                      </TD>
                      <TD>
                        <StatusBadge status={c.status} />
                      </TD>
                      <TD className="text-slate-500">{formatRelative(c.last_seen_at)}</TD>
                    </TR>
                  ))}
                </TBody>
              </Table>
            )}
          </Card>

          <Card>
            <CardHeader title="Últimos deployments" description="El deployment actual de cada scope, del más reciente al más viejo." />
            {appsQ.isPending || (appNames.length > 0 && scopesQ.isPending) ? (
              <LoadingState />
            ) : appsQ.isError ? (
              <ErrorState error={appsQ.error} onRetry={() => void appsQ.refetch()} />
            ) : scopesQ.isError ? (
              <ErrorState error={scopesQ.error} />
            ) : recentDeployments.length === 0 ? (
              <EmptyState title="Todavía no hay deployments" description="Importá una aplicación y desplegala a un scope." />
            ) : (
              <Table>
                <THead>
                  <tr>
                    <TH>App</TH>
                    <TH>Scope</TH>
                    <TH>Imagen</TH>
                    <TH>Estado</TH>
                    <TH>Cuándo</TH>
                  </tr>
                </THead>
                <TBody>
                  {recentDeployments.map((d) => (
                    <TR key={d.id}>
                      <TD>
                        <Link to={`/o/${org}/apps/${d.app}`} className="font-medium text-slate-900 hover:underline">
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
                      <TD className="text-slate-500" title={d.created_at}>
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
          <CardHeader title="Actividad" description={live.state === "open" ? "Actualizada en vivo" : "Esperando conexión en vivo"} />
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
    <Link to={to} className="block rounded-lg border border-slate-200 bg-white px-5 py-4 shadow-sm hover:border-slate-300">
      <div className="flex items-center gap-2 text-xs font-medium text-slate-500">
        <span className="text-slate-400">{icon}</span>
        {label}
      </div>
      <div className="mt-1.5 text-2xl font-semibold tracking-tight text-slate-900">{value}</div>
    </Link>
  );
}
