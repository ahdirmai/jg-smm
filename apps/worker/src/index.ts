import { actionQueue, controlChannel } from '@smm/shared';

/**
 * Worker skeleton. P0-09 adds Playwright, the Redis BLPOP loop, the control
 * channel subscriber and heartbeat; this entrypoint only proves the module
 * builds and reads its identity from the environment.
 */
const workerId = process.env.WORKER_ID ?? 'worker-local';

console.log(
  JSON.stringify({
    level: 'info',
    msg: 'smm-worker: skeleton',
    workerId,
    control: controlChannel(workerId),
    queue: actionQueue(workerId),
  }),
);
