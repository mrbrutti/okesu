// Phase 21.7 — shared cloud-params form. Originally inline in
// Federation.tsx for the managed CP-deploy panel; lifted here so the
// Nodes page's managed-deploy panel can reuse the same auto-discovery
// UI (compartments / ADs / subnets / images / shapes for OCI; flat
// fields for AWS) without duplicating ~250 lines of code.
//
// The component is purely a controlled-form: parent owns the
// cloud_params object, this component renders inputs + writes patches
// back via onChange. Per-cloud validators in `cloudParamsMissingFields`
// mirror the server-side requireKeys() so the submit button can be
// gated before the worker rejects an empty payload.
//
// `Field` is duplicated locally rather than imported from a page file
// so this component has no upstream dependency on Federation.tsx.
import { useEffect, useState, type ReactNode } from 'react';
import { api, type CloudKind, type DiscoveryItem } from '../../api';

export function CloudParamsForm({
  cloud, credentialID, region, value, onChange,
}: {
  cloud: CloudKind;
  credentialID: number;
  region: string;
  value: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
}) {
  if (cloud === 'oci') {
    return <OciCloudParamsForm credentialID={credentialID} region={region} value={value} onChange={onChange} />;
  }
  if (cloud === 'aws') {
    return <AwsCloudParamsFormStub value={value} onChange={onChange} />;
  }
  return null;
}

// cloudParamsMissingFields enumerates which keys the per-cloud
// Provisioner.Launch validator will reject if absent. Mirrors the
// requireKeys() / decodeLaunchParams() logic on the server so the UI
// blocks the submit instead of letting the worker fail post-token-mint.
export function cloudParamsMissingFields(
  cloud: CloudKind | '',
  params: Record<string, unknown>,
): string[] {
  const has = (k: string) => {
    const v = params[k];
    if (v === undefined || v === null) return false;
    if (typeof v === 'string') return v.trim() !== '';
    if (Array.isArray(v)) return v.length > 0;
    return true;
  };
  switch (cloud) {
    case 'oci':
      return ['compartment_id', 'availability_domain', 'subnet_id', 'image_id', 'shape']
        .filter((k) => !has(k));
    case 'aws':
      return ['ami_id', 'instance_type', 'subnet_id', 'security_group_ids']
        .filter((k) => !has(k));
    default:
      return [];
  }
}

function OciCloudParamsForm({
  credentialID, region, value, onChange,
}: {
  credentialID: number;
  region: string;
  value: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
}) {
  const compartmentID = (value.compartment_id as string) ?? '';
  const ad = (value.availability_domain as string) ?? '';
  const subnetID = (value.subnet_id as string) ?? '';
  const imageID = (value.image_id as string) ?? '';
  const shape = (value.shape as string) ?? '';
  const ocpus = value.ocpus as number | undefined;
  const memoryGB = value.memory_in_gbs as number | undefined;
  const sshKeys = (value.ssh_authorized_keys as string) ?? '';

  const set = (patch: Record<string, unknown>) => onChange({ ...value, ...patch });

  // Discovery state. Each list loads when its prerequisites are set.
  const [compartments, setCompartments] = useState<DiscoveryItem[] | null>(null);
  const [ads, setAds] = useState<DiscoveryItem[] | null>(null);
  const [subnets, setSubnets] = useState<DiscoveryItem[] | null>(null);
  const [images, setImages] = useState<DiscoveryItem[] | null>(null);
  const [shapes, setShapes] = useState<DiscoveryItem[] | null>(null);
  const [discoveryError, setDiscoveryError] = useState<string | null>(null);

  // Compartments load on credential/region change.
  useEffect(() => {
    let cancelled = false;
    setCompartments(null); setDiscoveryError(null);
    api.cloudDiscoveryOCI.compartments(credentialID, region || undefined)
      .then((items) => { if (!cancelled) setCompartments(items); })
      .catch((e) => { if (!cancelled) setDiscoveryError(`compartments: ${e}`); });
    return () => { cancelled = true; };
  }, [credentialID, region]);

  // ADs / subnets / shapes load when compartment is set. Images
  // depend ALSO on the selected shape — see the next effect — so an
  // operator can't accidentally pair an ARM image with an x86 shape
  // (OCI silently terminates the launch when arches don't match).
  useEffect(() => {
    if (!compartmentID) {
      setAds(null); setSubnets(null); setShapes(null);
      return;
    }
    let cancelled = false;
    api.cloudDiscoveryOCI.availabilityDomains(credentialID, compartmentID, region || undefined)
      .then((items) => { if (!cancelled) setAds(items); }).catch(() => {});
    api.cloudDiscoveryOCI.subnets(credentialID, compartmentID, { region: region || undefined })
      .then((items) => { if (!cancelled) setSubnets(items); }).catch(() => {});
    api.cloudDiscoveryOCI.shapes(credentialID, compartmentID, { region: region || undefined })
      .then((items) => { if (!cancelled) setShapes(items); }).catch(() => {});
    return () => { cancelled = true; };
  }, [credentialID, compartmentID, region]);

  // Images list is shape-scoped — OCI only returns images compatible
  // with the requested shape, so the operator can't pick a wrong-arch
  // image. Refires when shape changes.
  useEffect(() => {
    if (!compartmentID) {
      setImages(null);
      return;
    }
    let cancelled = false;
    api.cloudDiscoveryOCI.images(credentialID, compartmentID, {
      region: region || undefined,
      shape: shape || undefined,
    })
      .then((items) => { if (!cancelled) setImages(items); }).catch(() => {});
    return () => { cancelled = true; };
  }, [credentialID, compartmentID, region, shape]);

  // Shape attrs gate the OCPU/memory inputs (.Flex shapes only) and
  // give us min/max bounds for the number fields.
  const flexShape = shape.endsWith('.Flex') || shape.includes('.Flex.');
  const shapeAttrs = shapes?.find((s) => s.id === shape)?.attrs as
    | { ocpus_min?: number; ocpus_max?: number; memory_min_gb?: number; memory_max_gb?: number }
    | undefined;

  // Fill sensible defaults when shape changes — and clear the
  // image because the new shape's image list is about to refresh
  // and the previously-picked id may not be in it (different arch).
  useEffect(() => {
    if (shape && imageID) {
      // Only clear if the image is no longer in the latest list.
      // The list itself updates via the shape-scoped effect; this
      // local reset just keeps the form in a coherent state during
      // the brief gap between shape change and list refresh.
      if (images && !images.some((i) => i.id === imageID)) {
        set({ image_id: '' });
      }
    }
    if (flexShape && shapeAttrs && (ocpus == null || memoryGB == null)) {
      set({
        ocpus: ocpus ?? shapeAttrs.ocpus_min ?? 1,
        memory_in_gbs: memoryGB ?? Math.min(shapeAttrs.memory_max_gb ?? 8, Math.max(shapeAttrs.memory_min_gb ?? 8, 8)),
      });
    }
    if (!flexShape && (ocpus != null || memoryGB != null)) {
      // Strip flex-only fields when leaving a flex shape.
      const { ocpus: _o, memory_in_gbs: _m, ...rest } = value;
      void _o; void _m;
      onChange(rest);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [shape]);

  return (
    <div className="bg-slate-50 border border-border rounded-md p-3 space-y-3">
      <div className="text-[11px] uppercase tracking-wide text-ink-mute font-medium flex items-center justify-between">
        <span>OCI deployment target</span>
        <span className="normal-case text-ink-mute">auto-discovered from credential</span>
      </div>
      {discoveryError && (
        <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-2 py-1 rounded">{discoveryError}</div>
      )}

      <Field label="Compartment" hint="Where the VM will be created. The tenancy root is the safe default if you don't have sub-compartments.">
        <DiscoverySelect
          items={compartments}
          value={compartmentID}
          onChange={(v) => set({
            compartment_id: v,
            // Resetting downstream selections on compartment change —
            // their valid values depend on it.
            availability_domain: '', subnet_id: '', image_id: '', shape: '',
          })}
          placeholder={compartments === null ? 'loading…' : '— pick compartment —'}
        />
      </Field>

      <div className="grid grid-cols-2 gap-3">
        <Field label="Availability domain">
          <DiscoverySelect
            items={ads} value={ad}
            onChange={(v) => set({ availability_domain: v })}
            placeholder={!compartmentID ? 'pick compartment first' : ads === null ? 'loading…' : '— pick AD —'}
            disabled={!compartmentID}
          />
        </Field>
        <Field label="Shape">
          <DiscoverySelect
            items={shapes} value={shape}
            onChange={(v) => set({ shape: v })}
            placeholder={!compartmentID ? 'pick compartment first' : shapes === null ? 'loading…' : '— pick shape —'}
            disabled={!compartmentID}
          />
        </Field>
      </div>

      <Field label="Subnet" hint="The VNIC attaches here. Subnets that prohibit public IPs are still selectable; provisioning will succeed but the bootstrap callback path needs your own NAT.">
        <DiscoverySelect
          items={subnets} value={subnetID}
          onChange={(v) => set({ subnet_id: v })}
          placeholder={!compartmentID ? 'pick compartment first' : subnets === null ? 'loading…' : '— pick subnet —'}
          disabled={!compartmentID}
          renderItem={(s) => `${s.name}${(s.attrs?.cidr_block as string) ? `  (${s.attrs!.cidr_block})` : ''}`}
        />
      </Field>

      <Field label="Image" hint="Latest build per OS+version is shown.">
        <DiscoverySelect
          items={images} value={imageID}
          onChange={(v) => set({ image_id: v })}
          placeholder={!compartmentID ? 'pick compartment first' : images === null ? 'loading…' : '— pick image —'}
          disabled={!compartmentID}
        />
      </Field>

      {flexShape && (
        <div className="grid grid-cols-2 gap-3">
          <Field label="OCPUs" hint={shapeAttrs ? `${shapeAttrs.ocpus_min}–${shapeAttrs.ocpus_max} for ${shape}` : ''}>
            <input
              type="number"
              value={ocpus ?? ''}
              min={shapeAttrs?.ocpus_min ?? 1}
              max={shapeAttrs?.ocpus_max ?? 64}
              step="0.25"
              onChange={(e) => set({ ocpus: e.target.value === '' ? undefined : Number(e.target.value) })}
              className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
            />
          </Field>
          <Field label="Memory (GB)" hint={shapeAttrs ? `${shapeAttrs.memory_min_gb}–${shapeAttrs.memory_max_gb} for ${shape}` : ''}>
            <input
              type="number"
              value={memoryGB ?? ''}
              min={shapeAttrs?.memory_min_gb ?? 1}
              max={shapeAttrs?.memory_max_gb ?? 1024}
              onChange={(e) => set({ memory_in_gbs: e.target.value === '' ? undefined : Number(e.target.value) })}
              className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
            />
          </Field>
        </div>
      )}

      <Field label="SSH authorized keys (optional)" hint="One public key per line. Used for break-glass debugging only — the CP itself doesn't need SSH to function.">
        <textarea
          value={sshKeys}
          onChange={(e) => set({ ssh_authorized_keys: e.target.value })}
          rows={2}
          placeholder="ssh-ed25519 AAAA…"
          className="w-full px-3 py-1.5 text-xs font-mono border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
        />
      </Field>
    </div>
  );
}

function DiscoverySelect({
  items, value, onChange, placeholder, disabled, renderItem,
}: {
  items: DiscoveryItem[] | null;
  value: string;
  onChange: (v: string) => void;
  placeholder: string;
  disabled?: boolean;
  renderItem?: (item: DiscoveryItem) => string;
}) {
  return (
    <select
      value={value}
      onChange={(e) => onChange(e.target.value)}
      disabled={disabled || items === null}
      className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 bg-panel disabled:opacity-50"
    >
      <option value="">{placeholder}</option>
      {(items ?? []).map((it) => (
        <option key={it.id} value={it.id}>
          {renderItem ? renderItem(it) : it.name}
        </option>
      ))}
    </select>
  );
}

// AwsCloudParamsFormStub — auto-discovery for AWS lands in a follow-up.
// Until then operators on AWS get the same JSON-textarea fallback they
// had pre-this-PR, with up-front validation so the worker can't
// fail-on-empty-payload.
function AwsCloudParamsFormStub({
  value, onChange,
}: {
  value: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
}) {
  const set = (patch: Record<string, unknown>) => onChange({ ...value, ...patch });
  const sgs = (value.security_group_ids as string[] | undefined)?.join(',') ?? '';
  return (
    <div className="bg-slate-50 border border-border rounded-md p-3 space-y-3">
      <div className="text-[11px] uppercase tracking-wide text-ink-mute font-medium flex items-center justify-between">
        <span>AWS deployment target</span>
        <span className="normal-case text-amber-700">auto-discovery for AWS lands in a follow-up</span>
      </div>
      <div className="grid grid-cols-2 gap-3">
        <Field label="AMI id">
          <input value={(value.ami_id as string) ?? ''} onChange={(e) => set({ ami_id: e.target.value })}
            placeholder="ami-0abcd1234efgh"
            className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30" />
        </Field>
        <Field label="Instance type">
          <input value={(value.instance_type as string) ?? ''} onChange={(e) => set({ instance_type: e.target.value })}
            placeholder="t3.small"
            className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30" />
        </Field>
      </div>
      <Field label="Subnet id">
        <input value={(value.subnet_id as string) ?? ''} onChange={(e) => set({ subnet_id: e.target.value })}
          placeholder="subnet-0abc1234"
          className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30" />
      </Field>
      <Field label="Security group ids" hint="Comma-separated list of sg-… ids.">
        <input value={sgs}
          onChange={(e) => set({ security_group_ids: e.target.value.split(',').map((s) => s.trim()).filter(Boolean) })}
          placeholder="sg-0abc1234,sg-0def5678"
          className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30" />
      </Field>
    </div>
  );
}

function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <div>
      <div className="text-[11px] uppercase tracking-wide text-ink-mute font-medium mb-1">{label}</div>
      {children}
      {hint && <div className="text-[11px] text-ink-mute mt-1">{hint}</div>}
    </div>
  );
}
