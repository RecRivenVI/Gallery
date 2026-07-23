import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createContext, useContext, useMemo, type ReactNode } from 'react';
import { api, csrfHeaders, expectData, expectNoContent, type Bootstrap } from '../api/client';

type SessionContextValue = {
  bootstrap: Bootstrap;
  can: (capability: string) => boolean;
  refresh: () => Promise<void>;
  logout: () => Promise<void>;
};

const SessionContext = createContext<SessionContextValue | null>(null);

export function SessionProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient();
  const bootstrapQuery = useQuery({
    queryKey: ['bootstrap'],
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/bootstrap', { signal })),
    staleTime: 15_000,
    refetchOnWindowFocus: true
  });
  const logoutMutation = useMutation({
    mutationFn: async () => {
      const bootstrap = bootstrapQuery.data;
      if (!bootstrap) return;
      expectNoContent(
        await api.POST('/api/v1/auth/logout', { params: { header: csrfHeaders(bootstrap.csrfToken) } })
      );
    },
    onSettled: async () => {
      queryClient.removeQueries({ predicate: (query) => query.queryKey[0] !== 'bootstrap' });
      await queryClient.refetchQueries({ queryKey: ['bootstrap'], type: 'active' });
    }
  });

  const value = useMemo<SessionContextValue | null>(() => {
    if (!bootstrapQuery.data) return null;
    const capabilities = new Set(bootstrapQuery.data.effectiveCapabilities);
    return {
      bootstrap: bootstrapQuery.data,
      can: (capability) => capabilities.has(capability),
      refresh: async () => {
        await queryClient.refetchQueries({ queryKey: ['bootstrap'], type: 'active' });
      },
      logout: async () => logoutMutation.mutateAsync()
    };
  }, [bootstrapQuery.data, logoutMutation, queryClient]);

  if (bootstrapQuery.isPending)
    return (
      <div className="app-loading" role="status">
        正在连接 Gallery…
      </div>
    );
  if (bootstrapQuery.isError || !value)
    throw bootstrapQuery.error instanceof Error ? bootstrapQuery.error : new Error('Bootstrap failed');
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

export function useSession(): SessionContextValue {
  const value = useContext(SessionContext);
  if (!value) throw new Error('SessionProvider 缺失');
  return value;
}
