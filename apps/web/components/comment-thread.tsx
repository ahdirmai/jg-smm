'use client';

import { useMemo } from 'react';
import { CornerDownRight } from 'lucide-react';
import type { ScrapedComment } from '@/lib/api';
import { SentimentBadge } from '@/components/sentiment';

/**
 * Scraped comments, threaded by parentId.
 *
 * Why nest at all: a flat list of a busy thread is unreadable — a reply reads as
 * a non-sequitur next to comments it does not answer. The API supplies
 * `parentId`, so the tree is built here rather than stored: one source of truth
 * (the rows), no denormalised copy to go stale.
 *
 * Two failure modes are handled as ordinary cases, because both are expected:
 *   • `parentId` pointing at a comment this page did not receive (the parent
 *     fell outside the page limit). The reply is rendered TOP-LEVEL rather than
 *     dropped: it is real content, and hiding it would understate the thread.
 *   • a cycle (a → b → a). Impossible from the platform, but a malformed row
 *     must not hang the render, so the walk is depth-bounded.
 */
const MAX_DEPTH = 6;

interface Node {
  comment: ScrapedComment;
  children: Node[];
}

/** Build the forest. Ids seen as children are tracked so a cycle cannot recurse
 *  forever, and an orphan is promoted to the root instead of vanishing. */
function buildForest(comments: ScrapedComment[]): Node[] {
  const byId = new Map<string, Node>();
  for (const c of comments) byId.set(c.id, { comment: c, children: [] });

  const roots: Node[] = [];
  const attached = new Set<string>();

  for (const c of comments) {
    const node = byId.get(c.id);
    if (!node) continue;
    const parent = c.parentId ? byId.get(c.parentId) : undefined;
    // Orphan (parent not in this page) or no parent → a root.
    if (!parent || parent === node) {
      roots.push(node);
      continue;
    }
    parent.children.push(node);
    attached.add(c.id);
  }

  // A cycle would leave every member attached with no root, so nothing would
  // render. Promote any comment that never attached and is not already a root.
  if (roots.length === 0 && comments.length > 0) {
    for (const c of comments) {
      if (!attached.has(c.id)) {
        const node = byId.get(c.id);
        if (node) roots.push(node);
      }
    }
  }
  return roots;
}

function CommentRow({
  node,
  depth,
  selectable,
  selectedId,
  onSelect,
}: {
  node: Node;
  depth: number;
  selectable: boolean;
  // `| undefined` is explicit because the repo compiles with
  // exactOptionalPropertyTypes: an optional prop does NOT accept an explicit
  // undefined, and CommentThread passes these through unconditionally.
  selectedId?: string | null | undefined;
  onSelect?: ((c: ScrapedComment) => void) | undefined;
}) {
  const { comment, children } = node;
  const selected = selectedId === comment.id;
  // A reply needs an addressable target. Keep the row visible (hiding a parent
  // would strand its replies) but make the missing permalink explicit.
  const disabled = selectable && !comment.url;

  return (
    <li className={depth > 0 ? 'border-l border-border/60 pl-2.5' : undefined}>
      <div
        role={selectable ? 'button' : undefined}
        tabIndex={selectable && !disabled ? 0 : undefined}
        aria-disabled={disabled || undefined}
        aria-pressed={selectable ? selected : undefined}
        title={disabled ? 'This comment has no permalink. Paste its link instead.' : undefined}
        onClick={disabled ? undefined : selectable ? () => onSelect?.(comment) : undefined}
        onKeyDown={
          selectable && !disabled
            ? (e) => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault();
                  onSelect?.(comment);
                }
              }
            : undefined
        }
        className={`rounded-md px-2 py-1.5 text-xs transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring ${
          disabled
            ? 'cursor-not-allowed opacity-55'
            : selectable
              ? 'cursor-pointer hover:bg-secondary/60'
              : ''
        } ${selected ? 'bg-secondary text-foreground' : ''}`}
      >
        <div className="flex items-start gap-1.5">
          {depth > 0 ? (
            <CornerDownRight className="mt-0.5 size-3 shrink-0 text-muted-foreground/60" aria-hidden />
          ) : null}
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-1.5">
              <span className="truncate font-medium text-foreground">@{comment.authorHandle}</span>
              <SentimentBadge sentiment={comment.sentiment} />
            </div>
            <p className="mt-0.5 whitespace-pre-wrap break-words text-muted-foreground">
              {comment.text}
            </p>
          </div>
        </div>
      </div>

      {children.length > 0 && depth < MAX_DEPTH ? (
        <ul className="mt-0.5 space-y-0.5">
          {children.map((child) => (
            <CommentRow
              key={child.comment.id}
              node={child}
              depth={depth + 1}
              selectable={selectable}
              selectedId={selectedId}
              onSelect={onSelect}
            />
          ))}
        </ul>
      ) : children.length > 0 ? (
        // Depth cap reached: say so rather than silently swallowing the tail.
        <p className="px-2 py-1 text-[10px] text-muted-foreground">
          {children.length} further {children.length === 1 ? 'reply' : 'replies'} not shown
        </p>
      ) : null}
    </li>
  );
}

export function CommentThread({
  comments,
  selectable = false,
  selectedId,
  onSelect,
}: {
  comments: ScrapedComment[];
  /** When true, rows are clickable (the Reply flow picks its target this way). */
  selectable?: boolean | undefined;
  selectedId?: string | null | undefined;
  onSelect?: ((c: ScrapedComment) => void) | undefined;
}) {
  const forest = useMemo(() => buildForest(comments), [comments]);
  if (comments.length === 0) return null;

  return (
    <ul className="space-y-1">
      {forest.map((node) => (
        <CommentRow
          key={node.comment.id}
          node={node}
          depth={0}
          selectable={selectable}
          selectedId={selectedId}
          onSelect={onSelect}
        />
      ))}
    </ul>
  );
}
