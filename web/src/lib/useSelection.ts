import { useCallback, useMemo, useState } from 'react';

// useSelection tracks a set of row keys for bulk actions on a list page.
// The key is whatever uniquely identifies a row (`${name}@${host}` for
// daimons, the finding id for findings, the node id for nodes).
//
// Pages wire up:
//   const sel = useSelection<string>();
//   <Row checked={sel.isSelected(key)} onChange={() => sel.toggle(key)} />
//   <BulkActionBar count={sel.count} onClear={sel.clear}> ... </BulkActionBar>
//
// On bulk action, fan out via Promise.allSettled over `sel.all`. The hook
// stays unaware of the action vocabulary so the same primitive serves
// daimons, nodes, and findings without forking.
export function useSelection<K extends string>(initial?: K[]) {
  const [selected, setSelected] = useState<Set<K>>(() => new Set(initial ?? []));

  const toggle = useCallback((key: K) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key); else next.add(key);
      return next;
    });
  }, []);

  const set = useCallback((key: K, on: boolean) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (on) next.add(key); else next.delete(key);
      return next;
    });
  }, []);

  const setMany = useCallback((keys: K[], on: boolean) => {
    setSelected((prev) => {
      const next = new Set(prev);
      for (const k of keys) {
        if (on) next.add(k); else next.delete(k);
      }
      return next;
    });
  }, []);

  const clear = useCallback(() => setSelected(new Set()), []);

  const isSelected = useCallback((key: K) => selected.has(key), [selected]);

  const all = useMemo(() => Array.from(selected), [selected]);
  const count = selected.size;

  return { selected, count, all, toggle, set, setMany, clear, isSelected };
}
