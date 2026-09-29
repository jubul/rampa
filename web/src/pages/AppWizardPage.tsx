import { useMemo } from "react";
import { Link, useParams } from "react-router-dom";
import { ArrowLeft } from "lucide-react";
import { useAppSpec } from "../api/hooks";
import { PageHeader } from "../components/ui/PageHeader";
import { ErrorState, LoadingState } from "../components/ui/States";
import { AppWizard } from "../components/appwizard/AppWizard";
import { defaultSpec, hydrateSpec } from "../lib/spec";

/** /o/:org/apps/new: wizard vacío. */
export function NewAppPage() {
  const { org = "" } = useParams();
  const initial = useMemo(defaultSpec, []);
  return (
    <>
      <Back to={`/o/${org}/apps`} label="Aplicaciones" />
      <PageHeader title="Nueva aplicación" description="Completá los pasos. No hace falta escribir YAML: lo generamos por vos." />
      <AppWizard org={org} mode="create" initialSpec={initial} />
    </>
  );
}

/** /o/:org/apps/:app/edit: wizard precargado con GET /spec. */
export function EditAppPage() {
  const { org = "", app = "" } = useParams();
  const specQ = useAppSpec(org, app);
  return (
    <>
      <Back to={`/o/${org}/apps/${app}`} label={app} />
      <PageHeader title={`Editar ${app}`} description="Los cambios se guardan como un nuevo manifest de la aplicación." />
      {specQ.isPending ? (
        <LoadingState label="Cargando la configuración" />
      ) : specQ.isError ? (
        <ErrorState title="No se pudo cargar la configuración" error={specQ.error} onRetry={() => void specQ.refetch()} />
      ) : (
        <AppWizard org={org} mode="edit" initialSpec={hydrateSpec(specQ.data.spec)} />
      )}
    </>
  );
}

function Back({ to, label }: { to: string; label: string }) {
  return (
    <Link to={to} className="mb-3 inline-flex items-center gap-1 text-xs font-medium text-slate-500 hover:text-slate-900">
      <ArrowLeft className="size-3.5" /> {label}
    </Link>
  );
}
