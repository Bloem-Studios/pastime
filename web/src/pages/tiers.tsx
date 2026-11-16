import { useEffect, useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ArrowDown, ArrowUp, Download, Plus, Star, Trash2 } from 'lucide-react';
import { api, keyHeader, unwrap, type Schema, type Summary } from '../api/client';
import { privateKey, useRefresh, useUser } from '../state';
import { Cover } from '../media';
import { useRatingsOn } from '../organize';
import { ErrorNote, Loading, useAction } from '../ui';
import { t } from '../i18n';

type Tier = { label: string; color: string | null; ids: string[] };
const UNRANKED = -1;
const palette = ['#e5484d', '#f76b15', '#ffc53d', '#46a758', '#0090ff', '#8e4ec6'];

// Rating bands for "Fill from ratings", on the stored 0–10 scale.
const bands: [string, number][] = [['S', 9], ['A', 7], ['B', 5], ['C', 3], ['D', 0]];

/** Every member of a list, read page by page (lists hold at most a few thousand). */
function useMembers(listID: string, version: number) {
  const user = useUser();
  return useQuery({
    queryKey: privateKey(user.id, 'list-members', listID, version),
    queryFn: async ({ signal }) => {
      const out: Summary[] = []; let cursor: string | undefined;
      do {
        const page = unwrap(await api.GET('/api/v1/library', { signal, params: { query: { list_id: listID, cursor, limit: 100 } } }));
        out.push(...page.items); cursor = page.next_cursor ?? undefined;
      } while (cursor && out.length < 5000);
      return out;
    },
  });
}

export function TierBoard({ list }: { list: Schema['List'] }) {
  const user = useUser(); const refresh = useRefresh(); const ratingsOn = useRatingsOn();
  const board = useQuery({ queryKey: privateKey(user.id, 'list-tiers', list.id, list.version), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/lists/{listID}/tiers', { signal, params: { path: { listID: list.id } } })) });
  const members = useMembers(list.id, list.version);
  const [tiers, setTiers] = useState<Tier[]>([]); const [unranked, setUnranked] = useState<string[]>([]);
  const [picked, setPicked] = useState<string | null>(null); const [dirty, setDirty] = useState(false);
  useEffect(() => {
    if (!board.data) return;
    setTiers(board.data.tiers.map(x => ({ label: x.label, color: x.color, ids: x.entry_ids })));
    setUnranked(board.data.unranked); setDirty(false);
  }, [board.data]);
  const byID = useMemo(() => new Map((members.data ?? []).map(e => [e.id, e])), [members.data]);
  const save = useAction(async (_: void, key) => {
    unwrap(await api.PUT('/api/v1/lists/{listID}/tiers', { params: { path: { listID: list.id }, header: keyHeader(key) }, body: { version: board.data!.version, tiers: tiers.map(x => ({ label: x.label, color: x.color, entry_ids: x.ids })) } }));
    setDirty(false); await refresh();
  });
  if (board.isPending || members.isPending) return <Loading label={t('Loading tiers')} />;
  if (board.error || members.error) return <ErrorNote error={board.error ?? members.error} />;

  const change = (next: Tier[], rest: string[]) => { setTiers(next); setUnranked(rest); setDirty(true); };
  const without = (id: string): [Tier[], string[]] => [tiers.map(x => ({ ...x, ids: x.ids.filter(v => v !== id) })), unranked.filter(v => v !== id)];
  const place = (target: number) => {
    if (!picked) return;
    const [next, rest] = without(picked);
    if (target === UNRANKED) change(next, [...rest, picked]); else { next[target] = { ...next[target], ids: [...next[target].ids, picked] }; change(next, rest); }
    setPicked(null);
  };
  const shift = (tier: number, index: number, delta: number) => {
    const next = tiers.map(x => ({ ...x, ids: [...x.ids] })); const ids = next[tier].ids;
    const [id] = ids.splice(index, 1); ids.splice(index + delta, 0, id); change(next, unranked);
  };
  const moveTier = (index: number, delta: number) => { const next = [...tiers]; const [x] = next.splice(index, 1); next.splice(index + delta, 0, x); change(next, unranked); };
  const fill = () => {
    const next: Tier[] = bands.map(([label], i) => ({ label, color: palette[i], ids: [] })); const rest: string[] = [];
    for (const e of members.data!) {
      const band = e.rating == null ? -1 : bands.findIndex(([, min]) => e.rating! >= min);
      if (band < 0) rest.push(e.id); else next[band].ids.push(e.id);
    }
    for (const x of next) x.ids.sort((a, b) => (byID.get(b)?.rating ?? 0) - (byID.get(a)?.rating ?? 0));
    change(next, rest);
  };

  const card = (id: string, tier: number, index: number, count: number) => {
    const e = byID.get(id); if (!e) return null;
    return <li key={id} className={`tier-item${picked === id ? ' picked' : ''}`}>
      <button type="button" className="tier-pick" aria-pressed={picked === id} title={e.title} onClick={() => setPicked(picked === id ? null : id)}><Cover entry={e} compact /><span>{e.title}</span></button>
      {tier !== UNRANKED && <span className="tier-shift">
        <button type="button" className="icon-button" aria-label={t('Move {title} earlier', { title: e.title })} disabled={index === 0} onClick={() => shift(tier, index, -1)}><ArrowUp aria-hidden /></button>
        <button type="button" className="icon-button" aria-label={t('Move {title} later', { title: e.title })} disabled={index === count - 1} onClick={() => shift(tier, index, 1)}><ArrowDown aria-hidden /></button>
      </span>}
    </li>;
  };

  return <section className="tier-board" aria-label={t('Tier board')}>
    <div className="toolbar">
      <button type="button" className="button" disabled={tiers.length >= 20} onClick={() => change([...tiers, { label: t('New tier'), color: palette[tiers.length % palette.length], ids: [] }], unranked)}><Plus aria-hidden />{t('Add tier')}</button>
      {ratingsOn && <button type="button" className="button" onClick={fill}><Star aria-hidden />{t('Fill from ratings')}</button>}
      <button type="button" className="button" onClick={() => void exportBoard(list.name, tiers, unranked, byID)}><Download aria-hidden />{t('Export image')}</button>
      <button type="button" className="button primary" disabled={!dirty || save.pending} onClick={() => void save.run()}>{t('Save board')}</button>
    </div>
    <ErrorNote error={save.error} />
    {picked && <p role="status">{t('Choose a tier for {title}.', { title: byID.get(picked)?.title ?? '' })}</p>}
    {tiers.map((tier, i) => <div key={i} className="tier-row">
      <div className="tier-label" style={{ background: tier.color ?? undefined }}>
        <input aria-label={t('Tier name')} maxLength={40} value={tier.label} onChange={e => change(tiers.map((x, j) => j === i ? { ...x, label: e.target.value } : x), unranked)} />
        <input aria-label={t('Tier color')} type="color" value={tier.color ?? '#888888'} onChange={e => change(tiers.map((x, j) => j === i ? { ...x, color: e.target.value } : x), unranked)} />
        <span className="tier-shift">
          <button type="button" className="icon-button" aria-label={t('Move tier up')} disabled={i === 0} onClick={() => moveTier(i, -1)}><ArrowUp aria-hidden /></button>
          <button type="button" className="icon-button" aria-label={t('Move tier down')} disabled={i === tiers.length - 1} onClick={() => moveTier(i, 1)}><ArrowDown aria-hidden /></button>
          <button type="button" className="icon-button" aria-label={t('Delete tier {name}', { name: tier.label })} onClick={() => change(tiers.filter((_, j) => j !== i), [...unranked, ...tier.ids])}><Trash2 aria-hidden /></button>
        </span>
      </div>
      <ol className="tier-items">
        {tier.ids.map((id, index) => card(id, i, index, tier.ids.length))}
        {picked && <li><button type="button" className="button" onClick={() => place(i)}>{t('Place here')}</button></li>}
      </ol>
    </div>)}
    <div className="tier-row unranked">
      <div className="tier-label"><strong>{t('Unranked')}</strong></div>
      <ol className="tier-items">
        {unranked.map((id, index) => card(id, UNRANKED, index, unranked.length))}
        {picked && !unranked.includes(picked) && <li><button type="button" className="button" onClick={() => place(UNRANKED)}>{t('Place here')}</button></li>}
      </ol>
    </div>
  </section>;
}

/** Draws the board to a canvas and downloads it as a PNG. Titles are drawn as text, so
 * the export never depends on third-party artwork servers allowing canvas use. */
async function exportBoard(name: string, tiers: Tier[], unranked: string[], byID: Map<string, Summary>) {
  const rows = unranked.length ? [...tiers, { label: t('Unranked'), color: '#555555', ids: unranked }] : tiers;
  const width = 1200, label = 140, tile = 150, gap = 8, perRow = Math.floor((width - label - gap) / (tile + gap));
  const heights = rows.map(x => Math.max(1, Math.ceil(x.ids.length / perRow)) * (tile * 0.5 + gap) + gap);
  const canvas = document.createElement('canvas');
  canvas.width = width; canvas.height = 70 + heights.reduce((a, b) => a + b, 0);
  const g = canvas.getContext('2d')!;
  g.fillStyle = '#16161a'; g.fillRect(0, 0, canvas.width, canvas.height);
  g.fillStyle = '#ffffff'; g.font = 'bold 28px system-ui, sans-serif'; g.fillText(name, 16, 44);
  let y = 70;
  rows.forEach((row, r) => {
    g.fillStyle = row.color ?? '#888888'; g.fillRect(0, y, label, heights[r] - gap);
    g.fillStyle = '#111111'; g.font = 'bold 26px system-ui, sans-serif'; g.fillText(row.label.slice(0, 10), 12, y + 38);
    row.ids.forEach((id, i) => {
      const x = label + gap + (i % perRow) * (tile + gap), top = y + gap + Math.floor(i / perRow) * (tile * 0.5 + gap);
      g.fillStyle = '#2a2a31'; g.fillRect(x, top, tile, tile * 0.5);
      g.fillStyle = '#f2f2f2'; g.font = '14px system-ui, sans-serif';
      wrap(g, byID.get(id)?.title ?? '', x + 8, top + 20, tile - 16, 17, 3);
    });
    y += heights[r];
  });
  const blob = await new Promise<Blob | null>(done => canvas.toBlob(done, 'image/png'));
  if (!blob) return;
  const link = document.createElement('a'); link.href = URL.createObjectURL(blob);
  link.download = `${name.replace(/[^\p{L}\p{N}]+/gu, '-').replace(/^-|-$/g, '') || 'tiers'}.png`;
  link.click(); setTimeout(() => URL.revokeObjectURL(link.href), 1000);
}

function wrap(g: CanvasRenderingContext2D, text: string, x: number, y: number, max: number, line: number, lines: number) {
  const words = text.split(/\s+/); let current = ''; let n = 0;
  for (const word of words) {
    const next = current ? `${current} ${word}` : word;
    if (g.measureText(next).width > max && current) {
      g.fillText(n === lines - 1 ? `${current}…` : current, x, y + n * line); n++; current = word;
      if (n === lines) return;
    } else current = next;
  }
  if (current && n < lines) g.fillText(current, x, y + n * line);
}
