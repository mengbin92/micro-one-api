let pendingCredential: string | null = null;

function normalizeCredential(secret: string) {
  const normalized = secret.trim();
  return normalized || null;
}

export function setPlaygroundCredential(secret: string) {
  pendingCredential = normalizeCredential(secret);
}

export function takePlaygroundCredential() {
  const credential = pendingCredential;
  pendingCredential = null;
  return credential;
}

export function clearPlaygroundCredential() {
  pendingCredential = null;
}
