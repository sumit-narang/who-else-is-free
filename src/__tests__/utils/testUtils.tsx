/**
 * Test utilities for React Native Testing Library
 *
 * This module provides comprehensive testing utilities for component rendering tests.
 * It includes:
 * - Mock context providers (Auth, Events, Chat)
 * - Navigation mocking helpers
 * - Route params helpers for screen tests
 * - Custom render function with all providers
 * - Jest mock setup helpers for context hooks
 */

import React, { ReactElement, ReactNode } from 'react';
import { render, RenderOptions } from '@testing-library/react-native';
import { NavigationContainer } from '@react-navigation/native';

import { mockUsers, mockEvents, mockConversations, mockMessages, MockAuthUser, MockUserEvent } from '../mocks/mockData';
import type { ChatConversation, ChatMessage, ChatJoinRequest } from '@context/ChatContext';

// ============================================================================
// NAVIGATION MOCKING
// ============================================================================

/**
 * Mock navigation object for testing navigation calls.
 * Use this to verify navigation.navigate(), goBack(), etc. were called correctly.
 *
 * @example
 * ```tsx
 * import { mockNavigation } from '../utils/testUtils';
 *
 * // In your test
 * expect(mockNavigation.navigate).toHaveBeenCalledWith('EventDetails', { eventId: '1' });
 * ```
 */
export const mockNavigation = {
  navigate: jest.fn(),
  goBack: jest.fn(),
  reset: jest.fn(),
  setOptions: jest.fn(),
  setParams: jest.fn(),
  dispatch: jest.fn(),
  isFocused: jest.fn().mockReturnValue(true),
  canGoBack: jest.fn().mockReturnValue(true),
  getId: jest.fn().mockReturnValue('test-id'),
  getParent: jest.fn().mockReturnValue(null),
  getState: jest.fn().mockReturnValue({ routes: [], index: 0 }),
  addListener: jest.fn().mockReturnValue(() => {}),
  removeListener: jest.fn(),
  push: jest.fn(),
  pop: jest.fn(),
  popToTop: jest.fn(),
  replace: jest.fn(),
};

/**
 * Creates a mock route object with optional params.
 * Use this for screens that receive route params.
 *
 * @example
 * ```tsx
 * const route = createMockRoute({ eventId: '1', origin: 'Home' });
 * render(<EventDetailsScreen navigation={mockNavigation} route={route} />);
 * ```
 */
export const createMockRoute = <T extends Record<string, unknown>>(
  params?: T,
  routeName = 'TestScreen'
) => ({
  key: `${routeName}-test-key`,
  name: routeName,
  params: params ?? ({} as T),
  path: undefined,
});

/**
 * Reset all navigation mock functions.
 * Call this in beforeEach to ensure clean state between tests.
 *
 * @example
 * ```tsx
 * beforeEach(() => {
 *   resetNavigationMocks();
 * });
 * ```
 */
export const resetNavigationMocks = () => {
  mockNavigation.navigate.mockClear();
  mockNavigation.goBack.mockClear();
  mockNavigation.reset.mockClear();
  mockNavigation.setOptions.mockClear();
  mockNavigation.setParams.mockClear();
  mockNavigation.dispatch.mockClear();
  mockNavigation.push.mockClear();
  mockNavigation.pop.mockClear();
  mockNavigation.popToTop.mockClear();
  mockNavigation.replace.mockClear();
};

// ============================================================================
// JEST MOCK SETUP HELPERS
// ============================================================================

/**
 * Setup jest.mock for @context/AuthContext.
 * Call this at the top of your test file (before imports) or in jest.setup.
 *
 * @example
 * ```tsx
 * // At the top of your test file
 * import { setupAuthContextMock, mockAuthValues } from '../utils/testUtils';
 *
 * // Must be called before other imports that use useAuth
 * jest.mock('@context/AuthContext', () => setupAuthContextMock());
 *
 * // Then in your tests, you can override values
 * mockAuthValues.user = null; // Test logged out state
 * ```
 */
export const mockAuthValues: MockAuthContextValue = {
  user: mockUsers[0],
  token: 'mock-token',
  isSigningIn: false,
  signInWithGoogle: jest.fn(),
  signInWithApple: jest.fn(),
  signOut: jest.fn(),
  refreshSessionSilently: jest.fn().mockResolvedValue(null),
  updateProfile: jest.fn(),
  deleteAccount: jest.fn().mockResolvedValue(undefined),
  handleSessionExpired: jest.fn(),
  authFetch: jest.fn(),
};

export const setupAuthContextMock = () => ({
  ...jest.requireActual('@context/AuthContext'),
  useAuth: () => mockAuthValues,
  AuthProvider: ({ children }: { children: ReactNode }) => <>{children}</>,
});

/**
 * Setup jest.mock for @context/EventsContext.
 *
 * @example
 * ```tsx
 * jest.mock('@context/EventsContext', () => setupEventsContextMock());
 *
 * // In tests
 * mockEventsValues.events = []; // Test empty state
 * mockEventsValues.isLoading = true; // Test loading state
 * ```
 */
export const mockEventsValues: MockEventsContextValue = {
  events: mockEvents,
  userEvents: mockEvents.filter((e) => e.ownerId === 1),
  requestedEvents: [],
  isLoading: false,
  error: null,
  refreshEvents: jest.fn().mockResolvedValue(undefined),
  refreshRequestedEvents: jest.fn().mockResolvedValue(undefined),
  addUserEvent: jest.fn().mockResolvedValue('new-event-id'),
  updateUserEvent: jest.fn().mockResolvedValue(undefined),
  deleteUserEvent: jest.fn().mockResolvedValue(undefined),
  queueGuestEvent: jest.fn(),
  markEventRequested: jest.fn(),
  isEventRequested: jest.fn().mockReturnValue(false),
  unmarkEventRequested: jest.fn(),
  markEventReported: jest.fn(),
  isEventReported: jest.fn().mockReturnValue(false),
};

export const setupEventsContextMock = () => ({
  ...jest.requireActual('@context/EventsContext'),
  useEvents: () => mockEventsValues,
  EventsProvider: ({ children }: { children: ReactNode }) => <>{children}</>,
});

/**
 * Setup jest.mock for @context/ChatContext.
 *
 * @example
 * ```tsx
 * jest.mock('@context/ChatContext', () => setupChatContextMock());
 *
 * // In tests
 * mockChatValues.conversations = []; // Test empty state
 * mockChatValues.isConnecting = true; // Test connecting state
 * ```
 */
export const mockChatValues: MockChatContextValue = {
  conversations: mockConversations,
  activeConversationId: null,
  isConnecting: false,
  error: null,
  messages: mockMessages,
  joinRequestsByConversation: {},
  setActiveConversation: jest.fn(),
  refreshConversations: jest.fn().mockResolvedValue(undefined),
  sendMessage: jest.fn(),
  retryMessage: jest.fn(),
  refreshJoinRequests: jest.fn().mockResolvedValue(undefined),
  approveJoinRequest: jest.fn().mockResolvedValue(undefined),
  denyJoinRequest: jest.fn().mockResolvedValue(undefined),
  reportMember: jest.fn().mockResolvedValue(undefined),
  isRefreshingConversations: false,
};

export const setupChatContextMock = () => ({
  ...jest.requireActual('@context/ChatContext'),
  useChat: () => mockChatValues,
  ChatProvider: ({ children }: { children: ReactNode }) => <>{children}</>,
});

/**
 * Reset all mock values to their defaults.
 * Call this in beforeEach to ensure clean state between tests.
 *
 * @example
 * ```tsx
 * beforeEach(() => {
 *   resetAllMockValues();
 * });
 * ```
 */
export const resetAllMockValues = () => {
  // Reset Auth
  mockAuthValues.user = mockUsers[0];
  mockAuthValues.token = 'mock-token';
  mockAuthValues.isSigningIn = false;
  mockAuthValues.signInWithGoogle = jest.fn();
  mockAuthValues.signInWithApple = jest.fn();
  mockAuthValues.signOut = jest.fn();
  mockAuthValues.refreshSessionSilently = jest.fn().mockResolvedValue(null);
  mockAuthValues.updateProfile = jest.fn();

  // Reset Events
  mockEventsValues.events = mockEvents;
  mockEventsValues.userEvents = mockEvents.filter((e) => e.ownerId === 1);
  mockEventsValues.requestedEvents = [];
  mockEventsValues.isLoading = false;
  mockEventsValues.error = null;
  mockEventsValues.refreshEvents = jest.fn().mockResolvedValue(undefined);
  mockEventsValues.refreshRequestedEvents = jest.fn().mockResolvedValue(undefined);
  mockEventsValues.addUserEvent = jest.fn().mockResolvedValue('new-event-id');
  mockEventsValues.updateUserEvent = jest.fn().mockResolvedValue(undefined);
  mockEventsValues.deleteUserEvent = jest.fn().mockResolvedValue(undefined);
  mockEventsValues.queueGuestEvent = jest.fn();
  mockEventsValues.markEventRequested = jest.fn();
  mockEventsValues.isEventRequested = jest.fn().mockReturnValue(false);
  mockEventsValues.unmarkEventRequested = jest.fn();
  mockEventsValues.markEventReported = jest.fn();
  mockEventsValues.isEventReported = jest.fn().mockReturnValue(false);

  // Reset Chat
  mockChatValues.conversations = mockConversations;
  mockChatValues.activeConversationId = null;
  mockChatValues.isConnecting = false;
  mockChatValues.error = null;
  mockChatValues.messages = mockMessages;
  mockChatValues.joinRequestsByConversation = {};
  mockChatValues.setActiveConversation = jest.fn();
  mockChatValues.refreshConversations = jest.fn().mockResolvedValue(undefined);
  mockChatValues.sendMessage = jest.fn();
  mockChatValues.retryMessage = jest.fn();
  mockChatValues.refreshJoinRequests = jest.fn().mockResolvedValue(undefined);
  mockChatValues.approveJoinRequest = jest.fn().mockResolvedValue(undefined);
  mockChatValues.denyJoinRequest = jest.fn().mockResolvedValue(undefined);
  mockChatValues.reportMember = jest.fn().mockResolvedValue(undefined);
  mockChatValues.isRefreshingConversations = false;

  // Reset Navigation
  resetNavigationMocks();
};

/**
 * Setup all context mocks at once.
 * Use this in jest.setup.ts or at the top of test files.
 *
 * @example
 * ```tsx
 * // In jest.setup.ts
 * import { setupAllContextMocks } from './src/__tests__/utils/testUtils';
 * setupAllContextMocks();
 * ```
 */
export const setupAllContextMocks = () => {
  jest.mock('@context/AuthContext', () => setupAuthContextMock());
  jest.mock('@context/EventsContext', () => setupEventsContextMock());
  jest.mock('@context/ChatContext', () => setupChatContextMock());
};

// ============================================================================
// CONTEXT TYPES AND PROVIDERS
// ============================================================================

// Mock Auth Context
interface MockAuthContextValue {
  user: MockAuthUser | null;
  token: string | null;
  isSigningIn: boolean;
  signInWithGoogle: jest.Mock;
  signInWithApple: jest.Mock;
  signOut: jest.Mock;
  refreshSessionSilently: jest.Mock;
  updateProfile: jest.Mock;
  deleteAccount: jest.Mock;
  handleSessionExpired: jest.Mock;
  authFetch: jest.Mock;
}

const defaultMockAuthContext: MockAuthContextValue = {
  user: mockUsers[0],
  token: 'mock-token',
  isSigningIn: false,
  signInWithGoogle: jest.fn(),
  signInWithApple: jest.fn(),
  signOut: jest.fn(),
  refreshSessionSilently: jest.fn().mockResolvedValue(null),
  updateProfile: jest.fn(),
  deleteAccount: jest.fn().mockResolvedValue(undefined),
  handleSessionExpired: jest.fn(),
  authFetch: jest.fn(),
};

export const MockAuthContext = React.createContext<MockAuthContextValue | undefined>(undefined);

export const MockAuthProvider = ({
  children,
  value = defaultMockAuthContext,
}: {
  children: ReactNode;
  value?: Partial<MockAuthContextValue>;
}) => {
  const contextValue = { ...defaultMockAuthContext, ...value };
  return <MockAuthContext.Provider value={contextValue}>{children}</MockAuthContext.Provider>;
};

// Mock Events Context
interface MockEventsContextValue {
  events: MockUserEvent[];
  userEvents: MockUserEvent[];
  requestedEvents: MockUserEvent[];
  isLoading: boolean;
  error: string | null;
  refreshEvents: jest.Mock;
  refreshRequestedEvents: jest.Mock;
  addUserEvent: jest.Mock;
  updateUserEvent: jest.Mock;
  deleteUserEvent: jest.Mock;
  queueGuestEvent: jest.Mock;
  markEventRequested: jest.Mock;
  isEventRequested: jest.Mock;
  unmarkEventRequested: jest.Mock;
  markEventReported: jest.Mock;
  isEventReported: jest.Mock;
}

const defaultMockEventsContext: MockEventsContextValue = {
  events: mockEvents,
  userEvents: mockEvents.filter((e) => e.ownerId === 1),
  requestedEvents: [],
  isLoading: false,
  error: null,
  refreshEvents: jest.fn().mockResolvedValue(undefined),
  refreshRequestedEvents: jest.fn().mockResolvedValue(undefined),
  addUserEvent: jest.fn().mockResolvedValue('new-event-id'),
  updateUserEvent: jest.fn().mockResolvedValue(undefined),
  deleteUserEvent: jest.fn().mockResolvedValue(undefined),
  queueGuestEvent: jest.fn(),
  markEventRequested: jest.fn(),
  isEventRequested: jest.fn().mockReturnValue(false),
  unmarkEventRequested: jest.fn(),
  markEventReported: jest.fn(),
  isEventReported: jest.fn().mockReturnValue(false),
};

export const MockEventsContext = React.createContext<MockEventsContextValue | undefined>(undefined);

export const MockEventsProvider = ({
  children,
  value = {},
}: {
  children: ReactNode;
  value?: Partial<MockEventsContextValue>;
}) => {
  const contextValue = { ...defaultMockEventsContext, ...value };
  return <MockEventsContext.Provider value={contextValue}>{children}</MockEventsContext.Provider>;
};

// Mock Chat Context
interface MockChatContextValue {
  conversations: ChatConversation[];
  activeConversationId: number | null;
  isConnecting: boolean;
  error: string | null;
  messages: ChatMessage[];
  joinRequestsByConversation: Record<number, ChatJoinRequest[]>;
  setActiveConversation: jest.Mock;
  refreshConversations: jest.Mock;
  sendMessage: jest.Mock;
  retryMessage: jest.Mock;
  refreshJoinRequests: jest.Mock;
  approveJoinRequest: jest.Mock;
  denyJoinRequest: jest.Mock;
  reportMember: jest.Mock;
  isRefreshingConversations: boolean;
}

const defaultMockChatContext: MockChatContextValue = {
  conversations: mockConversations,
  activeConversationId: null,
  isConnecting: false,
  error: null,
  messages: mockMessages,
  joinRequestsByConversation: {},
  setActiveConversation: jest.fn(),
  refreshConversations: jest.fn().mockResolvedValue(undefined),
  sendMessage: jest.fn(),
  retryMessage: jest.fn(),
  refreshJoinRequests: jest.fn().mockResolvedValue(undefined),
  approveJoinRequest: jest.fn().mockResolvedValue(undefined),
  denyJoinRequest: jest.fn().mockResolvedValue(undefined),
  reportMember: jest.fn().mockResolvedValue(undefined),
  isRefreshingConversations: false,
};

export const MockChatContext = React.createContext<MockChatContextValue | undefined>(undefined);

export const MockChatProvider = ({
  children,
  value = {},
}: {
  children: ReactNode;
  value?: Partial<MockChatContextValue>;
}) => {
  const contextValue = { ...defaultMockChatContext, ...value };
  return <MockChatContext.Provider value={contextValue}>{children}</MockChatContext.Provider>;
};

// Combined providers
interface AllProvidersProps {
  children: ReactNode;
  authValue?: Partial<MockAuthContextValue>;
  eventsValue?: Partial<MockEventsContextValue>;
  chatValue?: Partial<MockChatContextValue>;
}

export const AllProviders = ({ children, authValue, eventsValue, chatValue }: AllProvidersProps) => {
  return (
    <NavigationContainer>
      <MockAuthProvider value={authValue}>
        <MockEventsProvider value={eventsValue}>
          <MockChatProvider value={chatValue}>{children}</MockChatProvider>
        </MockEventsProvider>
      </MockAuthProvider>
    </NavigationContainer>
  );
};

// Custom render function
interface CustomRenderOptions extends Omit<RenderOptions, 'wrapper'> {
  authValue?: Partial<MockAuthContextValue>;
  eventsValue?: Partial<MockEventsContextValue>;
  chatValue?: Partial<MockChatContextValue>;
}

export const customRender = (ui: ReactElement, options: CustomRenderOptions = {}) => {
  const { authValue, eventsValue, chatValue, ...renderOptions } = options;

  const Wrapper = ({ children }: { children: ReactNode }) => (
    <AllProviders authValue={authValue} eventsValue={eventsValue} chatValue={chatValue}>
      {children}
    </AllProviders>
  );

  return render(ui, { wrapper: Wrapper, ...renderOptions });
};

// Re-export everything from testing library
export * from '@testing-library/react-native';

// Override render
export { customRender as render };

// Context hook mocks for when components use real useAuth, useEvents, useChat
export const createMockUseAuth = (overrides: Partial<MockAuthContextValue> = {}) => {
  return () => ({ ...defaultMockAuthContext, ...overrides });
};

export const createMockUseEvents = (overrides: Partial<MockEventsContextValue> = {}) => {
  return () => ({ ...defaultMockEventsContext, ...overrides });
};

export const createMockUseChat = (overrides: Partial<MockChatContextValue> = {}) => {
  return () => ({ ...defaultMockChatContext, ...overrides });
};

// Helper to wait for async operations
export const waitForAsync = () => new Promise((resolve) => setTimeout(resolve, 0));

// Helper to flush promises
export const flushPromises = () => new Promise(setImmediate);

// ============================================================================
// SCREEN-SPECIFIC TEST HELPERS
// ============================================================================

/**
 * Common route params interfaces for type-safe testing
 */
export interface EventDetailsRouteParams {
  eventId: string;
  origin?: 'Events' | 'MyEvents' | 'Messages';
}

export interface ChatThreadRouteParams {
  conversationId: number;
  eventId?: number;
}

export interface JoinRequestsRouteParams {
  conversationId: number;
  eventId: number;
}

export interface CreateEventRouteParams {
  editEvent?: MockUserEvent;
}

/**
 * Creates props for screen components that receive navigation and route.
 * This is useful for screens rendered by React Navigation.
 *
 * @example
 * ```tsx
 * const props = createScreenProps<EventDetailsRouteParams>({ eventId: '1' });
 * render(<EventDetailsScreen {...props} />);
 *
 * // Verify navigation was called
 * expect(props.navigation.navigate).toHaveBeenCalled();
 * ```
 */
export const createScreenProps = <T extends Record<string, unknown>>(
  params?: T,
  routeName = 'TestScreen'
) => ({
  navigation: mockNavigation as any,
  route: createMockRoute(params, routeName),
});

/**
 * Renders a screen component with navigation props and all providers.
 * Combines createScreenProps with customRender for convenience.
 *
 * @example
 * ```tsx
 * const { getByText, navigation } = renderScreen(
 *   EventDetailsScreen,
 *   { eventId: '1' },
 *   { authValue: { user: mockUsers[0] } }
 * );
 * ```
 */
export const renderScreen = <TParams extends Record<string, unknown>>(
  ScreenComponent: React.ComponentType<any>,
  params?: TParams,
  options: CustomRenderOptions = {}
) => {
  const screenProps = createScreenProps(params);
  const result = customRender(<ScreenComponent {...screenProps} />, options);
  return {
    ...result,
    navigation: mockNavigation,
    route: screenProps.route,
  };
};

// ============================================================================
// WRAPPER COMPONENTS FOR SPECIFIC TEST SCENARIOS
// ============================================================================

/**
 * Wrapper that only provides NavigationContainer.
 * Use for components that only need navigation context.
 */
export const NavigationWrapper = ({ children }: { children: ReactNode }) => (
  <NavigationContainer>{children}</NavigationContainer>
);

/**
 * Wrapper for testing authenticated user scenarios.
 */
export const AuthenticatedWrapper = ({
  children,
  user = mockUsers[0],
}: {
  children: ReactNode;
  user?: MockAuthUser;
}) => (
  <NavigationContainer>
    <MockAuthProvider value={{ user, token: 'mock-token' }}>
      <MockEventsProvider>
        <MockChatProvider>{children}</MockChatProvider>
      </MockEventsProvider>
    </MockAuthProvider>
  </NavigationContainer>
);

/**
 * Wrapper for testing guest (unauthenticated) user scenarios.
 */
export const GuestWrapper = ({ children }: { children: ReactNode }) => (
  <NavigationContainer>
    <MockAuthProvider value={{ user: null, token: null }}>
      <MockEventsProvider>
        <MockChatProvider>{children}</MockChatProvider>
      </MockEventsProvider>
    </MockAuthProvider>
  </NavigationContainer>
);

/**
 * Wrapper for testing loading states.
 */
export const LoadingWrapper = ({ children }: { children: ReactNode }) => (
  <NavigationContainer>
    <MockAuthProvider>
      <MockEventsProvider value={{ isLoading: true }}>
        <MockChatProvider value={{ isConnecting: true }}>{children}</MockChatProvider>
      </MockEventsProvider>
    </MockAuthProvider>
  </NavigationContainer>
);

/**
 * Wrapper for testing error states.
 */
export const ErrorWrapper = ({
  children,
  eventsError = 'Failed to load events',
  chatError = 'Failed to connect',
}: {
  children: ReactNode;
  eventsError?: string;
  chatError?: string;
}) => (
  <NavigationContainer>
    <MockAuthProvider>
      <MockEventsProvider value={{ error: eventsError }}>
        <MockChatProvider value={{ error: chatError }}>{children}</MockChatProvider>
      </MockEventsProvider>
    </MockAuthProvider>
  </NavigationContainer>
);

/**
 * Wrapper for testing empty states (no events, no conversations).
 */
export const EmptyStateWrapper = ({ children }: { children: ReactNode }) => (
  <NavigationContainer>
    <MockAuthProvider>
      <MockEventsProvider value={{ events: [], userEvents: [], requestedEvents: [] }}>
        <MockChatProvider value={{ conversations: [], messages: [] }}>
          {children}
        </MockChatProvider>
      </MockEventsProvider>
    </MockAuthProvider>
  </NavigationContainer>
);

// ============================================================================
// TESTING HELPERS
// ============================================================================

/**
 * Simulates a user with incomplete profile (needs onboarding).
 */
export const incompleteProfileUser: MockAuthUser = {
  ...mockUsers[2],
  profileComplete: false,
};

/**
 * Creates a conversation with specific properties for testing.
 */
export const createMockConversation = (
  overrides: Partial<ChatConversation> = {}
): ChatConversation => ({
  id: Date.now(),
  createdBy: 1,
  title: 'Test Conversation',
  memberIds: [1, 2],
  participants: [
    { id: 1, name: 'User 1' },
    { id: 2, name: 'User 2' },
  ],
  displayName: 'Test Conversation',
  unreadCount: 0,
  eventId: null,
  ...overrides,
});

/**
 * Creates a message with specific properties for testing.
 */
export const createMockMessage = (
  overrides: Partial<ChatMessage> = {}
): ChatMessage => ({
  id: `msg-${Date.now()}`,
  conversationId: 1,
  senderId: 1,
  body: 'Test message',
  createdAt: new Date().toISOString(),
  ...overrides,
});

/**
 * Creates a join request with specific properties for testing.
 */
export const createMockJoinRequest = (
  overrides: Partial<ChatJoinRequest> = {}
): ChatJoinRequest => ({
  id: Date.now(),
  eventId: 1,
  userId: 3,
  message: 'I would like to join!',
  status: 'pending',
  createdAt: new Date().toISOString(),
  requester: { id: 3, name: 'New User' },
  ...overrides,
});

/**
 * Waits for all timers and promises to complete.
 * Useful for testing async effects.
 */
export const waitForEffects = async () => {
  await waitForAsync();
  await flushPromises();
  jest.runAllTimers?.();
};

/**
 * Creates a deferred promise for testing async flows.
 * Returns a promise and functions to resolve/reject it.
 *
 * @example
 * ```tsx
 * const { promise, resolve, reject } = createDeferredPromise<string>();
 * mockAuthValues.signInWithGoogle.mockReturnValue(promise);
 *
 * // Trigger the action
 * fireEvent.press(signInButton);
 *
 * // Verify loading state
 * expect(getByText('Signing in...')).toBeTruthy();
 *
 * // Resolve and verify success state
 * resolve('success');
 * await waitFor(() => expect(getByText('Welcome!')).toBeTruthy());
 * ```
 */
export function createDeferredPromise<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}
