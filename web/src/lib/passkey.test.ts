import { describe, expect, it } from 'vitest';
import { base64UrlToBytes, bytesToBase64Url, parsePasskeyOptions, passkeyUnavailable, prepareCreationOptions, prepareRequestOptions, serializeAssertion, serializeAttestation } from './passkey';

describe('passkey codec', () => {
  it('round-trips base64url including unpadded input', () => {
    const bytes = new Uint8Array([0xfb, 0xff, 0x01, 0x30]);
    const encoded = bytesToBase64Url(bytes);
    expect(encoded).toBe('-_8BMA');
    expect(base64UrlToBytes(encoded)).toEqual(bytes);
    expect(base64UrlToBytes('-_8BMA')).toEqual(bytes); // unpadded accepted
  });

  it('converts challenge, user.id and excludeCredentials on creation options', () => {
    const options = prepareCreationOptions({
      challenge: 'Y2hhbGxlbmdl',
      user: { id: 'dXNlcg', name: 'bob', displayName: 'Bob' },
      excludeCredentials: [{ type: 'public-key', id: 'Y3JlZA' }],
    } as unknown as PublicKeyCredentialCreationOptions);
    expect(new Uint8Array(options.challenge as ArrayBuffer)).toEqual(base64UrlToBytes('Y2hhbGxlbmdl'));
    expect(new Uint8Array((options.user as PublicKeyCredentialUserEntity).id as ArrayBuffer)).toEqual(base64UrlToBytes('dXNlcg'));
    const excluded = options.excludeCredentials?.[0];
    expect(excluded && 'id' in excluded ? new Uint8Array(excluded.id as ArrayBuffer) : undefined).toEqual(base64UrlToBytes('Y3JlZA'));
  });

  it('converts allowCredentials on request options', () => {
    const options = prepareRequestOptions({
      challenge: 'Y2hhbGxlbmdl',
      allowCredentials: [{ type: 'public-key', id: 'Y3JlZA' }],
    } as unknown as PublicKeyCredentialRequestOptions);
    const allowed = options.allowCredentials?.[0];
    expect(allowed && 'id' in allowed ? new Uint8Array(allowed.id as ArrayBuffer) : undefined).toEqual(base64UrlToBytes('Y3JlZA'));
  });

  it('validates options shape and localizes the missing error', () => {
    expect(() => parsePasskeyOptions<PublicKeyCredentialCreationOptions>(undefined)).toThrow('通行密钥参数缺失，请重试');
    expect(() => parsePasskeyOptions<PublicKeyCredentialCreationOptions>('{}')).toThrow('通行密钥参数缺失，请重试');
    expect(() => parsePasskeyOptions<PublicKeyCredentialCreationOptions>('{"user":{"id":"dXNlcg"}}')).toThrow('通行密钥参数缺失，请重试');
  });
});

describe('passkey serializers', () => {
  function credential(response: object) {
    return {
      id: 'cred-1',
      rawId: new Uint8Array([1, 2, 3]),
      type: 'public-key',
      response,
      getClientExtensionResults: () => ({ ok: true }),
    } as unknown as PublicKeyCredential;
  }

  it('serializes attestation in the WebAuthn JSON dialect', () => {
    const parsed = JSON.parse(serializeAttestation(credential({
      attestationObject: new Uint8Array([4]),
      clientDataJSON: new Uint8Array([5]),
      getTransports: () => ['usb'],
    })));
    expect(parsed).toMatchObject({
      id: 'cred-1',
      rawId: 'AQID',
      type: 'public-key',
      response: { attestationObject: 'BA', clientDataJSON: 'BQ', transports: ['usb'] },
      clientExtensionResults: { ok: true },
    });
  });

  it('serializes assertion with a null userHandle when absent', () => {
    const parsed = JSON.parse(serializeAssertion(credential({
      authenticatorData: new Uint8Array([6]),
      clientDataJSON: new Uint8Array([5]),
      signature: new Uint8Array([7]),
      userHandle: null,
    })));
    expect(parsed.response).toMatchObject({ authenticatorData: 'Bg', clientDataJSON: 'BQ', signature: 'Bw', userHandle: null });
  });
});

describe('passkeyUnavailable', () => {
  it('reports unavailable without a secure context or credentials container', () => {
    expect(passkeyUnavailable()).toBe(true);
  });
});
