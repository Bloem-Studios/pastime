import { Link } from 'react-router-dom';
import { Plug } from 'lucide-react';
import { useTrackingProfile } from './profileContext';
import { useSiloConnect } from './siloConnect';
import { siloConnectNotice } from './siloConnectPolicy';
import { t } from './i18n';

// Reads the live key state, not the provisioning flags on the profile, which stop
// changing once Silo connects through a key.
export function ProfileConnectionNotice() {
  const { active } = useTrackingProfile();
  const connect = useSiloConnect();
  const notice = siloConnectNotice(connect.data, active.id);
  if (!notice) return null;
  return <aside className="notice profile-connection" aria-label={t('Silo connection')}>
    <strong>{t('This profile is not connected to Silo')}</strong>
    <p>{notice.otherConnected ? t('Another profile on this account is connected to Silo. Choose it from the profile menu, or connect this profile too.')
      : t('Connect it to record what you watch in Silo here, along with your Silo favourites and watchlist.')}</p>
    <Link className="button" to="/integrations?section=silo"><Plug aria-hidden />{t('Connect Silo')}</Link>
  </aside>;
}
