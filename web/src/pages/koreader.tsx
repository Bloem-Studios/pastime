import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { BookOpen, KeyRound, Link2, Unlink } from 'lucide-react';
import { api, unwrap, type Schema } from '../api/client';
import { typeLabel, unitLabel } from '../media';
import { privateKey, useUser } from '../state';
import { CopyField, ErrorNote, Failed, Field, Loading, Modal, submit, Timestamp, useAction, useConfirm } from '../ui';
import { t, tx } from '../i18n';

export function KOReaderSection() {
  const user = useUser(); const client = useQueryClient(); const confirm = useConfirm();
  const status = useQuery({ queryKey: privateKey(user.id, 'koreader'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/me/koreader', { signal })) });
  const [setting, setSetting] = useState(false); const [linking, setLinking] = useState<Schema['KOReaderDocument'] | null>(null);
  const reload = () => client.invalidateQueries({ queryKey: privateKey(user.id, 'koreader') });
  const disable = useAction(async (_: void) => { unwrap(await api.DELETE('/api/v1/me/koreader')); await reload(); });
  const unlink = useAction(async (document: string) => { unwrap(await api.PUT('/api/v1/me/koreader/documents/{document}', { params: { path: { document } }, body: { entry_id: null } })); await reload(); });
  return <section className="detail-section"><h2>{t('KOReader')}</h2>
    <p className="subtle">{t('This server speaks KOReader’s progress sync. Your reading position follows you between devices, and a synced book linked to a title moves it forward as you read.')}</p>
    {status.isPending ? <Loading label={t('Loading KOReader sync')} /> : status.error ? <Failed error={status.error} retry={() => void status.refetch()} /> : <>
      <ol className="setup-steps">
        <li>{status.data.configured ? t('Your KOReader password is set.') : t('Set a KOReader password. Use a new one; KOReader stores it with weak protection.')}</li>
        <li>{tx('In KOReader open {menu} and enter this address:', { menu: <strong>{t('Tools → Progress sync → Custom sync server')}</strong> })}<CopyField label={t('KOReader sync server address')} value={`${window.location.origin}/kosync`} /></li>
        <li>{tx('Sign in as {user} with the KOReader password.', { user: <strong>{status.data.username}</strong> })}</li>
      </ol>
      <div className="actions">
        <button className={`button${status.data.configured ? '' : ' primary'}`} onClick={() => setSetting(true)}><KeyRound aria-hidden />{status.data.configured ? t('Change password') : t('Set KOReader password')}</button>
        {status.data.configured && <button className="button danger-outline" disabled={disable.pending} onClick={async () => { if (await confirm({ title: t('Stop KOReader sync?'), description: t('Devices can no longer sign in. Synced positions stay.'), label: t('Stop sync'), danger: true })) void disable.run(); }}>{t('Stop sync')}</button>}
      </div>
      {status.data.documents.length > 0 && <ul className="koreader-documents">{status.data.documents.map(d => <li key={d.document} className="koreader-document">
        <BookOpen aria-hidden className="koreader-icon" />
        <div className="koreader-document-body"><strong>{d.entry_title ?? t('Not linked to a title')}</strong>
          <span className="subtle"><code title={d.document}>{d.document.slice(0, 10)}…</code> · {Math.round(d.percentage * 100)}%{d.device && <> · {d.device}</>} · <Timestamp value={d.updated_at} /></span>
          <progress max={1} value={d.percentage} aria-label={t('Reading position')} /></div>
        {d.entry_id ? <button className="button" disabled={unlink.pending} onClick={() => void unlink.run(d.document)}><Unlink aria-hidden />{t('Unlink')}</button>
          : <button className="button" onClick={() => setLinking(d)}><Link2 aria-hidden />{t('Link')}</button>}
      </li>)}</ul>}
      <ErrorNote error={disable.error || unlink.error} />
    </>}
    {setting && <PasswordDialog onClose={() => setSetting(false)} />}
    {linking && <LinkDocument document={linking} onClose={() => setLinking(null)} />}
  </section>;
}

function PasswordDialog({ onClose }: { onClose: () => void }) {
  const user = useUser(); const client = useQueryClient();
  const [password, setPassword] = useState('');
  const save = useAction(async (_: void) => { unwrap(await api.PUT('/api/v1/me/koreader', { body: { password } })); setPassword(''); await client.invalidateQueries({ queryKey: privateKey(user.id, 'koreader') }); onClose(); });
  return <Modal title={t('KOReader password')} description={t('Devices sign in with this password. It is separate from your account password.')} onClose={onClose} busy={save.pending} dirty={!!password}>
    <form onSubmit={submit(() => void save.run())}><fieldset disabled={save.pending}>
      <Field label={t('New KOReader password')} hint={t('At least 8 characters.')}>{(id, hint) => <input id={id} aria-describedby={hint} type="password" required minLength={8} maxLength={200} autoComplete="new-password" value={password} onChange={e => setPassword(e.target.value)} />}</Field>
    </fieldset><ErrorNote error={save.error} />
    <div className="dialog-actions"><button className="button primary" type="submit" disabled={save.pending}>{save.pending ? t('Saving…') : t('Save password')}</button></div></form>
  </Modal>;
}

function LinkDocument({ document, onClose }: { document: Schema['KOReaderDocument']; onClose: () => void }) {
  const user = useUser(); const client = useQueryClient();
  const [q, setQ] = useState('');
  const results = useQuery({ queryKey: privateKey(user.id, 'library', 'koreader-pick', q), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/library', { signal, params: { query: { q: q.trim() || undefined, media_type: 'book,audiobook,comic,manga', limit: 12, sort: 'activity' } } })).items });
  const link = useAction(async (entryID: string) => { unwrap(await api.PUT('/api/v1/me/koreader/documents/{document}', { params: { path: { document: document.document } }, body: { entry_id: entryID } })); await client.invalidateQueries({ queryKey: privateKey(user.id, 'koreader') }); onClose(); });
  return <Modal title={t('Link to a title')} description={t('Choose the book this document is. Its position ({value}%) is applied right away.', { value: Math.round(document.percentage * 100) })} onClose={onClose} busy={link.pending}>
    <Field label={t('Search your books')}>{id => <input id={id} type="search" value={q} onChange={e => setQ(e.target.value)} autoFocus />}</Field>
    {results.isPending ? <Loading label={t('Searching')} /> : results.error ? <Failed error={results.error} retry={() => void results.refetch()} />
      : results.data.length === 0 ? <p className="subtle">{t('No matching books in your library.')}</p>
      : <ul className="pick-list">{results.data.map(e => <li key={e.id}><button className="pick-row" disabled={link.pending} onClick={() => void link.run(e.id)}><strong>{e.title}</strong><span className="subtle">{typeLabel(e.media_type)}{e.total ? ` · ${e.total} ${unitLabel(e.unit)}` : ''}</span></button></li>)}</ul>}
    <ErrorNote error={link.error} />
  </Modal>;
}
