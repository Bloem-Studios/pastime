type Result = {request_id: string; status: string};
function pause(ms:number,signal:AbortSignal):Promise<void>{
 signal.throwIfAborted();
 return new Promise((resolve,reject)=>{
  const aborted=()=>{clearTimeout(timer);reject(signal.reason)};
  const timer=setTimeout(()=>{signal.removeEventListener('abort',aborted);resolve()},ms);
  signal.addEventListener('abort',aborted,{once:true});
 });
}
export async function readLibraryPage<T extends Result>(submit:(signal:AbortSignal)=>Promise<T>,read:(id:string,signal:AbortSignal)=>Promise<T>,signal:AbortSignal,options={pollMs:2000,timeoutMs:90000}):Promise<T>{
 const bounded=AbortSignal.any([signal,AbortSignal.timeout(options.timeoutMs)]);
 bounded.throwIfAborted();
 const request=await submit(bounded);
 if(request.status!=='pending'&&request.status!=='ready')return request;
 while(true){
  bounded.throwIfAborted();
  const result=await read(request.request_id,bounded);
  if(result.status!=='pending')return result;
  await pause(options.pollMs,bounded);
 }
}

type ArtworkResult={status:string;page?:{expires_at:string;items:Array<{kind:string;catalog_id?:string;external_ids?:Record<string,string>|null}>}};
export function libraryArtworkPending(result:ArtworkResult|undefined,deadline:number,now=Date.now()):boolean {
 const page=result?.page;
 if(result?.status!=='ready'||!page||now>=deadline||Date.parse(page.expires_at)<=now)return false;
 return page.items.some(item=>{
  if(item.catalog_id)return false;
  if(item.kind==='ebook')return !!item.external_ids?.isbn;
  if(item.kind==='audiobook')return !!item.external_ids?.asin;
  return (item.kind==='movie'||item.kind==='series')&&['tmdb','tvdb','imdb'].some(key=>!!item.external_ids?.[key]);
 });
}
