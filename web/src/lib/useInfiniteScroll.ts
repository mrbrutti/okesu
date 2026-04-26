import { useCallback, useEffect, useRef, useState } from 'react';

// useInfiniteScroll wires the consumer's bottom-of-list sentinel to a
// scroll-event listener on the nearest scrollable ancestor. When the
// sentinel is within `threshold` pixels of the visible bottom of that
// ancestor (or of the window if the page itself scrolls), and `hasMore`
// is true, and we aren't already loading, it calls `loadMore`.
//
// Why scroll listener instead of IntersectionObserver:
//   - IO behaviour is unreliable for inner scroll containers when `root`
//     is null and the sentinel sits inside an overflow:auto ancestor —
//     ancestor scroll doesn't always re-trigger callbacks.
//   - Setting `root` to the scroll ancestor works in theory, but quietly
//     breaks if the consumer's scroll element is several layers up or
//     the layout shifts (we found that the dashboard's outer <main> AND
//     the page's inner <div> both have overflow-auto).
//   - getBoundingClientRect + a scroll listener is dumb but works the
//     same way every render in every browser. The added overhead is
//     negligible for the small number of mounted sentinels.
//
// Usage:
//
//   const { sentinelRef, loading } = useInfiniteScroll({
//     hasMore: items.length < total,
//     loadMore: async () => {
//       const next = await api.list({ offset: items.length, limit: 50 });
//       setItems((prev) => [...prev, ...next]);
//     },
//   });
//
//   {items.length > 0 && (
//     <div ref={sentinelRef}>{loading ? 'loading…' : 'scroll for more'}</div>
//   )}

function findScrollAncestor(el: Element | null): HTMLElement | null {
  let p: Element | null = el?.parentElement ?? null;
  while (p) {
    const style = getComputedStyle(p);
    const ovr = style.overflowY;
    if (ovr === 'auto' || ovr === 'scroll' || ovr === 'overlay') {
      // Only treat it as the scroller if it actually clips its content;
      // overflow:auto with content shorter than the box doesn't scroll.
      if (p.scrollHeight > p.clientHeight) {
        return p as HTMLElement;
      }
    }
    p = p.parentElement;
  }
  return null;
}

export function useInfiniteScroll({
  hasMore,
  loadMore,
  threshold = 240,
}: {
  hasMore: boolean;
  loadMore: () => Promise<unknown> | unknown;
  /** How many pixels above the bottom of the scroll viewport the
   *  sentinel must reach before triggering. Default 240px so the next
   *  page starts loading before the user actually hits the end. */
  threshold?: number;
}) {
  const [loading, setLoading] = useState(false);

  const sentinelElRef = useRef<HTMLElement | null>(null);
  const scrollerElRef = useRef<HTMLElement | null>(null);

  // Latest values exposed via refs so the scroll handler reads current
  // props without re-binding the listener on every render.
  const loadMoreRef = useRef(loadMore);
  const hasMoreRef = useRef(hasMore);
  const loadingRef = useRef(loading);
  loadMoreRef.current = loadMore;
  hasMoreRef.current = hasMore;
  loadingRef.current = loading;

  const checkAndLoad = useCallback(() => {
    if (!hasMoreRef.current || loadingRef.current) return;
    const sentinel = sentinelElRef.current;
    if (!sentinel) return;

    const sRect = sentinel.getBoundingClientRect();
    const scroller = scrollerElRef.current;

    let bottomEdge: number;
    if (scroller) {
      const cRect = scroller.getBoundingClientRect();
      bottomEdge = cRect.bottom;
    } else {
      bottomEdge = window.innerHeight || document.documentElement.clientHeight;
    }

    // Trigger when the top of the sentinel is within `threshold` pixels
    // of the bottom of the scroll viewport.
    if (sRect.top - bottomEdge < threshold) {
      // Lock synchronously to prevent re-entry from a burst of scroll
      // events firing before setState propagates back to loadingRef.
      loadingRef.current = true;
      setLoading(true);
      Promise.resolve(loadMoreRef.current())
        .catch(() => {
          /* swallow — the operator can scroll again to retry */
        })
        .finally(() => {
          loadingRef.current = false;
          setLoading(false);
          // After a load completes, the sentinel may still be in view
          // because the new content pushed it back to the bottom of the
          // viewport. Re-check on the next frame so we kick off another
          // page if so.
          requestAnimationFrame(() => checkAndLoad());
        });
    }
  }, [threshold]);

  // Attach scroll listener whenever the sentinel mounts. Re-detect the
  // scroll ancestor each attach in case layout has moved the sentinel
  // under a different scroller.
  const sentinelRef = useCallback((el: HTMLDivElement | null) => {
    // Detach old listener.
    const oldScroller = scrollerElRef.current;
    if (oldScroller) {
      oldScroller.removeEventListener('scroll', checkAndLoad);
    } else if (sentinelElRef.current) {
      // Was attached to window.
      window.removeEventListener('scroll', checkAndLoad);
    }

    sentinelElRef.current = el;
    scrollerElRef.current = null;

    if (!el) return;

    const newScroller = findScrollAncestor(el);
    scrollerElRef.current = newScroller;
    if (newScroller) {
      newScroller.addEventListener('scroll', checkAndLoad, { passive: true });
    } else {
      window.addEventListener('scroll', checkAndLoad, { passive: true });
    }
    // Trigger an immediate check: if the sentinel is already in view
    // (e.g. content shorter than the viewport), kick off a load.
    requestAnimationFrame(() => checkAndLoad());
  }, [checkAndLoad]);

  // Re-check whenever the consumer's data changes — the sentinel may have
  // entered view because new items below it weren't yet rendered.
  useEffect(() => {
    if (hasMore && sentinelElRef.current) {
      requestAnimationFrame(() => checkAndLoad());
    }
  }, [hasMore, checkAndLoad]);

  // Tear down on unmount.
  useEffect(() => {
    return () => {
      const scroller = scrollerElRef.current;
      if (scroller) {
        scroller.removeEventListener('scroll', checkAndLoad);
      } else if (sentinelElRef.current) {
        window.removeEventListener('scroll', checkAndLoad);
      }
      sentinelElRef.current = null;
      scrollerElRef.current = null;
    };
  }, [checkAndLoad]);

  return { sentinelRef, loading };
}
