import { useQuery } from '@tanstack/react-query';
import { api, unwrap } from './api/client';

/** How this server lets people sign in; public and cached for five minutes. */
export function useAuthMethods() {
  return useQuery({ queryKey: ['auth-methods'], staleTime: 300_000, queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/auth/methods', { signal })) });
}
