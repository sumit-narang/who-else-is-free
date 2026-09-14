import React, { useCallback, useState } from 'react';
import { ActivityIndicator, StyleSheet, Text, View } from 'react-native';
import { useNavigation } from '@react-navigation/native';
import { NativeStackNavigationProp } from '@react-navigation/native-stack';

import { AppButton } from '@components/ui';
import { useAuth } from '@context/AuthContext';
import { RootStackParamList } from '@navigation/types';
import { logger } from '@services/logger';
import { colors, spacing, typography } from '@theme/index';

/**
 * DEV ONLY. Bypasses Google/Apple sign-in by calling /api/dev-login, which is
 * registered on the backend only when DEV_LOGIN_ENABLED=1. Renders nothing in
 * release builds: the default export short-circuits to a no-op render when
 * __DEV__ is false.
 *
 * The preset users keep stable identities across sessions so events /
 * conversations accumulate reproducibly. `tester` is the canonical
 * single-user test identity; `host` and `member2` exist for cross-user
 * verification (host-vs-member flows, group membership, 1:1 conversations).
 */
export type DevLoginUser = {
  email: string;
  name: string;
  label: string;
  testID: string;
  /** When true, signs in with an incomplete profile so the app routes to Onboarding. */
  skipProfileCompletion?: boolean;
};

export const PRESET_DEV_USERS: DevLoginUser[] = [
  { email: 'tester@who-else-is-free.test', name: 'Tester', label: 'Dev Login (tester)', testID: 'dev-login-button' },
  { email: 'host@who-else-is-free.test', name: 'Host', label: 'Dev Login (host)', testID: 'dev-login-button-host' },
  { email: 'member2@who-else-is-free.test', name: 'Member2', label: 'Dev Login (member2)', testID: 'dev-login-button-member2' },
  { email: 'onboarding@who-else-is-free.test', name: 'Onboard Tester', label: 'Dev Login (onboarding)', testID: 'dev-login-button-onboarding', skipProfileCompletion: true },
];

const DevLoginButtonImpl: React.FC = () => {
  const navigation = useNavigation<NativeStackNavigationProp<RootStackParamList>>();
  const { signInWithDevUser, isSigningIn } = useAuth();
  const [error, setError] = useState<string | null>(null);
  const [pendingEmail, setPendingEmail] = useState<string | null>(null);

  const handleDevLogin = useCallback(
    async (user: DevLoginUser) => {
      setError(null);
      setPendingEmail(user.email);
      try {
        const signedIn = await signInWithDevUser(user.email, user.name, {
          profileComplete: !user.skipProfileCompletion,
        });
        navigation.reset({
          index: 0,
          routes: [{ name: signedIn.profileComplete ? 'Main' : 'Onboarding' }],
        });
      } catch (e) {
        logger.warn('Dev login failed', e);
        setError(
          e instanceof Error
            ? e.message
            : 'Dev login failed. Is DEV_LOGIN_ENABLED=1 set on the backend?',
        );
      } finally {
        setPendingEmail(null);
      }
    },
    [signInWithDevUser, navigation],
  );

  return (
    <View style={styles.container}>
      {PRESET_DEV_USERS.map((user) => {
        const isPending = pendingEmail === user.email;
        return (
          <AppButton
            key={user.email}
            label={isPending ? 'Dev signing in…' : user.label}
            icon={isPending ? <ActivityIndicator color={colors.buttonText} size="small" /> : undefined}
            onPress={() => handleDevLogin(user)}
            disabled={isSigningIn}
            variant="secondary"
            testID={user.testID}
          />
        );
      })}
      {error ? (
        <View style={styles.errorWrap}>
          <Text style={styles.errorText}>{error}</Text>
        </View>
      ) : null}
    </View>
  );
};

const styles = StyleSheet.create({
  container: {
    gap: spacing.xs,
    marginTop: spacing.sm,
  },
  errorWrap: {
    paddingHorizontal: spacing.sm,
  },
  errorText: {
    color: colors.error,
    fontSize: 12,
    fontFamily: typography.fontFamilyRegular,
    textAlign: 'center',
  },
});

/**
 * __DEV__ gate: in release builds this entire export becomes a no-op render
 * so the dev-login buttons can never appear to end users, regardless of whether
 * the backend happens to have the route enabled.
 */
const DevLoginButton: React.FC = __DEV__ ? DevLoginButtonImpl : () => null;

export default DevLoginButton;
