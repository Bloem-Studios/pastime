import { ContextBackLink, SourceLink } from '../navigation';
import { useState } from 'react';
import { useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Building2, Tv, UserRound } from 'lucide-react';
import { api, unwrap, type Schema } from '../api/client';
import { privateKey, useUser } from '../state';
import { Shelf, typeLabel } from '../media';
import { Empty, Failed, Loading, PageTitle } from '../ui';
import { SearchResult } from './discover';
import { locale, t } from '../i18n';

type Item = Schema['CatalogItem'];
type PersonRef = { id: string; name: string; role?: string; image_url?: string };
type StudioRef = { id: string; name: string; kind: 'company' | 'network' };

/** Cast, crew and studios of a title, linking to their pages where the provider has them. */
export function CreditLinks({ item }: { item: Item }) {
  const details = item.details as { people?: PersonRef[]; studio_refs?: StudioRef[] };
  const people = details.people ?? []; const studios = details.studio_refs ?? [];
  if (!people.length && !studios.length) return item.credits.length > 0 ? <section className="detail-section"><h2>{t('Credits')}</h2><p className="subtle">{item.credits.slice(0, 16).join(' · ')}</p></section> : null;
  return <>
    {people.length > 0 && <section className="detail-section"><h2>{item.kind === 'music' ? t('Artists') : t('Cast and crew')}</h2>
      <ul className="people-row">{people.map(p => <li key={p.id + (p.role ?? '')}><SourceLink className="person-chip" to={`/people/${item.provider}/${p.id}`}>
        <span className="person-avatar">{p.image_url ? <img src={p.image_url} alt="" loading="lazy" decoding="async" referrerPolicy="no-referrer" /> : <UserRound aria-hidden />}</span>
        <span className="person-text"><strong>{p.name}</strong>{p.role && <span className="subtle">{p.role}</span>}</span></SourceLink></li>)}</ul></section>}
    {studios.length > 0 && <section className="detail-section"><h2>{item.kind === 'series' ? t('Networks and studios') : t('Studios')}</h2>
      <ul className="chips link-chips">{studios.map(st => <li key={st.kind + st.id}><SourceLink to={`/studios/${item.provider}/${st.kind}/${st.id}`}>{st.kind === 'network' ? <Tv aria-hidden /> : <Building2 aria-hidden />}{st.name}</SourceLink></li>)}</ul></section>}
  </>;
}

function lifespan(p: Schema['PersonPage']) {
  const fmt = (d: string) => new Date(d + 'T00:00:00').toLocaleDateString(locale, { year: 'numeric', month: 'long', day: 'numeric' });
  if (p.birthday && p.deathday) return `${fmt(p.birthday)} – ${fmt(p.deathday)}`;
  return p.birthday ? t('Born {birthday}', { birthday: fmt(p.birthday) }) : null;
}

export function PersonPage() {
  const { provider = '', personID = '' } = useParams(); const user = useUser();
  const query = useQuery({ queryKey: privateKey(user.id, 'person', provider, personID), staleTime: 3_600_000, queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/people/{provider}/{personID}', { signal, params: { path: { provider, personID } } })) });
  const [expanded, setExpanded] = useState(false);
  if (query.isPending) return <Loading label={t('Loading person')} />;
  if (query.error) return <><ContextBackLink /><Failed error={query.error} retry={() => void query.refetch()} /></>;
  const p = query.data; const life = lifespan(p);
  const known = p.credits.slice(0, 16); const rest = p.credits.slice(16);
  return <>
    <ContextBackLink />
    <header className="person-hero">
      <div className="person-portrait">{p.image_url ? <img src={p.image_url} alt="" decoding="async" referrerPolicy="no-referrer" /> : <UserRound aria-hidden />}</div>
      <div className="person-hero-body">
        <p className="eyebrow">{[p.known_for, life].filter(Boolean).join(' · ') || 'Person'}</p>
        <h1 tabIndex={-1}>{p.name}</h1>
        {p.biography && <p className={`person-bio${expanded ? ' expanded' : ''}`}>{p.biography}</p>}
        {p.biography.length > 600 && <button className="text-button" onClick={() => setExpanded(v => !v)}>{expanded ? t('Show less') : t('Read more')}</button>}
      </div>
    </header>
    {known.length > 0 && <Shelf title={t('Known for')}>{known.map(c => <SearchResult key={c.media_type + c.provider_id} result={c} />)}</Shelf>}
    {rest.length > 0 && <section className="detail-section"><h2>{t('More credits')}</h2>
      <ul className="credit-list">{rest.map(c => <li key={c.media_type + c.provider_id}><span className="credit-year">{c.year ?? '—'}</span><span className="credit-title">{c.title}</span><span className="subtle">{[typeLabel(c.media_type), c.role].filter(Boolean).join(' · ')}</span></li>)}</ul></section>}
  </>;
}

export function StudioPage() {
  const { provider = '', studioKind = 'company', studioID = '' } = useParams(); const user = useUser();
  const query = useQuery({ queryKey: privateKey(user.id, 'studio', provider, studioKind, studioID), staleTime: 3_600_000,
    queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/studios/{provider}/{studioKind}/{studioID}', { signal, params: { path: { provider, studioKind: studioKind as 'company' | 'network', studioID } } })) });
  if (query.isPending) return <Loading label={t('Loading studio')} />;
  if (query.error) return <><ContextBackLink /><Failed error={query.error} retry={() => void query.refetch()} /></>;
  const st = query.data;
  return <>
    <ContextBackLink />
    <PageTitle eyebrow={[st.kind === 'network' ? t('Network') : t('Studio'), st.country].filter(Boolean).join(' · ')} title={st.name} description={st.description || undefined}
      actions={st.logo_url ? <span className="studio-logo"><img src={st.logo_url} alt="" referrerPolicy="no-referrer" /></span> : undefined} />
    {st.titles.length ? <div className="result-grid">{st.titles.map(item => <SearchResult key={item.media_type + item.provider_id} result={item} />)}</div> : <Empty title={t('No titles listed')}>{t('The provider has no popular titles for this studio.')}</Empty>}
  </>;
}

export { RecommendationsPage } from './recommended';
