import { useQuery } from '@tanstack/react-query';
import { adminApiClient } from '@/lib/api';
import { unwrapApiData } from '@/lib/api-response';

export interface RoutingCoverage { routing_group_id: number; routing_group_key?: string; grants_access: boolean }
export interface SubscriptionContract {
  digest: string;
  coverage_mode: string;
  coverage: RoutingCoverage[];
  quota_policy: { id: number; version: string; rate_multiplier: number; daily_limit_usd: number | null; weekly_limit_usd: number | null; monthly_limit_usd: number | null };
}
export function ContractSummary({ contract }: { contract?: SubscriptionContract | null }) {
  if (!contract) return <p className="text-xs text-muted-foreground">旧版合同：覆盖已授权组，不授予访问权；额度策略随原配置生效。</p>;
  return <div className="space-y-1 text-xs text-muted-foreground">
    <p>固定额度策略 #{contract.quota_policy.id} · 消耗倍率 {contract.quota_policy.rate_multiplier}× · 各覆盖组共享同一额度窗口</p>
    <p>{contract.coverage.map((g) => `${g.routing_group_key || `#${g.routing_group_id}`}（${g.grants_access ? '含访问权' : '仅费用覆盖'}）`).join('、')}</p>
    <p>路由价格按请求时价格计算，额度消耗倍率独立于路由价格。</p>
  </div>;
}
export function CoverageEditor({ value, onChange }: { value: RoutingCoverage[]; onChange: (value: RoutingCoverage[]) => void }) {
  const groups = useQuery({ queryKey: ['subscription-coverage-groups'], queryFn: async () => {
    const rows: { id: number; key: string; display_name: string }[] = [];
    let token = '';
    const seen = new Set<string>();
    do {
      const res = await adminApiClient.get('/v1/admin/routing-groups', { params: { page_size: 200, page_token: token, filter: 'status = "enabled"' } });
      const page = unwrapApiData<{ groups: typeof rows; next_page_token: string }>(res.data);
      rows.push(...page.groups);
      token = page.next_page_token;
      if (token && seen.has(token)) throw new Error('分组分页重复');
      seen.add(token);
    } while (token);
    return rows;
  } });
  return <fieldset className="space-y-2 rounded-md border p-3"><legend className="text-sm">覆盖路由组</legend>
    <p className="text-xs text-muted-foreground">新合同至少选择一组。仅费用覆盖不授予访问权；不可覆盖仅钱包组。</p>
    {groups.isPending ? <p>加载中…</p> : groups.isError ? <p role="alert">加载失败，请重新打开表单。</p> : groups.data?.map((g) => {
      const selected = value.find((v) => v.routing_group_id === g.id);
      return <div key={g.id} className="flex flex-wrap justify-between gap-2 text-sm">
        <label><input type="checkbox" checked={!!selected} onChange={(e) => onChange(e.target.checked ? [...value, { routing_group_id: g.id, routing_group_key: g.key, grants_access: false }] : value.filter((v) => v.routing_group_id !== g.id))} /> {g.display_name || g.key} ({g.key})</label>
        {selected && <label><input type="checkbox" checked={selected.grants_access} onChange={(e) => onChange(value.map((v) => v.routing_group_id === g.id ? { ...v, grants_access: e.target.checked } : v))} /> 授予访问权</label>}
      </div>;
    })}
    {value.filter((v) => !groups.data?.some((g) => g.id === v.routing_group_id)).map((v) => <div key={v.routing_group_id} className="text-sm">{v.routing_group_key || `#${v.routing_group_id}`}（当前不可选） <button type="button" onClick={() => onChange(value.filter((g) => g.routing_group_id !== v.routing_group_id))}>移除</button></div>)}
  </fieldset>;
}
