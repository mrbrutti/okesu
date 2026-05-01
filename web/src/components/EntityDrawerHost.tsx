// EntityDrawerHost listens for `entity:open` events fired by
// SmartPayload chips, and opens the matching drawer in-place.
// Mounted once at the App root so the drawer overlays anything
// without prop drilling.
//
// v1: only Finding has a real in-place drawer. Other kinds rely on
// the chip's ↗ icon for navigation; the body click is a no-op.
// Future phases can add per-kind drawers here without touching the
// chips.
import { useEffect, useState } from 'react';
import { FindingDrawer } from '../pages/Findings';

interface OpenEvent {
  kind: string;
  identityKey: string;
  cpInstanceID?: string;
}

export default function EntityDrawerHost() {
  const [finding, setFinding] = useState<{ id: number; cpInstanceID?: string } | null>(null);

  useEffect(() => {
    function onOpen(e: Event) {
      const detail = (e as CustomEvent<OpenEvent>).detail;
      if (!detail) return;
      if (detail.kind === 'finding') {
        const id = parseInt(detail.identityKey, 10);
        if (Number.isFinite(id)) setFinding({ id, cpInstanceID: detail.cpInstanceID });
      }
      // Other kinds intentionally fall through — the chip's ↗ link
      // already navigates to the detail page. Per-kind drawers are
      // a future phase.
    }
    window.addEventListener('entity:open', onOpen);
    return () => window.removeEventListener('entity:open', onOpen);
  }, []);

  if (finding) {
    return (
      <FindingDrawer
        id={finding.id}
        cpInstanceID={finding.cpInstanceID}
        onClose={() => setFinding(null)}
        onChanged={() => { /* drawer's own refresh handles re-fetch */ }}
      />
    );
  }
  return null;
}
