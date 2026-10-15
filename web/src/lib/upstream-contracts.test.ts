import { describe, expect, it } from 'vitest';
import { blankContractDocument, contractInteger, contractWriteBody, exactContractScope, normalizeContractList, parseContractDocument, type ContractList } from './upstream-contracts';

const scope = { source_kind: 'subscription', source_id: '9007199254740993', upstream_model_id: 'model-1', protocol: 'responses' };
const document = { ...blankContractDocument(scope), provider: 'supplier', adapter_build: 'installed-build', parser_revision: 'installed-parser', usage_revision: 'installed-usage', fixture_evidence_id: 'fixture-1', basis_revision: 'basis-1' };

describe('contract DTO boundaries', () => {
  it('keeps source IDs and read-back CAS strings exact across the int64 range', () => {
    expect(exactContractScope(scope)).toEqual(scope);
    expect(contractInteger('9223372036854775807')).toBe('9223372036854775807');
    expect(contractWriteBody({ scope, expected_revision: '9007199254740995' }, false)).toEqual({ scope, expected_revision: '9007199254740995' });
  });
  it.each(['0', '-1', '1.5', '1e3', ' 7', '9223372036854775808'])('rejects an invalid source ID %s', source_id => {
    expect(() => exactContractScope({ ...scope, source_id })).toThrow();
  });
  it.each(['model-*', 'model-?', 'bad\u0000model', 'bad\nmodel'])('refuses non-exact model %s', upstream_model_id => {
    expect(() => exactContractScope({ ...scope, upstream_model_id })).toThrow();
  });
  it('rejects an already-rounded numeric CAS and unsafe legacy conversions', () => {
    expect(() => contractInteger(Number(scope.source_id))).toThrow('安全范围');
    expect(() => contractWriteBody({ scope, expected_revision: '7' }, true)).toThrow('兼容模式');
    expect(contractWriteBody({ scope: { ...scope, source_id: '7' }, expected_revision: '8' }, true)).toEqual({ scope: { ...scope, source_id: 7 }, expected_revision: 8 });
  });
  it('validates a candidate against the selected scope and does not fabricate installed versions', () => {
    expect(blankContractDocument(scope).adapter_build).toBe('');
    expect(parseContractDocument(JSON.stringify(document), scope)).toEqual(document);
    expect(() => parseContractDocument(JSON.stringify({ ...document, scope: { ...scope, protocol: 'gemini' } }), scope)).toThrow('scope');
    expect(() => parseContractDocument(JSON.stringify({ ...document, adapter_build: '' }), scope)).toThrow('格式无效');
    expect(() => parseContractDocument(JSON.stringify({ ...document, paid_probe: true }), scope)).toThrow('格式无效');
  });
  it('restores only an exact legacy seed CAS zero, without concealing malformed IAM or active bindings', () => {
    // The legacy generated-struct wire omits proto zero values.
    const raw = { binding: { scope, status: 'candidate' }, contracts: [{ revision: 'version-1', document: { ...document, rule_sets: undefined } }] } as unknown as ContractList;
    expect(normalizeContractList(raw, true, scope).binding?.revision).toBe('0');
    expect(normalizeContractList(raw, true, scope).contracts?.[0].document.rule_sets).toEqual([]);
    expect(normalizeContractList(raw, false, scope).binding?.revision).toBeUndefined();
    expect(normalizeContractList(raw, true, { ...scope, source_id: '9' }).binding?.revision).toBeUndefined();
    expect(normalizeContractList({ ...raw, binding: { ...raw.binding!, status: 'active' } }, true, scope).binding?.revision).toBeUndefined();
    expect(normalizeContractList({ ...raw, binding: { ...raw.binding!, revision: 9007199254740992 } }, true, scope).binding?.revision).toBe(9007199254740992);
  });
});
