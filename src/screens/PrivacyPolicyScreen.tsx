import { Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { useNavigation } from "@react-navigation/native";
import { NativeStackNavigationProp } from "@react-navigation/native-stack";
import { Feather } from "@expo/vector-icons";

import ScreenContainer from "@components/ScreenContainer";
import { colors, spacing, typography } from "@theme/index";
import { RootStackParamList } from "@navigation/types";

const BODY_TEXT =
  "Lorem Ipsum is simply dummy text of the printing and typesetting industry. Lorem Ipsum has been the industry's standard dummy text ever since the 1500s, when an unknown printer took a galley of type and scrambled it to make a type specimen book. It has survived not only five centuries, but also the leap into electronic typesetting, remaining essentially unchanged. It was popularised in the 1960s with the release of Letraset sheets containing Lorem Ipsum passages, and more recently with desktop publishing software like Aldus PageMaker including versions of Lorem Ipsum.";

const PrivacyPolicyScreen = () => {
  const navigation =
    useNavigation<NativeStackNavigationProp<RootStackParamList>>();

  return (
    <ScreenContainer>
      <View style={styles.header}>
        <Pressable
          accessibilityRole="button"
          accessibilityLabel="Go back"
          onPress={() => navigation.goBack()}
          style={styles.backButton}
          hitSlop={8}
        >
          <Feather name="chevron-left" size={20} color={colors.text} />
        </Pressable>
        <Text style={styles.headerTitle}>Privacy Policy</Text>
      </View>
      <ScrollView
        showsVerticalScrollIndicator={false}
        contentContainerStyle={styles.scrollContent}
      >
        <Text style={styles.bodyText}>{BODY_TEXT}</Text>
      </ScrollView>
    </ScreenContainer>
  );
};

const styles = StyleSheet.create({
  header: {
    flexDirection: "row",
    alignItems: "center",
    gap: 4,
    paddingTop: spacing.lg - spacing.md,
    paddingBottom: spacing.md,
  },
  backButton: {
    width: 32,
    height: 44,
    justifyContent: "center",
  },
  headerTitle: {
    fontSize: 18,
    fontFamily: typography.fontFamilyMedium,
    color: colors.text,
    letterSpacing: -0.4,
  },
  scrollContent: {
    paddingBottom: spacing.xl,
  },
  bodyText: {
    fontSize: 16,
    fontFamily: typography.fontFamilyRegular,
    color: "#000000",
    lineHeight: 20,
    letterSpacing: -0.3,
  },
});

export default PrivacyPolicyScreen;
