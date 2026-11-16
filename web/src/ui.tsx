import { createContext, useCallback, useContext, useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode, type FormEvent } from 'react';
import { createPortal } from 'react-dom';
import { Link } from 'react-router-dom';
import * as Dialog from '@radix-ui/react-dialog';
import { ArrowLeft, ArrowRight, Check, CircleAlert, Copy, X } from 'lucide-react';
import { ApiError, errorMessage } from './api/client';
import { t, locale } from './i18n';

/** Names the browser tab after the screen. `PageTitle` does this; screens without one call it directly. */
export function useDocumentTitle(title?: string) {
  useEffect(() => { document.title = title ? `${title} · Pastime` : 'Pastime'; }, [title]);
}

/** The screen's title row. The sidebar already names the section, so `eyebrow` is accepted but not shown. */
export function PageTitle({ title, description, actions }: { title: string; description?: string; actions?: ReactNode; eyebrow?: string }) {
  useDocumentTitle(title);
  return <header className="page-heading"><div><h1 tabIndex={-1}>{title}</h1>{description && <p>{description}</p>}</div>{actions && <div className="actions">{actions}</div>}</header>;
}
export function Field({ label, hint, children }: { label: string; hint?: string; children: (id: string, hintID?: string) => ReactNode }) {
  const id = useId(); const hintID = hint ? `${id}-hint` : undefined;
  return <div className="field"><label htmlFor={id}>{label}</label>{children(id, hintID)}{hint && <p className="field-hint" id={hintID}>{hint}</p>}</div>;
}
export function ErrorNote({ error }: { error: unknown }) {
  if (!error) return null;
  return <div className="error-note" role="alert"><CircleAlert size={18} aria-hidden /><span>{errorMessage(error)}</span></div>;
}
export function Loading({ label = t('Loading') }: { label?: string }) {
  return <div className="loading" role="status" aria-label={label}><span className="sr-only">{label}</span><div className="skeleton skeleton-title" /><div className="skeleton skeleton-line" /><div className="skeleton skeleton-block" /></div>;
}
/** An empty block that keeps its frame. `compact` fits a section or a dialog: one line with its action beside it. */
export function Empty({ title, children, action, compact = false, heading = 'h2' }: { title: string; children?: ReactNode; action?: ReactNode; compact?: boolean; heading?: 'h1' | 'h2' | 'h3' }) {
  const Heading = heading;
  return <div className={`empty-state${compact ? ' compact' : ''}`}><div className="empty-text"><Heading tabIndex={heading === 'h1' ? -1 : undefined}>{title}</Heading>{children && <p>{children}</p>}</div>{action && <div className="empty-actions">{action}</div>}</div>;
}
/** Whether running the same request again could succeed: not for a missing, forbidden or invalid request. */
export function retryable(error: unknown) {
  return !(error instanceof ApiError) || error.status === 0 || error.status === 408 || error.status === 409 || error.status === 429 || error.status >= 500;
}
/** A failed block. With `title` it keeps the page's heading; Retry shows only when retrying can help. */
export function Failed({ error, retry, title }: { error: unknown; retry?: () => void; title?: string }) {
  return <div className="failed-state">{title && <PageTitle title={title} />}<ErrorNote error={error} />{retry && retryable(error) && <button className="button" onClick={retry}>{t('Try again')}</button>}</div>;
}
/** The screen for an address that leads nowhere, with the ways forward. */
export function NotFound() {
  return <div className="not-found"><PageTitle title={t('Page not found')} /><p className="subtle">{t('This destination does not exist.')}</p><div className="actions"><Link className="button primary" to="/">{t('Go home')}</Link><Link className="button" to="/library">{t('Open your library')}</Link></div></div>;
}
/** A read-only value with a Copy button joined to it. */
export function CopyField({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false);
  useEffect(() => { if (!copied) return; const id = setTimeout(() => setCopied(false), 2000); return () => clearTimeout(id); }, [copied]);
  return <div className="copy-field"><input readOnly aria-label={label} value={value} spellCheck={false} onFocus={e => e.currentTarget.select()} /><button type="button" className="button" onClick={async () => { try { await navigator.clipboard.writeText(value); setCopied(true); } catch { /* the value stays selectable */ } }}>{copied ? <Check aria-hidden /> : <Copy aria-hidden />}{copied ? t('Copied') : t('Copy')}</button></div>;
}
/** Underline tabs on one rule. On a phone the row scrolls sideways and keeps the current tab in view. */
export function Tabs({ label, items, current }: { label: string; items: { value: string; label: string; to: string }[]; current: string }) {
  const nav = useRef<HTMLElement>(null);
  useEffect(() => {
    const row = nav.current; const tab = row?.querySelector<HTMLElement>('[aria-current="page"]');
    if (row && tab && row.scrollWidth > row.clientWidth) row.scrollLeft = tab.offsetLeft - (row.clientWidth - tab.offsetWidth) / 2;
  }, [current]);
  return <nav className="tabs" aria-label={label} ref={nav}>{items.map(item => <Link key={item.value} to={item.to} aria-current={item.value === current ? 'page' : undefined}>{item.label}</Link>)}</nav>;
}
export function useAction<T, R>(task: (value: T, key: string) => Promise<R>) {
  const [pending, setPending] = useState(false); const [error, setError] = useState<unknown>(null); const [saved, setSaved] = useState(false);
  const busy = useRef(false); const intent = useRef<{ fingerprint: string; key: string } | null>(null);
  async function run(value: T) {
    if (busy.current) return;
    busy.current = true; setPending(true); setError(null); setSaved(false);
    const fingerprint = JSON.stringify(value);
    if (!intent.current || intent.current.fingerprint !== fingerprint) intent.current = { fingerprint, key: crypto.randomUUID() };
    try { const out = await task(value, intent.current.key); intent.current = null; setSaved(true); return out; }
    catch (e) { setError(e); }
    finally { busy.current = false; setPending(false); }
  }
  return { pending, error, saved, run, reset: () => { setError(null); setSaved(false); } };
}
export const submit = (action: () => unknown) => (event: FormEvent) => { event.preventDefault(); action(); };

type Confirmation = { title: string; description: string; label?: string; danger?: boolean };
const ConfirmContext = createContext<(options: Confirmation) => Promise<boolean>>(async () => false);
export const useConfirm = () => useContext(ConfirmContext);
export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [options, setOptions] = useState<Confirmation | null>(null); const resolve = useRef<((result: boolean) => void) | null>(null);
  const finish = (value: boolean) => { resolve.current?.(value); resolve.current = null; setOptions(null); };
  const ask = useCallback((value: Confirmation) => new Promise<boolean>(done => { resolve.current?.(false); resolve.current = done; setOptions(value); }), []);
  return <ConfirmContext.Provider value={ask}>{children}<Dialog.Root open={!!options} onOpenChange={open => { if (!open) finish(false); }}><Dialog.Portal><Dialog.Overlay className="dialog-overlay confirmation-overlay" /><Dialog.Content className="dialog-content confirmation-content"><Dialog.Title>{options?.title}</Dialog.Title><Dialog.Description>{options?.description}</Dialog.Description><div className="dialog-actions"><button className="button" onClick={() => finish(false)}>{t('Cancel')}</button><button className={`button ${options?.danger ? 'danger' : 'primary'}`} onClick={() => finish(true)}>{options?.label ?? 'Continue'}</button></div></Dialog.Content></Dialog.Portal></Dialog.Root></ConfirmContext.Provider>;
}
/**
 * A dialog whose body scrolls under a sticky footer. A footer (`.dialog-actions`) that submits a form and
 * offers no other way out gets a Cancel button, so every form dialog can be left without saving.
 */
export function Modal({ title, description, children, onClose, dirty = false, busy = false, wide = false }: { title: string; description: string; children: ReactNode; onClose: () => void; dirty?: boolean; busy?: boolean; wide?: boolean }) {
  const confirm = useConfirm();
  const content = useRef<HTMLDivElement | null>(null); const [root, setRoot] = useState<HTMLDivElement | null>(null);
  const attach = useCallback((node: HTMLDivElement | null) => { content.current = node; setRoot(node); }, []);
  const [footers, setFooters] = useState<HTMLElement[]>([]);
  const previousFocus = useRef(document.activeElement instanceof HTMLElement ? document.activeElement : null);
  async function close() {
    if (busy) return;
    if (!dirty || await confirm({ title: t('Discard changes?'), description: t('Your unsaved changes will be lost.'), label: t('Discard changes'), danger: true })) onClose();
  }
  useLayoutEffect(() => {
    if (!root) return;
    const scan = () => {
      const found = [...root.querySelectorAll<HTMLElement>('.dialog-actions')].filter(footer => footer.querySelector('button[type="submit"]') && !footer.querySelector('button:not(.primary):not(.danger):not([data-dialog-cancel]), a'));
      setFooters(now => now.length === found.length && now.every((footer, i) => footer === found[i]) ? now : found);
    };
    scan();
    const observer = new MutationObserver(scan); observer.observe(root, { childList: true, subtree: true });
    return () => observer.disconnect();
  }, [root]);
  return <Dialog.Root open onOpenChange={open => { if (!open) void close(); }}><Dialog.Portal><Dialog.Overlay className="dialog-overlay" /><Dialog.Content ref={attach} className={`dialog-content ${wide ? 'wide' : ''}`} onOpenAutoFocus={event => { const input = content.current?.querySelector<HTMLElement>('input:not([type="hidden"]):not(:disabled), select:not(:disabled), textarea:not(:disabled)'); if (input) { event.preventDefault(); input.focus({ preventScroll: true }); } }} onCloseAutoFocus={event => { event.preventDefault(); const target = previousFocus.current; if (target?.isConnected) target.focus(); else document.querySelector<HTMLElement>('main h1')?.focus(); }} onEscapeKeyDown={event => { if (busy) event.preventDefault(); }} onPointerDownOutside={event => { if (busy) event.preventDefault(); }}><div className="dialog-heading"><Dialog.Title>{title}</Dialog.Title><button className="icon-button" aria-label={t('Close dialog')} disabled={busy} onClick={() => void close()}><X aria-hidden /></button></div><Dialog.Description>{description}</Dialog.Description>{children}
    {footers.map((footer, i) => createPortal(<button type="button" className="button dialog-cancel" data-dialog-cancel disabled={busy} onClick={() => void close()}>{t('Cancel')}</button>, footer, `cancel-${i}`))}</Dialog.Content></Dialog.Portal></Dialog.Root>;
}
export function Secret({ title, value, description, onClose }: { title: string; value: string; description: string; onClose: () => void }) {
  const [copied, setCopied] = useState(false); const [failed, setFailed] = useState(false);
  return <Modal title={title} description={description} onClose={onClose}><Field label={t('One-time secret')} hint={t('Store or deliver this privately. It cannot be displayed again.')}>{id => <textarea id={id} className="secret-value" readOnly value={value} rows={4} onFocus={e => e.currentTarget.select()} spellCheck={false} />}</Field>{failed && <p role="alert">{t('Clipboard access was denied. Select and copy the value above.')}</p>}<div className="dialog-actions"><button className="button" onClick={onClose}>{t('Done')}</button><button className="button primary" onClick={async () => { try { await navigator.clipboard.writeText(value); setCopied(true); setFailed(false); } catch { setFailed(true); } }}>{copied ? <Check aria-hidden /> : <Copy aria-hidden />}{copied ? t('Copied') : t('Copy')}</button></div></Modal>;
}
/** A status or state as people read it, in the interface language. */
export function statusText(value: string): string {
  const labels: Record<string, string> = {
    planned: t('Planned'), in_progress: t('In progress'), completed: t('Completed'), paused: t('Paused'), dropped: t('Dropped'), rated: t('Rated only'),
    queued: t('Queued'), running: t('Running'), succeeded: t('Succeeded'), partial_success: t('Needs repair'), failed: t('Failed'), cancelled: t('Cancelled'), rolled_back: t('Rolled back'),
    active: t('Active'), pending: t('Pending'), accepted: t('Accepted'), dismissed: t('Dismissed'), disabled: t('Disabled'), revoked: t('Revoked'),
    temporarily_unavailable: t('Temporarily unavailable'), search_only: t('Search only'), local_only: t('Local catalog only'), healthy: t('Healthy'), unknown: t('Not checked'), credentials_rejected: t('Credentials rejected'), rate_limited: t('Rate limited'), quota_limited: t('Quota limit reached'), not_configured: t('Not configured'), invalid_response: t('Invalid response'), available: t('Available'), no_match: t('No match'), ambiguous: t('Ambiguous match'), claimed: t('Claimed'), expired: t('Expired'), ready: t('Ready'), degraded: t('Degraded'), blocked: t('Blocked'), unavailable: t('Unavailable'),
    retrying: t('Retrying'), dead: t('Dead'), resolved: t('Resolved'), ignored: t('Ignored'), linked: t('Linked'), owner: t('Owner'), admin: t('Admin'), member: t('Member'),
  };
  const plain = value.replaceAll('_', ' ');
  return labels[value] ?? plain.charAt(0).toUpperCase() + plain.slice(1);
}
export function Status({ value }: { value: string }) { return <span className={`status status-${value.replaceAll('_', '-')}`}>{statusText(value)}</span>; }
export function Timestamp({ value }: { value?: string | null }) { return value ? <time dateTime={value} title={new Date(value).toLocaleString(locale)}>{new Date(value).toLocaleDateString(locale, { month: 'short', day: 'numeric', year: 'numeric' })}</time> : <span>{t('Not yet')}</span>; }
export function LoadMore({ available, pending, onLoad }: { available: boolean; pending: boolean; onLoad: () => void }) { return available ? <div className="load-more"><button className="button" disabled={pending} onClick={onLoad}>{pending ? t('Loading…') : t('Load more')}</button></div> : null; }

export function Pager({ current, next, change }: { current?: string | null; next?: string | null; change: (cursor: string) => void }) {
  if (!current && !next) return null;
  return <nav className="pager" aria-label={t('Result pages')}><button className="button" disabled={!current} onClick={() => change('')}><ArrowLeft aria-hidden />{t('First page')}</button><span>{t('Use Back to revisit a previous page.')}</span><button className="button" disabled={!next} onClick={() => change(next!)}>{t('Next page')}<ArrowRight aria-hidden /></button></nav>;
}
