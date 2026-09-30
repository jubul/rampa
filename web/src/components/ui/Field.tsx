import { useId, type ReactNode } from "react";

interface FieldProps {
  label: string;
  help?: string;
  error?: string;
  required?: boolean;
  children: (id: string) => ReactNode;
}

/** Etiqueta + control + ayuda + error. El hijo recibe el id para el aria. */
export function Field({ label, help, error, required, children }: FieldProps) {
  const id = useId();
  return (
    <div className="space-y-1.5">
      <label htmlFor={id} className="block text-sm font-medium text-slate-700 dark:text-slate-200">
        {label}
        {required && <span className="ml-0.5 text-red-600 dark:text-red-400">*</span>}
      </label>
      {children(id)}
      {error ? (
        <p className="text-xs text-red-600 dark:text-red-400">{error}</p>
      ) : help ? (
        <p className="text-xs text-slate-500 dark:text-slate-400">{help}</p>
      ) : null}
    </div>
  );
}
