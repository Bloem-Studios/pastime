import {TrackingProfileProvider,useTrackingProfile} from './profileContext';
import { ProfileConnectionNotice } from './profileConnection';
import { useRouteFocus } from './navigation';
import { SavedViewLinks } from './savedViews';
import { createContext, useContext, useEffect, useMemo, useState } from 'react';
import { Link, NavLink, Navigate, Outlet, useLocation, useNavigate, useNavigation } from 'react-router-dom';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import * as Dropdown from '@radix-ui/react-dropdown-menu';
import * as Dialog from '@radix-ui/react-dialog';
import {
  Activity, ArrowLeft, Disc3, BookOpenText, CalendarDays, ChartColumn, ChevronDown, Compass, Database, Download, Ellipsis, Gauge,
  History, House, Layers, LibraryBig, ListChecks, LogOut, Mail, Menu, Monitor, Moon, PanelLeftClose, PanelLeftOpen, Plug, Plus, ScrollText, Search, Settings, ShieldCheck,
  Sun, Unplug, Users, X, type LucideIcon,
} from 'lucide-react';
import { api, unwrap } from './api/client';
import { BrandMark } from './brand';
import { privateKey, useAuth, useDirtyState, useUser } from './state';
import { readTheme, saveTheme, type ThemePreference } from './theme';
import { ErrorNote, Failed, Loading, statusText, useAction, useConfirm } from './ui';
import { Cover, entryPath, typeLabel } from './media';
import { usePreferences } from './organize';
import { overviewQueue, queueLabel, useAdminOverview } from './queueStatus';
import { languageSetting, setLanguage, t, tx, type Language } from './i18n';

type Item = { to: string; label: string; Icon: LucideIcon; end?: boolean; external?: boolean };
type Group = { label: string; items: Item[] };

const personal: Group[] = [
  { label: t('Watch'), items: [
    { to: '/', label: t('Home'), Icon: House, end: true },
    { to: '/discover', label: t('Discover'), Icon: Compass },
    { to: '/library', label: t('Library'), Icon: LibraryBig },
    { to: '/calendar', label: t('Calendar'), Icon: CalendarDays },
  ] },
  { label: t('Your record'), items: [
    { to: '/history', label: t('History'), Icon: History },
    { to: '/stats', label: t('Statistics'), Icon: ChartColumn },
    { to: '/lists', label: t('Lists'), Icon: Layers },
    { to: '/collection', label: t('Owned'), Icon: Disc3 },
    { to: '/activity', label: t('Activity'), Icon: Activity },
  ] },
  { label: t('Connect'), items: [{ to: '/integrations', label: t('Integrations'), Icon: Plug }] },
];
const mobile: Item[] = [personal[0].items[0], personal[0].items[1], personal[0].items.find(item => item.to === '/library')!, personal[1].items[0], { to: '/more', label: t('More'), Icon: Ellipsis }];
export const administration: Group[] = [
  { label: t('Overview'), items: [
    { to: '/admin', label: t('Dashboard'), Icon: Gauge, end: true },
    { to: '/admin/jobs', label: t('Jobs'), Icon: ListChecks },
  ] },
  { label: t('Catalog'), items: [
    { to: '/admin/providers', label: t('Providers'), Icon: Unplug },
    { to: '/admin/catalog', label: t('Catalog'), Icon: Database },
    { to: '/admin/imports', label: t('Imports'), Icon: Download },
  ] },
  { label: t('Access'), items: [
    { to: '/admin/users', label: t('Users'), Icon: Users },
    { to: '/admin/invitations', label: t('Invitations'), Icon: Mail },
    { to: '/admin/audit', label: t('Audit log'), Icon: ScrollText },
  ] },
  { label: t('Developer'), items: [{ to: '/docs/', label: t('API reference'), Icon: BookOpenText, external: true }] },
];

const Workspace = createContext({ addMedia() {}, signOut() {} });
export const useWorkspace = () => useContext(Workspace);

/** Pages reached from More: on the phone the More tab stays marked while one is open. */
const moreSections = /^\/(?:more|collection|lists|collab|calendar|stats|review|activity|integrations|account|device|admin)(?:\/|$)/;
/** Pages with their own way to add a title, where the header's Add would be a second one. */
const ownAdd = /^\/(?:discover|library)(?:\/?$|\?)/;
const shortcut = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent) ? '⌘K' : 'Ctrl K';

export function ProtectedLayout() {
  const auth = useAuth(); const location = useLocation();
  if (auth.loading) return <main id="main" className="auth-loading"><Loading label={t('Checking your session')} /></main>;
  if (auth.error) return <main id="main" className="auth-loading"><Failed error={auth.error} retry={auth.retry} /></main>;
  if (!auth.session) return <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />;
  return <TrackingProfileProvider key={auth.session.user.id}><ProfileShell/></TrackingProfileProvider>;
}

function ProfileShell(){const {active}=useTrackingProfile();return <Shell key={active.id}/>;}

export function AdminGuard() {
  const user = useUser();
  if (user.role !== 'member') return <Outlet />;
  return <div className="empty-state"><h1>{t('Administration is restricted')}</h1><p>{t('Your account does not have administration access.')}</p><Link className="button" to="/">{t('Back to home')}</Link></div>;
}

function useRail() {
  const [rail, setRail] = useState(() => document.documentElement.dataset.rail === 'true');
  useEffect(() => {
    if (rail) document.documentElement.dataset.rail = 'true'; else delete document.documentElement.dataset.rail;
    try { localStorage.setItem('pastime-rail', String(rail)); } catch { /* per-browser convenience only */ }
  }, [rail]);
  return [rail, setRail] as const;
}

function NavGroups({ groups, label }: { groups: Group[]; label: string }) {
  return <div className="nav-scroll">{groups.map(group => (
    <div className="nav-group" key={group.label}>
      <p className="nav-label">{group.label}</p>
      <nav aria-label={`${label}: ${group.label}`}>{group.items.map(({ to, label: text, Icon, end, external }) => external
        ? <a key={to} href={to} title={text}><Icon size={20} aria-hidden /><span className="nav-text">{text}</span></a>
        : <NavLink key={to} to={to} end={end} title={text}><Icon size={20} aria-hidden /><span className="nav-text">{text}</span></NavLink>)}
      </nav>
    </div>
  ))}{groups === personal && <SavedViewLinks />}</div>;
}

// The account's interface language wins over this browser's, so it follows the person.
function useAccountLanguage() {
  const { prefs, loaded } = usePreferences();
  useEffect(() => {
    if (loaded && prefs.language && prefs.language !== languageSetting()) setLanguage(prefs.language as Language);
  }, [loaded, prefs.language]);
}

function Shell() {
  useAccountLanguage();
  const user = useUser(); const auth = useAuth(); const navigate = useNavigate(); const location = useLocation(); const navigation = useNavigation();
  const confirm = useConfirm(); const dirty = useDirtyState();
  const admin = location.pathname.startsWith('/admin');
  // Home and title pages put artwork under a transparent header.
  const home = location.pathname === '/';
  const [rail, setRail] = useRail();
  const [searchOpen, setSearchOpen] = useState(false);
  const [commandOpen, setCommandOpen] = useState(false); const [drawer, setDrawer] = useState(false);
  useRouteFocus();
  const [scrolled, setScrolled] = useState(false);
  useEffect(() => {
    if (!home) return;
    const onScroll = () => setScrolled(window.scrollY > 48);
    onScroll();
    window.addEventListener('scroll', onScroll, { passive: true });
    return () => window.removeEventListener('scroll', onScroll);
  }, [home]);
  const logout = useAction(async (_: void) => { unwrap(await api.POST('/api/v1/logout')); dirty.clear(); await auth.clear(); navigate('/login', { replace: true }); });
  async function signOut() {
    if (dirty.isDirty() && !await confirm({ title: t('Sign out and discard changes?'), description: t('Your unsaved changes will be lost.'), label: t('Sign out'), danger: true })) return;
    void logout.run();
  }
  useEffect(() => {
    setDrawer(false);

  }, [location.pathname]);
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault();
        if (admin) setCommandOpen(true); else setSearchOpen(true);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [admin]);
  const profiles=useTrackingProfile();
  async function changeTrackingProfile(id:string){
    if(dirty.isDirty() && !await confirm({title:t('Discard changes?'),description:t('You have unsaved changes on this page.'),label:t('Discard changes'),danger:true}))return;
    dirty.clear();await profiles.select(id);
  }
  const groups = admin ? administration : personal;
  const isAdmin = user.role !== 'member';
  // Add opens Discover with its search field focused: that is where a new title is found and added.
  const addMedia = () => navigate('/discover', { state: { focus: '.discover-search input' } });

  return <Workspace.Provider value={{ addMedia, signOut: () => void signOut() }}>
    <div className="app-shell">
      <a className="skip-link" href="#main">{t('Skip to content')}</a>
      <aside className="sidebar">
        <Link className="brand" to={admin ? '/admin' : '/'}><span className="brand-mark"><BrandMark /></span><span className="nav-text">{admin ? t('Console') : 'Pastime'}</span></Link>
        <NavGroups groups={groups} label={admin ? t('Administration') : t('Workspace')} />
        <div className="sidebar-footer">
          {admin
            ? <><BuildLabel /><Link to="/" title={t('Back to app')}><ArrowLeft size={20} aria-hidden /><span className="nav-text">{t('Back to app')}</span></Link></>
            : <><NavLink to="/account" title={t('Account')}><Settings size={20} aria-hidden /><span className="nav-text">{t('Account')}</span></NavLink>
              {isAdmin && <Link to="/admin" title={t('Administration')}><ShieldCheck size={20} aria-hidden /><span className="nav-text">{t('Administration')}</span></Link>}</>}
          <button className="collapse-toggle" title={rail ? t('Expand navigation') : t('Collapse navigation')} aria-label={rail ? t('Expand navigation') : t('Collapse navigation')} aria-expanded={!rail} onClick={() => setRail(!rail)}>{rail ? <PanelLeftOpen size={20} aria-hidden /> : <PanelLeftClose size={20} aria-hidden />}<span className="nav-text">{t('Collapse navigation')}</span></button>
        </div>
      </aside>
      <div className="workspace">
        <header className={`topbar ${home ? 'over-hero' : ''} ${home && scrolled ? 'scrolled' : ''}`}>
          <Link className="mobile-brand" aria-label={admin ? t('Console') : 'Pastime'} to={admin ? '/admin' : '/'}><span className="brand-mark"><BrandMark /></span><span>{admin ? t('Console') : 'Pastime'}</span></Link>
          {admin
            ? <button className="global-search" aria-label={t('Go to an administration section')} aria-keyshortcuts="Control+K Meta+K" onClick={() => setCommandOpen(true)}><Search size={18} aria-hidden /><span>{t('Jump to a section')}</span><kbd>{shortcut}</kbd></button>
            : <button className="global-search" aria-label={t('Search your library')} aria-keyshortcuts="Control+K Meta+K" onClick={() => setSearchOpen(true)}><Search size={18} aria-hidden /><span>{t('Search your library')}</span><kbd>{shortcut}</kbd></button>}
          <div className="top-actions">
            {!admin && profiles.profiles.length > 1 && <label className="tracking-profile-picker"><span className="sr-only">{t('Profile')}</span><select aria-label={t('Profile')} value={profiles.active.id} disabled={profiles.switching} onChange={event=>void changeTrackingProfile(event.target.value)}>{profiles.profiles.map(profile=><option key={profile.id} value={profile.id}>{profile.name}</option>)}</select></label>}
            {admin ? <JobActivity /> : !ownAdd.test(location.pathname) && <button className="button add-media" onClick={addMedia}><Plus aria-hidden /><span>{t('Add a title')}</span></button>}
            <AccountMenu onSignOut={() => void signOut()} pending={logout.pending} />
            {admin && <Dialog.Root open={drawer} onOpenChange={setDrawer}>
              <Dialog.Trigger className="icon-button admin-menu" aria-label={t('Open administration navigation')}><Menu aria-hidden /></Dialog.Trigger>
              <Dialog.Portal><Dialog.Overlay className="drawer-overlay" /><Dialog.Content className="drawer-content">
                <div className="dialog-heading"><Dialog.Title>{t('Administration')}</Dialog.Title><Dialog.Close className="icon-button" aria-label={t('Close navigation')}><X aria-hidden /></Dialog.Close></div>
                <Dialog.Description className="sr-only">{t('Choose an administration page.')}</Dialog.Description>
                <NavGroups groups={administration} label={t('Administration')} />
                <Link className="button" to="/"><ArrowLeft aria-hidden />{t('Back to app')}</Link>
              </Dialog.Content></Dialog.Portal>
            </Dialog.Root>}
          </div>
        </header>
        {navigation.state !== 'idle' && <div className="navigation-pending" role="status">{t('Opening page…')}</div>}
        <main id="main" className="main-content" tabIndex={-1}><ErrorNote error={logout.error} />{!admin && ['/', '/calendar'].includes(location.pathname) && <ProfileConnectionNotice />}<div className="page-enter" key={location.pathname}><Outlet /></div></main>
      </div>
      {!admin && <nav className="bottom-nav" aria-label={t('Mobile workspace')}>{mobile.map(({ to, label, Icon, end }) => <NavLink key={to} to={to} end={end} className={({ isActive }) => isActive || (to === '/more' && moreSections.test(location.pathname)) ? 'active' : undefined} aria-current={to === '/more' && moreSections.test(location.pathname) ? 'page' : undefined}><Icon size={22} aria-hidden /><span>{label}</span></NavLink>)}</nav>}
    </div>
    {searchOpen && <LibrarySearch onClose={() => setSearchOpen(false)} />}
    {commandOpen && <CommandPalette groups={administration} onClose={() => setCommandOpen(false)} />}
  </Workspace.Provider>;
}

function AccountMenu({ onSignOut, pending }: { onSignOut: () => void; pending: boolean }) {
  const user = useUser();
  const [theme, setTheme] = useState<ThemePreference>(readTheme);
  const choose = (value: ThemePreference) => { setTheme(value); saveTheme(value); };
  const themes: [ThemePreference, string, LucideIcon][] = [['dark', t('Dark'), Moon], ['light', t('Light'), Sun], ['system', t('Auto'), Monitor]];
  return <Dropdown.Root>
    <Dropdown.Trigger className="account-trigger" aria-label={t('Account menu')}><span className="avatar">{Array.from(user.display_name)[0]?.toUpperCase()}</span><span className="account-name">{user.display_name}</span><ChevronDown size={14} aria-hidden /></Dropdown.Trigger>
    <Dropdown.Portal><Dropdown.Content className="dropdown-menu" sideOffset={10} align="end">
      <Dropdown.Label className="menu-label">{tx('Signed in as {user}', { user: <strong>@{user.username}</strong> })}</Dropdown.Label>
      <div className="theme-options" role="group" aria-label={t('Theme')}>{themes.map(([value, label, Icon]) => <button key={value} aria-pressed={theme === value} onClick={() => choose(value)}><Icon aria-hidden />{label}</button>)}</div>
      <Dropdown.Separator className="menu-separator" />
      <Dropdown.Item asChild><a href="/docs/"><BookOpenText aria-hidden />{t('API reference')}</a></Dropdown.Item>
      <Dropdown.Item disabled={pending} onSelect={onSignOut}><LogOut aria-hidden />{t('Sign out')}</Dropdown.Item>
    </Dropdown.Content></Dropdown.Portal>
  </Dropdown.Root>;
}

function BuildLabel() {
  const info = useQuery({ queryKey: ['info'], queryFn: async () => unwrap(await api.GET('/api/v1/info')), staleTime: Infinity });
  return <span className="build mono" title={t('Server build')}>{info.data ? t('Build {version}', { version: info.data.version }) : ' '}</span>;
}

/** Live queue state in the admin header, from the cached dashboard overview. */
function JobActivity() {
  const overview = useAdminOverview();
  if (!overview.data) return null;
  const queue = overviewQueue(overview.data); const label = queueLabel(queue);
  return <Link to={queue.to} className={`health-pill ${queue.tone === 'ok' ? '' : queue.tone}`} aria-label={`${t('Background jobs')}: ${label}`}>
    <span className="dot" aria-hidden />{label}
  </Link>;
}

/** ⌘K: titles in the library as you type, chosen with the arrow keys; the last row searches the whole library. */
function LibrarySearch({ onClose }: { onClose: () => void }) {
  const user = useUser(); const navigate = useNavigate();
  const [text, setText] = useState(''); const [q, setQ] = useState(''); const [active, setActive] = useState(0);
  useEffect(() => { const id = setTimeout(() => { setQ(text.trim()); setActive(0); }, 150); return () => clearTimeout(id); }, [text]);
  const results = useQuery({ queryKey: privateKey(user.id, 'library', 'quick-search', q), staleTime: 30_000, placeholderData: keepPreviousData,
    queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/library', { signal, params: { query: { q: q || undefined, limit: q ? 8 : 5, sort: 'activity' } } })).items });
  const items = results.data ?? [];
  const searchAll = text.trim();
  const count = items.length + (searchAll ? 1 : 0);
  function go(index: number) {
    if (index < items.length) navigate(entryPath(items[index]));
    else if (searchAll) navigate(`/library?q=${encodeURIComponent(searchAll)}`);
    else return;
    onClose();
  }
  const option = (i: number) => `quick-search-${i}`;
  return <Dialog.Root open onOpenChange={open => { if (!open) onClose(); }}>
    <Dialog.Portal><Dialog.Overlay className="dialog-overlay" /><Dialog.Content className="command" aria-describedby={undefined}>
      <Dialog.Title className="sr-only">{t('Search your library')}</Dialog.Title>
      <div className="command-input"><Search size={18} aria-hidden /><input autoFocus role="combobox" aria-expanded={count > 0} aria-controls="quick-search-results" aria-activedescendant={count ? option(active) : undefined} aria-autocomplete="list"
        placeholder={t('Search titles')} aria-label={t('Search titles')} maxLength={100} value={text} onChange={e => setText(e.target.value)}
        onKeyDown={e => {
          if (e.key === 'ArrowDown') { e.preventDefault(); setActive(i => Math.min(i + 1, count - 1)); }
          if (e.key === 'ArrowUp') { e.preventDefault(); setActive(i => Math.max(i - 1, 0)); }
          if (e.key === 'Enter') { e.preventDefault(); go(q === text.trim() ? active : items.length); }
        }} /></div>
      {!q && items.length > 0 && <p className="command-group">{t('Recent activity')}</p>}
      {count > 0 ? <ul role="listbox" id="quick-search-results" aria-label={t('Titles')}>
        {items.map((entry, i) => <li key={entry.id} id={option(i)} role="option" aria-selected={i === active}>
          <a href={entryPath(entry)} data-active={i === active} onMouseEnter={() => setActive(i)} onClick={e => { e.preventDefault(); go(i); }}>
            <Cover entry={entry} compact /><span className="command-text"><strong>{entry.title}</strong><span>{[typeLabel(entry.media_type), entry.year, statusText(entry.status)].filter(Boolean).join(' · ')}</span></span></a></li>)}
        {searchAll && <li id={option(items.length)} role="option" aria-selected={active === items.length}>
          <a href={`/library?q=${encodeURIComponent(searchAll)}`} data-active={active === items.length} onMouseEnter={() => setActive(items.length)} onClick={e => { e.preventDefault(); go(items.length); }}>
            <span className="command-glyph"><Search size={16} aria-hidden /></span><span className="command-text"><strong>{t('Search the library for “{value}”', { value: searchAll })}</strong></span></a></li>}
      </ul> : <p className="empty">{results.isPending ? t('Loading') : q ? t('No titles match.') : t('Your library is empty.')}</p>}
    </Dialog.Content></Dialog.Portal>
  </Dialog.Root>;
}

function CommandPalette({ groups, onClose }: { groups: Group[]; onClose: () => void }) {
  const navigate = useNavigate();
  const [query, setQuery] = useState(''); const [active, setActive] = useState(0);
  const items = useMemo(() => groups.flatMap(g => g.items.map(item => ({ ...item, group: g.label })))
    .filter(item => (item.label + ' ' + item.group).toLowerCase().includes(query.trim().toLowerCase())), [groups, query]);
  function go(index: number) {
    const item = items[index];
    if (!item) return;
    onClose();
    if (item.external) window.location.assign(item.to); else navigate(item.to);
  }
  return <Dialog.Root open onOpenChange={open => { if (!open) onClose(); }}>
    <Dialog.Portal><Dialog.Overlay className="dialog-overlay" /><Dialog.Content className="command" aria-describedby={undefined}>
      <Dialog.Title className="sr-only">{t('Jump to a section')}</Dialog.Title>
      <input autoFocus placeholder={t('Jump to…')} aria-label={t('Section name')} value={query}
        onChange={e => { setQuery(e.target.value); setActive(0); }}
        onKeyDown={e => {
          if (e.key === 'ArrowDown') { e.preventDefault(); setActive(i => Math.min(i + 1, items.length - 1)); }
          if (e.key === 'ArrowUp') { e.preventDefault(); setActive(i => Math.max(i - 1, 0)); }
          if (e.key === 'Enter') { e.preventDefault(); go(active); }
        }} />
      {items.length ? <ul role="listbox" aria-label={t('Sections')}>{items.map(({ to, label, Icon, group }, i) => <li key={to} role="option" aria-selected={i === active}>
        <a href={to} data-active={i === active} onMouseEnter={() => setActive(i)} onClick={e => { e.preventDefault(); go(i); }}><Icon size={18} aria-hidden />{label}<span className="group">{group}</span></a>
      </li>)}</ul> : <p className="empty">{t('No section matches.')}</p>}
    </Dialog.Content></Dialog.Portal>
  </Dialog.Root>;
}
