import './discovery.css';
import { discoveryKind, discoverySearchKind, discoveryPage, discoveryOptions, discoveryReason, discoveryMembership, type DiscoveryCapabilities } from './discoveryPolicy';
import { searchSources } from '../browsePolicy';
import { useEffect, useRef, useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, CircleAlert, EyeOff, PenLine, Plus, Search, Star } from 'lucide-react';
import { usePreferences } from '../organize';
import { api, keyHeader, unwrap, type Schema } from '../api/client';
import { privateKey, useRefresh, useUser } from '../state';
import { Cover, EntryEditor, Shelf, typeLabel, providerName } from '../media';
import { BarcodeButton } from './editions';
import { Empty, ErrorNote, Failed, Loading, PageTitle, Status, submit, useAction, statusText } from '../ui';
import { useSourceNavigation } from '../navigation';
import { t, plural } from '../i18n';

type Kind = Schema['CatalogResult']['media_type'];
type Item = Schema['CatalogItem'];
type DiscoveryProvider = Schema['ProviderChoice'];
// Credits can predate the batched membership contract. Discover itself always gets annotations.
type CardResult = Omit<Schema['CatalogResult'], 'library_entry_id' | 'library_status'> & Partial<Pick<Schema['CatalogResult'], 'library_entry_id' | 'library_status'>>;

function AvailabilityNotice({ availability, local = false }: { availability?: string; local?: boolean }) {
  const message = availability === 'not_configured' ? t('This source is not set up. Search titles already on this server or add a title manually.')
    : availability === 'temporarily_unavailable' ? t('This source is temporarily unavailable. Results may only include titles already on this server.')
    : availability === 'local_only' ? t('Only titles already on this server are available.')
    : availability === 'search_only' ? t('This source supports search. Enter a title above to find something to track.')
    : local ? t('The metadata provider is unavailable, so these results come from titles already known to this server.') : null;
  return message ? <p className="notice browse-notice" role="status"><CircleAlert size={16} aria-hidden /><span>{message}</span></p> : null;
}

export const searchKinds: [Kind, string][] = [
  ['movie', t('Movies')], ['series', 'TV'], ['anime', t('Anime')], ['manga', t('Manga')], ['book', t('Books')], ['audiobook', t('Audiobooks')],
  ['comic', t('Comics')], ['game', t('Games')], ['boardgame', t('Board games')], ['music', t('Music')], ['podcast', t('Podcasts')], ['video', t('Videos')],
];
const catalogKey = (id: string) => ['catalog', id] as const;

async function resolve(result: Pick<Schema['CatalogResult'], 'catalog_id' | 'provider' | 'media_type' | 'provider_id'>) {
  if (result.catalog_id) return result.catalog_id;
  const item = unwrap(await api.POST('/api/v1/catalog/resolve', { body: { provider: result.provider, media_type: result.media_type, provider_id: result.provider_id } }));
  return item.id;
}

/** Discover answers "what could I track next?": one search row, one row of media types, then titles. */
export function Discover() {
  const user = useUser();
  const [params, setParams] = useSearchParams(); const [manual, setManual] = useState(false); const { prefs } = usePreferences();
  const kind = discoveryKind(params.get('type')); const q = params.get('q') ?? '';
  const searchKind = discoverySearchKind(kind, prefs.anime_shape);
  const [text, setText] = useState(q); useEffect(() => setText(q), [q]);
  const providers = useQuery({ queryKey: ['catalog-providers'], staleTime: 300_000, queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/catalog/providers', { signal })).items });
  const choices = ((providers.data ?? []) as DiscoveryProvider[]).filter(p => p.kinds.includes(searchKind));
  // Only sources that can answer are offered, and only when there is a real choice between them.
  const sources = searchSources(choices, searchKind);
  const requested = choices.find(p => p.name === params.get('provider'));
  const chosen = requested?.name;
  const fallback = choices.find(p => p.defaults?.includes(searchKind));
  const selected = choices.find(p => p.name === chosen) ?? fallback;
  const browse = !q && params.get('browse') === '1';
  const caps = selected?.configured ? (browse ? selected.browse : selected.search) : undefined;
  const options = discoveryOptions(params, caps);
  const page = options.page ?? 1;
  const query = useInfiniteQuery({ queryKey: privateKey(user.id, 'catalog', 'infinite', browse ? 'browse' : 'search', searchKind, q, chosen, options), enabled: (q.length > 0 || browse) && providers.isSuccess, staleTime: 300_000,
    initialPageParam: page,
    getNextPageParam: (last: Schema['CatalogSearch'], _pages, previous) => caps?.pagination && last.next_page && discoveryPage(last.next_page) > previous ? discoveryPage(last.next_page) : undefined,
    queryFn: async ({ signal, pageParam }): Promise<Schema['CatalogSearch']> => {
      const query = { media_type: searchKind, provider: chosen, ...options, ...(caps?.pagination ? { page: pageParam } : {}) };
      const result = browse ? unwrap(await api.GET('/api/v1/catalog/browse', { signal, params: { query } }))
        : unwrap(await api.GET('/api/v1/catalog/search', { signal, params: { query: { ...query, q } } }));
      if (pageParam > page && result.availability === 'temporarily_unavailable') throw new Error(t('This source is temporarily unavailable. Results may only include titles already on this server.'));
      return result;
    } });
  function change(next: Record<string, string>) {
    const p = new URLSearchParams(params);
    if (!('page' in next)) p.delete('page');
    if ('type' in next || 'provider' in next || 'q' in next || 'browse' in next) for (const key of ['year', 'genre', 'sort']) p.delete(key);
    for (const [k, v] of Object.entries(next)) { if (v) p.set(k, v); else p.delete(k); }
    setParams(p);
  }
  const first = query.data?.pages[0];
  const items = [...new Map((query.data?.pages.flatMap(p => p.items) ?? []).map(item => [item.provider + ':' + item.media_type + ':' + item.provider_id, item])).values()];
  const kindLabel = searchKinds.find(k => k[0] === kind)?.[1] ?? '';
  const mode = selected?.browse?.supported && selected.configured
    ? <div className="segmented labelled discover-mode" role="group" aria-label={t('Discovery view')}><button aria-pressed={!browse && !q} onClick={() => change({ q: '', browse: '' })}>{t('Suggestions')}</button><button aria-pressed={browse} onClick={() => change({ q: '', browse: '1' })}>{t('Browse')}</button></div> : null;
  const count = !first ? '' : first.source === 'local' ? plural(items.length, '{n} title on this server', '{n} titles on this server') : query.hasNextPage ? plural(items.length, 'First {n} result', 'First {n} results') : plural(items.length, '{n} result', '{n} results');
  return <div className="discovery-page"><PageTitle title={t('Discover')} />
    <form className="discover-search" role="search" onSubmit={submit(() => change({ q: text.trim(), browse: '' }))}>
      <div className="search-form"><Search size={18} aria-hidden /><input name="q" type="search" aria-label={t('Search {value}', { value: kindLabel })} maxLength={kind === 'podcast' || kind === 'video' ? 200 : 100} placeholder={kind === 'podcast' ? t('Show name or feed address…') : kind === 'book' || kind === 'audiobook' ? t('Title, author or ISBN…') : kind === 'video' ? t('Paste a YouTube link…') : t('Title…')} value={text} onChange={e => setText(e.target.value)} />
        {(kind === 'book' || kind === 'audiobook') && <BarcodeButton onCode={code => { setText(code); change({ q: code, browse: '' }); }} />}
        <button className="button small" type="submit">{t('Search')}</button></div>
      {sources.length > 1 && <label className="select-pill discover-source"><span className="sr-only">{t('Search with')}</span><select name="source" value={sources.some(p => p.name === chosen) ? chosen : ''} onChange={e => change({ provider: e.target.value, browse: '' })}><option value="">{fallback?.configured ? providerName(fallback.name) : t('Default source')}</option>{sources.filter(p => !(fallback?.configured && p.name === fallback.name)).map(p => <option key={p.name} value={p.name}>{providerName(p.name)}</option>)}</select></label>}
      <button type="button" className="button discover-manual" onClick={() => setManual(true)} aria-label={t('Add manually')}><PenLine aria-hidden /><span>{t('Add manually')}</span></button>
    </form>
    <div className="chip-filter discover-kinds" role="group" aria-label={t('Media type')}>{searchKinds.map(([value, label]) => <button key={value} aria-pressed={kind === value} onClick={() => change({ type: value, provider: '' })}>{label}</button>)}</div>
    {(q || browse) && caps && <DiscoveryFilters key={searchKind + chosen + q + browse + params.get('year') + params.get('genre') + params.get('sort')} caps={caps} provider={selected!.name} kind={searchKind} params={params} change={change} />}
    {requested?.configured === false && !q && !browse && <AvailabilityNotice availability="not_configured" />}
    {providers.error && <Failed error={providers.error} retry={() => void providers.refetch()} />}
    {searchKind !== kind && <p className="subtle anime-note">{t('Anime is found as whole shows with seasons.')} <Link className="text-link" to="/account?tab=preferences">{t('Change')}</Link></p>}
    {!q && !browse ? <>{mode && <div className="results-head">{mode}</div>}<DiscoverFeed key={searchKind} kind={searchKind} /></>
      : providers.isPending ? <Loading label={t('Loading sources')} />
      : providers.error ? null
      : query.isPending ? <Loading label={browse ? t('Loading titles') : t('Searching')} />
      : query.error && !first ? <Failed error={query.error} retry={() => void query.refetch()} />
      : <><AvailabilityNotice availability={first!.availability} local={first!.source === 'local'} />
        {(items.length > 0 || mode) && <div className="results-head">{items.length > 0 && <p className="results-note" role="status">{count}</p>}{mode}</div>}
        {items.length ? <div className="result-grid">{items.map(result => <SearchResult key={result.provider + result.media_type + result.provider_id} result={result} />)}</div>
          : <Empty title={first!.source === 'local' ? t('No titles found on this server.') : t('No matches.')}>{first!.availability === 'temporarily_unavailable' || first!.availability === 'not_configured' ? t('Try again later, choose another source, or add a title manually.') : first!.source === 'local' ? t('Search another source or add the title manually.') : t('Try a shorter title or another media type.')}</Empty>}
        <InfiniteCatalog available={query.hasNextPage} pending={query.isFetching} error={query.isFetchNextPageError ? query.error : null} onLoad={() => void query.fetchNextPage()} /></>}
    {manual && <EntryEditor onClose={() => setManual(false)} />}</div>;
}

function InfiniteCatalog({ available, pending, error, onLoad }: { available: boolean; pending: boolean; error: unknown; onLoad: () => void }) {
  const sentinel = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const target = sentinel.current;
    if (!target || !available || pending || error || !('IntersectionObserver' in window)) return;
    const observer = new IntersectionObserver(entries => { if (entries.some(entry => entry.isIntersecting)) onLoad(); }, { rootMargin: '300px' });
    observer.observe(target);
    return () => observer.disconnect();
  }, [available, pending, error, onLoad]);
  return <div ref={sentinel} className="discovery-paging">
    <ErrorNote error={error} />
    {available && <button className="button" disabled={pending} onClick={onLoad}>{pending ? t('Loading…') : error ? t('Retry') : t('Load more')}</button>}
  </div>;
}

function DiscoveryFilters({ caps, provider, kind, params, change }: { caps: DiscoveryCapabilities; provider: string; kind: string; params: URLSearchParams; change: (next: Record<string, string>) => void }) {
  const labels: Record<string, string> = { popular: t('Popular'), rating: t('Highest rated'), newest: t('Newest') };
  const genres = provider === 'tmdb' ? [['16', t('Animation')], ['35', t('Comedy')], ['18', t('Drama')], ['99', t('Documentary')], ['10751', t('Family')], ['9648', t('Mystery')], ...(kind === 'movie' ? [['28', t('Action')], ['12', t('Adventure')], ['14', t('Fantasy')], ['27', t('Horror')], ['10749', t('Romance')], ['878', t('Science fiction')], ['53', t('Thriller')]] : [])] : provider === 'anilist' ? [['Action', t('Action')], ['Adventure', t('Adventure')], ['Comedy', t('Comedy')], ['Drama', t('Drama')], ['Fantasy', t('Fantasy')], ['Mystery', t('Mystery')], ['Romance', t('Romance')], ['Sci-Fi', t('Science fiction')], ['Sports', t('Sports')], ['Thriller', t('Thriller')]] : [];
  if (!caps.year && !caps.genre && !caps.sorts.length) return null;
  const active = !!(params.get('year') || params.get('genre') || params.get('sort'));
  return <form className="discovery-filters" onSubmit={event => { event.preventDefault(); const data = new FormData(event.currentTarget); change({ year: String(data.get('year') ?? ''), genre: String(data.get('genre') ?? ''), sort: String(data.get('sort') ?? '') }); }}>
    {caps.year && <label>{t('Year')}<input name="year" type="number" min={1800} max={2200} step={1} placeholder={t('Any year')} defaultValue={discoveryOptions(params, caps).year ?? ''} /></label>}
    {caps.genre && <label>{t('Genre')}{genres.length ? <select name="genre" defaultValue={params.get('genre') ?? ''}><option value="">{t('Any genre')}</option>{genres.map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select> : <input name="genre" maxLength={80} defaultValue={params.get('genre') ?? ''} />}</label>}
    {!!caps.sorts.length && <label>{t('Sort')}<select name="sort" defaultValue={params.get('sort') ?? ''}><option value="">{t('Default')}</option>{caps.sorts.map(value => <option key={value} value={value}>{labels[value] ?? value}</option>)}</select></label>}
    <button className="button" type="submit">{t('Apply filters')}</button>{active && <button className="text-button" type="button" onClick={() => change({ year: '', genre: '', sort: '' })}>{t('Clear filters')}</button>}
  </form>;
}

function localizedRowTitle(reason: Schema['DiscoverRow']['reason']) {
  const { key, values } = discoveryReason(reason);
  const titles: Record<string, string> = {
    'Suggestions': t('Suggestions'), 'Popular now': t('Popular now'), 'From the existing catalog': t('From the existing catalog'),
    'Because you liked {title}': t('Because you liked {title}', values),
    'More {genre} from this server': t('More {genre} from this server', values), 'More {genre}': t('More {genre}', values),
  };
  return titles[key];
}

function DiscoverFeed({ kind }: { kind: Kind }) {
  const user = useUser(); const client = useQueryClient(); const [hidden, setHidden] = useState<Schema['CatalogResult'][]>([]);
  const feed = useQuery({ queryKey: privateKey(user.id, 'discover', kind), staleTime: 300_000,
    queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/discover', { signal, params: { query: { media_type: kind as Schema['DiscoverFeed']['media_type'] } } })) });
  const dismiss = useAction(async (result: Schema['CatalogResult']) => {
    unwrap(await api.POST('/api/v1/discover/dismissals', { body: { provider: result.provider, media_type: result.media_type, provider_id: result.provider_id } }));
    setHidden(old => [...old, result]);
    await client.invalidateQueries({ queryKey: privateKey(user.id, 'discover', 'dismissals', kind) });
    client.setQueryData<Schema['DiscoverFeed']>(privateKey(user.id, 'discover', kind), old => old && { ...old, rows: old.rows.map(r => ({ ...r, items: r.items.filter(i => !(i.provider === result.provider && i.provider_id === result.provider_id)) })) });
  });
  const undo = useAction(async (result: Schema['CatalogResult']) => {
    unwrap(await api.DELETE('/api/v1/discover/dismissals', { params: { query: { provider: result.provider, media_type: result.media_type, provider_id: result.provider_id } } }));
    setHidden(old => old.filter(i => !(i.provider === result.provider && i.provider_id === result.provider_id)));
    await client.invalidateQueries({ queryKey: privateKey(user.id, 'discover') });
  });
  if (feed.isPending) return <Loading label={t('Loading suggestions')} />;
  if (feed.error) return <Failed error={feed.error} retry={() => void feed.refetch()} />;
  return <div className="discover-feed">
    {/* With nothing to suggest, the empty state below already says to search. */}
    {(feed.data.availability !== 'search_only' || feed.data.rows.some(row => row.items.length)) && <AvailabilityNotice availability={feed.data.availability} local={feed.data.degraded} />}
    <ErrorNote error={dismiss.error || undo.error} />
    {hidden.length > 0 && <section className="discovery-undo" aria-label={t('Hidden suggestions')}>{hidden.map(result => <div key={result.provider + result.provider_id} role="status"><span>{t('Hid {title}.', { title: result.title })}</span><button className="text-button" disabled={undo.pending || dismiss.pending} onClick={() => void undo.run(result)}>{t('Undo')}</button></div>)}</section>}
    {!feed.data.rows.some(row => row.items.length) && <Empty title={t('Search a catalog')}>{t('Choose a media type and search by title, or add a title manually.')}</Empty>}
    {feed.data.rows.filter(row => row.items.length > 0).map(row => <Shelf key={row.key} title={localizedRowTitle(row.reason)}>{row.items.map(result => <SearchResult key={result.provider + result.provider_id} result={result} dismissPending={dismiss.pending || undo.pending} onDismiss={() => void dismiss.run(result)} />)}</Shelf>)}
    <HiddenSuggestions kind={kind} onRestore={result => setHidden(old => old.filter(i => !(i.provider === result.provider && i.provider_id === result.provider_id && i.media_type === result.media_type)))} />
  </div>;
}

/** The titles hidden from suggestions, offered only when there are some. */
function HiddenSuggestions({ kind, onRestore }: { kind: Kind; onRestore: (result: Schema['DiscoverDismissal']) => void }) {
  const user = useUser(); const client = useQueryClient(); const [open, setOpen] = useState(false);
  const hidden = useInfiniteQuery({ queryKey: privateKey(user.id, 'discover', 'dismissals', kind), initialPageParam: '',
    queryFn: async ({ signal, pageParam }) => unwrap(await api.GET('/api/v1/discover/dismissals', { signal, params: { query: { media_type: kind as Schema['DiscoverFeed']['media_type'], cursor: pageParam || undefined, limit: 50 } } })),
    getNextPageParam: page => page.next_cursor ?? undefined });
  const restore = useAction(async (result: Schema['DiscoverDismissal']) => {
    unwrap(await api.DELETE('/api/v1/discover/dismissals', { params: { query: { provider: result.provider, media_type: result.media_type, provider_id: result.provider_id } } }));
    onRestore(result);
    await client.invalidateQueries({ queryKey: privateKey(user.id, 'discover') });
  });
  const items = hidden.data?.pages.flatMap(page => page.items) ?? [];
  if (!items.length && !open) return null;
  return <section className="discovery-hidden">
    <button className="text-button" aria-expanded={open} onClick={() => setOpen(!open)}>{hidden.hasNextPage ? t('Hidden suggestions ({count}+)', { count: items.length }) : t('Hidden suggestions ({count})', { count: items.length })}</button>
    {open && <div className="discovery-undo">
      {hidden.isPending ? <Loading label={t('Loading hidden suggestions')} /> : hidden.error ? <Failed error={hidden.error} retry={() => void hidden.refetch()} />
        : !items.length ? <p className="subtle">{t('No hidden suggestions for this media type.')}</p>
        : items.map(result => <div key={result.provider + result.media_type + result.provider_id}><span>{result.title === result.provider_id ? t('Hidden title from {provider}', { provider: providerName(result.provider) }) : result.title}</span><button className="button small" disabled={restore.pending} onClick={() => void restore.run(result)}>{t('Restore')}</button></div>)}
      <ErrorNote error={restore.error} />
      {hidden.hasNextPage && <button className="button" disabled={hidden.isFetchingNextPage} onClick={() => void hidden.fetchNextPage()}>{hidden.isFetchingNextPage ? t('Loading…') : t('Load more')}</button>}
    </div>}
  </section>;
}

export function SearchResult({ result, onDismiss, dismissPending }: { result: CardResult; onDismiss?: () => void; dismissPending?: boolean }) {
  const navigate = useNavigate(); const source = useSourceNavigation(); const refresh = useRefresh(); const user = useUser();
  const [resolvedID, setResolvedID] = useState<string | null>(null);
  const id = result.catalog_id ?? resolvedID;
  const legacy = useQuery({ queryKey: privateKey(user.id, ...catalogKey(id ?? '')), enabled: result.library_entry_id === undefined && !!id, staleTime: 300_000,
    queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/catalog/{catalogID}', { signal, params: { path: { catalogID: id! } } })) });
  const { id: entryID, status: libraryStatus } = discoveryMembership(result, legacy.data?.entry);
  const open = useAction(async (_: void) => { const from = source(); navigate(`/title/${await resolve(result)}`, { state: from }); });
  const add = useAction(async (_: void, key) => {
    const id = await resolve(result);
    unwrap(await api.POST('/api/v1/library/track', { params: { header: keyHeader(key) }, body: { catalog_id: id, status: 'planned' } }));
    if (result.library_entry_id === undefined) setResolvedID(id);
    await refresh();
  });
  return <article className="poster-card result-card">
    <div className="art-wrap"><button className="art" onClick={() => void open.run()} tabIndex={-1} aria-hidden="true"><Cover entry={{ title: result.title, media_type: result.media_type, image_url: result.image_url }} /></button>
      {onDismiss && <button className="art-dismiss" disabled={dismissPending} onClick={onDismiss} aria-label={t('Not interested in {title}', { title: result.title })} title={t('Not interested')}><EyeOff aria-hidden /></button>}</div>
    <div className="card-text">
      <button className="card-title media-title result-title" onClick={() => void open.run()}>{result.title}</button>
      <span className="card-meta">{[result.year, ...result.credits.slice(0, 1)].filter(Boolean).join(' · ') || typeLabel(result.media_type)}</span>
    </div>
    {/* One mark for a title already tracked: its status, which opens your entry. */}
    {entryID ? <button className="card-state" onClick={() => navigate(`/library/${entryID}`, { state: source() })} aria-label={t('In your library: {status}. Open', { status: libraryStatus ? statusText(libraryStatus) : t('In your library') })}>{libraryStatus ? <Status value={libraryStatus} /> : <span className="status">{t('In your library')}</span>}</button>
      : <button className="button small card-action" disabled={add.pending || (legacy.isPending && legacy.fetchStatus !== 'idle')} onClick={() => void add.run()} aria-label={t('Add {title} to your library', { title: result.title })}><Plus aria-hidden />{add.pending ? t('Adding…') : t('Add')}</button>}
    <ErrorNote error={open.error || add.error || legacy.error} />
  </article>;
}

export function PersonalMembershipControls({ item }: { item: Pick<Item, 'id'> }) {
  const user = useUser(); const client = useQueryClient(); const refresh = useRefresh();
  const query = useQuery({ queryKey: privateKey(user.id, 'personal-state', item.id), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/catalog/{catalogID}/personal-state', { signal, params: { path: { catalogID: item.id } } })) });
  const change = useAction(async (value: Schema['PersonalMembershipInput'], key) => {
    unwrap(await api.PUT('/api/v1/catalog/{catalogID}/personal-state', { params: { path: { catalogID: item.id }, header: keyHeader(key) }, body: value }));
    await client.invalidateQueries({ queryKey: privateKey(user.id, 'personal-state', item.id) }); await refresh();
  });
  return <div className="tracking-panel"><div className="actions">
    <button className="button" aria-pressed={query.data?.favourite ?? false} disabled={!query.data || change.pending} onClick={() => void change.run({ kind: 'favorite', present: !query.data?.favourite })}><Star aria-hidden fill={query.data?.favourite ? 'currentColor' : 'none'} />{query.data?.favourite ? t('Remove from favourites') : t('Add to favourites')}</button>
    <button className="button" aria-pressed={query.data?.watchlist ?? false} disabled={!query.data || change.pending} onClick={() => void change.run({ kind: 'watchlist', present: !query.data?.watchlist })}>{query.data?.watchlist ? <Check aria-hidden /> : <Plus aria-hidden />}{query.data?.watchlist ? t('Remove from watchlist') : t('Add to watchlist')}</button>
  </div><ErrorNote error={query.error || change.error} /></div>;
}
