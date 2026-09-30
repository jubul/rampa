import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { Link, useParams } from "react-router-dom";
import { ArrowLeft } from "lucide-react";
import { useAppSpec } from "../api/hooks";
import { PageHeader } from "../components/ui/PageHeader";
import { ErrorState, LoadingState } from "../components/ui/States";
import { AppWizard } from "../components/appwizard/AppWizard";
import { defaultSpec, hydrateSpec } from "../lib/spec";

/** /o/:org/apps/new: wizard vacío. */
export function NewAppPage() {
  const { t } = useTranslation();
  const { org = "" } = useParams();
  const initial = useMemo(defaultSpec, []);
  return (
    <>
      <Back to={`/o/${org}/apps`} label={t("nav.apps")} />
      <PageHeader title={t("wizard.newTitle")} description={t("wizard.newDescription")} />
      <AppWizard org={org} mode="create" initialSpec={initial} />
    </>
  );
}

/** /o/:org/apps/:app/edit: wizard precargado con GET /spec. */
export function EditAppPage() {
  const { t } = useTranslation();
  const { org = "", app = "" } = useParams();
  const specQ = useAppSpec(org, app);
  return (
    <>
      <Back to={`/o/${org}/apps/${app}`} label={app} />
      <PageHeader title={t("wizard.editTitle", { app })} description={t("wizard.editDescription")} />
      {specQ.isPending ? (
        <LoadingState label={t("wizard.loadingSpec")} />
      ) : specQ.isError ? (
        <ErrorState title={t("wizard.loadSpecError")} error={specQ.error} onRetry={() => void specQ.refetch()} />
      ) : (
        <AppWizard org={org} mode="edit" initialSpec={hydrateSpec(specQ.data.spec)} />
      )}
    </>
  );
}

function Back({ to, label }: { to: string; label: string }) {
  return (
    <Link to={to} className="mb-3 inline-flex items-center gap-1 text-xs font-medium text-slate-500 dark:text-slate-400 hover:text-slate-900 dark:hover:text-slate-100">
      <ArrowLeft className="size-3.5" /> {label}
    </Link>
  );
}
