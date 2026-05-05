// Phase 21.7 — Add Node → Managed deploy tab body. Direct port of
// Federation.tsx::ManagedDeployPanel against the node-side
// /api/node-provision endpoint. The federation-transport radios are
// dropped — managed-deploy nodes always go via S3 dead-drop, so the
// transport_config_id picker is required (operator picks which CP's
// bucket the new node enrolls to).
//
// Cost preview uses the simpler /api/node-provision/estimate response
// ({hourly_usd} only — no budget gate yet). Cloud picker reuses the
// same registry that serves cp_provision so OCI/AWS slot in here
// once the CP-side impls are registered.
import { useEffect, useState } from 'react';
import { Loader2 } from 'lucide-react';
import {
  api,
  type CloudCredential,
  type CloudKind,
  type TransportConfigSummary,
} from '../../api';
import { CloudParamsForm, cloudParamsMissingFields } from '../cloud/CloudParamsForm';

export function AddNodeManaged({ onClose }: { onClose: () => void }) {
  const [provisioners, setProvisioners] = useState<string[] | null>(null);
  const [credentials, setCredentials] = useState<CloudCredential[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const [cloud, setCloud] = useState<CloudKind | ''>('');
  const [credentialID, setCredentialID] = useState<number | null>(null);
  const [displayName, setDisplayName] = useState('');
  const [region, setRegion] = useState('');
  const [transportConfigID, setTransportConfigID] = useState<number | null>(null);
  const [transportConfigs, setTransportConfigs] = useState<TransportConfigSummary[] | null>(null);
  // Structured cloud_params — populated by per-cloud form components.
  const [cloudParams, setCloudParams] = useState<Record<string, unknown>>({});
  const [busy, setBusy] = useState(false);
  const [submitted, setSubmitted] = useState<{ id: number; status: string } | null>(null);

  // Live cost preview. Re-fetched (debounced) on every change to
  // (cloud, params). Node-side estimate has no budget context — just
  // an hourly_usd number — so we render simply.
  const [estimate, setEstimate] = useState<{ hourly_usd: number } | null>(null);

  // Reset cloud_params when cloud changes — different schemas, no
  // sense carrying OCI fields into an AWS submit.
  useEffect(() => {
    setCloudParams({});
  }, [cloud]);

  useEffect(() => {
    if (!cloud) {
      setEstimate(null);
      return;
    }
    let cancelled = false;
    const handle = setTimeout(() => {
      api.nodeProvisionEstimate({ cloud, cloud_params: cloudParams })
        .then((est) => { if (!cancelled) setEstimate(est); })
        .catch(() => { if (!cancelled) setEstimate(null); });
    }, 250);
    return () => { cancelled = true; clearTimeout(handle); };
  }, [cloud, cloudParams]);

  useEffect(() => {
    api.cpProvisionersList()
      .then((r) => setProvisioners(r.clouds))
      .catch((e) => setError(String(e)));
    api.cloudCredentialsList()
      .then(setCredentials)
      .catch((e) => setError(String(e)));
    // transport_configs is required for managed-deploy nodes (the
    // dead-drop pipe) — load eagerly.
    api.transportConfigs()
      .then((rows) => {
        setTransportConfigs(rows);
        if (rows.length > 0) setTransportConfigID(rows[0].id);
      })
      .catch((e) => setError('load transport configs: ' + String(e)));
  }, []);

  // Auto-pick the first registered cloud + first credential of that
  // cloud as a usability nicety.
  useEffect(() => {
    if (cloud === '' && provisioners && provisioners.length > 0) {
      setCloud(provisioners[0] as CloudKind);
    }
  }, [provisioners, cloud]);
  useEffect(() => {
    if (cloud && credentials) {
      const match = credentials.find((c) => c.cloud === cloud);
      if (match) {
        setCredentialID(match.id);
        if (!region && match.region) setRegion(match.region);
      } else {
        setCredentialID(null);
      }
    }
  }, [cloud, credentials, region]);

  const usableCreds = (credentials ?? []).filter((c) => c.cloud === cloud);

  // Per-cloud required-key list. Mirrors the validators in
  // controlplane/cpprovision/{oci,aws}/*.go so the UI blocks the
  // submit before the worker rejects it.
  const missing = cloudParamsMissingFields(cloud, cloudParams);

  async function submit() {
    if (!cloud || !credentialID) return;
    if (missing.length > 0) {
      setError(`missing required cloud_params: ${missing.join(', ')}`);
      return;
    }
    if (!transportConfigID) {
      setError('pick a bucket (transport_config) — managed-deploy nodes enroll via S3 dead-drop');
      return;
    }
    setBusy(true); setError(null);
    try {
      const row = await api.nodeProvisionCreate({
        display_name: displayName,
        region,
        cloud,
        credential_id: credentialID,
        cloud_params: cloudParams,
        transport_config_id: transportConfigID,
      });
      setSubmitted({ id: row.id, status: row.status });
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  // Loading state — registry + creds in flight.
  if (provisioners === null || credentials === null) {
    return (
      <div className="p-5 text-xs text-ink-mute flex items-center gap-2">
        <Loader2 size={12} className="animate-spin" /> Loading provisioners…
      </div>
    );
  }

  // No registered clouds — same default as the CP-side managed deploy
  // panel; help the operator toward the working alternatives.
  if (provisioners.length === 0) {
    return (
      <>
        <div className="p-5 text-sm space-y-3">
          <div className="text-xs text-ink-dim">
            No cloud provisioners are registered on this CP yet. Per-cloud impls land in upcoming
            releases (OCI in 21.3b, AWS in 21.3c).
          </div>
          <div className="text-xs bg-amber-50 border border-amber-200 rounded-md px-3 py-2 text-ink">
            For now, use <strong>SSH push</strong> to register a node by pushing the daemon over SSH,
            or <strong>S3 dead-drop</strong> to mint an enrollment package the operator drops on
            the target host themselves.
          </div>
        </div>
        <footer className="px-5 py-3 border-t border-border flex items-center justify-end">
          <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md">Close</button>
        </footer>
      </>
    );
  }

  // Submitted — show the job-id + status, point at the panel below
  // the node list for the live job log.
  if (submitted) {
    return (
      <>
        <div className="p-5 text-sm space-y-3">
          <div className="text-xs text-emerald-700 bg-emerald-50 border border-emerald-200 rounded-md px-3 py-2">
            Provision job <strong>#{submitted.id}</strong> created (status: <code>{submitted.status}</code>).
            Watch progress in the <strong>Managed deploys</strong> panel below the node list — it
            auto-refreshes while the deploy is in flight.
          </div>
        </div>
        <footer className="px-5 py-3 border-t border-border flex items-center justify-end">
          <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md">Close</button>
        </footer>
      </>
    );
  }

  return (
    <>
      <div className="p-5 space-y-3 text-sm max-h-[70vh] overflow-y-auto">
        <p className="text-xs text-ink-dim">
          Provisions a node via your stored cloud credentials. The new VM auto-enrolls with this
          parent on first boot via the S3 dead-drop bucket — no manual intervention.
        </p>
        <Field label="Display name">
          <input
            type="text" value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            placeholder="prod-web-01"
            className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
          />
        </Field>
        <Field label="Cloud">
          <select
            value={cloud}
            onChange={(e) => setCloud(e.target.value as CloudKind)}
            className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 bg-panel"
          >
            {provisioners.map((c) => <option key={c} value={c}>{c}</option>)}
          </select>
        </Field>
        <Field label="Credential">
          <select
            value={credentialID ?? ''}
            onChange={(e) => setCredentialID(e.target.value ? Number(e.target.value) : null)}
            className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 bg-panel"
          >
            <option value="">— select —</option>
            {usableCreds.map((c) => (
              <option key={c.id} value={c.id}>{c.name}{c.region ? ` (${c.region})` : ''}</option>
            ))}
          </select>
          {usableCreds.length === 0 && (
            <div className="text-[11px] text-amber-700 mt-1">
              No credentials saved for {cloud}. Add one under Settings → Cloud first.
            </div>
          )}
        </Field>
        <Field label="Region">
          <input
            type="text" value={region}
            onChange={(e) => setRegion(e.target.value)}
            placeholder="defaults to credential's region"
            className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
          />
        </Field>
        <Field
          label="Bucket (transport_config)"
          hint="Which dead-drop bucket the new node enrolls through. Pick an existing transport_config; both the CP and the new node need outbound HTTPS to it."
        >
          {transportConfigs === null ? (
            <div className="text-xs text-ink-mute">loading…</div>
          ) : transportConfigs.length === 0 ? (
            <div className="text-xs text-amber-700">
              No transport_configs configured. Add one under Settings → Cloud → Object storage buckets,
              or via Nodes → Add Node → S3 dead-drop, before continuing.
            </div>
          ) : (
            <select
              value={transportConfigID ?? ''}
              onChange={(e) => setTransportConfigID(e.target.value ? Number(e.target.value) : null)}
              className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 bg-panel"
            >
              {transportConfigs.map((tc) => (
                <option key={tc.id} value={tc.id}>
                  {tc.name} · {tc.bucket} @ {tc.endpoint}
                </option>
              ))}
            </select>
          )}
        </Field>
        {cloud && credentialID && (
          <CloudParamsForm
            cloud={cloud}
            credentialID={credentialID}
            region={region}
            value={cloudParams}
            onChange={setCloudParams}
          />
        )}
        {estimate && estimate.hourly_usd > 0 && (
          <div className="text-xs px-3 py-2 rounded-md border bg-slate-50 border-border text-ink-dim">
            <strong className="text-ink">Estimated cost:</strong>{' '}
            ~${estimate.hourly_usd.toFixed(2)}/hr
          </div>
        )}
        {error && (
          <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">{error}</div>
        )}
      </div>
      <footer className="px-5 py-3 border-t border-border flex items-center justify-between gap-2">
        <div className="text-[11px] text-ink-mute">
          {missing.length > 0
            ? <>missing: <span className="text-red-700">{missing.join(', ')}</span></>
            : <>all required fields populated</>
          }
        </div>
        <div className="flex items-center gap-2">
          <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md">Cancel</button>
          <button
            onClick={submit}
            disabled={
              busy || !displayName || !cloud || !credentialID || !region ||
              !transportConfigID || missing.length > 0
            }
            className="text-xs px-3 py-1.5 bg-brand-500 hover:bg-brand-600 disabled:opacity-50 text-white rounded-md font-medium inline-flex items-center gap-1.5"
          >
            {busy && <Loader2 size={12} className="animate-spin" />}
            Provision
          </button>
        </div>
      </footer>
    </>
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
