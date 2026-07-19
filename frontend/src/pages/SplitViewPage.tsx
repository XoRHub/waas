import { useRef, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router';
import { useTranslation } from 'react-i18next';
import { DesktopPane } from '@/components/DesktopPane';
import { useWorkspaces } from '@/hooks/useApi';
import type { Workspace } from '@/types';

// ---- Layout model: a binary split tree with at most MAX_PANES leaves ----

const MAX_PANES = 3;

type LeafNode = { type: 'leaf'; id: number; workspaceId: string | null };
type SplitNode = {
  type: 'split';
  id: number;
  dir: 'row' | 'col';
  ratio: number;
  a: PaneNode;
  b: PaneNode;
};
type PaneNode = LeafNode | SplitNode;

let nextId = 1;
const newLeaf = (workspaceId: string | null = null): LeafNode => ({
  type: 'leaf',
  id: nextId++,
  workspaceId,
});

function countLeaves(node: PaneNode): number {
  return node.type === 'leaf' ? 1 : countLeaves(node.a) + countLeaves(node.b);
}

// splitLeaf replaces the target leaf with a split holding it plus a fresh
// empty leaf (the user then picks a workspace in place).
function splitLeaf(node: PaneNode, leafId: number, dir: 'row' | 'col'): PaneNode {
  if (node.type === 'leaf') {
    if (node.id !== leafId) return node;
    return { type: 'split', id: nextId++, dir, ratio: 0.5, a: node, b: newLeaf() };
  }
  return { ...node, a: splitLeaf(node.a, leafId, dir), b: splitLeaf(node.b, leafId, dir) };
}

// closeLeaf removes the leaf and promotes its sibling.
function closeLeaf(node: PaneNode, leafId: number): PaneNode | null {
  if (node.type === 'leaf') {
    return node.id === leafId ? null : node;
  }
  const a = closeLeaf(node.a, leafId);
  const b = closeLeaf(node.b, leafId);
  if (a === null) return b;
  if (b === null) return a;
  return { ...node, a, b };
}

function setLeafWorkspace(node: PaneNode, leafId: number, workspaceId: string | null): PaneNode {
  if (node.type === 'leaf') {
    return node.id === leafId ? { ...node, workspaceId } : node;
  }
  return {
    ...node,
    a: setLeafWorkspace(node.a, leafId, workspaceId),
    b: setLeafWorkspace(node.b, leafId, workspaceId),
  };
}

function setSplitRatio(node: PaneNode, splitId: number, ratio: number): PaneNode {
  if (node.type === 'leaf') return node;
  if (node.id === splitId) return { ...node, ratio };
  return {
    ...node,
    a: setSplitRatio(node.a, splitId, ratio),
    b: setSplitRatio(node.b, splitId, ratio),
  };
}

// ---- Page ----

// Multi-desktop split view: 1 to 3 workspaces side by side, split
// horizontally or vertically per pane (Termius/Termix style), each divider
// draggable. Every pane keeps its own keyboard focus.
export function SplitViewPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [search] = useSearchParams();
  const [root, setRoot] = useState<PaneNode>(() => newLeaf(search.get('ws')));

  const leaves = countLeaves(root);
  const canSplit = leaves < MAX_PANES;

  const onSplit = (leafId: number, dir: 'row' | 'col') =>
    setRoot((r) => (countLeaves(r) < MAX_PANES ? splitLeaf(r, leafId, dir) : r));
  const onClose = (leafId: number) => setRoot((r) => closeLeaf(r, leafId) ?? newLeaf());
  const onPick = (leafId: number, workspaceId: string | null) =>
    setRoot((r) => setLeafWorkspace(r, leafId, workspaceId));
  const onRatio = (splitId: number, ratio: number) =>
    setRoot((r) => setSplitRatio(r, splitId, ratio));

  return (
    <div className="flex h-screen flex-col bg-black">
      <header className="flex h-9 shrink-0 items-center justify-between bg-slate-900 px-3 text-sm text-slate-200">
        <button onClick={() => navigate('/')} className="text-blue-400 hover:text-blue-300">
          ← {t('connect.back')}
        </button>
        <span className="text-xs text-slate-400">{t('splitView.hint', { max: MAX_PANES })}</span>
      </header>
      <div className="min-h-0 flex-1">
        <PaneTree
          node={root}
          canSplit={canSplit}
          onSplit={onSplit}
          onClose={onClose}
          onPick={onPick}
          onRatio={onRatio}
        />
      </div>
    </div>
  );
}

interface PaneActions {
  canSplit: boolean;
  onSplit: (leafId: number, dir: 'row' | 'col') => void;
  onClose: (leafId: number) => void;
  onPick: (leafId: number, workspaceId: string | null) => void;
  onRatio: (splitId: number, ratio: number) => void;
}

function PaneTree({ node, ...actions }: { node: PaneNode } & PaneActions) {
  if (node.type === 'leaf') {
    return <Leaf leaf={node} {...actions} />;
  }
  return <Split split={node} {...actions} />;
}

function Split({ split, ...actions }: { split: SplitNode } & PaneActions) {
  const containerRef = useRef<HTMLDivElement>(null);
  const dragRectRef = useRef<DOMRect | null>(null);
  const horizontal = split.dir === 'row';

  // Pointer capture instead of window listeners: move/up stay on the
  // divider element, so an unmount mid-drag releases everything with the
  // node — nothing to leak.
  const onDividerDown = (down: React.PointerEvent) => {
    down.preventDefault();
    const container = containerRef.current;
    if (!container) return;
    dragRectRef.current = container.getBoundingClientRect();
    down.currentTarget.setPointerCapture(down.pointerId);
  };
  const onDividerMove = (e: React.PointerEvent) => {
    const rect = dragRectRef.current;
    if (!rect || !e.currentTarget.hasPointerCapture(e.pointerId)) return;
    const ratio = horizontal
      ? (e.clientX - rect.left) / rect.width
      : (e.clientY - rect.top) / rect.height;
    actions.onRatio(split.id, Math.min(0.85, Math.max(0.15, ratio)));
  };
  const onDividerUp = (e: React.PointerEvent) => {
    if (e.currentTarget.hasPointerCapture(e.pointerId)) {
      e.currentTarget.releasePointerCapture(e.pointerId);
    }
    dragRectRef.current = null;
  };

  return (
    <div
      ref={containerRef}
      className={`flex h-full w-full ${horizontal ? 'flex-row' : 'flex-col'}`}
    >
      <div
        style={{ flexBasis: `${split.ratio * 100}%` }}
        className="min-h-0 min-w-0 shrink-0 grow-0"
      >
        <PaneTree node={split.a} {...actions} />
      </div>
      <div
        onPointerDown={onDividerDown}
        onPointerMove={onDividerMove}
        onPointerUp={onDividerUp}
        className={`shrink-0 touch-none bg-slate-700 transition-colors hover:bg-blue-500 ${
          horizontal ? 'w-1.5 cursor-col-resize' : 'h-1.5 cursor-row-resize'
        }`}
      />
      <div className="min-h-0 min-w-0 flex-1">
        <PaneTree node={split.b} {...actions} />
      </div>
    </div>
  );
}

function Leaf({ leaf, canSplit, onSplit, onClose, onPick }: { leaf: LeafNode } & PaneActions) {
  const { t } = useTranslation();

  return (
    <div className="group/pane relative h-full w-full">
      {leaf.workspaceId ? (
        <DesktopPane workspaceId={leaf.workspaceId} />
      ) : (
        <WorkspacePicker onPick={(id) => onPick(leaf.id, id)} />
      )}
      {/* Hover toolbar: split horizontally/vertically, swap workspace, close. */}
      <div className="absolute right-2 top-2 z-10 flex gap-1 rounded-md bg-slate-900/85 p-1 opacity-0 shadow transition-opacity group-hover/pane:opacity-100">
        {canSplit && (
          <>
            <ToolButton title={t('splitView.splitRight')} onClick={() => onSplit(leaf.id, 'row')}>
              ◫
            </ToolButton>
            <ToolButton title={t('splitView.splitDown')} onClick={() => onSplit(leaf.id, 'col')}>
              ⬒
            </ToolButton>
          </>
        )}
        {leaf.workspaceId && (
          <ToolButton title={t('splitView.changeWorkspace')} onClick={() => onPick(leaf.id, null)}>
            ⇄
          </ToolButton>
        )}
        <ToolButton title={t('splitView.closePane')} onClick={() => onClose(leaf.id)}>
          ✕
        </ToolButton>
      </div>
    </div>
  );
}

function ToolButton({
  title,
  onClick,
  children,
}: {
  title: string;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      title={title}
      onClick={onClick}
      className="rounded px-1.5 py-0.5 text-sm text-slate-200 hover:bg-slate-700 hover:text-white"
    >
      {children}
    </button>
  );
}

// WorkspacePicker fills an empty pane: choose among running workspaces.
function WorkspacePicker({ onPick }: { onPick: (id: string) => void }) {
  const { t } = useTranslation();
  const workspaces = useWorkspaces();
  const running = (workspaces.data?.data ?? []).filter((ws: Workspace) => ws.phase === 'Running');

  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 bg-slate-950 p-4 text-sm text-slate-300">
      <p className="text-slate-400">{t('splitView.pickWorkspace')}</p>
      {workspaces.isPending && <p>{t('app.loading')}</p>}
      {workspaces.isSuccess && running.length === 0 && <p>{t('splitView.noRunning')}</p>}
      <div className="flex max-h-64 w-full max-w-xs flex-col gap-2 overflow-y-auto">
        {running.map((ws) => (
          <button
            key={ws.id}
            onClick={() => onPick(ws.id)}
            className="rounded-md border border-slate-700 px-3 py-2 text-left hover:border-blue-500 hover:bg-slate-900"
          >
            <span className="block font-medium text-white">{ws.displayName || ws.name}</span>
            <span className="block text-xs text-slate-400">{ws.templateRef}</span>
          </button>
        ))}
      </div>
    </div>
  );
}
