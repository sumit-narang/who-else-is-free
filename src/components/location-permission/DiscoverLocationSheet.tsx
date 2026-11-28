import { memo } from 'react';

import BottomSheetModal from '@components/BottomSheetModal';

import { LocationSheetContent } from './LocationPermissionSheet';

/**
 * - `permission`: the system can still ask; the startup sheet's explanation,
 *   whose Continue leads to the system prompt.
 * - `settings`: location was denied before and the system will not ask again;
 *   Open settings is the only way to turn it on.
 */
export type DiscoverLocationSheetVariant = 'permission' | 'settings';

export const LOCATION_SETTINGS_SHEET_TITLE = 'Location is off for weif';
export const LOCATION_SETTINGS_SHEET_DESCRIPTION =
  'Turn on location in Settings to see which plans are closest to you. We never store your exact location.';

export type DiscoverLocationSheetProps = {
  visible: boolean;
  variant: DiscoverLocationSheetVariant;
  /** Continue or Open settings; acts once the sheet has closed. */
  onConfirm: () => void;
  onClose: () => void;
  onClosed: () => void;
};

/** Stands between the Nearby chip and precise location when it is not allowed. */
const DiscoverLocationSheet = ({
  visible,
  variant,
  onConfirm,
  onClose,
  onClosed,
}: DiscoverLocationSheetProps) => (
  <BottomSheetModal
    visible={visible}
    onClose={onClose}
    onClosed={onClosed}
    testID="discover-location-sheet"
  >
    {variant === 'settings' ? (
      <LocationSheetContent
        title={LOCATION_SETTINGS_SHEET_TITLE}
        description={LOCATION_SETTINGS_SHEET_DESCRIPTION}
        confirmLabel="Open settings"
        onConfirm={onConfirm}
        onClose={onClose}
      />
    ) : (
      <LocationSheetContent onConfirm={onConfirm} onClose={onClose} />
    )}
  </BottomSheetModal>
);

export default memo(DiscoverLocationSheet);
