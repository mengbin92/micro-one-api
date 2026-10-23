/// <reference types="node" />
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import { parseVendorReportImport } from '../src/lib/vendor-report-import';

describe('supplier import archive preview', () => {
  it('previews the complete 203-record prepared successor without truncating its request', () => {
    const evidence = resolve(process.cwd(), '../docs/runbooks/evidence/deepseek-channel11-closed-window-2026-10-23');
    const document = readFileSync(resolve(evidence, 'retrospective-owner/final-manifest.json'), 'utf8');
    const manifest = JSON.parse(document);
    const request = {
      manifest_json: btoa(Array.from(new TextEncoder().encode(document), byte => String.fromCharCode(byte)).join('')),
      csv: readFileSync(resolve(evidence, 'owner-execution/vendor-normalized.csv'), 'utf8'),
      expected_report_id: manifest.retrospective_evidence.report_id,
      reason: manifest.reason,
    };
    expect(request.manifest_json.length).toBeGreaterThan(120_000);
    expect(manifest.retrospective_evidence.records).toHaveLength(203);
    const result = parseVendorReportImport(JSON.stringify(request));
    expect(result).toMatchObject({ provider: 'deepseek', model: 'deepseek-flash', version: 'deepseek-key11-20261021-22-export-20261023-v1-final-v2', supersedes: '2bc6d3dd046db8ee94ef5f576a1482fca3815cb9bc9e4d198f2e1a0ea5f419db' });
    expect(result.request.expected_report_id).toBe('aecaa5c09fe36405cd59d59eb1e8e7c4d62e6c72f04e9c67052db5cf045ed9b1');
    expect(result.request).toEqual(request);
  });
});
