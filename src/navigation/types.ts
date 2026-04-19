export type RootStackParamList = {
  Splash: undefined;
  Login:
    | {
        redirect?: {
          screen: keyof RootTabParamList;
          params?: RootTabParamList[keyof RootTabParamList];
        };
      }
    | undefined;
  Main: any;
  Onboarding: undefined;
  EventDetails: {
    eventId: string;
    origin?: 'Events' | 'MyEvents';
    showEventUpdatedBadge?: boolean;
    readOnly?: boolean;
  };
  EventDetailsOverlay: {
    eventId: string;
    readOnly: true;
  };
  JoinRequests: {
    conversationId: number;
    eventId: number;
    title: string;
    groupType?: "Single" | "Group";
  };
  PendingRequests: {
    conversationId: number;
    eventId: number;
  };
  ChatThread: undefined;
  CreateEvent: { editEventId?: string | null } | undefined;
  EditProfile: undefined;
  PastEvents: undefined;
  PrivacyPolicy: undefined;
  Help: undefined;
};

export type RootTabParamList = {
  Events: {
    showEventReportedBadge?: boolean;
    showEventDeletedBadge?: boolean;
    showEventLeftBadge?: boolean;
  } | undefined;
  MyEvents: {
    showEventCreatedBadge?: boolean;
    showEventDeletedBadge?: boolean;
  } | undefined;
  Create: { editEventId?: string | null };
  Profile: undefined;
  Messages: undefined;
};
