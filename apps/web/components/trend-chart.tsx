/**
 * TrendChart (P2-08): a dependency-free SVG line + area chart for a metric
 * series. No chart library is in the dependency set, and a single-series daily
 * trend does not justify adding one.
 *
 * Accessibility: the chart is decorative — the same numbers are available in
 * the KPI cards and the table — so it is marked aria-hidden and the summary is
 * the machine-readable description next to it.
 */

'use client';

import { useId } from 'react';

import { cn } from '@smm/ui';

export type TrendPoint = { bucket: string; value: number };

const VIEW_W = 600;
const VIEW_H = 200;
const PAD_TOP = 10;
const PAD_BOTTOM = 10;

type Props = {
  points: TrendPoint[];
  className?: string;
};

export function TrendChart({ points, className }: Props) {
  // A provider metric is a gauge, so the axis is scaled to the series' own
  // range; a flat series would otherwise divide by zero.
  const min = points.length ? Math.min(...points.map((p) => p.value)) : 0;
  const max = points.length ? Math.max(...points.map((p) => p.value)) : 0;
  const span = max - min || 1;
  const x = (i: number, len: number) => (len <= 1 ? VIEW_W / 2 : (i / (len - 1)) * VIEW_W);
  const y = (v: number) =>
    VIEW_H - ((v - min) / span) * (VIEW_H - PAD_TOP - PAD_BOTTOM) - PAD_BOTTOM;

  const line = (series: TrendPoint[]) =>
    series.map((p, i) => `${x(i, series.length).toFixed(1)},${y(p.value).toFixed(1)}`).join(' ');

  const area = (series: TrendPoint[]) =>
    `${line(series)} ${x(series.length - 1, series.length).toFixed(1)},${VIEW_H} ${x(0, series.length).toFixed(1)},${VIEW_H}`;

  const gridY = [40, 80, 120, 160];
  const gradientId = useId();

  const empty = points.length === 0;

  return (
    <div
      className={cn('h-56 w-full rounded-lg border border-border/60 bg-background/40 p-2', className)}
      aria-hidden="true"
    >
      {empty ? (
        <div className="flex h-full items-center justify-center text-xs text-muted-foreground">
          No samples in this window yet.
        </div>
      ) : (
        <svg
          viewBox={`0 0 ${VIEW_W} ${VIEW_H}`}
          preserveAspectRatio="none"
          className="h-full w-full"
        >
          <defs>
            <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="hsl(var(--chart-1))" stopOpacity={0.28} />
              <stop offset="100%" stopColor="hsl(var(--chart-1))" stopOpacity={0} />
            </linearGradient>
          </defs>
          <g>
            {gridY.map((gy) => (
              <line
                key={gy}
                x1="0"
                y1={gy}
                x2={VIEW_W}
                y2={gy}
                stroke="hsl(var(--border))"
                strokeWidth={1}
              />
            ))}
          </g>
          <polygon points={area(points)} fill={`url(#${gradientId})`} />
          <polyline
            fill="none"
            stroke="hsl(var(--chart-1))"
            strokeWidth={2.5}
            strokeLinejoin="round"
            points={line(points)}
          />
        </svg>
      )}
    </div>
  );
}
