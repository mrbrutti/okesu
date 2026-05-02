import { useEffect, useState } from 'react';
import { Loader2, Plus, X } from 'lucide-react';

import {
  api,
  type BucketCloudProvider,
  type BucketInfo,
  type BucketProvisionReq,
  type CloudCompartment,
  type TransportConfigCreateReq,
  type TransportConfigSummary,
} from '../api';
import { cn } from '../lib/cn';

type Source = 'configured' | 'manual';
type SubMode = 'discover' | 'create';

interface Props {
  onClose: () => void;
  onCreated: (tc: TransportConfigSummary) => void;
}

export default function AddBucketWizard({ onClose, onCreated }: Props) {
  const [step, setStep] = useState<1 | 2>(1);
  const [source, setSource] = useState<Source>('configured');
  const [providers, setProviders] = useState<BucketCloudProvider[] | null>(null);
  const [providerID, setProviderID] = useState<number | null>(null);
  const [region, setRegion] = useState('');
  const [subMode, setSubMode] = useState<SubMode>('create');
  const [bucketName, setBucketName] = useState('');
  const [discovered, setDiscovered] = useState<BucketInfo[] | null>(null);
  const [discoverLoading, setDiscoverLoading] = useState(false);
  const [displayName, setDisplayName] = useState('');
  const [scannerIntervalMs, setScannerIntervalMs] = useState(30000);
  const [generateFleetKeys, setGenerateFleetKeys] = useState(true);
  const [compartments, setCompartments] = useState<CloudCompartment[] | null>(null);
  const [compartmentID, setCompartmentID] = useState('');
  const [compartmentsLoading, setCompartmentsLoading] = useState(false);
  const [compartmentManualMode, setCompartmentManualMode] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Manual-entry fields (the existing manual flow's shape).
  const [manual, setManual] = useState<TransportConfigCreateReq>({
    name: '',
    kind: 's3',
    bucket: '',
    endpoint: '',
    region: '',
    use_ssl: true,
    access_key: '',
    secret_key: '',
    generate_fleet_keys: true,
    scanner_interval_ms: 30000,
  });

  useEffect(() => {
    api.bucketCloudProviders()
      .then((p) => {
        setProviders(p ?? []);
        if ((p ?? []).length === 0) setSource('manual');
      })
      .catch((e) => setError(String(e)));
  }, []);

  // When provider+region change in configured discover mode, fetch.
  useEffect(() => {
    if (source !== 'configured' || !providerID || !region) return;
    if (subMode !== 'discover') return;
    setDiscoverLoading(true);
    setDiscovered(null);
    api.bucketsDiscover(providerID, region)
      .then((rows) => setDiscovered(rows ?? []))
      .catch((e) => setError(String(e)))
      .finally(() => setDiscoverLoading(false));
  }, [source, providerID, region, subMode]);

  // Fetch compartments when provider + region are set in configured mode.
  useEffect(() => {
    if (source !== 'configured' || !providerID || !region) {
      setCompartments(null);
      setCompartmentID('');
      return;
    }
    setCompartmentsLoading(true);
    api.cloudCredentialCompartments(providerID, region)
      .then((c) => {
        setCompartments(c);
        // Auto-select if exactly one compartment is returned.
        if (c.length === 1) setCompartmentID(c[0].ocid);
      })
      .catch((err) => {
        // If listing fails (likely IAM gap), fall back to manual entry.
        setCompartments([]);
        setCompartmentManualMode(true);
        console.warn('compartments list failed:', err);
      })
      .finally(() => setCompartmentsLoading(false));
  }, [source, providerID, region]);

  function submitConfigured() {
    if (!providerID || !displayName || !bucketName || !region) {
      setError('Pick a provider, region, and bucket; enter a display name.');
      return;
    }
    const req: BucketProvisionReq = {
      cloud_credential_id: providerID,
      region,
      compartment_id: compartmentID || undefined,
      bucket_name: bucketName,
      mode: subMode,
      display_name: displayName,
      generate_fleet_keys: generateFleetKeys,
      scanner_interval_ms: scannerIntervalMs,
    };
    setSubmitting(true);
    setError(null);
    api.bucketsProvision(req)
      .then((tc) => onCreated(tc))
      .catch((e) => setError(String(e)))
      .finally(() => setSubmitting(false));
  }

  function submitManual() {
    if (!manual.name || !manual.bucket || !manual.endpoint) {
      setError('Display name, bucket, and endpoint are required.');
      return;
    }
    setSubmitting(true);
    setError(null);
    api.transportConfigCreate(manual)
      .then((tc) => onCreated(tc))
      .catch((e) => setError(String(e)))
      .finally(() => setSubmitting(false));
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="bg-panel border border-border rounded-lg shadow-lg w-[640px] max-h-[90vh] overflow-y-auto">
        <div className="px-5 py-3 border-b border-border flex items-center justify-between">
          <h2 className="text-sm font-semibold flex items-center gap-2">
            <Plus size={14} /> Add bucket
          </h2>
          <button onClick={onClose} className="text-ink-mute hover:text-ink" aria-label="Close">
            <X size={16} />
          </button>
        </div>

        <div className="px-5 py-4 space-y-4">
          {error && (
            <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
              {error}
            </div>
          )}

          {step === 1 && (
            <>
              <div className="space-y-2">
                <label className="flex items-start gap-2 text-sm">
                  <input
                    type="radio"
                    checked={source === 'configured'}
                    onChange={() => setSource('configured')}
                    disabled={(providers?.length ?? 0) === 0}
                  />
                  <div>
                    <div className="font-medium">Use a configured cloud provider</div>
                    <div className="text-[11px] text-ink-mute">
                      AWS / OCI / MinIO. Use credentials already saved in Settings → Cloud.
                    </div>
                  </div>
                </label>
                <label className="flex items-start gap-2 text-sm">
                  <input type="radio" checked={source === 'manual'} onChange={() => setSource('manual')} />
                  <div>
                    <div className="font-medium">Manual entry</div>
                    <div className="text-[11px] text-ink-mute">
                      Type bucket + endpoint + access keys by hand. Useful for on-prem or third-party S3-compatible services.
                    </div>
                  </div>
                </label>
              </div>
              {source === 'configured' && (
                <div className="space-y-2">
                  <label className="text-xs text-ink-mute">Provider</label>
                  <select
                    value={providerID ?? ''}
                    onChange={(e) => setProviderID(e.target.value ? Number(e.target.value) : null)}
                    className="w-full text-sm px-2.5 py-1.5 rounded-md ring-1 ring-border bg-white"
                  >
                    <option value="">Pick one…</option>
                    {(providers ?? []).map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.display_name} · {p.cloud}
                        {p.region ? ` · ${p.region}` : ''}
                      </option>
                    ))}
                  </select>
                  {(providers ?? []).length === 0 && (
                    <div className="text-[11px] text-ink-mute">
                      No AWS/OCI/MinIO credentials configured. Add one in Settings → Cloud first.
                    </div>
                  )}
                </div>
              )}
              <div className="flex justify-end gap-2 pt-2">
                <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md hover:bg-slate-50">
                  Cancel
                </button>
                <button
                  onClick={() => setStep(2)}
                  disabled={source === 'configured' && !providerID}
                  className="text-xs px-3 py-1.5 bg-brand-600 text-white rounded-md hover:bg-brand-700 disabled:opacity-50"
                >
                  Next →
                </button>
              </div>
            </>
          )}

          {step === 2 && source === 'configured' && (
            <>
              <div className="space-y-2">
                <label className="text-xs text-ink-mute">Region</label>
                <input
                  type="text"
                  value={region}
                  onChange={(e) => setRegion(e.target.value)}
                  placeholder="us-east-1"
                  className="w-full text-sm px-2.5 py-1.5 rounded-md ring-1 ring-border bg-white"
                />
              </div>
              <div className="space-y-2">
                <div className="flex items-center justify-between">
                  <label className="text-xs text-ink-mute">Compartment</label>
                  {compartments && compartments.length > 0 && (
                    <button
                      type="button"
                      onClick={() => setCompartmentManualMode((v) => !v)}
                      className="text-[10px] text-ink-dim hover:text-ink underline"
                    >
                      {compartmentManualMode ? 'Pick from list' : 'Enter OCID manually'}
                    </button>
                  )}
                </div>
                {compartmentsLoading ? (
                  <div className="flex items-center gap-2 text-xs text-ink-dim">
                    <Loader2 size={12} className="animate-spin" /> Listing compartments…
                  </div>
                ) : compartments === null ? (
                  <div className="text-[11px] text-ink-mute">
                    Pick a provider and region above to list compartments.
                  </div>
                ) : compartments.length === 0 && !compartmentManualMode ? (
                  <div className="text-[11px] text-ink-mute">
                    This cloud doesn't use compartments. Leave blank — buckets land in the account default.
                  </div>
                ) : compartmentManualMode ? (
                  <input
                    type="text"
                    value={compartmentID}
                    onChange={(e) => setCompartmentID(e.target.value)}
                    placeholder="ocid1.compartment.oc1..…"
                    className="w-full text-xs font-mono px-2.5 py-1.5 rounded-md ring-1 ring-border bg-white"
                  />
                ) : (
                  <select
                    value={compartmentID}
                    onChange={(e) => setCompartmentID(e.target.value)}
                    className="w-full text-sm px-2.5 py-1.5 rounded-md ring-1 ring-border bg-white"
                  >
                    <option value="">Choose a compartment…</option>
                    {compartments.map((c) => (
                      <option key={c.ocid} value={c.ocid}>
                        {c.name} · {c.ocid.length > 30 ? c.ocid.slice(0, 20) + '…' + c.ocid.slice(-8) : c.ocid}
                      </option>
                    ))}
                  </select>
                )}
              </div>
              <div className="flex gap-1 border-b border-border">
                <button
                  onClick={() => setSubMode('discover')}
                  className={cn(
                    'px-3 py-1.5 text-xs border-b-2 -mb-px',
                    subMode === 'discover'
                      ? 'border-brand-600 text-brand-700 font-medium'
                      : 'border-transparent text-ink-dim hover:text-ink',
                  )}
                >
                  Discover existing
                </button>
                <button
                  onClick={() => setSubMode('create')}
                  className={cn(
                    'px-3 py-1.5 text-xs border-b-2 -mb-px',
                    subMode === 'create'
                      ? 'border-brand-600 text-brand-700 font-medium'
                      : 'border-transparent text-ink-dim hover:text-ink',
                  )}
                >
                  Create new
                </button>
              </div>
              {subMode === 'discover' && (
                <div>
                  {discoverLoading ? (
                    <div className="flex items-center gap-2 text-xs text-ink-dim">
                      <Loader2 size={12} className="animate-spin" /> Listing buckets…
                    </div>
                  ) : !discovered ? (
                    <div className="text-[11px] text-ink-mute">Pick a region above to list buckets.</div>
                  ) : discovered.length === 0 ? (
                    <div className="text-[11px] text-ink-mute">No buckets found in this account/region.</div>
                  ) : (
                    <ul className="border border-border rounded-md divide-y divide-border bg-white max-h-48 overflow-y-auto">
                      {discovered.map((b) => (
                        <li
                          key={b.name}
                          onClick={() => setBucketName(b.name)}
                          className={cn(
                            'px-3 py-2 text-sm cursor-pointer hover:bg-slate-50',
                            bucketName === b.name && 'bg-brand-50',
                          )}
                        >
                          <div className="font-mono">{b.name}</div>
                          <div className="text-[10px] text-ink-mute">{b.region} · {b.endpoint}</div>
                        </li>
                      ))}
                    </ul>
                  )}
                </div>
              )}
              {subMode === 'create' && (
                <div className="space-y-2">
                  <label className="text-xs text-ink-mute">New bucket name</label>
                  <input
                    type="text"
                    value={bucketName}
                    onChange={(e) => setBucketName(e.target.value)}
                    placeholder="okesu-fleet-prod"
                    className="w-full text-sm px-2.5 py-1.5 rounded-md ring-1 ring-border bg-white"
                  />
                  <div className="text-[10px] text-ink-mute">
                    AWS/MinIO: 3-63 chars, lowercase, no underscores. OCI: more permissive.
                  </div>
                </div>
              )}
              <div className="space-y-2 pt-2 border-t border-border">
                <label className="text-xs text-ink-mute">Display name</label>
                <input
                  type="text"
                  value={displayName}
                  onChange={(e) => setDisplayName(e.target.value)}
                  placeholder="Prod fleet bucket"
                  className="w-full text-sm px-2.5 py-1.5 rounded-md ring-1 ring-border bg-white"
                />
                <label className="text-xs text-ink-mute">Scanner interval (ms)</label>
                <input
                  type="number"
                  value={scannerIntervalMs}
                  onChange={(e) => setScannerIntervalMs(Number(e.target.value))}
                  className="w-full text-sm px-2.5 py-1.5 rounded-md ring-1 ring-border bg-white"
                />
                <label className="flex items-center gap-2 text-xs">
                  <input type="checkbox" checked={generateFleetKeys} onChange={(e) => setGenerateFleetKeys(e.target.checked)} />
                  Generate fleet keypair
                </label>
              </div>
              <div className="flex justify-between pt-2">
                <button onClick={() => setStep(1)} className="text-xs px-3 py-1.5 border border-border rounded-md hover:bg-slate-50">
                  ← Back
                </button>
                <button
                  onClick={submitConfigured}
                  disabled={submitting || compartmentsLoading}
                  className="text-xs px-3 py-1.5 bg-brand-600 text-white rounded-md hover:bg-brand-700 disabled:opacity-50"
                >
                  {submitting ? 'Provisioning…' : 'Add bucket'}
                </button>
              </div>
            </>
          )}

          {step === 2 && source === 'manual' && (
            <>
              <div className="grid grid-cols-2 gap-3 text-xs">
                <Field label="Display name" value={manual.name} onChange={(v) => setManual({ ...manual, name: v })} />
                <Field label="Bucket" value={manual.bucket} onChange={(v) => setManual({ ...manual, bucket: v })} />
                <Field label="Endpoint" value={manual.endpoint} onChange={(v) => setManual({ ...manual, endpoint: v })} />
                <Field label="Region" value={manual.region ?? ''} onChange={(v) => setManual({ ...manual, region: v })} />
                <Field label="Access key" value={manual.access_key ?? ''} onChange={(v) => setManual({ ...manual, access_key: v })} />
                <Field label="Secret key" type="password" value={manual.secret_key ?? ''} onChange={(v) => setManual({ ...manual, secret_key: v })} />
              </div>
              <label className="flex items-center gap-2 text-xs">
                <input type="checkbox" checked={manual.use_ssl} onChange={(e) => setManual({ ...manual, use_ssl: e.target.checked })} />
                Use SSL
              </label>
              <label className="flex items-center gap-2 text-xs">
                <input type="checkbox" checked={!!manual.generate_fleet_keys} onChange={(e) => setManual({ ...manual, generate_fleet_keys: e.target.checked })} />
                Generate fleet keypair
              </label>
              <div className="flex justify-between pt-2">
                <button onClick={() => setStep(1)} className="text-xs px-3 py-1.5 border border-border rounded-md hover:bg-slate-50">
                  ← Back
                </button>
                <button
                  onClick={submitManual}
                  disabled={submitting}
                  className="text-xs px-3 py-1.5 bg-brand-600 text-white rounded-md hover:bg-brand-700 disabled:opacity-50"
                >
                  {submitting ? 'Saving…' : 'Add bucket'}
                </button>
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  );
}

function Field({ label, value, onChange, type = 'text' }: { label: string; value: string; onChange: (v: string) => void; type?: string }) {
  return (
    <label className="flex flex-col gap-1">
      <span className="text-ink-mute">{label}</span>
      <input
        type={type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="text-sm px-2.5 py-1.5 rounded-md ring-1 ring-border bg-white"
      />
    </label>
  );
}
