import { useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Search } from 'lucide-react';
import { api, unwrap } from '../api/client';
import { privateKey, useUser } from '../state';
import { Cover } from '../media';
import { copySummary, formatLabel, formats, money, type Copy } from '../organize';
import { Empty, Failed, Loading, PageTitle, Pager } from '../ui';
import { t, locale } from '../i18n';

export function CollectionPage() {
  const user = useUser(); const [params, setParams] = useSearchParams();
  const format = (params.get('format') || undefined) as Copy['format'] | undefined;
  const q = params.get('q') || ''; const [search, setSearch] = useState(q);
  const cursor = params.get('cursor') || undefined;
  const all = useQuery({ queryKey: privateKey(user.id, 'collection', format, cursor, q), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/collection', { signal, params: { query: { format, cursor, limit: 30, q } } })) });
  const copies = all.data?.items ?? [];
  const counts = new Map(Object.entries(all.data?.formats ?? {}));
  const spent = new Map(Object.entries(all.data?.spent ?? {}));
  const titles = all.data?.titles ?? 0;
  const total = all.data?.total ?? 0;
  const empty = !!all.data && !counts.size;
  const filtered = !!format || !!q;
  return <div className="collection-page">
    <PageTitle title={t('Owned')} />
    {all.isPending ? <Loading label={t('Loading your collection')} /> : all.error ? <Failed error={all.error} retry={() => void all.refetch()} /> : empty
      ? <Empty title={t('Nothing recorded yet')} action={<Link className="button" to="/library">{t('Open your library')}</Link>}>{t('Open any title and choose Add a copy to record the discs, books, records and games you own.')}</Empty>
      : <>
        <dl className="figure-strip">
          <div><dt>{format ? t('{format} copies', { format: formatLabel(format) }) : t('Copies')}</dt><dd>{total.toLocaleString(locale)}</dd></div>
          <div><dt>{t('Titles with a copy')}</dt><dd>{titles.toLocaleString(locale)}</dd></div>
          <div><dt>{t('Spent, where a price is recorded')}</dt><dd>{spent.size ? [...spent].map(([cur, cents]) => money(cents, cur)).join(' + ') : <span className="subtle">—</span>}</dd></div>
        </dl>
        <div className="collection-tools">
          <form className="search-form" role="search" onSubmit={e => { e.preventDefault(); const next = new URLSearchParams(params); next.delete('cursor'); if (search.trim()) next.set('q', search.trim()); else next.delete('q'); setParams(next); }}>
            <Search size={18} aria-hidden /><input aria-label={t('Search owned titles')} placeholder={t('Search owned titles')} maxLength={100} value={search} onChange={e => setSearch(e.target.value)} /><button className="button small">{t('Search')}</button></form>
          <div className="chip-filter" role="group" aria-label={t('Format')}>
            <button aria-pressed={!format} onClick={() => setParams(q ? { q } : {})}>{t('All')}<span className="count">{[...counts.values()].reduce((a, b) => a + b, 0)}</span></button>
            {formats.filter(([v]) => counts.has(v)).map(([v, l]) => <button key={v} aria-pressed={format === v} onClick={() => setParams({ format: v, ...(q ? { q } : {}) })}>{l}<span className="count">{counts.get(v)}</span></button>)}
          </div>
        </div>
        {copies.length ? <div className="media-grid">{copies.map(c => <article className="poster-card" key={c.id}>
          <Link to={`/library/${c.entry_id}`} className="art" tabIndex={-1} aria-hidden="true"><Cover entry={{ title: c.entry_title, media_type: c.media_type, image_url: c.image_url }} /><span className="art-badge">{formatLabel(c.format)}</span></Link>
          <div><Link to={`/library/${c.entry_id}`} className="card-title media-title">{c.entry_title}</Link><span className="card-meta">{copySummary(c)}</span>
            {(c.store || c.price_cents != null) && <span className="card-meta">{[money(c.price_cents, c.currency), c.store].filter(Boolean).join(' · ')}</span>}</div>
        </article>)}</div>
          : <Empty compact title={t('No copies match')} action={filtered ? <button className="button" onClick={() => { setSearch(''); setParams({}); }}>{t('Clear filters')}</button> : undefined} />}
        <Pager current={cursor} next={all.data?.next_cursor} change={value => setParams({ ...(format ? { format } : {}), ...(q ? { q } : {}), ...(value ? { cursor: value } : {}) })} />
      </>}
  </div>;
}
