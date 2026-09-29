import { useEffect, useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Pause, Play, RotateCcw, Trash2 } from "lucide-react";
import { keys, useClusters, useCloudAccounts, useDestroyInfraRun, useInfraRun, useRetryInfraRun } from "../api/hooks";
import { errorMessage } from "../api/client";
import { useOrgRole } from "../auth/AuthContext";
import { useInfraLog } from "../hooks/useInfraLog";
import type { InfraRun } from "../api/types";
import { PageHeader } from "../components/ui/PageHeader";
import { Card, CardBody, CardHeader } from "../components/ui/Card";
import { Button } from "../components/ui/Button";
import { Input } from "../components/ui/Input";
import { CopyField } from "../components/ui/CopyField";
import { Dialog } from "../components/ui/Dialog";
import { ErrorState, InlineError, LoadingState, Notice } from "../components/ui/States";
import { RunStatusBadge } from "../components/RunStatusBadge";
import { InstallCommandPanel } from "../components/InstallCommandPanel";
import { StatusBadge } from "../components/StatusBadge";
import { formatDateTime, formatRelative } from "../lib/format";
import { isRunActive, outputEntries } from "../lib/infra";

export function InfraRunPage() {
  const { org = "", id = "" } = useParams();
  const runQ = useInfraRun(org, id);

  return (
    <>
      <Link to={`/o/${org}/infra`} className="mb-3 inline-flex items-center gap-1 text-xs font-medium text-slate-500 hover:text-slate-900">
        <ArrowLeft className="size-3.5" /> Infraestructura
      </Link>
      {runQ.isPending ? (
        <LoadingState label="Cargando la ejecución" />
      ) : runQ.isError ? (
        <ErrorState title="No se pudo cargar la ejecución" error={runQ.error} onRetry={() => void runQ.refetch()} />
      ) : (
        <RunDetail org={org} run={runQ.data} />
      )}
    </>
  );
}

function RunDetail({ org, run }: { org: string; run: InfraRun }) {
  const qc = useQueryClient();
  const { isAdmin } = useOrgRole(org);
  const accountsQ = useCloudAccounts(org);
  const clustersQ = useClusters(org);
  const retry = useRetryInfraRun(org);
  const destroy = useDestroyInfraRun(org);
  const [destroyOpen, setDestroyOpen] = useState(false);
  const [confirmText, setConfirmText] = useState("");

  const refresh = () => {
    void qc.invalidateQueries({ queryKey: keys.infraRun(org, run.id) });
    void qc.invalidateQueries({ queryKey: keys.infraRuns(org) });
    void qc.invalidateQueries({ queryKey: keys.clusters(org) });
  };
  const log = useInfraLog(org, run.id, run.log_tail ?? [], isRunActive(run.status), { onStatus: refresh, onDone: refresh });

  const status = run.status;
  const account = accountsQ.data?.find((a) => a.id === run.cloud_account_id);
  const cluster = clustersQ.data?.find((c) => c.id === run.cluster_id);
  const outputs = outputEntries(run);
  const installFromOutputs = typeof run.outputs?.["install_command"] === "string" ? (run.outputs["install_command"] as string) : "";

  return (
    <>
      <PageHeader
        title={
          <span className="flex flex-wrap items-center gap-2">
            {run.name}
            <RunStatusBadge status={status} />
          </span>
        }
        description={`Ejecución ${run.id.slice(0, 8)} · creada ${formatRelative(run.created_at)}`}
        actions={
          status === "failed" ? (
            <>
              <Button
                disabled={!isAdmin}
                title={isAdmin ? undefined : "Necesitás rol admin"}
                loading={retry.isPending}
                icon={<RotateCcw className="size-4" />}
                onClick={() => retry.mutate(run.id)}
              >
                Reintentar
              </Button>
              <Button variant="danger" disabled={!isAdmin} title={isAdmin ? undefined : "Necesitás rol admin"} icon={<Trash2 className="size-4" />} onClick={() => setDestroyOpen(true)}>
                Destruir
              </Button>
            </>
          ) : status === "succeeded" ? (
            <Button variant="ghost" disabled={!isAdmin} title={isAdmin ? "Borra la infraestructura creada" : "Necesitás rol admin"} icon={<Trash2 className="size-4 text-red-600" />} onClick={() => setDestroyOpen(true)}>
              Destruir
            </Button>
          ) : undefined
        }
      />

      <Card>
        <CardBody>
          <dl className="grid gap-x-6 gap-y-3 text-sm sm:grid-cols-2 lg:grid-cols-5">
            <Item label="Estado">
              <RunStatusBadge status={status} />
            </Item>
            <Item label="Cuenta">{account ? `${account.name} (${account.region})` : run.cloud_account_id.slice(0, 8)}</Item>
            <Item label="Módulo">
              <span className="font-mono text-xs">{run.module}</span>
            </Item>
            <Item label="Cluster">
              {cluster ? (
                <span className="inline-flex items-center gap-2">
                  <Link to={`/o/${org}/clusters`} className="text-slate-900 hover:underline">
                    {cluster.name}
                  </Link>
                  <StatusBadge status={cluster.status} />
                </span>
              ) : run.cluster_id ? (
                <Link to={`/o/${org}/clusters`} className="text-slate-900 hover:underline">
                  Ver clusters
                </Link>
              ) : (
                "-"
              )}
            </Item>
            <Item label="Quién y cuándo">
              <span title={formatDateTime(run.created_at)}>
                {run.requested_by?.name || run.requested_by?.email || "-"}
              </span>
              {run.finished_at && <div className="text-xs text-slate-500">terminó {formatRelative(run.finished_at)}</div>}
            </Item>
          </dl>
        </CardBody>
      </Card>

      {(retry.isError || destroy.isError) && (
        <div className="mt-4">
          <InlineError error={retry.error ?? destroy.error} />
        </div>
      )}

      {status === "failed" && (
        <div className="mt-4">
          <Notice tone="amber">
            <div className="font-medium text-red-800">La ejecución falló</div>
            <pre className="mt-1 whitespace-pre-wrap break-words font-mono text-xs text-red-800">{run.error || "Sin detalle. Mirá el log."}</pre>
            {/permission|AccessDenied|not authorized|UnauthorizedOperation/i.test(run.error) && (
              <p className="mt-2 text-xs text-slate-700">
                Parece un problema de permisos en la nube. Actualizá el stack RampaConnect de esa cuenta con ManageInfrastructure en true y reintentá.
              </p>
            )}
          </Notice>
        </div>
      )}

      {status === "succeeded" && (
        <Card className="mt-4">
          <CardHeader title="Listo" description="La infraestructura se creó correctamente." />
          <CardBody className="space-y-4">
            <div className="text-sm text-slate-700">
              Tu cluster ya aparece online en Rampa; si no, acá está el install_command.{" "}
              <Link to={`/o/${org}/clusters`} className="font-medium text-slate-900 underline">
                Ver cluster
              </Link>
              {cluster && (
                <span className="ml-2 inline-block align-middle">
                  <StatusBadge status={cluster.status} />
                </span>
              )}
            </div>
            {installFromOutputs ? (
              <CopyField label="install_command" value={installFromOutputs} multiline />
            ) : (
              cluster &&
              cluster.status !== "online" && <InstallCommandPanel org={org} cluster={cluster} canRotate={isAdmin} />
            )}
            {outputs.length > 0 && (
              <div>
                <div className="mb-1.5 text-sm font-medium text-slate-700">Outputs</div>
                <dl className="divide-y divide-slate-100 rounded-md border border-slate-200 text-sm">
                  {outputs.map(([k, v]) => (
                    <div key={k} className="grid gap-1 px-3 py-2 sm:grid-cols-3">
                      <dt className="font-mono text-xs text-slate-500">{k}</dt>
                      <dd className="break-all font-mono text-xs text-slate-800 sm:col-span-2">{v}</dd>
                    </div>
                  ))}
                </dl>
              </div>
            )}
          </CardBody>
        </Card>
      )}

      <LogCard lines={log.lines} connected={log.connected} done={log.done} active={isRunActive(status)} />

      <Dialog
        open={destroyOpen}
        onClose={() => {
          setDestroyOpen(false);
          setConfirmText("");
          destroy.reset();
        }}
        title="Destruir la infraestructura"
        description="Rampa corre tofu destroy con los mismos inputs. Se borran el cluster y la red en tu nube, y el cluster desaparece de Rampa si no tiene scopes."
        footer={
          <>
            <Button variant="secondary" onClick={() => setDestroyOpen(false)}>
              Cancelar
            </Button>
            <Button
              variant="danger"
              disabled={confirmText !== run.name}
              loading={destroy.isPending}
              onClick={() =>
                destroy.mutate(run.id, {
                  onSuccess: () => {
                    setDestroyOpen(false);
                    setConfirmText("");
                  },
                })
              }
            >
              Destruir definitivamente
            </Button>
          </>
        }
      >
        <div className="space-y-3">
          <p className="text-sm text-slate-700">
            Para confirmar, escribí el nombre <span className="font-mono font-medium">{run.name}</span>.
          </p>
          <Input value={confirmText} onChange={(e) => setConfirmText(e.target.value)} aria-label="Nombre para confirmar" />
          {destroy.isError && <p className="text-sm text-red-600">{errorMessage(destroy.error)}</p>}
        </div>
      </Dialog>
    </>
  );
}

function Item({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <dt className="text-xs text-slate-500">{label}</dt>
      <dd className="mt-0.5 text-slate-800">{children}</dd>
    </div>
  );
}

function LogCard({ lines, connected, done, active }: { lines: string[]; connected: boolean; done: boolean; active: boolean }) {
  const [paused, setPaused] = useState(false);
  const ref = useRef<HTMLPreElement>(null);

  useEffect(() => {
    const el = ref.current;
    if (el && !paused) el.scrollTop = el.scrollHeight;
  }, [lines.length, paused]);

  return (
    <Card className="mt-4">
      <CardHeader
        title="Log"
        description={done ? "Fin del log" : connected ? "En vivo" : active ? "Conectando con el log en vivo" : "Cargando el log"}
        actions={
          <Button
            size="sm"
            variant="secondary"
            icon={paused ? <Play className="size-3.5" /> : <Pause className="size-3.5" />}
            onClick={() => setPaused((p) => !p)}
          >
            {paused ? "Seguir el log" : "Pausar autoscroll"}
          </Button>
        }
      />
      <pre
        ref={ref}
        className="h-[28rem] overflow-auto bg-slate-950 p-4 font-mono text-xs leading-relaxed text-slate-100"
        tabIndex={0}
        aria-label="Log de la ejecución"
        aria-live="off"
      >
        {lines.length === 0 ? <span className="text-slate-500">{done ? "El log está vacío." : "Todavía no hay salida."}</span> : lines.join("\n")}
      </pre>
    </Card>
  );
}
