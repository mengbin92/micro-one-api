// WebAuthn browser bridge: the server speaks the standard WebAuthn JSON
// dialect (base64url, no padding) inside `options` / `attestation` string
// fields, while navigator.credentials wants ArrayBuffers — convert here.

const PASSKEY_OPTIONS_MISSING = '通行密钥参数缺失，请重试';

export function base64UrlToBytes(value: string): Uint8Array {
  const binary = atob(value.replace(/-/g, '+').replace(/_/g, '/'));
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

export function bytesToBase64Url(bytes: ArrayBuffer | Uint8Array): string {
  const view = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes);
  let binary = '';
  for (const byte of view) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function toBufferSource(value: unknown): unknown {
  return typeof value === 'string' ? base64UrlToBytes(value) : value;
}

export function parsePasskeyOptions<T>(raw: string | undefined): T {
  if (!raw) throw new Error(PASSKEY_OPTIONS_MISSING);
  const parsed = JSON.parse(raw) as { challenge?: unknown; user?: { id?: unknown } };
  if (!parsed || typeof parsed.challenge !== 'string' || (parsed.user && typeof parsed.user.id !== 'string')) {
    throw new Error(PASSKEY_OPTIONS_MISSING);
  }
  return parsed as T;
}

// challenge, user.id and exclude/allowCredentials ids are base64url strings
// server-side; the browser needs buffers for every one of them.
export function prepareCreationOptions(options: PublicKeyCredentialCreationOptions): PublicKeyCredentialCreationOptions {
  return {
    ...options,
    challenge: toBufferSource(options.challenge) as BufferSource,
    user: { ...options.user, id: toBufferSource(options.user.id) as BufferSource },
    excludeCredentials: options.excludeCredentials?.map(credential => ({ ...credential, id: toBufferSource(credential.id) as BufferSource })),
  };
}

export function prepareRequestOptions(options: PublicKeyCredentialRequestOptions): PublicKeyCredentialRequestOptions {
  return {
    ...options,
    challenge: toBufferSource(options.challenge) as BufferSource,
    allowCredentials: options.allowCredentials?.map(credential => ({ ...credential, id: toBufferSource(credential.id) as BufferSource })),
  };
}

export function serializeAttestation(credential: PublicKeyCredential): string {
  const response = credential.response as AuthenticatorAttestationResponse;
  return JSON.stringify({
    id: credential.id,
    rawId: bytesToBase64Url(credential.rawId),
    type: credential.type,
    response: {
      attestationObject: bytesToBase64Url(response.attestationObject),
      clientDataJSON: bytesToBase64Url(response.clientDataJSON),
      transports: response.getTransports?.() ?? [],
    },
    clientExtensionResults: credential.getClientExtensionResults(),
  });
}

export function serializeAssertion(credential: PublicKeyCredential): string {
  const response = credential.response as AuthenticatorAssertionResponse;
  return JSON.stringify({
    id: credential.id,
    rawId: bytesToBase64Url(credential.rawId),
    type: credential.type,
    response: {
      authenticatorData: bytesToBase64Url(response.authenticatorData),
      clientDataJSON: bytesToBase64Url(response.clientDataJSON),
      signature: bytesToBase64Url(response.signature),
      userHandle: response.userHandle ? bytesToBase64Url(response.userHandle) : null,
    },
    clientExtensionResults: credential.getClientExtensionResults(),
  });
}

export function passkeyUnavailable(): boolean {
  return typeof window === 'undefined'
    || typeof navigator === 'undefined'
    || !window.isSecureContext
    || !window.PublicKeyCredential
    || !navigator.credentials?.create
    || !navigator.credentials?.get;
}
