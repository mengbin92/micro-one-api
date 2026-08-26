import { API_BASE_URL } from '@/lib/api';

export type RelayAddressSource = 'server-status' | 'build-env' | 'same-origin-fallback';

export interface RelayAddress {
  url: string;
  source: RelayAddressSource;
  warning?: string;
}

export function normalizeRelayBaseUrl(value: string) {
  const candidate = value.trim().replace(/\/+$/, '');
  if (!candidate) {
    throw new Error('Relay 地址不能为空');
  }

  let parsed: URL;
  try {
    parsed = new URL(candidate);
  } catch {
    throw new Error('Relay 地址格式无效');
  }

  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    throw new Error('Relay 地址必须使用 HTTP 或 HTTPS');
  }
  if (parsed.username || parsed.password) {
    throw new Error('Relay 地址不能包含用户名或密码');
  }

  return parsed.toString().replace(/\/+$/, '');
}

export function validateRelayAddressForPage(address: RelayAddress, pageProtocol?: string) {
  const protocol = pageProtocol ?? (typeof window === 'undefined' ? undefined : window.location.protocol);
  if (protocol === 'https:' && address.url.startsWith('http:')) {
    throw new Error('HTTPS 控制台不能调用 HTTP Relay');
  }
}

export async function resolveRelayBaseUrl(): Promise<RelayAddress> {
  let configuredAddress: string | undefined;
  try {
    const response = await fetch(`${API_BASE_URL}/status`);
    if (response.ok) {
      const payload = (await response.json()) as { data?: { server_address?: unknown } };
      if (typeof payload.data?.server_address === 'string' && payload.data.server_address.trim()) {
        configuredAddress = payload.data.server_address;
      }
    }
  } catch {
    // Only status transport/JSON failures fall through to local defaults.
  }

  if (configuredAddress) {
    const address: RelayAddress = {
      url: normalizeRelayBaseUrl(configuredAddress),
      source: 'server-status',
    };
    validateRelayAddressForPage(address);
    return address;
  }

  const buildAddress = import.meta.env.VITE_RELAY_BASE_URL;
  if (typeof buildAddress === 'string' && buildAddress.trim()) {
    const address: RelayAddress = {
      url: normalizeRelayBaseUrl(buildAddress),
      source: 'build-env',
    };
    validateRelayAddressForPage(address);
    return address;
  }

  const address: RelayAddress = {
    url: normalizeRelayBaseUrl(window.location.origin),
    source: 'same-origin-fallback',
    warning: '未读取到平台 Relay 地址，当前使用同源地址；若未配置反向代理，调用可能失败。',
  };
  validateRelayAddressForPage(address);
  return address;
}
