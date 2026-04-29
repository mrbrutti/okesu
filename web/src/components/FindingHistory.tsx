// Audit-trail component for the finding-detail drawer. One row per
// edit, newest first. Each row shows what field changed, the
// before/after, who made the change (operator email or
// orchestration run #N / step), and a relative time.
//
// The component fetches its own data — pass the finding id and it
// handles loading + error states. Refreshing is the parent's
// responsibility (e.g. after a status change in the same drawer).
import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { Bot, Clock, History, User } from 'lucide-react';
import { api, type FindingEditEntry } from '../api';

interface Props {
  findingId: number;
  /** Bumped by the parent to force a refresh after a write. */
  refreshKey?: number;
}

export function FindingHistory({ findingId, refreshKey }: Props) {
  const [rows, setRows] = useState<FindingEditEntry[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    api.findingHistory(findingId)
      .then((r) => { if (!cancelled) setRows(r); })
      .catch((e) => { if (!cancelled) setError(String(e)); });
    return () => { cancelled = true; };
  }, [findingId, refreshKey]);

  if (error) {
    return <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">{error}</div>;
  }
  if (rows === null) {
    return <div className="text-xs text-ink-mute">Loading…</div>;
  }
  if (rows.length === 0) {
    return <div className="text-xs text-ink-mute italic">No edits yet — this finding has not been touched.</div>;
  }

  return (
    <ul className="space-y-1.5">
      {rows.map((e) => <Entry key={e.id} entry={e} />)}
    </ul>
  );
}

function Entry({ entry }: { entry: FindingEditEntry }) {
  const isAuto = !!entry.orchestration_run_id;
  return (
    <li className="bg-slate-50 border border-border rounded-md px-2.5 py-2 text-xs">
      <div className="flex items-center gap-2">
        {isAuto ? <Bot size={11} className="text-brand-700" /> : <User size={11} className="text-ink-dim" />}
        <span className="font-medium text-ink">{prettyField(entry.field)}</span>
        {entry.field === 'status' && entry.new_value && (
          <span className="text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded ring-1 text-ink bg-slate-100 ring-slate-200">
            {entry.new_value}
          </span>
        )}
        <span className="ml-auto text-[10px] text-ink-mute inline-flex items-center gap-1">
          <Clock size={9} />
          {fmtAge(entry.edited_at)}
        </span>
      </div>
      <div className="mt-1 text-ink-dim">
        {renderChange(entry)}
      </div>
      {entry.reason && (
        <div className="mt-1 text-ink-mute italic line-clamp-2">"{entry.reason}"</div>
      )}
      <div className="mt-1 flex items-center gap-1.5 text-[11px] text-ink-mute">
        {isAuto ? (
          <>
            <span>by</span>
            <Link
              to={`/orchestrations?tab=runs&run=${entry.orchestration_run_id}`}
              className="font-mono text-brand-700 hover:underline"
            >
              run #{entry.orchestration_run_id}
            </Link>
            {entry.orchestration_step_id && (
              <>
                <span className="text-ink-mute">·</span>
                <code className="text-[10px] text-ink-dim">{entry.orchestration_step_id}</code>
              </>
            )}
          </>
        ) : entry.edited_by_email ? (
          <span>by {entry.edited_by_email}</span>
        ) : (
          <span>by system</span>
        )}
      </div>
    </li>
  );
}

function prettyField(field: string): string {
  switch (field) {
    case 'status':            return 'Status changed';
    case 'severity_override': return 'Severity overridden';
    case 'tag_add':           return 'Tag added';
    case 'tag_remove':        return 'Tag removed';
    case 'linked_run':        return 'Run linked';
    default:                  return field.replace(/_/g, ' ');
  }
}

function renderChange(e: FindingEditEntry): string {
  switch (e.field) {
    case 'status':
      return `${e.old_value || '(open)'} → ${e.new_value || '(unset)'}`;
    case 'severity_override':
      return `${e.old_value || '(none)'} → ${e.new_value || '(none)'}`;
    case 'tag_add':
      return e.new_value || '';
    case 'tag_remove':
      return e.old_value || '';
    case 'linked_run':
      return e.new_value || '';
    default:
      return e.new_value || '';
  }
}

function fmtAge(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime();
  if (ms < 60_000) return 'just now';
  const min = Math.floor(ms / 60_000);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const day = Math.floor(hr / 24);
  return `${day}d ago`;
}

// Re-export the section header icon for the parent's <Section> usage.
export { History };
