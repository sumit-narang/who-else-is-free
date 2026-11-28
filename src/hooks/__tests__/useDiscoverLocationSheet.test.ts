import { Linking } from 'react-native';

import { act, renderHook } from '@testing-library/react-native';

import { useDiscoverLocationSheet } from '../useDiscoverLocationSheet';

import type { DiscoverLocationMode } from '../useDiscoverLocation';

type Permission = 'granted' | 'denied' | 'undetermined' | null;

const setup = ({
  mode = 'approximate' as DiscoverLocationMode,
  permission = 'denied' as Permission,
  isPromptBlocked = false,
}: { mode?: DiscoverLocationMode; permission?: Permission; isPromptBlocked?: boolean } = {}) => {
  const setNearbyEnabled = jest.fn();
  const requestPermission = jest.fn().mockResolvedValue(undefined);
  const hook = renderHook(() =>
    useDiscoverLocationSheet({
      discoverLocation: { mode, setNearbyEnabled },
      viewerLocation: { permission, isPromptBlocked, requestPermission },
    }),
  );
  return { ...hook, setNearbyEnabled, requestPermission };
};

describe('useDiscoverLocationSheet', () => {
  let openSettings: jest.SpyInstance;

  beforeEach(() => {
    openSettings = jest.spyOn(Linking, 'openSettings').mockResolvedValue(undefined);
  });

  afterEach(() => {
    openSettings.mockRestore();
  });

  it('is active only in precise mode', () => {
    expect(setup({ mode: 'precise', permission: 'granted' }).result.current.isNearbyActive).toBe(
      true,
    );
    expect(setup({ mode: 'approximate' }).result.current.isNearbyActive).toBe(false);
    expect(setup({ mode: 'none' }).result.current.isNearbyActive).toBe(false);
  });

  it('switches Nearby off when it is on, without a sheet', () => {
    const { result, setNearbyEnabled } = setup({ mode: 'precise', permission: 'granted' });

    act(() => result.current.onChipPress());

    expect(setNearbyEnabled).toHaveBeenCalledWith(false);
    expect(result.current.sheetProps.visible).toBe(false);
  });

  it('switches Nearby back on when permission is granted, without a sheet', () => {
    const { result, setNearbyEnabled } = setup({ mode: 'approximate', permission: 'granted' });

    act(() => result.current.onChipPress());

    expect(setNearbyEnabled).toHaveBeenCalledWith(true);
    expect(result.current.sheetProps.visible).toBe(false);
  });

  it('explains, then asks the system only once the sheet has closed', () => {
    const { result, requestPermission, setNearbyEnabled } = setup();

    act(() => result.current.onChipPress());
    expect(result.current.sheetProps.visible).toBe(true);
    expect(result.current.sheetProps.variant).toBe('permission');
    expect(setNearbyEnabled).not.toHaveBeenCalled();

    act(() => result.current.sheetProps.onConfirm());
    expect(result.current.sheetProps.visible).toBe(false);
    expect(requestPermission).not.toHaveBeenCalled();

    act(() => result.current.sheetProps.onClosed());
    expect(requestPermission).toHaveBeenCalledTimes(1);
    expect(openSettings).not.toHaveBeenCalled();
  });

  it('asks the same way when no location is available at all', () => {
    const { result, requestPermission } = setup({ mode: 'none', permission: 'undetermined' });

    act(() => result.current.onChipPress());
    expect(result.current.sheetProps.variant).toBe('permission');
    act(() => result.current.sheetProps.onConfirm());
    act(() => result.current.sheetProps.onClosed());

    expect(requestPermission).toHaveBeenCalledTimes(1);
  });

  it('does nothing when the sheet is closed', () => {
    const { result, requestPermission } = setup();

    act(() => result.current.onChipPress());
    act(() => result.current.sheetProps.onClose());
    act(() => result.current.sheetProps.onClosed());

    expect(requestPermission).not.toHaveBeenCalled();
    expect(openSettings).not.toHaveBeenCalled();
  });

  it('shows the Settings variant after a refusal and opens Settings once closed', () => {
    const { result, requestPermission } = setup({ isPromptBlocked: true });

    act(() => result.current.onChipPress());
    expect(result.current.sheetProps.visible).toBe(true);
    expect(result.current.sheetProps.variant).toBe('settings');

    act(() => result.current.sheetProps.onConfirm());
    expect(openSettings).not.toHaveBeenCalled();
    act(() => result.current.sheetProps.onClosed());

    expect(openSettings).toHaveBeenCalledTimes(1);
    expect(requestPermission).not.toHaveBeenCalled();
  });
});
