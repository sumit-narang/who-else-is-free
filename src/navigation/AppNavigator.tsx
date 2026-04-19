import { NavigationContainer, DefaultTheme } from "@react-navigation/native";
import { createBottomTabNavigator } from "@react-navigation/bottom-tabs";
import { createNativeStackNavigator } from "@react-navigation/native-stack";
import {
  Animated,
  TouchableOpacity,
  View,
  StyleSheet,
} from "react-native";
import * as Haptics from "expo-haptics";
import { useMemo, useRef } from "react";
import Svg, { Circle, Path } from "react-native-svg";
import { BlurView } from "expo-blur";

import HomeScreen from "@screens/HomeScreen";
import CreateEventScreen from "@screens/CreateEventScreen";
import MyEventsScreen from "@screens/MyEventsScreen";
import MessagesScreen from "@screens/MessagesScreen";
import ChatThreadScreen from "@screens/ChatThreadScreen";
import ProfileScreen from "@screens/ProfileScreen";
import LoginScreen from "@screens/LoginScreen";
import EventDetailsScreen from "@screens/EventDetailsScreen";
import SplashScreen from "@screens/SplashScreen";
import OnboardingScreen from "@screens/OnboardingScreen";
import { navigationRef } from "@navigation/navigationRef";
import { RootStackParamList, RootTabParamList } from "@navigation/types";
import { colors } from "@theme/colors";
import { useChat } from "@context/ChatContext";
import GoogleSignIn from "@screens/GoogleSignIn";
import JoinRequestsScreen from "@screens/JoinRequestsScreen";
import PendingRequestsScreen from "@screens/PendingRequestsScreen";
import EditProfileScreen from "@screens/EditProfileScreen";
import PastEventsScreen from "@screens/PastEventsScreen";
import PrivacyPolicyScreen from "@screens/PrivacyPolicyScreen";
import HelpScreen from "@screens/HelpScreen";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import type { BottomTabBarButtonProps } from "@react-navigation/bottom-tabs";

const Tab = createBottomTabNavigator<RootTabParamList>();
const Stack = createNativeStackNavigator<RootStackParamList>();

const TabBarBackground = () => (
  <View style={tabBarStyles.backgroundContainer}>
    <BlurView
      intensity={54}
      tint="light"
      style={StyleSheet.absoluteFill}
    />
    {/* Fill: #FBFBFB at 60% opacity */}
    <View style={tabBarStyles.frostedOverlay} />
    {/* Stroke on top: 1px solid white */}
    <View style={tabBarStyles.topBorder} />
  </View>
);

const tabBarStyles = StyleSheet.create({
  backgroundContainer: {
    ...StyleSheet.absoluteFillObject,
    overflow: "hidden",
  },
  frostedOverlay: {
    ...StyleSheet.absoluteFillObject,
    backgroundColor: "rgba(251, 251, 251, 0.6)",
  },
  topBorder: {
    position: "absolute",
    top: 0,
    left: 0,
    right: 0,
    height: 1,
    backgroundColor: "#FFFFFF",
  },
});

type TabIconProps = {
  focused: boolean;
  color: string;
};

const TAB_ICON_WIDTH = 29;
const TAB_ICON_HEIGHT = 29;

const getFillColor = (focused: boolean) =>
  focused ? colors.activeTabIndicator : "none";

const VibratingTabBarButton = (props: BottomTabBarButtonProps) => {
  const { onPress, style, children, accessibilityLabel, testID } = props;
  const scaleAnim = useRef(new Animated.Value(1)).current;

  const triggerScale = () => {
    scaleAnim.setValue(1);
    Animated.sequence([
      Animated.timing(scaleAnim, {
        toValue: 0.8,
        duration: 67,
        useNativeDriver: true,
      }),
      Animated.timing(scaleAnim, {
        toValue: 1,
        duration: 67,
        useNativeDriver: true,
      }),
    ]).start();
  };

  const handlePress = (e: Parameters<NonNullable<typeof onPress>>[0]) => {
    Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light);
    triggerScale();
    if (onPress) {
      onPress(e);
    }
  };

  return (
    <TouchableOpacity
      style={style}
      onPress={handlePress}
      activeOpacity={1}
      accessibilityLabel={accessibilityLabel}
      testID={testID}
    >
      <Animated.View style={{ transform: [{ scale: scaleAnim }] }}>
        {children}
      </Animated.View>
    </TouchableOpacity>
  );
};

const EventsTabIcon = ({ focused, color }: TabIconProps) => {
  const strokeColor = color;
  const fillColor = getFillColor(focused);

  return (
    <Svg
      width={TAB_ICON_WIDTH}
      height={TAB_ICON_HEIGHT}
      viewBox="-1.5 -1.5 26 26"
      fill="none"
    >
      <Path
        d="M11.1736 1.45571C11.7187 1.0456 12.466 1.04931 13.0085 1.46281C15.489 3.35338 22.3821 8.38046 22.5533 9.38231C23.4589 11.7647 23.1687 18.6122 20.7074 20.4582C19.4767 21.3811 4.70892 21.3811 3.47827 20.4582C1.01352 18.6122 0.705862 9.99763 1.62885 9.38231C2.00454 8.50621 8.69315 3.35338 11.1736 1.45571Z"
        fill={fillColor}
        stroke={strokeColor}
        strokeWidth={2.3}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <Path
        d="M12.0918 16.3047C13.8759 16.3047 15.3222 14.8583 15.3222 13.0742C15.3222 11.2901 13.8759 9.84375 12.0918 9.84375C10.3076 9.84375 8.86133 11.2901 8.86133 13.0742C8.86133 14.8583 10.3076 16.3047 12.0918 16.3047Z"
        fill={focused ? '#FFFFFF' : 'none'}
        stroke={focused ? 'none' : strokeColor}
        strokeWidth={2.3}
      />
    </Svg>
  );
};

const MyEventsTabIcon = ({ focused, color }: TabIconProps) => {
  const strokeColor = color;
  const fillColor = getFillColor(focused);

  return (
    <Svg
      width={TAB_ICON_WIDTH}
      height={TAB_ICON_HEIGHT}
      viewBox="-1.5 -1.5 26 26"
      fill="none"
    >
      <Path
        d="M12.1043 7.62467C13.8922 7.62467 15.3415 6.17535 15.3415 4.38753C15.3415 2.59971 13.8922 1.15039 12.1043 1.15039C10.3165 1.15039 8.86719 2.59971 8.86719 4.38753C8.86719 6.17535 10.3165 7.62467 12.1043 7.62467Z"
        fill={fillColor}
        stroke={strokeColor}
        strokeWidth={2.2}
      />
      <Path
        d="M4.70292 5.31152C7.94006 14.5605 16.2641 14.5605 19.5013 5.31152L22.7384 7.16132C24.4341 8.08621 18.8847 19.4933 15.8017 20.5723C13.9519 21.3431 10.2523 21.3431 8.40251 20.5723C5.31951 19.4933 -0.229868 8.08621 1.46578 7.16132L4.70292 5.31152Z"
        fill={fillColor}
        stroke={strokeColor}
        strokeWidth={2.2}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </Svg>
  );
};

const CreateTabIcon = ({ focused: _focused, color }: TabIconProps) => {
  const strokeColor = color;

  return (
    <Svg
      width={TAB_ICON_WIDTH}
      height={TAB_ICON_HEIGHT}
      viewBox="-1.5 -1.5 26 26"
      fill="none"
    >
      <Circle
        cx={11.1504}
        cy={11.1504}
        r={10}
        stroke={strokeColor}
        strokeWidth={2.2}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <Path
        d="M11.1504 6.70605V15.5949"
        stroke={strokeColor}
        strokeWidth={2.2}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <Path
        d="M6.70312 11.1514H15.592"
        stroke={strokeColor}
        strokeWidth={2.2}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </Svg>
  );
};

const MessagesTabIcon = ({ focused, color }: TabIconProps) => {
  const strokeColor = color;
  const fillColor = getFillColor(focused);
  const { hasUnseenMessages } = useChat();

  return (
    <View style={{ width: TAB_ICON_WIDTH, height: TAB_ICON_HEIGHT }}>
      <Svg
        width={TAB_ICON_WIDTH}
        height={TAB_ICON_HEIGHT}
        viewBox="-1.5 -1.5 26 27"
        fill="none"
      >
        <Path
          d="M11.7109 1.17383L12.2236 1.22363L12.2344 1.22461C14.6867 1.55365 16.9235 2.78204 18.5166 4.66113L18.8271 5.04492L18.8311 5.05078C20.4116 7.13905 21.1012 9.76869 20.75 12.3643C20.1615 16.736 16.5076 20.2702 12.1426 20.7656C11.0944 20.8845 10.3281 20.8254 9.36426 20.6943C9.33153 20.6899 9.29882 20.684 9.2666 20.6768C8.69404 20.5478 8.39429 20.5904 8.11426 20.6768C7.94133 20.7301 7.75817 20.8062 7.49316 20.9219C7.23989 21.0324 6.92331 21.1736 6.5498 21.3135V21.3145C5.70173 21.6317 4.85389 21.809 4.03027 21.9365C3.55833 22.0096 3.08943 21.7834 2.85352 21.3682C2.61761 20.9529 2.66333 20.4344 2.96777 20.0664C3.07385 19.9382 3.17837 19.8095 3.28125 19.6807C4.10494 18.6451 4.02244 18.2873 3.97168 18.1172C3.92176 17.9499 3.80674 17.7337 3.56055 17.3857C3.44029 17.2158 3.30517 17.0357 3.14551 16.8232C2.98945 16.6156 2.81509 16.3832 2.63477 16.1289C1.81118 14.9674 1.35056 13.3563 1.20605 12.0684V12.0615C0.93166 9.46773 1.69424 6.87046 3.32812 4.83789L3.66602 4.43555C5.39029 2.48695 7.60982 1.47707 10.1455 1.19141L10.1709 1.18848C10.6832 1.14214 11.1981 1.13782 11.7109 1.17383Z"
          fill={fillColor}
          stroke={strokeColor}
          strokeWidth={2.3}
          strokeLinejoin="round"
        />
      </Svg>
      {hasUnseenMessages && (
        <View
          style={{
            position: "absolute",
            top: 5,
            right: 15,
            width: 10,
            height: 10,
            borderRadius: 5,
            backgroundColor: "#FF1519",
            borderWidth: 2,
            borderColor: "#FFFFFF",
          }}
        />
      )}
    </View>
  );
};

const ProfileTabIcon = ({ focused, color }: TabIconProps) => {
  const strokeColor = color;
  const innerStroke = focused ? '#FFFFFF' : strokeColor;

  return (
    <Svg
      width={TAB_ICON_WIDTH}
      height={TAB_ICON_HEIGHT}
      viewBox="-1.5 -1.5 26 26"
      fill="none"
    >
      <Path
        d="M11.1504 21.1504C16.6732 21.1504 21.1504 16.6732 21.1504 11.1504C21.1504 5.62754 16.6732 1.15039 11.1504 1.15039C5.62754 1.15039 1.15039 5.62754 1.15039 11.1504C1.15039 16.6732 5.62754 21.1504 11.1504 21.1504Z"
        fill={focused ? strokeColor : 'none'}
        stroke={strokeColor}
        strokeWidth={2.1}
        strokeLinejoin="round"
      />
      <Path
        d="M6.45117 8.6861C7.22796 7.30515 8.78153 7.30515 9.55831 8.6861"
        stroke={innerStroke}
        strokeWidth={2.1}
        strokeLinecap="round"
      />
      <Path
        d="M12.666 8.6861C13.4428 7.30515 14.9964 7.30515 15.7732 8.6861"
        stroke={innerStroke}
        strokeWidth={2.1}
        strokeLinecap="round"
      />
      <Path
        d="M14.6504 14.6504C14.6504 14.6504 13.6504 16.6504 11.1504 16.6504C8.65039 16.6504 7.65039 14.6504 7.65039 14.6504"
        stroke={innerStroke}
        strokeWidth={2.1}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </Svg>
  );
};

const MainTabs = () => {
  const insets = useSafeAreaInsets();
  const tabBarBaseStyle = useMemo(
    () => ({
      backgroundColor: "transparent",
      height: 50 + insets.bottom,
      paddingBottom: insets.bottom,
      paddingTop: 8,
      position: "absolute" as const,
      // borderTopWidth: 3.18,
      // borderTopColor: "#FFFFFF",
      elevation: 0,
    }),
    [insets.bottom],
  );
  return (
    <Tab.Navigator
      screenOptions={() => {
        return {
          headerShown: false,
          tabBarShowLabel: false,
          tabBarStyle: tabBarBaseStyle,
          tabBarBackground: () => <TabBarBackground />,
          tabBarActiveTintColor: colors.activeTabIndicator,
          tabBarInactiveTintColor: colors.tabInactive,
          tabBarButton: (props) => <VibratingTabBarButton {...props} />,
          lazy: false,
          animation: "none",
          detachInactiveScreens: false,
          sceneStyle: { backgroundColor: colors.background },
        };
      }}
    >
      <Tab.Screen
        name="Events"
        component={HomeScreen}
        options={{
          tabBarIcon: ({ focused, color }) => (
            <EventsTabIcon focused={focused} color={color} />
          ),
        }}
      />
      <Tab.Screen
        name="MyEvents"
        component={MyEventsScreen}
        options={{
          tabBarIcon: ({ focused, color }) => (
            <MyEventsTabIcon focused={focused} color={color} />
          ),
        }}
      />
      <Tab.Screen
        name="Create"
        component={View}
        listeners={({ navigation }) => ({
          tabPress: (e) => {
            e.preventDefault();
            (navigation as any).navigate("CreateEvent", { editEventId: null });
          },
        })}
        options={{
          tabBarIcon: ({ focused, color }) => (
            <CreateTabIcon focused={focused} color={color} />
          ),
        }}
      />
      <Tab.Screen
        name="Messages"
        component={MessagesScreen}
        options={{
          tabBarIcon: ({ focused, color }) => (
            <MessagesTabIcon focused={focused} color={color} />
          ),
        }}
      />
      <Tab.Screen
        name="Profile"
        component={ProfileScreen}
        options={{
          tabBarIcon: ({ focused, color }) => (
            <ProfileTabIcon focused={focused} color={color} />
          ),
        }}
      />
    </Tab.Navigator>
  );
};

const AppNavigator = () => {
  const navigationTheme = {
    ...DefaultTheme,
    colors: {
      ...DefaultTheme.colors,
      background: colors.background,
      primary: colors.primary,
      card: colors.background,
      text: colors.text,
      border: colors.border,
    },
  };

  return (
    <NavigationContainer ref={navigationRef} theme={navigationTheme}>
      <Stack.Navigator
        initialRouteName="Splash"
        screenOptions={{
          headerShown: false,
          gestureEnabled: true,
          contentStyle: { backgroundColor: colors.background },
          animation: "fade_from_bottom",
          animationDuration: 200,
        }}
      >
        <Stack.Screen
          name="Splash"
          component={SplashScreen}
          options={{
            animation: "none",
            headerShown: false,
            contentStyle: { backgroundColor: "#050F29" },
          }}
        />
        <Stack.Screen
          name="Main"
          component={MainTabs}
          options={{
            animation: "fade",
          }}
        />
        <Stack.Screen
          name="Login"
          component={GoogleSignIn}
          options={{
            presentation: "transparentModal",
            animation: "fade",
            animationDuration: 150,
          }}
        />
        <Stack.Screen
          name="Onboarding"
          component={OnboardingScreen}
          options={{
            gestureEnabled: false,
            animation: "fade",
          }}
        />
        <Stack.Screen
          name="EventDetails"
          component={EventDetailsScreen}
          options={{
            animation: "slide_from_right",
            animationDuration: 350,
          }}
        />
        <Stack.Screen
          name="JoinRequests"
          component={JoinRequestsScreen}
          options={{
            presentation: "modal",
            animation: "slide_from_bottom",
            animationDuration: 350,
          }}
        />
        <Stack.Screen
          name="PendingRequests"
          component={PendingRequestsScreen}
          options={{
            presentation: "modal",
            animation: "slide_from_bottom",
            animationDuration: 350,
          }}
        />
        <Stack.Screen
          name="EventDetailsOverlay"
          component={EventDetailsScreen}
          options={{
            presentation: "modal",
            animation: "slide_from_bottom",
            animationDuration: 350,
          }}
        />
        <Stack.Screen
          name="CreateEvent"
          component={CreateEventScreen}
          options={{
            animation: "slide_from_bottom",
            animationDuration: 250,
          }}
        />
        <Stack.Screen
          name="EditProfile"
          component={EditProfileScreen}
          options={{
            animation: "slide_from_right",
            animationDuration: 350,
          }}
        />
        <Stack.Screen
          name="PastEvents"
          component={PastEventsScreen}
          options={{
            animation: "slide_from_right",
            animationDuration: 350,
          }}
        />
        <Stack.Screen
          name="PrivacyPolicy"
          component={PrivacyPolicyScreen}
          options={{
            animation: "slide_from_right",
            animationDuration: 350,
          }}
        />
        <Stack.Screen
          name="Help"
          component={HelpScreen}
          options={{
            animation: "slide_from_right",
            animationDuration: 350,
          }}
        />
        <Stack.Screen
          name="ChatThread"
          component={ChatThreadScreen}
          options={{
            animation: "slide_from_right",
            animationDuration: 350,
          }}
        />
      </Stack.Navigator>
    </NavigationContainer>
  );
};

export default AppNavigator;
