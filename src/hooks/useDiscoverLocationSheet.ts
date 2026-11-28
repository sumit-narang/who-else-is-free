import { useCallback, useRef, useState } from 'react';

import { Linking } from 'react-native';

import type {
  DiscoverLocationSheetProps,
  DiscoverLocationSheetVariant,
} from '@components/location-permission/DiscoverLocationSheet';
import type { DiscoverLocation } from '@hooks/useDiscoverLocation';
import type { ViewerLocationState } from '@hooks/useViewerLocation';

type Params = {
  discoverLocation: Pick<DiscoverLocation, 'mode' | 'setNearbyEnabled'>;
  viewerLocation: Pick<ViewerLocationState, 'permission' | 'isPromptBlocked' | 'requestPermission'>;
};

/**
 * Runs Discover's Nearby chip, an in-app switch for precise location.
 *
 * - On: switches it off (remembered across launches). The permission stays
 *   granted; only the person can revoke it, in Settings.
 * - Off with permission granted: switches it back on, no sheet.
 * - Off without permission: a sheet always stands in the way. While the
 *   system can still ask it is the startup explanation, whose Continue shows
 *   the system prompt once the sheet has closed; after a refusal it is the
 *   Open settings variant. A grant switches Nearby on (useDiscoverLocation).
 *
 * Unlike the startup offer this ignores the once-per-install record: the
 * person tapped the chip, so they always get the way to precise location.
 */
export const useDiscoverLocationSheet = ({ discoverLocation, viewerLocation }: Params) => {
  const [visible, setVisible] = useState(false);
  // Fixed when the sheet opens, so the content cannot swap while it closes.
  const [variant, setVariant] = useState<DiscoverLocationSheetVariant>('permission');
  const confirmedRef = useRef(false);
  const { mode, setNearbyEnabled } = discoverLocation;
  const { permission, isPromptBlocked, requestPermission } = viewerLocation;

  const onChipPress = useCallback(() => {
    if (mode === 'precise') {
      setNearbyEnabled(false);
      return;
    }
    if (permission === 'granted') {
      setNearbyEnabled(true);
      return;
    }
    confirmedRef.current = false;
    setVariant(isPromptBlocked ? 'settings' : 'permission');
    setVisible(true);
  }, [isPromptBlocked, mode, permission, setNearbyEnabled]);

  const onConfirm = useCallback(() => {
    confirmedRef.current = true;
    setVisible(false);
  }, []);

  const onClose = useCallback(() => setVisible(false), []);

  // The system dialog and Settings wait for the sheet to be gone, so neither
  // appears over a sheet that is still closing.
  const onClosed = useCallback(() => {
    if (!confirmedRef.current) return;
    confirmedRef.current = false;
    if (variant === 'settings') {
      Linking.openSettings().catch(() => undefined);
    } else {
      requestPermission().catch(() => undefined);
    }
  }, [requestPermission, variant]);

  const sheetProps: DiscoverLocationSheetProps = {
    visible,
    variant,
    onConfirm,
    onClose,
    onClosed,
  };

  return { onChipPress, isNearbyActive: mode === 'precise', sheetProps };
};
