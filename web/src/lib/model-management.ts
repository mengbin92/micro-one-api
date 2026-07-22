// Types and API helpers for the independent model management system (方案B).
// The backend returns raw protobuf JSON (snake_case) without an envelope,
// so these helpers differ from the `unwrapApiData` pattern used elsewhere.

import { adminApiClient } from '@/lib/api';

// ── Response types ────────────────────────────────────────────────────────

export interface ModelSummary {
  id: number;
  model_id: string;
  display_name: string;
  provider: string;
  model_type: string;
  status: number; // 0=disabled, 1=enabled, 2=testing
  category: string;
  tier: string;
  is_public: boolean;
  channel_count: number;
  subscription_count: number;
}

export interface ModelInfo {
  id: number;
  model_id: string;
  display_name: string;
  description: string;
  provider: string;
  model_type: string;
  context_window: number;
  pricing_input: number;
  pricing_output: number;
  status: number;
  is_public: boolean;
  capabilities: string[];
  tags: string[];
  category: string;
  tier: string;
  metadata: string;
  created_at: number;
  updated_at: number;
  channel_count: number;
  subscription_count: number;
}

export interface ModelAlias {
  id: number;
  model_pk: number;
  alias: string;
  is_primary: boolean;
  created_at: number;
}

export interface ModelChannelMapping {
  id: number;
  channel_id: number;
  model_pk: number;
  enabled: boolean;
  priority: number;
  config: string;
  created_at: number;
  updated_at: number;
}

export interface ModelSubscriptionMapping {
  id: number;
  subscription_account_id: number;
  model_pk: number;
  group_name: string;
  enabled: boolean;
  priority: number;
  created_at: number;
  updated_at: number;
}

export interface ListModelsResponse {
  models: ModelSummary[];
  total: number;
}

export interface GetModelResponse {
  model: ModelInfo;
  aliases: ModelAlias[];
  channel_mappings: ModelChannelMapping[];
  subscription_mappings: ModelSubscriptionMapping[];
}

export interface CreateModelResponse {
  success: boolean;
  message: string;
  model_pk: number;
}

export interface UpdateModelResponse {
  success: boolean;
  message: string;
}

export interface BatchModelsResponse {
  success: boolean;
  message: string;
  affected: number;
}

// ── Request payloads ──────────────────────────────────────────────────────

export interface ListModelsParams {
  page?: number;
  page_size?: number;
  keyword?: string;
  provider?: string;
  model_type?: string;
  status?: number;
  category?: string;
  tier?: string;
  public_only?: boolean;
}

export interface CreateModelPayload {
  model_id: string;
  display_name: string;
  description?: string;
  provider?: string;
  model_type?: string;
  context_window?: number;
  pricing_input?: number;
  pricing_output?: number;
  status?: number;
  is_public?: boolean;
  capabilities?: string[];
  tags?: string[];
  category?: string;
  tier?: string;
  metadata?: string;
}

export interface UpdateModelPayload {
  model_pk: number;
  display_name: string;
  description?: string;
  provider?: string;
  model_type?: string;
  context_window?: number;
  pricing_input?: number;
  pricing_output?: number;
  is_public?: boolean;
  capabilities?: string[];
  tags?: string[];
  category?: string;
  tier?: string;
  metadata?: string;
}

export interface BatchModelsPayload {
  action: 'enable' | 'disable' | 'delete';
  model_pks: number[];
}

// ── API functions ─────────────────────────────────────────────────────────

export async function listModels(params: ListModelsParams = {}): Promise<ListModelsResponse> {
  const { data } = await adminApiClient.get<ListModelsResponse>('/admin/models', { params });
  return data;
}

export async function getModel(modelPk: number): Promise<GetModelResponse> {
  const { data } = await adminApiClient.get<GetModelResponse>(`/admin/models/${modelPk}`);
  return data;
}

export async function createModel(payload: CreateModelPayload): Promise<CreateModelResponse> {
  const { data } = await adminApiClient.post<CreateModelResponse>('/admin/models', payload);
  return data;
}

export async function updateModel(payload: UpdateModelPayload): Promise<UpdateModelResponse> {
  const { data } = await adminApiClient.put<UpdateModelResponse>('/admin/models', payload);
  return data;
}

export async function deleteModel(modelPk: number): Promise<UpdateModelResponse> {
  const { data } = await adminApiClient.delete<UpdateModelResponse>(`/admin/models/${modelPk}`);
  return data;
}

export async function changeModelStatus(modelPk: number, status: number): Promise<UpdateModelResponse> {
  const { data } = await adminApiClient.patch<UpdateModelResponse>(`/admin/models/${modelPk}/status`, { status });
  return data;
}

export async function batchModels(payload: BatchModelsPayload): Promise<BatchModelsResponse> {
  const { data } = await adminApiClient.post<BatchModelsResponse>('/admin/models/batch', payload);
  return data;
}

// ── Helpers ───────────────────────────────────────────────────────────────

export const MODEL_STATUS_LABELS: Record<number, string> = {
  0: '禁用',
  1: '启用',
  2: '测试中',
};

export const MODEL_TYPE_LABELS: Record<string, string> = {
  chat: '对话',
  completion: '补全',
  embedding: '嵌入',
  image: '图像',
  audio: '音频',
};

export const MODEL_TIER_LABELS: Record<string, string> = {
  entry: '入门',
  standard: '标准',
  premium: '高级',
};

export function statusBadgeClass(status: number): string {
  if (status === 0) return 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200';
  if (status === 2) return 'bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200';
  return 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200';
}

export function formatPricing(price: number): string {
  if (!price || price === 0) return '—';
  return `$${price.toFixed(4)}/1K`;
}

export function formatContextWindow(window: number): string {
  if (!window || window === 0) return '—';
  if (window >= 1000) return `${Math.round(window / 1000)}K`;
  return String(window);
}
