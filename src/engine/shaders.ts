// Written by AI on 2026.10.17 by model gpt-6.1-sol.
import { COLD_GRADE_BY_FEELS_IN_SUN, HEAT_GRADE_BY_FEELS_IN_SHADE, EVENING_GRADE_BY_FEELS_AT_21, RAIN_FACTOR_BY_MM, WIND_FACTOR_BY_KMH, HUMIDITY_FACTOR_BY_DEW_POINT, SUN_FACTOR_BY_SUNNY_SHARE, type Curve, BANDS, EVENING_WORTH_IN_HOURS, NICE_SCORE } from './taste';

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
out vec4 result;
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
  if (ordinal >= u_cell_count) { result = vec4(0.0); return; }
  float shift = ${SHIFT_MINIMUM}.0 + float(pixel.y / u_cell_rows) * ${SHIFT_STEP}.0;
  float scoreSum = 0.0;
  float niceSum = 0.0;
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
    scoreSum += score;
    niceSum += step(${NICE_SCORE}.0, score);
  }
  result = vec4(scoreSum / float(u_day_count), niceSum / float(u_day_count), 0.0, 1.0);
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
uniform float u_weights[15];
out vec4 result;
float cellScore(ivec2 position, float shift, int layer) {
  position.x = (position.x % 360 + 360) % 360;
  position.y = clamp(position.y, 0, 179);
  int ordinal = int(texelFetch(u_cell_ordinals, position, 0).r);
  float level = clamp((shift - ${SHIFT_MINIMUM}.0) / ${SHIFT_STEP}.0, 0.0, ${SHIFT_LEVELS - 1}.0);
  int lower = min(int(level), ${SHIFT_LEVELS - 2});
  ivec2 address = ivec2(ordinal % ${CELL_TEXTURE_WIDTH}, ordinal / ${CELL_TEXTURE_WIDTH} + lower * u_cell_rows);
  float a = texelFetch(u_scores, ivec3(address, layer), 0).r;
  float b = texelFetch(u_scores, ivec3(address + ivec2(0, u_cell_rows), layer), 0).r;
  return mix(a, b, level - float(lower));
}
void main() {
  ivec2 pixel = ivec2(gl_FragCoord.xy);
  // A cell record describes the one-degree square centred at .5 degrees.
  vec2 cell = vec2(float(pixel.x) * 0.25 - 0.5, float(pixel.y) * 0.25 - 0.5);
  ivec2 base = ivec2(floor(cell));
  vec2 weight = fract(cell);
  float total = 0.0;
  for (int month = 0; month < 15; month++) {
    if (month >= u_month_count) break;
    int layer = u_month_layers[month];
    float offsetByte = float(texelFetch(u_offsets, ivec3(pixel, layer), 0).r);
    float shift = offsetByte == 255.0 ? 0.0 : (offsetByte - 128.0) / 10.0;
    float north = mix(cellScore(base, shift, month), cellScore(base + ivec2(1, 0), shift, month), weight.x);
    float south = mix(cellScore(base + ivec2(0, 1), shift, month), cellScore(base + ivec2(1, 1), shift, month), weight.x);
    total += mix(north, south, weight.y) * u_weights[month];
  }
  result = vec4(total, 0.0, 0.0, 1.0);
}`;

export const mapVertex = `#version 300 es
uniform mat4 u_matrix;
uniform float u_world_offset;
in vec2 a_position;
out vec2 v_mercator;
void main() {
  v_mercator = a_position;
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
uniform vec4 u_band_colours[6];
in vec2 v_mercator;
out vec4 fragColour;
${splineFunctions}
void main() {
  float latitude = degrees(atan(sinh(3.141592653589793 * (1.0 - 2.0 * v_mercator.y))));
  if (latitude < -60.0) discard;
  vec2 cell = vec2(v_mercator.x * 1440.0, (90.0 - latitude) * 4.0);
  ivec2 base = ivec2(floor(cell));
  vec4 wx = cubicBSplineWeights(fract(cell.x));
  vec4 wy = cubicBSplineWeights(fract(cell.y));
  float score = 0.0;
  for (int row = 0; row < 4; row++) {
    for (int column = 0; column < 4; column++) {
      ivec2 position = base + ivec2(column - 1, row - 1);
      position.x = (position.x % 1440 + 1440) % 1440;
      position.y = clamp(position.y, 0, 720);
      score += texelFetch(u_grid, position, 0).r * wx[column] * wy[row];
    }
  }
  float edgeWidth = max(fwidth(score) * 0.75, 0.02);
  vec4 colour = u_band_colours[0];
  float contour = 0.0;
  float minimums[6] = float[6](${BANDS.map(band => `${band.minimum}.0`).join(',')});
  for (int band = 1; band < 6; band++) {
    float distanceToEdge = score - minimums[band];
    colour = mix(colour, u_band_colours[band], smoothstep(-edgeWidth, edgeWidth, distanceToEdge));
    contour = max(contour, 1.0 - smoothstep(0.0, edgeWidth * 2.0, abs(distanceToEdge)));
  }
  colour.rgb *= 1.0 - 0.28 * contour;
  fragColour = colour;
}`;
