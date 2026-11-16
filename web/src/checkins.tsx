import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { CirclePlay, Flag, Plus, Trash2, X } from 'lucide-react';
import { api, keyHeader, unwrap, type Schema } from './api/client';
import { privateKey, useRefresh, useUser } from './state';
import { Cover, Shelf, unitLabel } from './media';
import { ErrorNote, Field, Modal, submit, useAction } from './ui';
import { locale, plural, t } from './i18n';
import { clock, duration, episodeCode } from './format';

type Checkin = Schema['Checkin'];
type Seat = { name: string; score: string; winner: boolean };

// Active check-ins: what the profile has started and not finished. Films and episodes
// finish on their own at the end of their runtime, so the list refreshes each minute.
export function useCheckins() {
  const user = useUser();
  return useQuery({ queryKey: privateKey(user.id, 'checkins'), refetchInterval: 60_000, queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/checkins', { signal })).items });
}

function useAfterCheckin() {
  const user = useUser(); const client = useQueryClient(); const refresh = useRefresh();
  return async () => { await client.invalidateQueries({ queryKey: privateKey(user.id, 'checkins') }); await refresh(); };
}

function useNow(every = 30_000) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => { const id = window.setInterval(() => setNow(Date.now()), every); return () => window.clearInterval(id); }, [every]);
  return now;
}

const minutesSince = (from: string, now: number) => Math.max(0, Math.round((now - Date.parse(from)) / 60_000));
function partLabel(c: Checkin) {
  if (!c.item_title) return null;
  return c.season_number != null && c.episode_number != null ? `${episodeCode(c.season_number, c.episode_number)} · ${c.item_title}` : c.item_title;
}

// CheckInButton starts a check-in on a title or one of its episodes, issues or tracks.
export function CheckInButton({ catalogID, compact = false }: { catalogID: string; compact?: boolean }) {
  const checkins = useCheckins(); const after = useAfterCheckin();
  const start = useAction(async (_: void, key) => { unwrap(await api.POST('/api/v1/checkins', { params: { header: keyHeader(key) }, body: { catalog_id: catalogID } })); await after(); });
  if (checkins.data?.some(c => c.catalog_id === catalogID)) return <span className="button checked-in" role="status"><CirclePlay aria-hidden />{compact ? null : t('Checked in')}</span>;
  return <>
    <button type="button" className="button" aria-label={compact ? t('Check in') : undefined} disabled={start.pending || checkins.isPending} onClick={() => void start.run()}>
      <CirclePlay aria-hidden />{compact ? null : start.pending ? t('Checking in…') : t('Check in')}
    </button>
    <ErrorNote error={start.error} />
  </>;
}

// NowPlaying is the row of active check-ins, each with Finish and Cancel.
export function NowPlaying() {
  const checkins = useCheckins(); const now = useNow(); const after = useAfterCheckin();
  const [finishing, setFinishing] = useState<Checkin | null>(null);
  const cancel = useAction(async (id: string, key) => { unwrap(await api.POST('/api/v1/checkins/{checkinID}/cancel', { params: { path: { checkinID: id }, header: keyHeader(key) } })); await after(); });
  const items = checkins.data ?? [];
  if (items.length === 0) return checkins.error ? <ErrorNote error={checkins.error} /> : null;
  return <>
    <Shelf title={t('Now playing')} count={items.length}>
      {items.map(c => <article key={c.id} className="now-playing-card">
        <Cover entry={{ title: c.title, media_type: c.media_kind, image_url: c.image_url }} />
        <div className="now-playing-body">
          <h3>{c.title}</h3>
          {partLabel(c) && <p className="subtle">{partLabel(c)}</p>}
          <p className="subtle">{t('Started {time} ago', { time: duration(minutesSince(c.started_at, now)) })}</p>
          <div className="actions">
            <button type="button" className="button primary" onClick={() => setFinishing(c)}><Flag aria-hidden />{t('Finish')}</button>
            <button type="button" className="button ghost" disabled={cancel.pending} onClick={() => void cancel.run(c.id)}><X aria-hidden />{t('Cancel check-in')}</button>
          </div>
        </div>
      </article>)}
    </Shelf>
    <ErrorNote error={cancel.error} />
    {finishing && <FinishDialog checkin={finishing} onClose={() => setFinishing(null)} />}
  </>;
}

const wholeNumber = (value: string) => { const n = Number(value); return value.trim() !== '' && Number.isFinite(n) && n >= 0 ? Math.round(n) : undefined; };

function FinishDialog({ checkin, onClose }: { checkin: Checkin; onClose: () => void }) {
  const after = useAfterCheckin();
  const session = checkin.kind === 'session'; const board = checkin.media_kind === 'boardgame'; const game = checkin.media_kind === 'game';
  const [minutes, setMinutes] = useState(() => String(minutesSince(checkin.started_at, Date.now())));
  const [amount, setAmount] = useState(''); const [completed, setCompleted] = useState(false);
  const [platform, setPlatform] = useState(''); const [location, setLocation] = useState('');
  const [seats, setSeats] = useState<Seat[]>([{ name: '', score: '', winner: false }, { name: '', score: '', winner: false }]);
  const finish = useAction(async (body: Schema['CheckinFinish'], key) => {
    unwrap(await api.POST('/api/v1/checkins/{checkinID}/finish', { params: { path: { checkinID: checkin.id }, header: keyHeader(key) }, body }));
    await after(); onClose();
  });
  const seat = (i: number, change: Partial<Seat>) => setSeats(seats.map((s, j) => j === i ? { ...s, ...change } : s));
  function save() {
    const body: Schema['CheckinFinish'] = {};
    const spent = wholeNumber(minutes); if (spent !== undefined) body.minutes = Math.min(spent, 100000);
    if (session) { const read = wholeNumber(amount); if (read !== undefined && !game) body.amount = read; body.completed = completed; }
    if (game && platform.trim()) body.platform = platform.trim();
    if (board) {
      const players = seats.filter(s => s.name.trim()).map(s => { const score = Number(s.score); return { name: s.name.trim(), winner: s.winner, ...(s.score.trim() !== '' && Number.isFinite(score) ? { score: score } : {}) }; });
      if (players.length) body.players = players;
      if (location.trim()) body.location = location.trim();
    }
    void finish.run(body);
  }
  return <Modal title={t('Finish {title}', { title: checkin.title })} description={session ? t('Record this session. Progress moves on, and the title is completed only when you finish it.') : t('Record a play with the time you spent.')} onClose={onClose} busy={finish.pending} wide={board}>
    <form onSubmit={submit(save)}>
      <Field label={t('Minutes spent')}>{id => <input id={id} type="number" inputMode="numeric" min={0} max={100000} value={minutes} onChange={e => setMinutes(e.target.value)} />}</Field>
      {session && !game && <Field label={t('{unit} read', { unit: unitLabel(checkin.unit) })}>{id => <input id={id} type="number" inputMode="numeric" min={0} value={amount} onChange={e => setAmount(e.target.value)} />}</Field>}
      {game && <Field label={t('Platform')}>{id => <input id={id} maxLength={60} value={platform} onChange={e => setPlatform(e.target.value)} />}</Field>}
      {session && <label className="check-row"><input type="checkbox" checked={completed} onChange={e => setCompleted(e.target.checked)} /> {t('I finished it')}</label>}
      {board && <fieldset className="player-list"><legend>{t('Players')}</legend>
        {seats.map((s, i) => <div key={i} className="player-row">
          <input aria-label={t('Player name')} placeholder={t('Name')} maxLength={80} value={s.name} onChange={e => seat(i, { name: e.target.value })} />
          <input aria-label={t('Score')} placeholder={t('Score')} type="number" step="any" inputMode="decimal" value={s.score} onChange={e => seat(i, { score: e.target.value })} />
          <label className="check-row"><input type="checkbox" checked={s.winner} onChange={e => seat(i, { winner: e.target.checked })} /> {t('Winner')}</label>
          <button type="button" className="button ghost" aria-label={t('Remove player')} onClick={() => setSeats(seats.filter((_, j) => j !== i))}><Trash2 aria-hidden /></button>
        </div>)}
        {seats.length < 20 && <button type="button" className="button ghost" onClick={() => setSeats([...seats, { name: '', score: '', winner: false }])}><Plus aria-hidden />{t('Add player')}</button>}
      </fieldset>}
      {board && <Field label={t('Where you played')}>{id => <input id={id} maxLength={120} value={location} onChange={e => setLocation(e.target.value)} />}</Field>}
      <ErrorNote error={finish.error} />
      <div className="dialog-actions"><button className="button primary" type="submit" disabled={finish.pending}>{finish.pending ? t('Saving…') : t('Finish')}</button></div>
    </form>
  </Modal>;
}

// PlayHistorySection lists a title's plays and finished sessions with totals and, for
// board games, how often each player played and won.
export function PlayHistorySection({ entryID, boardGame }: { entryID: string; boardGame: boolean }) {
  const user = useUser();
  const history = useQuery({ queryKey: privateKey(user.id, 'plays', 'history', entryID), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/library/{entryID}/play-history', { signal, params: { path: { entryID: entryID } } })) });
  const h = history.data;
  if (!h) return history.error ? <ErrorNote error={history.error} /> : null;
  if (h.items.length === 0) return null;
  const totals = [boardGame || h.plays ? plural(h.plays, '{n} play', '{n} plays') : null, h.sessions ? plural(h.sessions, '{n} session', '{n} sessions') : null, h.minutes ? duration(h.minutes) : null].filter(Boolean).join(' · ');
  const seat = (p: { name?: string; score?: number; winner?: boolean }) => `${p.name ?? ''}${typeof p.score === 'number' ? ` ${p.score.toLocaleString(locale)}` : ''}${p.winner ? ` (${t('won')})` : ''}`;
  const day = (iso: string) => new Date(iso).toLocaleDateString(locale, { month: 'short', day: 'numeric', year: new Date(iso).getFullYear() === new Date().getFullYear() ? undefined : 'numeric' });
  return <section className="item-section play-history" aria-labelledby={`play-history-${entryID}`}>
    <div className="item-section-head"><h2 id={`play-history-${entryID}`}>{t('Your plays')}</h2>{totals && <span className="num">{totals}</span>}</div>
    {boardGame && h.players.length > 0 && <table className="player-table">
      <caption className="sr-only">{t('Players')}</caption>
      <thead><tr><th scope="col">{t('Player')}</th><th scope="col" className="num">{t('Plays')}</th><th scope="col" className="num">{t('Wins')}</th></tr></thead>
      <tbody>{h.players.slice(0, 12).map(p => <tr key={p.name}><th scope="row">{p.name}</th><td className="num">{p.plays.toLocaleString(locale)}</td><td className="num">{p.wins.toLocaleString(locale)}</td></tr>)}</tbody>
    </table>}
    <ol className="play-history-list">{h.items.slice(0, 50).map((it, i) => <li key={i}>
      <div><p>{[it.item_title || (it.kind === 'session' ? t('Session') : t('Play')), it.minutes ? duration(it.minutes) : null, typeof it.details.platform === 'string' ? it.details.platform : null, typeof it.details.location === 'string' ? it.details.location : null].filter(Boolean).join(' · ')}</p>
        {Array.isArray(it.details.players) && <p className="subtle">{(it.details.players as { name?: string; score?: number; winner?: boolean }[]).map(seat).join(', ')}</p>}</div>
      <p className="num play-history-when"><time dateTime={it.at}>{day(it.at)}</time><span>{clock(it.at)}</span></p>
    </li>)}</ol>
  </section>;
}
