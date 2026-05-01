// Returns the first 16 hex chars of sha256(input). Mirrors the Go
// helper at controlplane/orchestrator/prompt_entities.go so the
// client and server agree on every hash for matching server-emitted
// PromptEntityRef.literal_hash against client-tokenized JSON
// segments.
//
// Uses the Web Crypto SubtleCrypto API; available in all modern
// browsers and the Vitest happy-dom environment.

export async function literalHash(input: string): Promise<string> {
  const enc = new TextEncoder().encode(input);
  const buf = await crypto.subtle.digest('SHA-256', enc);
  const hex = Array.from(new Uint8Array(buf))
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('');
  return hex.slice(0, 16);
}
