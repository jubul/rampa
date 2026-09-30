import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { Boxes, KeyRound, Plus, Terminal, Trash2 } from "lucide-react";
import { useCloudAccounts, useClusters, useCreateCluster, useRemoveCluster, useRotateClusterToken } from "../api/hooks";
import { useOrgRole } from "../auth/AuthContext";
import type { Cluster, ClusterEnv, ClusterWithToken } from "../api/types";
import { errorMessage } from "../api/client";
import { PageHeader } from "../components/ui/PageHeader";
import { Card } from "../components/ui/Card";
import { Button } from "../components/ui/Button";
import { LinkButton } from "../components/ui/LinkButton";
import { Input } from "../components/ui/Input";
import { Select } from "../components/ui/Select";
import { Field } from "../components/ui/Field";
import { Badge } from "../components/ui/Badge";
import { ConfirmDialog, Dialog } from "../components/ui/Dialog";
import { CopyField } from "../components/ui/CopyField";
import { EmptyState, ErrorState, InlineError, LoadingState, Notice } from "../components/ui/States";
import { Table, TBody, TD, TH, THead, TR } from "../components/ui/Table";
import { StatusBadge } from "../components/StatusBadge";
import { ClusterKindBadge } from "../components/ClusterKindBadge";
import { InstallCommandPanel } from "../components/InstallCommandPanel";
import { useClusterTerms, useTerms } from "../hooks/useTerms";
import { formatDateTime, formatRelative } from "../lib/format";
import { envLabel } from "../lib/status";
import { cap } from "../lib/terminology";

export function ClustersPage() {
  const { t } = useTranslation();
  const { org = "" } = useParams();
  const { isAdmin } = useOrgRole(org);
  const clustersQ = useClusters(org);
  const accountsQ = useCloudAccounts(org);
  const clusterTerms = useClusterTerms(org);
  const generic = useTerms(org).terms();
  const rotate = useRotateClusterToken(org);
  const remove = useRemoveCluster(org);

  const [searchParams, setSearchParams] = useSearchParams();
  const [createOpen, setCreateOpen] = useState(searchParams.get("new") === "1");
  const closeCreate = () => {
    setCreateOpen(false);
    if (searchParams.has("new")) setSearchParams({}, { replace: true });
  };
  const [issued, setIssued] = useState<{ cluster: ClusterWithToken; reason: "created" | "rotated" } | null>(null);
  const [toRotate, setToRotate] = useState<Cluster | null>(null);
  const [toDelete, setToDelete] = useState<Cluster | null>(null);
  const [installFor, setInstallFor] = useState<Cluster | null>(null);

  const accountName = (id: string) => accountsQ.data?.find((a) => a.id === id)?.name ?? id.slice(0, 8);
  const adminTitle = isAdmin ? undefined : t("common.needAdmin");

  return (
    <>
      <PageHeader
        title={t("nav.clusters")}
        description={t("clusters.description")}
        actions={
          <>
            <Button variant="secondary" onClick={() => setCreateOpen(true)} disabled={!isAdmin} title={adminTitle} icon={<Plus className="size-4" />}>
              {t("clusters.registerExisting")}
            </Button>
            <LinkButton to={`/o/${org}/infra/new-cluster`} icon={<Boxes className="size-4" />}>
              {t("clusters.createOneClick")}
            </LinkButton>
          </>
        }
      />

      <Card>
        {clustersQ.isPending ? (
          <LoadingState />
        ) : clustersQ.isError ? (
          <ErrorState error={clustersQ.error} onRetry={() => void clustersQ.refetch()} />
        ) : clustersQ.data.length === 0 ? (
          <EmptyState
            icon={<Boxes className="size-8" />}
            title={t("clusters.emptyTitle")}
            description={t("clusters.emptyDescription")}
            action={
              <div className="flex gap-2">
                <LinkButton to={`/o/${org}/infra/new-cluster`}>{t("clusters.createOneClick")}</LinkButton>
                <Button variant="secondary" onClick={() => setCreateOpen(true)} disabled={!isAdmin} icon={<Plus className="size-4" />}>
                  {t("clusters.registerExisting")}
                </Button>
              </div>
            }
          />
        ) : (
          <Table>
            <THead>
              <tr>
                <TH>{t("clusters.colName")}</TH>
                <TH>{t("clusters.colType")}</TH>
                <TH>{t("clusters.colEnv")}</TH>
                <TH>{cap(generic.account)}</TH>
                <TH>{t("clusters.colDomain")}</TH>
                <TH>{t("clusters.colStatus")}</TH>
                <TH>{t("clusters.colAgent")}</TH>
                <TH>Kubernetes</TH>
                <TH className="text-right">{t("clusters.colActions")}</TH>
              </tr>
            </THead>
            <TBody>
              {clustersQ.data.map((c) => {
                const terms = clusterTerms(c);
                return (
                  <TR key={c.id}>
                    <TD>
                      <div className="font-medium text-slate-900 dark:text-slate-100">{c.name}</div>
                      <div className="text-xs text-slate-500 dark:text-slate-400">
                        {c.ingress_class} · {c.secret_store}
                      </div>
                      {c.infra_run_id && (
                        <Link to={`/o/${org}/infra/runs/${c.infra_run_id}`} className="text-xs font-medium text-slate-700 dark:text-slate-200 underline">
                          {t("clusters.viewRun")}
                        </Link>
                      )}
                    </TD>
                    <TD>
                      <ClusterKindBadge cluster={c} terms={terms} />
                    </TD>
                    <TD>
                      <Badge tone={c.env === "prod" ? "blue" : "gray"}>{envLabel(c.env)}</Badge>
                    </TD>
                    <TD>{accountName(c.cloud_account_id)}</TD>
                    <TD className="font-mono text-xs">{c.domain}</TD>
                    <TD>
                      <div className="flex flex-col gap-0.5">
                        <StatusBadge status={c.status} />
                        <span className="text-xs text-slate-500 dark:text-slate-400" title={c.last_seen_at ?? ""}>
                          {c.last_seen_at ? formatRelative(c.last_seen_at) : t("clusters.neverConnected")}
                        </span>
                      </div>
                    </TD>
                    <TD className="font-mono text-xs">{c.agent_version || "-"}</TD>
                    <TD className="font-mono text-xs">{c.kubernetes_version || "-"}</TD>
                    <TD>
                      <div className="flex justify-end gap-1.5">
                        <Button size="sm" variant="secondary" disabled={!isAdmin} title={adminTitle} icon={<Terminal className="size-3.5" />} onClick={() => setInstallFor(c)}>
                          {t("clusters.installCommand")}
                        </Button>
                        <Button size="sm" variant="secondary" disabled={!isAdmin} title={adminTitle} icon={<KeyRound className="size-3.5" />} onClick={() => setToRotate(c)}>
                          {t("clusters.rotateToken")}
                        </Button>
                        <Button size="sm" variant="ghost" disabled={!isAdmin} aria-label={t("common.delete")} title={t("common.delete")} icon={<Trash2 className="size-3.5 text-red-600 dark:text-red-400" />} onClick={() => setToDelete(c)} />
                      </div>
                    </TD>
                  </TR>
                );
              })}
            </TBody>
          </Table>
        )}
      </Card>

      <CreateClusterDialog
        org={org}
        open={createOpen}
        onClose={closeCreate}
        onCreated={(c) => {
          closeCreate();
          setIssued({ cluster: c, reason: "created" });
        }}
      />

      <Dialog
        open={installFor !== null}
        onClose={() => setInstallFor(null)}
        size="lg"
        title={t("clusters.installTitle", { name: installFor?.name ?? "" })}
        footer={
          <Button variant="secondary" onClick={() => setInstallFor(null)}>
            {t("common.close")}
          </Button>
        }
      >
        {installFor && <InstallCommandPanel org={org} cluster={installFor} canView={isAdmin} autoLoad />}
      </Dialog>

      <ConfirmDialog
        open={toRotate !== null}
        onClose={() => {
          setToRotate(null);
          rotate.reset();
        }}
        title={t("clusters.rotateTitle", { cluster: toRotate ? clusterTerms(toRotate).cluster : generic.cluster })}
        description={toRotate ? t("clusters.rotateDescription", { name: toRotate.name }) : undefined}
        confirmLabel={t("clusters.rotateToken")}
        danger
        loading={rotate.isPending}
        error={rotate.isError ? errorMessage(rotate.error) : null}
        onConfirm={() => {
          if (!toRotate) return;
          rotate.mutate(toRotate.id, {
            onSuccess: (c) => {
              setToRotate(null);
              setIssued({ cluster: c, reason: "rotated" });
            },
          });
        }}
      />

      <ConfirmDialog
        open={toDelete !== null}
        onClose={() => {
          setToDelete(null);
          remove.reset();
        }}
        title={t("clusters.deleteTitle", { cluster: toDelete ? clusterTerms(toDelete).cluster : generic.cluster })}
        description={toDelete ? t("clusters.deleteDescription", { name: toDelete.name, cluster: clusterTerms(toDelete).cluster }) : undefined}
        confirmLabel={t("common.delete")}
        danger
        loading={remove.isPending}
        error={remove.isError ? errorMessage(remove.error) : null}
        onConfirm={() => {
          if (!toDelete) return;
          remove.mutate(toDelete.id, { onSuccess: () => setToDelete(null) });
        }}
      />

      <TokenIssuedDialog issued={issued} onClose={() => setIssued(null)} />
    </>
  );
}

function CreateClusterDialog({
  org,
  open,
  onClose,
  onCreated,
}: {
  org: string;
  open: boolean;
  onClose: () => void;
  onCreated: (c: ClusterWithToken) => void;
}) {
  const { t } = useTranslation();
  return (
    <Dialog open={open} onClose={onClose} title={t("clusters.registerTitle")} description={t("clusters.registerDescription")}>
      {open && <CreateClusterForm org={org} onClose={onClose} onCreated={onCreated} />}
    </Dialog>
  );
}

function CreateClusterForm({ org, onClose, onCreated }: { org: string; onClose: () => void; onCreated: (c: ClusterWithToken) => void }) {
  const { t } = useTranslation();
  const { terms, providers } = useTerms(org);
  const generic = terms();
  const accountsQ = useCloudAccounts(org);
  const create = useCreateCluster(org);
  const connected = (accountsQ.data ?? []).filter((a) => a.status === "connected");
  const [form, setForm] = useState({
    name: "",
    env: "nonprod" as ClusterEnv,
    cloud_account_id: "",
    domain: "",
    ingress_class: "nginx",
    secret_store: "aws-secrets-manager",
  });
  const [secretStoreTouched, setSecretStoreTouched] = useState(false);
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => {
    if (k === "secret_store") setSecretStoreTouched(true);
    setForm((f) => ({ ...f, [k]: e.target.value }));
  };
  // Las nubes con token (DigitalOcean, OCI, OVH) no traen gestor de secretos: el default es "kubernetes".
  const onAccountChange = (e: React.ChangeEvent<HTMLSelectElement>) => {
    const id = e.target.value;
    const kind = connected.find((a) => a.id === id)?.provider;
    const auth = providers?.find((p) => p.kind === kind)?.auth;
    setForm((f) => ({
      ...f,
      cloud_account_id: id,
      secret_store: secretStoreTouched ? f.secret_store : auth === "token" ? "kubernetes" : "aws-secrets-manager",
    }));
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!form.name || !form.cloud_account_id || !form.domain) return;
    create.mutate(
      { ...form, name: form.name.trim(), domain: form.domain.trim(), ingress_class: form.ingress_class.trim(), secret_store: form.secret_store.trim() },
      { onSuccess: onCreated },
    );
  };

  return (
    <form onSubmit={submit} className="space-y-4">
      {accountsQ.isSuccess && connected.length === 0 && (
        <Notice tone="amber">
          {t("clusters.noAccounts", { account: generic.account })}{" "}
          <Link to={`/o/${org}/cloud-accounts`} className="font-medium underline">
            {t("clusters.connectFirst")}
          </Link>
        </Notice>
      )}
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label={t("clusters.fieldName")} required>
          {(id) => <Input id={id} required placeholder="eks-nonprod" value={form.name} onChange={set("name")} />}
        </Field>
        <Field label={t("clusters.fieldEnv")} required help={t("clusters.fieldEnvHelp")}>
          {(id) => (
            <Select
              id={id}
              value={form.env}
              onChange={set("env")}
              options={[
                { value: "nonprod", label: envLabel("nonprod") },
                { value: "prod", label: envLabel("prod") },
              ]}
            />
          )}
        </Field>
      </div>
      <Field label={cap(generic.account)} required>
        {(id) => (
          <Select
            id={id}
            required
            value={form.cloud_account_id}
            onChange={onAccountChange}
            placeholder={accountsQ.isPending ? `${t("common.loading")}...` : t("validation.pickAccount")}
            options={connected.map((a) => ({ value: a.id, label: `${a.name} (${providers?.find((p) => p.kind === a.provider)?.display_name ?? a.provider}, ${a.region})` }))}
          />
        )}
      </Field>
      <Field label={t("clusters.fieldDomain")} required help={t("clusters.fieldDomainHelp")}>
        {(id) => <Input id={id} required placeholder="apps.dev.acme.com" value={form.domain} onChange={set("domain")} />}
      </Field>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Ingress class" required>
          {(id) => <Input id={id} required value={form.ingress_class} onChange={set("ingress_class")} />}
        </Field>
        <Field label={cap(generic.secretStore)} required help={t("clusters.fieldSecretStoreHelp")}>
          {(id) => <Input id={id} required value={form.secret_store} onChange={set("secret_store")} />}
        </Field>
      </div>
      {create.isError && <InlineError error={create.error} />}
      <div className="flex justify-end gap-2 pt-1">
        <Button variant="secondary" onClick={onClose}>
          {t("common.cancel")}
        </Button>
        <Button type="submit" loading={create.isPending} disabled={connected.length === 0}>
          {t("clusters.registerSubmit")}
        </Button>
      </div>
    </form>
  );
}

function TokenIssuedDialog({ issued, onClose }: { issued: { cluster: ClusterWithToken; reason: "created" | "rotated" } | null; onClose: () => void }) {
  const { t } = useTranslation();
  const [ack, setAck] = useState(false);
  return (
    <Dialog
      open={issued !== null}
      onClose={onClose}
      persistent
      size="lg"
      title={issued?.reason === "rotated" ? t("clusters.tokenRotated") : t("clusters.clusterCreated")}
      description={t("clusters.installOrReinstall")}
      footer={
        <>
          <label className="mr-auto flex items-center gap-2 text-sm text-slate-700 dark:text-slate-200">
            <input type="checkbox" checked={ack} onChange={(e) => setAck(e.target.checked)} className="size-4 rounded border-slate-300 dark:border-slate-700" />
            {t("clusters.ackCopied")}
          </label>
          <Button
            disabled={!ack}
            onClick={() => {
              setAck(false);
              onClose();
            }}
          >
            {t("common.close")}
          </Button>
        </>
      }
    >
      {issued && (
        <div className="space-y-4">
          <Notice tone="amber">
            <strong>{t("clusters.onceTitle")}</strong> {t("clusters.onceBody")}
          </Notice>
          <CopyField label={t("clusters.clusterToken")} value={issued.cluster.token} secret />
          <CopyField label={t("installCommand.label")} value={issued.cluster.install_command} multiline />
          <p className="text-xs text-slate-500 dark:text-slate-400">
            {t("clusters.issuedMeta", { name: issued.cluster.name, env: envLabel(issued.cluster.env), date: formatDateTime(issued.cluster.created_at) })}
          </p>
        </div>
      )}
    </Dialog>
  );
}
