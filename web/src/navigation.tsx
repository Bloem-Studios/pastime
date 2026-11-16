import { useEffect, useLayoutEffect, useRef } from 'react';
import { Link, useLocation, useNavigationType, useNavigate, type LinkProps } from 'react-router-dom';
import { ArrowLeft } from 'lucide-react';
import { t } from './i18n';

type Source = { returnTo: string; returnScroll: number };
const positions = new Map<string, number>();
const allowedSource = /^\/(?:$|discover(?:\?|$)|library(?:\/|\?|$)|lists(?:\/|\?|$)|collection(?:\?|$)|recommendations(?:\?|$)|history(?:\?|$)|calendar(?:\?|$)|stats(?:\?|$)|activity(?:\?|$)|people\/|studios\/|title\/|collab(?:\/|\?|$))/;
function source(value: unknown): Source | null {
  if (!value || typeof value !== 'object') return null;
  const candidate = value as Partial<Source>;
  if (typeof candidate.returnTo !== 'string' || !allowedSource.test(candidate.returnTo) || candidate.returnTo.includes('\\') || candidate.returnTo.startsWith('//')) return null;
  return { returnTo: candidate.returnTo, returnScroll: typeof candidate.returnScroll === 'number' && Number.isFinite(candidate.returnScroll) ? Math.max(0, Math.min(candidate.returnScroll, 1_000_000)) : 0 };
}
/** Capture at click time so a long shelf retains its actual scroll position. */
export function useSourceNavigation() {
  const location = useLocation();
  return () => ({ returnTo: location.pathname + location.search, returnScroll: window.scrollY });
}
/** Ordinary anchor behavior is retained for new tabs and modified clicks. */
export function SourceLink({ to, onClick, ...props }: LinkProps) {
  const navigate = useNavigate(); const getSource = useSourceNavigation();
  return <Link {...props} to={to} onClick={event => {
    onClick?.(event);
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || props.target === '_blank') return;
    event.preventDefault(); navigate(to, { state: props.state ?? getSource() });
  }} />;
}
/** Keep the original search/library source when crossing between title and personal detail. */
export function ReturnLink(props: LinkProps) {
  const location = useLocation();
  return <SourceLink {...props} state={source(location.state) ?? undefined} />;
}
export function ContextBackLink({ fallback = '/discover', label = t('Back') }: { fallback?: string; label?: string }) {
  const location = useLocation(); const from = source(location.state);
  return <Link className="back-link" to={from?.returnTo ?? fallback} state={from ? { restoreScroll: from.returnScroll } : undefined}><ArrowLeft size={16} aria-hidden />{from ? t('Back') : label}</Link>;
}
/** Focus section navigation without resetting scroll every time a search filter changes. */
export function useRouteFocus() {
  const location = useLocation(); const action = useNavigationType();
  const params = new URLSearchParams(location.search);
  const section = location.pathname === '/account' ? params.get('tab') ?? '' : location.pathname === '/integrations' ? ['section', 'service', 'server'].map(key => params.get(key) ?? '').join(':') : '';
  const previous = useRef(location.pathname + ':' + section);
  useLayoutEffect(() => {
    const key = location.key;
    const record = () => { positions.set(key, window.scrollY); if (positions.size > 100) positions.delete(positions.keys().next().value!); };
    window.addEventListener('scroll', record, { passive: true });
    return () => { record(); window.removeEventListener('scroll', record); };
  }, [location.key]);
  useEffect(() => {
    const state = location.state as { restoreScroll?: unknown; focus?: unknown } | null;
    const restored = typeof state?.restoreScroll === 'number' && Number.isFinite(state.restoreScroll) ? Math.max(0, state.restoreScroll) : action === 'POP' ? positions.get(location.key) : undefined;
    // A link may ask for a control on the next screen, such as Discover's search field, instead of its heading.
    const focusTarget = typeof state?.focus === 'string' && action !== 'POP' ? state.focus : undefined;
    const identity = location.pathname + ':' + section;
    const changed = previous.current !== identity; previous.current = identity;
    if (!changed && restored === undefined && !focusTarget) return;
    let attempts = 0; let timer: ReturnType<typeof setTimeout>;
    const settle = () => {
      const main = document.querySelector<HTMLElement>('main');
      const heading = main?.querySelector<HTMLElement>('h1');
      if (!main?.querySelector('.loading') && heading) {
        const target = focusTarget ? main?.querySelector<HTMLElement>(focusTarget) : null;
        (target ?? heading).focus({ preventScroll: true });
        window.scrollTo(0, restored ?? 0);
        if (restored === undefined || Math.abs(window.scrollY - restored) < 2) return;
      }
      if (++attempts < 600) timer = setTimeout(settle, 50);
    };
    settle(); return () => clearTimeout(timer);
  }, [location.key, location.pathname, section, action, location.state]);
}
