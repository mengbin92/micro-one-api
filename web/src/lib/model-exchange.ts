// Model import/export client (v0.11.0 Phase 4).
//
// Mirrors the admin HTTP endpoints:
//   GET  /api/admin/models/export        → versioned JSON document download
//   POST /api/admin/models/import        → dry-run or real import
//
// The export document is a versioned JSON payload covering models, aliases and
// channel/subscription mappings. Prices are only included when export_prices
// is true AND the caller has root role (enforced server-side). The document
// NEVER contains channel API keys or OAuth tokens.

import { adminApiClient } from '@/lib/api';

export interface ModelExportChannelMapping {
  channel_id: number;
  enabled: boolean;
  priority: number;
  config: string;
  upstream_model_id: string;
}

export interface ModelExportSubscriptionMapping {
  subscription_account_id: number;
  group_name: string;
  enabled: boolean;
  priority: number;
  upstream_model_id: string;
}

export interface ModelExportModel {
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
  aliases: { alias: string; is_primary: boolean }[];
  channel_mappings: ModelExportChannelMapping[];
  subscription_mappings: ModelExportSubscriptionMapping[];
}

export interface ModelExportDocument {
  schema_version: string;
  exported_at: string;
  content_hash: string;
  models: ModelExportModel[];
}

export interface ExportModelsParams {
  export_prices?: boolean;
  keyword?: string;
  provider?: string;
  model_type?: string;
  status?: number;
  category?: string;
  tier?: string;
}

export interface ImportModelsPayload {
  schema_version: string;
  models: ModelExportModel[];
  conflict_strategy: 'reject' | 'update';
  import_prices: boolean;
}

export interface ImportRecordResult {
  model_id: string;
  action: 'create' | 'update' | 'skip' | 'conflict' | 'error';
  message: string;
}

export interface ImportModelsResponse {
  success: boolean;
  message: string;
  created: number;
  updated: number;
  skipped: number;
  conflicts: number;
  errors: number;
  results: ImportRecordResult[];
}

export interface ImportModelsDryRunResponse {
  would_succeed: boolean;
  message: string;
  would_create: number;
  would_update: number;
  would_skip: number;
  conflicts: number;
  errors: number;
  results: ImportRecordResult[];
}

export const MODEL_EXCHANGE_SCHEMA_VERSION = '1.1.0';

/**
 * Export the model registry as a versioned JSON document and trigger a browser
 * download. Prices are only included when exportPrices is true; the server
 * additionally requires root role for price export.
 */
export async function exportModels(params: ExportModelsParams = {}): Promise<ModelExportDocument> {
  const searchParams = new URLSearchParams();
  if (params.export_prices) searchParams.set('export_prices', 'true');
  if (params.keyword) searchParams.set('keyword', params.keyword);
  if (params.provider) searchParams.set('provider', params.provider);
  if (params.model_type) searchParams.set('model_type', params.model_type);
  if (params.status) searchParams.set('status', String(params.status));
  if (params.category) searchParams.set('category', params.category);
  if (params.tier) searchParams.set('tier', params.tier);
  const { data } = await adminApiClient.get<ModelExportDocument>(
    '/admin/models/export',
    { params: Object.fromEntries(searchParams) },
  );
  return data;
}

/**
 * Dry-run an import: validate and preview the diff without writing anything.
 */
export async function dryRunImportModels(
  payload: ImportModelsPayload,
): Promise<ImportModelsDryRunResponse> {
  const { data } = await adminApiClient.post<ImportModelsDryRunResponse>(
    '/admin/models/import?dry_run=true',
    payload,
  );
  return data;
}

/**
 * Apply an import document in one transaction. A conflict under the reject
 * strategy aborts the whole batch (no partial writes).
 */
export async function importModels(
  payload: ImportModelsPayload,
): Promise<ImportModelsResponse> {
  const { data } = await adminApiClient.post<ImportModelsResponse>(
    '/admin/models/import',
    payload,
  );
  return data;
}

/**
 * Parse an uploaded JSON file into an import document. Validates the schema
 * version before returning.
 */
export function parseImportFile(text: string): ModelExportDocument {
  const doc = JSON.parse(text) as ModelExportDocument;
  if (doc.schema_version !== MODEL_EXCHANGE_SCHEMA_VERSION) {
    throw new Error(
      `Schema version mismatch: expected ${MODEL_EXCHANGE_SCHEMA_VERSION}, got ${doc.schema_version ?? '(missing)'}`,
    );
  }
  if (!Array.isArray(doc.models)) {
    throw new Error('Invalid document: "models" must be an array');
  }
  return doc;
}

/**
 * Trigger a browser download of the export document as a JSON file.
 */
export function downloadExportDocument(doc: ModelExportDocument, filename = 'model-export.json'): void {
  const blob = new Blob([JSON.stringify(doc, null, 2)], { type: 'application/json' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
}
