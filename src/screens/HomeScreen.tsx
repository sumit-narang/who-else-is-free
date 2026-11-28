import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

import { StyleSheet, Text, useWindowDimensions, View } from 'react-native';

import { BottomTabNavigationProp } from '@react-navigation/bottom-tabs';
import {
  useNavigation,
  useRoute,
  useIsFocused,
  CompositeNavigationProp,
  RouteProp,
} from '@react-navigation/native';
import { NativeStackNavigationProp } from '@react-navigation/native-stack';
import { useSafeAreaInsets } from 'react-native-safe-area-context';

import AnimatedPager from '@components/AnimatedPager';
import ConfettiOverlay from '@components/ConfettiOverlay';
import EmptyState from '@components/EmptyState';
import EventActionBadge from '@components/EventActionBadge';
import EventActionOverlay from '@components/EventActionOverlay';
import { EventItemProps } from '@components/EventCard';
import {
  EventListPage,
  EventListLoadState,
  EventSection,
  buildEventSections,
  buildSingleEventSection,
  sortEventsByCreatedAtDesc,
} from '@components/events';
import { emptyStateCenteredTop } from '@components/FullPageEmptyState';
import DiscoverLocationSheet from '@components/location-permission/DiscoverLocationSheet';
import LocationPermissionSheet from '@components/location-permission/LocationPermissionSheet';
import ScreenContainer from '@components/ScreenContainer';
import SelectionModal from '@components/SelectionModal';
import { DropdownChip, StatusChip } from '@components/ui';
import { useAuth } from '@context/AuthContext';
import { useBloom } from '@context/BloomContext';
import { useChat } from '@context/ChatContext';
import { UserEvent, useEvents } from '@context/EventsContext';
import { usePush } from '@context/PushContext';
import { useDiscoverLocation } from '@hooks/useDiscoverLocation';
import { useDiscoverLocationSheet } from '@hooks/useDiscoverLocationSheet';
import { useLocationPermissionPrompt } from '@hooks/useLocationPermissionPrompt';
import { useTabbedPages } from '@hooks/useTabbedPages';
import { useViewerLocation } from '@hooks/useViewerLocation';
import { RootStackParamList, RootTabParamList } from '@navigation/types';
import { colors, componentTokens, spacing, typography } from '@theme/index';
import { EventWithDistance, LOCAL_RADIUS_KM, withEventDistances } from '@utils/eventDiscovery';

const isLocalOrViewerOwnedEvent = (event: EventWithDistance<UserEvent>, viewerUserId?: number) =>
  (event.distanceKm != null && event.distanceKm <= LOCAL_RADIUS_KM) ||
  (viewerUserId != null && event.ownerId === viewerUserId);

type SortOptionValue = 'upcoming' | 'newest';
type NotificationNotice = 'unavailable';

// Shown as one dropdown chip whose sheet lists both. Distance is not a sort:
// both sorts show plans near the viewer, measured from
// the precise device location when shared, else the approximate network area
// (useDiscoverLocation). The Nearby chip switches precise location on and off.
const sortOptions: Array<{ label: string; value: SortOptionValue }> = [
  { label: 'Upcoming', value: 'upcoming' },
  { label: 'Newest created', value: 'newest' },
];

const HomeScreen = () => {
  type HomeScreenNavigation = CompositeNavigationProp<
    BottomTabNavigationProp<RootTabParamList, 'Events'>,
    NativeStackNavigationProp<RootStackParamList>
  >;

  const navigation = useNavigation<HomeScreenNavigation>();
  const route = useRoute<RouteProp<RootTabParamList, 'Events'>>();
  const {
    events: allEvents,
    isLoading,
    hasLoadedEvents,
    error,
    refreshEvents,
    refreshRequestedEvents,
    isEventRequested,
  } = useEvents();
  const { user } = useAuth();
  const { conversations } = useChat();
  const { signalReady, transitionComplete } = useBloom();
  const { requestPushPermission } = usePush();
  const viewerLocation = useViewerLocation();
  const requestLocationPermission = viewerLocation.requestPermission;
  const { offer: offerLocationPermission, sheetProps: locationPermissionSheetProps } =
    useLocationPermissionPrompt(requestLocationPermission);
  const discoverLocation = useDiscoverLocation(viewerLocation);
  const {
    onChipPress: handleNearbyChipPress,
    isNearbyActive,
    sheetProps: discoverLocationSheetProps,
  } = useDiscoverLocationSheet({ discoverLocation, viewerLocation });
  const isFocused = useIsFocused();
  const insets = useSafeAreaInsets();
  const [headerHeight, setHeaderHeight] = useState(0);
  const { height: windowHeight } = useWindowDimensions();
  const emptyStateTopPadding = emptyStateCenteredTop(windowHeight, 245);
  const hasSignalledReady = useRef(false);
  const hasStartedPermissionSequence = useRef(false);
  const [isPullRefreshing, setIsPullRefreshing] = useState(false);
  const [showReportedBadge, setShowReportedBadge] = useState(false);
  const [showEventDeletedBadge, setShowEventDeletedBadge] = useState(false);
  const [showEventLeftBadge, setShowEventLeftBadge] = useState(false);
  const [showWelcomeBadge, setShowWelcomeBadge] = useState(false);
  // Separate from the toast: the confetti runs to its own end, not the toast's.
  const [showWelcomeConfetti, setShowWelcomeConfetti] = useState(false);
  const [notificationNotice, setNotificationNotice] = useState<NotificationNotice | null>(null);

  useEffect(() => {
    if (!isFocused || !route.params?.notificationNotice) return;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- consume one-shot navigation notice after focus.
    setNotificationNotice(route.params.notificationNotice);
    navigation.setParams({ notificationNotice: undefined });
  }, [isFocused, navigation, route.params?.notificationNotice]);

  useEffect(() => {
    if (!route.params?.showWelcomeBadge) return;
    // Onboarding just finished: welcome toast and confetti together.
    const t = setTimeout(() => {
      setShowWelcomeBadge(true);
      setShowWelcomeConfetti(true);
    }, 350);
    return () => clearTimeout(t);
  }, [route.params?.showWelcomeBadge]);

  useEffect(() => {
    if (!route.params?.showEventReportedBadge) return;
    const t = setTimeout(() => setShowReportedBadge(true), 350);
    return () => clearTimeout(t);
  }, [route.params?.showEventReportedBadge]);

  useEffect(() => {
    if (!route.params?.showEventDeletedBadge) return;
    const t = setTimeout(() => setShowEventDeletedBadge(true), 350);
    return () => clearTimeout(t);
  }, [route.params?.showEventDeletedBadge]);

  useEffect(() => {
    if (!route.params?.showEventLeftBadge) return;
    const t = setTimeout(() => setShowEventLeftBadge(true), 350);
    return () => clearTimeout(t);
  }, [route.params?.showEventLeftBadge]);

  // Set of event IDs user has joined (is a member of conversation but not owner)
  const joinedEventIds = useMemo(() => {
    if (!user) return new Set<string>();
    const ids = new Set<string>();
    conversations.forEach((conversation) => {
      if (conversation.eventId && conversation.createdBy !== user.id) {
        ids.add(String(conversation.eventId));
      }
    });
    return ids;
  }, [conversations, user]);

  // Compute badge label based on user's relationship to the event
  const getBadgeLabel = useCallback(
    (event: UserEvent): string | undefined => {
      if (!user) return undefined;
      if (event.ownerId === user.id) return 'Hosting';
      if (joinedEventIds.has(event.id)) return 'Joined';
      if (isEventRequested(event.id)) return 'Pending';
      return undefined;
    },
    [user, joinedEventIds, isEventRequested],
  );

  const { pagerProps, tabsProps } = useTabbedPages(sortOptions, 'upcoming');
  const [isSortSheetVisible, setIsSortSheetVisible] = useState(false);
  const sortLabel =
    sortOptions.find((option) => option.value === tabsProps.value)?.label ?? sortOptions[0].label;
  const selectSort = tabsProps.onChange;
  const openSortSheet = useCallback(() => setIsSortSheetVisible(true), []);
  // Create Event's pill sheet without Done: a tap applies the sort and closes.
  const handleSortSelect = useCallback(
    (option: (typeof sortOptions)[number]) => {
      selectSort(option.value);
      setIsSortSheetVisible(false);
    },
    [selectSort],
  );

  const discoverableEvents = useMemo(() => {
    if (discoverLocation.isLoading || !discoverLocation.coords) {
      return null;
    }
    return withEventDistances(allEvents, discoverLocation.coords).filter((event) =>
      isLocalOrViewerOwnedEvent(event, user?.id),
    );
  }, [allEvents, user?.id, discoverLocation.coords, discoverLocation.isLoading]);

  const upcomingSections = useMemo<EventSection[]>(() => {
    if (!discoverableEvents) {
      return buildEventSections(allEvents, getBadgeLabel);
    }

    return buildEventSections(discoverableEvents, getBadgeLabel);
  }, [allEvents, discoverableEvents, getBadgeLabel]);

  const newestSections = useMemo<EventSection[]>(() => {
    if (discoverableEvents) {
      return buildSingleEventSection(
        'Newest created',
        [...discoverableEvents].sort(sortEventsByCreatedAtDesc),
        getBadgeLabel,
      );
    }

    const sorted = [...allEvents].sort(sortEventsByCreatedAtDesc);
    return buildSingleEventSection('Newest created', sorted, getBadgeLabel);
  }, [allEvents, discoverableEvents, getBadgeLabel]);

  useEffect(() => {
    if (!isLoading && !hasSignalledReady.current) {
      hasSignalledReady.current = true;
      signalReady();
    }
  }, [isLoading, signalReady]);

  useEffect(() => {
    if (!isFocused || !transitionComplete || hasStartedPermissionSequence.current) {
      return;
    }

    let isCancelled = false;
    hasStartedPermissionSequence.current = true;

    // Location first: it changes this screen straight away (Nearby turns on),
    // while a new user has nothing to be notified about yet.
    const requestStartupPermissions = async () => {
      // Location is explained in a sheet first; Continue leads to the system prompt.
      await offerLocationPermission();
      if (!isCancelled) {
        await requestPushPermission();
      }
    };

    requestStartupPermissions()
      .catch(() => undefined)
      .finally(() => {
        if (isCancelled) {
          hasStartedPermissionSequence.current = false;
        }
      });

    return () => {
      isCancelled = true;
    };
  }, [isFocused, offerLocationPermission, requestPushPermission, transitionComplete]);

  const showAllEventsLoading = !hasLoadedEvents && !error;
  const showAllEventsError = !!error && !isLoading && allEvents.length === 0;
  const showUpcomingEmpty = hasLoadedEvents && !error && upcomingSections.length === 0;
  const showNewestEmpty = hasLoadedEvents && !error && newestSections.length === 0;

  const refreshAll = useCallback(
    async () => Promise.all([refreshEvents(), refreshRequestedEvents()]),
    [refreshEvents, refreshRequestedEvents],
  );

  const retryDiscoverLocation = discoverLocation.retry;
  const handleRefresh = useCallback(() => {
    setIsPullRefreshing(true);
    retryDiscoverLocation();
    refreshAll()
      .catch(() => undefined)
      .finally(() => setIsPullRefreshing(false));
  }, [refreshAll, retryDiscoverLocation]);

  useEffect(() => {
    if (!isFocused) {
      return;
    }
    refreshAll().catch(() => undefined);
    return;
  }, [isFocused, refreshAll]);

  const handleEventPress = useCallback(
    (item: EventItemProps) => {
      navigation.navigate('EventDetails', {
        eventId: item.id,
        origin: 'Events',
      });
    },
    [navigation],
  );

  const discoverEmptyState = (
    <EmptyState
      title="Nothing happening yet"
      description="Create a plan and see who else is free."
      imageSource={require('@assets/empty-state/discover.png')}
    />
  );

  return (
    <ScreenContainer edges={['bottom']}>
      <View style={styles.content}>
        {showAllEventsLoading ? (
          <EventListLoadState status="loading" topPadding={headerHeight} />
        ) : showAllEventsError ? (
          <EventListLoadState
            status="error"
            errorMessage={error}
            onRetry={handleRefresh}
            topPadding={headerHeight}
          />
        ) : (
          <AnimatedPager
            {...pagerProps}
            style={styles.pager}
            isActive={isFocused}
            // The sort is a closed menu, so the list never changes under it by swipe.
            swipeEnabled={false}
          >
            <EventListPage
              sections={upcomingSections}
              onEventPress={handleEventPress}
              headerPaddingTop={headerHeight}
              emptyStateTopPadding={emptyStateTopPadding}
              bottomInset={insets.bottom}
              emptyState={showUpcomingEmpty ? discoverEmptyState : null}
              refreshing={isPullRefreshing}
              onRefresh={handleRefresh}
            />
            <EventListPage
              sections={newestSections}
              onEventPress={handleEventPress}
              headerPaddingTop={headerHeight}
              emptyStateTopPadding={emptyStateTopPadding}
              bottomInset={insets.bottom}
              emptyState={showNewestEmpty ? discoverEmptyState : null}
              refreshing={isPullRefreshing}
              onRefresh={handleRefresh}
            />
          </AnimatedPager>
        )}
        {/* Floating header */}
        <View
          style={[styles.floatingHeader, { paddingTop: insets.top }]}
          onLayout={(e) => setHeaderHeight(e.nativeEvent.layout.height)}
        >
          <View style={styles.headerSpacing}>
            <Text style={styles.headerTitle}>Discover</Text>
          </View>
          <View style={styles.filtersRow}>
            <StatusChip
              label="Nearby"
              active={isNearbyActive}
              onPress={handleNearbyChipPress}
              testID="discover-location-chip"
            />
            <DropdownChip
              label={sortLabel}
              onPress={openSortSheet}
              active
              accessibilityPrefix="Sort"
              testID="discover-sort-chip"
            />
          </View>
        </View>
      </View>
      <EventActionBadge
        visible={showWelcomeBadge}
        label="Welcome to weif"
        onHidden={() => {
          setShowWelcomeBadge(false);
          navigation.setParams({ showWelcomeBadge: false });
        }}
      />
      <EventActionBadge
        visible={showReportedBadge}
        label="Report submitted. We'll review it shortly."
        onHidden={() => {
          setShowReportedBadge(false);
          navigation.setParams({ showEventReportedBadge: false });
        }}
      />
      <EventActionBadge
        visible={showEventDeletedBadge}
        label="Plan deleted"
        onHidden={() => {
          setShowEventDeletedBadge(false);
          navigation.setParams({ showEventDeletedBadge: false });
        }}
      />
      <EventActionBadge
        visible={showEventLeftBadge}
        label="You left the plan"
        onHidden={() => {
          setShowEventLeftBadge(false);
          navigation.setParams({ showEventLeftBadge: false });
        }}
      />
      <EventActionOverlay
        isVisible={notificationNotice != null}
        type="result"
        title="Plan unavailable"
        // "to you" matters: a removed member's plan is still running for
        // everyone else, so the bare "no longer available" would be untrue for
        // access_removed. This reading holds for a cancelled plan too.
        description="This plan is no longer available to you. You can discover other plans here."
        dismissLabel="Explore"
        onDismiss={() => setNotificationNotice(null)}
        onBackdropPress={() => setNotificationNotice(null)}
      />
      <ConfettiOverlay
        active={showWelcomeConfetti}
        onFinished={() => setShowWelcomeConfetti(false)}
      />
      <LocationPermissionSheet {...locationPermissionSheetProps} />
      <DiscoverLocationSheet {...discoverLocationSheetProps} />
      <SelectionModal
        visible={isSortSheetVisible}
        title="Sort by"
        options={sortOptions}
        selectedValue={tabsProps.value}
        onSelect={handleSortSelect}
        onClose={() => setIsSortSheetVisible(false)}
        getLabel={(option) => option.label}
        getKey={(option) => option.value}
        isSelected={(option, selected) => option.value === selected}
      />
    </ScreenContainer>
  );
};

const styles = StyleSheet.create({
  content: {
    flex: 1,
  },
  floatingHeader: {
    position: 'absolute',
    top: 0,
    left: -spacing.md,
    right: -spacing.md,
    zIndex: 10,
    paddingHorizontal: spacing.md,
    backgroundColor: colors.background,
  },
  pager: {
    flex: 1,
    marginHorizontal: -spacing.md,
  },
  filtersRow: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: componentTokens.segmentedControl.gap,
    marginBottom: spacing.md,
  },
  headerSpacing: {
    paddingTop: spacing.lg - spacing.md,
    paddingBottom: 24,
  },
  headerTitle: {
    fontSize: typography.header,
    fontFamily: typography.fontFamilySemiBold,
    color: colors.text,
    lineHeight: typography.lineHeight,
    letterSpacing: typography.letterSpacing,
  },
});

export default HomeScreen;
