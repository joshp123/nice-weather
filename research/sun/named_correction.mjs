// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Fit the satellite month to the exact representative weather used by the card.
const clamp=value=>Math.max(0,Math.min(1,value));
export const sunshineCoordinateKey=(latitude,longitude)=>`${Math.round(latitude*1e5)},${Math.round(longitude*1e5)}`;
export function fitNamedSunshine(packet,months) {
  const replay=packet.replay;
  return months.map((target,monthIndex)=> {
    if(target===null)return null;
    const indices=replay.selectedIndices.filter(index=>Number(replay.dates[index].slice(5,7))===monthIndex+1);
    const shares=indices.map(index=>replay.sunnyHours[index]/Math.max(1,replay.daylightHours[index]));
    let lower=-1,upper=1;
    for(let iteration=0;iteration<48;iteration++) {
      const offset=(lower+upper)/2,mean=shares.reduce((sum,value)=>sum+clamp(value+offset),0)/shares.length;
      if(mean<target.sunnyShare)lower=offset;else upper=offset;
    }
    const dniScale=Array.from({length:12},(_,hour)=> {
      let total=0;
      for(const index of indices) {
        const at=index*12+hour,air=replay.airC[at],dew=Math.min(air,replay.dewC[at]);
        const vapour=6.105*Math.exp(17.27*dew/(237.7+dew));
        const windMetresPerSecond=(air+.33*vapour-4-replay.shadeC[at])/.7;
        total+=Math.max(0,(replay.sunC[at]-replay.shadeC[at])*(windMetresPerSecond+10)/.105);
      }
      const nativeMean=total/indices.length;
      return nativeMean>0?target.hourlyDni[hour]/nativeMean:1;
    });
    return {sunnyShareOffset:(lower+upper)/2,dniScale,sunnyShare:target.sunnyShare};
  });
}
