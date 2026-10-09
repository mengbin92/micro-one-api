import axios from 'axios';
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
  (error) => {
    if (error.response?.status === 401 && isSessionAuthPath(requestPath(error))) {
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
    if (token) {
      config.headers.Authorization = `Bearer ${token}`;
    }
    return prepareAdminRequest(config);
  },
  (error) => Promise.reject(error)
);

adminApiClient.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error.response?.status === 403) {
      refreshAuthorization();
    }
    if (error.response?.status === 401) {
      clearUserSession();
      clearAdminSession();
      refreshAuthorization();
      toast.error('Session expired. Please sign in again.');
      window.location.href = '/login';
    }
    return Promise.reject(error);
  }
);
