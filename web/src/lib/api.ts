import axios from 'axios';
import { requestSecurityVerification } from '@/lib/security-verification';
import { clearPlaygroundCredential } from '@/lib/playground-credential';
import { toast } from 'sonner';
import { protectedSignal, refreshAuthorization, prepareAdminRequest } from '@/lib/authorization-events';

export const API_BASE_URL = import.meta.env.VITE_API_BASE_URL || '/api';

function requestPath(error: unknown): string {
  if (!error || typeof error !== 'object') return '';
  const config = (error as { config?: { url?: string } }).config;
  return config?.url ?? '';
}

export function isSessionAuthPath(path: string) {
  return path.startsWith('/user/') || path === '/token' || path.startsWith('/token/');
}

function matchesCurrentSession(config: { headers?: { Authorization?: unknown } } | undefined) {
  const token = localStorage.getItem('token');
  return !!token && config?.headers?.Authorization === `Bearer ${token}`;
}

export function clearUserSession() {
  clearPlaygroundCredential();
  localStorage.removeItem('token');
  localStorage.removeItem('userId');
  localStorage.removeItem('userRole');
}

export function clearAdminSession() {
  localStorage.removeItem('adminToken');
}

export const apiClient = axios.create({
  baseURL: API_BASE_URL,
  timeout: 30_000,
  headers: {
    'Content-Type': 'application/json',
  },
});

// Request interceptor: attach token from localStorage
apiClient.interceptors.request.use(
  (config) => {
    config.signal ??= protectedSignal();
    const token = localStorage.getItem('token');
    if (config.headers.Authorization && !matchesCurrentSession(config)) throw new Error('Session changed before request');
    if (token) {
      config.headers.Authorization = `Bearer ${token}`;
    }
    return config;
  },
  (error) => Promise.reject(error)
);

// Response interceptor: handle 401 and redirect to login
apiClient.interceptors.response.use(
  (response) => response,
  async (error) => {
    const currentSession = matchesCurrentSession(error.config);
    if (currentSession && error.response?.status === 403 && error.response.data?.reason === 'STEP_UP_REQUIRED' && !error.config.url?.startsWith('/user/security') && !error.config.securityRetried) {
      error.config.securityRetried = true;
      const originalToken = localStorage.getItem('token');
      await requestSecurityVerification();
      if (localStorage.getItem('token') !== originalToken) throw new Error('Session changed during verification');
      return apiClient.request(error.config);
    }
    if (currentSession && error.response?.status === 401 && isSessionAuthPath(requestPath(error))) {
      clearUserSession();
      refreshAuthorization();
      toast.error('Session expired. Please sign in again.');
      window.location.href = '/login';
    }
    return Promise.reject(error);
  }
);

// Admin endpoints authenticate the user's session and authorize at the owner.
export const adminApiClient = axios.create({
  baseURL: API_BASE_URL,
  timeout: 30_000,
  headers: {
    'Content-Type': 'application/json',
  },
});

adminApiClient.interceptors.request.use(
  (config) => {
    config.signal ??= protectedSignal();
    const token = localStorage.getItem('token');
    if (config.headers.Authorization && !matchesCurrentSession(config)) throw new Error('Session changed before request');
    if (token) {
      config.headers.Authorization = `Bearer ${token}`;
    }
    return prepareAdminRequest(config);
  },
  (error) => Promise.reject(error)
);

adminApiClient.interceptors.response.use(
  (response) => response,
  async (error) => {
    const currentSession = matchesCurrentSession(error.config);
    if (currentSession && error.response?.status === 403 && error.response.data?.reason === 'STEP_UP_REQUIRED' && !error.config.securityRetried) {
      error.config.securityRetried = true;
      const originalToken = localStorage.getItem('token');
      await requestSecurityVerification();
      if (localStorage.getItem('token') !== originalToken) throw new Error('Session changed during verification');
      return adminApiClient.request(error.config);
    }
    if (error.response?.status === 403) {
      refreshAuthorization();
    }
    if (currentSession && error.response?.status === 401) {
      clearUserSession();
      clearAdminSession();
      refreshAuthorization();
      toast.error('Session expired. Please sign in again.');
      window.location.href = '/login';
    }
    return Promise.reject(error);
  }
);
