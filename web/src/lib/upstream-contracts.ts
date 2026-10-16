import { t } from '@/lib/i18n';

export type ContractInteger = string | number;
export interface ContractScope { source_kind: string; source_id: ContractInteger; upstream_model_id: string; protocol: string }
export interface RepairReview { proposal_id: string; base_revision: string; binding_revision: ContractInteger; incident_revision: ContractInteger; source_revision: string; replay_id: string; ci_evidence_id: string; reviewer: string; key_id: string; expires_at: ContractInteger; signature: string }
export interface ContractDocument { scope: ContractScope; provider: string; adapter_build: string; parser_revision: string; usage_revision: string; api_version?: string; endpoint?: string; inbound_protocols: string[]; rule_sets: unknown[]; fixture_evidence_id: string; basis_revision: string; repair_review?: RepairReview | null }
export interface ContractSnapshot { document: ContractDocument; revision: string; binding_revision?: ContractInteger; incident_revision?: ContractInteger }
export interface ContractBinding { scope: ContractScope; current_revision: string; previous_revision: string; revision?: ContractInteger; incident_revision?: ContractInteger; status: string; gray_percent: number; started_at?: ContractInteger; complete_attempts?: ContractInteger; failures?: ContractInteger; cooldown_until?: ContractInteger }
export interface ContractList { contracts?: ContractSnapshot[] | null; binding?: ContractBinding | null }
export const contractProtocols = ['openai_chat', 'responses', 'anthropic_messages', 'gemini'] as const;
export const contractReadPermission = 'channel.upstream_contract.read';
export const contractPublishPermission = 'channel.upstream_contract.publish';
export const contractCreatePermission = 'channel.upstream_contract.create';
export const contractQuarantinePermission = 'channel.upstream_contract.quarantine';

// ProtoJSON uses decimal strings for int64/uint64. Never round a read-back CAS.
export function contractInteger(value: unknown): string {
  if (typeof value !== 'string' && typeof value !== 'number') throw new Error(t('版本或 ID 无效，请重新查询。'));
  if (typeof value === 'number' && (!Number.isSafeInteger(value) || value < 0)) throw new Error(t('版本或 ID 超出安全范围，请重新查询。'));
  const text = String(value ?? '');
  if (!/^(0|[1-9]\d*)$/.test(text) || BigInt(text) > 9223372036854775807n) throw new Error(t('版本或 ID 无效，请重新查询。'));
  return text;
}
export function exactContractScope(value: ContractScope): ContractScope {
  const id = contractInteger(value.source_id);
  if (!['channel', 'subscription'].includes(value.source_kind) || id === '0' || typeof value.upstream_model_id !== 'string' || !value.upstream_model_id || value.upstream_model_id.length > 255 || /[*?\r\n]/.test(value.upstream_model_id) || value.upstream_model_id.includes(String.fromCharCode(0)) || !contractProtocols.includes(value.protocol as typeof contractProtocols[number])) throw new Error(t('请填写精确来源、正整数 ID、上游模型和协议，不支持通配符。'));
  return { ...value, source_id: id };
}
export function sameContractScope(a: ContractScope, b: ContractScope) { return a.source_kind === b.source_kind && String(a.source_id) === String(b.source_id) && a.upstream_model_id === b.upstream_model_id && a.protocol === b.protocol; }
function record(value: unknown): value is Record<string, unknown> { return !!value && typeof value === 'object' && !Array.isArray(value); }
function safeNumbers(value: unknown): boolean {
  if (typeof value === 'number') return Number.isFinite(value) && (!Number.isInteger(value) || Number.isSafeInteger(value));
  if (Array.isArray(value)) return value.every(safeNumbers);
  return !record(value) || Object.values(value).every(safeNumbers);
}
export function parseContractDocument(text: string, scope: ContractScope): ContractDocument {
  let value: unknown;
  try { value = JSON.parse(text); } catch { throw new Error(t('候选文档必须是有效的 JSON 对象。')); }
  const required = ['provider', 'adapter_build', 'parser_revision', 'usage_revision', 'fixture_evidence_id', 'basis_revision'];
  const allowed = ['scope', ...required, 'api_version', 'endpoint', 'inbound_protocols', 'rule_sets', 'repair_review'];
  if (!record(value) || !safeNumbers(value) || Object.keys(value).some(key => !allowed.includes(key)) || required.some(key => typeof value[key] !== 'string' || !(value[key] as string).trim()) || !record(value.scope) || !Array.isArray(value.inbound_protocols) || !value.inbound_protocols.length || value.inbound_protocols.some(protocol => !contractProtocols.includes(protocol)) || !Array.isArray(value.rule_sets) || value.rule_sets.length > 3 || value.rule_sets.some(set => !record(set) || !['provider', 'source', 'model'].includes(String(set.level)) || !Array.isArray(set.rules))) throw new Error(t('候选文档格式无效，请补齐版本、供应商、独立证据和规则集；整数 ID 请使用字符串。'));
  const document = value as unknown as ContractDocument;
  if (!sameContractScope(exactContractScope(document.scope), scope)) throw new Error(t('候选文档的 scope 必须与当前查询完全一致。'));
  return { ...document, scope };
}
export function blankContractDocument(scope: ContractScope): ContractDocument { return { scope, provider: '', adapter_build: '', parser_revision: '', usage_revision: '', inbound_protocols: [scope.protocol], rule_sets: [], fixture_evidence_id: '', basis_revision: '' }; }
// Legacy jsonx marshals generated structs with omitempty; ProtoJSON emits
// zero values. Only restore the omitted CAS for an exact, unbound seed row.
export function normalizeContractList(data: ContractList, legacy: boolean, scope: ContractScope): ContractList {
  if (!legacy) return data;
  const binding = data.binding;
  const exactBinding = binding?.scope && sameContractScope(binding.scope, scope);
  const seed = exactBinding && binding.status === 'candidate' && !binding.current_revision && !binding.previous_revision;
  return {
    ...data,
    binding: binding && {
      ...binding,
      ...(seed && binding.revision === undefined ? { revision: '0' } : {}),
      ...(exactBinding ? { incident_revision: binding.incident_revision ?? '0', complete_attempts: binding.complete_attempts ?? '0', failures: binding.failures ?? '0' } : {}),
    },
    contracts: data.contracts?.map(snapshot => ({
      ...snapshot,
      binding_revision: snapshot.binding_revision ?? '0',
      incident_revision: snapshot.incident_revision ?? '0',
      document: {
        ...snapshot.document,
        rule_sets: snapshot.document.rule_sets ?? [],
        ...(snapshot.document.repair_review ? { repair_review: { ...snapshot.document.repair_review, binding_revision: snapshot.document.repair_review.binding_revision ?? '0', incident_revision: snapshot.document.repair_review.incident_revision ?? '0' } } : {}),
      },
    })),
  };
}
// Legacy JSON handlers need numeric int64 values. Refuse an unsafe conversion.
export function contractWriteBody<T>(body: T, legacy: boolean): T {
  if (!legacy) return body;
  const fields = ['source_id', 'expected_revision', 'binding_revision', 'incident_revision', 'expires_at'];
  const convert = (value: unknown): unknown => Array.isArray(value) ? value.map(convert) : record(value) ? Object.fromEntries(Object.entries(value).map(([key, item]) => {
    if (!fields.includes(key)) return [key, convert(item)];
    const number = Number(contractInteger(item));
    if (!Number.isSafeInteger(number)) throw new Error(t('当前兼容模式无法安全写入此 ID 或版本，请使用 IAM 授权会话。'));
    return [key, number];
  })) : value;
  return convert(body) as T;
}
