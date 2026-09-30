import type { ReactNode } from "react";
import { cn } from "../../lib/cn";
import type { Tone } from "../../lib/status";

const tones: Record<Tone, string> = {
  green: "bg-green-50 dark:bg-green-950/40 text-green-700 dark:text-green-300 ring-green-600/20 dark:ring-green-400/30",
  red: "bg-red-50 dark:bg-red-950/40 text-red-700 dark:text-red-300 ring-red-600/20 dark:ring-red-400/30",
  amber: "bg-amber-50 dark:bg-amber-950/40 text-amber-800 dark:text-amber-300 ring-amber-600/20 dark:ring-amber-400/30",
  violet: "bg-violet-50 dark:bg-violet-950/40 text-violet-700 dark:text-violet-300 ring-violet-600/20 dark:ring-violet-400/30",
  gray: "bg-slate-100 dark:bg-slate-800 text-slate-600 dark:text-slate-300 ring-slate-500/20 dark:ring-slate-400/30",
  blue: "bg-blue-50 dark:bg-blue-950/40 text-blue-700 dark:text-blue-300 ring-blue-600/20 dark:ring-blue-400/30",
};

export function Badge({
  tone = "gray",
  children,
  className,
  pulse = false,
}: {
  tone?: Tone;
  children: ReactNode;
  className?: string;
  pulse?: boolean;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset whitespace-nowrap",
        tones[tone],
        className,
      )}
    >
      {pulse && <span className="size-1.5 animate-pulse rounded-full bg-current" aria-hidden />}
      {children}
    </span>
  );
}
