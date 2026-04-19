import {
  Alert,
  Animated,
  Image,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from "react-native";

import { useCallback, useMemo, useRef, useState } from "react";
import {
  CompositeNavigationProp,
  useNavigation,
} from "@react-navigation/native";
import { BottomTabNavigationProp } from "@react-navigation/bottom-tabs";
import { StackNavigationProp } from "@react-navigation/stack";
import { Feather } from "@expo/vector-icons";

import ScreenContainer from "@components/ScreenContainer";
import { colors, spacing, typography } from "@theme/index";
import { useAuth } from "@context/AuthContext";
import { useEvents } from "@context/EventsContext";
import { useChat } from "@context/ChatContext";
import { RootStackParamList, RootTabParamList } from "@navigation/types";
import BottomSheetModal from "@components/BottomSheetModal";
import SignInButtons from "@components/SignInButtons";
import EditProfileIcon from "@assets/account-icons/edit.svg";
import PastEventsIcon from "@assets/account-icons/past event.svg";
import PrivacyPolicyIcon from "@assets/account-icons/privacy.svg";
import HelpIcon from "@assets/account-icons/help.svg";
import LogoutIcon from "@assets/account-icons/logout.svg";
import TrashIcon from "@assets/account-icons/delete.svg";
import * as Haptics from "expo-haptics";


type ProfileNavigation = CompositeNavigationProp<
  BottomTabNavigationProp<RootTabParamList, "Profile">,
  StackNavigationProp<RootStackParamList>
>;

interface MenuItemProps {
  icon: React.ReactNode;
  label: string;
  onPress: () => void;
  showChevron?: boolean;
}

const MenuItem = ({
  icon,
  label,
  onPress,
  showChevron = true,
}: MenuItemProps) => {
  return (
    <Pressable
      style={styles.menuItem}
      onPress={onPress}
      accessibilityRole="button"
    >
      <View style={styles.menuItemLeft}>
        <View style={styles.menuIconContainer}>
          {icon}
        </View>
        <Text style={styles.menuItemText}>{label}</Text>
      </View>
      {showChevron && (
        <Feather name="chevron-right" size={20} color="#808080" />
      )}
    </Pressable>
  );
};

const ProfileScreen = () => {
  const { user, signOut } = useAuth();
  const { events, userEvents } = useEvents();
  const { conversations } = useChat();
  const navigation = useNavigation<ProfileNavigation>();

  // Calculate stats
  const hostedCount = userEvents.length;
  const joinedCount = useMemo(() => {
    if (!user) {
      return 0;
    }

    const activeEventIDs = new Set<number>();
    events.forEach((event) => {
      const eventID = Number(event.id);
      if (Number.isInteger(eventID) && eventID > 0) {
        activeEventIDs.add(eventID);
      }
    });

    const joinedEventIDs = new Set<number>();
    conversations.forEach((conversation) => {
      if (!conversation.eventId || conversation.eventId <= 0) {
        return;
      }
      if (conversation.createdBy === user.id) {
        return;
      }
      if (!activeEventIDs.has(conversation.eventId)) {
        return;
      }
      joinedEventIDs.add(conversation.eventId);
    });

    return joinedEventIDs.size;
  }, [conversations, events, user]);

  const handleSignOut = useCallback(() => {
    signOut();
  }, [signOut]);

  const handleEditProfile = useCallback(() => {
    navigation.navigate("EditProfile");
  }, [navigation]);

  const handlePastEvents = useCallback(() => {
    navigation.navigate("PastEvents");
  }, [navigation]);

  const handlePrivacyPolicy = useCallback(() => {
    navigation.navigate("PrivacyPolicy");
  }, [navigation]);

  const handleHelp = useCallback(() => {
    navigation.navigate("Help");
  }, [navigation]);

  const handleDelete = useCallback(() => {
    Alert.alert("Delete Account", "Coming Soon");
  }, []);

  const scaleAnim = useRef(new Animated.Value(1)).current;

  const handlePressIn = useCallback(() => {
    Animated.timing(scaleAnim, {
      toValue: 0.97,
      duration: 100,
      useNativeDriver: true,
    }).start();
  }, [scaleAnim]);

  const handlePressOut = useCallback(() => {
    Animated.timing(scaleAnim, {
      toValue: 1,
      duration: 100,
      useNativeDriver: true,
    }).start();
  }, [scaleAnim]);

  const [signInVisible, setSignInVisible] = useState(false);

  if (!user) {
    return (
      <ScreenContainer>
        <View style={styles.headerSpacing}>
          <Text style={styles.headerTitle}>Profile</Text>
        </View>
        <ScrollView
          style={styles.scrollView}
          contentContainerStyle={styles.scrollContent}
          showsVerticalScrollIndicator={false}
        >
          <View style={styles.guestCard}>
            <View style={styles.guestAvatar} />
            <Text style={styles.guestTitle}>No profile to show</Text>
            <Text style={styles.guestDescription}>
              Sign in to view your account
            </Text>
            <Pressable
              style={styles.guestButton}
              onPress={() => setSignInVisible(true)}
            >
              <Text style={styles.guestButtonText}>Continue</Text>
            </Pressable>
          </View>
          <View style={styles.menuSection}>
            <MenuItem
              icon={<PrivacyPolicyIcon width={20} height={20} />}
              label="Privacy Policy"
              onPress={handlePrivacyPolicy}
            />
            <MenuItem
              icon={<HelpIcon width={20} height={20} />}
              label="Help"
              onPress={handleHelp}
            />
          </View>
        </ScrollView>
        <BottomSheetModal visible={signInVisible} onClose={() => setSignInVisible(false)}>
          <SignInButtons />
        </BottomSheetModal>
      </ScreenContainer>
    );
  }

  const initial = user?.name?.charAt(0).toUpperCase() ?? "Y";

  return (
    <ScreenContainer>
      <View style={styles.headerSpacing}>
        <Text style={styles.headerTitle}>Account</Text>
      </View>
      <ScrollView
        style={styles.scrollView}
        contentContainerStyle={styles.scrollContent}
        showsVerticalScrollIndicator={false}
      >
        {/* Profile Header Card with Gradient */}
        <Pressable onPressIn={handlePressIn} onPressOut={handlePressOut}>
          <Animated.View style={[styles.headerCard, { transform: [{ scale: scaleAnim }] }]}>
            <View style={styles.headerCardGradient}>
              <View style={styles.headerContent}>
                <View style={styles.avatar}>
                  {user.avatar ? (
                    <Image
                      source={{ uri: `data:image/jpeg;base64,${user.avatar}` }}
                      style={styles.avatarImage}
                    />
                  ) : (
                    <Text style={styles.avatarInitial}>{initial}</Text>
                  )}
                </View>
                <Text style={styles.name}>{user?.name ?? "Your Profile"}</Text>
                <Text style={styles.email}>{user?.email ?? ""}</Text>

                {/* Stats Row */}
                <View style={styles.statsPill}>
                  <View style={styles.statItem}>
                    <Text style={styles.statNumber}>{hostedCount}</Text>
                    <Text style={styles.statLabel}>Hosted</Text>
                  </View>
                  <View style={styles.statDivider} />
                  <View style={styles.statItem}>
                    <Text style={styles.statNumber}>{joinedCount}</Text>
                    <Text style={styles.statLabel}>Joined</Text>
                  </View>
                </View>
              </View>
            </View>
          </Animated.View>
        </Pressable>
        {/* Menu Section */}
        <View style={styles.menuSection}>
          <MenuItem
            icon={<EditProfileIcon width={20} height={20} />}
            label="Edit Profile"
            onPress={handleEditProfile}
          />
          <MenuItem
            icon={<PastEventsIcon width={20} height={20} />}
            label="Past Events"
            onPress={handlePastEvents}
          />
          <MenuItem
            icon={<PrivacyPolicyIcon width={20} height={20} />}
            label="Privacy Policy"
            onPress={handlePrivacyPolicy}
          />
          <MenuItem
            icon={<HelpIcon width={20} height={20} />}
            label="Help"
            onPress={handleHelp}
          />
          <MenuItem
            icon={<LogoutIcon width={20} height={20} />}
            label="Logout"
            onPress={handleSignOut}
            showChevron={false}
          />
          <MenuItem
            icon={<TrashIcon width={20} height={20} />}
            label="Delete"
            onPress={handleDelete}
            showChevron={false}
          />
        </View>
      </ScrollView>
    </ScreenContainer>
  );
};

const styles = StyleSheet.create({
  scrollView: {
    flex: 1,
    marginHorizontal: -spacing.md,
  },
  scrollContent: {
    flexGrow: 1,
    paddingHorizontal: spacing.md,
  },
  headerSpacing: {
    paddingTop: spacing.lg - spacing.md,
    paddingBottom: spacing.md,
  },
  headerTitle: {
    fontSize: typography.header,
    fontFamily: typography.fontFamilySemiBold,
    color: colors.text,
    lineHeight: typography.lineHeight,
    letterSpacing: typography.letterSpacing,
  },
  headerCard: {
    borderRadius: 16,
    borderCurve: "continuous",
    overflow: "hidden",
  },
  headerCardGradient: {
    backgroundColor: "#1B50E3",
  },
  headerContent: {
    alignItems: "center",
    paddingTop: 20,
    paddingBottom: 20,
    paddingHorizontal: spacing.md,
  },
  avatar: {
    width: 68,
    height: 68,
    borderRadius: 34,
    backgroundColor: "rgba(255,255,255,0.2)",
    alignItems: "center",
    justifyContent: "center",
    marginBottom: spacing.md,
    overflow: "hidden",
    borderWidth: 1,
    borderColor: "rgba(255,255,255,0.2)",
  },
  avatarImage: {
    width: 68,
    height: 68,
    borderRadius: 34,
  },
  avatarInitial: {
    fontSize: 28,
    color: "#FFFFFF",
    fontFamily: typography.fontFamilyBold,
  },
  name: {
    fontSize: 20,
    color: "#FFFFFF",
    fontFamily: typography.fontFamilyMedium,
    letterSpacing: -0.5,
    marginBottom: 4,
  },
  email: {
    fontSize: 14,
    color: "rgba(255, 255, 255, 0.5)",
    fontFamily: typography.fontFamilyRegular,
    letterSpacing: -0.5,
    marginBottom: 24,
  },
  statsPill: {
    flexDirection: "row",
    alignItems: "center",
    gap: spacing.xl,
  },
  statDivider: {
    width: 1,
    height: 36,
    backgroundColor: "rgba(255,255,255,0.25)",
  },
  statItem: {
    alignItems: "center",
    gap: 2,
  },
  statNumber: {
    fontSize: 18,
    color: "#FFFFFF",
    fontFamily: typography.fontFamilyMedium,
    letterSpacing: -0.5,
  },
  statLabel: {
    fontSize: 14,
    color: "rgba(255,255,255,0.5)",
    fontFamily: typography.fontFamilyRegular,
  },
  menuSection: {
    paddingTop: 12,
  },
  menuItem: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingVertical: 17,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: "#E6E6E6",
  },
  menuItemLeft: {
    flexDirection: "row",
    alignItems: "center",
    gap: 14,
  },
  menuIconContainer: {
    width: 24,
    height: 24,
    alignItems: "center",
    justifyContent: "center",
  },
  menuItemText: {
    fontSize: 16,
    fontFamily: typography.fontFamilyRegular,
    letterSpacing: -0.4,
    color: "#000000",
  },
  guestCard: {
    backgroundColor: "#F5F5F5",
    borderRadius: 20,
    alignItems: "center",
    paddingVertical: 24,
    paddingHorizontal: spacing.md,
  },
  guestAvatar: {
    width: 46,
    height: 46,
    borderRadius: 23,
    backgroundColor: "#3345E9",
    marginBottom: 12,
  },
  guestTitle: {
    fontSize: 16,
    fontFamily: typography.fontFamilyMedium,
    color: colors.text,
    lineHeight: 20,
    letterSpacing: -0.5,
    textAlign: "center",
    marginBottom: 6,
  },
  guestDescription: {
    fontSize: 15,
    fontFamily: typography.fontFamilyRegular,
    color: "#707070",
    textAlign: "center",
    lineHeight: 20,
    letterSpacing: -0.5,
    marginBottom: 16,
  },
  guestButton: {
    width: 173,
    height: 48,
    borderRadius: 52,
    backgroundColor: colors.buttonBackground,
    alignItems: "center",
    justifyContent: "center",
  },
  guestButtonText: {
    fontSize: 16,
    fontFamily: typography.fontFamilySemiBold,
    color: "#FFFFFF",
  },
});

export default ProfileScreen;
