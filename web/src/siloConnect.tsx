import { useQuery } from '@tanstack/react-query';
import { Compass, ExternalLink, Search } from 'lucide-react';
import { api, unwrap } from './api/client';
import { privateKey, useUser } from './state';
import { useTrackingProfile } from './profileContext';
import { siloConnected, siloPage, type SiloState } from './siloConnectPolicy';
import { t } from './i18n';

export const siloConnectKey = (userID: string) => privateKey(userID, 'silo-connect');

/** This account's profiles and which of them Silo can send to; null when the server has no Silo connection. */
export function useSiloConnect() {
  const user = useUser();
  return useQuery({
    queryKey: siloConnectKey(user.id), staleTime: 60_000, retry: false,
    queryFn: async ({ signal }): Promise<SiloState | null> => {
      const result = await api.GET('/api/v1/integrations/silo/connect', { signal });
      if (result.response.status === 404 || result.response.status === 405) return null;
      return unwrap(result);
    },
  });
}

/** The registered Silo address when the selected profile is connected to it. */
function useConnectedSilo(): string | null {
  const { active } = useTrackingProfile();
  const query = useSiloConnect();
  return query.data && siloConnected(query.data, active.id) ? query.data.silo_url || null : null;
}

/** Personal recommendations now live in Silo; this points there. */
export function SiloForYouLink({ button = false }: { button?: boolean }) {
  const href = siloPage(useConnectedSilo(), '/recommendations');
  if (!href) return null;
  const link = <a className={button ? 'button primary' : 'text-link'} href={href} rel="noreferrer"><Compass size={16} aria-hidden />{t('For you in Silo')}{!button && <ExternalLink size={14} aria-hidden />}</a>;
  return button ? link : <p className="silo-for-you">{link}</p>;
}

/** Searches Silo for a title, in place of the retired availability check. */
export function FindInSilo({ title }: { title: string }) {
  const href = siloPage(useConnectedSilo(), '/search', { q: title });
  if (!href) return null;
  return <div className="actions"><a className="button small" href={href} rel="noreferrer"><Search size={15} aria-hidden />{t('Find in Silo')}</a></div>;
}
