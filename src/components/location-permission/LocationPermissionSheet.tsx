import { StyleSheet, View } from 'react-native';

import BottomSheetModal from '@components/BottomSheetModal';
import EventActionConfirm from '@components/EventActionConfirm';
import { spacing } from '@theme/index';

import LocationMapIllustration from './LocationMapIllustration';

export const LOCATION_PERMISSION_TITLE = 'See plans near you';
export const LOCATION_PERMISSION_DESCRIPTION =
  'Turn on location to see which plans are closest to you. We never store your exact location.';

export interface LocationPermissionSheetProps {
  visible: boolean;
  /** Continue: the sheet closes, then the system location prompt appears. */
  onContinue: () => void;
  /** Close button, backdrop tap or swipe down. */
  onDismiss: () => void;
  /** Fires once the sheet has finished closing. */
  onClosed: () => void;
}

type LocationSheetContentProps = {
  title?: string;
  description?: string;
  confirmLabel?: string;
  onConfirm: () => void;
  onClose: () => void;
};

/**
 * The map, copy and two buttons, shared by this sheet and Discover's Nearby
 * chip sheet. Defaults to the permission copy with Continue/Close.
 */
export const LocationSheetContent = ({
  title = LOCATION_PERMISSION_TITLE,
  description = LOCATION_PERMISSION_DESCRIPTION,
  confirmLabel = 'Continue',
  onConfirm,
  onClose,
}: LocationSheetContentProps) => (
  <View style={styles.content}>
    <View style={styles.illustration}>
      <LocationMapIllustration />
    </View>
    <EventActionConfirm
      headerAlign="center"
      title={title}
      description={description}
      confirmLabel={confirmLabel}
      cancelLabel="Close"
      onConfirm={onConfirm}
      onCancel={onClose}
    />
  </View>
);

/**
 * Explains why weif asks for location before the system prompt does. The
 * system dialog itself cannot be styled on either platform, so this sheet only
 * leads into it: Continue always shows the real prompt.
 */
const LocationPermissionSheet = ({
  visible,
  onContinue,
  onDismiss,
  onClosed,
}: LocationPermissionSheetProps) => (
  <BottomSheetModal
    visible={visible}
    onClose={onDismiss}
    onClosed={onClosed}
    testID="location-permission-sheet"
  >
    <LocationSheetContent onConfirm={onContinue} onClose={onDismiss} />
  </BottomSheetModal>
);

const styles = StyleSheet.create({
  content: {
    gap: spacing.md,
  },
  illustration: {
    alignItems: 'center',
    paddingTop: spacing.sm,
  },
});

export default LocationPermissionSheet;
