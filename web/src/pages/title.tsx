/** The title page, /title/:catalogID: the title itself, its episodes and where you are with it. */
import { useMemo, useState } from 'react';
import { useParams } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, ChevronDown, ChevronRight, Circle, CircleCheckBig, Plus, SkipForward, Star, UserRound, Users, X } from 'lucide-react';
import { api, keyHeader, unwrap, type Entry, type Schema } from '../api/client';
import { privateKey, useAuth, useRefresh, useUser } from '../state';
import { isProfileLogin } from '../profileLoginPolicy';
import { providerName, statuses, typeLabel, unitLabel } from '../media';
import { CopiesSection, rating, TagPicker, usePreferences, useRatingsOn } from '../organize';
import { ErrorNote, Failed, Loading, statusText, submit, useAction } from '../ui';
import { ContextBackLink, ReturnLink, SourceLink } from '../navigation';
import { FindInSilo } from '../siloConnect';
import { CheckInButton, PlayHistorySection } from '../checkins';
import { AddToCollabDialog } from './collab';
import { EditionSection } from './editions';
import { dayText, episodeCount, EpisodeRatingSelect, hierarchical, isAired, latestEntry, sentence, TitleHead, TrackingPanel, useEpisodeRatings, useEpisodes, useMarks, type EpisodeIndex, type Item } from './tracking';
import { duration, episodeCode } from '../format';
import { locale, plural, t } from '../i18n';

export function TitleDetail() {
  const { catalogID = '' } = useParams(); const user = useUser();
  const query = useQuery({ queryKey: privateKey(user.id, 'catalog', catalogID), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/catalog/{catalogID}', { signal, params: { path: { catalogID } } })) });
  if (query.isPending) return <Loading label={t('Loading title')} />;
  if (query.error) return <><ContextBackLink fallback="/discover" label={t('Discover')} /><Failed error={query.error} retry={() => void query.refetch()} /></>;
  return <TitleContent key={catalogID} detail={query.data} />;
}

function TitleContent({ detail }: { detail: Schema['CatalogDetail'] }) {
  const { item, entry } = detail; const [sharing, setSharing] = useState(false); const household = !isProfileLogin(useAuth().session);
  const index = useEpisodes(hierarchical.has(item.kind) ? item : null);
  const silo = item.kind === 'movie' || item.kind === 'series';
  return <div className="item-page title-page">
    <ContextBackLink fallback={entry ? '/library' : '/discover'} label={entry ? t('Library') : t('Discover')} />
    <TitleHead title={item.title} original={item.original_title} cover={{ title: item.title, media_type: item.kind, image_url: item.image_url }} backdrop={item.backdrop_url || undefined}
      facts={[typeLabel(item.kind), item.year, item.kind === 'movie' && item.runtime_minutes ? duration(item.runtime_minutes) : null, item.genres.slice(0, 3).join(', ')]}
      links={entry ? <ReturnLink className="text-link" to={`/library/${entry.id}`}>{t('Open in your library')}<ChevronRight size={16} aria-hidden /></ReturnLink> : null}
      actions={silo ? <FindInSilo title={item.title} /> : null} />
    <div className="item-layout">
      <div className="item-main">
        {entry ? <TrackingPanel entry={entry} item={item} index={index} extra={silo ? <Memberships item={item} completed={entry.status === 'completed'} /> : null} /> : <AddPanel item={item} />}
        {item.overview && <section className="item-section" aria-labelledby="overview-heading"><div className="item-section-head"><h2 id="overview-heading">{t('Overview')}</h2></div><p className="overview">{item.overview}</p></section>}
        {index && <Episodes item={item} entry={entry} index={index} />}
        {entry && (item.kind === 'book' || item.kind === 'audiobook') && <EditionSection item={item} entry={entry} />}
        {entry && (item.kind === 'game' || item.kind === 'boardgame') && <PlayHistorySection entryID={entry.id} boardGame={item.kind === 'boardgame'} />}
        <TimeToBeat item={item} />
        {item.kind === 'game' && <CriticScore catalogID={item.id} />}
        <Credits item={item} />
      </div>
      <aside className="item-rail" aria-label={t('About this title')}>
        <TitleFacts item={item} index={index} />
        {entry && <section className="rail-block" aria-labelledby="title-tags-heading"><h2 id="title-tags-heading">{t('Tags')}</h2><TagPicker entryID={entry.id} /></section>}
        {entry && <div className="rail-block"><CopiesSection entryID={entry.id} seasons={index?.seasons.map(s => ({ id: s.id, title: s.title }))} /></div>}
        {household && <section className="rail-block" aria-labelledby="share-heading"><h2 id="share-heading">{t('Watch together')}</h2>
          <button className="button small" onClick={() => setSharing(true)}><Users aria-hidden />{t('Add to a collaborative list')}</button></section>}
      </aside>
    </div>
    {sharing && <AddToCollabDialog catalogID={item.id} title={item.title} onClose={() => setSharing(false)} />}
  </div>;
}

/** Before the title is in your library: one primary action to add it. */
function AddPanel({ item }: { item: Item }) {
  const refresh = useRefresh(); const [status, setStatus] = useState<Schema['TrackInput']['status']>('planned');
  const track = useAction(async (value: Schema['TrackInput']['status'], key) => { unwrap(await api.POST('/api/v1/library/track', { params: { header: keyHeader(key) }, body: { catalog_id: item.id, status: value } })); await refresh(); });
  const single = !hierarchical.has(item.kind);
  return <section className="track" aria-label={t('Your progress')}>
    <div className="track-summary"><p className="track-figure track-figure-text">{t('Not in your library')}</p>
      <p className="track-note">{hierarchical.has(item.kind) ? t('Add it to mark episodes watched and see what is next.') : t('Add it to keep your progress, rating and notes.')}</p></div>
    <form className="track-next" onSubmit={submit(() => void track.run(status))}>
      <div className="track-field"><label htmlFor="track-status">{t('Add as')}</label>
        <select id="track-status" value={status} onChange={e => setStatus(e.target.value as typeof status)}>{statuses.map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></div>
      <button className="button primary" disabled={track.pending} type="submit"><Plus aria-hidden />{track.pending ? t('Adding…') : t('Add to library')}</button>
      {single && <CheckInButton catalogID={item.id} />}
    </form>
    <ErrorNote error={track.error} />
    {(item.kind === 'movie' || item.kind === 'series') && <div className="track-controls"><Memberships item={item} completed={false} /></div>}
  </section>;
}

/** Favourite and watchlist, kept with your other state for the title. */
function Memberships({ item, completed }: { item: Pick<Item, 'id'>; completed: boolean }) {
  const user = useUser(); const client = useQueryClient(); const refresh = useRefresh();
  const query = useQuery({ queryKey: privateKey(user.id, 'personal-state', item.id), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/catalog/{catalogID}/personal-state', { signal, params: { path: { catalogID: item.id } } })) });
  const change = useAction(async (value: Schema['PersonalMembershipInput'], key) => {
    unwrap(await api.PUT('/api/v1/catalog/{catalogID}/personal-state', { params: { path: { catalogID: item.id }, header: keyHeader(key) }, body: value }));
    await client.invalidateQueries({ queryKey: privateKey(user.id, 'personal-state', item.id) }); await refresh();
  });
  const state = query.data; const busy = !state || change.pending;
  return <div className="track-field track-memberships">
    <span className="track-field-label">{t('Your lists')}</span>
    <div className="track-toggles">
      <button className="button small" aria-pressed={state?.favourite ?? false} disabled={busy} onClick={() => void change.run({ kind: 'favorite', present: !state?.favourite })}><Star aria-hidden fill={state?.favourite ? 'currentColor' : 'none'} />{t('Favourite')}</button>
      {/* A finished title on the watchlist is left over; offer to clear it rather than to add it again. */}
      {completed ? state?.watchlist && <button className="button small" disabled={busy} onClick={() => void change.run({ kind: 'watchlist', present: false })}><X aria-hidden />{t('Take off watchlist')}</button>
        : <button className="button small" aria-pressed={state?.watchlist ?? false} disabled={busy} onClick={() => void change.run({ kind: 'watchlist', present: !state?.watchlist })}>{state?.watchlist ? <Check aria-hidden /> : <Plus aria-hidden />}{state?.watchlist ? t('On watchlist') : t('Watchlist')}</button>}
    </div>
    <ErrorNote error={query.error || change.error} />
  </div>;
}

// ---------------------------------------------------------------- episodes

function useEpisodeActions(entry: Entry | null) {
  const refresh = useRefresh();
  const mark = useAction(async (ids: string[], key) => { const c = await latestEntry(entry!.id); unwrap(await api.POST('/api/v1/library/{entryID}/plays', { params: { path: { entryID: entry!.id }, header: keyHeader(key) }, body: { version: c.version, items: ids.map(catalog_id => ({ catalog_id })) } })); await refresh(); });
  const unmark = useAction(async (playID: string, key) => { const c = await latestEntry(entry!.id); unwrap(await api.DELETE('/api/v1/library/{entryID}/plays/{playID}', { params: { path: { entryID: entry!.id, playID }, header: keyHeader(key) }, body: { version: c.version } })); await refresh(); });
  const skip = useAction(async (value: { ids: string[]; skip: boolean }, key) => { const c = await latestEntry(entry!.id); unwrap(await api.POST('/api/v1/library/{entryID}/skips', { params: { path: { entryID: entry!.id }, header: keyHeader(key) }, body: { version: c.version, catalog_ids: value.ids, skip: value.skip } })); await refresh(); });
  return { mark, unmark, skip, busy: mark.pending || unmark.pending || skip.pending, error: mark.error || unmark.error || skip.error };
}
type Actions = ReturnType<typeof useEpisodeActions>;
type Ratings = Map<string, Schema['EpisodeRating']>;

function Episodes({ item, entry, index }: { item: Item; entry: Entry | null; index: EpisodeIndex }) {
  const user = useUser(); const refresh = useRefresh(); const ratingsOn = useRatingsOn(); const { prefs } = usePreferences();
  const { marks } = useMarks(entry);
  const series = item.kind === 'series';
  const ratings = useEpisodeRatings(entry && ratingsOn && series ? entry.id : null);
  const derived = useQuery({ queryKey: privateKey(user.id, 'derived-rating', entry?.id), enabled: !!entry && ratingsOn && series,
    queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/library/{entryID}/derived-rating', { signal, params: { path: { entryID: entry!.id } } })) });
  const actions = useEpisodeActions(entry);
  const [viewed, setViewed] = useState<string | null>(null);
  const chosen = entry ? entry.episode_order ?? '' : viewed ?? '';
  const orders = useQuery({ queryKey: ['episode-orders', item.id], enabled: series, staleTime: 3_600_000, queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/catalog/{catalogID}/episode-orders', { signal, params: { path: { catalogID: item.id } } })).items });
  const order = useQuery({ queryKey: ['episode-order', item.id, chosen], enabled: !!chosen, staleTime: 3_600_000,
    queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/catalog/{catalogID}/episode-orders/{orderID}', { signal, params: { path: { catalogID: item.id, orderID: chosen } } })) });
  const choose = useAction(async (value: string, key) => {
    if (!entry) { setViewed(value || null); return; }
    const c = await latestEntry(entry.id);
    unwrap(await api.PUT('/api/v1/library/{entryID}/episode-order', { params: { path: { entryID: entry.id }, header: keyHeader(key) }, body: { version: c.version, order_id: value || null } })); await refresh();
  });
  const next = series ? entry?.next_item ?? null : null;
  const heading = series ? (chosen && order.data ? order.data.name : t('Seasons')) : item.kind === 'comic' ? t('Issues') : item.kind === 'music' ? t('Tracks') : t('Episodes');
  const average = derived.data && derived.data.rated_episodes > 0 && derived.data.rating != null ? t('Episode average {value} · {rated} of {watched} rated', { value: rating(Math.round(derived.data.rating * 10) / 10, prefs.rating_scale), rated: derived.data.rated_episodes, watched: derived.data.watched_episodes }) : '';
  const groups: { key: string; name: string; year?: number | null; episodes: Item[]; total?: number | null; special?: boolean }[] = !series ? []
    : chosen ? (order.data?.groups ?? []).map(g => ({ key: `g${g.number}`, name: g.name, episodes: g.episodes }))
    : index.seasons.map(s => ({ key: s.id, name: s.title, year: s.year, episodes: index.bySeason.get(s.id) ?? [], total: s.total, special: (s.season_number ?? 0) === 0 }));
  return <section className="item-section" aria-labelledby="episodes-heading">
    <div className="item-section-head"><h2 id="episodes-heading">{heading}</h2>{average && <span className="num">{average}</span>}</div>
    {series && orders.data && orders.data.length > 0 && <div className="order-row"><label htmlFor="episode-order">{t('Order')}</label>
      <select id="episode-order" value={chosen} disabled={choose.pending} onChange={e => void choose.run(e.target.value)}>
        <option value="">{t('Aired order')}</option>{orders.data.map(o => <option key={o.id} value={o.id}>{o.kind && o.kind !== o.name ? `${o.name} (${o.kind})` : o.name}</option>)}</select>
      {entry && chosen && <span className="subtle">{t('Next follows this order. What you watched stays the same.')}</span>}</div>}
    {!entry && <p className="item-empty">{series ? t('Add this to your library to mark episodes watched.') : item.kind === 'comic' ? t('Add this to your library to mark issues read.') : item.kind === 'music' ? t('Add this to your library to mark tracks played.') : t('Add this to your library to mark episodes watched.')}</p>}
    <ErrorNote error={actions.error || choose.error} />
    {index.pending || (chosen && order.isPending) ? <Loading label={t('Loading episodes')} />
      : index.error ? <Failed error={index.error} retry={index.retry} />
      : order.error ? <Failed error={order.error} retry={() => void order.refetch()} />
      : index.empty ? <p className="item-empty">{t('The provider has not listed any yet.')}</p>
      : series ? <div className="season-stack">{groups.map(g => <SeasonBlock key={g.key} name={g.name} year={g.year} total={g.total} special={g.special} episodes={g.episodes} entry={entry} marks={marks} ratings={ratings} actions={actions} startOpen={!!next && g.episodes.some(e => e.id === next.catalog_id)} />)}</div>
      : <EpisodeRows episodes={index.flat} entry={entry} marks={marks} ratings={ratings} actions={actions} cap={50} />}
  </section>;
}

function SeasonBlock({ name, year, total, special, episodes, entry, marks, ratings, actions, startOpen }: { name: string; year?: number | null; total?: number | null; special?: boolean; episodes: Item[]; entry: Entry | null; marks: Map<string, string>; ratings: Ratings; actions: Actions; startOpen: boolean }) {
  const [open, setOpen] = useState(startOpen);
  const count = episodes.length || total || 0;
  const aired = episodes.filter(isAired);
  const done = aired.filter(e => marks.has(e.id)).length;
  const upcoming = episodes.length - aired.length;
  const remaining = aired.filter(e => !marks.has(e.id));
  const state = !entry ? '' : !aired.length ? t('Not aired yet') : done >= aired.length ? t('Watched') : done ? t('{done} of {aired} watched', { done, aired: aired.length }) : special ? t('Not watched') : t('Not started');
  const meta = [year, count ? plural(count, '{n} episode', '{n} episodes') : t('No episodes listed yet'), upcoming ? plural(upcoming, '{n} not aired yet', '{n} not aired yet') : ''].filter(Boolean).join(' · ');
  return <details className={`season-block${special ? ' special' : ''}`} open={open} onToggle={e => setOpen(e.currentTarget.open)}>
    <summary>
      <span className="season-text"><span className="season-name">{name}</span><span className="season-meta">{meta}</span></span>
      {state && <span className={`season-state num${done && done >= aired.length ? ' done' : ''}`}>{state}</span>}
      <ChevronDown aria-hidden className="season-chevron" />
      {entry && aired.length > 0 && !special && <span className="season-meter" aria-hidden><span style={{ width: `${Math.round((done / aired.length) * 100)}%` }} /></span>}
    </summary>
    {open && <div className="season-body">
      {entry && remaining.length > 1 && <button className="button small ghost season-mark" disabled={actions.busy} onClick={() => void actions.mark.run(remaining.map(e => e.id))}><Check aria-hidden />{t('Mark {count} aired as watched', { count: remaining.length })}</button>}
      {episodes.length ? <EpisodeRows episodes={episodes} entry={entry} marks={marks} ratings={ratings} actions={actions} /> : <p className="item-empty">{t('The provider has not listed these episodes yet.')}</p>}
    </div>}
  </details>;
}

const issueNumber = (e: Item) => e.episode_number ?? (e.details as { issue_number?: string }).issue_number ?? null;

function EpisodeRows({ episodes, entry, marks, ratings, actions, cap }: { episodes: Item[]; entry: Entry | null; marks: Map<string, string>; ratings: Ratings; actions: Actions; cap?: number }) {
  const ratingsOn = useRatingsOn(); const [all, setAll] = useState(false);
  const shown = cap && !all ? episodes.slice(0, cap) : episodes;
  return <>
    <ol className="ep-list">{shown.map(e => {
      const mark = marks.get(e.id); const skipped = mark === 'skipped'; const playID = skipped ? undefined : mark; const future = !isAired(e);
      const label = [e.kind === 'episode' ? episodeCode(e.season_number, e.episode_number) : issueNumber(e) != null ? `#${issueNumber(e)}` : '', e.title].filter(Boolean).join(' ');
      return <li key={e.id} className={playID ? 'watched' : skipped ? 'skipped' : future ? 'future' : ''}>
        {entry && !future ? skipped
          ? <button className="ep-toggle" disabled={actions.busy} aria-pressed="true" aria-label={t('Stop skipping {title}', { title: label })} onClick={() => void actions.skip.run({ ids: [e.id], skip: false })}><SkipForward aria-hidden /></button>
          : <button className="ep-toggle" disabled={actions.busy} aria-pressed={!!playID} aria-label={playID ? t('Mark {title} as not watched', { title: label }) : t('Mark {title} as watched', { title: label })} onClick={() => void (playID ? actions.unmark.run(playID) : actions.mark.run([e.id]))}>{playID ? <CircleCheckBig aria-hidden /> : <Circle aria-hidden />}</button>
          : <span className="ep-toggle" aria-hidden />}
        <span className="ep-num num">{issueNumber(e) ?? ''}</span>
        <div className="ep-body"><p className="ep-title">{e.title}</p>
          <p className="ep-meta">{[skipped && t('Skipped'), e.release_date && (future ? t('Airs {date}', { date: dayText(e.release_date, true) }) : dayText(e.release_date, true)), e.runtime_minutes ? duration(e.runtime_minutes) : ''].filter(Boolean).join(' · ')}</p></div>
        <div className="ep-side">
          {entry && playID && ratingsOn && e.kind === 'episode' ? <EpisodeRatingSelect entryID={entry.id} episode={e} value={ratings.get(e.id)} />
            : !playID && !skipped && !future ? <>{entry && e.kind === 'episode' && <button className="button small ghost ep-skip" disabled={actions.busy} aria-label={t('Skip {title}', { title: label })} onClick={() => void actions.skip.run({ ids: [e.id], skip: true })}>{t('Skip')}</button>}<CheckInButton catalogID={e.id} compact /></> : null}
        </div>
      </li>;
    })}</ol>
    {cap && episodes.length > cap && <button className="text-button" onClick={() => setAll(!all)}>{all ? t('Show fewer') : plural(episodes.length, 'Show all {n}', 'Show all {n}')}</button>}
  </>;
}

// ---------------------------------------------------------------- about the title

function TitleFacts({ item, index }: { item: Item; index: EpisodeIndex | null }) {
  const seasons = (index?.seasons ?? []).filter(s => (s.season_number ?? 0) > 0).length;
  const rows: [string, string][] = [[t('Type'), typeLabel(item.kind)]];
  if (item.release_date) rows.push([item.kind === 'series' ? t('First aired') : t('Released'), dayText(item.release_date, true)]);
  else if (item.year) rows.push([t('Year'), String(item.year)]);
  if (item.runtime_minutes) rows.push([item.kind === 'series' ? t('Episode length') : t('Runtime'), duration(item.runtime_minutes)]);
  if (seasons) rows.push([t('Seasons'), seasons.toLocaleString(locale)]);
  const episodes = episodeCount(index);
  if (episodes) rows.push([t('Episodes'), episodes]);
  else if (item.total && item.kind !== 'movie') rows.push([sentence(unitLabel(item.unit)), item.total.toLocaleString(locale)]);
  if (item.release_status) rows.push([t('Production'), sentence(item.release_status)]);
  if (item.provider_rating !== null) rows.push([t('{provider} rating', { provider: providerName(item.provider) }), `${item.provider_rating.toFixed(1)}/10`]);
  return <section className="rail-block" aria-labelledby="title-facts-heading"><h2 id="title-facts-heading">{t('Details')}</h2>
    <dl className="fact-list">{rows.map(([k, v]) => <div key={k}><dt>{k}</dt><dd>{v}</dd></div>)}</dl>
    {item.genres.length > 3 && <p className="rail-note">{item.genres.join(', ')}</p>}
  </section>;
}

type PersonRef = { id: string; name: string; role?: string; image_url?: string };
type StudioRef = { id: string; name: string; kind: 'company' | 'network' };

/** Cast, crew and studios. They link to their own pages only when the catalog can open them. */
function Credits({ item }: { item: Item }) {
  const providers = useQuery({ queryKey: ['catalog-providers'], staleTime: 300_000, queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/catalog/providers', { signal })).items });
  const linked = !!providers.data?.some(p => p.name === item.provider && p.configured);
  const [all, setAll] = useState(false);
  const details = item.details as { people?: PersonRef[]; studio_refs?: StudioRef[] };
  const people = details.people ?? []; const studios = details.studio_refs ?? [];
  const shown = useMemo(() => all ? people : people.slice(0, 8), [all, people]);
  if (!people.length && !studios.length) return item.credits.length ? <section className="item-section" aria-labelledby="credits-heading"><div className="item-section-head"><h2 id="credits-heading">{t('Credits')}</h2></div><p className="credit-names">{item.credits.slice(0, 16).join(', ')}</p></section> : null;
  const person = (p: PersonRef) => <><span className="person-face">{p.image_url ? <img src={p.image_url} alt="" loading="lazy" decoding="async" referrerPolicy="no-referrer" /> : <UserRound aria-hidden />}</span>
    <span className="person-words"><span className="person-name">{p.name}</span>{p.role && <span className="person-role">{p.role}</span>}</span></>;
  return <>
    {people.length > 0 && <section className="item-section" aria-labelledby="cast-heading">
      <div className="item-section-head"><h2 id="cast-heading">{item.kind === 'music' ? t('Artists') : t('Cast and crew')}</h2><span className="num">{people.length.toLocaleString(locale)}</span></div>
      <ul className="people-list">{shown.map(p => <li key={p.id + (p.role ?? '')}>{linked ? <SourceLink className="person" to={`/people/${item.provider}/${p.id}`}>{person(p)}</SourceLink> : <span className="person">{person(p)}</span>}</li>)}</ul>
      {people.length > 8 && <button className="text-button" onClick={() => setAll(!all)}>{all ? t('Show fewer') : plural(people.length, 'Show all {n} people', 'Show all {n} people')}</button>}
    </section>}
    {studios.length > 0 && <section className="item-section" aria-labelledby="studios-heading">
      <div className="item-section-head"><h2 id="studios-heading">{item.kind === 'series' ? t('Networks and studios') : t('Studios')}</h2></div>
      <ul className="credit-names">{studios.map(st => <li key={st.kind + st.id}>{linked ? <SourceLink className="credit-link" to={`/studios/${item.provider}/${st.kind}/${st.id}`}>{st.name}</SourceLink> : st.name}</li>)}</ul>
    </section>}
  </>;
}

function TimeToBeat({ item }: { item: Item }) {
  const beat = (item.details as { time_to_beat?: { main?: number; main_extra?: number; completionist?: number; submissions?: number } }).time_to_beat;
  if (!beat) return null;
  const rows = ([[t('Main story'), beat.main], [t('Main and extras'), beat.main_extra], [t('Completionist'), beat.completionist]] as const).filter(([, v]) => v);
  if (!rows.length) return null;
  return <section className="item-section" aria-labelledby="beat-heading"><div className="item-section-head"><h2 id="beat-heading">{t('Time to beat')}</h2>{beat.submissions ? <span className="num">{plural(beat.submissions, 'From {n} player on IGDB', 'From {n} players on IGDB')}</span> : null}</div>
    <dl className="fact-list wide">{rows.map(([label, v]) => <div key={label}><dt>{label}</dt><dd>{duration(v!)}</dd></div>)}</dl>
  </section>;
}

function CriticScore({ catalogID }: { catalogID: string }) {
  const result = useQuery({ queryKey: ['critic-score', catalogID], queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/catalog/{catalogID}/critic', { signal, params: { path: { catalogID } } })), refetchInterval: query => query.state.data?.status === 'pending' ? 5000 : false });
  const score = result.data; if (!score || score.status === 'not_configured') return null;
  return <section className="item-section" aria-labelledby="critic-heading"><div className="item-section-head"><h2 id="critic-heading">{t('Critic score')}</h2>{score.external_id && <a className="text-link" href={`https://opencritic.com/game/${score.external_id}`} target="_blank" rel="noreferrer">OpenCritic</a>}</div>
    {score.score != null ? <p className="critic-line"><strong className="num">{score.score.toFixed(0)}</strong><span className="subtle">/100</span>{score.tier && <span> · {score.tier}</span>}{score.recommended != null && <span className="subtle"> · {t('{value}% recommend it', { value: score.recommended.toFixed(0) })}</span>}</p> : <p className="item-empty">{statusText(score.status)}</p>}
  </section>;
}
