import { Link } from "react-router-dom";

export function NotFoundPage() {
  return (
    <div className="flex min-h-full flex-col items-center justify-center gap-2 p-12 text-center">
      <p className="text-4xl font-semibold text-slate-300">404</p>
      <p className="text-sm text-slate-600">Esa página no existe.</p>
      <Link to="/" className="text-sm font-medium text-slate-900 underline">
        Ir al inicio
      </Link>
    </div>
  );
}
