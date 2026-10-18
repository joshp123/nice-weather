// Written by AI on 2026.10.17 by model gpt-6.1-sol.
import { COLD_GRADE_BY_FEELS_IN_SUN, HEAT_GRADE_BY_FEELS_IN_SHADE, EVENING_GRADE_BY_FEELS_AT_21, RAIN_FACTOR_BY_MM, WIND_FACTOR_BY_KMH, HUMIDITY_FACTOR_BY_DEW_POINT, SUN_FACTOR_BY_SUNNY_SHARE, type Curve, BANDS, EVENING_WORTH_IN_HOURS } from './taste';

export const SHIFT_MINIMUM = -15;
export const SHIFT_STEP = 3;
export const SHIFT_LEVELS = 11;
export const CELL_TEXTURE_WIDTH = 256;

function curveFunction(name: string, curve: Curve): string {
  const number = (value: number) => Number.isInteger(value) ? `${value}.0` : String(value);
  return `float ${name}(float x) {\n` +
    `if (x <= ${number(curve[0][0])}) return ${number(curve[0][1])};\n` +
    curve.slice(1).map(([x, y], index) => {
      const [previousX, previousY] = curve[index];
      return `if (x <= ${number(x)}) return ${number(previousY)} + (x - ${number(previousX)}) * ${number((y - previousY) / (x - previousX))};`;
    }).join('\n') + `\nreturn ${number(curve[curve.length - 1][1])}; }`;
}

export const fullscreenVertex = `#version 300 es
in vec2 a_position;
void main() { gl_Position = vec4(a_position * 2.0 - 1.0, 0.0, 1.0); }
`;

export const scoringFragment = `#version 300 es
precision highp float;
precision highp int;
uniform highp usampler2D u_records;
uniform int u_cell_rows;
uniform int u_cell_count;
uniform int u_samples;
uniform int u_first_day;
uniform int u_day_count;
uniform vec4 u_thermal_taste;
uniform vec2 u_weather_taste;
layout(location=0) out vec4 days0;
layout(location=1) out vec4 days1;
layout(location=2) out vec4 days2;
layout(location=3) out vec4 days3;
${curveFunction('cold', COLD_GRADE_BY_FEELS_IN_SUN)}
${curveFunction('heat', HEAT_GRADE_BY_FEELS_IN_SHADE)}
${curveFunction('eveningGrade', EVENING_GRADE_BY_FEELS_AT_21)}
${curveFunction('rain', RAIN_FACTOR_BY_MM)}
${curveFunction('wind', WIND_FACTOR_BY_KMH)}
${curveFunction('humidity', HUMIDITY_FACTOR_BY_DEW_POINT)}
${curveFunction('sunFactor', SUN_FACTOR_BY_SUNNY_SHARE)}
void main() {
  ivec2 pixel = ivec2(gl_FragCoord.xy);
  int cellRow = pixel.y % u_cell_rows;
  int ordinal = cellRow * ${CELL_TEXTURE_WIDTH} + pixel.x;
  days0=vec4(0.0);days1=vec4(0.0);days2=vec4(0.0);days3=vec4(0.0);
  if (ordinal >= u_cell_count) return;
  vec4 packedDays[4]=vec4[4](vec4(0.0),vec4(0.0),vec4(0.0),vec4(0.0));
  float shift = ${SHIFT_MINIMUM}.0 + float(pixel.y / u_cell_rows) * ${SHIFT_STEP}.0;
  for (int day = 0; day < 15; day++) {
    if (day >= u_day_count) break;
    ivec2 address = ivec2(pixel.x * 4, cellRow * u_samples + u_first_day + day);
    vec4 a = vec4(texelFetch(u_records, address, 0));
    vec4 b = vec4(texelFetch(u_records, address + ivec2(1, 0), 0));
    vec4 c = vec4(texelFetch(u_records, address + ivec2(2, 0), 0));
    vec4 d = vec4(texelFetch(u_records, address + ivec2(3, 0), 0));
    float sun[6] = float[6](a.x, a.y, a.z, a.w, b.x, b.y);
    float shade[4] = float[4](b.z, b.w, c.x, c.y);
    float daylight = max(d.z, 1.0);
    float warmth = 0.0;
    for (int hour = 0; hour < 11; hour++) {
      if (float(hour) >= daylight) break;
      int sunIndex = min(hour / 2, 4);
      float feltSun = mix(sun[sunIndex], sun[sunIndex + 1], float(hour - sunIndex * 2) / 2.0) / 3.0 - 30.0 + shift;
      float shadeHour = clamp(float(hour - 2) / 3.0, 0.0, 3.0);
      int shadeIndex = min(int(shadeHour), 2);
      float feltShade = mix(shade[shadeIndex], shade[shadeIndex + 1], shadeHour - float(shadeIndex)) / 3.0 - 30.0 + shift;
      warmth += min(cold(feltSun - u_thermal_taste.x), heat(feltShade + u_thermal_taste.y));
    }
    float at21 = shade[3] / 3.0 - 30.0 + shift;
    warmth = (warmth + ${EVENING_WORTH_IN_HOURS}.0 * min(eveningGrade(at21 - u_thermal_taste.z), heat(at21 + u_thermal_taste.y))) / (daylight + ${EVENING_WORTH_IN_HOURS}.0);
    float score = warmth * rain(pow(c.w / 50.0, 2.0) * u_weather_taste.y)
      * wind(d.x / 2.0 - u_weather_taste.x)
      * humidity(c.z / 3.0 - 30.0 - u_thermal_taste.w)
      * sunFactor(d.y / daylight);
    packedDays[day/4][day%4]=score;
  }

  days0=packedDays[0];days1=packedDays[1];days2=packedDays[2];days3=packedDays[3];
}`;

export const resolveFragment = `#version 300 es
precision highp float;
precision highp int;
uniform highp sampler2DArray u_scores;
uniform highp usampler2DArray u_offsets;
uniform highp usampler2D u_cell_ordinals;
uniform int u_cell_rows;
uniform int u_month_count;
uniform int u_month_layers[15];
uniform int u_day_counts[15];
uniform float u_weights[15];
uniform bool u_single_day;
out vec4 result;
void main() {
  ivec2 pixel = ivec2(gl_FragCoord.xy);
  ivec2 cell = clamp(pixel / 4, ivec2(0), ivec2(359,179));
  int ordinal = int(texelFetch(u_cell_ordinals, cell, 0).r);
  float shares[7] = float[7](1.0,0.0,0.0,0.0,0.0,0.0,0.0);
  float thresholds[7] = float[7](0.0,25.0,40.0,55.0,70.0,85.0,100.0);
  float singleScore = 0.0;
  for (int month = 0; month < 15; month++) {
    if (month >= u_month_count) break;
    float offsetByte = float(texelFetch(u_offsets, ivec3(pixel, u_month_layers[month]), 0).r);
    if (offsetByte == 255.0) { result = vec4(255.0); return; }
    float level = clamp(((offsetByte - 128.0) / 10.0 - ${SHIFT_MINIMUM}.0) / ${SHIFT_STEP}.0, 0.0, ${SHIFT_LEVELS - 1}.0);
    int lower = min(int(level), ${SHIFT_LEVELS - 2});
    ivec2 address = ivec2(ordinal % ${CELL_TEXTURE_WIDTH}, ordinal / ${CELL_TEXTURE_WIDTH} + lower * u_cell_rows);
    for(int pack=0;pack<4;pack++) {
      if(pack*4>=u_day_counts[month]) break;
      vec4 a=texelFetch(u_scores,ivec3(address,month*4+pack),0);
      vec4 b=texelFetch(u_scores,ivec3(address+ivec2(0,u_cell_rows),month*4+pack),0);
      vec4 days=mix(a,b,level-float(lower));
      for(int day=0;day<4;day++) {
        if(pack*4+day>=u_day_counts[month]) break;
        float score=floor(days[day]*10000.0+0.5)/10000.0; singleScore=score;
        for(int threshold=1;threshold<7;threshold++) shares[threshold]+=step(thresholds[threshold],score)*u_weights[month]/float(u_day_counts[month]);
      }
    }
  }
  float typical=0.0;
  for(int threshold=5;threshold>=0;threshold--) {
    if(shares[threshold]<0.5-0.000001) continue;
    typical=threshold==5 ? 85.0+(shares[5]-0.5)*30.0 : mix(thresholds[threshold],thresholds[threshold+1],(shares[threshold]-0.5)/(shares[threshold]-shares[threshold+1]));
    typical=max(typical,thresholds[threshold]);
    break;
  }
  result=vec4(u_single_day ? singleScore : floor(typical*10000.0+0.5)/10000.0,shares[3],shares[5],max(0.0,1.0-shares[1]));
}`;

export const mapVertex = `#version 300 es
uniform mat4 u_matrix;
uniform float u_world_offset;
in vec2 a_position;
out vec2 v_mercator;
void main() {
  v_mercator = vec2(a_position.x + u_world_offset, a_position.y);
  gl_Position = u_matrix * vec4(a_position.x + u_world_offset, a_position.y, 0.0, 1.0);
}`;

export const splineFunctions = `
vec4 cubicBSplineWeights(float t) {
  float u = 1.0 - t;
  return vec4(u*u*u, 3.0*t*t*t-6.0*t*t+4.0, -3.0*t*t*t+3.0*t*t+3.0*t+1.0, t*t*t) / 6.0;
}`;

export const mapFragment = `#version 300 es
precision highp float;
uniform highp sampler2D u_grid;
uniform float u_grid_scale;
uniform bool u_round_scores;
uniform vec4 u_band_colours[6];
uniform vec4 u_land_colour;
uniform float u_edge_pixels;
uniform float u_overview_smoothing;
in vec2 v_mercator;
out vec4 fragColour;
${splineFunctions}
vec2 sampleScoreSpline(vec2 cell, float spacing) {
  vec2 coarseCell = (cell - vec2(floor(spacing / 2.0))) / spacing;
  ivec2 base = ivec2(floor(coarseCell));
  vec4 wx = cubicBSplineWeights(fract(coarseCell.x));
  vec4 wy = cubicBSplineWeights(fract(coarseCell.y));
  vec2 weighted = vec2(0.0);
  for (int row = 0; row < 4; row++) {
    for (int column = 0; column < 4; column++) {
      ivec2 position = ivec2(vec2(base + ivec2(column - 1, row - 1)) * spacing + vec2(floor(spacing / 2.0)));
      position.x = (position.x % 1440 + 1440) % 1440;
      position.y = clamp(position.y, 0, 720);
      float value = texelFetch(u_grid, position, 0).r * u_grid_scale;
      if (value > 100.5) continue;
      if (u_round_scores) value = floor(value);
      float weight = wx[column] * wy[row];
      weighted += vec2(value, 1.0) * weight;
    }
  }
  return weighted;
}
void main() {
  if (v_mercator.x < 0.0 || v_mercator.x > 1.0) discard;
  float latitude = degrees(atan(sinh(3.141592653589793 * (1.0 - 2.0 * v_mercator.y))));
  if (latitude < -60.0) discard;
  vec2 cell = vec2(v_mercator.x * 1440.0, (90.0 - latitude) * 4.0);
  vec2 local = sampleScoreSpline(cell, 1.0);
  if (local.y < 0.00001) discard;
  float score = local.x / local.y;
  if (u_overview_smoothing > 0.0) {
    vec2 overview = sampleScoreSpline(cell, 4.0);
    if (overview.y > 0.00001) score = mix(score, overview.x / overview.y, u_overview_smoothing);
  }
  float landWeight = local.y;
  float edgeWidth = max(fwidth(score) * u_edge_pixels, 0.02);
  vec4 colour = u_band_colours[0];
  float contour = 0.0;
  float minimums[6] = float[6](${BANDS.map(band => `${band.minimum}.0`).join(',')});
  for (int band = 1; band < 6; band++) {
    float distanceToEdge = score - minimums[band];
    colour = mix(colour, u_band_colours[band], smoothstep(-edgeWidth, edgeWidth, distanceToEdge));
    contour = max(contour, 1.0 - smoothstep(0.0, edgeWidth * 2.0, abs(distanceToEdge)));
  }
  colour.rgb *= 1.0 - 0.14 * contour;
  float landCoverage = smoothstep(0.0, 0.02, landWeight);
  vec3 landComposite = colour.rgb + u_land_colour.rgb * (1.0 - colour.a);
  fragColour = vec4(landComposite * landCoverage, landCoverage);
}`;

/** Exact loaded 30 arc-second cells replace the overview at their real geographic area. */
export const finePixelVertex = `#version 300 es
uniform mat4 u_matrix;
uniform float u_world_offset;
in vec2 a_position;
in float a_score;
out float v_score;
out float v_world_x;
void main() {
  v_score = a_score;
  v_world_x = a_position.x + u_world_offset;
  gl_Position = u_matrix * vec4(a_position.x + u_world_offset, a_position.y, 0.0, 1.0);
}`;

export const finePixelFragment = `#version 300 es
precision highp float;
uniform vec4 u_band_colours[6];
uniform vec4 u_land_colour;
in float v_score;
in float v_world_x;
out vec4 fragColour;
void main() {
  if (v_world_x < 0.0 || v_world_x > 1.0) discard;
  vec4 colour = u_band_colours[0];
  float minimums[6] = float[6](${BANDS.map(band => `${band.minimum}.0`).join(',')});
  for (int band = 1; band < 6; band++) if (v_score >= minimums[band]) colour = u_band_colours[band];
  fragColour = vec4(colour.rgb + u_land_colour.rgb * (1.0 - colour.a), 1.0);
}`;
