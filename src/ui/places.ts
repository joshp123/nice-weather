// Written by AI on 2026.10.17 by model Claude Fable 5.1.
// Well-known places around the world, for the one-line answer at the top ("Nicest in November: ...").
// Names are what a traveller would say, so "Tenerife" rather than "Los Cristianos".
import type { Point } from '../engine';

export type NamedPlace = Point & Readonly<{ name: string }>;

const place = (name: string, latitude: number, longitude: number): NamedPlace => ({ name, latitude, longitude });

export const HEADLINE_PLACES: readonly NamedPlace[] = [
  place('Lisbon', 38.72, -9.14), place('Porto', 41.15, -8.61), place('the Algarve', 37.02, -7.93), place('Madrid', 40.42, -3.7),
  place('Málaga', 36.72, -4.42), place('Seville', 37.39, -5.99), place('Valencia', 39.47, -0.38), place('Alicante', 38.35, -0.48),
  place('Barcelona', 41.39, 2.17), place('Mallorca', 39.57, 2.65), place('Tenerife', 28.05, -16.72), place('Gran Canaria', 28.12, -15.43),
  place('Madeira', 32.65, -16.91), place('Marrakech', 31.63, -7.99), place('Agadir', 30.42, -9.6), place('Tunis', 36.8, 10.18),
  place('Cairo', 30.04, 31.24), place('Nice', 43.7, 7.27), place('Rome', 41.9, 12.5), place('Sicily', 38.12, 13.36),
  place('Malta', 35.9, 14.51), place('Dubrovnik', 42.65, 18.09), place('Athens', 37.98, 23.73), place('Crete', 35.51, 24.02),
  place('Cyprus', 34.68, 33.04), place('Antalya', 36.9, 30.7), place('Istanbul', 41.01, 28.98), place('Tel Aviv', 32.07, 34.78),
  place('Dubai', 25.2, 55.27), place('Tehran', 35.69, 51.39), place('Tbilisi', 41.72, 44.83), place('London', 51.51, -0.13),
  place('Dublin', 53.35, -6.26), place('Edinburgh', 55.95, -3.19), place('Paris', 48.86, 2.35), place('Rotterdam', 51.92, 4.48),
  place('Berlin', 52.52, 13.41), place('Oslo', 59.91, 10.75), place('Stockholm', 59.33, 18.07), place('Moscow', 55.76, 37.62),
  place('Reykjavik', 64.15, -21.94), place('Dakar', 14.72, -17.47), place('Cape Verde', 16.89, -25.0), place('Accra', 5.6, -0.19),
  place('Lagos', 6.52, 3.38), place('Addis Ababa', 9.03, 38.74), place('Nairobi', -1.29, 36.82), place('Zanzibar', -6.16, 39.2),
  place('Windhoek', -22.56, 17.08), place('Johannesburg', -26.2, 28.04), place('Durban', -29.86, 31.02), place('Cape Town', -33.93, 18.42),
  place('Delhi', 28.61, 77.21), place('Mumbai', 19.08, 72.88), place('Goa', 15.49, 73.83), place('Bangalore', 12.97, 77.59),
  place('Colombo', 6.93, 79.85), place('Kathmandu', 27.72, 85.32), place('Bangkok', 13.76, 100.5), place('Chiang Mai', 18.79, 98.98),
  place('Phuket', 7.88, 98.39), place('Hanoi', 21.03, 105.85), place('Ho Chi Minh City', 10.82, 106.63), place('Kuala Lumpur', 3.14, 101.69),
  place('Singapore', 1.35, 103.82), place('Jakarta', -6.21, 106.85), place('Bali', -8.65, 115.22), place('Manila', 14.6, 120.98),
  place('Hong Kong', 22.32, 114.17), place('Taipei', 25.03, 121.57), place('Kunming', 25.04, 102.72), place('Seoul', 37.57, 126.98),
  place('Tokyo', 35.68, 139.69), place('Darwin', -12.46, 130.84), place('Perth', -31.95, 115.86), place('Adelaide', -34.93, 138.6),
  place('Melbourne', -37.81, 144.96), place('Sydney', -33.87, 151.21), place('Brisbane', -27.47, 153.03), place('Auckland', -36.85, 174.76),
  place('Christchurch', -43.53, 172.64), place('Fiji', -18.14, 178.44), place('Tahiti', -17.53, -149.57), place('Honolulu', 21.31, -157.86),
  place('Vancouver', 49.28, -123.12), place('Seattle', 47.61, -122.33), place('San Francisco', 37.77, -122.42), place('Los Angeles', 34.05, -118.24),
  place('San Diego', 32.72, -117.16), place('Las Vegas', 36.17, -115.14), place('Phoenix', 33.45, -112.07), place('Denver', 39.74, -104.99),
  place('Austin', 30.27, -97.74), place('Houston', 29.76, -95.37), place('New Orleans', 29.95, -90.07), place('Chicago', 41.88, -87.63),
  place('Toronto', 43.65, -79.38), place('New York', 40.71, -74.01), place('Miami', 25.76, -80.19), place('Havana', 23.11, -82.37),
  place('Cancún', 21.16, -86.85), place('Mexico City', 19.43, -99.13), place('Guadalajara', 20.67, -103.35), place('Puerto Vallarta', 20.65, -105.23),
  place('Costa Rica', 9.93, -84.08), place('Panama City', 8.98, -79.52), place('Bogotá', 4.71, -74.07), place('Medellín', 6.25, -75.56),
  place('Quito', -0.18, -78.47), place('Lima', -12.05, -77.04), place('Cusco', -13.53, -71.97), place('Santiago', -33.45, -70.67),
  place('Buenos Aires', -34.6, -58.38), place('Montevideo', -34.9, -56.16), place('Rio de Janeiro', -22.91, -43.17), place('Florianópolis', -27.6, -48.55),
  place('Salvador', -12.97, -38.51),
];
