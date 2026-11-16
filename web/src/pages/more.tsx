import { Link } from 'react-router-dom';
import { Activity, BookOpenText, CalendarDays, CalendarRange, ChartColumn, ChevronRight, Disc3, Layers, LogOut, Plug, Settings, ShieldCheck, type LucideIcon } from 'lucide-react';
import { useWorkspace } from '../layout';
import { useUser } from '../state';
import { PageTitle } from '../ui';
import { t } from '../i18n';

type Destination = { to: string; label: string; Icon: LucideIcon; external?: boolean };

/** The phone's fifth tab: every destination the bottom bar has no room for, and signing out. */
export function MorePage() {
  const user = useUser(); const { signOut } = useWorkspace();
  const groups: { label: string; items: Destination[] }[] = [
    { label: t('Your record'), items: [
      { to: '/lists', label: t('Lists'), Icon: Layers }, { to: '/collection', label: t('Owned'), Icon: Disc3 },
      { to: '/calendar', label: t('Calendar'), Icon: CalendarDays }, { to: '/stats', label: t('Statistics'), Icon: ChartColumn },
      { to: '/review', label: t('Year in Review'), Icon: CalendarRange }, { to: '/activity', label: t('Activity'), Icon: Activity },
    ] },
    { label: t('Connect'), items: [{ to: '/integrations', label: t('Integrations'), Icon: Plug }] },
    { label: t('Account'), items: [
      { to: '/account', label: t('Account & security'), Icon: Settings },
      ...(user.role !== 'member' ? [{ to: '/admin', label: t('Administration'), Icon: ShieldCheck }] : []),
      { to: '/docs/', label: t('API reference'), Icon: BookOpenText, external: true },
    ] },
  ];
  return <div className="more-page">
    <PageTitle title={t('More')} />
    <nav aria-label={t('More destinations')}>{groups.map(group => <section key={group.label} className="more-group" aria-label={group.label}>
      <h2 className="more-group-label">{group.label}</h2>
      <ul className="more-list">{group.items.map(({ to, label, Icon, external }) => <li key={to}>
        {external ? <a href={to}><Icon aria-hidden /><span>{label}</span><ChevronRight aria-hidden /></a> : <Link to={to}><Icon aria-hidden /><span>{label}</span><ChevronRight aria-hidden /></Link>}
      </li>)}
        {group.label === t('Account') && <li><button type="button" onClick={signOut}><LogOut aria-hidden /><span>{t('Sign out')}</span></button></li>}
      </ul>
    </section>)}</nav>
  </div>;
}
