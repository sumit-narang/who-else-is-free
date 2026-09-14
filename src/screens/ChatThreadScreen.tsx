import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Animated,
  Easing,
  FlatList,
  Keyboard,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';
import { NativeStackNavigationProp } from '@react-navigation/native-stack';
import { RouteProp, StackActions, useNavigation, useRoute } from '@react-navigation/native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import {
  AndroidSoftInputModes,
  KeyboardController,
  KeyboardEvents,
} from 'react-native-keyboard-controller';

import SendIcon from '@assets/chat/send.svg';
import ScreenContainer from '@components/ScreenContainer';
import ChatEventHeader from '@components/ChatEventHeader';
import ConnectionStatusIndicator from '@components/ConnectionStatusIndicator';
import { CountBadge } from '@components/ui';
import EventActionOverlay from '@components/EventActionOverlay';
import useSingleEventMemberActions from '@hooks/useSingleEventMemberActions';
import UserAvatar from '@components/UserAvatar';
import { colors, spacing, typography } from '@theme/index';
import { useChat } from '@context/ChatContext';
import type { ChatMessage } from '@context/ChatContext';
import { useAuth } from '@context/AuthContext';
import { useEvents } from '@context/EventsContext';
import { resolveCoverUri } from '@constants/covers';
import { RootStackParamList } from '@navigation/types';
import { triggerHaptic } from '@services/haptics';
import { logger } from '@services/logger';
import { buildEventMemberSubtitle, buildOneToOneSubtitle } from '@utils/chatHeaderSubtitle';
import { getKeyboardTranslation } from '@utils/bottomObstruction';
import { formatTimeAmPm } from '@utils/dateTime';

const ANDROID_KEYBOARD_GAP = spacing.xs;

interface ComposerProps {
  composerBottomPadding: number;
  draft: string;
  isSendDisabled: boolean;
  onDraftChange: (text: string) => void;
  onSend: () => void;
}

const ChatComposer = ({
  composerBottomPadding,
  draft,
  isSendDisabled,
  onDraftChange,
  onSend,
}: ComposerProps) => (
  <View
    style={[styles.composerContainer, { paddingBottom: composerBottomPadding }]}
    testID="chat-composer-container"
  >
    <View style={styles.composerInputWrapper}>
      <TextInput
        placeholder="Write a message"
        value={draft}
        onChangeText={onDraftChange}
        style={styles.composerInput}
        placeholderTextColor={colors.tabInactive}
        multiline
      />
      <Pressable
        onPress={onSend}
        disabled={isSendDisabled}
        style={[styles.sendIconButton, isSendDisabled && styles.sendIconButtonDisabled]}
        accessibilityRole="button"
        accessibilityLabel="Send message"
        accessibilityState={{ disabled: isSendDisabled }}
      >
        <SendIcon
          width={15}
          height={16}
          color={isSendDisabled ? colors.tabInactive : colors.buttonText}
        />
      </Pressable>
    </View>
  </View>
);

/**
 * Android keyboard handling for the thread. The window is kept in
 * ADJUST_NOTHING so the header stays put, and the thread body gets bottom
 * padding equal to the keyboard lift instead. That pushes the composer up and
 * shrinks the message list together, so the latest messages stay visible and
 * scrollable above the composer while typing.
 */
const useAndroidKeyboardLift = (safeAreaBottom: number, onSettled: () => void) => {
  const lift = useRef(new Animated.Value(0)).current;
  const onSettledRef = useRef(onSettled);

  useEffect(() => {
    onSettledRef.current = onSettled;
  }, [onSettled]);

  useEffect(() => {
    if (Platform.OS !== 'android') {
      return undefined;
    }
    KeyboardController.setInputMode(AndroidSoftInputModes.SOFT_INPUT_ADJUST_NOTHING);

    const showSub = KeyboardEvents.addListener('keyboardWillShow', (event) => {
      const keyboardTranslation = getKeyboardTranslation({
        keyboardHeight: event.height,
        safeAreaBottom,
      });
      Animated.timing(lift, {
        toValue: keyboardTranslation + ANDROID_KEYBOARD_GAP,
        duration: event.duration || 250,
        easing: Easing.out(Easing.cubic),
        useNativeDriver: false,
      }).start(({ finished }) => {
        if (finished) {
          onSettledRef.current();
        }
      });
    });

    const hideSub = KeyboardEvents.addListener('keyboardWillHide', (event) => {
      Animated.timing(lift, {
        toValue: 0,
        duration: event.duration || 250,
        easing: Easing.out(Easing.cubic),
        useNativeDriver: false,
      }).start();
    });

    return () => {
      showSub.remove();
      hideSub.remove();
      KeyboardController.setDefaultMode();
    };
  }, [lift, safeAreaBottom]);

  return lift;
};

const countRoutesAbove = (
  navigation: NativeStackNavigationProp<RootStackParamList>,
  routeKey: string,
): number => {
  const routes = navigation.getState()?.routes ?? [];
  const index = routes.findIndex((entry) => entry.key === routeKey);
  return index === -1 ? 0 : routes.length - 1 - index;
};

const ChatThreadScreen = () => {
  const navigation = useNavigation<NativeStackNavigationProp<RootStackParamList>>();
  const route = useRoute<RouteProp<RootStackParamList, 'ChatThread'>>();
  const insets = useSafeAreaInsets();
  const { user } = useAuth();
  const { events } = useEvents();
  const {
    activeConversationId,
    conversations,
    setActiveConversation,
    messages,
    sendMessage,
    retryMessage,
    isConnecting,
    isRefreshingConversations,
    threadError,
    joinRequestsByConversation,
    refreshJoinRequests,
    refreshConversations,
  } = useChat();

  const [draft, setDraft] = useState('');
  const messagesListRef = useRef<FlatList<ChatMessage>>(null);
  const scrollMessagesToEnd = useCallback(() => {
    messagesListRef.current?.scrollToEnd({ animated: true });
  }, []);
  const androidKeyboardLift = useAndroidKeyboardLift(insets.bottom, scrollMessagesToEnd);

  const activeConversation = useMemo(
    () => conversations.find((conversation) => conversation.id === activeConversationId) ?? null,
    [conversations, activeConversationId],
  );

  useEffect(() => {
    if (activeConversationId == null || activeConversation || isRefreshingConversations) {
      return;
    }
    refreshConversations().catch((err) => {
      logger.error('Failed to refresh missing active conversation', err);
    });
  }, [activeConversation, activeConversationId, isRefreshingConversations, refreshConversations]);

  const activeEvent = useMemo(() => {
    if (!activeConversation?.eventId) {
      return null;
    }
    return events.find((eventItem) => Number(eventItem.id) === activeConversation.eventId) ?? null;
  }, [activeConversation, events]);

  const eventCoverUri = useMemo(() => {
    if (activeEvent?.imageUri) {
      return activeEvent.imageUri;
    }
    if (!activeConversation?.event?.coverKey) {
      return null;
    }
    return resolveCoverUri(activeConversation.event.coverKey ?? null);
  }, [activeConversation, activeEvent]);

  const activeEventDetails = useMemo(
    () => activeEvent ?? activeConversation?.event ?? null,
    [activeConversation, activeEvent],
  );

  const activeEventGroupType = useMemo(
    () => activeEventDetails?.groupType ?? null,
    [activeEventDetails],
  );

  const counterpart = useMemo(() => {
    if (!activeConversation || !user) {
      return null;
    }
    return (
      activeConversation.participants?.find((participant) => participant.id !== user.id) ?? null
    );
  }, [activeConversation, user]);

  const activeEventOwnerId = useMemo(() => {
    if (!activeEventDetails) {
      return null;
    }
    if ('ownerId' in activeEventDetails && typeof activeEventDetails.ownerId === 'number') {
      return activeEventDetails.ownerId;
    }
    if ('userId' in activeEventDetails && typeof activeEventDetails.userId === 'number') {
      return activeEventDetails.userId;
    }
    return null;
  }, [activeEventDetails]);

  const isGroupEventConversation = activeEventGroupType === 'Group';
  const isSingleEventConversation = activeEventGroupType === 'Single';

  const headerTitle = useMemo(() => {
    if (isSingleEventConversation && counterpart?.name) {
      return counterpart.name;
    }
    if (activeEventGroupType === 'Single' && activeConversation && user) {
      const otherUser = activeConversation.participants?.find((p) => p.id !== user.id);
      if (otherUser?.name) {
        return otherUser.name;
      }
    }
    return activeConversation?.displayName ?? '';
  }, [activeConversation, activeEventGroupType, counterpart, isSingleEventConversation, user]);

  const headerSubtitle = useMemo(() => {
    if (isSingleEventConversation) {
      return buildOneToOneSubtitle({
        planName: activeEventDetails?.title ?? activeConversation?.displayName ?? 'One to one',
        schedule: activeEventDetails,
      });
    }
    if (isGroupEventConversation) {
      return buildEventMemberSubtitle({
        groupType: 'Group',
        memberCount: activeConversation?.memberIds.length ?? 0,
        schedule: activeEventDetails,
      });
    }
    return undefined;
  }, [activeEventDetails, activeConversation, isGroupEventConversation, isSingleEventConversation]);

  const joinRequests = useMemo(() => {
    if (activeConversationId == null) {
      return [];
    }
    const primary = joinRequestsByConversation[activeConversationId] ?? [];
    const eventScopedKey = activeConversation?.eventId ? -activeConversation.eventId : null;
    if (eventScopedKey == null) {
      return primary;
    }

    const fallback = joinRequestsByConversation[eventScopedKey] ?? [];
    if (fallback.length === 0) {
      return primary;
    }
    if (primary.length === 0) {
      return fallback;
    }

    const merged = new Map<number, (typeof primary)[number]>();
    for (const item of fallback) {
      merged.set(item.id, item);
    }
    for (const item of primary) {
      merged.set(item.id, item);
    }
    return Array.from(merged.values());
  }, [activeConversation?.eventId, activeConversationId, joinRequestsByConversation]);

  const isConversationHost = useMemo(() => {
    if (!activeConversation || !user) {
      return false;
    }
    return (
      user.id === activeConversation.createdBy ||
      (activeEventOwnerId != null && user.id === activeEventOwnerId)
    );
  }, [activeConversation, activeEventOwnerId, user]);
  const canOpenSingleChatActions =
    isSingleEventConversation &&
    isConversationHost &&
    !!activeConversation?.eventId &&
    counterpart != null;

  const isManuallyLeavingRef = useRef(false);
  const isRouteRemovingRef = useRef(false);

  useEffect(() => {
    const unsubscribeBeforeRemove = navigation.addListener('beforeRemove', () => {
      isRouteRemovingRef.current = true;
    });
    const unsubscribeTransitionEnd = navigation.addListener('transitionEnd', (event) => {
      if (event.data?.closing) {
        setActiveConversation(null);
      } else {
        isRouteRemovingRef.current = false;
      }
    });

    return () => {
      unsubscribeBeforeRemove();
      unsubscribeTransitionEnd();
    };
  }, [navigation, setActiveConversation]);

  useEffect(() => {
    if (!activeConversationId) {
      if (isManuallyLeavingRef.current) {
        isManuallyLeavingRef.current = false;
        return;
      }
      if (isRouteRemovingRef.current) {
        return;
      }
      if (!navigation.canGoBack()) {
        navigation.navigate('Main', { screen: 'Messages' });
        return;
      }
      // A sheet (Requests, member actions) may sit above this thread. `goBack`
      // would only pop that sheet and strand this screen on its spinner, so pop
      // the thread together with everything stacked on it.
      const routesAbove = navigation.isFocused() ? 0 : countRoutesAbove(navigation, route.key);
      if (routesAbove > 0) {
        navigation.dispatch(StackActions.pop(routesAbove + 1));
      } else {
        navigation.goBack();
      }
    }
  }, [activeConversationId, navigation, route.key]);

  useEffect(() => {
    if (
      !activeConversation ||
      !activeConversation.eventId ||
      !isConversationHost ||
      !isGroupEventConversation
    ) {
      return;
    }
    refreshJoinRequests(activeConversation.id, activeConversation.eventId, {
      includeApproved: false,
    }).catch(() => undefined);
  }, [activeConversation, isConversationHost, isGroupEventConversation, refreshJoinRequests]);

  useEffect(() => {
    const showEvent = Platform.OS === 'ios' ? 'keyboardWillShow' : 'keyboardDidShow';
    const showSub = Keyboard.addListener(showEvent, () => {
      setTimeout(() => {
        messagesListRef.current?.scrollToEnd({ animated: true });
      }, 50);
    });

    return () => showSub.remove();
  }, []);

  // Scroll to bottom when new messages arrive (non-inverted list)
  const prevMessageCount = useRef(messages.length);
  useEffect(() => {
    if (messages.length === 0) return;
    const animated = prevMessageCount.current > 0 && messages.length > prevMessageCount.current;
    prevMessageCount.current = messages.length;
    const timer = setTimeout(() => {
      messagesListRef.current?.scrollToEnd({ animated });
    }, 50);
    return () => clearTimeout(timer);
  }, [messages.length]);

  const handleBack = () => {
    triggerHaptic('light');
    isManuallyLeavingRef.current = true;
    setActiveConversation(null);
    if (navigation.canGoBack()) {
      navigation.goBack();
    } else {
      navigation.navigate('Main', { screen: 'Messages' });
    }
  };

  const refreshSingleConversation = useCallback(async () => {
    if (!activeConversation?.eventId) {
      return;
    }
    await refreshConversations().catch((err) => {
      logger.error('Failed to refresh conversations after single chat action', err);
    });
    await refreshJoinRequests(
      activeConversationId ?? activeConversation.eventId,
      activeConversation.eventId,
      {
        includeApproved: true,
      },
    ).catch((err) => {
      logger.error('Failed to refresh join requests after single chat action', err);
    });
    setActiveConversation(null);
  }, [
    activeConversation?.eventId,
    activeConversationId,
    refreshConversations,
    refreshJoinRequests,
    setActiveConversation,
  ]);

  const memberActions = useSingleEventMemberActions({
    eventId: activeConversation?.eventId,
    onSuccess: refreshSingleConversation,
  });

  useEffect(() => {
    memberActions.reset();
  }, [activeConversationId, memberActions.reset]);

  const handleSend = () => {
    if (!activeConversationId || !draft.trim()) {
      return;
    }
    triggerHaptic('submit');
    sendMessage(activeConversationId, draft.trim());
    setDraft('');
  };

  const isSendDisabled = draft.trim().length === 0;
  const composerBottomPadding =
    Platform.OS === 'ios'
      ? insets.bottom + spacing.xs
      : insets.bottom >= spacing.lg
        ? insets.bottom
        : spacing.sm;
  const pendingJoinRequestCount =
    isConversationHost && isGroupEventConversation
      ? joinRequests.filter((request) => request.status === 'pending').length
      : 0;
  const canViewJoinRequests =
    isConversationHost && isGroupEventConversation && !!activeConversation?.eventId;

  if (!activeConversation) {
    return (
      <ScreenContainer>
        <View style={styles.loadingContainer}>
          <ActivityIndicator size="large" color={colors.primary} />
        </View>
      </ScreenContainer>
    );
  }

  const handleOpenJoinRequests = () => {
    if (
      !activeConversation ||
      !activeConversation.eventId ||
      !isConversationHost ||
      !isGroupEventConversation
    ) {
      return;
    }
    triggerHaptic('light');
    navigation.navigate('JoinRequest', {
      conversationId: activeConversation.id,
      eventId: activeConversation.eventId,
    });
  };

  const renderMessage = ({ item, index }: { item: (typeof messages)[number]; index: number }) => {
    const lowerBody = item.body.toLowerCase();
    const isJoinSystemMessage = lowerBody.endsWith('joined the chat');
    const isEventUpdateSystemMessage =
      lowerBody === 'updated event detail' || lowerBody === 'plan details updated';

    if (item.kind === 'system' || isJoinSystemMessage || isEventUpdateSystemMessage) {
      return (
        <View style={styles.systemMessageRow}>
          <Text style={styles.systemMessageText}>{item.body}</Text>
        </View>
      );
    }

    const isOwn = item.senderId === user?.id;
    const participant = activeConversation.participants?.find((p) => p.id === item.senderId);
    const senderName = participant?.name ?? '';
    const firstName = senderName.split(' ')[0] || '';

    const prevMessage = index > 0 ? messages[index - 1] : null;
    const prevIsSystem = prevMessage
      ? prevMessage.kind === 'system' ||
        prevMessage.body.toLowerCase().endsWith('joined the chat') ||
        prevMessage.body.toLowerCase() === 'updated event detail' ||
        prevMessage.body.toLowerCase() === 'plan details updated'
      : false;
    const isFirstInRun = !prevMessage || prevMessage.senderId !== item.senderId || prevIsSystem;

    const nextMessage = index < messages.length - 1 ? messages[index + 1] : null;
    const nextIsSystem = nextMessage
      ? nextMessage.kind === 'system' ||
        nextMessage.body.toLowerCase().endsWith('joined the chat') ||
        nextMessage.body.toLowerCase() === 'updated event detail' ||
        nextMessage.body.toLowerCase() === 'plan details updated'
      : false;
    const isLastInRun = !nextMessage || nextMessage.senderId !== item.senderId || nextIsSystem;

    const showAvatar = !isOwn;
    const showName = showAvatar && isFirstInRun;
    // Every message shows its own inline timestamp (kept compact by being inline).
    const showMeta = true;

    const timeText = item.pending
      ? 'Sending…'
      : item.failed
        ? "Couldn't send. Tap to retry."
        : formatTimeAmPm(
            new Date(item.createdAt).getHours(),
            new Date(item.createdAt).getMinutes(),
          );

    const bubble = (
      <View
        style={[
          styles.messageBubble,
          isOwn ? styles.messageBubbleOwn : styles.messageBubbleOther,
          item.failed ? styles.messageBubbleFailed : undefined,
        ]}
      >
        <Text style={[styles.messageText, isOwn ? styles.messageTextOwn : styles.messageTextOther]}>
          {item.body}
          {showMeta ? (
            <Text
              style={[
                styles.messageMeta,
                item.failed
                  ? styles.messageMetaFailed
                  : isOwn
                    ? styles.messageMetaOwn
                    : styles.messageMetaOther,
              ]}
            >
              {`  ${timeText}`}
            </Text>
          ) : null}
        </Text>
      </View>
    );

    const bubbleContent = item.failed ? (
      <Pressable
        onPress={() => {
          triggerHaptic('warning');
          retryMessage(item.conversationId, item);
        }}
      >
        {bubble}
      </Pressable>
    ) : (
      bubble
    );

    return (
      <View
        style={[
          styles.messageRow,
          isOwn ? styles.messageRowOwn : styles.messageRowOther,
          // Tight gap within a run, full gap between senders (grouping).
          { marginBottom: isLastInRun ? spacing.sm : 3 },
        ]}
      >
        {showAvatar ? (
          isLastInRun ? (
            <UserAvatar
              avatar={participant?.avatar}
              name={senderName}
              seed={item.senderId}
              size={30}
              style={styles.avatarCircle}
            />
          ) : (
            <View style={styles.avatarSpacer} />
          )
        ) : null}
        <View style={styles.messageBubbleContainer}>
          {showName && firstName ? <Text style={styles.senderName}>{firstName}</Text> : null}
          {bubbleContent}
        </View>
      </View>
    );
  };

  const composerProps: ComposerProps = {
    composerBottomPadding,
    draft,
    isSendDisabled,
    onDraftChange: setDraft,
    onSend: handleSend,
  };

  return (
    <ScreenContainer edges={['top']}>
      <ChatEventHeader
        onBack={handleBack}
        title={headerTitle}
        subtitle={headerSubtitle}
        coverUri={canOpenSingleChatActions ? undefined : eventCoverUri}
        leadingElement={
          isSingleEventConversation && counterpart ? (
            <UserAvatar
              avatar={counterpart.avatar}
              name={counterpart.name}
              seed={counterpart.id}
              size={40}
            />
          ) : undefined
        }
        onTitlePress={() => {
          if (canOpenSingleChatActions && counterpart) {
            memberActions.openMenu({
              userId: counterpart.id,
              name: counterpart.name,
            });
            return;
          }
          if (activeConversation?.eventId) {
            triggerHaptic('light');
            navigation.navigate('EventDetailsOverlay', {
              eventId: String(activeConversation.eventId),
              readOnly: true,
            });
          }
        }}
        titleAccessibilityLabel={canOpenSingleChatActions ? 'Open actions' : 'View plan details'}
        rightElement={
          <>
            {isConnecting ? (
              <ConnectionStatusIndicator visible testID="chat-connection-status" />
            ) : null}
            {canViewJoinRequests && pendingJoinRequestCount > 0 ? (
              <Pressable
                accessibilityRole="button"
                accessibilityLabel="View requests"
                onPress={handleOpenJoinRequests}
                style={styles.joinIconButton}
              >
                <CountBadge count={pendingJoinRequestCount} />
              </Pressable>
            ) : null}
          </>
        }
        testID="chat-event-info-button"
      />
      {Platform.OS === 'ios' ? (
        <KeyboardAvoidingView
          style={styles.threadContainer}
          behavior="padding"
          keyboardVerticalOffset={16}
        >
          <View style={styles.threadBody}>
            {threadError ? <Text style={styles.errorText}>{threadError}</Text> : null}
            <FlatList
              data={messages}
              keyExtractor={(message) => message.id}
              renderItem={renderMessage}
              ref={messagesListRef}
              style={{ flex: 1 }}
              contentContainerStyle={styles.messagesList}
              showsVerticalScrollIndicator={false}
              keyboardShouldPersistTaps="handled"
              keyboardDismissMode="interactive"
            />
            <ChatComposer {...composerProps} />
          </View>
        </KeyboardAvoidingView>
      ) : (
        <View style={styles.threadContainer}>
          <Animated.View style={[styles.threadBody, { paddingBottom: androidKeyboardLift }]}>
            {threadError ? <Text style={styles.errorText}>{threadError}</Text> : null}
            <FlatList
              data={messages}
              keyExtractor={(message) => message.id}
              renderItem={renderMessage}
              ref={messagesListRef}
              style={{ flex: 1 }}
              contentContainerStyle={styles.messagesList}
              showsVerticalScrollIndicator={false}
              keyboardShouldPersistTaps="handled"
              keyboardDismissMode="on-drag"
            />
            <ChatComposer {...composerProps} />
          </Animated.View>
        </View>
      )}
      <EventActionOverlay
        isVisible={memberActions.showMenu}
        onBackdropPress={memberActions.closeMenu}
        type="menu"
        items={memberActions.menuItems}
      />
      <EventActionOverlay
        isVisible={memberActions.showReportConfirm}
        onBackdropPress={memberActions.cancelReport}
        type="confirm"
        title={`Report & block ${memberActions.selectedTargetFirstName}?`}
        description="They won't be able to interact with you on future plans. Their report will be reviewed by our team."
        confirmLabel="Report & block"
        cancelLabel="Cancel"
        confirmTone="destructive"
        onConfirm={memberActions.confirmReport}
        onCancel={memberActions.cancelReport}
      />
      <EventActionOverlay
        isVisible={memberActions.showReportOverlay}
        onBackdropPress={memberActions.closeReportOverlay}
        type="report"
        placeholder={`Tell us why you're reporting ${memberActions.selectedTargetFirstName}`}
        reportMessage={memberActions.reportMessage}
        onReportMessageChange={memberActions.setReportMessage}
        onSubmitReport={memberActions.handleSubmitReport}
        reportError={memberActions.reportError}
        reportSubmitting={memberActions.isSubmittingReport}
        reportDisabled={!memberActions.reportMessage.trim()}
      />
    </ScreenContainer>
  );
};

const styles = StyleSheet.create({
  joinIconButton: {
    marginLeft: spacing.sm,
    padding: spacing.xs,
  },
  errorText: {
    fontSize: typography.caption,
    fontFamily: typography.fontFamilyRegular,
    color: colors.accent,
  },
  threadContainer: {
    flex: 1,
  },
  threadBody: {
    flex: 1,
  },
  loadingContainer: {
    flex: 1,
    alignItems: 'center',
    justifyContent: 'center',
  },
  messagesList: {
    flexGrow: 1,
    justifyContent: 'flex-end',
    paddingTop: spacing.sm,
    paddingBottom: spacing.xs,
  },
  messageRow: {
    marginBottom: spacing.sm,
    flexDirection: 'row',
    alignItems: 'flex-end',
  },
  messageRowOwn: {
    justifyContent: 'flex-end',
  },
  messageRowOther: {
    justifyContent: 'flex-start',
  },
  messageBubbleContainer: {
    maxWidth: '75%',
  },
  avatarCircle: {
    width: 30,
    height: 30,
    borderRadius: 15,
    marginRight: spacing.xs,
    flexShrink: 0,
  },
  avatarSpacer: {
    width: 30,
    marginRight: spacing.xs,
    flexShrink: 0,
  },
  // Sender label above the bubble: distinct from the smaller, centered system notices.
  senderName: {
    fontSize: 13,
    fontFamily: typography.fontFamilyRegular,
    lineHeight: 16,
    letterSpacing: -0.3,
    color: colors.subText,
    marginBottom: 4,
    marginLeft: spacing.md, // = messageBubble paddingLeft, so the name lines up with the text
  },
  messageBubble: {
    paddingTop: 10,
    paddingBottom: 10,
    paddingLeft: 14,
    paddingRight: 14,
    borderRadius: 18,
    borderCurve: 'continuous',
  },
  messageBubbleOwn: {
    alignSelf: 'flex-end',
    backgroundColor: colors.primary,
  },
  messageBubbleOther: {
    alignSelf: 'flex-start',
    backgroundColor: colors.inputSurface,
  },
  messageBubbleFailed: {
    borderColor: colors.accent,
  },
  messageText: {
    fontSize: 16,
    fontFamily: typography.fontFamilyRegular,
    fontWeight: '400',
    lineHeight: 22,
    letterSpacing: -0.5,
  },
  messageTextOwn: {
    color: colors.buttonText,
  },
  messageTextOther: {
    color: colors.text,
  },
  // Inline timestamp appended to the message text (nested Text).
  messageMeta: {
    fontSize: 11,
    fontFamily: typography.fontFamilyRegular,
    fontWeight: '400',
    letterSpacing: -0.3,
  },
  messageMetaOwn: {
    color: colors.buttonText,
    opacity: 0.8,
  },
  messageMetaOther: {
    color: colors.subText,
  },
  messageMetaFailed: {
    color: colors.accent,
  },
  systemMessageRow: {
    alignItems: 'center',
    justifyContent: 'center',
    marginBottom: 8,
    paddingHorizontal: spacing.lg,
  },
  // Quiet system notices: small (12) keeps them recessive; grey stays readable.
  systemMessageText: {
    fontSize: 12,
    color: colors.subText,
    textAlign: 'center',
    letterSpacing: -0.3,
  },
  composerContainer: {
    backgroundColor: colors.transparent,
    paddingHorizontal: 0,
  },
  composerInputWrapper: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: spacing.sm,
    backgroundColor: colors.inputSurface,
    borderRadius: 999,
    paddingHorizontal: spacing.sm,
    paddingVertical: spacing.sm,
  },
  composerInput: {
    flex: 1,
    maxHeight: 100,
    paddingVertical: spacing.xs,
    paddingLeft: 12,
    paddingRight: spacing.sm,
    fontFamily: typography.fontFamilyRegular,
    fontSize: 15,
    letterSpacing: typography.inputDetailLetterSpacing,
  },
  sendIconButton: {
    width: 32,
    height: 32,
    borderRadius: 16,
    borderCurve: 'continuous',
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: colors.primary,
  },
  sendIconButtonDisabled: {
    backgroundColor: colors.secondaryButtonBackground,
  },
});

export default ChatThreadScreen;
