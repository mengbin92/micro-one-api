import { adminApiClient, apiClient } from '@/lib/api';
import { platformContext, type IAMReply, type IAMRequest } from '@/lib/iam-types';
export const iamBase = '/v1/admin/iam';
export async function readIAM(path: string, params: Record<string, unknown> = {}, signal?: AbortSignal) {
  return (await adminApiClient.get<IAMReply>(iamBase + path, { params: { 'context.context_type': 'platform', 'context.context_key': 'platform', ...params }, signal })).data;
}
export async function writeIAM(path: string, method: 'post' | 'put' | 'patch' | 'delete', body: IAMRequest) {
  const data = { context: platformContext, ...body };
  // DELETE's protobuf query decoder accepts scalar CAS/reason fields.
  return (await adminApiClient.request<IAMReply>({ url: iamBase + path, method, ...(method === 'delete' ? { params: { ...data, context: undefined, 'context.context_type': 'platform', 'context.context_key': 'platform' } } : { data }) })).data;
}
export async function activateRoles(roleIds: string[], expectedRevision: string, reason: string) {
  return (await apiClient.put<IAMReply>('/user/session/roles', { context: platformContext, role_ids: roleIds, expected_revision: expectedRevision, reason, request_id: crypto.randomUUID() })).data;
}
export async function readAllIAM(path: string, collection: 'roles' | 'permissions' | 'audits', params: Record<string, unknown> = {}, signal?: AbortSignal) {
  const items: unknown[] = []; const seen = new Set<string>(); let token = ''; let result: IAMReply;
  do {
    result = await readIAM(path, { ...params, page_size: 200, page_token: token }, signal);
    items.push(...result[collection] ?? []);
    token = result.next_page_token ?? '';
    if (token && seen.has(token)) throw new Error('IAM 分页重复');
    seen.add(token);
  } while (token);
  return { ...result, [collection]: items, next_page_token: '' } as IAMReply;
}
