import { useState } from 'react';
import { useInfiniteQuery } from '@tanstack/react-query';
import { api, unwrap } from './api/client';
import { privateKey, useUser } from './state';
import { ErrorNote, statusText } from './ui';
import { providerNames } from './media';
import { dayText } from './pages/tracking';
import { clock } from './format';
import { t } from './i18n';

/** Each change of a title's status, newest first: what it became, who changed it and when. */
export function StatusHistory({ entryID }: { entryID: string }) {
  const user = useUser(); const [all, setAll] = useState(false);
  const history = useInfiniteQuery({ queryKey: privateKey(user.id, 'status-history', entryID), initialPageParam: undefined as string | undefined,
    queryFn: async ({ pageParam, signal }) => unwrap(await api.GET('/api/v1/library/{entryID}/status-history', { signal, params: { path: { entryID }, query: { cursor: pageParam } } })),
    getNextPageParam: page => page.next_cursor ?? undefined });
  const items = history.data?.pages.flatMap(p => p.items) ?? [];
  const shown = all ? items : items.slice(0, 5);
  if (history.isPending || (!items.length && !history.error)) return null;
  return <section className="item-section status-history" aria-labelledby={`status-history-${entryID}`}>
    <div className="item-section-head"><h2 id={`status-history-${entryID}`}>{t('Status history')}</h2></div>
    <ErrorNote error={history.error} />
    <ol className="change-list">{shown.map(v => <li key={v.id}>
      <div><p>{v.previous_status ? `${statusText(v.previous_status)} → ${statusText(v.status)}` : statusText(v.status)}</p><p className="change-source">{changeSource(v.source)}</p></div>
      <p className="change-when num"><time dateTime={v.created_at} title={new Date(v.created_at).toLocaleString()}>{dayText(v.created_at)}</time><span>{clock(v.created_at)}</span></p>
    </li>)}</ol>
    {(items.length > 5 || history.hasNextPage) && (!all || history.hasNextPage) && <button className="text-button" disabled={history.isFetchingNextPage} onClick={() => { if (all) void history.fetchNextPage(); else setAll(true); }}>{all ? t('Show older changes') : t('Show all changes')}</button>}
  </section>;
}

/** A service named in a source such as "import:trakt": "Trakt". */
export const sourceName = (name: string) => providerNames[name] ?? name.charAt(0).toUpperCase() + name.slice(1);

export function changeSource(source: string) {
  if (source.startsWith('user')) return t('Your edit');
  if (source.startsWith('import:')) return t('Imported from {name}', { name: sourceName(source.slice(7)) });
  if (source.startsWith('scrobble:')) return t('Playback from {name}', { name: sourceName(source.slice(9)) });
  return t('Automatic tracking');
}
