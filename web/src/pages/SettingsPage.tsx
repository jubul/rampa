import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
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
import { roleLabel } from "../lib/status";

const roleOptions = () => ROLES.map((r) => ({ value: r, label: roleLabel(r) }));
// La API rechaza tokens con rol owner.
const tokenRoleOptions = () => roleOptions().filter((o) => o.value !== "owner");

export function SettingsPage() {
  const { t } = useTranslation();
  const { org = "" } = useParams();
  const orgQ = useOrg(org);
  const { isAdmin } = useOrgRole(org);
  const update = useUpdateOrg(org);

  return (
    <>
      <PageHeader title={t("nav.settings")} description={t("settings.description")} />
      <Card>
        <CardHeader title={t("settings.organization")} />
        <CardBody>
          {orgQ.isPending ? (
            <LoadingState />
          ) : orgQ.isError ? (
            <ErrorState error={orgQ.error} onRetry={() => void orgQ.refetch()} />
          ) : (
            <dl className="grid gap-4 text-sm sm:grid-cols-4">
              <div>
                <dt className="text-xs text-slate-500 dark:text-slate-400">{t("settings.name")}</dt>
                <dd className="mt-0.5 font-medium text-slate-900 dark:text-slate-100">{orgQ.data.name}</dd>
              </div>
              <div>
                <dt className="text-xs text-slate-500 dark:text-slate-400">Slug</dt>
                <dd className="mt-0.5 font-mono text-xs">{orgQ.data.slug}</dd>
              </div>
              <div>
                <dt className="text-xs text-slate-500 dark:text-slate-400">{t("settings.created")}</dt>
                <dd className="mt-0.5">{formatDateTime(orgQ.data.created_at)}</dd>
              </div>
              <div>
                <dt className="text-xs text-slate-500 dark:text-slate-400">{t("settings.prodApproval")}</dt>
                <dd className="mt-0.5 flex items-center gap-2">
                  <Badge tone={orgQ.data.settings.require_approval_prod ? "violet" : "gray"}>
                    {orgQ.data.settings.require_approval_prod ? t("settings.required") : t("settings.notRequired")}
                  </Badge>
                  <label className="flex items-center gap-1.5 text-xs text-slate-600 dark:text-slate-300" title={isAdmin ? t("settings.requireHint") : t("common.needAdmin")}>
                    <input
                      type="checkbox"
                      className="size-4 rounded border-slate-300 dark:border-slate-700"
                      checked={orgQ.data.settings.require_approval_prod}
                      disabled={!isAdmin || update.isPending}
                      onChange={(e) => update.mutate({ settings: { ...orgQ.data.settings, require_approval_prod: e.target.checked } })}
                    />
                    {t("settings.requireApproval")}
                  </label>
                </dd>
              </div>
              <GithubOrgField
                key={orgQ.data.settings.github_org ?? ""}
                value={orgQ.data.settings.github_org ?? ""}
                canEdit={isAdmin}
                saving={update.isPending}
                onSave={(v) => update.mutate({ settings: { ...orgQ.data.settings, github_org: v } })}
              />
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
  const { t } = useTranslation();
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
      <CardHeader title={t("settings.members")} description={t("settings.membersDescription")} />
      {isAdmin && (
        <CardBody className="border-b border-slate-200 dark:border-slate-800">
          <form onSubmit={submit} className="flex flex-wrap items-end gap-3">
            <div className="min-w-[16rem] flex-1">
              <Field label="Email">
                {(id) => <Input id={id} type="email" required placeholder={t("settings.emailPlaceholder")} value={email} onChange={(e) => setEmail(e.target.value)} />}
              </Field>
            </div>
            <div className="w-40">
              <Field label={t("settings.role")}>{(id) => <Select id={id} value={role} onChange={(e) => setRole(e.target.value as Role)} options={roleOptions()} />}</Field>
            </div>
            <Button type="submit" loading={add.isPending} icon={<UserPlus className="size-4" />}>
              {t("settings.add")}
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
        <EmptyState title={t("settings.noMembers")} />
      ) : (
        <Table>
          <THead>
            <tr>
              <TH>{t("settings.colUser")}</TH>
              <TH>Email</TH>
              <TH>{t("settings.role")}</TH>
              <TH className="text-right">{t("settings.colActions")}</TH>
            </tr>
          </THead>
          <TBody>
            {membersQ.data.map((m) => {
              const isSelf = m.user.id === me.user.id;
              return (
                <TR key={m.user.id}>
                  <TD className="font-medium text-slate-900 dark:text-slate-100">
                    {m.user.name || "-"}
                    {isSelf && <span className="ml-2 text-xs text-slate-500 dark:text-slate-400">{t("settings.you")}</span>}
                  </TD>
                  <TD>{m.user.email}</TD>
                  <TD>
                    {isAdmin && !isSelf ? (
                      <Select
                        aria-label={t("settings.roleOf", { email: m.user.email })}
                        className="w-36"
                        value={m.role}
                        disabled={changing === m.user.id && add.isPending}
                        onChange={(e) => {
                          setChanging(m.user.id);
                          add.mutate({ email: m.user.email, role: e.target.value as Role }, { onSettled: () => setChanging(null) });
                        }}
                        options={roleOptions()}
                      />
                    ) : (
                      <Badge tone={m.role === "owner" ? "blue" : "gray"}>{roleLabel(m.role)}</Badge>
                    )}
                  </TD>
                  <TD>
                    <div className="flex justify-end">
                      <Button size="sm" variant="ghost" disabled={!isAdmin || isSelf} aria-label={t("settings.remove")} title={isSelf ? t("settings.cantRemoveSelf") : t("settings.removeFromOrg")} icon={<Trash2 className="size-3.5 text-red-600 dark:text-red-400" />} onClick={() => setToRemove(m)} />
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
        title={t("settings.removeTitle")}
        description={toRemove ? t("settings.removeDescription", { email: toRemove.user.email }) : undefined}
        confirmLabel={t("settings.remove")}
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
  const { t } = useTranslation();
  const { isAdmin } = useOrgRole(org);
  const tokensQ = useTokens(org, isAdmin);
  const remove = useRemoveToken(org);
  const [createOpen, setCreateOpen] = useState(false);
  const [issued, setIssued] = useState<Token | null>(null);
  const [toDelete, setToDelete] = useState<Token | null>(null);

  return (
    <Card className="mt-6">
      <CardHeader
        title={t("settings.tokens")}
        description={t("settings.tokensDescription")}
        actions={
          <Button size="sm" disabled={!isAdmin} title={isAdmin ? undefined : t("common.needAdmin")} icon={<Plus className="size-3.5" />} onClick={() => setCreateOpen(true)}>
            {t("settings.newToken")}
          </Button>
        }
      />
      {!isAdmin ? (
        <EmptyState icon={<KeyRound className="size-8" />} title={t("settings.adminsOnly")} description={t("settings.adminsOnlyDescription")} />
      ) : tokensQ.isPending ? (
        <LoadingState />
      ) : tokensQ.isError ? (
        <ErrorState error={tokensQ.error} onRetry={() => void tokensQ.refetch()} />
      ) : tokensQ.data.length === 0 ? (
        <EmptyState icon={<KeyRound className="size-8" />} title={t("settings.noTokens")} description={t("settings.noTokensDescription")} />
      ) : (
        <Table>
          <THead>
            <tr>
              <TH>{t("settings.name")}</TH>
              <TH>{t("settings.role")}</TH>
              <TH>{t("settings.colCreated")}</TH>
              <TH>{t("settings.colExpires")}</TH>
              <TH>{t("settings.colLastUsed")}</TH>
              <TH className="text-right">{t("settings.colActions")}</TH>
            </tr>
          </THead>
          <TBody>
            {tokensQ.data.map((tk) => (
              <TR key={tk.id}>
                <TD className="font-medium text-slate-900 dark:text-slate-100">{tk.name}</TD>
                <TD>
                  <Badge tone="gray">{roleLabel(tk.role)}</Badge>
                </TD>
                <TD className="text-slate-500 dark:text-slate-400">{formatDateTime(tk.created_at)}</TD>
                <TD className="text-slate-500 dark:text-slate-400">{tk.expires_at ? formatRelative(tk.expires_at) : t("settings.never")}</TD>
                <TD className="text-slate-500 dark:text-slate-400">{tk.last_used_at ? formatRelative(tk.last_used_at) : "-"}</TD>
                <TD>
                  <div className="flex justify-end">
                    <Button size="sm" variant="ghost" disabled={!isAdmin} aria-label={t("common.delete")} title={t("settings.deleteToken")} icon={<Trash2 className="size-3.5 text-red-600 dark:text-red-400" />} onClick={() => setToDelete(tk)} />
                  </div>
                </TD>
              </TR>
            ))}
          </TBody>
        </Table>
      )}

      <Dialog open={createOpen} onClose={() => setCreateOpen(false)} title={t("settings.newTokenTitle")}>
        {createOpen && (
          <CreateTokenForm
            org={org}
            onClose={() => setCreateOpen(false)}
            onCreated={(tk) => {
              setCreateOpen(false);
              setIssued(tk);
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
        title={t("settings.deleteToken")}
        description={toDelete ? t("settings.deleteTokenDescription", { name: toDelete.name }) : undefined}
        confirmLabel={t("common.delete")}
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

function CreateTokenForm({ org, onClose, onCreated }: { org: string; onClose: () => void; onCreated: (tk: Token) => void }) {
  const { t } = useTranslation();
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
      <Field label={t("settings.name")} required help={t("settings.tokenNameHelp")}>
        {(id) => <Input id={id} required placeholder="ci-checkout" value={name} onChange={(e) => setName(e.target.value)} />}
      </Field>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label={t("settings.role")} required help={t("settings.tokenRoleHelp")}>
          {(id) => <Select id={id} value={role} onChange={(e) => setRole(e.target.value as Role)} options={tokenRoleOptions()} />}
        </Field>
        <Field label={t("settings.expiresIn")} required>
          {(id) => <Input id={id} type="number" min={1} max={3650} required value={days} onChange={(e) => setDays(e.target.value)} />}
        </Field>
      </div>
      {create.isError && <InlineError error={create.error} />}
      <div className="flex justify-end gap-2">
        <Button variant="secondary" onClick={onClose}>
          {t("common.cancel")}
        </Button>
        <Button type="submit" loading={create.isPending}>
          {t("settings.createToken")}
        </Button>
      </div>
    </form>
  );
}

function TokenIssuedDialog({ token, onClose }: { token: Token | null; onClose: () => void }) {
  const { t } = useTranslation();
  const [ack, setAck] = useState(false);
  return (
    <Dialog
      open={token !== null}
      onClose={onClose}
      persistent
      title={t("settings.tokenCreated")}
      footer={
        <>
          <label className="mr-auto flex items-center gap-2 text-sm text-slate-700 dark:text-slate-200">
            <input type="checkbox" checked={ack} onChange={(e) => setAck(e.target.checked)} className="size-4 rounded border-slate-300 dark:border-slate-700" />
            {t("settings.ackSaved")}
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
      {token && (
        <div className="space-y-4">
          <Notice tone="amber">
            <strong>{t("settings.tokenOnceTitle")}</strong> {t("settings.tokenOnceBody")}
          </Notice>
          <CopyField label={t("settings.tokenLabel", { name: token.name, role: roleLabel(token.role) })} value={token.token ?? ""} secret />
          <p className="text-xs text-slate-500 dark:text-slate-400">{t("settings.tokenUsage")}</p>
        </div>
      )}
    </Dialog>
  );
}

function GithubOrgField({ value, canEdit, saving, onSave }: { value: string; canEdit: boolean; saving: boolean; onSave: (v: string) => void }) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState(value);
  const dirty = draft.trim() !== value;
  return (
    <div className="sm:col-span-4">
      <dt className="text-xs text-slate-500 dark:text-slate-400">{t("settings.githubOrg")}</dt>
      <dd className="mt-1 flex flex-wrap items-center gap-2">
        <Input
          className="max-w-xs"
          aria-label={t("settings.githubOrg")}
          value={draft}
          disabled={!canEdit}
          placeholder="acme"
          onChange={(e) => setDraft(e.target.value)}
        />
        <Button size="sm" variant="secondary" disabled={!canEdit || !dirty} loading={saving} onClick={() => onSave(draft.trim())}>
          {t("common.save")}
        </Button>
        <span className="text-xs text-slate-500 dark:text-slate-400">{t("settings.githubOrgHelp")}</span>
      </dd>
    </div>
  );
}
