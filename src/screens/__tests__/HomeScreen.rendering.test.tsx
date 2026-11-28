/**
 * Rendering tests for HomeScreen
 * Tests actual component rendering using @testing-library/react-native
 */

import React from 'react';

import { render, fireEvent, act, waitFor } from '@testing-library/react-native';

import {
  mockEvents,
  mockUsers,
  mockConversations,
  createTodayEvent,
  createTomorrowEvent,
} from '../../__tests__/mocks/mockData';
import { mockNavigation, mockRoute } from '../../__tests__/mocks/mockModules';
import HomeScreen from '../HomeScreen';

const mockUser = mockUsers[0];

let mockAuthValue: { user: typeof mockUser | null; token: string | null } = {
  user: mockUser,
  token: 'test-token',
};
let mockEventsValue = {
  events: mockEvents,
  isLoading: false,
  hasLoadedEvents: true,
  error: null as string | null,
  refreshEvents: jest.fn().mockResolvedValue(undefined),
  refreshRequestedEvents: jest.fn().mockResolvedValue(undefined),
  isEventRequested: jest.fn().mockReturnValue(false),
};
let mockChatValue = { conversations: mockConversations };
const mockSignalReady = jest.fn();
const mockRequestPushPermission = jest.fn().mockResolvedValue(true);
const mockRequestLocationPermission = jest.fn().mockResolvedValue(undefined);
const mockOfferLocationPermission = jest.fn().mockResolvedValue(undefined);
let mockTransitionComplete = false;
let mockViewerLocation = {
  coords: null as { latitude: number; longitude: number } | null,
  permission: null as 'granted' | 'denied' | 'undetermined' | null,
  isPromptBlocked: false,
  isLoading: false,
  requestPermission: mockRequestLocationPermission,
};

jest.mock('@context/AuthContext', () => ({ useAuth: () => mockAuthValue }));
jest.mock('@context/EventsContext', () => ({ useEvents: () => mockEventsValue }));
jest.mock('@context/ChatContext', () => ({ useChat: () => mockChatValue }));
jest.mock('@context/BloomContext', () => ({
  useBloom: () => ({
    signalReady: mockSignalReady,
    transitionComplete: mockTransitionComplete,
  }),
}));
jest.mock('@hooks/useLocationPermissionPrompt', () => ({
  useLocationPermissionPrompt: () => ({
    offer: mockOfferLocationPermission,
    sheetProps: {
      visible: false,
      onContinue: jest.fn(),
      onDismiss: jest.fn(),
      onClosed: jest.fn(),
    },
  }),
}));
jest.mock('@context/PushContext', () => ({
  usePush: () => ({ requestPushPermission: mockRequestPushPermission }),
}));
jest.mock('@hooks/useViewerLocation', () => ({
  useViewerLocation: () => mockViewerLocation,
}));
const mockRetryDiscoverLocation = jest.fn();
const mockSetNearbyEnabled = jest.fn();
let mockDiscoverLocation = {
  mode: 'none' as 'precise' | 'approximate' | 'none',
  coords: null as { latitude: number; longitude: number } | null,
  isLoading: false,
  isNearbyEnabled: true,
  setNearbyEnabled: mockSetNearbyEnabled,
  retry: mockRetryDiscoverLocation,
};
jest.mock('@hooks/useDiscoverLocation', () => ({
  useDiscoverLocation: () => mockDiscoverLocation,
}));
jest.mock('@components/location-permission/DiscoverLocationSheet', () => {
  const { Pressable, Text, View } = require('react-native');
  return {
    __esModule: true,
    default: ({
      visible,
      variant,
      onConfirm,
    }: {
      visible: boolean;
      variant: string;
      onConfirm: () => void;
    }) =>
      visible ? (
        <View testID="discover-location-sheet" accessibilityHint={variant}>
          <Pressable testID="discover-location-confirm" onPress={onConfirm}>
            <Text>Confirm</Text>
          </Pressable>
        </View>
      ) : null,
  };
});
jest.mock('@react-navigation/bottom-tabs', () => {
  const actual = jest.requireActual('@react-navigation/bottom-tabs');
  return {
    ...actual,
    useBottomTabBarHeight: () => 0,
  };
});

jest.mock('@components/EmptyState', () => {
  const { View, Text } = require('react-native');
  return ({
    title,
    description,
    actionLabel,
    onActionPress,
  }: {
    title: string;
    description: string;
    actionLabel?: string;
    onActionPress?: () => void;
  }) => (
    <View testID="empty-state">
      <Text testID="empty-state-title">{title}</Text>
      <Text testID="empty-state-description">{description}</Text>
      {actionLabel ? <Text onPress={onActionPress}>{actionLabel}</Text> : null}
    </View>
  );
});

jest.mock('@components/ScreenContainer', () => {
  const { View } = require('react-native');
  return ({ children }: { children: React.ReactNode }) => (
    <View testID="screen-container">{children}</View>
  );
});

jest.mock('@components/AnimatedPager', () => {
  const React = require('react');
  const { View } = require('react-native');
  return ({
    children,
    selectedIndex,
  }: {
    children: React.ReactNode;
    selectedIndex: number;
    onPageChange: (index: number) => void;
  }) => {
    const pages = React.Children.toArray(children);
    return <View testID="animated-pager">{pages[selectedIndex]}</View>;
  };
});

jest.mock('@components/BottomSheetModal', () => {
  const { View } = require('react-native');
  return ({
    visible,
    children,
    testID,
  }: {
    visible: boolean;
    children: React.ReactNode;
    testID?: string;
  }) => (visible ? <View testID={testID}>{children}</View> : null);
});

jest.mock('@components/EventActionOverlay', () => {
  const { Pressable, Text, View } = require('react-native');
  return ({
    isVisible,
    title,
    description,
    dismissLabel,
    onDismiss,
  }: {
    isVisible: boolean;
    title: string;
    description: string;
    dismissLabel: string;
    onDismiss: () => void;
  }) =>
    isVisible ? (
      <View testID="notification-notice-overlay">
        <Text>{title}</Text>
        <Text>{description}</Text>
        <Pressable testID="notification-notice-dismiss" onPress={onDismiss}>
          <Text>{dismissLabel}</Text>
        </Pressable>
      </View>
    ) : null;
});

jest.mock('@assets/empty-state/discover.png', () => 'mock-empty-state-image');

describe('HomeScreen Rendering', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockAuthValue = { user: mockUser, token: 'test-token' };
    mockEventsValue = {
      events: mockEvents,
      isLoading: false,
      hasLoadedEvents: true,
      error: null,
      refreshEvents: jest.fn().mockResolvedValue(undefined),
      refreshRequestedEvents: jest.fn().mockResolvedValue(undefined),
      isEventRequested: jest.fn().mockReturnValue(false),
    };
    mockChatValue = { conversations: mockConversations };
    mockTransitionComplete = false;
    mockViewerLocation = {
      coords: null,
      permission: null,
      isPromptBlocked: false,
      isLoading: false,
      requestPermission: mockRequestLocationPermission,
    };
    mockDiscoverLocation = {
      mode: 'none',
      coords: null,
      isLoading: false,
      isNearbyEnabled: true,
      setNearbyEnabled: mockSetNearbyEnabled,
      retry: mockRetryDiscoverLocation,
    };
    mockRoute.params = {};
  });

  it('waits for the splash handoff, then offers the location sheet before asking for push', async () => {
    const view = render(<HomeScreen />);

    expect(mockRequestPushPermission).not.toHaveBeenCalled();
    expect(mockOfferLocationPermission).not.toHaveBeenCalled();

    mockTransitionComplete = true;
    view.rerender(<HomeScreen />);

    await waitFor(() => expect(mockRequestPushPermission).toHaveBeenCalledTimes(1));
    expect(mockOfferLocationPermission).toHaveBeenCalledTimes(1);
    expect(mockOfferLocationPermission.mock.invocationCallOrder[0]).toBeLessThan(
      mockRequestPushPermission.mock.invocationCallOrder[0],
    );
    // The system prompt is only reached through the sheet's Continue.
    expect(mockRequestLocationPermission).not.toHaveBeenCalled();
  });

  it('consumes the deleted-event notice once and shows the informational prompt', () => {
    mockRoute.params = { notificationNotice: 'unavailable' };
    const { getByText, getByTestId, queryByTestId } = render(<HomeScreen />);

    expect(getByText('Plan unavailable')).toBeTruthy();
    expect(
      getByText('This plan is no longer available to you. You can discover other plans here.'),
    ).toBeTruthy();
    expect(getByText('Explore')).toBeTruthy();
    expect(mockNavigation.setParams).toHaveBeenCalledWith({ notificationNotice: undefined });

    fireEvent.press(getByTestId('notification-notice-dismiss'));
    expect(queryByTestId('notification-notice-overlay')).toBeNull();
  });

  it('celebrates finishing onboarding with the welcome toast and confetti together', async () => {
    mockRoute.params = { showWelcomeBadge: true };
    const view = render(<HomeScreen />);

    expect(view.queryByTestId('confetti-overlay')).toBeNull();
    await waitFor(() => expect(view.getByText('Welcome to weif')).toBeTruthy());
    expect(view.getByTestId('confetti-overlay')).toBeTruthy();
  });

  describe('Loading State', () => {
    it('renders loading indicator when loading with no events', () => {
      mockEventsValue.isLoading = true;
      mockEventsValue.hasLoadedEvents = false;
      mockEventsValue.events = [];
      const { getByTestId, queryByTestId } = render(<HomeScreen />);
      expect(getByTestId('screen-container')).toBeTruthy();
      expect(queryByTestId('empty-state')).toBeNull();
    });

    it('does not show loading when events exist', () => {
      mockEventsValue.isLoading = true;
      const { getAllByTestId } = render(<HomeScreen />);
      expect(getAllByTestId('event-card').length).toBeGreaterThan(0);
    });

    it('renders events while viewer location is still loading', () => {
      mockDiscoverLocation.isLoading = true;

      const { getAllByTestId, queryByTestId } = render(<HomeScreen />);

      expect(queryByTestId('empty-state')).toBeNull();
      expect(getAllByTestId('event-card').length).toBeGreaterThan(0);
    });
  });

  describe('Error State', () => {
    it('displays error message and retry button', () => {
      mockEventsValue.isLoading = false;
      mockEventsValue.events = [];
      mockEventsValue.error = 'Unable to load plans.';
      const { getByText, getByLabelText } = render(<HomeScreen />);
      expect(getByText('Unable to load plans.')).toBeTruthy();
      expect(getByText('Try again')).toBeTruthy();
      expect(getByLabelText('Unable to load plans')).toBeTruthy();
    });

    it('calls refreshEvents when retry button is pressed', () => {
      mockEventsValue.events = [];
      mockEventsValue.error = 'Network error';
      const { getByText } = render(<HomeScreen />);
      fireEvent.press(getByText('Try again'));
      expect(mockEventsValue.refreshEvents).toHaveBeenCalled();
    });
  });

  describe('Empty State', () => {
    it('renders empty state with correct content when no events', () => {
      mockEventsValue.events = [];
      const { getByTestId } = render(<HomeScreen />);
      expect(getByTestId('empty-state')).toBeTruthy();
      expect(getByTestId('empty-state-title').props.children).toBe('Nothing happening yet');
      expect(getByTestId('empty-state-description').props.children).toContain('Create a plan');
    });

    it('keeps a resolved empty state mounted during a focus refresh', () => {
      mockEventsValue.events = [];
      const screen = render(<HomeScreen />);
      const emptyState = screen.getByTestId('empty-state');

      mockEventsValue.isLoading = true;
      screen.rerender(<HomeScreen />);

      expect(screen.getByTestId('empty-state')).toBe(emptyState);
    });
  });

  describe('Events Grouped by Sections', () => {
    it('renders Today section for today events', () => {
      mockEventsValue.events = [createTodayEvent({ id: '100' })];
      const { getByText } = render(<HomeScreen />);
      expect(getByText('Today')).toBeTruthy();
    });

    it('renders Tomorrow section for tomorrow events', () => {
      mockEventsValue.events = [createTomorrowEvent({ id: '101' })];
      const { getByText } = render(<HomeScreen />);
      expect(getByText('Tomorrow')).toBeTruthy();
    });

    it('renders both sections when events exist for both days', () => {
      mockEventsValue.events = [
        createTodayEvent({ id: '100' }),
        createTomorrowEvent({ id: '101' }),
      ];
      const { getByText } = render(<HomeScreen />);
      expect(getByText('Today')).toBeTruthy();
      expect(getByText('Tomorrow')).toBeTruthy();
    });

    it('renders formatted headers for non-Today/Tomorrow dates', () => {
      mockEventsValue.events = [
        createTodayEvent({
          id: '102',
          eventDate: '2099-01-15',
        }),
      ];
      const futureDate = new Date(2099, 0, 15);
      const expectedHeader = `${String(futureDate.getDate()).padStart(2, '0')} ${futureDate.toLocaleString('en-US', { month: 'short' })}, ${futureDate.toLocaleString('en-US', { weekday: 'short' })}`;

      const { getByText } = render(<HomeScreen />);
      expect(getByText(expectedHeader)).toBeTruthy();
    });

    it('renders correct number of event cards', () => {
      const { getAllByTestId } = render(<HomeScreen />);
      expect(getAllByTestId('event-card').length).toBe(mockEvents.length);
    });

    it('renders compact third-line card metadata', () => {
      mockEventsValue.events = [
        createTodayEvent({
          id: '103',
          groupType: 'Group',
          gender: 'Any',
          minAge: 30,
          maxAge: 40,
        }),
      ];

      const { getByText, queryByText } = render(<HomeScreen />);
      expect(getByText('Group · 30-40')).toBeTruthy();
      expect(queryByText('All Gender, 18 to 50 years')).toBeNull();
    });
  });

  describe('Sort Toggle', () => {
    it('shows the sort as one dropdown chip that defaults to Upcoming', () => {
      const { getByTestId, queryByTestId } = render(<HomeScreen />);
      expect(getByTestId('discover-sort-chip')).toHaveTextContent('Upcoming');
      expect(queryByTestId('option-upcoming')).toBeNull();
    });

    it('lists Upcoming and Newest as pills in the sort sheet, the current one selected', () => {
      const { getByTestId, queryByTestId } = render(<HomeScreen />);

      fireEvent.press(getByTestId('discover-sort-chip'));

      expect(getByTestId('option-upcoming').props.accessibilityState.selected).toBe(true);
      expect(getByTestId('option-newest').props.accessibilityState.selected).toBe(false);
      expect(queryByTestId('option-nearest')).toBeNull();
    });

    it('applies the sort on tap and closes, with no Done button', () => {
      const { getByTestId, queryByTestId } = render(<HomeScreen />);

      fireEvent.press(getByTestId('discover-sort-chip'));
      expect(queryByTestId('selection-modal-confirm')).toBeNull();
      fireEvent.press(getByTestId('option-newest'));

      expect(queryByTestId('option-newest')).toBeNull();
      expect(getByTestId('discover-sort-chip')).toHaveTextContent('Newest');
    });

    it('switches to newest mode and shows Newest Created section header', () => {
      mockEventsValue.events = [
        createTodayEvent({ id: '100', createdAt: new Date().toISOString() }),
      ];
      const { getByTestId, getAllByText, queryByText, queryByTestId } = render(<HomeScreen />);
      expect(getAllByText('Today')[0]).toBeTruthy();
      fireEvent.press(getByTestId('discover-sort-chip'));
      fireEvent.press(getByTestId('option-newest'));
      expect(queryByTestId('option-newest')).toBeNull();
      expect(getByTestId('discover-sort-chip')).toHaveTextContent('Newest');
      expect(getAllByText('Newest created')[0]).toBeTruthy();
      expect(queryByText('Today')).toBeNull();
    });

    it('only shows nearby events when location is available', () => {
      mockDiscoverLocation.mode = 'precise';
      mockDiscoverLocation.coords = { latitude: 12.9716, longitude: 77.5946 };
      mockEventsValue.events = [
        createTodayEvent({
          id: 'blr',
          title: 'Bangalore Coffee',
          latitude: 12.975,
          longitude: 77.6,
        }),
        createTodayEvent({
          id: 'dub',
          title: 'Dublin Pint',
          ownerId: 2,
          latitude: 53.3498,
          longitude: -6.2603,
        }),
        createTodayEvent({
          id: 'unknown',
          title: 'Mystery Hangout',
          ownerId: 2,
        }),
      ];

      const { getByText, queryByText } = render(<HomeScreen />);

      expect(getByText('Today')).toBeTruthy();
      expect(getByText('Bangalore Coffee')).toBeTruthy();
      expect(queryByText('Farther away')).toBeNull();
      expect(queryByText('Dublin Pint')).toBeNull();
      expect(queryByText('Unknown distance')).toBeNull();
      expect(queryByText('Mystery Hangout')).toBeNull();
    });

    it('keeps viewer-owned events visible even when they are beyond the local radius', () => {
      mockDiscoverLocation.mode = 'precise';
      mockDiscoverLocation.coords = { latitude: 12.9716, longitude: 77.5946 };
      mockEventsValue.events = [
        createTodayEvent({
          id: 'owned-dub',
          title: 'My Dublin Meetup',
          ownerId: mockUser.id,
          latitude: 53.3498,
          longitude: -6.2603,
        }),
        createTodayEvent({
          id: 'other-dub',
          title: 'Other Dublin Meetup',
          ownerId: 2,
          latitude: 53.3498,
          longitude: -6.2603,
        }),
      ];

      const { getByText, queryByText } = render(<HomeScreen />);

      expect(getByText('My Dublin Meetup')).toBeTruthy();
      expect(getByText('Hosting')).toBeTruthy();
      expect(queryByText('Other Dublin Meetup')).toBeNull();
    });

    it('filters by the approximate area when precise location is not in use', () => {
      mockDiscoverLocation.mode = 'approximate';
      mockDiscoverLocation.coords = { latitude: 53.35, longitude: -6.23 };
      mockEventsValue.events = [
        createTodayEvent({
          id: 'dub',
          title: 'Dublin Pint',
          ownerId: 2,
          latitude: 53.3498,
          longitude: -6.2603,
        }),
        createTodayEvent({
          id: 'blr',
          title: 'Bangalore Coffee',
          ownerId: 2,
          latitude: 12.975,
          longitude: 77.6,
        }),
      ];

      const { getByText, queryByText } = render(<HomeScreen />);

      expect(getByText('Dublin Pint')).toBeTruthy();
      expect(queryByText('Bangalore Coffee')).toBeNull();
    });
  });

  describe('Nearby chip', () => {
    it.each([
      ['precise', true],
      ['approximate', false],
      ['none', false],
    ] as const)('in %s mode is on: %s', (mode, on) => {
      mockDiscoverLocation.mode = mode;
      const { getByTestId } = render(<HomeScreen />);
      const chip = getByTestId('discover-location-chip');
      expect(chip).toHaveTextContent('Nearby');
      expect(chip.props.accessibilityState).toEqual(expect.objectContaining({ checked: on }));
    });

    it('switches Nearby off with one tap when it is on', () => {
      mockDiscoverLocation.mode = 'precise';
      mockViewerLocation.permission = 'granted';
      const { getByTestId, queryByTestId } = render(<HomeScreen />);

      fireEvent.press(getByTestId('discover-location-chip'));

      expect(mockSetNearbyEnabled).toHaveBeenCalledWith(false);
      expect(queryByTestId('discover-location-sheet')).toBeNull();
    });

    it('asks for location when permission is not given', () => {
      mockDiscoverLocation.mode = 'approximate';
      mockViewerLocation.permission = 'denied';
      const { getByTestId } = render(<HomeScreen />);

      fireEvent.press(getByTestId('discover-location-chip'));

      expect(getByTestId('discover-location-sheet').props.accessibilityHint).toBe('permission');
      expect(mockSetNearbyEnabled).not.toHaveBeenCalled();
    });

    it('points to Settings after a refusal', () => {
      mockDiscoverLocation.mode = 'approximate';
      mockViewerLocation.permission = 'denied';
      mockViewerLocation.isPromptBlocked = true;
      const { getByTestId } = render(<HomeScreen />);

      fireEvent.press(getByTestId('discover-location-chip'));

      expect(getByTestId('discover-location-sheet').props.accessibilityHint).toBe('settings');
    });

    it('retries the approximate lookup on pull-to-refresh', async () => {
      const { UNSAFE_getAllByType } = render(<HomeScreen />);
      const { SectionList } = require('react-native');
      await act(async () => {
        UNSAFE_getAllByType(SectionList)[0].props.refreshControl.props.onRefresh();
      });
      expect(mockRetryDiscoverLocation).toHaveBeenCalledTimes(1);
    });
  });

  describe('Pull-to-Refresh', () => {
    it('calls refreshEvents on pull-to-refresh', async () => {
      const { UNSAFE_getByType } = render(<HomeScreen />);
      const { SectionList } = require('react-native');
      const refreshControl = UNSAFE_getByType(SectionList).props.refreshControl;
      await act(async () => {
        refreshControl.props.onRefresh();
      });
      expect(mockEventsValue.refreshEvents).toHaveBeenCalled();
    });

    it('does not tie pull-refresh spinner to background loading', () => {
      mockEventsValue.isLoading = true;
      const { UNSAFE_getByType } = render(<HomeScreen />);
      const { SectionList } = require('react-native');
      expect(UNSAFE_getByType(SectionList).props.refreshControl.props.refreshing).toBe(false);
    });
  });

  describe('Event Card Navigation', () => {
    it('navigates to EventDetails with correct params when card pressed', () => {
      const { getAllByTestId } = render(<HomeScreen />);
      fireEvent.press(getAllByTestId('event-card')[0]);
      expect(mockNavigation.navigate).toHaveBeenCalledWith('EventDetails', {
        eventId: mockEvents[0].id,
        origin: 'Events',
      });
    });

    it('passes correct eventId for different events', () => {
      const { getAllByTestId } = render(<HomeScreen />);
      fireEvent.press(getAllByTestId('event-card')[1]);
      // Events are grouped by date: Today events first, then Tomorrow
      // mockEvents[0] (id: '1') and mockEvents[2] (id: '3') are Today, so index 1 is id '3'
      expect(mockNavigation.navigate).toHaveBeenCalledWith('EventDetails', {
        eventId: '3',
        origin: 'Events',
      });
    });
  });

  describe('Badge Labels', () => {
    it('shows Hosting badge for user-owned events', () => {
      mockEventsValue.events = [createTodayEvent({ id: '100', ownerId: mockUser.id })];
      const { getByTestId } = render(<HomeScreen />);
      expect(getByTestId('event-card-badge')).toBeTruthy();
    });

    it('shows Pending badge for requested events', () => {
      mockEventsValue.events = [createTodayEvent({ id: '99', ownerId: 999 })];
      mockEventsValue.isEventRequested = jest.fn().mockReturnValue(true);
      const { getByTestId } = render(<HomeScreen />);
      expect(getByTestId('event-card-badge')).toBeTruthy();
    });

    it('shows Joined badge for events user has joined', () => {
      mockEventsValue.events = [createTodayEvent({ id: '1', ownerId: 999 })];
      mockChatValue.conversations = [{ ...mockConversations[0], eventId: 1, createdBy: 999 }];
      const { getByTestId } = render(<HomeScreen />);
      expect(getByTestId('event-card-badge')).toBeTruthy();
    });
  });

  describe('Header', () => {
    it('renders Discover header', () => {
      const { getByText } = render(<HomeScreen />);
      expect(getByText('Discover')).toBeTruthy();
    });
  });

  describe('Guest User', () => {
    it('renders events without badges for guest user', () => {
      mockAuthValue.user = null;
      const { getAllByTestId, queryAllByTestId } = render(<HomeScreen />);
      expect(getAllByTestId('event-card').length).toBe(mockEvents.length);
      expect(queryAllByTestId('event-card-badge').length).toBe(0);
    });
  });
});
