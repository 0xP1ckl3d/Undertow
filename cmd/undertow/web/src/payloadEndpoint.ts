// Parse the host and optional port without relying on a browser-specific URL
// scheme. Saved profiles use host:port; listener binds may use [IPv6]:port.
export function endpointParts(endpoint:string):{host:string;port:string}{
  const value=endpoint.trim();
  if(value.startsWith('[')){
    const close=value.indexOf(']');
    if(close>0){
      const suffix=value.slice(close+1);
      return {host:value.slice(1,close),port:/^:\d+$/.test(suffix)?suffix.slice(1):''};
    }
  }
  const colon=value.lastIndexOf(':');
  if(colon>0&&value.indexOf(':')===colon&&/^\d+$/.test(value.slice(colon+1)))return {host:value.slice(0,colon),port:value.slice(colon+1)};
  return {host:value,port:''};
}
