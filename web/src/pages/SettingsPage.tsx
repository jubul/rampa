import { useState, type FormEvent } from "react";
import { useParams } from "react-router-dom";
import { KeyRound, Plus, Trash2, UserPlus } from "lucide-react";
import { useAddMember, useCreateToken, useMembers, useOrg, useRemoveMember, useRemoveToken, useTokens, useUpdateOrg } from "../api/hooks";
import { useAuth, useOrgRole } from "../auth/AuthContext";
import type { Member, Role, Token } from "../api/types";
import { ROLES } from "../api/types";
import { errorMessage } from "../api/client";
import { PageHeader } from "../components/ui/PageHeader";
import { Card, CardBody, CardHeader } from "../components/ui/Card";
import { Button } from "../components/ui/Button";
import { Input } from "../components/ui/Input";
import { Select } from "../components/ui/Select";
import { Field } from "../components/ui/Field";
import { Badge } from "../components/ui/Badge";
import { ConfirmDialog, Dialog } from "../components/ui/Dialog";
import { CopyField } from "../components/ui/CopyField";
import { EmptyState, ErrorState, InlineError, LoadingState, Notice } from "../components/ui/States";
import { Table, TBody, TD, TH, THead, TR } from "../components/ui/Table";
import { formatDateTime, formatRelative } from "../lib/format";
import { roleLabels } from "../lib/status";

const roleOptions = ROLES.map((r) => ({ value: r, label: roleLabels[r] ?? r }));
// La API rechaza tokens con rol owner.
const tokenRoleOptions = roleOptions.filter((o) => o.value !== "owner");

export function SettingsPage() {
  const { org = "" } = useParams();
  const orgQ = useOrg(org);
  const { isAdmin } = useOrgRole(org);
  const update = useUpdateOrg(org);

  return (
    <>
      <PageHeader title="Configuración" description="Miembros, tokens de API y datos de la organización." />
      <Card>
        <CardHeader title="Organización" />
        <CardBody>
          {orgQ.isPending ? (
            <LoadingState />
          ) : orgQ.isError ? (
            <ErrorState error={orgQ.error} onRetry={() => void orgQ.refetch()} />
          ) : (
            <dl className="grid gap-4 text-sm sm:grid-cols-4">
              <div>
                <dt className="text-xs text-slate-500">Nombre</dt>
                <dd className="mt-0.5 font-medium text-slate-900">{orgQ.data.name}</dd>
              </div>
              <div>
                <dt className="text-xs text-slate-500">Slug</dt>
                <dd className="mt-0.5 font-mono text-xs">{orgQ.data.slug}</dd>
              </div>
              <div>
                <dt className="text-xs text-slate-500">Creada</dt>
                <dd className="mt-0.5">{formatDateTime(orgQ.data.created_at)}</dd>
              </div>
              <div>
                <dt className="text-xs text-slate-500">Aprobación en prod</dt>
                <dd className="mt-0.5 flex items-center gap-2">
                  <Badge tone={orgQ.data.settings.require_approval_prod ? "violet" : "gray"}>
                    {orgQ.data.settings.require_approval_prod ? "Requerida" : "No requerida"}
                  </Badge>
                  <label className="flex items-center gap-1.5 text-xs text-slate-600" title={isAdmin ? "Los deploys a prod esperan la aprobación de un admin" : "Necesitás rol admin"}>
                    <input
                      type="checkbox"
                      className="size-4 rounded border-slate-300"
                      checked={orgQ.data.settings.require_approval_prod}
                      disabled={!isAdmin || update.isPending}
                      onChange={(e) => update.mutate({ settings: { require_approval_prod: e.target.checked } })}
                    />
                    Exigir aprobación
                  </label>
                </dd>
              </div>
            </dl>
          )}
          {update.isError && (
            <div className="mt-3">
              <InlineError error={update.error} />
            </div>
          )}
        </CardBody>
      </Card>

      <MembersCard org={org} />
      <TokensCard org={org} />
    </>
  );
}

function MembersCard({ org }: { org: string }) {
  const me = useAuth();
  const { isAdmin } = useOrgRole(org);
  const membersQ = useMembers(org);
  const add = useAddMember(org);
  const remove = useRemoveMember(org);
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<Role>("developer");
  const [toRemove, setToRemove] = useState<Member | null>(null);
  const [changing, setChanging] = useState<string | null>(null);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const v = email.trim();
    if (!v) return;
    add.mutate({ email: v, role }, { onSuccess: () => setEmail("") });
  };

  return (
    <Card className="mt-6">
      <CardHeader title="Miembros" description="Agregar por email crea el usuario si no existe. Cambiar el rol usa la misma acción." />
      {isAdmin && (
        <CardBody className="border-b border-slate-200">
          <form onSubmit={submit} className="flex flex-wrap items-end gap-3">
            <div className="min-w-[16rem] flex-1">
              <Field label="Email">
                {(id) => <Input id={id} type="email" required placeholder="persona@empresa.com" value={email} onChange={(e) => setEmail(e.target.value)} />}
              </Field>
            </div>
            <div className="w-40">
              <Field label="Rol">{(id) => <Select id={id} value={role} onChange={(e) => setRole(e.target.value as Role)} options={roleOptions} />}</Field>
            </div>
            <Button type="submit" loading={add.isPending} icon={<UserPlus className="size-4" />}>
              Agregar
            </Button>
          </form>
          {add.isError && (
            <div className="mt-3">
              <InlineError error={add.error} />
            </div>
          )}
        </CardBody>
      )}
      {membersQ.isPending ? (
        <LoadingState />
      ) : membersQ.isError ? (
        <ErrorState error={membersQ.error} onRetry={() => void membersQ.refetch()} />
      ) : membersQ.data.length === 0 ? (
        <EmptyState title="No hay miembros" />
      ) : (
        <Table>
          <THead>
            <tr>
              <TH>Usuario</TH>
              <TH>Email</TH>
              <TH>Rol</TH>
              <TH className="text-right">Acciones</TH>
            </tr>
          </THead>
          <TBody>
            {membersQ.data.map((m) => {
              const isSelf = m.user.id === me.user.id;
              return (
                <TR key={m.user.id}>
                  <TD className="font-medium text-slate-900">
                    {m.user.name || "-"}
                    {isSelf && <span className="ml-2 text-xs text-slate-400">(vos)</span>}
                  </TD>
                  <TD>{m.user.email}</TD>
                  <TD>
                    {isAdmin && !isSelf ? (
                      <Select
                        aria-label={`Rol de ${m.user.email}`}
                        className="w-36"
                        value={m.role}
                        disabled={changing === m.user.id && add.isPending}
                        onChange={(e) => {
                          setChanging(m.user.id);
                          add.mutate({ email: m.user.email, role: e.target.value as Role }, { onSettled: () => setChanging(null) });
                        }}
                        options={roleOptions}
                      />
                    ) : (
                      <Badge tone={m.role === "owner" ? "blue" : "gray"}>{roleLabels[m.role] ?? m.role}</Badge>
                    )}
                  </TD>
                  <TD>
                    <div className="flex justify-end">
                      <Button size="sm" variant="ghost" disabled={!isAdmin || isSelf} aria-label="Quitar" title={isSelf ? "No podés quitarte a vos mismo" : "Quitar de la organización"} icon={<Trash2 className="size-3.5 text-red-600" />} onClick={() => setToRemove(m)} />
                    </div>
                  </TD>
                </TR>
              );
            })}
          </TBody>
        </Table>
      )}
      <ConfirmDialog
        open={toRemove !== null}
        onClose={() => {
          setToRemove(null);
          remove.reset();
        }}
        title="Quitar miembro"
        description={toRemove ? `${toRemove.user.email} va a perder el acceso a esta organización.` : undefined}
        confirmLabel="Quitar"
        danger
        loading={remove.isPending}
        error={remove.isError ? errorMessage(remove.error) : null}
        onConfirm={() => {
          if (!toRemove) return;
          remove.mutate(toRemove.user.id, { onSuccess: () => setToRemove(null) });
        }}
      />
    </Card>
  );
}

function TokensCard({ org }: { org: string }) {
  const { isAdmin } = useOrgRole(org);
  const tokensQ = useTokens(org, isAdmin);
  const remove = useRemoveToken(org);
  const [createOpen, setCreateOpen] = useState(false);
  const [issued, setIssued] = useState<Token | null>(null);
  const [toDelete, setToDelete] = useState<Token | null>(null);

  return (
    <Card className="mt-6">
      <CardHeader
        title="Tokens de API"
        description="Para el CLI y la CI. Se guardan hasheados: el secreto se muestra una sola vez."
        actions={
          <Button size="sm" disabled={!isAdmin} title={isAdmin ? undefined : "Necesitás rol admin"} icon={<Plus className="size-3.5" />} onClick={() => setCreateOpen(true)}>
            Nuevo token
          </Button>
        }
      />
      {!isAdmin ? (
        <EmptyState icon={<KeyRound className="size-8" />} title="Solo para admins" description="Los tokens de API los gestionan los admins y owners de la organización." />
      ) : tokensQ.isPending ? (
        <LoadingState />
      ) : tokensQ.isError ? (
        <ErrorState error={tokensQ.error} onRetry={() => void tokensQ.refetch()} />
      ) : tokensQ.data.length === 0 ? (
        <EmptyState icon={<KeyRound className="size-8" />} title="No hay tokens" description="Creá uno para que la CI pueda llamar a la API." />
      ) : (
        <Table>
          <THead>
            <tr>
              <TH>Nombre</TH>
              <TH>Rol</TH>
              <TH>Creado</TH>
              <TH>Expira</TH>
              <TH>Último uso</TH>
              <TH className="text-right">Acciones</TH>
            </tr>
          </THead>
          <TBody>
            {tokensQ.data.map((t) => (
              <TR key={t.id}>
                <TD className="font-medium text-slate-900">{t.name}</TD>
                <TD>
                  <Badge tone="gray">{roleLabels[t.role] ?? t.role}</Badge>
                </TD>
                <TD className="text-slate-500">{formatDateTime(t.created_at)}</TD>
                <TD className="text-slate-500">{t.expires_at ? formatRelative(t.expires_at) : "nunca"}</TD>
                <TD className="text-slate-500">{t.last_used_at ? formatRelative(t.last_used_at) : "-"}</TD>
                <TD>
                  <div className="flex justify-end">
                    <Button size="sm" variant="ghost" disabled={!isAdmin} aria-label="Eliminar" title="Eliminar token" icon={<Trash2 className="size-3.5 text-red-600" />} onClick={() => setToDelete(t)} />
                  </div>
                </TD>
              </TR>
            ))}
          </TBody>
        </Table>
      )}

      <Dialog open={createOpen} onClose={() => setCreateOpen(false)} title="Nuevo token de API">
        {createOpen && (
          <CreateTokenForm
            org={org}
            onClose={() => setCreateOpen(false)}
            onCreated={(t) => {
              setCreateOpen(false);
              setIssued(t);
            }}
          />
        )}
      </Dialog>

      <TokenIssuedDialog token={issued} onClose={() => setIssued(null)} />

      <ConfirmDialog
        open={toDelete !== null}
        onClose={() => {
          setToDelete(null);
          remove.reset();
        }}
        title="Eliminar token"
        description={toDelete ? `"${toDelete.name}" deja de valer al instante. Lo que lo use va a recibir 401.` : undefined}
        confirmLabel="Eliminar"
        danger
        loading={remove.isPending}
        error={remove.isError ? errorMessage(remove.error) : null}
        onConfirm={() => {
          if (!toDelete) return;
          remove.mutate(toDelete.id, { onSuccess: () => setToDelete(null) });
        }}
      />
    </Card>
  );
}

function CreateTokenForm({ org, onClose, onCreated }: { org: string; onClose: () => void; onCreated: (t: Token) => void }) {
  const create = useCreateToken(org);
  const [name, setName] = useState("");
  const [role, setRole] = useState<Role>("developer");
  const [days, setDays] = useState("365");

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const n = Number.parseInt(days, 10);
    if (!name.trim() || !Number.isFinite(n) || n <= 0) return;
    create.mutate({ name: name.trim(), role, expires_in_days: n }, { onSuccess: onCreated });
  };

  return (
    <form onSubmit={submit} className="space-y-4">
      <Field label="Nombre" required help="Por ejemplo el repo o el pipeline que lo va a usar.">
        {(id) => <Input id={id} required placeholder="ci-checkout" value={name} onChange={(e) => setName(e.target.value)} />}
      </Field>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Rol" required help="Developer alcanza para importar y desplegar a no prod.">
          {(id) => <Select id={id} value={role} onChange={(e) => setRole(e.target.value as Role)} options={tokenRoleOptions} />}
        </Field>
        <Field label="Expira en (días)" required>
          {(id) => <Input id={id} type="number" min={1} max={3650} required value={days} onChange={(e) => setDays(e.target.value)} />}
        </Field>
      </div>
      {create.isError && <InlineError error={create.error} />}
      <div className="flex justify-end gap-2">
        <Button variant="secondary" onClick={onClose}>
          Cancelar
        </Button>
        <Button type="submit" loading={create.isPending}>
          Crear token
        </Button>
      </div>
    </form>
  );
}

function TokenIssuedDialog({ token, onClose }: { token: Token | null; onClose: () => void }) {
  const [ack, setAck] = useState(false);
  return (
    <Dialog
      open={token !== null}
      onClose={onClose}
      persistent
      title="Token creado"
      footer={
        <>
          <label className="mr-auto flex items-center gap-2 text-sm text-slate-700">
            <input type="checkbox" checked={ack} onChange={(e) => setAck(e.target.checked)} className="size-4 rounded border-slate-300" />
            Ya lo guardé en un lugar seguro
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
      {token && (
        <div className="space-y-4">
          <Notice tone="amber">
            <strong>Este token se muestra una sola vez.</strong> Guardalo ahora: no vas a poder volver a verlo.
          </Notice>
          <CopyField label={`Token "${token.name}" (${roleLabels[token.role] ?? token.role})`} value={token.token ?? ""} secret />
          <p className="text-xs text-slate-500">Usalo como header: Authorization: Bearer {"<token>"}. Con el CLI: rampa login --token ...</p>
        </div>
      )}
    </Dialog>
  );
}
