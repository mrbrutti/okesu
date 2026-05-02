// Tiny pub/sub for label mutations so a LabelStrip in one part of
// the page (header pill bar) and a LabelEditor in another (lower
// detail card) stay in sync without prop-drilling.
//
// Each component already pulls its own labels from
// /api/labels/{kind}/{id_or_key} on mount + on its own mutations.
// This bus closes the cross-component gap: editor mutates → strip
// receives the event and re-fetches.

import type { LabelKind } from '../../api';

export const LABELS_CHANGED_EVENT = 'okesu:labels:changed';

export interface LabelsChangedDetail {
  kind: LabelKind;
  idOrKey: string | number;
}

export function notifyLabelsChanged(kind: LabelKind, idOrKey: string | number) {
  window.dispatchEvent(
    new CustomEvent<LabelsChangedDetail>(LABELS_CHANGED_EVENT, {
      detail: { kind, idOrKey },
    }),
  );
}
