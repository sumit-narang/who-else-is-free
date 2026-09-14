import { FlatList, RefreshControl, StyleSheet, Text, View } from 'react-native';
import { Image } from 'expo-image';
import { useCallback, useEffect, useMemo, useState } from 'react';
import ScalePressable from '@components/ScalePressable';
import BottomSheetModal from '@components/BottomSheetModal';
import SignInButtons from '@components/SignInButtons';
import { useFocusEffect, useNavigation, CompositeNavigationProp } from '@react-navigation/native';
import { BottomTabNavigationProp } from '@react-navigation/bottom-tabs';
import { NativeStackNavigationProp } from '@react-navigation/native-stack';

import ScreenContainer from '@components/ScreenContainer';
import EmptyState from '@components/EmptyState';
import FullPageEmptyState from '@components/FullPageEmptyState';
import UserAvatar from '@components/UserAvatar';
import { UnreadDot } from '@components/ui';
import { colors, spacing, typography } from '@theme/index';
import { useChat } from '@context/ChatContext';
import type { ChatConversation } from '@context/ChatContext';
import { useAuth } from '@context/AuthContext';
import { useEvents } from '@context/EventsContext';
import { RootStackParamList, RootTabParamList } from '@navigation/types';
import { triggerHaptic } from '@services/haptics';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { formatCompactRelativeTime, getNextCompactRelativeTimeUpdateMs } from '@utils/relativeTime';

type MessagesNavigation = CompositeNavigationProp<
  BottomTabNavigationProp<RootTabParamList, 'Messages'>,
  NativeStackNavigationProp<RootStackParamList>
>;

const isSingleHostEventConversation = (conversation: ChatConversation, userId?: number) =>
  conversation.event?.groupType === 'Single' &&
  conversation.event?.userId === userId &&
  conversation.eventId != null;

const isPendingSingleHostPlaceholder = (conversation: ChatConversation, userId?: number) =>
  conversation.id < 0 && isSingleHostEventConversation(conversation, userId);

const ConversationRow = ({
  item,
  onPress,
  activeConversationId,
  events,
  userId,
  nowMs,
  isLast,
}: {
  item: ChatConversation;
  onPress: (item: ChatConversation) => void;
  activeConversationId: number | null;
  events: { id: string; imageUri: string }[];
  userId?: number;
  nowMs: number;
  isLast: boolean;
}) => {
  const { participants = [] } = item;
  const counterpart = participants.find((p) => p.id !== userId) ?? participants[0];
  const memberCount = item.memberIds?.length ?? participants.length;
  const isGroup = memberCount > 2 || !!item.event || (!!item.title && memberCount > 1);
  const titleLabel = isGroup
    ? (item.event?.title ?? item.title ?? item.displayName)
    : (counterpart?.name ?? item.displayName);
  const lastMessageBody =
    item.lastMessage?.body ??
    (isPendingSingleHostPlaceholder(item, userId) ? 'No one accepted yet' : 'No messages yet');
  const isOwnMessage = item.lastMessage?.senderId === userId;
  const isSystemMessage = item.lastMessage?.kind === 'system';
  const lastMessageSender = item.lastMessage
    ? participants.find((participant) => participant.id === item.lastMessage?.senderId)
    : undefined;
  const previewText = item.lastMessage
    ? isSystemMessage
      ? lastMessageBody
      : `${isOwnMessage ? 'You' : (lastMessageSender?.name?.split(' ')[0] ?? '')}: ${lastMessageBody}`
    : lastMessageBody;
  const timestampLabel = item.lastMessage?.createdAt
    ? formatCompactRelativeTime(item.lastMessage.createdAt, nowMs)
    : '';
  const eventImageUri =
    item.eventId != null
      ? events.find((event) => Number(event.id) === item.eventId)?.imageUri
      : undefined;
  const hasUnread = (item.unreadCount ?? 0) > 0;
  const avatarName = isGroup ? titleLabel : (counterpart?.name ?? titleLabel);
  const avatarValue = isGroup ? undefined : counterpart?.avatar;
  const avatarSeed = isGroup ? titleLabel : (counterpart?.id ?? titleLabel);

  return (
    <ScalePressable
      onPress={() => onPress(item)}
      onPressIn={() => triggerHaptic('light')}
      delay={80}
      style={[
        styles.conversationRow,
        item.id === activeConversationId && styles.conversationRowActive,
      ]}
    >
      {hasUnread && (
        <UnreadDot testID={`conversation-unread-dot-${item.id}`} style={styles.unreadDot} />
      )}
      <View style={styles.conversationRowContent}>
        <View style={styles.conversationAvatar}>
          {eventImageUri ? (
            <Image
              source={{ uri: eventImageUri }}
              style={styles.conversationAvatarImage}
              contentFit="cover"
              transition={150}
            />
          ) : (
            <UserAvatar avatar={avatarValue} name={avatarName} seed={avatarSeed} size={52} />
          )}
        </View>
        <View style={styles.conversationCopyInner}>
          <View style={styles.conversationTitleRow}>
            <Text
              style={[styles.conversationName, hasUnread && styles.conversationNameUnread]}
              numberOfLines={1}
            >
              {titleLabel}
            </Text>
            {timestampLabel ? (
              <Text
                style={[
                  styles.conversationTimestamp,
                  hasUnread && styles.conversationTimestampUnread,
                ]}
                numberOfLines={1}
              >
                {timestampLabel}
              </Text>
            ) : null}
          </View>
          <Text
            style={[styles.conversationPreview, hasUnread && styles.conversationPreviewUnread]}
            numberOfLines={1}
          >
            {previewText}
          </Text>
        </View>
      </View>
      {!isLast && <View style={styles.conversationDivider} />}
    </ScalePressable>
  );
};

const MessagesScreen = () => {
  const navigation = useNavigation<MessagesNavigation>();
  const { user } = useAuth();
  const {
    conversations,
    activeConversationId,
    setActiveConversation,
    isConnecting,
    error,
    refreshConversations,
    isRefreshingConversations,
  } = useChat();
  const { events, userEvents, isEventReported } = useEvents();
  const insets = useSafeAreaInsets();
  const [isPullRefreshing, setIsPullRefreshing] = useState(false);
  const [relativeTimeNow, setRelativeTimeNow] = useState(() => Date.now());

  useFocusEffect(
    useCallback(() => {
      if (!user) {
        return undefined;
      }

      refreshConversations().catch(() => undefined);
      return undefined;
    }, [refreshConversations, user]),
  );

  const handleRefresh = useCallback(() => {
    setIsPullRefreshing(true);
    refreshConversations()
      .catch(() => undefined)
      .finally(() => setIsPullRefreshing(false));
  }, [refreshConversations]);

  const displayConversations = useMemo(() => {
    if (!user) return conversations;

    // Filter out conversations linked to reported events
    const filtered = conversations.filter(
      (convo) => !convo.eventId || !isEventReported(String(convo.eventId)),
    );

    const singleHostGroups = new Map<number, ChatConversation[]>();
    const others: ChatConversation[] = [];

    for (const convo of filtered) {
      const isSingleHost =
        convo.event?.groupType === 'Single' &&
        convo.event?.userId === user.id &&
        convo.eventId != null;

      if (isSingleHost) {
        const group = singleHostGroups.get(convo.eventId!) ?? [];
        group.push(convo);
        singleHostGroups.set(convo.eventId!, group);
      } else {
        others.push(convo);
      }
    }

    const consolidated: ChatConversation[] = [...others];
    const singleHostEventIds = new Set<number>();
    for (const [eventId, group] of singleHostGroups) {
      // Pick the conversation with the most recent message as representative
      const representative = group.reduce((best, current) => {
        const bestTime = best.lastMessage ? Date.parse(best.lastMessage.createdAt) : 0;
        const currentTime = current.lastMessage ? Date.parse(current.lastMessage.createdAt) : 0;
        return currentTime > bestTime ? current : best;
      });
      consolidated.push(representative);
      singleHostEventIds.add(eventId);
    }

    const pendingSingleEventRows = userEvents
      .filter(
        (event) =>
          event.ownerId === user.id && event.groupType === 'Single' && !isEventReported(event.id),
      )
      .filter((event) => {
        const eventId = Number(event.id);
        return !Number.isNaN(eventId) && !singleHostEventIds.has(eventId);
      })
      .map((event) => {
        const eventId = Number(event.id);
        return {
          id: -eventId,
          createdBy: user.id,
          title: event.title,
          memberIds: [user.id],
          participants: [
            {
              id: user.id,
              name: event.hostName,
              avatar: event.hostAvatar,
            },
          ],
          displayName: event.title,
          unreadCount: 0,
          eventId,
          event: {
            id: eventId,
            userId: event.ownerId,
            title: event.title,
            location: event.location,
            time: event.time,
            dateLabel: event.dateLabel,
            eventDate: event.eventDate,
            groupType: event.groupType,
            coverKey: event.coverKey ?? undefined,
            scheduledAt: event.scheduledAt,
          },
        } satisfies ChatConversation;
      });

    consolidated.push(...pendingSingleEventRows);

    // Sort by most recent message
    consolidated.sort((a, b) => {
      const aTime = a.lastMessage ? Date.parse(a.lastMessage.createdAt) : 0;
      const bTime = b.lastMessage ? Date.parse(b.lastMessage.createdAt) : 0;
      return bTime - aTime;
    });

    return consolidated;
  }, [conversations, isEventReported, user, userEvents]);
  const isConversationListBusy = isConnecting || isRefreshingConversations || isPullRefreshing;

  useEffect(() => {
    const nextRefreshMs = displayConversations.reduce<number | null>((soonest, conversation) => {
      const delay = getNextCompactRelativeTimeUpdateMs(
        conversation.lastMessage?.createdAt,
        relativeTimeNow,
      );
      if (delay == null) {
        return soonest;
      }
      if (soonest == null) {
        return delay;
      }
      return Math.min(soonest, delay);
    }, null);

    if (nextRefreshMs == null) {
      return undefined;
    }

    const timeoutId = setTimeout(
      () => {
        setRelativeTimeNow(Date.now());
      },
      Math.max(1_000, nextRefreshMs),
    );

    return () => clearTimeout(timeoutId);
  }, [displayConversations, relativeTimeNow]);

  const handleConversationPress = (conversation: ChatConversation) => {
    const is1to1Host = isSingleHostEventConversation(conversation, user?.id);

    if (is1to1Host && conversation.eventId) {
      navigation.navigate('OneToOneHub', {
        conversationId: conversation.id,
        eventId: conversation.eventId,
        title: conversation.event?.title ?? conversation.displayName,
      });
    } else {
      setActiveConversation(conversation.id);
      navigation.navigate('ChatThread');
    }
  };

  const renderConversation = ({ item, index }: { item: ChatConversation; index: number }) => {
    return (
      <ConversationRow
        item={item}
        onPress={handleConversationPress}
        activeConversationId={activeConversationId}
        events={events}
        userId={user?.id}
        nowMs={relativeTimeNow}
        isLast={index === displayConversations.length - 1}
      />
    );
  };

  const [signInVisible, setSignInVisible] = useState(false);

  if (!user) {
    return (
      <View style={styles.screenRoot}>
        <ScreenContainer>
          <View style={styles.headerSpacing}>
            <Text style={styles.headerTitle}>Chat</Text>
          </View>
        </ScreenContainer>
        <FullPageEmptyState visible imageHeight={245} centered hasAction>
          <EmptyState
            title="No messages to show"
            description={'Get started to view your chats.'}
            actionLabel="Get started"
            onActionPress={() => setSignInVisible(true)}
            imageSource={require('@assets/empty-state/chat.png')}
            imageWidth={279}
            imageHeight={245}
          />
        </FullPageEmptyState>
        <BottomSheetModal visible={signInVisible} onClose={() => setSignInVisible(false)}>
          <SignInButtons />
        </BottomSheetModal>
      </View>
    );
  }

  return (
    <View style={styles.screenRoot}>
      <ScreenContainer>
        <View style={styles.headerSpacing}>
          <Text style={styles.headerTitle}>Chat</Text>
        </View>
        <View style={styles.container}>
          {error ? <Text style={styles.errorText}>{error}</Text> : null}
          {isConnecting ? <Text style={styles.helperText}>Connecting to chat…</Text> : null}
          <FlatList
            data={displayConversations}
            extraData={displayConversations}
            keyExtractor={(conversation) => String(conversation.id)}
            renderItem={renderConversation}
            style={styles.flatList}
            contentContainerStyle={{ paddingBottom: spacing.xl + insets.bottom, flexGrow: 1 }}
            refreshControl={
              <RefreshControl
                refreshing={isPullRefreshing}
                onRefresh={handleRefresh}
                tintColor={colors.primary}
              />
            }
            keyboardShouldPersistTaps="handled"
            showsVerticalScrollIndicator={false}
          />
        </View>
      </ScreenContainer>
      <FullPageEmptyState
        visible={displayConversations.length === 0 && !isConversationListBusy}
        imageHeight={245}
        centered
      >
        <EmptyState
          title="No messages to show"
          description={'Your chats will appear here.'}
          imageSource={require('@assets/empty-state/chat.png')}
          imageWidth={279}
          imageHeight={245}
        />
      </FullPageEmptyState>
    </View>
  );
};

const styles = StyleSheet.create({
  screenRoot: {
    flex: 1,
  },
  container: {
    flex: 1,
    gap: spacing.md,
    overflow: 'visible',
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
  helperText: {
    fontSize: typography.caption,
    fontFamily: typography.fontFamilyRegular,
    color: colors.muted,
  },
  errorText: {
    fontSize: typography.caption,
    fontFamily: typography.fontFamilyRegular,
    color: colors.accent,
  },

  flatList: {
    marginLeft: -spacing.md,
  },
  conversationRow: {
    position: 'relative',
  },
  conversationRowContent: {
    flexDirection: 'row',
    alignItems: 'center',
    paddingLeft: spacing.md,
  },
  conversationRowActive: {
    // borderWidth: 1,
    // borderColor: colors.primary,
  },
  conversationAvatar: {
    width: 52,
    height: 52,
    borderRadius: 26,
    alignItems: 'center',
    justifyContent: 'center',
    marginRight: 12,
    overflow: 'hidden',
  },
  conversationAvatarImage: {
    width: '100%',
    height: '100%',
    resizeMode: 'cover',
  },
  conversationCopy: {
    flex: 1,
    gap: 2,
    paddingVertical: spacing.md,
  },
  conversationCopyInner: {
    flex: 1,
    minWidth: 0,
    gap: 4,
    paddingVertical: spacing.md,
  },
  conversationTitleRow: {
    flexDirection: 'row',
    alignItems: 'flex-start',
    justifyContent: 'space-between',
  },
  conversationDivider: {
    height: 1,
    backgroundColor: colors.divider,
    marginLeft: spacing.md + 52 + 12,
  },
  conversationName: {
    flex: 1,
    fontSize: 17,
    lineHeight: 20,
    letterSpacing: -0.5,
    color: colors.text,
    fontFamily: typography.fontFamilyMedium,
  },
  conversationNameUnread: {
    fontFamily: typography.fontFamilySemiBold,
  },
  conversationTimestamp: {
    marginLeft: spacing.sm,
    fontSize: 13,
    lineHeight: 16,
    letterSpacing: -0.2,
    color: '#8B8B8B',
    fontFamily: typography.fontFamilyRegular,
    textAlign: 'right',
  },
  conversationTimestampUnread: {
    color: '#5E5E5E',
    fontFamily: typography.fontFamilyMedium,
  },
  conversationPreview: {
    fontSize: 15,
    lineHeight: 20,
    letterSpacing: -0.5,
    color: '#707070',
    fontFamily: typography.fontFamilyRegular,
  },
  conversationPreviewUnread: {
    color: colors.text,
    fontFamily: typography.fontFamilyMedium,
  },
  unreadDot: {
    position: 'absolute',
    left: 5,
    top: '50%',
    marginTop: -4,
  },
});

export default MessagesScreen;
