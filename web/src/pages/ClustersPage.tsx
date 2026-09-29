import { useState, type FormEvent } from "react";
import { Link, useParams } from "react-router-dom";
import { Boxes, KeyRound, Plus, Trash2 } from "lucide-react";
import { useCloudAccounts, useClusters, useCreateCluster, useRemoveCluster, useRotateClusterToken } from "../api/hooks";
import { useOrgRole } from "../auth/AuthContext";
import type { Cluster, ClusterEnv, ClusterWithToken } from "../api/types";
import { errorMessage } from "../api/client";
import { PageHeader } from "../components/ui/PageHeader";
import { Card } from "../components/ui/Card";
import { Button } from "../components/ui/Button";
import { Input } from "../components/ui/Input";
import { Select } from "../components/ui/Select";
import { Field } from "../components/ui/Field";
import { Badge } from "../components/ui/Badge";
import { ConfirmDialog, Dialog } from "../components/ui/Dialog";
import { CopyField } from "../components/ui/CopyField";
import { EmptyState, ErrorState, InlineError, LoadingState, Notice } from "../components/ui/States";
import { Table, TBody, TD, TH, THead, TR } from "../components/ui/Table";
import { StatusBadge } from "../components/StatusBadge";
import { formatDateTime, formatRelative } from "../lib/format";
import { envLabels } from "../lib/status";

export function ClustersPage() {
  const { org = "" } = useParams();
  const { isAdmin } = useOrgRole(org);
  const clustersQ = useClusters(org);
  const accountsQ = useCloudAccounts(org);
  const rotate = useRotateClusterToken(org);
  const remove = useRemoveCluster(org);

  const [createOpen, setCreateOpen] = useState(false);
  const [issued, setIssued] = useState<{ cluster: ClusterWithToken; reason: "created" | "rotated" } | null>(null);
  const [toRotate, setToRotate] = useState<Cluster | null>(null);
  const [toDelete, setToDelete] = useState<Cluster | null>(null);

  const accountName = (id: string) => accountsQ.data?.find((a) => a.id === id)?.name ?? id.slice(0, 8);

  return (
    <>
      <PageHeader
        title="Clusters"
        description="Un agente por cluster. El agente abre una conexión saliente: no hace falta exponer nada."
        actions={
          <Button onClick={() => setCreateOpen(true)} disabled={!isAdmin} title={isAdmin ? undefined : "Necesitás rol admin"} icon={<Plus className="size-4" />}>
            Nuevo cluster
          </Button>
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
            title="No hay clusters"
            description="Registrá un cluster para obtener el comando de instalación del agente."
            action={
              <Button onClick={() => setCreateOpen(true)} disabled={!isAdmin} icon={<Plus className="size-4" />}>
                Nuevo cluster
              </Button>
            }
          />
        ) : (
          <Table>
            <THead>
              <tr>
                <TH>Nombre</TH>
                <TH>Entorno</TH>
                <TH>Cuenta</TH>
                <TH>Dominio</TH>
                <TH>Estado</TH>
                <TH>Agente</TH>
                <TH>Kubernetes</TH>
                <TH className="text-right">Acciones</TH>
              </tr>
            </THead>
            <TBody>
              {clustersQ.data.map((c) => (
                <TR key={c.id}>
                  <TD>
                    <div className="font-medium text-slate-900">{c.name}</div>
                    <div className="text-xs text-slate-500">
                      {c.ingress_class} · {c.secret_store}
                    </div>
                  </TD>
                  <TD>
                    <Badge tone={c.env === "prod" ? "blue" : "gray"}>{envLabels[c.env] ?? c.env}</Badge>
                  </TD>
                  <TD>{accountName(c.cloud_account_id)}</TD>
                  <TD className="font-mono text-xs">{c.domain}</TD>
                  <TD>
                    <div className="flex flex-col gap-0.5">
                      <StatusBadge status={c.status} />
                      <span className="text-xs text-slate-500" title={c.last_seen_at ?? ""}>
                        {c.last_seen_at ? formatRelative(c.last_seen_at) : "nunca conectó"}
                      </span>
                    </div>
                  </TD>
                  <TD className="font-mono text-xs">{c.agent_version || "-"}</TD>
                  <TD className="font-mono text-xs">{c.kubernetes_version || "-"}</TD>
                  <TD>
                    <div className="flex justify-end gap-1.5">
                      <Button size="sm" variant="secondary" disabled={!isAdmin} icon={<KeyRound className="size-3.5" />} onClick={() => setToRotate(c)}>
                        Rotar token
                      </Button>
                      <Button size="sm" variant="ghost" disabled={!isAdmin} aria-label="Eliminar" title="Eliminar" icon={<Trash2 className="size-3.5 text-red-600" />} onClick={() => setToDelete(c)} />
                    </div>
                  </TD>
                </TR>
              ))}
            </TBody>
          </Table>
        )}
      </Card>

      <CreateClusterDialog
        org={org}
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        onCreated={(c) => {
          setCreateOpen(false);
          setIssued({ cluster: c, reason: "created" });
        }}
      />

      <ConfirmDialog
        open={toRotate !== null}
        onClose={() => {
          setToRotate(null);
          rotate.reset();
        }}
        title="Rotar el token del cluster"
        description={toRotate ? `El token actual de "${toRotate.name}" deja de valer al instante. El agente instalado va a desconectarse hasta que lo reinstales con el nuevo token.` : undefined}
        confirmLabel="Rotar token"
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
        title="Eliminar cluster"
        description={toDelete ? `Se va a eliminar "${toDelete.name}" de Rampa. Si tiene scopes la API lo va a rechazar. El agente instalado no se desinstala solo.` : undefined}
        confirmLabel="Eliminar"
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
  return (
    <Dialog open={open} onClose={onClose} title="Nuevo cluster" description="Los datos del cluster se usan para renderizar ingress, URLs y secretos de las apps.">
      {open && <CreateClusterForm org={org} onClose={onClose} onCreated={onCreated} />}
    </Dialog>
  );
}

function CreateClusterForm({ org, onClose, onCreated }: { org: string; onClose: () => void; onCreated: (c: ClusterWithToken) => void }) {
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
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setForm((f) => ({ ...f, [k]: e.target.value }));

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
          No hay cuentas de nube conectadas.{" "}
          <Link to={`/o/${org}/cloud-accounts`} className="font-medium underline">
            Conectá una primero.
          </Link>
        </Notice>
      )}
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Nombre" required>
          {(id) => <Input id={id} required placeholder="eks-nonprod" value={form.name} onChange={set("name")} />}
        </Field>
        <Field label="Entorno" required help="Los scopes prod solo viven en clusters prod.">
          {(id) => (
            <Select
              id={id}
              value={form.env}
              onChange={set("env")}
              options={[
                { value: "nonprod", label: "No prod" },
                { value: "prod", label: "Prod" },
              ]}
            />
          )}
        </Field>
      </div>
      <Field label="Cuenta de nube" required>
        {(id) => (
          <Select
            id={id}
            required
            value={form.cloud_account_id}
            onChange={set("cloud_account_id")}
            placeholder={accountsQ.isPending ? "Cargando..." : "Elegí una cuenta conectada"}
            options={connected.map((a) => ({ value: a.id, label: `${a.name} (${a.provider}, ${a.region})` }))}
          />
        )}
      </Field>
      <Field label="Dominio" required help="Wildcard de los scopes: las apps quedan en <app>-<scope>.<dominio>.">
        {(id) => <Input id={id} required placeholder="apps.dev.acme.com" value={form.domain} onChange={set("domain")} />}
      </Field>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Ingress class" required>
          {(id) => <Input id={id} required value={form.ingress_class} onChange={set("ingress_class")} />}
        </Field>
        <Field label="Secret store" required help="Nombre del ClusterSecretStore de External Secrets.">
          {(id) => <Input id={id} required value={form.secret_store} onChange={set("secret_store")} />}
        </Field>
      </div>
      {create.isError && <InlineError error={create.error} />}
      <div className="flex justify-end gap-2 pt-1">
        <Button variant="secondary" onClick={onClose}>
          Cancelar
        </Button>
        <Button type="submit" loading={create.isPending} disabled={connected.length === 0}>
          Crear cluster
        </Button>
      </div>
    </form>
  );
}

function TokenIssuedDialog({ issued, onClose }: { issued: { cluster: ClusterWithToken; reason: "created" | "rotated" } | null; onClose: () => void }) {
  const [ack, setAck] = useState(false);
  return (
    <Dialog
      open={issued !== null}
      onClose={onClose}
      persistent
      size="lg"
      title={issued?.reason === "rotated" ? "Token rotado" : "Cluster creado"}
      description="Instalá o reinstalá el agente con este comando."
      footer={
        <>
          <label className="mr-auto flex items-center gap-2 text-sm text-slate-700">
            <input type="checkbox" checked={ack} onChange={(e) => setAck(e.target.checked)} className="size-4 rounded border-slate-300" />
            Ya copié el token y el comando
          </label>
          <Button
            disabled={!ack}
            onClick={() => {
              setAck(false);
              onClose();
            }}
          >
            Cerrar
          </Button>
        </>
      }
    >
      {issued && (
        <div className="space-y-4">
          <Notice tone="amber">
            <strong>Este token se muestra una sola vez.</strong> Rampa guarda solo un hash. Si lo perdés vas a tener que rotarlo.
          </Notice>
          <CopyField label="Token del cluster" value={issued.cluster.token} secret />
          <CopyField label="Comando de instalación" value={issued.cluster.install_command} multiline />
          <p className="text-xs text-slate-500">
            Cluster <span className="font-medium text-slate-700">{issued.cluster.name}</span> · {envLabels[issued.cluster.env] ?? issued.cluster.env} · creado {formatDateTime(issued.cluster.created_at)}
          </p>
        </div>
      )}
    </Dialog>
  );
}
