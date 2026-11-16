import { Link } from 'react-router-dom';
import { Plug } from 'lucide-react';
import { useTrackingProfile } from '../profileContext';
import { SiloForYouLink, useSiloConnect } from '../siloConnect';
import { siloConnected } from '../siloConnectPolicy';
import { Loading, PageTitle } from '../ui';
import { t } from '../i18n';

// Personal AI recommendations live in Silo. The mirror that copied them here
// retired with the Silo library bridge, so this page points to Silo's own.
export function RecommendationsPage() {
  const { active } = useTrackingProfile();
  const query = useSiloConnect();
  const connected = siloConnected(query.data, active.id) && !!query.data?.silo_url;
  return <><PageTitle title={t('Recommendations')} />
    {query.isPending ? <Loading label={t('Loading recommendations')} />
      : <section className="recommendations">
        <p>{connected ? t('Silo recommends titles from what you watch there.') : t('Connect this profile to Silo to see what it recommends from what you watch there.')}</p>
        <div className="actions">{connected ? <SiloForYouLink button /> : <Link className="button primary" to="/integrations?section=silo"><Plug aria-hidden />{t('Connect Silo')}</Link>}</div>
      </section>}
  </>;
}
