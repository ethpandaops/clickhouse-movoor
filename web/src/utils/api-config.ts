/**
 * Runtime configuration for the generated API client (hey-api).
 *
 * Wired in via `openapi-ts.config.ts` (`runtimeConfigPath`). The generated
 * client imports `createClientConfig` from here and calls it with its default
 * config. Until `pnpm generate:api` has been run there is no generated client
 * to import the typed `CreateClientConfig` from, so we keep this file
 * self-contained with a minimal structural type.
 */

export const BASE_URL = import.meta.env.VITE_API_URL || '';
export const PATH_PREFIX = '/api/v1';

// Refetch intervals for background polling (in milliseconds).
export const REFETCH_INTERVALS = {
  CONFIG: 60_000,
} as const;

interface ClientConfig {
  baseUrl?: string;
  fetch?: typeof fetch;
}

export const createClientConfig = <T extends ClientConfig>(
  config: T
): T & { baseUrl: string; fetch: typeof fetch } => ({
  ...config,
  baseUrl: BASE_URL,
  fetch: async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const response = await globalThis.fetch(input, { ...init, credentials: 'include' });

    if (response.status === 401) {
      window.dispatchEvent(new CustomEvent('movoor:auth-expired'));
    }

    return response;
  },
});
