/* global AbortController */
import { useCallback, useEffect, useRef, useState } from 'react';

import * as SecureStore from 'expo-secure-store';

import { ApproximateLocation, fetchApproximateLocation } from '@api/location';
import type { ViewerLocationState } from '@hooks/useViewerLocation';
import { logger } from '@services/logger';
import { Coordinates } from '@utils/distance';

/**
 * Where Discover measures "near" from.
 *
 * - `precise`: the device location. Permission is granted and the Nearby chip
 *   is on; the chip shows filled only in this mode.
 * - `approximate`: the rough area the server resolves from the network
 *   address, used when device location is not shared or Nearby is off.
 * - `none`: neither is available; Discover shows every plan.
 */
export type DiscoverLocationMode = 'precise' | 'approximate' | 'none';

/**
 * Set while the person has switched Nearby off with the chip. The chip cannot
 * revoke the permission, so without this the next launch would see it granted
 * and switch Nearby straight back on.
 */
export const NEARBY_OFF_KEY = 'whoelseisfree.discoverNearbyOff';

/** One approximate lookup per hour is plenty: it only changes with the network. */
const APPROXIMATE_CACHE_MS = 60 * 60 * 1000;

let approximateCache: { value: ApproximateLocation | null; fetchedAt: number } | null = null;

const readApproximateCache = () =>
  approximateCache && Date.now() - approximateCache.fetchedAt < APPROXIMATE_CACHE_MS
    ? approximateCache
    : null;

/** Test hook: forget the cached approximate location. */
export const resetApproximateLocationCache = () => {
  approximateCache = null;
};

type ApproximateState =
  | { status: 'idle' }
  | { status: 'done'; value: ApproximateLocation | null }
  | { status: 'failed' };

const initialApproximateState = (): ApproximateState => {
  const cached = readApproximateCache();
  return cached ? { status: 'done', value: cached.value } : { status: 'idle' };
};

export type DiscoverLocation = {
  mode: DiscoverLocationMode;
  /** Coordinates to measure plan distances from, or null for `none`. */
  coords: Coordinates | null;
  /** True until the mode is settled; Discover shows every plan meanwhile. */
  isLoading: boolean;
  /** The chip's in-app switch; precise location also needs the permission. */
  isNearbyEnabled: boolean;
  /** Saved across launches. */
  setNearbyEnabled: (enabled: boolean) => void;
  /** Tries the approximate lookup again after a network failure. */
  retry: () => void;
};

/**
 * Resolves Discover's location: precise device location while Nearby is on
 * and permission is granted, else the approximate network area, else none.
 *
 * Nearby switches itself back on whenever the permission goes from not
 * granted to granted (through the chip's sheet, the startup sheet or
 * Settings): that grant is the person asking for it.
 *
 * The approximate lookup only runs when precise location is not in use, so
 * people who share their location never send it.
 */
export const useDiscoverLocation = (
  viewer: Pick<ViewerLocationState, 'coords' | 'isLoading' | 'permission'>,
): DiscoverLocation => {
  const [approximate, setApproximate] = useState<ApproximateState>(initialApproximateState);
  const [isNearbyEnabled, setNearbyEnabledState] = useState(true);
  const [hasLoadedNearbyChoice, setHasLoadedNearbyChoice] = useState(false);
  const previousPermissionRef = useRef(viewer.permission);
  // Set by any explicit change, so a slow read of the saved choice cannot
  // overwrite a switch (or a grant) that happened while it was loading.
  const hasChangedNearbyRef = useRef(false);

  useEffect(() => {
    let isActive = true;
    SecureStore.getItemAsync(NEARBY_OFF_KEY)
      .then((stored) => {
        if (isActive && stored && !hasChangedNearbyRef.current) {
          setNearbyEnabledState(false);
        }
      })
      .catch(() => undefined)
      .finally(() => {
        if (isActive) {
          setHasLoadedNearbyChoice(true);
        }
      });
    return () => {
      isActive = false;
    };
  }, []);

  const setNearbyEnabled = useCallback((enabled: boolean) => {
    hasChangedNearbyRef.current = true;
    setNearbyEnabledState(enabled);
    const write = enabled
      ? SecureStore.deleteItemAsync(NEARBY_OFF_KEY)
      : SecureStore.setItemAsync(NEARBY_OFF_KEY, '1');
    write.catch(() => undefined);
  }, []);

  useEffect(() => {
    const previous = previousPermissionRef.current;
    previousPermissionRef.current = viewer.permission;
    // null is "not checked yet": the first check finding a grant is no change.
    if (viewer.permission === 'granted' && previous != null && previous !== 'granted') {
      setNearbyEnabled(true);
    }
  }, [setNearbyEnabled, viewer.permission]);

  const usesPrecise = isNearbyEnabled && viewer.coords != null;
  const needsApproximate = hasLoadedNearbyChoice && !viewer.isLoading && !usesPrecise;

  useEffect(() => {
    if (!needsApproximate || approximate.status !== 'idle') {
      return;
    }
    const cached = readApproximateCache();
    if (cached) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- adopt a lookup another Discover instance already made.
      setApproximate({ status: 'done', value: cached.value });
      return;
    }

    const controller = new AbortController();
    fetchApproximateLocation(controller.signal)
      .then((value) => {
        approximateCache = { value, fetchedAt: Date.now() };
        setApproximate({ status: 'done', value });
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        logger.warn('Unable to resolve approximate location', error);
        setApproximate({ status: 'failed' });
      });
    return () => controller.abort();
  }, [approximate.status, needsApproximate]);

  const retry = useCallback(() => {
    setApproximate((current) => (current.status === 'failed' ? { status: 'idle' } : current));
  }, []);

  const approximateValue = approximate.status === 'done' ? approximate.value : null;

  let mode: DiscoverLocationMode = 'none';
  let coords: Coordinates | null = null;
  if (usesPrecise && viewer.coords) {
    mode = 'precise';
    coords = viewer.coords;
  } else if (approximateValue) {
    mode = 'approximate';
    coords = approximateValue.coords;
  }

  const isLoading =
    !hasLoadedNearbyChoice ||
    viewer.isLoading ||
    (needsApproximate && approximate.status === 'idle');

  return { mode, coords, isLoading, isNearbyEnabled, setNearbyEnabled, retry };
};
