/**
 * Minimal structured JSON logger. One line per event so the container log is
 * machine-parseable. NEVER log credentials, cookies or plaintext passwords
 * (enforced by the CI grep gate, DEVELOPMENT_RULE §7.6).
 */

type Level = 'debug' | 'info' | 'warn' | 'error';

const LEVEL_ORDER: Record<Level, number> = { debug: 10, info: 20, warn: 30, error: 40 };

export interface Logger {
  debug(msg: string, fields?: Record<string, unknown>): void;
  info(msg: string, fields?: Record<string, unknown>): void;
  warn(msg: string, fields?: Record<string, unknown>): void;
  error(msg: string, fields?: Record<string, unknown>): void;
  child(bindings: Record<string, unknown>): Logger;
}

function write(level: Level, min: number, bindings: Record<string, unknown>, msg: string, fields?: Record<string, unknown>): void {
  if (LEVEL_ORDER[level] < min) return;
  const line = JSON.stringify({ level, ts: new Date().toISOString(), msg, ...bindings, ...fields });
  if (level === 'error' || level === 'warn') process.stderr.write(`${line}\n`);
  else process.stdout.write(`${line}\n`);
}

export function createLogger(
  bindings: Record<string, unknown> = {},
  minLevel: Level = 'info',
): Logger {
  const min = LEVEL_ORDER[minLevel];
  return {
    debug: (msg, fields) => write('debug', min, bindings, msg, fields),
    info: (msg, fields) => write('info', min, bindings, msg, fields),
    warn: (msg, fields) => write('warn', min, bindings, msg, fields),
    error: (msg, fields) => write('error', min, bindings, msg, fields),
    child: (extra) => createLogger({ ...bindings, ...extra }, minLevel),
  };
}
