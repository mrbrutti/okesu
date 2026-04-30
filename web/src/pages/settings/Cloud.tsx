// Cloud credentials editor (Phase 21.2).
//
// Multiple clouds, multiple credentials per cloud (e.g. "prod-tenancy"
// + "staging-tenancy"). Plaintext is write-only at this layer — the
// list never roundtrips secrets through the browser, so editing is
// "delete + recreate" rather than "edit-in-place". That's a
// deliberate simplification: a credential is opaque to us once
// stored, and a half-edited credential is worse than a clean replace.
//
// Validation lives entirely on the server (db.AllowedCloudKinds +
// requireKeys per cloud). The UI just collects the right fields per
// cloud and surfaces errors as-is.

import { useEffect, useState } from 'react';
import { Cloud, Loader2, Pencil, Plus, RefreshCw, Trash2, X } from 'lucide-react';
import { api, type CloudCredential, type CloudCredentialCreateRequest, type CloudCredentialUpdateRequest, type CloudKind } from '../../api';
import { cn } from '../../lib/cn';

const CLOUDS: Array<{ kind: CloudKind; label: string; provisioned: boolean }> = [
  { kind: 'oci',          label: 'Oracle Cloud (OCI)',  provisioned: true  },
  { kind: 'aws',          label: 'AWS',                  provisioned: true  },
  { kind: 'gcp',          label: 'Google Cloud',         provisioned: false },
  { kind: 'azure',        label: 'Microsoft Azure',      provisioned: false },
  { kind: 'digitalocean', label: 'DigitalOcean',         provisioned: false },
];

export default function CloudSection() {
  const [items, setItems] = useState<CloudCredential[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [showAdd, setShowAdd] = useState<CloudKind | null>(null);
  const [editing, setEditing] = useState<CloudCredential | null>(null);
  const [busyTest, setBusyTest] = useState<number | null>(null);

  async function load() {
    try {
      const rows = await api.cloudCredentialsList();
      setItems(rows);
    } catch (e) {
      setError(String(e));
    }
  }
  useEffect(() => { load(); }, []);

  async function handleTest(id: number) {
    setBusyTest(id);
    try {
      await api.cloudCredentialTest(id);
    } catch (e) {
      // The error landed on last_test_error already; just refresh.
      void e;
    } finally {
      setBusyTest(null);
      load();
    }
  }

  async function handleDelete(id: number) {
    if (!confirm('Delete this credential? Any in-flight provisioning that depends on it will fail.')) return;
    try {
      await api.cloudCredentialDelete(id);
      load();
    } catch (e) {
      setError(String(e));
    }
  }

  return (
    <div className="space-y-6">
      <header>
        <h2 className="text-lg font-semibold flex items-center gap-2">
          <Cloud size={18} className="text-brand-500" /> Cloud credentials
        </h2>
        <p className="text-xs text-ink-dim mt-0.5">
          Stored encrypted-at-rest with AES-GCM under a key derived from the CP's session HMAC.
          Used (Phase 21.3+) to provision child CPs via cloud APIs without operators having to
          paste credentials into a CLI.
        </p>
      </header>

      {error && (
        <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">{error}</div>
      )}

      <div className="space-y-3">
        {CLOUDS.map((c) => {
          const rows = (items ?? []).filter((r) => r.cloud === c.kind);
          return (
            <section key={c.kind} className="border border-border rounded-xl overflow-hidden bg-panel">
              <header className="px-4 py-2.5 border-b border-border flex items-center justify-between">
                <div className="flex items-center gap-2">
                  <span className="text-sm font-medium">{c.label}</span>
                  {!c.provisioned && (
                    <span className="text-[10px] uppercase tracking-wider text-ink-mute bg-slate-100 px-1.5 py-0.5 rounded">
                      validation only — provisioner lands in Phase 21.3
                    </span>
                  )}
                </div>
                <button
                  onClick={() => setShowAdd(c.kind)}
                  className="text-xs px-2 py-1 bg-brand-50 hover:bg-brand-100 text-brand-700 rounded inline-flex items-center gap-1"
                >
                  <Plus size={12} /> Add
                </button>
              </header>
              {rows.length === 0 ? (
                <div className="px-4 py-3 text-xs text-ink-mute">No {c.label} credentials configured.</div>
              ) : (
                <ul className="divide-y divide-border">
                  {rows.map((r) => (
                    <li key={r.id} className="px-4 py-2.5 flex items-center justify-between gap-3 text-sm">
                      <div className="min-w-0 flex-1">
                        <div className="font-medium truncate">{r.name}</div>
                        <div className="text-[11px] text-ink-mute">
                          {r.region && <>region <code>{r.region}</code> · </>}
                          added {new Date(r.created_at).toLocaleString()}
                          {r.created_by_email && <> · by {r.created_by_email}</>}
                          <BudgetLine credential={r} />
                        </div>
                        {r.last_test_at && (
                          <div className={cn(
                            'text-[11px] mt-0.5',
                            r.last_test_ok ? 'text-emerald-700' : 'text-red-700',
                          )}>
                            {r.last_test_ok ? '✓' : '✗'} tested {new Date(r.last_test_at).toLocaleString()}
                            {!r.last_test_ok && r.last_test_error && <> · {r.last_test_error}</>}
                          </div>
                        )}
                      </div>
                      <div className="flex items-center gap-1.5">
                        <button
                          onClick={() => handleTest(r.id)}
                          disabled={busyTest === r.id}
                          title="Validate this credential"
                          className="text-xs px-2 py-1 border border-border hover:bg-slate-50 rounded inline-flex items-center gap-1"
                        >
                          {busyTest === r.id ? <Loader2 size={12} className="animate-spin" /> : <RefreshCw size={12} />} Test
                        </button>
                        <button
                          onClick={() => setEditing(r)}
                          title="Edit this credential"
                          className="text-xs px-2 py-1 border border-border hover:bg-slate-50 rounded inline-flex items-center gap-1"
                        >
                          <Pencil size={12} /> Edit
                        </button>
                        <button
                          onClick={() => handleDelete(r.id)}
                          title="Delete"
                          className="text-xs px-2 py-1 border border-border hover:bg-red-50 hover:text-red-700 rounded"
                        >
                          <Trash2 size={12} />
                        </button>
                      </div>
                    </li>
                  ))}
                </ul>
              )}
            </section>
          );
        })}
      </div>

      {showAdd && (
        <AddCredentialDialog
          cloud={showAdd}
          onClose={() => setShowAdd(null)}
          onAdded={() => { setShowAdd(null); load(); }}
        />
      )}
      {editing && (
        <AddCredentialDialog
          cloud={editing.cloud}
          editing={editing}
          onClose={() => setEditing(null)}
          onAdded={() => { setEditing(null); load(); }}
        />
      )}
    </div>
  );
}

// BudgetLine is a read-only display of the per-credential monthly
// USD cap, shown alongside region/created-at on each credential row.
// Editing happens in the AddCredentialDialog (Edit button) — keeping
// inline + dialog editors in sync was UX clutter for one number.
function BudgetLine({ credential }: { credential: CloudCredential }) {
  const display = credential.monthly_budget_usd != null
    ? `$${credential.monthly_budget_usd.toLocaleString('en-US', { maximumFractionDigits: 2 })}/mo`
    : 'no cap';
  return (
    <span>
      {' · '}budget: <span className={credential.monthly_budget_usd != null ? 'text-ink' : ''}>{display}</span>
    </span>
  );
}

function AddCredentialDialog({
  cloud, editing, onClose, onAdded,
}: {
  cloud: CloudKind;
  /** When set, the dialog is in edit mode: name/region pre-filled from
   *  the existing row, secret payload fields show "(unchanged)" and
   *  only get sent if the operator types something new. */
  editing?: CloudCredential;
  onClose: () => void;
  onAdded: () => void;
}) {
  const isEdit = editing != null;
  const [name, setName] = useState(editing?.name ?? '');
  const [region, setRegion] = useState(editing?.region ?? '');
  const [payload, setPayload] = useState<Record<string, string>>({});
  // Budget input — empty string means "no cap" semantically; the
  // dialog converts to null/number on submit. Stored as string so the
  // operator can clear an existing cap by deleting the digits.
  const [budgetStr, setBudgetStr] = useState<string>(
    editing?.monthly_budget_usd != null ? String(editing.monthly_budget_usd) : '',
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Per-cloud field schema. Keep the shape in lock-step with the
  // server-side requireKeys() in api/cloud_credentials.go — a typo
  // here will surface as a 400 on save, but it's friendlier to
  // catch it before the round-trip.
  const fields = fieldsForCloud(cloud);

  // Parse the budget input. null = "no cap"; number = cap. NaN/negative
  // is rejected up-front so we don't fire a doomed PUT.
  function parseBudget(): { ok: true; value: number | null } | { ok: false; error: string } {
    const trimmed = budgetStr.trim();
    if (trimmed === '') return { ok: true, value: null };
    const n = Number(trimmed);
    if (!Number.isFinite(n) || n < 0) {
      return { ok: false, error: 'budget must be a non-negative number, or blank for no cap' };
    }
    return { ok: true, value: n };
  }

  async function submit() {
    const budget = parseBudget();
    if (!budget.ok) { setError(budget.error); return; }
    setBusy(true); setError(null);
    try {
      let resultId: number;
      if (isEdit) {
        // Build a sparse update — name/region always sent (cheap +
        // server treats null/missing as "leave alone"); payload only
        // includes keys the operator actually re-typed (non-empty).
        const sparsePayload: Record<string, string> = {};
        for (const [k, v] of Object.entries(payload)) {
          if (v.trim() !== '') sparsePayload[k] = v;
        }
        const req: CloudCredentialUpdateRequest = {
          name: name !== editing!.name ? name : undefined,
          region: region !== (editing!.region ?? '') ? region : undefined,
          payload: Object.keys(sparsePayload).length > 0 ? sparsePayload : undefined,
        };
        const credChanged = req.name !== undefined || req.region !== undefined || req.payload !== undefined;
        if (credChanged) {
          await api.cloudCredentialUpdate(editing!.id, req);
        }
        resultId = editing!.id;
      } else {
        const req: CloudCredentialCreateRequest = {
          cloud,
          name,
          region: region || undefined,
          payload,
        };
        const created = await api.cloudCredentialCreate(req);
        resultId = created.id;
      }
      // Sync the budget if it differs from what's stored. Always
      // fires on create when the operator typed a value; on edit
      // only when changed (avoids a no-op PUT on rename-only edits).
      const currentBudget = editing?.monthly_budget_usd ?? null;
      if (budget.value !== currentBudget) {
        await api.cloudCredentialBudget(resultId, budget.value);
      }
      onAdded();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="fixed inset-0 bg-black/30 flex items-center justify-center p-4 z-50">
      <div className="bg-panel border border-border rounded-xl shadow-card w-full max-w-lg">
        <header className="px-5 py-3 border-b border-border flex items-center justify-between">
          <h3 className="text-sm font-semibold flex items-center gap-2">
            <Cloud size={14} className="text-brand-500" /> {isEdit ? `Edit ${editing!.name}` : `Add ${labelFor(cloud)} credential`}
          </h3>
          <button onClick={onClose} className="p-1 text-ink-dim hover:text-ink rounded-md">
            <X size={16} />
          </button>
        </header>
        <div className="p-5 space-y-3 text-sm max-h-[70vh] overflow-y-auto">
          <Field label="Credential name">
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="prod-tenancy"
              className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
            />
          </Field>
          <Field label="Default region (optional)">
            <input
              type="text"
              value={region}
              onChange={(e) => setRegion(e.target.value)}
              placeholder={defaultRegionFor(cloud)}
              className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
            />
          </Field>
          <Field
            label="Monthly budget (USD, optional)"
            hint="Caps managed-deploy provisioning against this credential. Leave blank for no cap. Enforced by the cost catalog at submit time."
          >
            <div className="flex items-center gap-2">
              <span className="text-ink-mute text-sm">$</span>
              <input
                type="number" min={0} step={1}
                value={budgetStr}
                onChange={(e) => setBudgetStr(e.target.value)}
                placeholder="no cap"
                className="w-32 px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
              />
              <span className="text-ink-mute text-sm">/mo</span>
            </div>
          </Field>
          <hr className="border-border/60 my-2" />
          {isEdit && (
            <p className="text-[11px] text-ink-mute -mt-1">
              Leave a field blank to keep its current value. Secret fields (private keys, access secrets, …) stay encrypted-at-rest unless you re-type them.
            </p>
          )}
          {fields.map((f) => {
            const editPlaceholder = isEdit
              ? (f.secret ? '(blank to keep current)' : f.placeholder)
              : f.placeholder;
            return (
              <Field key={f.key} label={f.label} hint={f.hint}>
                {f.multiline ? (
                  <textarea
                    value={payload[f.key] ?? ''}
                    onChange={(e) => setPayload((p) => ({ ...p, [f.key]: e.target.value }))}
                    rows={f.rows ?? 5}
                    className="w-full px-3 py-1.5 text-xs font-mono border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
                    placeholder={editPlaceholder}
                  />
                ) : (
                  <input
                    type={f.secret ? 'password' : 'text'}
                    value={payload[f.key] ?? ''}
                    onChange={(e) => setPayload((p) => ({ ...p, [f.key]: e.target.value }))}
                    placeholder={editPlaceholder}
                    className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
                  />
                )}
              </Field>
            );
          })}
          {error && (
            <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">{error}</div>
          )}
        </div>
        <footer className="px-5 py-3 border-t border-border flex items-center justify-end gap-2">
          <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md">Cancel</button>
          <button
            onClick={submit}
            disabled={
              busy || !name ||
              // In edit mode, a required field can stay blank (its
              // current value is preserved). Only block create-mode.
              (!isEdit && fields.some((f) => f.required && !(payload[f.key] ?? '').trim()))
            }
            className="text-xs px-3 py-1.5 bg-brand-500 hover:bg-brand-600 disabled:opacity-50 text-white rounded-md font-medium inline-flex items-center gap-1.5"
          >
            {busy && <Loader2 size={12} className="animate-spin" />}
            Save
          </button>
        </footer>
      </div>
    </div>
  );
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="text-[11px] uppercase tracking-wide text-ink-mute font-medium mb-1">{label}</div>
      {children}
      {hint && <div className="text-[11px] text-ink-mute mt-1">{hint}</div>}
    </div>
  );
}

interface FieldSpec {
  key: string;
  label: string;
  required?: boolean;
  secret?: boolean;
  multiline?: boolean;
  rows?: number;
  placeholder?: string;
  hint?: string;
}

function fieldsForCloud(cloud: CloudKind): FieldSpec[] {
  switch (cloud) {
    case 'oci':
      return [
        { key: 'tenancy_ocid', label: 'Tenancy OCID',  required: true, placeholder: 'ocid1.tenancy.oc1..…' },
        { key: 'user_ocid',    label: 'User OCID',      required: true, placeholder: 'ocid1.user.oc1..…' },
        { key: 'fingerprint',  label: 'Key fingerprint', required: true, placeholder: 'aa:bb:cc:…' },
        { key: 'private_key',  label: 'Private key (PEM)', required: true, secret: true, multiline: true, rows: 8,
          placeholder: '-----BEGIN PRIVATE KEY-----\n…\n-----END PRIVATE KEY-----',
          hint: 'PEM body of the API signing key associated with the user OCID.' },
        { key: 'region',       label: 'Region',         required: true, placeholder: 'us-ashburn-1' },
      ];
    case 'aws':
      return [
        { key: 'access_key_id',     label: 'Access key ID',     required: true, placeholder: 'AKIA…' },
        { key: 'secret_access_key', label: 'Secret access key', required: true, secret: true },
        { key: 'region',            label: 'Region',            required: true, placeholder: 'us-east-1' },
        { key: 'role_arn',          label: 'Role ARN (optional)', placeholder: 'arn:aws:iam::123:role/CPProvisioner',
          hint: 'When set, the CP will sts:AssumeRole into this ARN before making API calls.' },
      ];
    case 'gcp':
      return [
        { key: 'service_account_json', label: 'Service account JSON', required: true, secret: true, multiline: true, rows: 10,
          placeholder: '{"type":"service_account",…}',
          hint: 'Full contents of the JSON key file downloaded from the GCP console.' },
      ];
    case 'azure':
      return [
        { key: 'tenant_id',       label: 'Tenant ID',        required: true },
        { key: 'client_id',       label: 'Client ID',        required: true },
        { key: 'client_secret',   label: 'Client secret',    required: true, secret: true },
        { key: 'subscription_id', label: 'Subscription ID',  required: true },
      ];
    case 'digitalocean':
      return [
        { key: 'api_token', label: 'API token', required: true, secret: true, placeholder: 'dop_v1_…' },
        { key: 'region',    label: 'Region',    required: true, placeholder: 'nyc3' },
      ];
  }
}

function labelFor(cloud: CloudKind): string {
  return CLOUDS.find((c) => c.kind === cloud)?.label ?? cloud;
}

function defaultRegionFor(cloud: CloudKind): string {
  switch (cloud) {
    case 'oci': return 'us-ashburn-1';
    case 'aws': return 'us-east-1';
    case 'gcp': return 'us-central1';
    case 'azure': return 'eastus';
    case 'digitalocean': return 'nyc3';
  }
}
