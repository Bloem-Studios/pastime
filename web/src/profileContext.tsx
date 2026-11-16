import {createContext, useCallback, useContext, useLayoutEffect, useState, type ReactNode} from 'react';
import {useQuery, useQueryClient} from '@tanstack/react-query';
import {api, unwrap, type Schema} from './api/client';
import {useUser} from './state';
import {setTrackingProfile, trackingProfile} from './profileScope';
import {Failed, Loading} from './ui';
import {t} from './i18n';

type Profile = Schema['TrackingProfile'];
type Context = {profiles:Profile[]; active:Profile; switching:boolean; select:(id:string)=>Promise<void>};
const ProfileContext=createContext<Context|null>(null);
export function useTrackingProfile(){
 const value=useContext(ProfileContext);
 if(!value)throw new Error('Tracking profile context unavailable');
 return value;
}
function saved(accountID:string){try{return localStorage.getItem(`pastime-profile:${accountID}`)??'';}catch{return '';}}
export function TrackingProfileProvider({children}:{children:ReactNode}){
 const user=useUser();const client=useQueryClient();
 const [selected,setSelected]=useState(()=>saved(user.id));
 const [ready,setReady]=useState('');const [switching,setSwitching]=useState(false);
 const query=useQuery({queryKey:['profiles',user.id],refetchInterval:60000,queryFn:async({signal})=>unwrap(await api.GET('/api/v1/profiles',{signal})).items});
 const profiles=query.data??[];
 const active=profiles.find(p=>p.id===selected)??profiles.find(p=>p.default)??profiles[0];
 useLayoutEffect(()=>{
  if(active){setTrackingProfile(active.id);setReady(active.id);}
 },[active?.id]);
 const select=useCallback(async(id:string)=>{
  if(!profiles.some(p=>p.id===id)||id===active?.id||switching)return;
  setSwitching(true);setReady('');
  await client.cancelQueries({queryKey:['private',user.id]});
  setTrackingProfile(id);
  client.removeQueries({queryKey:['private',user.id]});
  try{localStorage.setItem(`pastime-profile:${user.id}`,id);}catch{/* convenience only */}
  setSelected(id);setReady(id);setSwitching(false);
 },[profiles,active?.id,switching,client,user.id]);
 if(query.error)return <main id="main"><Failed error={query.error} retry={()=>void query.refetch()}/></main>;
 if(!active||ready!==active.id||trackingProfile()!==active.id)return <main id="main"><Loading label={t('Profile')}/></main>;
 return <ProfileContext.Provider value={{profiles,active,switching,select}}>{children}</ProfileContext.Provider>;
}
