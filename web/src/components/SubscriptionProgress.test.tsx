import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { SubscriptionProgressCard, type SubscriptionProgressData } from './SubscriptionProgress';

const progress: SubscriptionProgressData = {
  id: 1,
  status: 'active',
  starts_at: 1,
  expires_at: 0,
  remaining_seconds: 86400,
  daily_used: { used: 1, settled: 1, frozen: 0, available: 0, limit: 0, remaining: -1, over_limit: true },
  weekly_used: null,
  monthly_used: null,
};

describe('SubscriptionProgressCard quota display', () => {
  it('uses the authoritative settled zero for both the amount and quota bar', () => {
    const { container } = render(<SubscriptionProgressCard progress={{
      ...progress,
      daily_used: { used: 38.46, settled: 0, frozen: 10, available: 490, limit: 500, remaining: 461.54 },
    }} />);

    expect(screen.getByText('$0.00 / $500.00')).toBeInTheDocument();
    expect(screen.getByText('已结算 $0.00')).toBeInTheDocument();
    expect(container.querySelector('[style]')).toHaveStyle({ width: '2%' });
  });

  it.each([
    { used: 1, frozen: 0 },
    { used: 0, frozen: 1 },
  ])('shows an exhausted zero quota with $used settled and $frozen frozen', ({ used, frozen }) => {
    const { container } = render(<SubscriptionProgressCard progress={{ ...progress, daily_used: { ...progress.daily_used!, used, settled: used, frozen } }} />);

    expect(screen.getByText('已超限')).toBeInTheDocument();
    const bar = container.querySelector('[style]');
    expect(bar).toHaveStyle({ width: '100%' });
    expect(bar).toHaveClass('bg-red-500');
  });
});
