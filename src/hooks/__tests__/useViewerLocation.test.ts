import { AppState, Platform } from 'react-native';

import { act, renderHook, waitFor } from '@testing-library/react-native';

import { useViewerLocation } from '../useViewerLocation';

const mockGetForegroundPermissionsAsync = jest.fn();
const mockRequestForegroundPermissionsAsync = jest.fn();
const mockGetLastKnownPositionAsync = jest.fn();
const mockGetCurrentPositionAsync = jest.fn();
const mockReverseGeocodeAsync = jest.fn();

jest.mock('expo-location', () => ({
  PermissionStatus: {
    GRANTED: 'granted',
    DENIED: 'denied',
    UNDETERMINED: 'undetermined',
  },
  Accuracy: { Balanced: 3 },
  getForegroundPermissionsAsync: (...args: unknown[]) => mockGetForegroundPermissionsAsync(...args),
  requestForegroundPermissionsAsync: (...args: unknown[]) =>
    mockRequestForegroundPermissionsAsync(...args),
  getLastKnownPositionAsync: (...args: unknown[]) => mockGetLastKnownPositionAsync(...args),
  getCurrentPositionAsync: (...args: unknown[]) => mockGetCurrentPositionAsync(...args),
  reverseGeocodeAsync: (...args: unknown[]) => mockReverseGeocodeAsync(...args),
}));

describe('useViewerLocation', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockGetForegroundPermissionsAsync.mockResolvedValue({ status: 'undetermined' });
    mockRequestForegroundPermissionsAsync.mockResolvedValue({ status: 'granted' });
    mockGetLastKnownPositionAsync.mockResolvedValue({
      coords: { latitude: 53.3498, longitude: -6.2603 },
    });
    mockGetCurrentPositionAsync.mockResolvedValue(null);
    mockReverseGeocodeAsync.mockResolvedValue([{ isoCountryCode: 'IE' }]);
  });

  it('checks silently on mount and requests location only after the explicit action', async () => {
    const { result } = renderHook(() => useViewerLocation());

    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.permission).toBe('undetermined');
    expect(mockRequestForegroundPermissionsAsync).not.toHaveBeenCalled();

    await act(async () => {
      await result.current.requestPermission();
    });

    expect(mockRequestForegroundPermissionsAsync).toHaveBeenCalledTimes(1);
    expect(result.current.permission).toBe('granted');
    expect(result.current.coords).toEqual({ latitude: 53.3498, longitude: -6.2603 });
    expect(result.current.countryCode).toBe('ie');
    expect(mockReverseGeocodeAsync).toHaveBeenCalledWith({ latitude: 53.3498, longitude: -6.2603 });
  });
  it('still prompts when Android reports denied but will show the dialog again', async () => {
    // expo-location records "we have asked before" in SharedPreferences, so after
    // a single denial it reports DENIED rather than UNDETERMINED forever. Android
    // will still show the dialog while canAskAgain is true, so gating on
    // UNDETERMINED alone means the app silently never asks again.
    mockGetForegroundPermissionsAsync.mockResolvedValue({
      status: 'denied',
      granted: false,
      canAskAgain: true,
    });

    const { result } = renderHook(() => useViewerLocation());
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(mockRequestForegroundPermissionsAsync).not.toHaveBeenCalled();

    await act(async () => {
      await result.current.requestPermission();
    });

    expect(mockRequestForegroundPermissionsAsync).toHaveBeenCalledTimes(1);
    expect(result.current.permission).toBe('granted');
  });

  it('still asks when denied with canAskAgain false, which a restored flag fakes', async () => {
    // Android auto-backup restores expo.modules.permissions.asked onto a fresh
    // install, so expo reports DENIED while shouldShowRequestPermissionRationale
    // is false because Android has genuinely never asked. That pair is
    // indistinguishable from a permanent denial, so it cannot be trusted: let
    // the OS decide. A genuinely blocked request is a no-op.
    mockGetForegroundPermissionsAsync.mockResolvedValue({
      status: 'denied',
      granted: false,
      canAskAgain: false,
    });
    mockRequestForegroundPermissionsAsync.mockResolvedValue({
      status: 'denied',
      granted: false,
      canAskAgain: false,
    });

    const { result } = renderHook(() => useViewerLocation());
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(mockRequestForegroundPermissionsAsync).not.toHaveBeenCalled();

    await act(async () => {
      await result.current.requestPermission();
    });

    expect(mockRequestForegroundPermissionsAsync).toHaveBeenCalledTimes(1);
    expect(result.current.permission).toBe('denied');
    expect(result.current.coords).toBeNull();
  });

  it('never asks silently on mount, whatever the reported status', async () => {
    mockGetForegroundPermissionsAsync.mockResolvedValue({
      status: 'denied',
      granted: false,
      canAskAgain: true,
    });

    renderHook(() => useViewerLocation());

    await waitFor(() => expect(mockGetForegroundPermissionsAsync).toHaveBeenCalled());
    expect(mockRequestForegroundPermissionsAsync).not.toHaveBeenCalled();
  });

  it('does not prompt when the permission is already granted', async () => {
    mockGetForegroundPermissionsAsync.mockResolvedValue({
      status: 'granted',
      granted: true,
      canAskAgain: false,
    });

    const { result } = renderHook(() => useViewerLocation());
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    await act(async () => {
      await result.current.requestPermission();
    });

    expect(mockRequestForegroundPermissionsAsync).not.toHaveBeenCalled();
    expect(result.current.permission).toBe('granted');
  });

  describe('isPromptBlocked', () => {
    const originalOS = Platform.OS;
    const blocked = { status: 'denied', granted: false, canAskAgain: false };

    afterEach(() => {
      Platform.OS = originalOS;
    });

    it('trusts iOS without asking', async () => {
      Platform.OS = 'ios';
      mockGetForegroundPermissionsAsync.mockResolvedValue(blocked);

      const { result } = renderHook(() => useViewerLocation());

      await waitFor(() => expect(result.current.isPromptBlocked).toBe(true));
    });

    it('on Android, only after this session has asked', async () => {
      Platform.OS = 'android';
      mockGetForegroundPermissionsAsync.mockResolvedValue(blocked);
      mockRequestForegroundPermissionsAsync.mockResolvedValue(blocked);

      const { result } = renderHook(() => useViewerLocation());
      await waitFor(() => expect(result.current.isLoading).toBe(false));
      // A restored auto-backup record can report this without any real refusal.
      expect(result.current.isPromptBlocked).toBe(false);

      await act(async () => {
        await result.current.requestPermission();
      });

      expect(result.current.isPromptBlocked).toBe(true);
    });
  });

  it('re-checks silently when the app returns, picking up a grant from Settings', async () => {
    const listeners: Array<(state: string) => void> = [];
    const addListener = jest
      .spyOn(AppState, 'addEventListener')
      .mockImplementation((_type, listener) => {
        listeners.push(listener as (state: string) => void);
        return { remove: jest.fn() } as unknown as ReturnType<typeof AppState.addEventListener>;
      });
    mockGetForegroundPermissionsAsync.mockResolvedValue({
      status: 'denied',
      granted: false,
      canAskAgain: false,
    });

    const { result } = renderHook(() => useViewerLocation());
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.coords).toBeNull();

    mockGetForegroundPermissionsAsync.mockResolvedValue({
      status: 'granted',
      granted: true,
      canAskAgain: true,
    });
    await act(async () => {
      listeners.forEach((listener) => listener('active'));
    });

    await waitFor(() =>
      expect(result.current.coords).toEqual({ latitude: 53.3498, longitude: -6.2603 }),
    );
    expect(mockRequestForegroundPermissionsAsync).not.toHaveBeenCalled();
    addListener.mockRestore();
  });

  it('notices location turned off in Settings when the app returns', async () => {
    const listeners: Array<(state: string) => void> = [];
    const addListener = jest
      .spyOn(AppState, 'addEventListener')
      .mockImplementation((_type, listener) => {
        listeners.push(listener as (state: string) => void);
        return { remove: jest.fn() } as unknown as ReturnType<typeof AppState.addEventListener>;
      });
    mockGetForegroundPermissionsAsync.mockResolvedValue({
      status: 'granted',
      granted: true,
      canAskAgain: true,
    });

    const { result } = renderHook(() => useViewerLocation());
    await waitFor(() =>
      expect(result.current.coords).toEqual({ latitude: 53.3498, longitude: -6.2603 }),
    );
    const positionLoads = mockGetLastKnownPositionAsync.mock.calls.length;

    // Still granted: only the status is checked, the position is not reloaded.
    await act(async () => {
      listeners.forEach((listener) => listener('active'));
    });
    expect(result.current.permission).toBe('granted');
    expect(mockGetLastKnownPositionAsync.mock.calls.length).toBe(positionLoads);

    mockGetForegroundPermissionsAsync.mockResolvedValue({
      status: 'denied',
      granted: false,
      canAskAgain: false,
    });
    await act(async () => {
      listeners.forEach((listener) => listener('active'));
    });

    await waitFor(() => expect(result.current.permission).toBe('denied'));
    expect(result.current.coords).toBeNull();
    expect(mockRequestForegroundPermissionsAsync).not.toHaveBeenCalled();
    addListener.mockRestore();
  });
});
