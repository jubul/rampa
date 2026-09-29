import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useAuth } from "../auth/AuthContext";
import { useCreateOrg } from "../api/hooks";
import { Button } from "../components/ui/Button";
import { Input } from "../components/ui/Input";
import { Field } from "../components/ui/Field";
import { InlineError } from "../components/ui/States";
import { SLUG_RE, slugify } from "../lib/slug";

export function NewOrgPage() {
  const me = useAuth();
  const navigate = useNavigate();
  const createOrg = useCreateOrg();
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [slugTouched, setSlugTouched] = useState(false);

  const slugError = slug !== "" && !SLUG_RE.test(slug) ? "Minúsculas, números y guiones, sin guion al inicio ni al final. Máximo 40 caracteres." : undefined;

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (slugError || !slug || !name.trim()) return;
    createOrg.mutate({ slug, name: name.trim() }, { onSuccess: (org) => navigate(`/o/${org.slug}`, { replace: true }) });
  };

  return (
    <div className="flex min-h-full items-center justify-center bg-slate-50 px-4 py-12">
      <div className="w-full max-w-md">
        <h1 className="text-xl font-semibold tracking-tight">Nueva organización</h1>
        <p className="mt-1 text-sm text-slate-500">
          {me.memberships.length === 0
            ? "Todavía no formás parte de ninguna organización. Creá una para empezar."
            : "Vas a quedar como owner de la nueva organización."}
        </p>
        <form onSubmit={submit} className="mt-6 space-y-4 rounded-lg border border-slate-200 bg-white p-6 shadow-sm">
          <Field label="Nombre" required>
            {(id) => (
              <Input
                id={id}
                required
                placeholder="Acme"
                value={name}
                onChange={(e) => {
                  setName(e.target.value);
                  if (!slugTouched) setSlug(slugify(e.target.value));
                }}
              />
            )}
          </Field>
          <Field label="Identificador (slug)" required help="Va en las URLs y en el CLI. No se puede cambiar después." error={slugError}>
            {(id) => (
              <Input
                id={id}
                required
                placeholder="acme"
                value={slug}
                aria-invalid={Boolean(slugError)}
                onChange={(e) => {
                  setSlugTouched(true);
                  setSlug(e.target.value.toLowerCase());
                }}
              />
            )}
          </Field>
          {createOrg.isError && <InlineError error={createOrg.error} />}
          <div className="flex items-center justify-between gap-2 pt-1">
            {me.memberships.length > 0 ? (
              <Link to="/" className="text-sm text-slate-600 hover:underline">
                Volver
              </Link>
            ) : (
              <span />
            )}
            <Button type="submit" loading={createOrg.isPending}>
              Crear organización
            </Button>
          </div>
        </form>
      </div>
    </div>
  );
}
