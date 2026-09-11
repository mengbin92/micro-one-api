import { apiClient } from './api';
import { unwrapApiData } from './api-response';
export interface RoutingGrant { routing_group_id: number; source_type: string; source_ref: string; starts_at: number; expires_at: number; status: string }
export interface RoutingFacts { tokens?: { id: number; name: string; mode: string; group_id: number; revision: number }[]; default_routing_group_id: number; revision: number; public_group_access: string; grants: RoutingGrant[] }
export interface AvailableGroup { id: number; key: string; display_name: string; price_ratio: number; price_source: string; price_version: string; billing_mode: string; subscription_covered: boolean; models: string[]; sources: RoutingGrant[] }
export interface AvailableGroups { groups: AvailableGroup[]; facts: RoutingFacts; default_available: boolean; creation_enabled: boolean; next_page_token: string }
export async function loadAvailableGroups(): Promise<AvailableGroups> {
  let token = '';
  const groups: AvailableGroup[] = [];
  const seen = new Set<string>();
  for (;;) {
    const res = await apiClient.get('/v1/routing-groups/available', { params: { page_size: 200, page_token: token } });
    const page = unwrapApiData<AvailableGroups>(res.data);
    if (!Array.isArray(page.groups) || !page.facts) throw new Error('可用分组暂不可用');
    groups.push(...page.groups);
    if (!page.next_page_token) return { ...page, groups };
    if (seen.has(page.next_page_token)) throw new Error('分组分页重复');
    seen.add(page.next_page_token);
    token = page.next_page_token;
  }
}
