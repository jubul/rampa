import { useMemo, useState, type FormEvent } from "react";
import { useParams } from "react-router-dom";
import { CheckCircle2, Cloud, ExternalLink, Plus, RefreshCw, Trash2, XCircle } from "lucide-react";
import {
  useCloudAccounts,
  useCreateCloudAccount,
  usePatchCloudAccount,
  useProviders,
  useRemoveCloudAccount,
  useVerifyCloudAccount,
} from "../api/hooks";
import { useOrgRole } from "../auth/AuthContext";
import type { CloudAccount, InstructionField, Provider, ProviderKind } from "../api/types";
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
import { errorMessage } from "../api/client";
import { cn } from "../lib/cn";

const providerLabels: Record<string, string> = { aws: "AWS", gcp: "Google Cloud", azure: "Azure" };
const defaultRegion: Record<string, string> = { aws: "us-east-1", gcp: "us-central1", azure: "eastus" };

export function CloudAccountsPage() {
  const { org = "" } = useParams();
  const { isAdmin } = useOrgRole(org);
  const accountsQ = useCloudAccounts(org);
  const providersQ = useProviders(org);
  const verify = useVerifyCloudAccount(org);
  const remove = useRemoveCloudAccount(org);

  const [wizardOpen, setWizardOpen] = useState(false);
  const [resumeAccount, setResumeAccount] = useState<CloudAccount | null>(null);
  const [toDelete, setToDelete] = useState<CloudAccount | null>(null);
  const [verifyingId, setVerifyingId] = useState<string | null>(null);

  const providerName = (kind: string) => providersQ.data?.find((p) => p.kind === kind)?.display_name ?? providerLabels[kind] ?? kind;

  return (
    <>
      <PageHeader
        title="Cuentas de nube"
        description="Rampa nunca guarda claves: se conecta a tu cuenta asumiendo un rol o una identidad federada."
        actions={
          <Button onClick={() => setWizardOpen(true)} disabled={!isAdmin} title={isAdmin ? undefined : "Necesitás rol admin"} icon={<Plus className="size-4" />}>
            Conectar cuenta
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
            title="No hay cuentas conectadas"
            description="Conectá tu cuenta de AWS para poder registrar clusters y desplegar."
            action={
              <Button onClick={() => setWizardOpen(true)} disabled={!isAdmin} icon={<Plus className="size-4" />}>
                Conectar cuenta
              </Button>
            }
          />
        ) : (
          <Table>
            <THead>
              <tr>
                <TH>Nombre</TH>
                <TH>Proveedor</TH>
                <TH>Región</TH>
                <TH>Estado</TH>
                <TH>Referencia</TH>
                <TH>Creada</TH>
                <TH className="text-right">Acciones</TH>
              </tr>
            </THead>
            <TBody>
              {accountsQ.data.map((a) => (
                <TR key={a.id}>
                  <TD className="font-medium text-slate-900">{a.name}</TD>
                  <TD>{providerName(a.provider)}</TD>
                  <TD className="font-mono text-xs">{a.region}</TD>
                  <TD>
                    <div className="flex flex-col gap-1">
                      <StatusBadge status={a.status} />
                      {a.verification && !a.verification.ok && a.status === "failed" && (
                        <span className="max-w-xs truncate text-xs text-red-600" title={a.verification.message}>
                          {a.verification.message}
                        </span>
                      )}
                      {a.verification?.ok && a.verification.identity && (
                        <span className="max-w-xs truncate font-mono text-[11px] text-slate-500" title={a.verification.identity}>
                          {a.verification.identity}
                        </span>
                      )}
                    </div>
                  </TD>
                  <TD className="max-w-xs truncate font-mono text-xs" title={a.external_ref}>
                    {a.external_ref || <span className="text-slate-400">sin completar</span>}
                  </TD>
                  <TD className="text-slate-500">{formatDateTime(a.created_at)}</TD>
                  <TD>
                    <div className="flex justify-end gap-1.5">
                      {a.status !== "connected" && (
                        <Button size="sm" variant="secondary" disabled={!isAdmin} onClick={() => setResumeAccount(a)}>
                          {a.external_ref ? "Revisar" : "Continuar"}
                        </Button>
                      )}
                      <Button
                        size="sm"
                        variant="secondary"
                        disabled={!isAdmin || !a.external_ref}
                        title={!a.external_ref ? "Primero completá la referencia" : "Comprobar acceso"}
                        loading={verify.isPending && verifyingId === a.id}
                        icon={<RefreshCw className="size-3.5" />}
                        onClick={() => {
                          setVerifyingId(a.id);
                          verify.mutate(a.id, { onSettled: () => setVerifyingId(null) });
                        }}
                      >
                        Verificar
                      </Button>
                      <Button size="sm" variant="ghost" disabled={!isAdmin} aria-label="Eliminar" title="Eliminar" icon={<Trash2 className="size-3.5 text-red-600" />} onClick={() => setToDelete(a)} />
                    </div>
                  </TD>
                </TR>
              ))}
            </TBody>
          </Table>
        )}
        {verify.isError && (
          <div className="border-t border-slate-200 px-5 py-3">
            <InlineError error={verify.error} />
          </div>
        )}
      </Card>

      <ConnectWizard
        org={org}
        open={wizardOpen || resumeAccount !== null}
        providers={providersQ.data ?? []}
        providersError={providersQ.isError ? providersQ.error : null}
        initialAccount={resumeAccount}
        onClose={() => {
          setWizardOpen(false);
          setResumeAccount(null);
        }}
      />

      <ConfirmDialog
        open={toDelete !== null}
        onClose={() => {
          setToDelete(null);
          remove.reset();
        }}
        title="Eliminar cuenta de nube"
        description={toDelete ? `Se va a eliminar "${toDelete.name}". Si tiene clusters asociados la API lo va a rechazar.` : undefined}
        confirmLabel="Eliminar"
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
  const initialStep: Step = initialAccount ? (initialAccount.external_ref ? "verify" : "instructions") : "choose";
  return (
    <Dialog open={open} onClose={onClose} title={initialAccount ? `Conectar ${initialAccount.name}` : "Conectar una cuenta de nube"} size="lg">
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
  const create = useCreateCloudAccount(org);
  const patch = usePatchCloudAccount(org);
  const verify = useVerifyCloudAccount(org);

  const [step, setStep] = useState<Step>(initialStep);
  const [account, setAccount] = useState<CloudAccount | null>(initialAccount);
  const [provider, setProvider] = useState<ProviderKind | "">(providers.find((p) => p.available)?.kind ?? "");
  const [name, setName] = useState("");
  const [region, setRegion] = useState(defaultRegion[provider] ?? "");
  const [values, setValues] = useState<Record<string, string>>(() => initialValues(initialAccount));

  const fields: InstructionField[] = useMemo(() => {
    const f = account?.instructions?.fields;
    if (f && f.length > 0) return f;
    // Si la API no devolvió instrucciones al releer la cuenta, pedimos al menos la referencia externa.
    return [
      {
        name: "external_ref",
        label: "Referencia externa",
        placeholder: "arn:aws:iam::123456789012:role/RampaConnect",
        help: "El identificador del rol o identidad que Rampa va a asumir.",
        required: true,
      },
    ];
  }, [account]);

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
      if (f.required && !v) return;
      if (f.name === "external_ref") externalRef = v;
      else if (v) settings[f.name] = v;
    }
    patch.mutate(
      { id: account.id, body: { ...(externalRef !== undefined ? { external_ref: externalRef } : {}), settings } },
      {
        onSuccess: (acc) => {
          setAccount(acc);
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
            <p className="mb-2 text-sm font-medium text-slate-700">Proveedor</p>
            <div className="grid gap-2 sm:grid-cols-3">
              {providers.map((p) => (
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
                    provider === p.kind ? "border-slate-900 ring-1 ring-slate-900" : "border-slate-200 hover:border-slate-300",
                    !p.available && "cursor-not-allowed opacity-60 hover:border-slate-200",
                  )}
                >
                  <span className="font-medium text-slate-900">{p.display_name}</span>
                  {p.available ? <span className="text-xs text-slate-500">Disponible</span> : <Badge tone="gray">Próximamente</Badge>}
                </button>
              ))}
              {providers.length === 0 && !providersError && <p className="text-sm text-slate-500">Cargando proveedores...</p>}
            </div>
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Nombre" required help="Cómo la vas a reconocer, por ejemplo acme-prod.">
              {(id) => <Input id={id} required value={name} onChange={(e) => setName(e.target.value)} placeholder="acme-prod" />}
            </Field>
            <Field label="Región" required help="Región por defecto para registry y services.">
              {(id) => <Input id={id} required value={region} onChange={(e) => setRegion(e.target.value)} placeholder="us-east-1" />}
            </Field>
          </div>
          {create.isError && <InlineError error={create.error} />}
          <div className="flex justify-end gap-2">
            <Button variant="secondary" onClick={onClose}>
              Cancelar
            </Button>
            <Button type="submit" disabled={!provider} loading={create.isPending}>
              Crear y ver instrucciones
            </Button>
          </div>
        </form>
      )}

      {step === "instructions" && account && (
        <form onSubmit={submitFields} className="space-y-4">
          {account.instructions ? (
            <div className="space-y-3 rounded-md border border-slate-200 bg-slate-50 p-4">
              <div>
                <p className="text-sm font-semibold text-slate-900">{account.instructions.title}</p>
                <p className="mt-1 whitespace-pre-line text-sm text-slate-600">{account.instructions.description}</p>
              </div>
              {account.instructions.url && (
                <a
                  href={account.instructions.url}
                  target="_blank"
                  rel="noreferrer noopener"
                  className="inline-flex h-9 items-center gap-2 rounded-md bg-slate-900 px-3.5 text-sm font-medium text-white hover:bg-slate-800"
                >
                  <ExternalLink className="size-4" aria-hidden />
                  Abrir en la consola de {providerLabels[account.provider] ?? account.provider}
                </a>
              )}
              {account.instructions.command && <CopyField label="O por línea de comandos" value={account.instructions.command} multiline />}
            </div>
          ) : (
            <Notice tone="blue">La API no devolvió instrucciones para esta cuenta. Completá la referencia externa a mano.</Notice>
          )}

          <div className="space-y-3">
            <p className="text-sm font-medium text-slate-700">Cuando termines, pegá lo que te pide:</p>
            {fields.map((f) => (
              <Field key={f.name} label={f.label} required={f.required} help={f.help}>
                {(id) => (
                  <Input
                    id={id}
                    required={f.required}
                    placeholder={f.placeholder}
                    value={values[f.name] ?? ""}
                    onChange={(e) => setValues((v) => ({ ...v, [f.name]: e.target.value }))}
                    className="font-mono text-xs"
                  />
                )}
              </Field>
            ))}
          </div>
          {patch.isError && <InlineError error={patch.error} />}
          <div className="flex justify-between gap-2">
            <Button variant="ghost" onClick={onClose}>
              Seguir más tarde
            </Button>
            <Button type="submit" loading={patch.isPending}>
              Guardar y continuar
            </Button>
          </div>
        </form>
      )}

      {step === "verify" && account && (
        <div className="space-y-4">
          <div className="rounded-md border border-slate-200 p-4 text-sm">
            <dl className="grid grid-cols-3 gap-y-2">
              <dt className="text-slate-500">Cuenta</dt>
              <dd className="col-span-2 font-medium text-slate-900">{account.name}</dd>
              <dt className="text-slate-500">Referencia</dt>
              <dd className="col-span-2 break-all font-mono text-xs">{account.external_ref || "-"}</dd>
              <dt className="text-slate-500">Estado</dt>
              <dd className="col-span-2">
                <StatusBadge status={account.status} />
              </dd>
            </dl>
          </div>

          {account.verification && (
            <div
              className={cn(
                "flex items-start gap-3 rounded-md border p-4 text-sm",
                account.verification.ok ? "border-green-200 bg-green-50 text-green-900" : "border-red-200 bg-red-50 text-red-900",
              )}
            >
              {account.verification.ok ? <CheckCircle2 className="mt-0.5 size-4 shrink-0" /> : <XCircle className="mt-0.5 size-4 shrink-0" />}
              <div className="min-w-0">
                <p className="font-medium">{account.verification.ok ? "Conexión verificada" : "La verificación falló"}</p>
                {account.verification.identity && <p className="mt-0.5 break-all font-mono text-xs">{account.verification.identity}</p>}
                {account.verification.message && <p className="mt-0.5">{account.verification.message}</p>}
              </div>
            </div>
          )}
          {verify.isError && <InlineError error={verify.error} />}

          <div className="flex justify-between gap-2">
            <Button variant="ghost" onClick={() => setStep("instructions")}>
              Volver a las instrucciones
            </Button>
            <div className="flex gap-2">
              <Button variant="secondary" onClick={onClose}>
                {account.status === "connected" ? "Listo" : "Cerrar"}
              </Button>
              <Button onClick={runVerify} loading={verify.isPending} icon={<RefreshCw className="size-4" />}>
                Verificar
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function initialValues(acc: CloudAccount | null): Record<string, string> {
  if (!acc) return {};
  const v: Record<string, string> = { external_ref: acc.external_ref ?? "" };
  for (const [k, val] of Object.entries(acc.settings ?? {})) v[k] = val;
  return v;
}

function Steps({ current }: { current: Step }) {
  const steps: Array<{ id: Step; label: string }> = [
    { id: "choose", label: "Proveedor" },
    { id: "instructions", label: "Conectar" },
    { id: "verify", label: "Verificar" },
  ];
  const idx = steps.findIndex((s) => s.id === current);
  return (
    <ol className="flex items-center gap-2 text-xs">
      {steps.map((s, i) => (
        <li key={s.id} className="flex items-center gap-2">
          <span
            className={cn(
              "flex size-5 items-center justify-center rounded-full text-[11px] font-semibold",
              i < idx ? "bg-green-100 text-green-700" : i === idx ? "bg-slate-900 text-white" : "bg-slate-100 text-slate-500",
            )}
          >
            {i + 1}
          </span>
          <span className={i === idx ? "font-medium text-slate-900" : "text-slate-500"}>{s.label}</span>
          {i < steps.length - 1 && <span className="h-px w-6 bg-slate-200" aria-hidden />}
        </li>
      ))}
    </ol>
  );
}
