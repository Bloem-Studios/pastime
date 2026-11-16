import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api, unwrap, type Schema } from '../api/client';
import { privateKey, useUser } from '../state';
import { ErrorNote, Failed, Field, Loading, submit, useAction } from '../ui';
import { t } from '../i18n';

export function BloemHandoffSettings() {
  const user = useUser();
  const [selected, setSelected] = useState('');
  const instances = useQuery({ queryKey: privateKey(user.id, 'bloem-handoff-instances'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/admin/v1/bloem/instances', { signal })).items });
  const instance = instances.data?.find(item => item.id === selected) ?? instances.data?.[0];
  return <details className="detail-section"><summary>{t('Silo playback links')}</summary>
    {instances.isPending ? <Loading /> : instances.error ? <Failed error={instances.error} retry={() => void instances.refetch()} /> : !instances.data.length ? <p>{t('No Silo instance is connected.')}</p> : <>
      <Field label={t('Connected server')}>{id => <select id={id} value={instance?.id ?? ''} onChange={e => setSelected(e.target.value)}>{instances.data.map(item => <option key={item.id} value={item.id}>{item.url}</option>)}</select>}</Field>
      {instance && <HandoffForm key={instance.id} instanceID={instance.id} />}
    </>}
  </details>;
}
function HandoffForm({ instanceID }: { instanceID: string }) {
  const user = useUser(); const client = useQueryClient();
  const queryKey = privateKey(user.id, 'bloem-handoff', instanceID);
  const query = useQuery({ queryKey, queryFn: async ({ signal }) => unwrap(await api.GET('/api/admin/v1/bloem/instances/{instanceID}/handoff', { signal, params: { path: { instanceID } } })) });
  const [settings, setSettings] = useState<Schema['BloemHandoffSettings'] | null>(null);
  useEffect(() => { if (query.data) setSettings(query.data); }, [query.data]);
  const save = useAction(async (_: void) => {
    if (!settings) return;
    const result = unwrap(await api.PUT('/api/admin/v1/bloem/instances/{instanceID}/handoff', { params: { path: { instanceID } }, body: settings }));
    client.setQueryData(queryKey, result);
    await client.invalidateQueries({ queryKey: privateKey(user.id, 'calendar') });
  });
  if (query.error) return <Failed error={query.error} retry={() => void query.refetch()} />;
  if (!settings) return <Loading />;
  return <form onSubmit={submit(() => void save.run())}><fieldset disabled={save.pending}>
    <label><input type="checkbox" checked={settings.enabled} onChange={e => setSettings({ ...settings, enabled: e.target.checked })} /> {t('Enable playback links')}</label>
    <Field label={t('Playback button label')}>{id => <input id={id} required maxLength={100} value={settings.label} onChange={e => setSettings({ ...settings, label: e.target.value })} />}</Field>
    <Field label={t('Silo web address')} hint={t('Use an HTTP or HTTPS origin without credentials, a path, or a query.')}>{id => <input id={id} type="url" required maxLength={500} value={settings.base_url} onChange={e => setSettings({ ...settings, base_url: e.target.value })} />}</Field>
    {query.data && <Field label={t('Example playback address')}>{id => <output id={id} className="identifier">{query.data.base_url.replace(/\/$/, '')}/item/example-content-id</output>}</Field>}
    <button className="button" type="submit">{save.pending ? t('Saving…') : t('Save')}</button>
    <ErrorNote error={save.error} />
  </fieldset></form>;
}
