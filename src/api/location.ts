/* global AbortSignal */
import { requestJson } from '@api/client';
import { Coordinates } from '@utils/distance';

/**
 * Rough area from the caller's IP address (`GET /api/location/approximate`),
 * used by Discover's Nearby mode when device location is not shared. City
 * level only: the server rounds the coordinates and resolves nothing finer.
 */
export type ApproximateLocation = {
  city: string;
  countryCode: string | null;
  coords: Coordinates;
};

type ApproximateLocationPayload = {
  city?: unknown;
  country_code?: unknown;
  latitude?: unknown;
  longitude?: unknown;
};

const isFiniteNumber = (value: unknown): value is number =>
  typeof value === 'number' && Number.isFinite(value);

export const mapApproximateLocation = (payload: unknown): ApproximateLocation | null => {
  if (!payload || typeof payload !== 'object') {
    return null;
  }
  const { city, country_code, latitude, longitude } = payload as ApproximateLocationPayload;
  if (!isFiniteNumber(latitude) || !isFiniteNumber(longitude)) {
    return null;
  }
  return {
    city: typeof city === 'string' ? city.trim() : '',
    countryCode:
      typeof country_code === 'string' && country_code.trim()
        ? country_code.trim().toLowerCase()
        : null,
    coords: { latitude, longitude },
  };
};

/**
 * Resolves to null when the server has no answer (204: no database, a private
 * address, or no city for this network). Network and server errors throw.
 */
export const fetchApproximateLocation = async (
  signal?: AbortSignal,
): Promise<ApproximateLocation | null> => {
  const payload = await requestJson<unknown>('/api/location/approximate', { signal });
  return mapApproximateLocation(payload);
};
