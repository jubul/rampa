import type { Event } from "../api/types";
import { formatRelative, formatTime } from "../lib/format";
import { actorTypeLabels, toneForStatus } from "../lib/status";
import type { Tone } from "../lib/status";
import { EmptyState } from "./ui/States";

function toneForKind(kind: string): Tone {
  const verb = kind.split(".")[1] ?? "";
  if (verb === "failed" || verb === "offline") return "red";
  if (verb === "healthy" || verb === "verified" || verb === "online" || verb === "approved") return "green";
  if (verb === "cancelled" || verb === "deleted") return "gray";
  return toneForStatus(verb) === "gray" ? "blue" : toneForStatus(verb);
}

const dot: Record<Tone, string> = {
  green: "bg-green-500",
  red: "bg-red-500",
  amber: "bg-amber-500",
  violet: "bg-violet-500",
  gray: "bg-slate-400",
  blue: "bg-blue-500",
};

export function EventList({ events, emptyText = "Todavía no hay eventos" }: { events: Event[]; emptyText?: string }) {
  if (events.length === 0) return <EmptyState title={emptyText} />;
  return (
    <ol className="divide-y divide-slate-100">
      {events.map((e) => (
        <li key={e.id} className="flex gap-3 px-5 py-3">
          <span className={`mt-1.5 size-2 shrink-0 rounded-full ${dot[toneForKind(e.kind)]}`} aria-hidden />
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-baseline gap-x-2">
              <span className="font-mono text-xs text-slate-500">{e.kind}</span>
              <span className="text-xs text-slate-400" title={e.created_at}>
                {formatTime(e.created_at)} · {formatRelative(e.created_at)}
              </span>
            </div>
            <p className="mt-0.5 break-words text-sm text-slate-800">{e.message || e.kind}</p>
            <p className="mt-0.5 text-xs text-slate-500">
              {actorTypeLabels[e.actor?.type] ?? e.actor?.type}
              {e.actor?.name ? ` · ${e.actor.name}` : ""}
            </p>
          </div>
        </li>
      ))}
    </ol>
  );
}
