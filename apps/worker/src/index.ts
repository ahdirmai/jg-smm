import { actionQueue, controlChannel } from '@smm/shared';

/**
 * Worker skeleton. P0-09 replaces the keep-alive below with the real Redis BLPOP
 * loop, control-channel subscriber and heartbeat. For now the process stays
 * resident so the compose stack is stable under `--scale worker=N`.
 */
const workerId = process.env.WORKER_ID ?? 'worker-local';

console.log(
  JSON.stringify({
    level: 'info',
    msg: 'smm-worker: skeleton',
    workerId,
    control: controlChannel(workerId),
    queue: actionQueue(workerId),
    dryRun: process.env.ACTION_DRY_RUN ?? 'true',
  }),
);

// Keep-alive: emit a heartbeat log every 30s. Removed in P0-09.
const timer = setInterval(() => {
  console.log(JSON.stringify({ level: 'info', msg: 'heartbeat', workerId }));
}, 30_000);

const shutdown = (signal: string) => {
  clearInterval(timer);
  console.log(JSON.stringify({ level: 'info', msg: 'shutdown', workerId, signal }));
  process.exit(0);
};

process.on('SIGINT', () => shutdown('SIGINT'));
process.on('SIGTERM', () => shutdown('SIGTERM'));
