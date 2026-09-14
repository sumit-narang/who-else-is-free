import { RefObject, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { InteractionManager, Pressable, ScrollView, StyleSheet, Text, View } from 'react-native';

import { LinearGradient } from 'expo-linear-gradient';

import { triggerHaptic } from '@services/haptics';
import { colors, typography } from '@theme/index';
import {
  clampDateTime,
  getPickerSectionDateLabel,
  isPastDateTimeSelection,
  toDateKey,
} from '@utils/dateTime';
import BottomSheetModal from './BottomSheetModal';
import styles from './EventDateTimeModal.styles';

const WHEEL_ITEM_HEIGHT = 44;
const DRAG_END_SETTLE_DELAY_MS = 16;
const WHEEL_MOUNT_DEFER_FRAMES = 2;

const HOURS_12 = Array.from({ length: 12 }, (_, i) => String(i + 1)); // ["1".."12"]
const MINUTES = Array.from({ length: 60 }, (_, i) => String(i).padStart(2, '0')); // ["00".."59"]
const AMPM = ['AM', 'PM'];

// Looped arrays: original repeated 3× so user has one buffer copy above and below
const HOURS_LOOPED = [...HOURS_12, ...HOURS_12, ...HOURS_12];
const MINUTES_LOOPED = [...MINUTES, ...MINUTES, ...MINUTES];
const HOUR_LOOP_OFFSET = HOURS_12.length; // 12 — start index of the middle copy
const MINUTE_LOOP_OFFSET = MINUTES.length; // 60 — start index of the middle copy
const HOUR_SNAP_OFFSETS = HOURS_LOOPED.map((_, index) => index * WHEEL_ITEM_HEIGHT);
const MINUTE_SNAP_OFFSETS = MINUTES_LOOPED.map((_, index) => index * WHEEL_ITEM_HEIGHT);
const AMPM_SNAP_OFFSETS = AMPM.map((_, index) => index * WHEEL_ITEM_HEIGHT);

export type EventDateTimeModalProps = {
  visible: boolean;
  value: Date;
  minDate: Date;
  maxDate: Date;
  onClose: () => void;
  onConfirm: (value: Date) => void;
  deferWheelMount?: boolean;
};

type EventDateTimePickerContentProps = Omit<EventDateTimeModalProps, 'onClose'>;

const toSafeDate = (value: Date) => {
  if (Number.isNaN(value.getTime())) {
    return new Date();
  }
  return value;
};

type DateWheelOption = {
  key: string;
  label: string;
  year: number;
  month: number;
  day: number;
};

const buildDateOptions = (minDate: Date, maxDate: Date): DateWheelOption[] => {
  const start = new Date(minDate.getFullYear(), minDate.getMonth(), minDate.getDate());
  const end = new Date(maxDate.getFullYear(), maxDate.getMonth(), maxDate.getDate());
  const options: DateWheelOption[] = [];
  const cursor = new Date(start);
  while (cursor.getTime() <= end.getTime()) {
    const key = toDateKey(cursor);
    options.push({
      key,
      label: getPickerSectionDateLabel(key),
      year: cursor.getFullYear(),
      month: cursor.getMonth(),
      day: cursor.getDate(),
    });
    cursor.setDate(cursor.getDate() + 1);
  }
  return options;
};

const clampWheelIndex = (offsetY: number, length: number): number => {
  if (length <= 0) return 0;
  const rounded = Math.round(offsetY / WHEEL_ITEM_HEIGHT);
  return Math.max(0, Math.min(length - 1, rounded));
};

/** Convert 24-hour value to logical 12-hour index (0 = "1", 11 = "12") */
const hour24ToIndex = (hour24: number): number => {
  const hour12 = hour24 % 12;
  return hour12 === 0 ? 11 : hour12 - 1;
};

/** Convert 12-hour logical index + AM/PM index to 24-hour value */
const indexToHour24 = (hourIndex: number, amPmIndex: number): number => {
  const hourLabel = hourIndex + 1; // 1-12
  if (amPmIndex === 0) {
    return hourLabel === 12 ? 0 : hourLabel;
  }
  return hourLabel === 12 ? 12 : hourLabel + 12;
};

export const EventDateTimePickerContent = ({
  visible,
  value,
  minDate,
  maxDate,
  onConfirm,
  deferWheelMount = true,
}: EventDateTimePickerContentProps) => {
  const [draftValue, setDraftValue] = useState(() =>
    clampDateTime(toSafeDate(value), minDate, maxDate),
  );
  const [error, setError] = useState<string | null>(null);
  const [wheelContentReady, setWheelContentReady] = useState(false);

  const dateWheelRef = useRef<ScrollView>(null);
  const hourWheelRef = useRef<ScrollView>(null);
  const minuteWheelRef = useRef<ScrollView>(null);
  const amPmWheelRef = useRef<ScrollView>(null);
  const wheelMomentumStateRef = useRef<Record<string, boolean>>({});
  const wheelDragSettleTimeoutRef = useRef<Record<string, ReturnType<typeof setTimeout> | null>>(
    {},
  );
  const wheelOffsetRef = useRef<Record<string, number>>({});
  const wheelUserDragRef = useRef<Record<string, boolean>>({});

  const dateOptions = useMemo(() => buildDateOptions(minDate, maxDate), [minDate, maxDate]);
  const dateLabels = useMemo(() => dateOptions.map((option) => option.label), [dateOptions]);

  const selectedDateIndex = useMemo(() => {
    const key = toDateKey(draftValue);
    const index = dateOptions.findIndex((opt) => opt.key === key);
    return index >= 0 ? index : 0;
  }, [dateOptions, draftValue]);

  const dateSnapOffsets = useMemo(
    () => dateOptions.map((_, index) => index * WHEEL_ITEM_HEIGHT),
    [dateOptions],
  );

  // Logical indices (used for bold highlight — check against looped arrays with modulo)
  const selectedHourLogical = hour24ToIndex(draftValue.getHours());
  const selectedMinuteLogical = draftValue.getMinutes();
  const selectedAmPmIndex = draftValue.getHours() >= 12 ? 1 : 0;
  const shouldRenderWheels = !deferWheelMount || (visible && wheelContentReady);

  const scrollWheelToIndex = useCallback(
    (ref: RefObject<ScrollView | null>, index: number, animated = false) => {
      ref.current?.scrollTo({ y: index * WHEEL_ITEM_HEIGHT, animated });
    },
    [],
  );

  useEffect(() => {
    if (!visible) return;
    const initialDraft = clampDateTime(toSafeDate(value), minDate, maxDate);
    setDraftValue(initialDraft);
    setError(null);
  }, [maxDate, minDate, value, visible]);

  useEffect(() => {
    if (!visible || !deferWheelMount) {
      return undefined;
    }

    let isCancelled = false;
    const animationFrames: number[] = [];
    const scheduleWheelMount = (remainingFrames: number) => {
      const frame = requestAnimationFrame(() => {
        if (isCancelled) {
          return;
        }
        if (remainingFrames <= 1) {
          setWheelContentReady(true);
          return;
        }
        scheduleWheelMount(remainingFrames - 1);
      });
      animationFrames.push(frame);
    };

    const interactionHandle = InteractionManager.runAfterInteractions(() => {
      scheduleWheelMount(WHEEL_MOUNT_DEFER_FRAMES);
    });

    return () => {
      isCancelled = true;
      interactionHandle.cancel();
      animationFrames.forEach((frame) => globalThis.cancelAnimationFrame(frame));
    };
  }, [deferWheelMount, visible]);

  const applyDateIndex = useCallback(
    (index: number) => {
      const option = dateOptions[index];
      if (!option) return;
      setDraftValue((prev) => {
        const next = new Date(prev);
        next.setFullYear(option.year, option.month, option.day);
        next.setSeconds(0, 0);
        return next;
      });
    },
    [dateOptions],
  );

  const applyHourIndex = useCallback((logicalIndex: number) => {
    setDraftValue((prev) => {
      const hour24 = indexToHour24(logicalIndex, prev.getHours() >= 12 ? 1 : 0);
      const next = new Date(prev);
      next.setHours(hour24, prev.getMinutes(), 0, 0);
      return next;
    });
  }, []);

  const applyMinuteIndex = useCallback((logicalIndex: number) => {
    setDraftValue((prev) => {
      const next = new Date(prev);
      next.setHours(prev.getHours(), logicalIndex, 0, 0);
      return next;
    });
  }, []);

  const applyAmPmIndex = useCallback((index: number) => {
    setDraftValue((prev) => {
      const hour12 = prev.getHours() % 12;
      const hour24 = index === 0 ? hour12 : hour12 + 12;
      const next = new Date(prev);
      next.setHours(hour24, prev.getMinutes(), 0, 0);
      return next;
    });
  }, []);

  // Looped scroll handlers: extract logical index and apply.
  const handleHourScrollIndex = useCallback(
    (rawIndex: number) => {
      const logical = rawIndex % HOURS_12.length;
      applyHourIndex(logical);
    },
    [applyHourIndex],
  );

  const handleMinuteScrollIndex = useCallback(
    (rawIndex: number) => {
      const logical = rawIndex % MINUTES.length;
      applyMinuteIndex(logical);
    },
    [applyMinuteIndex],
  );

  const handleConfirm = useCallback(() => {
    if (isPastDateTimeSelection(draftValue)) {
      setError('Please choose a future time');
      return;
    }
    triggerHaptic('submit');
    setError(null);
    onConfirm(draftValue);
  }, [draftValue, onConfirm]);

  const renderWheel = useCallback(
    (
      ref: RefObject<ScrollView | null>,
      items: string[],
      snapOffsets: number[],
      isSelected: (index: number) => boolean,
      onScrollIndex: (index: number) => void,
      keyPrefix: string,
      // For looped columns: map any raw index to its middle-copy equivalent for press/snap
      toMiddleIndex?: (rawIndex: number) => number,
      initialOffset: number = 0,
    ) => {
      const clearDragSettleTimeout = () => {
        const timeout = wheelDragSettleTimeoutRef.current[keyPrefix];
        if (timeout) {
          clearTimeout(timeout);
          wheelDragSettleTimeoutRef.current[keyPrefix] = null;
        }
      };

      const settleWheelAtOffset = (offsetY: number, animateToSnap = false) => {
        const index = clampWheelIndex(offsetY, items.length);
        const target = toMiddleIndex ? toMiddleIndex(index) : index;
        if (wheelUserDragRef.current[keyPrefix]) {
          triggerHaptic('selection');
          wheelUserDragRef.current[keyPrefix] = false;
        }
        onScrollIndex(index);
        const targetOffset = target * WHEEL_ITEM_HEIGHT;
        const needsSnapAlignment = Math.abs(offsetY - targetOffset) > 0.5;

        if (target !== index) {
          scrollWheelToIndex(ref, target, false);
        } else if (needsSnapAlignment) {
          scrollWheelToIndex(ref, target, animateToSnap);
        }
      };

      return (
        <ScrollView
          ref={ref}
          contentOffset={{ x: 0, y: initialOffset }}
          showsVerticalScrollIndicator={false}
          snapToOffsets={snapOffsets}
          decelerationRate="fast"
          contentContainerStyle={styles.wheelContentContainer}
          onScroll={(e) => {
            wheelOffsetRef.current[keyPrefix] = e.nativeEvent.contentOffset.y;
          }}
          scrollEventThrottle={16}
          onScrollBeginDrag={() => {
            clearDragSettleTimeout();
            wheelMomentumStateRef.current[keyPrefix] = false;
            wheelUserDragRef.current[keyPrefix] = true;
          }}
          onMomentumScrollBegin={() => {
            clearDragSettleTimeout();
            wheelMomentumStateRef.current[keyPrefix] = true;
          }}
          onScrollEndDrag={(e) => {
            const y =
              e.nativeEvent.targetContentOffset?.y ??
              wheelOffsetRef.current[keyPrefix] ??
              e.nativeEvent.contentOffset.y;
            clearDragSettleTimeout();
            wheelDragSettleTimeoutRef.current[keyPrefix] = setTimeout(() => {
              if (wheelMomentumStateRef.current[keyPrefix]) {
                return;
              }
              settleWheelAtOffset(y, true);
              wheelDragSettleTimeoutRef.current[keyPrefix] = null;
            }, DRAG_END_SETTLE_DELAY_MS);
          }}
          onMomentumScrollEnd={(e) => {
            wheelMomentumStateRef.current[keyPrefix] = false;
            clearDragSettleTimeout();
            settleWheelAtOffset(wheelOffsetRef.current[keyPrefix] ?? e.nativeEvent.contentOffset.y);
          }}
        >
          {items.map((item, index) => (
            <Pressable
              key={`${keyPrefix}-${index}`}
              style={styles.wheelItem}
              onPress={() => {
                const target = toMiddleIndex ? toMiddleIndex(index) : index;
                onScrollIndex(target);
                scrollWheelToIndex(ref, target, true);
              }}
            >
              <Text style={[styles.wheelText, isSelected(index) && styles.wheelTextSelected]}>
                {item}
              </Text>
            </Pressable>
          ))}
        </ScrollView>
      );
    },
    [scrollWheelToIndex],
  );

  return (
    <>
      <View style={styles.pickerContainer}>
        {/* Selection indicator: two thin lines framing the centre row */}
        <View pointerEvents="none" style={styles.selectionIndicator}>
          <View style={styles.selectionLine} />
          <View style={styles.selectionSpacer} />
          <View style={styles.selectionLine} />
        </View>

        {shouldRenderWheels ? (
          <>
            <View style={styles.wheelRow}>
              {/* Date column — bounded, no looping */}
              <View style={[styles.wheelColumn, styles.dateWheelColumn]}>
                {renderWheel(
                  dateWheelRef,
                  dateLabels,
                  dateSnapOffsets,
                  (i) => i === selectedDateIndex,
                  applyDateIndex,
                  'date',
                  undefined,
                  selectedDateIndex * WHEEL_ITEM_HEIGHT,
                )}
              </View>

              {/* Hour column — looped */}
              <View style={[styles.wheelColumn, styles.timeWheelColumn]}>
                {renderWheel(
                  hourWheelRef,
                  HOURS_LOOPED,
                  HOUR_SNAP_OFFSETS,
                  (i) => i % HOURS_12.length === selectedHourLogical,
                  handleHourScrollIndex,
                  'hour',
                  (i) => HOUR_LOOP_OFFSET + (i % HOURS_12.length),
                  (HOUR_LOOP_OFFSET + selectedHourLogical) * WHEEL_ITEM_HEIGHT,
                )}
              </View>

              {/* Separator */}
              <Text style={styles.timeSeparator}>:</Text>

              {/* Minute column — looped */}
              <View style={[styles.wheelColumn, styles.timeWheelColumn]}>
                {renderWheel(
                  minuteWheelRef,
                  MINUTES_LOOPED,
                  MINUTE_SNAP_OFFSETS,
                  (i) => i % MINUTES.length === selectedMinuteLogical,
                  handleMinuteScrollIndex,
                  'minute',
                  (i) => MINUTE_LOOP_OFFSET + (i % MINUTES.length),
                  (MINUTE_LOOP_OFFSET + selectedMinuteLogical) * WHEEL_ITEM_HEIGHT,
                )}
              </View>

              {/* AM/PM column — only 2 items, no looping needed */}
              <View style={[styles.wheelColumn, styles.amPmWheelColumn]}>
                {renderWheel(
                  amPmWheelRef,
                  AMPM,
                  AMPM_SNAP_OFFSETS,
                  (i) => i === selectedAmPmIndex,
                  applyAmPmIndex,
                  'ampm',
                  undefined,
                  selectedAmPmIndex * WHEEL_ITEM_HEIGHT,
                )}
              </View>
            </View>

            {/* Fade gradients — each covers 2 items above/below centre */}
            <LinearGradient
              colors={['rgba(255,255,255,1)', 'rgba(255,255,255,0)']}
              style={styles.fadeTop}
              pointerEvents="none"
            />
            <LinearGradient
              colors={['rgba(255,255,255,0)', 'rgba(255,255,255,1)']}
              style={styles.fadeBottom}
              pointerEvents="none"
            />
          </>
        ) : (
          <View style={styles.wheelPlaceholder} pointerEvents="none" />
        )}
      </View>

      {error ? <Text style={errorStyle.text}>{error}</Text> : null}
      <Pressable style={styles.confirmButton} onPress={handleConfirm}>
        <Text style={styles.confirmButtonText}>Done</Text>
      </Pressable>
    </>
  );
};

const EventDateTimeModal = ({ onClose, ...contentProps }: EventDateTimeModalProps) => {
  return (
    <BottomSheetModal visible={contentProps.visible} onClose={onClose} title="When is your event?">
      <EventDateTimePickerContent {...contentProps} />
    </BottomSheetModal>
  );
};

const errorStyle = StyleSheet.create({
  text: {
    textAlign: 'center',
    fontSize: 13,
    fontFamily: typography.fontFamilyRegular,
    color: colors.error,
    marginBottom: 8,
  },
});

export default EventDateTimeModal;
