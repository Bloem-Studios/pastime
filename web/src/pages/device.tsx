import { useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { Check, MonitorSmartphone, X } from 'lucide-react';
import { api, unwrap, type Schema } from '../api/client';
import { ErrorNote, Field, PageTitle, submit, useAction } from '../ui';
import { t } from '../i18n';

const scopeNames: Record<string, string> = {
  'profile:read': t('Read your profile'), 'library:read': t('Read your library'), 'library:write': t('Update your library'),
  'lists:read': t('Read your lists'), 'lists:write': t('Update your lists'),
};

/** Approves an app on a TV or phone that shows a sign-in code. */
export function DevicePage() {
  const [params] = useSearchParams();
  const [code, setCode] = useState(params.get('code') ?? '');
  const [request, setRequest] = useState<Schema['DeviceRequest'] | null>(null);
  const [done, setDone] = useState<'approved' | 'denied' | null>(null);
  const lookup = useAction(async (value: string) => {
    setRequest(unwrap(await api.GET('/api/v1/device', { params: { query: { code: value } } })));
  });
  const decide = useAction(async (approve: boolean) => {
    unwrap(await api.POST('/api/v1/device/decision', { body: { code, approve } }));
    setDone(approve ? 'approved' : 'denied');
  });
  return <section className="bounded-form">
    <PageTitle title={t('Sign in an app')} />
    {done ? <p className="notice" role="status"><Check size={16} aria-hidden /> {done === 'approved' ? t('Done. The app signs in within a few seconds; you can remove it later under Account → API tokens.') : t('Denied. The app was not signed in.')}</p>
      : request ? <div className="panel">
        <p><MonitorSmartphone size={18} aria-hidden /> <strong>{request.client_name}</strong> {t('wants to use your account and will be able to:')}</p>
        <ul>{request.scopes.map(s => <li key={s}>{scopeNames[s] ?? s}</li>)}</ul>
        <p className="subtle">{t('Only approve a code you see on your own device right now.')}</p>
        <ErrorNote error={decide.error} />
        <div className="actions"><button className="button" disabled={decide.pending} onClick={() => void decide.run(false)}><X aria-hidden />{t('Deny')}</button><button className="button primary" disabled={decide.pending} onClick={() => void decide.run(true)}><Check aria-hidden />{t('Approve')}</button></div>
      </div>
      : <form onSubmit={submit(() => void lookup.run(code))}>
        <Field label={t('Code shown on your TV or phone')} hint={t('Eight letters, such as BCDF-GHJK.')}>{(id, hint) => <input id={id} aria-describedby={hint} autoComplete="one-time-code" autoCapitalize="characters" spellCheck={false} maxLength={12} required value={code} onChange={e => setCode(e.target.value.toUpperCase())} />}</Field>
        <ErrorNote error={lookup.error} />
        <div className="actions"><button className="button primary" type="submit" disabled={lookup.pending}>{t('Continue')}</button></div>
      </form>}
  </section>;
}
