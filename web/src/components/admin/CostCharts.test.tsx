import { describe, expect, it } from 'vitest';
import { getPieLabelLayouts } from './CostCharts';

const chart = { cx: 100, cy: 100, outerRadius: 60 } as const;

describe('getPieLabelLayouts', () => {
  it('keeps labels on each side separated and inside the chart bounds', () => {
    const layouts = getPieLabelLayouts(
      Array.from({ length: 10 }, (_, index) => ({
        name: `model-${index}`,
        value: 1,
        color: '#000',
      })),
      chart,
    );

    expect(layouts).toHaveLength(10);

    for (const side of ['left', 'right'] as const) {
      const ys = layouts
        .filter((layout) => layout.side === side)
        .map((layout) => layout.y)
        .sort((a, b) => a - b);

      expect(ys.every((y) => y >= 20 && y <= 180)).toBe(true);
      for (let index = 1; index < ys.length; index += 1) {
        expect(ys[index] - ys[index - 1]).toBeGreaterThanOrEqual(22);
      }
    }
  });

  it('returns the deterministic fallback when all values are zero', () => {
    const layouts = getPieLabelLayouts(
      [
        { name: 'empty-a', value: 0, color: '#000' },
        { name: 'empty-b', value: -1, color: '#000' },
      ],
      chart,
    );

    expect(layouts).toEqual([
      { x: 0, y: 0, textAnchor: 'start', side: 'right' },
      { x: 0, y: 0, textAnchor: 'start', side: 'right' },
    ]);
  });
});
