import type { IOCRecord } from '../api';

export default function CatalogDrawer({
  ioc,
  onClose,
  onOpenFullPage,
}: {
  ioc: IOCRecord;
  onClose: () => void;
  onOpenFullPage: () => void;
}) {
  return (
    <div className="fixed inset-y-0 right-0 w-96 bg-zinc-950 border-l border-zinc-800 p-4 overflow-y-auto">
      <div className="flex items-center justify-between mb-4">
        <span className="font-mono text-xs text-zinc-500">{ioc.Kind}</span>
        <button onClick={onClose} className="text-zinc-500 hover:text-zinc-200">✕</button>
      </div>
      <div className="text-zinc-200 font-mono text-sm break-all mb-4">{ioc.Value}</div>
      <p className="text-xs text-zinc-500">Drawer body — coming in Task F.</p>
      <button
        onClick={onOpenFullPage}
        className="mt-6 w-full text-sm bg-zinc-800 hover:bg-zinc-700 text-zinc-100 py-2 rounded"
      >
        Open full page →
      </button>
    </div>
  );
}
