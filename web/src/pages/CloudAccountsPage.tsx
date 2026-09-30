import { useMemo, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useParams, useSearchParams } from "react-router-dom";
import { CheckCircle2, Cloud, ExternalLink, KeyRound, Plus, RefreshCw, ShieldCheck, Trash2, XCircle } from "lucide-react";
import {
  useCloudAccounts,
  useCreateCloudAccount,
  usePatchCloudAccount,
  useProviders,
  useRemoveCloudAccount,
  useVerifyCloudAccount,
} from "../api/hooks";
import { useOrgRole } from "../auth/AuthContext";
import type { CloudAccount, InstructionField, Provider } from "../api/types";
import { PageHeader } from "../components/ui/PageHeader";
import { Card } from "../components/ui/Card";
import { Button } from "../components/ui/Button";
import { Input } from "../components/ui/Input";
import { Field } from "../components/ui/Field";
import { Badge } from "../components/ui/Badge";
import { Dialog, ConfirmDialog } from "../components/ui/Dialog";
import { CopyField } from "../components/ui/CopyField";
import { EmptyState, ErrorState, InlineError, LoadingState, Notice } from "../components/ui/States";
import { Table, TBody, TD, TH, THead, TR } from "../components/ui/Table";
import { StatusBadge } from "../components/StatusBadge";
import { formatDateTime } from "../lib/format";
import { errorMessage, isApiError } from "../api/client";
import { useTerms } from "../hooks/useTerms";
import { cap, termsFor, type Terms } from "../lib/terminology";
import { cn } from "../lib/cn";

const providerLabels: Record<string, string> = { aws: "AWS", gcp: "Google Cloud", azure: "Azure", digitalocean: "DigitalOcean", oci: "Oracle Cloud", ovh: "OVHcloud" };
const defaultRegion: Record<string, string> = { aws: "us-east-1", gcp: "us-central1", azure: "eastus", digitalocean: "nyc3", oci: "us-ashburn-1", ovh: "GRA" };

/** Un 501 de la API (por ejemplo sin RAMPA_STATE_KEY) trae en el mensaje qué falta configurar. */
function NotImplementedNotice({ error }: { error: unknown }) {
  const { t } = useTranslation();
  if (!isApiError(error) || error.status !== 501) return null;
  return (
    <Notice tone="amber">
      <p className="font-medium">{t("cloudAccounts.notImplemented")}</p>
      <p className="mt-0.5">{error.message}</p>
    </Notice>
  );
}

function ErrorOrNotImplemented({ error }: { error: unknown }) {
  if (!error) return null;
  if (isApiError(error) && error.status === 501) return <NotImplementedNotice error={error} />;
  return <InlineError error={error} />;
}

export function CloudAccountsPage() {
  const { t } = useTranslation();
  const { org = "" } = useParams();
  const { terms } = useTerms(org);
  const generic = terms();
  const { isAdmin } = useOrgRole(org);
  const accountsQ = useCloudAccounts(org);
  const providersQ = useProviders(org);
  const verify = useVerifyCloudAccount(org);
  const remove = useRemoveCloudAccount(org);

  const [searchParams, setSearchParams] = useSearchParams();
  const [wizardOpen, setWizardOpen] = useState(searchParams.get("connect") === "1");
  const [resumeAccount, setResumeAccount] = useState<CloudAccount | null>(null);
  const [toDelete, setToDelete] = useState<CloudAccount | null>(null);
  const [verifyingId, setVerifyingId] = useState<string | null>(null);
  const [toRotate, setToRotate] = useState<CloudAccount | null>(null);

  const accountTerms = (a: CloudAccount): Terms => terms(a.provider);
  const providerName = (kind: string) => providersQ.data?.find((p) => p.kind === kind)?.display_name ?? providerLabels[kind] ?? kind;
  const adminTitle = isAdmin ? undefined : t("common.needAdmin");

  return (
    <>
      <PageHeader
        title={t("nav.cloudAccounts")}
        description={t("cloudAccounts.description")}
        actions={
          <Button onClick={() => setWizardOpen(true)} disabled={!isAdmin} title={adminTitle} icon={<Plus className="size-4" />}>
            {t("cloudAccounts.connect")}
          </Button>
        }
      />

      <Card>
        {accountsQ.isPending ? (
          <LoadingState />
        ) : accountsQ.isError ? (
          <ErrorState error={accountsQ.error} onRetry={() => void accountsQ.refetch()} />
        ) : accountsQ.data.length === 0 ? (
          <EmptyState
            icon={<Cloud className="size-8" />}
            title={t("cloudAccounts.emptyTitle")}
            description={t("cloudAccounts.emptyDescription", { account: generic.account })}
            action={
              <Button onClick={() => setWizardOpen(true)} disabled={!isAdmin} icon={<Plus className="size-4" />}>
                {t("cloudAccounts.connect")}
              </Button>
            }
          />
        ) : (
          <Table>
            <THead>
              <tr>
                <TH>{t("cloudAccounts.colName")}</TH>
                <TH>{t("cloudAccounts.colProvider")}</TH>
                <TH>{cap(generic.region)}</TH>
                <TH>{t("cloudAccounts.colStatus")}</TH>
                <TH>{t("cloudAccounts.colReference")}</TH>
                <TH>{t("cloudAccounts.colCreated")}</TH>
                <TH className="text-right">{t("cloudAccounts.colActions")}</TH>
              </tr>
            </THead>
            <TBody>
              {accountsQ.data.map((a) => {
                const isToken = providersQ.data?.find((p) => p.kind === a.provider)?.auth === "token" || a.has_credentials === true;
                const ready = Boolean(a.external_ref) || a.has_credentials === true;
                return (
                <TR key={a.id}>
                  <TD className="font-medium text-slate-900 dark:text-slate-100">{a.name}</TD>
                  <TD>{providerName(a.provider)}</TD>
                  <TD className="font-mono text-xs">{a.region}</TD>
                  <TD>
                    <div className="flex flex-col gap-1">
                      <StatusBadge status={a.status} />
                      {a.verification && !a.verification.ok && a.status === "failed" && (
                        <span className="max-w-xs truncate text-xs text-red-600 dark:text-red-400" title={a.verification.message}>
                          {a.verification.message}
                        </span>
                      )}
                      {a.verification?.ok && a.verification.identity && (
                        <span className="max-w-xs truncate font-mono text-[11px] text-slate-500 dark:text-slate-400" title={a.verification.identity}>
                          {a.verification.identity}
                        </span>
                      )}
                    </div>
                  </TD>
                  <TD className="max-w-xs font-mono text-xs">
                    {isToken ? (
                      a.has_credentials ? (
                        <Badge tone="green">
                          <ShieldCheck className="size-3" aria-hidden />
                          {t("cloudAccounts.credentialsSaved")}
                        </Badge>
                      ) : (
                        <span className="font-sans text-slate-500 dark:text-slate-400">{t("cloudAccounts.noCredentials")}</span>
                      )
                    ) : (
                      <span className="block truncate" title={a.external_ref}>
                        {a.external_ref || <span className="font-sans text-slate-500 dark:text-slate-400">{t("cloudAccounts.incomplete")}</span>}
                      </span>
                    )}
                  </TD>
                  <TD className="text-slate-500 dark:text-slate-400">{formatDateTime(a.created_at)}</TD>
                  <TD>
                    <div className="flex justify-end gap-1.5">
                      {a.status !== "connected" && (
                        <Button size="sm" variant="secondary" disabled={!isAdmin} title={adminTitle} onClick={() => setResumeAccount(a)}>
                          {ready ? t("cloudAccounts.review") : t("cloudAccounts.continue")}
                        </Button>
                      )}
                      {isToken && (
                        <Button size="sm" variant="secondary" disabled={!isAdmin} title={adminTitle} icon={<KeyRound className="size-3.5" />} onClick={() => setToRotate(a)}>
                          {t("cloudAccounts.rotate")}
                        </Button>
                      )}
                      <Button
                        size="sm"
                        variant="secondary"
                        disabled={!isAdmin || !ready}
                        title={!ready ? t("cloudAccounts.completeFirst") : t("cloudAccounts.checkAccess")}
                        loading={verify.isPending && verifyingId === a.id}
                        icon={<RefreshCw className="size-3.5" />}
                        onClick={() => {
                          setVerifyingId(a.id);
                          verify.mutate(a.id, { onSettled: () => setVerifyingId(null) });
                        }}
                      >
                        {t("cloudAccounts.verify")}
                      </Button>
                      <Button size="sm" variant="ghost" disabled={!isAdmin} aria-label={t("common.delete")} title={t("common.delete")} icon={<Trash2 className="size-3.5 text-red-600 dark:text-red-400" />} onClick={() => setToDelete(a)} />
                    </div>
                  </TD>
                </TR>
                );
              })}
            </TBody>
          </Table>
        )}
        {verify.isError && (
          <div className="border-t border-slate-200 dark:border-slate-800 px-5 py-3">
            <InlineError error={verify.error} />
          </div>
        )}
      </Card>

      <ConnectWizard
        org={org}
        open={(wizardOpen && !providersQ.isPending) || resumeAccount !== null}
        providers={providersQ.data ?? []}
        providersError={providersQ.isError ? providersQ.error : null}
        initialAccount={resumeAccount}
        onClose={() => {
          setWizardOpen(false);
          setResumeAccount(null);
          if (searchParams.has("connect")) setSearchParams({}, { replace: true });
        }}
      />

      <RotateDialog
        org={org}
        account={toRotate}
        terms={toRotate ? accountTerms(toRotate) : generic}
        onClose={() => setToRotate(null)}
      />

      <ConfirmDialog
        open={toDelete !== null}
        onClose={() => {
          setToDelete(null);
          remove.reset();
        }}
        title={t("cloudAccounts.deleteTitle", { account: toDelete ? accountTerms(toDelete).account : generic.account })}
        description={toDelete ? t("cloudAccounts.deleteDescription", { name: toDelete.name, cluster: accountTerms(toDelete).cluster }) : undefined}
        confirmLabel={t("common.delete")}
        danger
        loading={remove.isPending}
        error={remove.isError ? errorMessage(remove.error) : null}
        onConfirm={() => {
          if (!toDelete) return;
          remove.mutate(toDelete.id, { onSuccess: () => setToDelete(null) });
        }}
      />
    </>
  );
}

type Step = "choose" | "instructions" | "verify";

function ConnectWizard({
  org,
  open,
  providers,
  providersError,
  initialAccount,
  onClose,
}: {
  org: string;
  open: boolean;
  providers: Provider[];
  providersError: unknown;
  initialAccount: CloudAccount | null;
  onClose: () => void;
}) {
  // Cuando se abre para retomar una cuenta pendiente arrancamos en instrucciones.
  const { t } = useTranslation();
  const initialStep: Step = initialAccount ? (initialAccount.external_ref || initialAccount.has_credentials ? "verify" : "instructions") : "choose";
  return (
    <Dialog open={open} onClose={onClose} title={initialAccount ? t("cloudAccounts.wizardTitleResume", { name: initialAccount.name }) : t("cloudAccounts.wizardTitle")} size="lg">
      {open && <WizardBody key={initialAccount?.id ?? "new"} org={org} providers={providers} providersError={providersError} initialAccount={initialAccount} initialStep={initialStep} onClose={onClose} />}
    </Dialog>
  );
}

function WizardBody({
  org,
  providers,
  providersError,
  initialAccount,
  initialStep,
  onClose,
}: {
  org: string;
  providers: Provider[];
  providersError: unknown;
  initialAccount: CloudAccount | null;
  initialStep: Step;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const create = useCreateCloudAccount(org);
  const patch = usePatchCloudAccount(org);
  const verify = useVerifyCloudAccount(org);

  const [step, setStep] = useState<Step>(initialStep);
  const [account, setAccount] = useState<CloudAccount | null>(initialAccount);
  const [provider, setProvider] = useState<string>(providers.find((p) => p.available)?.kind ?? "");
  const [name, setName] = useState("");
  const [region, setRegion] = useState(defaultRegion[provider] ?? "");
  const [values, setValues] = useState<Record<string, string>>(() => initialValues(initialAccount));
  const chosen = providers.find((p) => p.kind === (account?.provider ?? provider));
  const chosenTerms = termsFor(chosen);

  const fields: InstructionField[] = useMemo(() => {
    const f = account?.instructions?.fields;
    if (f && f.length > 0) return f;
    // Si la API no devolvió instrucciones al releer la cuenta, pedimos al menos la referencia externa.
    return [
      {
        name: "external_ref",
        label: t("cloudAccounts.externalRef"),
        placeholder: "arn:aws:iam::123456789012:role/RampaConnect",
        help: t("cloudAccounts.externalRefHelp"),
        required: true,
      },
    ];
  }, [account, t]);

  const submitCreate = (e: FormEvent) => {
    e.preventDefault();
    if (!provider || !name.trim() || !region.trim()) return;
    create.mutate(
      { provider, name: name.trim(), region: region.trim() },
      {
        onSuccess: (acc) => {
          setAccount(acc);
          setValues(initialValues(acc));
          setStep("instructions");
        },
      },
    );
  };

  const submitFields = (e: FormEvent) => {
    e.preventDefault();
    if (!account) return;
    const settings: Record<string, string> = {};
    let externalRef: string | undefined;
    for (const f of fields) {
      const v = (values[f.name] ?? "").trim();
      // Un secreto vacío en una cuenta que ya tiene credenciales guardadas no se toca.
      if (f.required && !v && !(f.secret && account.has_credentials)) return;
      if (f.secret && !v) continue;
      if (f.name === "external_ref") externalRef = v;
      else if (v) settings[f.name] = v;
    }
    patch.mutate(
      { id: account.id, body: { ...(externalRef !== undefined ? { external_ref: externalRef } : {}), settings } },
      {
        onSuccess: (acc) => {
          setAccount(acc);
          // Los secretos no se vuelven a mostrar: los sacamos del estado apenas se guardan.
          setValues(initialValues(acc));
          setStep("verify");
        },
      },
    );
  };

  const runVerify = () => {
    if (!account) return;
    verify.mutate(account.id, { onSuccess: (acc) => setAccount(acc) });
  };

  return (
    <div className="space-y-5">
      <Steps current={step} />

      {step === "choose" && (
        <form onSubmit={submitCreate} className="space-y-4">
          {providersError ? <InlineError error={providersError} /> : null}
          <div>
            <p className="mb-2 text-sm font-medium text-slate-700 dark:text-slate-200">{t("cloudAccounts.provider")}</p>
            <div className="grid gap-2 sm:grid-cols-3">
              {providers.map((p) => {
                const pt = termsFor(p);
                return (
                <button
                  key={p.kind}
                  type="button"
                  disabled={!p.available}
                  onClick={() => {
                    setProvider(p.kind);
                    setRegion(defaultRegion[p.kind] ?? "");
                  }}
                  className={cn(
                    "flex flex-col items-start gap-1 rounded-md border px-3 py-3 text-left text-sm",
                    provider === p.kind ? "border-slate-900 ring-1 ring-slate-900 dark:border-slate-200 dark:ring-slate-200" : "border-slate-200 dark:border-slate-800 hover:border-slate-300 dark:hover:border-slate-700",
                    !p.available && "cursor-not-allowed opacity-60 hover:border-slate-200 dark:hover:border-slate-800",
                  )}
                >
                  <span className="font-medium text-slate-900 dark:text-slate-100">{p.display_name}</span>
                  <span className="text-xs text-slate-500 dark:text-slate-400">{cap(pt.account)}</span>
                  {p.available ? (
                    <span className="text-xs text-slate-500 dark:text-slate-400">{p.auth === "token" ? t("cloudAccounts.authToken") : t("cloudAccounts.authFederated")}</span>
                  ) : (
                    <Badge tone="gray">{t("cloudAccounts.comingSoon")}</Badge>
                  )}
                </button>
                );
              })}
              {providers.length === 0 && !providersError && <p className="text-sm text-slate-500 dark:text-slate-400">{t("cloudAccounts.loadingProviders")}</p>}
            </div>
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label={t("cloudAccounts.fieldName")} required help={t("cloudAccounts.fieldNameHelp", { account: chosenTerms.account })}>
              {(id) => <Input id={id} required value={name} onChange={(e) => setName(e.target.value)} placeholder="acme-prod" />}
            </Field>
            <Field label={cap(chosenTerms.region)} required help={t("cloudAccounts.fieldRegionHelp", { registry: chosenTerms.registry })}>
              {(id) => <Input id={id} required value={region} onChange={(e) => setRegion(e.target.value)} placeholder="us-east-1" />}
            </Field>
          </div>
          {create.isError && <ErrorOrNotImplemented error={create.error} />}
          <div className="flex justify-end gap-2">
            <Button variant="secondary" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={!provider} loading={create.isPending}>
              {t("cloudAccounts.createAndInstructions")}
            </Button>
          </div>
        </form>
      )}

      {step === "instructions" && account && (
        <form onSubmit={submitFields} className="space-y-4">
          {account.instructions ? (
            <div className="space-y-3 rounded-md border border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-slate-800/60 p-4">
              <div>
                <p className="text-sm font-semibold text-slate-900 dark:text-slate-100">{account.instructions.title}</p>
                <p className="mt-1 whitespace-pre-line text-sm text-slate-600 dark:text-slate-300">{account.instructions.description}</p>
              </div>
              {account.instructions.url && (
                <a
                  href={account.instructions.url}
                  target="_blank"
                  rel="noreferrer noopener"
                  className="inline-flex h-9 items-center gap-2 rounded-md bg-slate-900 px-3.5 text-sm font-medium text-white hover:bg-slate-800 dark:bg-slate-100 dark:text-slate-900 dark:hover:bg-white"
                >
                  <ExternalLink className="size-4" aria-hidden />
                  {t("cloudAccounts.openConsole", { provider: chosen?.display_name ?? providerLabels[account.provider] ?? account.provider })}
                </a>
              )}
              {account.instructions.command && <CopyField label={t("cloudAccounts.orCommandLine")} value={account.instructions.command} multiline />}
            </div>
          ) : (
            <Notice tone="blue">{t("cloudAccounts.noInstructions", { account: chosenTerms.account })}</Notice>
          )}

          <div className="space-y-3">
            <p className="text-sm font-medium text-slate-700 dark:text-slate-200">{t("cloudAccounts.pasteWhenDone")}</p>
            {fields.map((f) => (
              <FieldInput
                key={f.name}
                field={f}
                value={values[f.name] ?? ""}
                hasCredentials={account.has_credentials === true}
                onChange={(v) => setValues((cur) => ({ ...cur, [f.name]: v }))}
              />
            ))}
          </div>
          {patch.isError && <ErrorOrNotImplemented error={patch.error} />}
          <div className="flex justify-between gap-2">
            <Button variant="ghost" onClick={onClose}>
              {t("cloudAccounts.later")}
            </Button>
            <Button type="submit" loading={patch.isPending}>
              {t("cloudAccounts.saveContinue")}
            </Button>
          </div>
        </form>
      )}

      {step === "verify" && account && (
        <div className="space-y-4">
          <div className="rounded-md border border-slate-200 dark:border-slate-800 p-4 text-sm">
            <dl className="grid grid-cols-3 gap-y-2">
              <dt className="text-slate-500 dark:text-slate-400">{cap(chosenTerms.account)}</dt>
              <dd className="col-span-2 font-medium text-slate-900 dark:text-slate-100">{account.name}</dd>
              {account.external_ref || !account.has_credentials ? (
                <>
                  <dt className="text-slate-500 dark:text-slate-400">{t("cloudAccounts.colReference")}</dt>
                  <dd className="col-span-2 break-all font-mono text-xs">{account.external_ref || "-"}</dd>
                </>
              ) : (
                <>
                  <dt className="text-slate-500 dark:text-slate-400">{t("cloudAccounts.credentials")}</dt>
                  <dd className="col-span-2">
                    <Badge tone="green">{t("cloudAccounts.credentialsSaved")}</Badge>
                  </dd>
                </>
              )}
              <dt className="text-slate-500 dark:text-slate-400">{t("cloudAccounts.colStatus")}</dt>
              <dd className="col-span-2">
                <StatusBadge status={account.status} />
              </dd>
            </dl>
          </div>

          {account.verification && (
            <div
              className={cn(
                "flex items-start gap-3 rounded-md border p-4 text-sm",
                account.verification.ok ? "border-green-200 dark:border-green-900 bg-green-50 dark:bg-green-950/40 text-green-900 dark:text-green-200" : "border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 text-red-900 dark:text-red-200",
              )}
            >
              {account.verification.ok ? <CheckCircle2 className="mt-0.5 size-4 shrink-0" /> : <XCircle className="mt-0.5 size-4 shrink-0" />}
              <div className="min-w-0">
                <p className="font-medium">{account.verification.ok ? t("cloudAccounts.verified") : t("cloudAccounts.verifyFailed")}</p>
                {account.verification.identity && <p className="mt-0.5 break-all font-mono text-xs">{account.verification.identity}</p>}
                {account.verification.message && <p className="mt-0.5">{account.verification.message}</p>}
              </div>
            </div>
          )}
          {verify.isError && <InlineError error={verify.error} />}

          <div className="flex justify-between gap-2">
            <Button variant="ghost" onClick={() => setStep("instructions")}>
              {t("cloudAccounts.backToInstructions")}
            </Button>
            <div className="flex gap-2">
              <Button variant="secondary" onClick={onClose}>
                {account.status === "connected" ? t("cloudAccounts.done") : t("common.close")}
              </Button>
              <Button onClick={runVerify} loading={verify.isPending} icon={<RefreshCw className="size-4" />}>
                {t("cloudAccounts.verify")}
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

/** Valores iniciales de los campos. Los secretos nunca vienen de la API, así que arrancan vacíos. */
function initialValues(acc: CloudAccount | null): Record<string, string> {
  if (!acc) return {};
  const v: Record<string, string> = { external_ref: acc.external_ref ?? "" };
  for (const [k, val] of Object.entries(acc.settings ?? {})) v[k] = val;
  return v;
}

/** Un campo de las instrucciones. Los secretos van como password y no se autocompletan. */
function FieldInput({
  field,
  value,
  hasCredentials,
  onChange,
}: {
  field: InstructionField;
  value: string;
  hasCredentials: boolean;
  onChange: (v: string) => void;
}) {
  const { t } = useTranslation();
  const optionalSecret = field.secret === true && hasCredentials;
  return (
    <Field
      label={field.label}
      required={field.required && !optionalSecret}
      help={optionalSecret ? `${field.help ? `${field.help} ` : ""}${t("cloudAccounts.secretKeepHelp")}` : field.help}
    >
      {(id) => (
        <Input
          id={id}
          type={field.secret ? "password" : "text"}
          autoComplete={field.secret ? "new-password" : "off"}
          spellCheck={false}
          required={field.required && !optionalSecret}
          placeholder={field.secret && hasCredentials ? t("cloudAccounts.secretPlaceholder") : field.placeholder}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          className="font-mono text-xs"
        />
      )}
    </Field>
  );
}

/** Rotar credenciales: volver a pegar el token. Solo los campos secretos, sin mostrar el valor guardado. */
function RotateDialog({ org, account, terms, onClose }: { org: string; account: CloudAccount | null; terms: Terms; onClose: () => void }) {
  const { t } = useTranslation();
  return (
    <Dialog open={account !== null} onClose={onClose} title={t("cloudAccounts.rotateTitle", { account: terms.account })} description={t("cloudAccounts.rotateDescription")}>
      {account && <RotateBody key={account.id} org={org} account={account} onClose={onClose} />}
    </Dialog>
  );
}

function RotateBody({ org, account, onClose }: { org: string; account: CloudAccount; onClose: () => void }) {
  const { t } = useTranslation();
  const patch = usePatchCloudAccount(org);
  const verify = useVerifyCloudAccount(org);
  const fields = (account.instructions?.fields ?? []).filter((f) => f.secret);
  const [values, setValues] = useState<Record<string, string>>({});
  const [saved, setSaved] = useState(false);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const settings: Record<string, string> = {};
    for (const f of fields) {
      const v = (values[f.name] ?? "").trim();
      if (!v) return;
      settings[f.name] = v;
    }
    patch.mutate(
      { id: account.id, body: { settings } },
      {
        onSuccess: () => {
          setValues({});
          setSaved(true);
        },
      },
    );
  };

  return (
    <form onSubmit={submit} className="space-y-4">
      {fields.length === 0 && <Notice tone="blue">{t("cloudAccounts.noSecretFields")}</Notice>}
      {fields.map((f) => (
        <FieldInput key={f.name} field={{ ...f, required: true }} value={values[f.name] ?? ""} hasCredentials={false} onChange={(v) => setValues((cur) => ({ ...cur, [f.name]: v }))} />
      ))}
      {patch.isError && <ErrorOrNotImplemented error={patch.error} />}
      {saved && (
        <Notice tone="green">
          {t("cloudAccounts.rotated")}
          {verify.data && (
            <span className="mt-1 block font-medium">{verify.data.verification?.ok ? t("cloudAccounts.verified") : t("cloudAccounts.verifyFailed")}</span>
          )}
        </Notice>
      )}
      {verify.isError && <InlineError error={verify.error} />}
      <div className="flex justify-end gap-2">
        <Button variant="secondary" onClick={onClose}>
          {t("common.close")}
        </Button>
        {saved ? (
          <Button onClick={() => verify.mutate(account.id)} loading={verify.isPending} icon={<RefreshCw className="size-4" />}>
            {t("cloudAccounts.verify")}
          </Button>
        ) : (
          <Button type="submit" loading={patch.isPending} disabled={fields.length === 0}>
            {t("cloudAccounts.saveCredentials")}
          </Button>
        )}
      </div>
    </form>
  );
}

function Steps({ current }: { current: Step }) {
  const { t } = useTranslation();
  const steps: Array<{ id: Step; label: string }> = [
    { id: "choose", label: t("cloudAccounts.stepProvider") },
    { id: "instructions", label: t("cloudAccounts.stepConnect") },
    { id: "verify", label: t("cloudAccounts.stepVerify") },
  ];
  const idx = steps.findIndex((s) => s.id === current);
  return (
    <ol className="flex items-center gap-2 text-xs">
      {steps.map((s, i) => (
        <li key={s.id} className="flex items-center gap-2">
          <span
            className={cn(
              "flex size-5 items-center justify-center rounded-full text-[11px] font-semibold",
              i < idx ? "bg-green-100 dark:bg-green-900/40 text-green-700 dark:text-green-300" : i === idx ? "bg-slate-900 text-white dark:bg-slate-100 dark:text-slate-900" : "bg-slate-100 dark:bg-slate-800 text-slate-500 dark:text-slate-400",
            )}
          >
            {i + 1}
          </span>
          <span className={i === idx ? "font-medium text-slate-900 dark:text-slate-100" : "text-slate-500 dark:text-slate-400"}>{s.label}</span>
          {i < steps.length - 1 && <span className="h-px w-6 bg-slate-200 dark:bg-slate-700" aria-hidden />}
        </li>
      ))}
    </ol>
  );
}
