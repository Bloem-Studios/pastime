import { createContext, useCallback, useContext, useEffect, useId, useRef, type ReactNode } from 'react';
import { QueryClient, useQuery, useQueryClient } from '@tanstack/react-query';
import { useBeforeUnload, useBlocker } from 'react-router-dom';
import { api, ApiError, setSessionCredential, unwrap, type Session } from './api/client';
import { useConfirm } from './ui';
import { t } from './i18n';
import { affectsQuery } from './api/invalidation';
import {profileQueryKey,setTrackingProfile} from './profileScope';
import { withCacheIdentity } from './profileLoginPolicy';

export const queryClient = new QueryClient({ defaultOptions: { queries: { staleTime: 15_000, retry: false, refetchOnWindowFocus: true }, mutations: { retry: false } } });
export const privateKey = profileQueryKey;
type Auth = { session: Session | null | undefined; loading: boolean; error: unknown; retry: () => void; establish: (session: Session) => Promise<void>; clear: () => Promise<void> };
const AuthContext = createContext<Auth | null>(null);
export function AuthProvider({ children }: { children: ReactNode }) {
  const client = useQueryClient();
  const query = useQuery({ queryKey: ['session'], queryFn: async ({ signal }) => {
    const response = await api.GET('/api/v1/session', { signal });
    if (response.response.status === 401) return null;
    return withCacheIdentity(unwrap(response));
  }, refetchInterval: 60_000 });
  const identity = query.data?.user.id ?? null;
  const previous = useRef<string | null>(null);
  const channel = useRef<BroadcastChannel | null>(null);
  const clear = useCallback(async (broadcast = true) => {
    setSessionCredential('');setTrackingProfile('');
    await client.cancelQueries({ queryKey: ['session'] });
    await client.cancelQueries({ queryKey: ['private'] }); client.removeQueries({ queryKey: ['private'] });
    // The household's profile list must not outlive the session that could see it.
    await client.cancelQueries({ queryKey: ['profiles'] }); client.removeQueries({ queryKey: ['profiles'] });
    client.setQueryData(['session'], null);
    if (broadcast) channel.current?.postMessage('logout');
  }, [client]);
  const establish = useCallback(async (session: Session) => {
    await client.cancelQueries({ queryKey: ['session'] });
    await client.cancelQueries({ queryKey: ['private'] }); client.removeQueries({ queryKey: ['private'] });
    await client.cancelQueries({ queryKey: ['profiles'] }); client.removeQueries({ queryKey: ['profiles'] });
    setTrackingProfile(''); setSessionCredential(session.csrf_token); client.setQueryData(['session'], withCacheIdentity(session));
  }, [client]);
  useEffect(() => {
    if (previous.current !== identity) {
      if (previous.current) { const old = privateKey(previous.current); void client.cancelQueries({ queryKey: old }); client.removeQueries({ queryKey: old }); client.removeQueries({ queryKey: ['profiles', previous.current] }); }
      previous.current = identity;
    }
    setSessionCredential(query.data?.csrf_token ?? '');
  }, [identity, query.data?.csrf_token, client]);
  useEffect(() => {
    if (!('BroadcastChannel' in window)) return;
    const sync = new BroadcastChannel('pastime-session'); channel.current = sync;
    sync.onmessage = event => { if (event.data === 'logout') void clear(false); };
    return () => { sync.close(); channel.current = null; };
  }, [clear]);
  useEffect(() => { const expired = () => { void clear(); }; window.addEventListener('pastime-session-expired', expired); return () => window.removeEventListener('pastime-session-expired', expired); }, [clear]);
  useEffect(() => { const conflict = () => { if (identity) void client.invalidateQueries({ queryKey: privateKey(identity) }); }; window.addEventListener('pastime-data-conflict', conflict); return () => window.removeEventListener('pastime-data-conflict', conflict); }, [client, identity]);
  return <AuthContext.Provider value={{ session: query.data, loading: query.isPending, error: query.error, retry: () => void query.refetch(), clear, establish }}>{children}</AuthContext.Provider>;
}
export function useAuth() { const context = useContext(AuthContext); if (!context) throw new Error(t('Missing authentication context')); return context; }
export function useUser() { const { session } = useAuth(); if (!session) throw new ApiError(401, 'unauthenticated', t('Sign in to continue.')); return session.user; }
export function useRefresh(resources?: readonly string[]) { const client = useQueryClient(); const user = useUser(); return () => client.invalidateQueries({ predicate: query => affectsQuery(query.queryKey, user.id, resources) }); }

type DirtyState = { set: (id: string, dirty: boolean) => void; isDirty: () => boolean; clear: () => void };
const DirtyContext = createContext<DirtyState>({ set() {}, isDirty: () => false, clear() {} });
export function DirtyProvider({ children }: { children: ReactNode }) {
  const sources = useRef(new Set<string>()); const confirm = useConfirm(); const asking = useRef(false);
  const set = useCallback((id: string, dirty: boolean) => { if (dirty) sources.current.add(id); else sources.current.delete(id); }, []);
  const blocker = useBlocker(useCallback(() => sources.current.size > 0, []));
  useBeforeUnload(useCallback(event => { if (sources.current.size) { event.preventDefault(); event.returnValue = ''; } }, []));
  useEffect(() => {
    if (blocker.state !== 'blocked' || asking.current) return;
    asking.current = true;
    void confirm({ title: t('Discard changes?'), description: t('You have unsaved changes on this page.'), label: t('Discard changes'), danger: true }).then(discard => {
      asking.current = false;
      if (discard) { sources.current.clear(); blocker.proceed(); } else blocker.reset();
    });
  }, [blocker, confirm]);
  return <DirtyContext.Provider value={{ set, isDirty: () => sources.current.size > 0, clear: () => sources.current.clear() }}>{children}</DirtyContext.Provider>;
}
export const useDirtyState = () => useContext(DirtyContext);
export function useUnsaved() {
  const id = useId(); const { set } = useDirtyState();
  useEffect(() => () => set(id, false), [id, set]);
  return { markDirty: () => set(id, true), markSaved: () => set(id, false) };
}
