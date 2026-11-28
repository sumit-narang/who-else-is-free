import { act, renderHook, waitFor } from '@testing-library/react-native';

import {
  NEARBY_OFF_KEY,
  resetApproximateLocationCache,
  useDiscoverLocation,
} from '../useDiscoverLocation';

const mockFetchApproximateLocation = jest.fn();
const mockGetItemAsync = jest.fn();
const mockSetItemAsync = jest.fn();
const mockDeleteItemAsync = jest.fn();

jest.mock('@api/location', () => ({
  fetchApproximateLocation: (...args: unknown[]) => mockFetchApproximateLocation(...args),
}));
jest.mock('expo-secure-store', () => ({
  getItemAsync: (...args: unknown[]) => mockGetItemAsync(...args),
  setItemAsync: (...args: unknown[]) => mockSetItemAsync(...args),
  deleteItemAsync: (...args: unknown[]) => mockDeleteItemAsync(...args),
}));
jest.mock('@services/logger', () => ({ logger: { warn: jest.fn() } }));

const gps = { latitude: 53.34, longitude: -6.26 };
const dublin = { city: 'Dublin', countryCode: 'ie', coords: { latitude: 53.35, longitude: -6.23 } };

type Viewer = {
  coords: typeof gps | null;
  isLoading: boolean;
  permission: 'granted' | 'denied' | 'undetermined' | null;
};

const granted: Viewer = { coords: gps, isLoading: false, permission: 'granted' };
const denied: Viewer = { coords: null, isLoading: false, permission: 'denied' };

const setup = (viewer: Viewer) =>
  renderHook((props: Viewer) => useDiscoverLocation(props), { initialProps: viewer });

describe('useDiscoverLocation', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    resetApproximateLocationCache();
    mockGetItemAsync.mockResolvedValue(null);
    mockSetItemAsync.mockResolvedValue(undefined);
    mockDeleteItemAsync.mockResolvedValue(undefined);
    mockFetchApproximateLocation.mockResolvedValue(dublin);
  });

  it('uses precise location when granted and never asks the server', async () => {
    const { result } = setup(granted);

    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.mode).toBe('precise');
    expect(result.current.coords).toEqual(gps);
    expect(result.current.isNearbyEnabled).toBe(true);
    expect(mockFetchApproximateLocation).not.toHaveBeenCalled();
  });

  it('falls back to the approximate area without device location', async () => {
    const { result } = setup(denied);

    expect(result.current.isLoading).toBe(true);
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.mode).toBe('approximate');
    expect(result.current.coords).toEqual(dublin.coords);
    expect(mockFetchApproximateLocation).toHaveBeenCalledTimes(1);
  });

  it('waits for the device location to settle before looking up the network area', async () => {
    const { result, rerender } = setup({ coords: null, isLoading: true, permission: null });

    await waitFor(() => expect(mockGetItemAsync).toHaveBeenCalled());
    expect(result.current.isLoading).toBe(true);
    expect(mockFetchApproximateLocation).not.toHaveBeenCalled();

    rerender(granted);
    await waitFor(() => expect(result.current.mode).toBe('precise'));
    expect(mockFetchApproximateLocation).not.toHaveBeenCalled();
  });

  it('shows every plan when neither location is available', async () => {
    mockFetchApproximateLocation.mockResolvedValue(null);
    const { result } = setup(denied);

    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.mode).toBe('none');
    expect(result.current.coords).toBeNull();
  });

  it('switching Nearby off uses the approximate area and is saved', async () => {
    const { result } = setup(granted);
    await waitFor(() => expect(result.current.mode).toBe('precise'));

    act(() => result.current.setNearbyEnabled(false));

    await waitFor(() => expect(result.current.mode).toBe('approximate'));
    expect(result.current.coords).toEqual(dublin.coords);
    expect(mockSetItemAsync).toHaveBeenCalledWith(NEARBY_OFF_KEY, '1');

    act(() => result.current.setNearbyEnabled(true));
    expect(result.current.mode).toBe('precise');
    expect(mockDeleteItemAsync).toHaveBeenCalledWith(NEARBY_OFF_KEY);
  });

  it('keeps Nearby off on the next launch although permission is still granted', async () => {
    mockGetItemAsync.mockResolvedValue('1');
    const { result } = setup(granted);

    expect(result.current.isLoading).toBe(true);
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.isNearbyEnabled).toBe(false);
    expect(result.current.mode).toBe('approximate');
  });

  it('switches Nearby on when permission is newly granted', async () => {
    mockGetItemAsync.mockResolvedValue('1');
    const { result, rerender } = setup(denied);
    await waitFor(() => expect(result.current.mode).toBe('approximate'));

    rerender(granted);

    await waitFor(() => expect(result.current.mode).toBe('precise'));
    expect(mockDeleteItemAsync).toHaveBeenCalledWith(NEARBY_OFF_KEY);
  });

  it('lets a switch made while the saved choice loads win over it', async () => {
    let resolveStored: (value: string | null) => void = () => undefined;
    mockGetItemAsync.mockReturnValue(
      new Promise<string | null>((resolve) => {
        resolveStored = resolve;
      }),
    );
    const { result } = setup(granted);

    act(() => result.current.setNearbyEnabled(true));
    await act(async () => resolveStored('1'));

    expect(result.current.isNearbyEnabled).toBe(true);
    expect(result.current.mode).toBe('precise');
  });

  it('retries the lookup after a network failure', async () => {
    mockFetchApproximateLocation.mockRejectedValueOnce(new Error('offline'));
    const { result } = setup(denied);

    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.mode).toBe('none');

    act(() => result.current.retry());
    await waitFor(() => expect(result.current.mode).toBe('approximate'));
    expect(mockFetchApproximateLocation).toHaveBeenCalledTimes(2);
  });

  it('reuses a recent lookup across mounts', async () => {
    const first = setup(denied);
    await waitFor(() => expect(first.result.current.mode).toBe('approximate'));
    first.unmount();

    const second = setup(denied);
    await waitFor(() => expect(second.result.current.mode).toBe('approximate'));
    expect(mockFetchApproximateLocation).toHaveBeenCalledTimes(1);
  });
});
