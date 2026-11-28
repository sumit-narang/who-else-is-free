import { memo } from 'react';

import { StyleSheet, Text } from 'react-native';

import ScalePressable from '@components/ScalePressable';
import { colors, componentTokens, radii, typography } from '@theme/index';

type StatusChipProps = {
  label: string;
  /** Filled like a selected pill tab when on, outlined when off. */
  active: boolean;
  onPress: () => void;
  accessibilityLabel?: string;
  accessibilityHint?: string;
  testID?: string;
};

/**
 * A pill that reports an on/off state and opens something about it. It shares
 * the pill tabs' geometry and colours (`AppTabs` variant "pill") so it sits in
 * the same row.
 */
const StatusChip = ({
  label,
  active,
  onPress,
  accessibilityLabel,
  accessibilityHint,
  testID,
}: StatusChipProps) => {
  const contentColor = active ? colors.selectedTextOnDark : colors.eventDetailRowText;
  return (
    <ScalePressable
      onPress={onPress}
      haptic="selection"
      accessibilityRole="switch"
      accessibilityState={{ checked: active }}
      accessibilityLabel={accessibilityLabel ?? label}
      accessibilityHint={accessibilityHint}
      testID={testID}
      style={[styles.chip, active ? styles.active : styles.inactive]}
    >
      <Text style={[styles.label, { color: contentColor }]} numberOfLines={1}>
        {label}
      </Text>
    </ScalePressable>
  );
};

const styles = StyleSheet.create({
  chip: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: componentTokens.segmentedControl.tabGap,
    paddingVertical: componentTokens.segmentedControl.tabPaddingVertical,
    paddingHorizontal: componentTokens.segmentedControl.tabPaddingHorizontal,
    borderRadius: radii.md,
    borderCurve: 'continuous',
    borderWidth: 1,
  },
  active: {
    backgroundColor: colors.primaryButtonBackground,
    borderColor: colors.transparent,
  },
  inactive: {
    backgroundColor: colors.transparent,
    borderColor: colors.borderSubtle,
  },
  label: {
    fontSize: 15,
    fontFamily: typography.fontFamilyMedium,
    lineHeight: 20,
    letterSpacing: typography.detailLetterSpacing,
  },
});

export default memo(StatusChip);
