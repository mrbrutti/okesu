// HypothesisCard — Phase 22.3 subtype renderer.
//
// Findings emitted with subtype="hypothesis" carry a structured
// attributes block: claim, confidence, how_to_test, and optional
// evidence_for / evidence_against arrays. The card surfaces these
// in the finding drawer above the standard evidence section.
//
// The data shape comes from controlplane/agents/hypothesis.md (Group C).
// Anything missing or wrong-shaped is rendered as fallback text rather
// than an error — operators should still see *something* if a daimon
// emits a partial payload.

import { Lightbulb } from 'lucide-react';
import { cn } from '../lib/cn';

export interface HypothesisAttributes {
  claim: string;
  confidence: 'low' | 'medium' | 'high';
  how_to_test: string;
  evidence_for?: { source: string; observation: string }[];
  evidence_against?: { source: string; observation: string }[];
}

// parseHypothesisAttributes extracts a HypothesisAttributes-shaped
// payload from the finding's `attributes` blob. Returns null when
// the required fields are missing — caller should fall through to
// the standard renderer.
export function parseHypothesisAttributes(
  attrs: Record<string, unknown> | undefined,
): HypothesisAttributes | null {
  if (!attrs || typeof attrs !== 'object') return null;
  const claim = typeof attrs.claim === 'string' ? attrs.claim : '';
  const confRaw = typeof attrs.confidence === 'string' ? attrs.confidence.toLowerCase() : '';
  if (!claim) return null;
  const confidence: HypothesisAttributes['confidence'] =
    confRaw === 'high' || confRaw === 'medium' || confRaw === 'low' ? confRaw : 'low';
  const howToTest = typeof attrs.how_to_test === 'string' ? attrs.how_to_test : '';
  return {
    claim,
    confidence,
    how_to_test: howToTest,
    evidence_for: parseEvidenceList(attrs.evidence_for),
    evidence_against: parseEvidenceList(attrs.evidence_against),
  };
}

function parseEvidenceList(v: unknown): { source: string; observation: string }[] | undefined {
  if (!Array.isArray(v)) return undefined;
  const out: { source: string; observation: string }[] = [];
  for (const item of v) {
    if (item && typeof item === 'object') {
      const obj = item as Record<string, unknown>;
      const source = typeof obj.source === 'string' ? obj.source : '';
      const observation = typeof obj.observation === 'string' ? obj.observation : '';
      if (observation) out.push({ source, observation });
    }
  }
  return out.length > 0 ? out : undefined;
}

export function HypothesisCard({ attrs }: { attrs: HypothesisAttributes }) {
  return (
    <section className="border border-yellow-200 rounded-md bg-yellow-50/60 p-4">
      <div className="flex items-center gap-1.5 text-[11px] uppercase tracking-wide text-yellow-800 font-semibold mb-1.5">
        <Lightbulb size={12} /> Hypothesis
      </div>
      <div className="text-sm font-medium text-ink leading-snug">{attrs.claim}</div>

      <div className="mt-2 flex items-center gap-2 text-xs">
        <span className="text-ink-mute">Confidence:</span>
        <ConfidenceChip confidence={attrs.confidence} />
      </div>

      {attrs.evidence_for && attrs.evidence_for.length > 0 && (
        <div className="mt-3">
          <div className="text-[11px] uppercase tracking-wide font-semibold text-green-800 mb-1">For</div>
          <ul className="list-disc pl-5 space-y-1 text-xs text-ink">
            {attrs.evidence_for.map((e, i) => (
              <li key={i}>
                {e.observation}
                {e.source && <span className="text-ink-mute"> ({e.source})</span>}
              </li>
            ))}
          </ul>
        </div>
      )}

      {attrs.evidence_against && attrs.evidence_against.length > 0 && (
        <div className="mt-3">
          <div className="text-[11px] uppercase tracking-wide font-semibold text-red-800 mb-1">Against</div>
          <ul className="list-disc pl-5 space-y-1 text-xs text-ink">
            {attrs.evidence_against.map((e, i) => (
              <li key={i}>
                {e.observation}
                {e.source && <span className="text-ink-mute"> ({e.source})</span>}
              </li>
            ))}
          </ul>
        </div>
      )}

      {attrs.how_to_test && (
        <div className="mt-3">
          <div className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute mb-1">How to test</div>
          <div className="text-xs text-ink whitespace-pre-wrap">{attrs.how_to_test}</div>
        </div>
      )}
    </section>
  );
}

function ConfidenceChip({ confidence }: { confidence: HypothesisAttributes['confidence'] }) {
  const styles: Record<HypothesisAttributes['confidence'], string> = {
    high:   'text-green-800 bg-green-100 ring-green-200',
    medium: 'text-yellow-800 bg-yellow-100 ring-yellow-200',
    low:    'text-slate-700 bg-slate-100 ring-slate-200',
  };
  return (
    <span
      className={cn(
        'text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded ring-1',
        styles[confidence],
      )}
    >
      {confidence}
    </span>
  );
}
