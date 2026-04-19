import { useCallback, useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  Pressable,
  RefreshControl,
  ScrollView,
  SectionList,
  SectionListRenderItemInfo,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { useNavigation, useIsFocused } from "@react-navigation/native";
import { NativeStackNavigationProp } from "@react-navigation/native-stack";
import { Feather } from "@expo/vector-icons";

import EmptyState from "@components/EmptyState";
import EventCard, { EventItemProps } from "@components/EventCard";
import ScreenContainer from "@components/ScreenContainer";
import { colors, spacing, typography } from "@theme/index";
import { useAuth } from "@context/AuthContext";
import { API_BASE_URL } from "@api/config";
import { CoverKey, resolveCoverUri } from "@constants/covers";
import { RootStackParamList } from "@navigation/types";
import {
  formatAbsoluteDateLabel,
  getScheduleDisplay,
  parseDateKey,
} from "@utils/dateTime";
import { formatAudienceLabel } from "@utils/eventDisplay";

type ApiEvent = {
  id: number;
  title: string;
  location: string;
  time: string;
  description?: string;
  gender: string;
  min_age: number;
  max_age: number;
  date_label?: string;
  event_date: string;
  group_type?: "Single" | "Group";
  user_id: number;
  host_name: string;
  cover_key?: CoverKey | null;
  scheduled_at?: string;
};

type PastEventItem = EventItemProps & { ownerId: number; eventDate: string };

type PastEventSection = {
  title: string;
  data: PastEventItem[];
};

const getPastSectionDateLabel = (eventDate: string): string => {
  const parsed = parseDateKey(eventDate);
  if (!parsed) return eventDate;

  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const eventDay = new Date(parsed.getFullYear(), parsed.getMonth(), parsed.getDate());
  const diffMs = today.getTime() - eventDay.getTime();
  const diffDays = Math.floor(diffMs / (24 * 60 * 60 * 1000));

  if (diffDays === 0) return "Today";
  if (diffDays === 1) return "Yesterday";
  return formatAbsoluteDateLabel(eventDate);
};

const buildPastSections = (items: PastEventItem[]): PastEventSection[] => {
  const grouped = new Map<string, PastEventItem[]>();

  items.forEach((event) => {
    const dateKey = event.eventDate || "";
    if (!dateKey) return;
    const sectionEvents = grouped.get(dateKey) ?? [];
    sectionEvents.push(event);
    grouped.set(dateKey, sectionEvents);
  });

  return Array.from(grouped.entries())
    .sort(([a], [b]) => b.localeCompare(a))
    .map(([eventDate, data]) => ({
      title: getPastSectionDateLabel(eventDate),
      data,
    }))
    .filter((section) => section.data.length > 0);
};

const PastEventsScreen = () => {
  const navigation =
    useNavigation<NativeStackNavigationProp<RootStackParamList>>();
  const { user, token, authFetch } = useAuth();
  const isFocused = useIsFocused();
  const [events, setEvents] = useState<PastEventItem[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [isPullRefreshing, setIsPullRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const fetchPastEvents = useCallback(async () => {
    if (!authFetch || !token) return;
    setError(null);
    try {
      const response = await authFetch(`${API_BASE_URL}/api/events/past`, {
        headers: { Authorization: `Bearer ${token}` },
      });
      if (!response.ok) {
        throw new Error(`Request failed with status ${response.status}`);
      }
      const payload: { data: ApiEvent[] | null } = await response.json();
      const mapped = (payload.data ?? []).map((event): PastEventItem => {
        const schedule = getScheduleDisplay({
          scheduledAt: event.scheduled_at,
          eventDate: event.event_date,
          time: event.time,
          dateLabel: event.date_label,
        });

        return {
          id: String(event.id),
          title: event.title,
          location: event.location,
          time: schedule.displayTime,
          audience: formatAudienceLabel({
            gender: event.gender,
            minAge: event.min_age,
            maxAge: event.max_age,
          }),
          imageUri: resolveCoverUri(event.cover_key),
          badgeLabel:
            user && event.user_id === user.id ? "Hosting" : "Joined",
          ownerId: event.user_id,
          eventDate: schedule.displayDate,
        };
      });
      setEvents(mapped);
    } catch (err) {
      setError("Failed to load past events");
    }
  }, [authFetch, token, user]);

  useEffect(() => {
    if (!isFocused) return;
    setIsLoading(true);
    fetchPastEvents().finally(() => setIsLoading(false));
  }, [isFocused, fetchPastEvents]);

  const handleRefresh = useCallback(() => {
    setIsPullRefreshing(true);
    fetchPastEvents().finally(() => setIsPullRefreshing(false));
  }, [fetchPastEvents]);

  const sections = useMemo(() => buildPastSections(events), [events]);

  const renderSectionHeader = ({ section }: { section: PastEventSection }) => (
    <Text style={styles.sectionHeader}>{section.title}</Text>
  );

  const renderItem = ({ item }: SectionListRenderItemInfo<PastEventItem>) => (
    <View style={styles.eventPressable}>
      <EventCard {...item} />
    </View>
  );

  const showLoading = isLoading && events.length === 0;
  const showError = !!error && !isLoading && events.length === 0;
  const showEmpty = !isLoading && events.length === 0 && !error;

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
        <Text style={styles.headerTitle}>Past Events</Text>
      </View>
      {showLoading ? (
        <View style={styles.centerContent}>
          <ActivityIndicator size="large" color={colors.primary} />
        </View>
      ) : showError ? (
        <View style={styles.centerContent}>
          <Text style={styles.errorText}>{error}</Text>
          <Pressable style={styles.retryButton} onPress={handleRefresh}>
            <Text style={styles.retryButtonText}>Try again</Text>
          </Pressable>
        </View>
      ) : showEmpty ? (
        <ScrollView
          contentContainerStyle={styles.centerContent}
          refreshControl={
            <RefreshControl
              refreshing={isPullRefreshing}
              onRefresh={handleRefresh}
              tintColor={colors.primary}
            />
          }
          showsVerticalScrollIndicator={false}
        >
          <EmptyState
            title="No Past Events"
            description="Events you've hosted or joined will appear here after they end."
          />
        </ScrollView>
      ) : (
        <SectionList<PastEventItem, PastEventSection>
          sections={sections}
          keyExtractor={(item) => item.id}
          renderItem={renderItem}
          renderSectionHeader={renderSectionHeader}
          stickySectionHeadersEnabled={false}
          showsVerticalScrollIndicator={false}
          contentContainerStyle={styles.listContent}
          SectionSeparatorComponent={({ leadingItem }) =>
            leadingItem ? <View style={styles.sectionSeparator} /> : null
          }
          ItemSeparatorComponent={() => <View style={styles.itemSeparator} />}
          refreshControl={
            <RefreshControl
              refreshing={isPullRefreshing}
              onRefresh={handleRefresh}
              tintColor={colors.primary}
            />
          }
        />
      )}
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
  listContent: {
    paddingBottom: spacing.xl,
  },
  sectionHeader: {
    fontSize: 16,
    color: "#000000",
    marginTop: 0,
    marginBottom: 14,
    fontFamily: typography.fontFamilyMedium,
    flexShrink: 1,
    lineHeight: 20,
    letterSpacing: -0.4,
  },
  sectionSeparator: {
    height: 28,
  },
  itemSeparator: {
    height: 14,
  },
  centerContent: {
    flex: 1,
    alignItems: "center",
    justifyContent: "center",
    paddingVertical: spacing.xl,
    gap: spacing.md,
  },
  errorText: {
    fontSize: typography.subtitle,
    fontFamily: typography.fontFamilyMedium,
    color: "#B00020",
    textAlign: "center",
  },
  retryButton: {
    paddingHorizontal: spacing.lg,
    paddingVertical: spacing.sm,
    borderRadius: 999,
    backgroundColor: colors.primary,
  },
  retryButtonText: {
    color: colors.buttonText,
    fontSize: typography.body,
    fontFamily: typography.fontFamilyMedium,
    lineHeight: typography.lineHeight,
    letterSpacing: typography.letterSpacing,
  },
  eventPressable: {
    borderRadius: 20,
  },
  eventPressablePressed: {
    opacity: 0.85,
  },
});

export default PastEventsScreen;
