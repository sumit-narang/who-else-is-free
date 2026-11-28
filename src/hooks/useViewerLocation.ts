import { useCallback, useEffect, useRef, useState } from 'react';

import { AppState, Platform } from 'react-native';

import * as Location from 'expo-location';

import { logger } from '@services/logger';
import { Coordinates } from '@utils/distance';

type ViewerLocationPermission = 'granted' | 'denied' | 'undetermined';

export type ViewerLocationState = {
  coords: Coordinates | null;
  /** ISO country code resolved from the granted device location, when available. */
  countryCode: string | null;
  permission: ViewerLocationPermission | null;
  /**
   * True when location is not granted and asking again would show nothing, so
   * the person has to turn it on in Settings. On Android this is only trusted
   * after this session has actually asked (see inspectPermission).
   */
  isPromptBlocked: boolean;
  isLoading: boolean;
  requestPermission: () => Promise<void>;
};

const normalizeCountryCode = (value: string | null | undefined): string | null => {
  const normalized = value?.trim().toLowerCase() ?? '';
  return /^[a-z]{2}$/.test(normalized) ? normalized : null;
};

export const useViewerLocation = (): ViewerLocationState => {
  const [coords, setCoords] = useState<Coordinates | null>(null);
  const [countryCode, setCountryCode] = useState<string | null>(null);
  const [permission, setPermission] = useState<ViewerLocationPermission | null>(null);
  const [isPromptBlocked, setIsPromptBlocked] = useState(false);
  const [isLoading, setIsLoading] = useState(true);
  const isMounted = useRef(true);
  const pendingRequest = useRef<Promise<void> | null>(null);
  const hasAskedThisSession = useRef(false);
  const permissionRef = useRef<ViewerLocationPermission | null>(null);

  const loadPosition = useCallback(async () => {
    const position =
      (await Location.getLastKnownPositionAsync()) ??
      (await Location.getCurrentPositionAsync({
        accuracy: Location.Accuracy.Balanced,
      }));
    if (!isMounted.current) {
      return;
    }
    const nextCoords = {
      latitude: position.coords.latitude,
      longitude: position.coords.longitude,
    };
    setCoords(nextCoords);

    try {
      const [address] = await Location.reverseGeocodeAsync(nextCoords);
      if (isMounted.current) {
        setCountryCode(normalizeCountryCode(address?.isoCountryCode));
      }
    } catch (error) {
      logger.warn('Unable to resolve viewer country', error);
      if (isMounted.current) {
        setCountryCode(null);
      }
    }
  }, []);

  const inspectPermission = useCallback(
    async (allowPrompt: boolean) => {
      try {
        let permissionResult = await Location.getForegroundPermissionsAsync();
        // Ask whenever we do not already hold the permission, and let the OS
        // decide whether a dialog is warranted. expo's reported status cannot
        // carry that decision:
        //
        //   - it derives UNDETERMINED vs DENIED from its own record of having
        //     asked, kept in the SharedPreferences file
        //     expo.modules.permissions.asked;
        //   - Android auto-backup restores that file onto a fresh install
        //     (the generated backup rules include every sharedpref except
        //     SecureStore), so a brand new install can report DENIED;
        //   - canAskAgain is shouldShowRequestPermissionRationale, which is
        //     false when Android has never actually asked.
        //
        // A restored flag therefore produces DENIED + canAskAgain false, which
        // is indistinguishable from a permanent refusal even though the system
        // would happily show the dialog. Requesting is the only reliable
        // signal, and costs nothing when the permission really is blocked:
        // the OS returns immediately without showing anything.
        if (allowPrompt && !permissionResult.granted) {
          permissionResult = await Location.requestForegroundPermissionsAsync();
          hasAskedThisSession.current = true;
        }
        if (!isMounted.current) {
          return;
        }
        permissionRef.current = permissionResult.status;
        setPermission(permissionResult.status);
        // A request's own answer is reliable on both platforms; iOS also
        // reports canAskAgain truthfully without one.
        setIsPromptBlocked(
          !permissionResult.granted &&
            !permissionResult.canAskAgain &&
            (Platform.OS === 'ios' || hasAskedThisSession.current),
        );

        if (permissionResult.status === Location.PermissionStatus.GRANTED) {
          await loadPosition();
        } else {
          setCoords(null);
          setCountryCode(null);
        }
      } catch (error) {
        logger.warn('Unable to resolve viewer location', error);
        if (isMounted.current) {
          setCoords(null);
          setCountryCode(null);
        }
      } finally {
        if (isMounted.current) {
          setIsLoading(false);
        }
      }
    },
    [loadPosition],
  );

  const requestPermission = useCallback(() => {
    if (!pendingRequest.current) {
      pendingRequest.current = inspectPermission(true).finally(() => {
        pendingRequest.current = null;
      });
    }
    return pendingRequest.current;
  }, [inspectPermission]);

  useEffect(() => {
    isMounted.current = true;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- Native permission inspection resolves asynchronously before updating hook state.
    inspectPermission(false);

    return () => {
      isMounted.current = false;
    };
  }, [inspectPermission]);

  // Pick up a permission changed in Settings when the app comes back, in either
  // direction: iOS does not restart an app whose location access is revoked
  // (or whose "Allow once" lapsed), so a stale grant would otherwise keep
  // Discover on precise location. While still granted this is only the cheap
  // status check; the position is not reloaded. Skipped while a request is
  // open: the system dialog itself can background the app.
  useEffect(() => {
    const subscription = AppState.addEventListener('change', (state) => {
      if (state !== 'active' || pendingRequest.current) {
        return;
      }
      if (permissionRef.current !== Location.PermissionStatus.GRANTED) {
        void inspectPermission(false);
        return;
      }
      Location.getForegroundPermissionsAsync()
        .then((current) => {
          if (!current.granted && isMounted.current && !pendingRequest.current) {
            void inspectPermission(false);
          }
        })
        .catch(() => undefined);
    });
    return () => subscription.remove();
  }, [inspectPermission]);

  return { coords, countryCode, permission, isPromptBlocked, isLoading, requestPermission };
};
