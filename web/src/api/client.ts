import createClient from 'openapi-fetch';
import type { paths, components } from './schema.gen';
import { t } from '../i18n';
import { commandKey } from './intent';
import {trackingProfile,profileGeneration} from '../profileScope';

export type Schema = components['schemas'];
export type Entry = Schema['Entry'];
export type Summary = Schema['EntrySummary'];
export type User = Schema['User'];
export type Session = Schema['SessionView'];
export class ApiError extends Error {
  constructor(public status: number, public code: string, message: string) { super(message); }
}
let csrf = '';
let generation = 0;
const requests = new WeakMap<Request, number>();
const profileRequests = new WeakMap<Request, number>();
export const csrfToken = () => csrf;
export function setSessionCredential(value: string) { if (csrf !== value) { csrf = value; generation++; } }
export const api = createClient<paths>({ baseUrl: '', credentials: 'same-origin', cache: 'no-store' });
api.use({
  async onRequest({ request }) {
    requests.set(request, generation);
    const path=new URL(request.url).pathname;
    if(path.startsWith('/api/v1/') && !['/api/v1/session','/api/v1/profiles','/api/v1/login','/api/v1/register'].includes(path) && trackingProfile()) {
      request.headers.set('X-Pastime-Profile-ID',trackingProfile());
      profileRequests.set(request,profileGeneration());
    }
    if (!['GET', 'HEAD'].includes(request.method) && csrf) request.headers.set('X-CSRF-Token', csrf);
    const intent = request.headers.get('Idempotency-Key');
    if (intent) request.headers.set('Idempotency-Key', await commandKey(intent, request.method, request.url, await request.clone().text()));
    return request;
  },
  onResponse({ request, response }) {
    if(profileRequests.has(request) && profileRequests.get(request)!==profileGeneration()) throw new DOMException('Profile changed','AbortError');
    if (response.status === 401 && !request.url.endsWith('/api/v1/login') && requests.get(request) === generation) {
      window.dispatchEvent(new Event('pastime-session-expired'));
    }
    if (response.status === 409 && requests.get(request) === generation) window.dispatchEvent(new Event('pastime-data-conflict'));
    return response;
  },
});
export function unwrap<T>(result: { data?: T; error?: unknown; response: Response }): T {
  if (!result.response.ok) {
    const p = result.error as Schema['Problem'] | undefined;
    throw new ApiError(result.response.status, p?.code ?? 'request_failed', p?.message ?? t('The request could not be completed.'));
  }
  return result.data as T;
}
export function errorMessage(error: unknown): string {
  // Server messages are English; known ones have translations in the catalogs.
  if (error instanceof ApiError) return t(error.message);
  return t('The response was not received. Retry the same action to check its outcome.');
}
export const keyHeader = (key: string) => ({ 'Idempotency-Key': key });

// Multipart imports use fetch directly; preserve the same profile boundary.
export async function profileFetch(path:string,init:RequestInit):Promise<Response>{
 const selected=trackingProfile(),revision=profileGeneration();
 const headers=new Headers(init.headers);if(selected)headers.set('X-Pastime-Profile-ID',selected);
 const response=await fetch(path,{...init,headers,credentials:'same-origin'});
 if(revision!==profileGeneration())throw new DOMException('Profile changed','AbortError');
 return response;
}
export function profileDownload(path:string):string{
 const url=new URL(path,window.location.origin);if(trackingProfile())url.searchParams.set('profile_id',trackingProfile());
 return url.pathname+url.search;
}
