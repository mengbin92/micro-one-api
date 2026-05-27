import axios from 'axios';
import { toast } from 'sonner';

const API_BASE_URL = import.meta.env.VITE_API_BASE_URL || '/api';

function requestPath(error: unknown): string {
  if (!error || typeof error !== 'object') return '';
  const config = (error as { config?: { url?: string } }).config;
  return config?.url ?? '';
}

export function isSessionAuthPath(path: string) {
  return path.startsWith('/user/') || path === '/token' || path.startsWith('/token/');
}

export function clearUserSession() {
  localStorage.removeItem('token');
  localStorage.removeItem('userId');
  localStorage.removeItem('userRole');
}

export function clearAdminSession() {
  localStorage.removeItem('adminToken');
}

export const apiClient = axios.create({
  baseURL: API_BASE_URL,
  headers: {
    'Content-Type': 'application/json',
  },
});

// Request interceptor: attach token from localStorage
apiClient.interceptors.request.use(
  (config) => {
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
      toast.error('Session expired. Please sign in again.');
      window.location.href = '/login';
    }
    return Promise.reject(error);
  }
);

// Admin API client: admin endpoints now accept the signed-in user's session
// token (backend authorises by role >= admin). The shared ADMIN_TOKEN remains
// a backend-only backdoor and is no longer entered from the UI.
export const adminApiClient = axios.create({
  baseURL: API_BASE_URL,
  headers: {
    'Content-Type': 'application/json',
  },
});

adminApiClient.interceptors.request.use(
  (config) => {
    const token = localStorage.getItem('token');
    if (token) {
      config.headers.Authorization = `Bearer ${token}`;
    }
    const operatorId = localStorage.getItem('userId');
    if (operatorId && operatorId !== '0') {
      config.headers['X-Operator-User-Id'] = operatorId;
    }
    return config;
  },
  (error) => Promise.reject(error)
);

adminApiClient.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error.response?.status === 401) {
      clearAdminSession();
      toast.error('Admin permission is required.');
    }
    return Promise.reject(error);
  }
);
