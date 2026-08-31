import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { UsageAuditPanel, UsageSummaryCell } from './UsageAuditPanel';

// §9.1 / F25 display contract: the admin detail view must distinguish
// canonical, legacy and ambiguous rows without fabricating values.

const verifiedLog = {
  usageParseStatus: 'verified',
  usageSemantics: 'anthropic_exclusive',
  usageProtocol: 'anthropic_messages',
  usageContractVersion: 1,
  uncachedInputTokens: 130,
  cacheReadTokens: 45056,
  cacheCreation5mTokens: 0,
  cacheCreation1hTokens: 0,
  completionTokens: 9,
  promptTokens: 130,
  reportedPromptTokens: 130,
  reportedTotalTokens: 0,
  billableTotalTokens: 45195,
  promptCost: 1300,
  completionCost: 18,
  cacheReadCost: 4505,
  cacheCreation5mCost: 0,
  cacheCreation1hCost: 0,
  pricingConfigHash: 'hash-abc',
  pricingSnapshot: {
    configHash: 'hash-abc',
    modelName: 'glm-5.3',
    inputPrice: 0.001,
    outputPrice: 0.002,
    cacheReadPrice: 0.0001,
    cacheCreation5mPrice: 0,
    cacheCreation1hPrice: 0,
    groupRatio: 1,
    cacheCreationMode: 'charge',
    snapshotVersion: 1,
  },
};

describe('UsageAuditPanel', () => {
  it('renders the five canonical buckets with snapshot unit prices and the hash', () => {
    render(<UsageAuditPanel log={verifiedLog} />);

    expect(screen.getByText('非缓存输入')).toBeInTheDocument();
    expect(screen.getByText('缓存读取')).toBeInTheDocument();
    expect(screen.getByText('缓存创建 5m')).toBeInTheDocument();
    expect(screen.getByText('缓存创建 1h')).toBeInTheDocument();
    // billable total includes every cache bucket
    expect(screen.getByText('45,195')).toBeInTheDocument();
    // frozen unit prices from the pricing snapshot, not derived from amounts
    expect(screen.getByText('$0.001')).toBeInTheDocument();
    expect(screen.getByText('$0.0001')).toBeInTheDocument();
    expect(screen.getByText('hash-abc')).toBeInTheDocument();
    expect(screen.getByText('charge')).toBeInTheDocument();
  });

  it('labels legacy rows and never fabricates an uncached bucket', () => {
    render(
      <UsageAuditPanel
        log={{
          usageParseStatus: 'legacy',
          promptTokens: 55619,
          completionTokens: 261,
          cacheReadTokens: 7232,
          reportedPromptTokens: 55619,
          reportedTotalTokens: 55880,
        }}
      />,
    );

    expect(screen.getByText('历史口径')).toBeInTheDocument();
    expect(screen.queryByText('非缓存输入')).not.toBeInTheDocument();
    expect(screen.getByText('上报输入 Token')).toBeInTheDocument();
    expect(screen.getAllByText('55,880').length).toBeGreaterThan(0);
  });

  it('shows both candidate costs for ambiguous rows', () => {
    render(
      <UsageAuditPanel
        log={{
          usageParseStatus: 'ambiguous',
          usageDecisionReason: 'cached_exceeds_reported_prompt',
          uncachedInputTokens: 0,
          cacheReadTokens: 45056,
          completionTokens: 9,
          billableTotalTokens: 45065,
          subsetCandidateCost: 4523,
          exclusiveCandidateCost: 4653,
        }}
      />,
    );

    expect(screen.getByText('ambiguous')).toBeInTheDocument();
    expect(screen.getByText(/cached_exceeds_reported_prompt/)).toBeInTheDocument();
    expect(screen.getByText(/subset 候选成本/)).toBeInTheDocument();
    expect(screen.getByText(/exclusive 候选成本/)).toBeInTheDocument();
  });
});

describe('UsageSummaryCell', () => {
  it('falls back to reported fields for legacy rows', () => {
    render(
      <UsageSummaryCell
        log={{ usageParseStatus: 'legacy', promptTokens: 1000, completionTokens: 100, reportedTotalTokens: 1100 }}
      />,
    );

    expect(screen.getByText('历史')).toBeInTheDocument();
    expect(screen.getByText('Σ 1,100')).toBeInTheDocument();
  });

  it('derives the legacy total from the raw buckets when no reported total exists (pre-085 rows)', () => {
    render(
      <UsageSummaryCell
        log={{ promptTokens: 500, completionTokens: 50, cacheReadTokens: 200, quota: 750 }}
      />,
    );

    // §9.1: quota keeps the legacy reported-total meaning; the sum must never
    // display 0 for a row that clearly has usage.
    expect(screen.getByText('Σ 750')).toBeInTheDocument();
  });

  it('marks ambiguous rows in the list', () => {
    render(
      <UsageSummaryCell
        log={{ usageParseStatus: 'ambiguous', uncachedInputTokens: 10, completionTokens: 5, billableTotalTokens: 15 }}
      />,
    );

    expect(screen.getByText('存疑')).toBeInTheDocument();
  });
});
