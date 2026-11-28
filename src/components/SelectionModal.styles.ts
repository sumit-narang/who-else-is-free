import { StyleSheet } from 'react-native';

import { colors, spacing, typography } from '@theme/index';

const styles = StyleSheet.create({
  chipsContainer: {
    flexDirection: 'row',
    flexWrap: 'wrap',
    gap: 6,
    marginBottom: spacing.lg,
  },
  chipsContainerLast: {
    marginBottom: 0,
  },
  chip: {
    paddingHorizontal: 10,
    paddingVertical: spacing.sm,
    borderRadius: 10,
    borderCurve: 'continuous',
    overflow: 'hidden',
    borderWidth: 1,
    borderColor: colors.sheetPillBorder,
    backgroundColor: 'transparent',
  },
  chipSelected: {
    backgroundColor: colors.primary,
    borderColor: colors.primary,
  },
  chipText: {
    fontSize: 15,
    fontFamily: typography.fontFamilyMedium,
    color: '#494949',
    lineHeight: 20,
    letterSpacing: -0.3,
  },
  chipTextSelected: {
    color: colors.selectedTextOnDark,
  },
  selectButton: {
    backgroundColor: colors.primary,
    borderRadius: 999,
    borderCurve: 'continuous',
    height: 52,
    alignItems: 'center',
    justifyContent: 'center',
  },
  helperText: {
    marginBottom: spacing.md,
  },
  selectButtonDisabled: {
    backgroundColor: colors.disabledButtonBackground,
  },
  selectButtonTextDisabled: {
    color: colors.disabledButtonText,
  },
  selectButtonText: {
    color: colors.buttonText,
    fontSize: 17,
    fontFamily: typography.fontFamilyMedium,
    lineHeight: 24,
    letterSpacing: -0.5,
    textAlign: 'center',
  },
});

export default styles;
