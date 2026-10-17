// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// The overview narrows candidate search; displayed numbers still use exact replay.
export function finePlaceCandidates(points, overviewScores, view) {
  const span=view.east-view.west;
  const inside=point=>point.latitude>=view.south&&point.latitude<=view.north
    &&(span>=360||((point.longitude-view.west)%360+360)%360<=span);
  const eligible=points.flatMap((point,index)=>inside(point)?[index]:[]);
  const selected=new Set(eligible.filter(index=>(points[index].population??0)>=view.minimumLabelPopulation));
  const score=index=>Number.isFinite(overviewScores[index])?overviewScores[index]:-Infinity;
  eligible.sort((left,right)=>score(right)-score(left)
    ||(points[right].population??0)-(points[left].population??0));
  for(const index of eligible.slice(0,128))selected.add(index);
  return selected;
}
