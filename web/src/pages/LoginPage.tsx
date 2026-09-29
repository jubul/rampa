import { useState, type FormEvent } from "react";
import { Navigate, useLocation, useNavigate } from "react-router-dom";
import { KeyRound, Mail } from "lucide-react";
import { auth } from "../api/client";
import { useDevLogin, useMe } from "../api/hooks";
import { Button } from "../components/ui/Button";
import { Input } from "../components/ui/Input";
import { Field } from "../components/ui/Field";
import { InlineError } from "../components/ui/States";
import { FullPageSpinner } from "../components/ui/Spinner";

export function LoginPage() {
  const navigate = useNavigate();
  const location = useLocation();
  const from = (location.state as { from?: string } | null)?.from;
  const me = useMe();
  const devLogin = useDevLogin();
  const [email, setEmail] = useState("");

  if (me.isPending) return <FullPageSpinner label="Comprobando sesión" />;
  if (me.isSuccess) return <Navigate to={from && from !== "/login" ? from : "/"} replace />;

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const value = email.trim();
    if (!value) return;
    devLogin.mutate(value, { onSuccess: () => navigate(from && from !== "/login" ? from : "/", { replace: true }) });
  };

  return (
    <div className="flex min-h-full items-center justify-center bg-slate-50 px-4 py-12">
      <div className="w-full max-w-sm">
        <div className="mb-8 text-center">
          <div className="mx-auto mb-3 flex size-10 items-center justify-center rounded-lg bg-slate-900 text-accent-500">
            <svg viewBox="0 0 32 32" className="size-6" aria-hidden>
              <path d="M7 23 L25 9 M7 23 H25" stroke="currentColor" strokeWidth="4" strokeLinecap="round" fill="none" />
            </svg>
          </div>
          <h1 className="text-xl font-semibold tracking-tight">Entrar a Rampa</h1>
          <p className="mt-1 text-sm text-slate-500">Plano de control para desplegar en tu nube.</p>
        </div>

        <div className="rounded-lg border border-slate-200 bg-white p-6 shadow-sm">
          <a
            href={auth.oidcStartUrl}
            className="inline-flex h-9 w-full items-center justify-center gap-2 rounded-md bg-slate-900 px-3.5 text-sm font-medium text-white hover:bg-slate-800"
          >
            <KeyRound className="size-4" aria-hidden />
            Entrar con el proveedor de identidad
          </a>

          <div className="my-5 flex items-center gap-3 text-xs text-slate-400">
            <span className="h-px flex-1 bg-slate-200" />
            acceso de desarrollo
            <span className="h-px flex-1 bg-slate-200" />
          </div>

          <form onSubmit={submit} className="space-y-3">
            <Field label="Email" help="Solo funciona si la API corre con RAMPA_DEV_LOGIN=true.">
              {(id) => (
                <Input
                  id={id}
                  type="email"
                  required
                  autoComplete="email"
                  placeholder="vos@empresa.com"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                />
              )}
            </Field>
            {devLogin.isError && <InlineError error={devLogin.error} />}
            <Button type="submit" variant="secondary" className="w-full" loading={devLogin.isPending} icon={<Mail className="size-4" />}>
              Entrar por email
            </Button>
          </form>
        </div>
      </div>
    </div>
  );
}
