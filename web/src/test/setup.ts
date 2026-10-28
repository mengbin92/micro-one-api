import '@testing-library/jest-dom/vitest';
import { configure } from '@testing-library/react';

// 满载机器上 jsdom 渲染慢于 testing-library 1s 默认等待，统一放宽到 5s
configure({ asyncUtilTimeout: 5000 });

import { afterAll, afterEach, beforeAll, vi } from 'vitest';
import { server } from './msw/server';

const storage = new Map<string, string>();

Object.defineProperty(window, 'localStorage', {
  value: {
    getItem: (key: string) => storage.get(key) ?? null,
    setItem: (key: string, value: string) => {
      storage.set(key, value);
    },
    removeItem: (key: string) => {
      storage.delete(key);
    },
    clear: () => {
      storage.clear();
    },
  },
  configurable: true,
});

Object.defineProperty(window, 'matchMedia', {
  value: (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    addListener: () => undefined,
    removeListener: () => undefined,
    dispatchEvent: () => false,
  }),
  configurable: true,
});

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }));

afterEach(() => {
  server.resetHandlers();
  window.localStorage.clear();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

afterAll(() => server.close());
