'use client';

import { useCallback, useEffect, useRef, useState } from 'react';

import { api, type CommentTemplate, type CreateTemplateRequest, type Platform } from '../api';

export type TemplatesState = {
  templates: CommentTemplate[];
  loading: boolean;
  error: string | null;
  refresh: () => void;
  create: (body: CreateTemplateRequest) => Promise<void>;
  update: (id: string, body: CreateTemplateRequest) => Promise<void>;
  remove: (id: string) => Promise<void>;
};

/**
 * The comment pool (P3-02/P3-03). Enqueue never carries text: a comment is
 * composed from these variants and denylist-screened at dispatch, so the pool
 * is the only place comment wording is edited.
 */
export function useTemplates(platform?: Platform): TemplatesState {
  const [templates, setTemplates] = useState<CommentTemplate[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const seq = useRef(0);

  const refresh = useCallback(async () => {
    const id = ++seq.current;
    try {
      const list = await api.listTemplates(platform);
      if (id === seq.current) {
        setTemplates(list.templates ?? []);
        setError(null);
      }
    } catch (err) {
      if (id === seq.current) {
        setError(err instanceof Error ? err.message : 'Failed to load templates');
      }
    } finally {
      if (id === seq.current) setLoading(false);
    }
  }, [platform]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const create = useCallback(
    async (body: CreateTemplateRequest) => {
      await api.createTemplate(body);
      await refresh();
    },
    [refresh],
  );

  const update = useCallback(
    async (id: string, body: CreateTemplateRequest) => {
      // Optimistic: swap the row so editing feels instant; the server's copy
      // is authoritative and a refresh reconciles on failure.
      // Scoped to the edited row. This used to map over every template and
      // spread the request body across all of them, so editing one comment
      // variant painted its text, vars, weight, bannedWords and platform onto
      // the whole table. `api.updateTemplate` persists only one row, so the
      // corruption was invisible until the next refresh snapped the others
      // back — and in between the operator was shown N identical variants with
      // no way to tell which one actually changed.
      setTemplates((prev) =>
        prev.map((t) =>
          t.id === id
            ? {
                ...t,
                ...body,
                ...(body.bannedWords ? { bannedWords: body.bannedWords } : {}),
              }
            : t,
        ),
      );
      try {
        await api.updateTemplate(id, body);
      } catch (err) {
        void refresh();
        throw err;
      }
    },
    [refresh],
  );

  const remove = useCallback(
    async (id: string) => {
      setTemplates((prev) => prev.filter((t) => t.id !== id));
      try {
        await api.deleteTemplate(id);
      } catch (err) {
        void refresh();
        throw err;
      }
    },
    [refresh],
  );

  return { templates, loading, error, refresh, create, update, remove };
}
