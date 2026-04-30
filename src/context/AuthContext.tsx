import {
  createContext,
  ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import { API_BASE_URL } from "@api/config";
import { resetToLogin } from "@navigation/navigationRef";

import * as SecureStore from "expo-secure-store";
import { GoogleSignin } from "@react-native-google-signin/google-signin";
import { GOOGLE_WEB_CLIENT_ID, GOOGLE_IOS_CLIENT_ID } from "@constants/google";

type AuthProviderName = "google" | "apple";

type AuthUser = {
  id: number;
  name: string;
  email: string;
  gender?: "Female" | "Male";
  age?: number;
  avatar?: string;
  profileComplete: boolean;
};

type ProfileUpdateData = {
  name: string;
  gender: "Female" | "Male";
  age: number;
  avatar?: string;
};

export type ApiError = Error & { status?: number };

type AuthFetchOptions = {
  includeAuth?: boolean;
  retryOnUnauthorized?: boolean;
};

interface AuthContextValue {
  user: AuthUser | null;
  token: string | null;
  isSigningIn: boolean;
  signInWithGoogle: (idToken: string) => Promise<AuthUser>;
  signInWithApple: (idToken: string) => Promise<AuthUser>;
  signOut: () => void;
  refreshSessionSilently: () => Promise<string | null>;
  updateProfile: (data: ProfileUpdateData) => Promise<AuthUser>;
  deleteAccount: () => Promise<void>;
  handleSessionExpired: () => void;
  authFetch: (
    input: RequestInfo | URL,
    init?: RequestInit,
    options?: AuthFetchOptions,
  ) => Promise<Response>;
}

const AuthContext = createContext<AuthContextValue | undefined>(undefined);

const TOKEN_KEY = "whoelseisfree.authToken";
const USER_KEY = "whoelseisfree.authUser";
const AUTH_PROVIDER_KEY = "whoelseisfree.authProvider";

export const AuthProvider = ({ children }: { children: ReactNode }) => {
  const [user, setUser] = useState<AuthUser | null>(null);
  const [token, setToken] = useState<string | null>(null);
  const [authProvider, setAuthProvider] = useState<AuthProviderName | null>(
    null,
  );
  const [isSigningIn, setIsSigningIn] = useState(false);
  const refreshInFlightRef = useRef<Promise<string | null> | null>(null);

  useEffect(() => {
    const configureGoogleSignIn = async () => {
      try {
        await GoogleSignin.configure({
          webClientId: GOOGLE_WEB_CLIENT_ID,
          iosClientId: GOOGLE_IOS_CLIENT_ID,
          offlineAccess: true,
        });
      } catch (err) {
        console.warn(
          "Google Sign-In native module unavailable. Silent auth disabled.",
          err,
        );
      }
    };
    configureGoogleSignIn().catch((err) => {
      console.warn("Failed to configure Google Sign-In", err);
    });
  }, []);

  useEffect(() => {
    let isActive = true;
    const restoreSession = async () => {
      try {
        const [storedToken, storedUser, storedProvider] = await Promise.all([
          SecureStore.getItemAsync(TOKEN_KEY),
          SecureStore.getItemAsync(USER_KEY),
          SecureStore.getItemAsync(AUTH_PROVIDER_KEY),
        ]);

        if (!isActive) {
          return;
        }

        if (storedToken && storedUser) {
          setToken(storedToken);
          if (storedProvider === "google" || storedProvider === "apple") {
            setAuthProvider(storedProvider);
          } else {
            // Backward compatibility for existing Google-only sessions.
            setAuthProvider("google");
          }

          try {
            const parsedUser = JSON.parse(storedUser) as AuthUser;
            setUser(parsedUser);
          } catch (err) {
            console.warn("Failed to parse stored user profile", err);
            setUser(null);
          }
        }
      } catch (err) {
        console.warn("Failed to restore auth session", err);
      }
    };

    restoreSession();

    return () => {
      isActive = false;
    };
  }, []);

  const signInWithProvider = useCallback(
    async (
      provider: AuthProviderName,
      endpoint: "google-login" | "apple-login",
      idToken: string,
      unauthorizedMessage: string,
    ) => {
      setIsSigningIn(true);
      try {
        const response = await fetch(`${API_BASE_URL}/api/${endpoint}`, {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
          },
          body: JSON.stringify({ id_token: idToken }),
        });

        if (!response.ok) {
          const message =
            response.status === 401
              ? unauthorizedMessage
              : `Unable to sign in. Server responded with status ${response.status}.`;
          throw new Error(message);
        }

        const payload = (await response.json()) as {
          user: {
            id: number;
            name: string;
            email: string;
            gender?: "Female" | "Male";
            age?: number;
            avatar?: string;
            profile_complete: boolean;
          };
          token: string;
        };

        const authUser: AuthUser = {
          id: payload.user.id,
          name: payload.user.name,
          email: payload.user.email,
          gender: payload.user.gender,
          age: payload.user.age,
          avatar: payload.user.avatar,
          profileComplete: payload.user.profile_complete,
        };

        setUser(authUser);
        setToken(payload.token);
        setAuthProvider(provider);

        await Promise.all([
          SecureStore.setItemAsync(TOKEN_KEY, payload.token),
          SecureStore.setItemAsync(USER_KEY, JSON.stringify(authUser)),
          SecureStore.setItemAsync(AUTH_PROVIDER_KEY, provider),
        ]);

        return authUser;
      } catch (error) {
        if (error instanceof Error) {
          throw error;
        }
        throw new Error("Unable to sign in. Please try again.");
      } finally {
        setIsSigningIn(false);
      }
    },
    [],
  );

  const signInWithGoogle = useCallback(
    async (idToken: string) =>
      signInWithProvider(
        "google",
        "google-login",
        idToken,
        "Unable to sign in with Google.",
      ),
    [signInWithProvider],
  );

  const signInWithApple = useCallback(
    async (idToken: string) =>
      signInWithProvider(
        "apple",
        "apple-login",
        idToken,
        "Unable to sign in with Apple.",
      ),
    [signInWithProvider],
  );

  const refreshSessionSilently = useCallback(async (): Promise<
    string | null
  > => {
    if (authProvider !== "google") {
      return null;
    }

    if (refreshInFlightRef.current) {
      return refreshInFlightRef.current;
    }

    const refreshPromise = (async (): Promise<string | null> => {
      try {
        const result = await GoogleSignin.signInSilently();
        const idToken = (result as any)?.idToken ?? result?.data?.idToken;
        if (!idToken) {
          return null;
        }
        await signInWithGoogle(idToken);
        const refreshedToken = await SecureStore.getItemAsync(TOKEN_KEY);
        return refreshedToken;
      } catch (err) {
        return null;
      }
    })();

    refreshInFlightRef.current = refreshPromise;
    try {
      return await refreshPromise;
    } finally {
      if (refreshInFlightRef.current === refreshPromise) {
        refreshInFlightRef.current = null;
      }
    }
  }, [authProvider, signInWithGoogle]);

  const clearStoredSession = useCallback(async () => {
    setUser(null);
    setToken(null);
    setAuthProvider(null);

    await Promise.all([
      SecureStore.deleteItemAsync(TOKEN_KEY),
      SecureStore.deleteItemAsync(USER_KEY),
      SecureStore.deleteItemAsync(AUTH_PROVIDER_KEY),
    ]);
  }, []);

  const signOut = useCallback(() => {
    void clearStoredSession();
  }, [clearStoredSession]);

  const handleSessionExpired = useCallback(() => {
    signOut();
    resetToLogin();
  }, [signOut]);

  const authFetch = useCallback(
    async (
      input: RequestInfo | URL,
      init: RequestInit = {},
      options: AuthFetchOptions = {},
    ) => {
      const includeAuth = options.includeAuth ?? true;
      const retryOnUnauthorized = options.retryOnUnauthorized ?? true;
      const baseHeaders = new Headers(init.headers);
      const hasAuthHeader = baseHeaders.has("Authorization");
      const hasAuthToken = Boolean(token) || hasAuthHeader;

      const buildInit = (overrideToken?: string) => {
        const headers = new Headers(baseHeaders);
        if (includeAuth) {
          if (overrideToken) {
            headers.set("Authorization", `Bearer ${overrideToken}`);
          } else if (!hasAuthHeader && token) {
            headers.set("Authorization", `Bearer ${token}`);
          }
        }
        return {
          ...init,
          headers,
        };
      };

      let response = await fetch(input, buildInit());
      if (
        response.status === 401 &&
        includeAuth &&
        retryOnUnauthorized &&
        hasAuthToken
      ) {
        const refreshedToken = await refreshSessionSilently();
        if (refreshedToken) {
          response = await fetch(input, buildInit(refreshedToken));
        }
      }

      if (response.status === 401 && includeAuth && hasAuthToken) {
        handleSessionExpired();
      }

      return response;
    },
    [handleSessionExpired, refreshSessionSilently, token],
  );

  const updateProfile = useCallback(
    async (data: ProfileUpdateData): Promise<AuthUser> => {
      if (!token) {
        throw new Error("Not authenticated");
      }

      const response = await authFetch(`${API_BASE_URL}/api/profile`, {
        method: "PUT",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify(data),
      });

      if (!response.ok) {
        const errorData = (await response.json().catch(() => ({}))) as {
          error?: string;
        };
        const message =
          errorData.error ||
          (response.status === 401
            ? "Session expired. Please sign in again."
            : "Failed to update profile");
        const apiError = new Error(message) as ApiError;
        apiError.status = response.status;
        throw apiError;
      }

      const payload = (await response.json()) as {
        user: {
          id: number;
          name: string;
          email: string;
          gender?: "Female" | "Male";
          age?: number;
          avatar?: string;
          profile_complete: boolean;
        };
      };
      const authUser: AuthUser = {
        id: payload.user.id,
        name: payload.user.name,
        email: payload.user.email,
        gender: payload.user.gender,
        age: payload.user.age,
        avatar: payload.user.avatar,
        profileComplete: payload.user.profile_complete,
      };
      setUser(authUser);

      await SecureStore.setItemAsync(USER_KEY, JSON.stringify(authUser));
      return authUser;
    },
    [authFetch, token],
  );

  const deleteAccount = useCallback(async (): Promise<void> => {
    if (!token) {
      throw new Error("Not authenticated");
    }

    const response = await authFetch(`${API_BASE_URL}/api/profile`, {
      method: "DELETE",
    });

    if (!response.ok) {
      const errorData = (await response.json().catch(() => ({}))) as {
        error?: string;
      };
      const message =
        errorData.error ||
        (response.status === 401
          ? "Session expired. Please sign in again."
          : "Failed to delete account");
      const apiError = new Error(message) as ApiError;
      apiError.status = response.status;
      throw apiError;
    }

    // Clear local auth only after the server confirms the account was deleted.
    // If the API fails, keeping the session avoids stranding the user locally.
    await clearStoredSession();
  }, [authFetch, clearStoredSession, token]);

  const value = useMemo(
    () => ({
      user,
      token,
      isSigningIn,
      signInWithGoogle,
      signInWithApple,
      signOut,
      refreshSessionSilently,
      updateProfile,
      deleteAccount,
      handleSessionExpired,
      authFetch,
    }),
    [
      user,
      token,
      isSigningIn,
      signInWithGoogle,
      signInWithApple,
      signOut,
      refreshSessionSilently,
      updateProfile,
      deleteAccount,
      handleSessionExpired,
      authFetch,
    ],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
};

export const useAuth = () => {
  const context = useContext(AuthContext);
  if (!context) {
    throw new Error("useAuth must be used within an AuthProvider");
  }

  return context;
};
