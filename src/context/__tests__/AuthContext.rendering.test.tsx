/**
 * AuthContext Rendering Tests
 * Tests AuthProvider with actual component rendering using @testing-library/react-native
 */

import React from 'react';
import { Text, Button, View } from 'react-native';
import { render, screen, waitFor, act, fireEvent } from '@testing-library/react-native';
import fetchMock from 'jest-fetch-mock';

import { AuthProvider, useAuth } from '../AuthContext';
import { mockSecureStore, mockGoogleSignIn } from '../../__tests__/mocks/mockModules';
import { mockApiResponses, mockUsers } from '../../__tests__/mocks/mockData';

// Test constants
const TOKEN_KEY = 'whoelseisfree.authToken';
const USER_KEY = 'whoelseisfree.authUser';
const AUTH_PROVIDER_KEY = 'whoelseisfree.authProvider';
const MOCK_TOKEN = 'mock-jwt-token';
const MOCK_ID_TOKEN = 'mock-google-id-token';

// Test component that consumes the AuthContext
const TestConsumer = () => {
  const { user, token, isSigningIn, signInWithGoogle, signOut, updateProfile, deleteAccount, authFetch } = useAuth();
  const [authFetchStatus, setAuthFetchStatus] = React.useState('idle');

  const handleSignIn = async () => {
    try {
      await signInWithGoogle(MOCK_ID_TOKEN);
    } catch {
      // Error handled silently for test purposes
    }
  };

  const handleUpdateProfile = async () => {
    try {
      await updateProfile({ name: 'Updated Name', gender: 'Female', age: 26 });
    } catch {
      // Error handled silently for test purposes
    }
  };

  const handleDeleteAccount = async () => {
    try {
      await deleteAccount();
    } catch {
      // Error handled silently for test purposes
    }
  };

  const handleConcurrentAuthFetch = async () => {
    setAuthFetchStatus('pending');
    try {
      const [first, second] = await Promise.all([
        authFetch('http://localhost:8080/api/chat/requests/me'),
        authFetch('http://localhost:8080/api/conversations'),
      ]);
      setAuthFetchStatus(`${first.status}-${second.status}`);
    } catch {
      setAuthFetchStatus('error');
    }
  };

  return (
    <View>
      <Text testID="user-name">{user?.name || 'No User'}</Text>
      <Text testID="user-email">{user?.email || 'No Email'}</Text>
      <Text testID="user-profile-complete">{user?.profileComplete ? 'Complete' : 'Incomplete'}</Text>
      <Text testID="token">{token || 'No Token'}</Text>
      <Text testID="is-signing-in">{isSigningIn ? 'Signing In' : 'Not Signing In'}</Text>
      <Text testID="auth-fetch-status">{authFetchStatus}</Text>
      <Button testID="sign-in-button" title="Sign In" onPress={handleSignIn} />
      <Button testID="sign-out-button" title="Sign Out" onPress={signOut} />
      <Button testID="update-profile-button" title="Update Profile" onPress={handleUpdateProfile} />
      <Button testID="delete-account-button" title="Delete Account" onPress={handleDeleteAccount} />
      <Button testID="auth-fetch-button" title="Auth Fetch" onPress={handleConcurrentAuthFetch} />
    </View>
  );
};

// Helper to render with AuthProvider
const renderWithAuthProvider = () => {
  return render(
    <AuthProvider>
      <TestConsumer />
    </AuthProvider>
  );
};

describe('AuthContext Rendering Tests', () => {
  beforeEach(() => {
    fetchMock.resetMocks();
    mockSecureStore.reset();
    mockGoogleSignIn.GoogleSignin.configure.mockClear();
    mockGoogleSignIn.GoogleSignin.signIn.mockClear();
    mockGoogleSignIn.GoogleSignin.signInSilently.mockClear();
    mockGoogleSignIn.GoogleSignin.signOut.mockClear();
    jest.clearAllTimers();
  });

  describe('Session Restore from SecureStore', () => {
    it('should render with no user when SecureStore is empty', async () => {
      renderWithAuthProvider();

      await waitFor(() => {
        expect(screen.getByTestId('user-name')).toHaveTextContent('No User');
        expect(screen.getByTestId('token')).toHaveTextContent('No Token');
      });
    });

    it('should restore user session from SecureStore on mount', async () => {
      const storedUser = mockUsers[0];
      mockSecureStore.storage.set(TOKEN_KEY, MOCK_TOKEN);
      mockSecureStore.storage.set(USER_KEY, JSON.stringify(storedUser));

      renderWithAuthProvider();

      await waitFor(() => {
        expect(screen.getByTestId('user-name')).toHaveTextContent('Ava Test');
        expect(screen.getByTestId('user-email')).toHaveTextContent('ava@example.com');
        expect(screen.getByTestId('token')).toHaveTextContent(MOCK_TOKEN);
      });
    });

    it('should handle corrupted user JSON gracefully', async () => {
      mockSecureStore.storage.set(TOKEN_KEY, MOCK_TOKEN);
      mockSecureStore.storage.set(USER_KEY, 'invalid-json-{');

      renderWithAuthProvider();

      await waitFor(() => {
        expect(screen.getByTestId('user-name')).toHaveTextContent('No User');
        expect(screen.getByTestId('token')).toHaveTextContent(MOCK_TOKEN);
      });
    });

    it('should not restore session when only token exists without user', async () => {
      mockSecureStore.storage.set(TOKEN_KEY, MOCK_TOKEN);

      renderWithAuthProvider();

      await waitFor(() => {
        expect(screen.getByTestId('user-name')).toHaveTextContent('No User');
        expect(screen.getByTestId('token')).toHaveTextContent('No Token');
      });
    });
  });

  describe('signInWithGoogle Flow', () => {
    it('should update user and token after successful sign-in', async () => {
      fetchMock.mockResponseOnce(JSON.stringify(mockApiResponses.googleLogin.success));

      renderWithAuthProvider();

      await act(async () => {
        fireEvent.press(screen.getByTestId('sign-in-button'));
      });

      await waitFor(() => {
        expect(screen.getByTestId('user-name')).toHaveTextContent('Ava Test');
        expect(screen.getByTestId('user-email')).toHaveTextContent('ava@example.com');
        expect(screen.getByTestId('token')).toHaveTextContent('mock-jwt-token');
      });
    });

    it('should persist session to SecureStore after sign-in', async () => {
      fetchMock.mockResponseOnce(JSON.stringify(mockApiResponses.googleLogin.success));

      renderWithAuthProvider();

      await act(async () => {
        fireEvent.press(screen.getByTestId('sign-in-button'));
      });

      await waitFor(() => {
        expect(mockSecureStore.setItemAsync).toHaveBeenCalledWith(TOKEN_KEY, 'mock-jwt-token');
        expect(mockSecureStore.setItemAsync).toHaveBeenCalledWith(
          USER_KEY,
          expect.stringContaining('Ava Test')
        );
      });
    });

    it('should handle new user with profile_complete=false', async () => {
      fetchMock.mockResponseOnce(JSON.stringify(mockApiResponses.googleLogin.newUser));

      renderWithAuthProvider();

      await act(async () => {
        fireEvent.press(screen.getByTestId('sign-in-button'));
      });

      await waitFor(() => {
        expect(screen.getByTestId('user-name')).toHaveTextContent('New User');
        expect(screen.getByTestId('user-profile-complete')).toHaveTextContent('Incomplete');
      });
    });

    it('should keep user state unchanged on 401 error', async () => {
      fetchMock.mockResponseOnce('', { status: 401 });

      renderWithAuthProvider();

      await act(async () => {
        fireEvent.press(screen.getByTestId('sign-in-button'));
      });

      await waitFor(() => {
        expect(screen.getByTestId('user-name')).toHaveTextContent('No User');
        expect(screen.getByTestId('token')).toHaveTextContent('No Token');
      });
    });

    it('should keep user state unchanged on server error', async () => {
      fetchMock.mockResponseOnce('', { status: 500 });

      renderWithAuthProvider();

      await act(async () => {
        fireEvent.press(screen.getByTestId('sign-in-button'));
      });

      await waitFor(() => {
        expect(screen.getByTestId('user-name')).toHaveTextContent('No User');
      });
    });
  });

  describe('Loading States', () => {
    it('should set isSigningIn to true during sign-in process', async () => {
      let resolvePromise: (value: Response) => void;
      const pendingPromise = new Promise<Response>((resolve) => {
        resolvePromise = resolve;
      });
      fetchMock.mockImplementationOnce(() => pendingPromise);

      renderWithAuthProvider();

      await act(async () => {
        fireEvent.press(screen.getByTestId('sign-in-button'));
      });

      expect(screen.getByTestId('is-signing-in')).toHaveTextContent('Signing In');

      await act(async () => {
        resolvePromise!(new Response(JSON.stringify(mockApiResponses.googleLogin.success)));
      });

      await waitFor(() => {
        expect(screen.getByTestId('is-signing-in')).toHaveTextContent('Not Signing In');
      });
    });

    it('should set isSigningIn to false after sign-in failure', async () => {
      fetchMock.mockRejectOnce(new Error('Network error'));

      renderWithAuthProvider();

      await act(async () => {
        fireEvent.press(screen.getByTestId('sign-in-button'));
      });

      await waitFor(() => {
        expect(screen.getByTestId('is-signing-in')).toHaveTextContent('Not Signing In');
      });
    });
  });

  describe('signOut Flow', () => {
    it('should clear user and token on sign-out', async () => {
      const storedUser = mockUsers[0];
      mockSecureStore.storage.set(TOKEN_KEY, MOCK_TOKEN);
      mockSecureStore.storage.set(USER_KEY, JSON.stringify(storedUser));

      renderWithAuthProvider();

      await waitFor(() => {
        expect(screen.getByTestId('user-name')).toHaveTextContent('Ava Test');
      });

      await act(async () => {
        fireEvent.press(screen.getByTestId('sign-out-button'));
      });

      await waitFor(() => {
        expect(screen.getByTestId('user-name')).toHaveTextContent('No User');
        expect(screen.getByTestId('token')).toHaveTextContent('No Token');
      });
    });

    it('should call SecureStore.deleteItemAsync on sign-out', async () => {
      const storedUser = mockUsers[0];
      mockSecureStore.storage.set(TOKEN_KEY, MOCK_TOKEN);
      mockSecureStore.storage.set(USER_KEY, JSON.stringify(storedUser));

      renderWithAuthProvider();

      await waitFor(() => {
        expect(screen.getByTestId('user-name')).toHaveTextContent('Ava Test');
      });

      await act(async () => {
        fireEvent.press(screen.getByTestId('sign-out-button'));
      });

      await waitFor(() => {
        expect(mockSecureStore.deleteItemAsync).toHaveBeenCalledWith(TOKEN_KEY);
        expect(mockSecureStore.deleteItemAsync).toHaveBeenCalledWith(USER_KEY);
      });
    });
  });

  describe('updateProfile Flow', () => {
    it('should update user after successful profile update', async () => {
      const storedUser = mockUsers[0];
      mockSecureStore.storage.set(TOKEN_KEY, MOCK_TOKEN);
      mockSecureStore.storage.set(USER_KEY, JSON.stringify(storedUser));
      fetchMock.mockResponseOnce(JSON.stringify(mockApiResponses.profile.success));

      renderWithAuthProvider();

      await waitFor(() => {
        expect(screen.getByTestId('user-name')).toHaveTextContent('Ava Test');
      });

      await act(async () => {
        fireEvent.press(screen.getByTestId('update-profile-button'));
      });

      await waitFor(() => {
        expect(screen.getByTestId('user-name')).toHaveTextContent('Updated Name');
      });
    });

    it('should persist updated user to SecureStore', async () => {
      const storedUser = mockUsers[0];
      mockSecureStore.storage.set(TOKEN_KEY, MOCK_TOKEN);
      mockSecureStore.storage.set(USER_KEY, JSON.stringify(storedUser));
      fetchMock.mockResponseOnce(JSON.stringify(mockApiResponses.profile.success));

      renderWithAuthProvider();

      await waitFor(() => {
        expect(screen.getByTestId('user-name')).toHaveTextContent('Ava Test');
      });

      mockSecureStore.setItemAsync.mockClear();

      await act(async () => {
        fireEvent.press(screen.getByTestId('update-profile-button'));
      });

      await waitFor(() => {
        expect(mockSecureStore.setItemAsync).toHaveBeenCalledWith(
          USER_KEY,
          expect.stringContaining('Updated Name')
        );
      });
    });
  });

  describe('deleteAccount Flow', () => {
    it('should call delete profile API and clear local session after success', async () => {
      const storedUser = mockUsers[0];
      mockSecureStore.storage.set(TOKEN_KEY, MOCK_TOKEN);
      mockSecureStore.storage.set(USER_KEY, JSON.stringify(storedUser));
      mockSecureStore.storage.set(AUTH_PROVIDER_KEY, 'google');
      fetchMock.mockResponseOnce(JSON.stringify({ message: 'account deleted' }));

      renderWithAuthProvider();

      await waitFor(() => {
        expect(screen.getByTestId('user-name')).toHaveTextContent('Ava Test');
      });

      await act(async () => {
        fireEvent.press(screen.getByTestId('delete-account-button'));
      });

      await waitFor(() => {
        expect(screen.getByTestId('user-name')).toHaveTextContent('No User');
        expect(screen.getByTestId('token')).toHaveTextContent('No Token');
      });

      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining('/api/profile'),
        expect.objectContaining({ method: 'DELETE' })
      );
      expect(mockSecureStore.deleteItemAsync).toHaveBeenCalledWith(TOKEN_KEY);
      expect(mockSecureStore.deleteItemAsync).toHaveBeenCalledWith(USER_KEY);
      expect(mockSecureStore.deleteItemAsync).toHaveBeenCalledWith(AUTH_PROVIDER_KEY);
    });

    it('should keep the current user when account deletion fails', async () => {
      const storedUser = mockUsers[0];
      mockSecureStore.storage.set(TOKEN_KEY, MOCK_TOKEN);
      mockSecureStore.storage.set(USER_KEY, JSON.stringify(storedUser));
      fetchMock.mockResponseOnce(JSON.stringify({ error: 'failed to delete account' }), { status: 500 });

      renderWithAuthProvider();

      await waitFor(() => {
        expect(screen.getByTestId('user-name')).toHaveTextContent('Ava Test');
      });

      await act(async () => {
        fireEvent.press(screen.getByTestId('delete-account-button'));
      });

      await waitFor(() => {
        expect(screen.getByTestId('user-name')).toHaveTextContent('Ava Test');
        expect(screen.getByTestId('token')).toHaveTextContent(MOCK_TOKEN);
      });
    });
  });

  describe('Error Handling', () => {
    it('should not modify user state on profile update error', async () => {
      const storedUser = mockUsers[0];
      mockSecureStore.storage.set(TOKEN_KEY, MOCK_TOKEN);
      mockSecureStore.storage.set(USER_KEY, JSON.stringify(storedUser));
      fetchMock.mockResponseOnce(JSON.stringify({ error: 'Validation failed' }), { status: 400 });

      renderWithAuthProvider();

      await waitFor(() => {
        expect(screen.getByTestId('user-name')).toHaveTextContent('Ava Test');
      });

      await act(async () => {
        fireEvent.press(screen.getByTestId('update-profile-button'));
      });

      await waitFor(() => {
        expect(screen.getByTestId('user-name')).toHaveTextContent('Ava Test');
      });
    });

    it('should handle network errors gracefully during sign-in', async () => {
      fetchMock.mockRejectOnce(new Error('Network error'));

      renderWithAuthProvider();

      await act(async () => {
        fireEvent.press(screen.getByTestId('sign-in-button'));
      });

      await waitFor(() => {
        expect(screen.getByTestId('user-name')).toHaveTextContent('No User');
        expect(screen.getByTestId('is-signing-in')).toHaveTextContent('Not Signing In');
      });
    });
  });

  describe('authFetch concurrency', () => {
    it('should deduplicate silent refresh across concurrent 401 responses', async () => {
      const storedUser = mockUsers[0];
      mockSecureStore.storage.set(TOKEN_KEY, MOCK_TOKEN);
      mockSecureStore.storage.set(USER_KEY, JSON.stringify(storedUser));
      mockGoogleSignIn.GoogleSignin.signInSilently.mockResolvedValueOnce({
        data: { idToken: 'silent-id-token' },
      });

      fetchMock
        .mockResponseOnce('', { status: 401 })
        .mockResponseOnce('', { status: 401 })
        .mockResponseOnce(JSON.stringify({
          user: {
            id: storedUser.id,
            name: storedUser.name,
            email: storedUser.email,
            gender: storedUser.gender,
            age: storedUser.age,
            profile_complete: storedUser.profileComplete,
          },
          token: 'refreshed-jwt-token',
        }))
        .mockResponseOnce('{}', { status: 200 })
        .mockResponseOnce('{}', { status: 200 });

      renderWithAuthProvider();

      await waitFor(() => {
        expect(screen.getByTestId('token')).toHaveTextContent(MOCK_TOKEN);
      });

      await act(async () => {
        fireEvent.press(screen.getByTestId('auth-fetch-button'));
      });

      await waitFor(() => {
        expect(screen.getByTestId('auth-fetch-status')).toHaveTextContent('200-200');
      });

      expect(mockGoogleSignIn.GoogleSignin.signInSilently).toHaveBeenCalledTimes(1);
      const googleLoginCalls = fetchMock.mock.calls.filter(([url]) =>
        String(url).includes('/api/google-login')
      );
      expect(googleLoginCalls).toHaveLength(1);
    });
  });

  describe('useAuth Hook', () => {
    it('should throw error when used outside AuthProvider', () => {
      const consoleError = console.error;
      console.error = jest.fn();

      expect(() => {
        render(<TestConsumer />);
      }).toThrow('useAuth must be used within an AuthProvider');

      console.error = consoleError;
    });
  });

  describe('Google Sign-In Configuration', () => {
    it('should configure Google Sign-In on mount', async () => {
      renderWithAuthProvider();

      await waitFor(() => {
        expect(mockGoogleSignIn.GoogleSignin.configure).toHaveBeenCalled();
      });
    });
  });
});
