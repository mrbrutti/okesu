// web/src/components/investigations/CaseGraph.tsx
//
// Bipartite relationship view for an investigation. Findings on
// the left, hosts/daimons/IOCs on the right; edges where a finding
// touches an entity. Uses @xyflow/react (already a dep — used by
// orchestration run canvas).
//
// Click affordances reuse the SmartPayload `entity:open` event bus
// + EntityDrawerHost (PR #82). Hosts have no drawer — clicking
// navigates to /findings filtered by host instead.
import { useEffect, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ReactFlow, Background, BackgroundVariant, Controls, MiniMap, type Edge, type Node } from '@xyflow/react';
import { nodeTypes } from './graph/nodes';
import { styleForEdgeWeight, weightByTarget } from './graph/edges';
import '@xyflow/react/dist/style.css';
import { api, type GraphNode, type GraphResponse } from '../../api';
import { computePositions } from './graph/layout';

interface Props {
  investigationID: number;
  cpInstanceID?: string;
  bundleFindingsCount: number;
}

export function CaseGraph({ investigationID, cpInstanceID, bundleFindingsCount }: Props) {
  const [data, setData] = useState<GraphResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const navigate = useNavigate();

  useEffect(() => {
    if (bundleFindingsCount === 0) {
      setData(null);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError(null);
    api.investigations
      .graph(investigationID, { cpInstanceID })
      .then((resp) => {
        if (!cancelled) {
          setData(resp);
          setLoading(false);
        }
      })
      .catch((e) => {
        if (!cancelled) {
          setError(e instanceof Error ? e.message : String(e));
          setLoading(false);
        }
      });
    return () => { cancelled = true; };
  }, [investigationID, cpInstanceID, bundleFindingsCount]);

  const { rfNodes, rfEdges } = useMemo(() => {
    if (!data) return { rfNodes: [] as Node[], rfEdges: [] as Edge[] };
    const positions = computePositions(data.nodes, data.edges);
    const weights = weightByTarget(data.edges);

    const rfNodes: Node[] = data.nodes.map((n) => {
      const pos = positions.get(n.id) ?? { x: 0, y: 0 };
      return {
        id: n.id,
        type: n.kind,
        position: pos,
        data: { ...nodeDataFor(n), graphNode: n },
        draggable: false,
        selectable: true,
      };
    });

    const rfEdges: Edge[] = data.edges.map((e) => {
      const style = styleForEdgeWeight(weights.get(e.target) ?? 1);
      return {
        id: e.id,
        source: e.source,
        target: e.target,
        type: 'smoothstep',
        style: { stroke: style.stroke, strokeWidth: style.strokeWidth, opacity: style.opacity },
        animated: false,
      };
    });
    return { rfNodes, rfEdges };
  }, [data]);

  function handleNodeClick(_e: React.MouseEvent, node: Node) {
    const g = node.data?.graphNode as GraphNode | undefined;
    if (!g) return;
    if (g.kind === 'finding') {
      const id = g.id.slice(2); // strip "f:"
      window.dispatchEvent(new CustomEvent('entity:open', { detail: { kind: 'finding', identityKey: id, cpInstanceID } }));
    } else if (g.kind === 'daimon') {
      const name = g.id.slice(2);
      window.dispatchEvent(new CustomEvent('entity:open', { detail: { kind: 'daimon', identityKey: name, cpInstanceID } }));
    } else if (g.kind === 'ioc') {
      const key = g.id.slice(2); // strip "i:"
      window.dispatchEvent(new CustomEvent('entity:open', { detail: { kind: 'ioc', identityKey: key, cpInstanceID } }));
    } else if (g.kind === 'host') {
      const host = g.id.slice(2);
      const cpQS = cpInstanceID ? `&cp=${encodeURIComponent(cpInstanceID)}` : '';
      navigate(`/findings?host=${encodeURIComponent(host)}${cpQS}`);
    }
  }

  if (bundleFindingsCount === 0) {
    return (
      <div className="border border-border rounded-md bg-white p-8 text-center text-sm text-ink-mute italic">
        No findings linked yet — link findings on the Findings tab to see relationships.
      </div>
    );
  }

  return (
    <div className="border border-border rounded-md bg-white" style={{ minHeight: 600 }}>
      {data && data.total_findings > data.limit_applied && (
        <div className="px-3 py-2 border-b border-border text-xs text-ink-dim bg-amber-50">
          {data.limit_applied} of {data.total_findings} findings shown — sorted by severity. Use the Findings tab for finer control.
        </div>
      )}
      {error && (
        <div className="px-3 py-2 border-b border-border text-xs text-red-700 bg-red-50">
          Couldn't load graph data: {error}{' '}
          <button
            type="button"
            onClick={() => {
              setError(null);
              setData(null);
              setLoading(true);
              api.investigations.graph(investigationID, { cpInstanceID })
                .then(setData).catch((e) => setError(String(e)))
                .finally(() => setLoading(false));
            }}
            className="ml-2 underline"
          >
            Retry
          </button>
        </div>
      )}
      {loading && !data && (
        <div className="p-8 text-center text-sm text-ink-mute italic">Loading graph…</div>
      )}
      {data && (
        <div
          style={{
            height: 600,
            background: 'linear-gradient(to bottom, #faf8ff, #ffffff), radial-gradient(circle at 8px 8px, #e9d5ff 1px, transparent 1px) 0 0 / 16px 16px',
          }}
        >
          <ReactFlow
            nodes={rfNodes}
            edges={rfEdges}
            nodeTypes={nodeTypes}
            onNodeClick={handleNodeClick}
            nodesDraggable={false}
            nodesConnectable={false}
            elementsSelectable
            fitView
            fitViewOptions={{ padding: 0.15 }}
            proOptions={{ hideAttribution: true }}
          >
            <Background variant={BackgroundVariant.Dots} gap={16} size={1} />
            <Controls showInteractive={false} />
            {data.nodes.filter((n) => n.kind === 'finding').length >= 10 && (
              <MiniMap pannable zoomable nodeStrokeWidth={2} />
            )}
          </ReactFlow>
        </div>
      )}
    </div>
  );
}

function nodeDataFor(n: GraphNode) {
  switch (n.kind) {
    case 'finding': return {
      id: n.id,
      numericID: Number(n.id.slice(2)),
      title: n.label,
      severity: n.severity ?? 'INFO',
      host: n.host ?? '',
      agent: n.agent ?? '',
    };
    case 'host':   return { label: n.label, finding_count: n.finding_count };
    case 'daimon': return { label: n.label, finding_count: n.finding_count };
    case 'ioc':    return {
      ioc_kind: n.ioc_kind ?? '',
      ioc_value: n.ioc_value ?? n.label,
      obs_count: n.obs_count,
      host_count: n.host_count,
    };
    default: return { label: n.label };
  }
}
