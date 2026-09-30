import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
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
import { useTerms } from "../hooks/useTerms";
import { cap } from "../lib/terminology";
import { isRunActive, outputEntries } from "../lib/infra";

export function InfraRunPage() {
  const { t } = useTranslation();
  const { org = "", id = "" } = useParams();
  const runQ = useInfraRun(org, id);

  return (
    <>
      <Link to={`/o/${org}/infra`} className="mb-3 inline-flex items-center gap-1 text-xs font-medium text-slate-500 dark:text-slate-400 hover:text-slate-900 dark:hover:text-slate-100">
        <ArrowLeft className="size-3.5" /> {t("nav.infra")}
      </Link>
      {runQ.isPending ? (
        <LoadingState label={t("infraRun.loading")} />
      ) : runQ.isError ? (
        <ErrorState title={t("infraRun.loadError")} error={runQ.error} onRetry={() => void runQ.refetch()} />
      ) : (
        <RunDetail org={org} run={runQ.data} />
      )}
    </>
  );
}

function RunDetail({ org, run }: { org: string; run: InfraRun }) {
  const { t } = useTranslation();
  const { terms } = useTerms(org);
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
  const rt = terms(account?.provider ?? run.provider);
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
        description={t("infraRun.subtitle", { id: run.id.slice(0, 8), when: formatRelative(run.created_at) })}
        actions={
          status === "failed" ? (
            <>
              <Button
                disabled={!isAdmin}
                title={isAdmin ? undefined : t("common.needAdmin")}
                loading={retry.isPending}
                icon={<RotateCcw className="size-4" />}
                onClick={() => retry.mutate(run.id)}
              >
                {t("common.retry")}
              </Button>
              <Button variant="danger" disabled={!isAdmin} title={isAdmin ? undefined : t("common.needAdmin")} icon={<Trash2 className="size-4" />} onClick={() => setDestroyOpen(true)}>
                {t("infraRun.destroy")}
              </Button>
            </>
          ) : status === "succeeded" ? (
            <Button variant="ghost" disabled={!isAdmin} title={isAdmin ? t("infraRun.destroyHint") : t("common.needAdmin")} icon={<Trash2 className="size-4 text-red-600 dark:text-red-400" />} onClick={() => setDestroyOpen(true)}>
              {t("infraRun.destroy")}
            </Button>
          ) : undefined
        }
      />

      <Card>
        <CardBody>
          <dl className="grid gap-x-6 gap-y-3 text-sm sm:grid-cols-2 lg:grid-cols-5">
            <Item label={t("infra.colStatus")}>
              <RunStatusBadge status={status} />
            </Item>
            <Item label={cap(rt.account)}>{account ? `${account.name} (${account.region})` : run.cloud_account_id.slice(0, 8)}</Item>
            <Item label={t("infra.colModule")}>
              <span className="font-mono text-xs">{run.module}</span>
            </Item>
            <Item label={cluster?.kind === "vm" ? cap(rt.vm) : cap(rt.cluster)}>
              {cluster ? (
                <span className="inline-flex items-center gap-2">
                  <Link to={`/o/${org}/clusters`} className="text-slate-900 dark:text-slate-100 hover:underline">
                    {cluster.name}
                  </Link>
                  <StatusBadge status={cluster.status} />
                </span>
              ) : run.cluster_id ? (
                <Link to={`/o/${org}/clusters`} className="text-slate-900 dark:text-slate-100 hover:underline">
                  {t("infraRun.viewClusters")}
                </Link>
              ) : (
                "-"
              )}
            </Item>
            <Item label={t("infraRun.whoWhen")}>
              <span title={formatDateTime(run.created_at)}>
                {run.requested_by?.name || run.requested_by?.email || "-"}
              </span>
              {run.finished_at && <div className="text-xs text-slate-500 dark:text-slate-400">{t("infraRun.finished", { when: formatRelative(run.finished_at) })}</div>}
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
            <div className="font-medium text-red-800 dark:text-red-300">{t("infraRun.failed")}</div>
            <pre className="mt-1 whitespace-pre-wrap break-words font-mono text-xs text-red-800 dark:text-red-300">{run.error || t("infraRun.noDetail")}</pre>
            {/permission|AccessDenied|not authorized|UnauthorizedOperation/i.test(run.error) && (
              <p className="mt-2 text-xs text-slate-700 dark:text-slate-200">
                {t("infraRun.permissionHint")}
              </p>
            )}
          </Notice>
        </div>
      )}

      {status === "succeeded" && (
        <Card className="mt-4">
          <CardHeader title={t("infraRun.doneTitle")} description={t("infraRun.doneDescription")} />
          <CardBody className="space-y-4">
            <div className="text-sm text-slate-700 dark:text-slate-200">
              {t("infraRun.doneBody", { cluster: cluster?.kind === "vm" ? rt.vm : rt.cluster })}{" "}
              <Link to={`/o/${org}/clusters`} className="font-medium text-slate-900 dark:text-slate-100 underline">
                {t("infraRun.viewCluster")}
              </Link>
              {cluster && (
                <span className="ml-2 inline-block align-middle">
                  <StatusBadge status={cluster.status} />
                </span>
              )}
            </div>
            {installFromOutputs ? (
              <CopyField label={t("installCommand.label")} value={installFromOutputs} multiline />
            ) : (
              cluster &&
              cluster.status !== "online" && <InstallCommandPanel org={org} cluster={cluster} canView={isAdmin} />
            )}
            {outputs.length > 0 && (
              <div>
                <div className="mb-1.5 text-sm font-medium text-slate-700 dark:text-slate-200">{t("infraRun.outputs")}</div>
                <dl className="divide-y divide-slate-100 dark:divide-slate-800 rounded-md border border-slate-200 dark:border-slate-800 text-sm">
                  {outputs.map(([k, v]) => (
                    <div key={k} className="grid gap-1 px-3 py-2 sm:grid-cols-3">
                      <dt className="font-mono text-xs text-slate-500 dark:text-slate-400">{k}</dt>
                      <dd className="break-all font-mono text-xs text-slate-800 dark:text-slate-100 sm:col-span-2">{v}</dd>
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
        title={t("infraRun.destroyTitle")}
        description={t("infraRun.destroyDescription")}
        footer={
          <>
            <Button variant="secondary" onClick={() => setDestroyOpen(false)}>
              {t("common.cancel")}
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
              {t("infraRun.destroyForever")}
            </Button>
          </>
        }
      >
        <div className="space-y-3">
          <p className="text-sm text-slate-700 dark:text-slate-200">
            {t("infraRun.confirmName")} <span className="font-mono font-medium">{run.name}</span>.
          </p>
          <Input value={confirmText} onChange={(e) => setConfirmText(e.target.value)} aria-label={t("infraRun.confirmNameLabel")} />
          {destroy.isError && <p className="text-sm text-red-600 dark:text-red-400">{errorMessage(destroy.error)}</p>}
        </div>
      </Dialog>
    </>
  );
}

function Item({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <dt className="text-xs text-slate-500 dark:text-slate-400">{label}</dt>
      <dd className="mt-0.5 text-slate-800 dark:text-slate-100">{children}</dd>
    </div>
  );
}

function LogCard({ lines, connected, done, active }: { lines: string[]; connected: boolean; done: boolean; active: boolean }) {
  const { t } = useTranslation();
  const [paused, setPaused] = useState(false);
  const ref = useRef<HTMLPreElement>(null);

  useEffect(() => {
    const el = ref.current;
    if (el && !paused) el.scrollTop = el.scrollHeight;
  }, [lines.length, paused]);

  return (
    <Card className="mt-4">
      <CardHeader
        title={t("infraRun.log")}
        description={done ? t("infraRun.logEnd") : connected ? t("infraRun.logLive") : active ? t("infraRun.logConnecting") : t("infraRun.logLoading")}
        actions={
          <Button
            size="sm"
            variant="secondary"
            icon={paused ? <Play className="size-3.5" /> : <Pause className="size-3.5" />}
            onClick={() => setPaused((p) => !p)}
          >
            {paused ? t("infraRun.followLog") : t("infraRun.pauseScroll")}
          </Button>
        }
      />
      <pre
        ref={ref}
        className="h-[28rem] overflow-auto bg-slate-950 p-4 font-mono text-xs leading-relaxed text-slate-100"
        tabIndex={0}
        aria-label={t("infraRun.logLabel")}
        aria-live="off"
      >
        {lines.length === 0 ? <span className="text-slate-500 dark:text-slate-400">{done ? t("infraRun.logEmpty") : t("infraRun.logNoOutput")}</span> : lines.join("\n")}
      </pre>
    </Card>
  );
}
