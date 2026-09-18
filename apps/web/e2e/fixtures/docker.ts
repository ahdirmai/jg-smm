import { execSync } from 'node:child_process';

/**
 * Docker helper. The workers spec asserts on real provisioning end-to-end, so
 * it has to read the daemon directly — the API says a container is READY, this
 * proves the container actually exists and carries the friendly name.
 *
 * Every call is tolerant of a missing daemon: a spec that runs without docker
 * installed should skip its assertion, not crash the runner.
 */
export function listWorkerContainers(): { name: string; status: string }[] {
  try {
    const out = execSync("docker ps -a --format '{{.Names}}\t{{.Status}}'", {
      encoding: 'utf8',
      timeout: 10_000,
    });
    return out
      .trim()
      .split('\n')
      .filter((line) => line.startsWith('smm-worker-'))
      .map((line) => {
        const [name, status] = line.split('\t');
        return { name: name ?? '', status: status ?? '' };
      })
      .filter((c) => c.name !== '');
  } catch {
    return [];
  }
}

export function containerExists(name: string): boolean {
  return listWorkerContainers().some((c) => c.name === name);
}

/** True when a docker daemon answers `docker ps` at all. */
export function dockerAvailable(): boolean {
  try {
    execSync('docker ps --format "{{.Names}}"', { stdio: 'ignore', timeout: 5_000 });
    return true;
  } catch {
    return false;
  }
}

/**
 * The name the docker driver builds from a worker row: slug(name) + short id.
 * Mirrors apps/api/internal/adapter/dockerprovisioner.workerContainerName so a
 * spec can predict what `docker ps` should show.
 */
export function expectedContainerName(workerName: string, workerId: string): string {
  const slug = workerName
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');
  return `smm-worker-${slug || 'unnamed'}-${workerId.slice(0, 12)}`;
}
