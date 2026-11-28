import { memo } from 'react';

import { StyleSheet, Text } from 'react-native';

import ChevronDownIcon from '@assets/ui/chevron-down.svg';
import ScalePressable from '@components/ScalePressable';
import { colors, componentTokens, radii, typography } from '@theme/index';

type DropdownChipProps = {
  label: string;
  onPress: () => void;
  /**
   * Filled like a selected pill tab. Use it when the chip always holds a
   * choice in effect (a sort), so it reads as applied rather than optional.
   */
  active?: boolean;
  /** Read before the label, e.g. "Sort" so it announces "Sort, Upcoming". */
  accessibilityPrefix?: string;
  testID?: string;
};

const CHEVRON_SIZE = 16;

/**
 * A chip showing the current choice that opens the list of choices (a sheet or
 * menu). It shares `StatusChip`'s and the pill tabs' geometry and resting
 * outline, and the chevron sets it apart from an on/off chip beside it.
 */
const DropdownChip = ({
  label,
  onPress,
  active = false,
  accessibilityPrefix,
  testID,
}: DropdownChipProps) => {
  const contentColor = active ? colors.selectedTextOnDark : colors.eventDetailRowText;
  return (
    <ScalePressable
      onPress={onPress}
      haptic="selection"
      accessibilityRole="button"
      accessibilityLabel={accessibilityPrefix ? `${accessibilityPrefix}, ${label}` : label}
      testID={testID}
      style={[styles.chip, active ? styles.active : styles.inactive]}
    >
      <Text style={[styles.label, { color: contentColor }]} numberOfLines={1}>
        {label}
      </Text>
      <ChevronDownIcon width={CHEVRON_SIZE} height={CHEVRON_SIZE} color={contentColor} />
    </ScalePressable>
  );
};

const styles = StyleSheet.create({
  chip: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: componentTokens.segmentedControl.tabGap,
    paddingVertical: componentTokens.segmentedControl.tabPaddingVertical,
    paddingLeft: componentTokens.segmentedControl.tabPaddingHorizontal,
    paddingRight: componentTokens.segmentedControl.tabPaddingHorizontal - 2,
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

export default memo(DropdownChip);
