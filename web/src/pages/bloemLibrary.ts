import {useQuery, useQueryClient} from '@tanstack/react-query';
import {useEffect, useMemo, useState} from 'react';
import {api, ApiError, unwrap, type Schema} from '../api/client';
import {privateKey, useUser} from '../state';
import {useAction} from '../ui';
import {t} from '../i18n';
import {readLibraryPage,libraryArtworkPending} from '../libraryPageRequest';
export function useConnectedLibraryPage(body:Schema['BloemLibraryQuery'],refresh=0,enabled=true){
 const user=useUser();
 const artworkDeadline=useMemo(()=>Date.now()+45000,[JSON.stringify(body),refresh]);
 return useQuery<Schema['BloemLibraryResult']>({queryKey:privateKey(user.id,'bloem-library-page',body,refresh),enabled,retry:false,refetchInterval:q=>enabled&&body.view==='catalog'&&!q.state.error&&libraryArtworkPending(q.state.data,artworkDeadline)?5000:false,
 queryFn:async({signal})=>{
  let out:Schema['BloemLibraryResult'];
  try{out=await readLibraryPage(
   async signal=>unwrap(await api.POST('/api/v1/bloem/library/queries',{signal,body})),
   async(requestID,signal)=>unwrap(await api.GET('/api/v1/bloem/library/queries/{requestID}',{signal,params:{path:{requestID}}})),signal);
  }catch(error){if(error instanceof DOMException&&error.name==='TimeoutError')throw new ApiError(504,'source_timeout',t('Silo is taking too long to respond. Refresh to try again.'));throw error;}
  if(out.status==='denied')throw new ApiError(403,'bloem_access_denied',t('Silo cannot share this page. Check your profile access in Silo.'));
  if(out.status==='invalid')throw new ApiError(409,'bloem_page_changed',t('This Silo page changed. Refresh to start again.'));
  return out;
 }});
}
export function useLibraryLease(){const [now,setNow]=useState(Date.now);useEffect(()=>{const timer=window.setInterval(()=>setNow(Date.now()),1000);return()=>window.clearInterval(timer)},[]);return now}
export function useBloemCollectionMutation(instance:string,onApplied:()=>void){
 const user=useUser();const client=useQueryClient();const [request,setRequest]=useState<string>();
 const action=useAction(async(intent:Omit<Schema['BloemLibraryMutationIntent'],'instance_id'|'event_id'>,key:string)=>{
  const out=unwrap(await api.POST('/api/v1/bloem/library/mutations',{body:{...intent,instance_id:instance,event_id:key}}));setRequest(out.request_id);return out;
 });
 const result=useQuery<Schema['BloemLibraryMutationResult']>({queryKey:privateKey(user.id,'bloem-collection-change',request),enabled:!!request,refetchInterval:q=>q.state.data?.status==='pending'?1000:false,
 queryFn:async({signal})=>unwrap(await api.GET('/api/v1/bloem/library/mutations/{requestID}',{signal,params:{path:{requestID:request!}}}))});
 const status=result.data?.status;
 useEffect(()=>{if(status==='applied'||status==='no_change'||status==='removed'){void client.invalidateQueries({queryKey:privateKey(user.id,'bloem-library-page')});onApplied();setRequest(undefined)}},[status,request]);
 return {...action,result,busy:action.pending||!!request&&(!status||status==='pending'),blocked:status==='conflict'||status==='denied'||status==='invalid',clear:()=>{setRequest(undefined);action.reset()}};
}
