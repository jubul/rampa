import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { Navigate, useLocation, useNavigate } from "react-router-dom";
import { KeyRound, Mail } from "lucide-react";
import { auth } from "../api/client";
import { useDevLogin, useMe } from "../api/hooks";
import { Button } from "../components/ui/Button";
import { Input } from "../components/ui/Input";
import { Field } from "../components/ui/Field";
import { InlineError } from "../components/ui/States";
import { FullPageSpinner } from "../components/ui/Spinner";
import { Logo } from "../components/Logo";
import { PreferencesControls } from "../components/PreferencesControls";

export function LoginPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const location = useLocation();
  const from = (location.state as { from?: string } | null)?.from;
  const me = useMe();
  const devLogin = useDevLogin();
  const [email, setEmail] = useState("");

  if (me.isPending) return <FullPageSpinner label={t("login.checking")} />;
  if (me.isSuccess) return <Navigate to={from && from !== "/login" ? from : "/"} replace />;

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const value = email.trim();
    if (!value) return;
    devLogin.mutate(value, { onSuccess: () => navigate(from && from !== "/login" ? from : "/", { replace: true }) });
  };

  return (
    <div className="relative flex min-h-full items-center justify-center bg-slate-50 dark:bg-slate-950 px-4 py-12">
      <div className="absolute right-4 top-4">
        <PreferencesControls persist={false} />
      </div>
      <div className="w-full max-w-sm">
        <div className="mb-8 text-center">
          <Logo size="lg" className="mb-4 justify-center" />
          <h1 className="text-xl font-semibold tracking-tight">{t("login.title")}</h1>
          <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">{t("login.subtitle")}</p>
        </div>

        <div className="rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 p-6 shadow-sm">
          <a
            href={auth.oidcStartUrl}
            className="inline-flex h-9 w-full items-center justify-center gap-2 rounded-md bg-slate-900 px-3.5 text-sm font-medium text-white hover:bg-slate-800 dark:bg-slate-100 dark:text-slate-900 dark:hover:bg-white"
          >
            <KeyRound className="size-4" aria-hidden />
            {t("login.oidc")}
          </a>

          <div className="my-5 flex items-center gap-3 text-xs text-slate-500 dark:text-slate-400">
            <span className="h-px flex-1 bg-slate-200 dark:bg-slate-700" />
            {t("login.devAccess")}
            <span className="h-px flex-1 bg-slate-200 dark:bg-slate-700" />
          </div>

          <form onSubmit={submit} className="space-y-3">
            <Field label={t("login.email")} help={t("login.emailHelp")}>
              {(id) => (
                <Input
                  id={id}
                  type="email"
                  required
                  autoComplete="email"
                  placeholder={t("login.emailPlaceholder")}
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                />
              )}
            </Field>
            {devLogin.isError && <InlineError error={devLogin.error} />}
            <Button type="submit" variant="secondary" className="w-full" loading={devLogin.isPending} icon={<Mail className="size-4" />}>
              {t("login.emailSubmit")}
            </Button>
          </form>
        </div>
      </div>
    </div>
  );
}
