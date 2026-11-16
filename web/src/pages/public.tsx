import { Link, useParams, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { ChevronRight, Globe, Rss } from 'lucide-react';
import { api, ApiError, unwrap } from '../api/client';
import { BrandMark } from '../brand';
import { Cover, typeLabel } from '../media';
import { Empty, ErrorNote, Loading, Pager, retryable, useDocumentTitle } from '../ui';
import { useAuth } from '../state';
import { locale, plural, t } from '../i18n';

function Unavailable({ error, title, retry }: { error: unknown; title: string; retry: () => void }) {
  const auth = useAuth(); useDocumentTitle(title.replace(/\.$/, ''));
  const missing = error instanceof ApiError && error.status === 404;
  return <div className="public-unavailable"><h1 tabIndex={-1}>{title}</h1>
    {missing ? <p className="subtle">{t('It may be private, removed, or the address may be incorrect.')}</p> : <ErrorNote error={error} />}
    <div className="actions">{!missing && retryable(error) && <button className="button" onClick={retry}>{t('Try again')}</button>}
      {auth.session ? <Link className="button" to="/">{t('Back to home')}</Link> : <Link className="button" to="/login">{t('Sign in to Pastime')}</Link>}</div></div>;
}

function PublicFrame({ children }: { children: React.ReactNode }) {
  return <div className="public-page"><header><Link className="brand" to="/"><span className="brand-mark"><BrandMark /></span><span>{'Pastime'}</span></Link></header><main id="main" className="public-main">{children}</main></div>;
}

function PublicTitle({ title }: { title: string }) {
  useDocumentTitle(title);
  return <h1 tabIndex={-1}>{title}</h1>;
}

export function PublicListPage() {
  const { username = '', slug = '' } = useParams();
  const [params, setParams] = useSearchParams(); const cursor = params.get('cursor') || undefined;
  const query = useQuery({ queryKey: ['public-list', username, slug, cursor], retry: false, queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/public/{username}/lists/{slug}', { signal, params: { path: { username, slug }, query: { cursor, limit: 30 } } })) });
  return <PublicFrame>{query.isPending ? <Loading label={t('Loading list')} /> : query.error ? <Unavailable error={query.error} title={t('This list is not available')} retry={() => void query.refetch()} /> : <>
    <PublicTitle title={query.data.name} />
    <p className="public-byline">{query.data.kind === 'smart' ? t('Smart list') : t('List')} {t('by')} {query.data.owner_name}</p>
    {query.data.description && <p className="subtle public-description">{query.data.description}</p>}
    <p className="public-meta"><Globe size={14} aria-hidden />{plural(query.data.items.length, '{n} title on this page', '{n} titles on this page')} · {t('updated {date}', { date: new Date(query.data.updated_at).toLocaleDateString(locale) })}
      <a className="text-link" href={`/lists/${username}/${slug}.rss`}><Rss size={14} aria-hidden />{t('RSS')}</a><a className="text-link" href={`/lists/${username}/${slug}.csv`}>{t('CSV')}</a></p>
    {query.data.items.length ? <ol className="public-items">{query.data.items.map((it, i) => <li key={i}>
      <span className="rank" aria-hidden>{cursor ? '·' : i + 1}</span><Cover entry={{ title: it.title, media_type: it.media_type, image_url: it.image_url }} compact />
      <div><strong>{it.title}</strong><p className="subtle">{[typeLabel(it.media_type), it.year].filter(Boolean).join(' · ')}</p>{it.note && <p className="list-note">{it.note}</p>}</div>
    </li>)}</ol> : <Empty compact title={t('Nothing here yet')}>{t('The owner has not added any titles.')}</Empty>}
    <Pager current={cursor} next={query.data.next_cursor} change={value => setParams(value ? { cursor: value } : {})} />
  </>}</PublicFrame>;
}

export function PublicProfilePage() {
  const { username = '' } = useParams();
  const query = useQuery({ queryKey: ['public-profile', username], retry: false, queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/public/{username}/lists', { signal, params: { path: { username } } })) });
  return <PublicFrame>{query.isPending ? <Loading label={t('Loading profile')} /> : query.error ? <Unavailable error={query.error} title={t('This profile is not available')} retry={() => void query.refetch()} /> : <>
    <PublicTitle title={query.data.display_name} />
    <p className="public-byline">{plural(query.data.items.length, '{n} public list', '{n} public lists')}</p>
    {query.data.items.length ? <ul className="list-rows">{query.data.items.map(l => <li key={l.id}><Link to={`/u/${username}/${l.slug}`}>
      <span className="list-row-text"><strong>{l.name}</strong><span className="subtle">{[l.kind === 'smart' ? t('Smart list') : t('List'), l.description].filter(Boolean).join(' · ')}</span></span>
      <ChevronRight aria-hidden /></Link></li>)}</ul>
      : <Empty compact title={t('No public lists yet')}>{t('Lists appear here when they are made public.')}</Empty>}
  </>}</PublicFrame>;
}
