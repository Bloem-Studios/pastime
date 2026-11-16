import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api, ApiError, unwrap, type Schema } from '../api/client';
import { useTrackingProfile } from '../profileContext';
import { privateKey, useUnsaved, useUser } from '../state';
import { ErrorNote, Failed, Field, Modal, submit, useAction, useConfirm } from '../ui';
import { PasswordInput } from './formControls';
import { SettingsSection } from './settings';
import { relative } from '../format';
import { t } from '../i18n';

type Profile = Schema['TrackingProfile'];
type Login = Schema['ProfileLogin'];

/**
 * The household's profiles, each with its own sign-in when the primary user
 * turns one on. Profiles come from Bloem; Pastime doesn't add them. A saved
 * password is never shown.
 */
export function ProfileLogins() {
  const user = useUser(); const client = useQueryClient(); const confirm = useConfirm(); const { profiles } = useTrackingProfile();
  const key = privateKey(user.id, 'profile-logins');
  const logins = useQuery({ queryKey: key, queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/profile-logins', { signal })).items });
  const [editing, setEditing] = useState<{ profile: Profile; login?: Login } | null>(null);
  const remove = useAction(async (profile: Profile) => {
    unwrap(await api.DELETE('/api/v1/profiles/{profileID}/login', { params: { path: { profileID: profile.id } } }));
    await client.invalidateQueries({ queryKey: key });
  });
  const loginOf = (profile: Profile) => logins.data?.find(l => l.profile_id === profile.id);
  return <SettingsSection title={t('Profiles')} description={t('Give a profile its own sign-in, so that person can open Pastime themselves. They see only their own profile, never the others or household settings.')}>
    <ErrorNote error={remove.error} />
    {logins.isPending ? <p className="subtle">{t('Loading')}</p> : logins.error ? <Failed error={logins.error} retry={() => void logins.refetch()} /> : <>
      <ul className="record-list">{profiles.map(profile => {
        const login = loginOf(profile);
        return <li className="record-row" key={profile.id}>
          <div><h3>{profile.name}</h3>
            {profile.default ? <p>{t('Your own profile. You sign in to it with your account.')}</p>
              : login ? <p><span>{t('Signs in as {login}', { login: login.login_name })}</span> · <span>{login.last_login_at ? t('Last used {time}', { time: relative(login.last_login_at) }) : t('Not used yet')}</span></p>
              : <p>{t('No sign-in of its own.')}</p>}</div>
          {!profile.default && (login
            ? <div className="actions profile-login-actions"><button className="button small" onClick={() => setEditing({ profile, login })}>{t('Change sign-in')}</button>
              <button className="button small ghost" disabled={remove.pending} onClick={async () => { if (await confirm({ title: t('Turn off sign-in for {name}?', { name: profile.name }), description: t('{name} is signed out of every browser and can’t sign in until you turn it on again. Their profile stays as it is.', { name: profile.name }), label: t('Turn off sign-in'), danger: true })) void remove.run(profile); }}>{t('Turn off sign-in')}</button></div>
            : <button className="button small" onClick={() => setEditing({ profile })}>{t('Turn on sign-in')}</button>)}
        </li>;
      })}</ul>
    </>}
    {editing && <SignInDialog profile={editing.profile} login={editing.login} onClose={() => setEditing(null)} onSaved={() => client.invalidateQueries({ queryKey: key })} />}
  </SettingsSection>;
}

function SignInDialog({ profile, login, onClose, onSaved }: { profile: Profile; login?: Login; onClose: () => void; onSaved: () => Promise<void> }) {
  const [name, setName] = useState(login?.login_name ?? ''); const [password, setPassword] = useState(''); const [dirty, setDirty] = useState(false); const unsaved = useUnsaved();
  const save = useAction(async (body: Schema['ProfileLoginInput']) => {
    const response = await api.PUT('/api/v1/profiles/{profileID}/login', { params: { path: { profileID: profile.id } }, body });
    // The server won't say whose name it is; this one form only needs to say it's taken.
    if (response.response.status === 409) throw new ApiError(409, 'conflict', t('That sign-in name is already used on this server. Choose another.'));
    unwrap(response); setPassword(''); setDirty(false); unsaved.markSaved(); await onSaved(); onClose();
  });
  const label = login ? t('Save sign-in') : t('Turn on sign-in');
  return <Modal title={login ? t('Change sign-in for {name}', { name: profile.name }) : t('Turn on sign-in for {name}', { name: profile.name })}
    description={t('{name} signs in on the usual Pastime sign-in page and sees only this profile.', { name: profile.name })} onClose={onClose} busy={save.pending} dirty={dirty}>
    <form onSubmit={submit(() => void save.run(password ? { login_name: name, password } : { login_name: name }))} onChange={() => { setDirty(true); unsaved.markDirty(); save.reset(); }}>
      <fieldset disabled={save.pending}>
        <Field label={t('Sign-in name')} hint={t('A username or an email address. It’s only a name: Pastime never sends it email.')}>{(id, hint) => <input id={id} aria-describedby={hint} required autoComplete="off" autoCapitalize="none" spellCheck={false} minLength={3} maxLength={254} value={name} onChange={e => setName(e.target.value)} />}</Field>
        <Field label={login ? t('New password') : t('Password')} hint={login ? t('Leave empty to keep the current password. A new one signs {name} out of every browser.', { name: profile.name }) : t('12–128 characters. Tell {name} the password yourself; Pastime doesn’t send it.', { name: profile.name })}>{(id, hint) => <PasswordInput id={id} aria-describedby={hint} type="password" required={!login} autoComplete="new-password" minLength={12} maxLength={128} value={password} onChange={e => setPassword(e.target.value)} />}</Field>
      </fieldset>
      <ErrorNote error={save.error} />
      <div className="dialog-actions"><button className="button primary" disabled={save.pending} type="submit">{save.pending ? t('Saving…') : label}</button></div>
    </form>
  </Modal>;
}
