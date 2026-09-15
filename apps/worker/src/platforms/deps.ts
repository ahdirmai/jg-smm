/**
 * Boot-time adapter dependencies. The action adapters need two things the
 * PlatformAdapter interface does not carry — the screenshot directory and the
 * worker id that makes `task-<id>-<worker>.jpg` deterministic (§7.3) — so they
 * are set once here, before the action loop starts, by the composition root.
 *
 * One configured global per process is deliberate: the worker is a single
 * composition root with one identity, and threading these through every
 * adapter method would widen the interface for a constant.
 */
export interface AdapterDeps {
  workerId: string;
  screenshotDir?: string;
}

let deps: AdapterDeps = { workerId: 'worker-local' };

/** Called once at boot by index.ts. Merges so a partial config still works. */
export function configureAdapters(next: Partial<AdapterDeps>): void {
  deps = { ...deps, ...next };
}

export function adapterDeps(): AdapterDeps {
  return deps;
}
