# CLAUDE.md

- Bottom-tab chrome lives in `src/navigation/TabBarBackground.tsx`: `FrostedSurface` with real blur so scrolling content is softened on both platforms. iOS keeps intensity 54 beneath the 60% white overlay. Android draws no overlay view, because Dimezis captures every view drawn above the blur (overlay, top border, tab icons) and blurred the overlay into the material before drawing it again; it passes `tabBarAndroidMaterial` (`src/theme/materials.ts`: LIGHT tint intensity 90, alpha 0.70, which carries the overlay's white after the blur, and a 19.5dp sigma converted for the screen density by `androidBlurReductionFactor`). The captured icons leave a faint glow around the active one (5.6/255); a weaker tint shows more of the list and more glow (#190).

Guide for Claude and other coding agents working in this repository.

Follow `AGENTS.md` first. This file mirrors the essentials for Claude-oriented workflows.

## Project

Who Else Is Free is an event discovery and social coordination app.

- Frontend: React Native Expo app in `src/`
- Installed app display name is `weif` (all lowercase, issue #176), owned by `expo.name` in
  `app.config.js`. Keep the Expo slug, EAS project ID, package/bundle identifiers, and persisted
  storage keys stable during display-name changes. Native rebuilds and separate store-title
  updates are required; see `docs/app-display-name.md`.
- Android's default navigation contrast stays enabled (`androidNavigationBar.enforceContrast: true`).
  Only focused onboarding disables it, through `useOnboardingNavigationBar` and the local
  `modules/system-navigation` Expo module. Blur/unmount restores other screens; native resume
  reapplies the active policy. Keep safe-area padding. A native rebuild is required; store builds include it from 1.0.0.
- Onboarding background coverage belongs to an unpadded full-page root; put safe-area/header
  padding on the pager. Measure the page with `onLayout` for radial-gradient geometry and pass
  `pageSize` to `AvatarEditBadge`. Android's reported app window can be shorter than the page.
  Check the contrast setting in the compiled APK, not just source config or version name.
- Backend: Go Gin server in `server/`
- API: REST plus WebSocket chat at `/api/ws`
- Navigation: React Navigation stack and bottom tabs in `src/navigation`
- Inactive bottom-tab scenes always use `pointerEvents` and accessibility props. On Android, hide them with `opacity: 0` rather than `display: 'none'` so event covers and empty-state art remain laid out and do not repaint on return; `importantForAccessibility="no-hide-descendants"` keeps hidden controls out of the accessibility tree. iOS keeps its existing native scene visibility. Pagers receive navigation focus through `AnimatedPager.isActive` and rebuild their gesture on the next frame for Android re-registration without remounting list content.
- `EventsContext.hasLoadedEvents` records the first settled events request. Discover keeps resolved empty pages mounted through focus refreshes; Messages waits for its first focus refresh for the current user, then keeps its empty state mounted through later refreshes.
- Bottom obstructions are normalized by `src/utils/bottomObstruction.ts`. When a surface already reserves the system bottom safe area, subtract that inset from keyboard movement or absolute tab-bar clearance instead of counting it twice. Use keyboard-top coordinates when available; do not add device-size or navigation-mode spacing branches in screens.
- Chat thread on Android keeps the window in `ADJUST_NOTHING` and pads the thread body by the keyboard lift (`useAndroidKeyboardLift` in `ChatThreadScreen.tsx`), so the composer rises and the message list shrinks together; do not translate the composer alone or the latest messages end up under the keyboard.
- Text inputs use `typography.inputLetterSpacing` / `inputDetailLetterSpacing` instead of the negative text tracking tokens. Android applies negative letter spacing symmetrically, which pushes a placeholder's first glyph under the caret.
- Frosted surfaces (cover chip, avatar edit badge, Event Details hero buttons, tab bar, submit
  button, cover-picker check badge, event card badge strip) render through the
  shared `FrostedSurface` primitive; do not use `BlurView` directly. `expo-blur` disagreed across
  platforms: iOS ran a real `UIVisualEffectView` while Android painted a flat scrim unless a
  surface passed `experimentalBlurMethod`, and only some of ours did. `FrostedSurface` decides once
  per surface, Android's opt-in included; native results still require visual validation.
- A frosted surface blurs (`blur`) whenever anything with a hard edge passes beneath it, because a
  tint alone leaves that edge running straight through the surface: the Create Event cover chip and
  the cover-picker check badge (photography), and `AvatarEditBadge`, which straddles the avatar's
  rim so the blur mixes the page behind the avatar into the badge. `EventActionBadge` also blurs:
  its toast can cross text, cards, and images as the underlying screen scrolls. Measured on the
  cover chip, the cover behind it varies by 46 (luminance sd); blurred that falls to 6, tinted alone it stays at 34.
  Do not judge this from a sample taken over a smooth patch of the backdrop - that is how the avatar
  badge was mistakenly first shipped tint-only. Surfaces over a uniformly smooth backdrop do tint
  through `src/theme/materials.ts`, whose multipliers were fitted to the iOS material within 1%
  (hero buttons sit over an already-blurred hero: 9 behind, 5 blurred). Prefer the tint where it
  holds: on Android a live blur captures the screen behind it every frame. `EventCard`'s badge strip
  stays tint-only regardless because it sits inside a `MaskedView`, the offscreen-capture pattern
  that hit a RenderScript crash.
  Pass the surface's existing `intensity` and keep its own background: the material composites over
  it. Android's blur radius is `intensity / blurReductionFactor`, and at the stock 4 it lands far
  weaker than the iOS material; `FrostedSurface` sets 2, which took the cover chip's residual cover
  texture from 0.40 of the backdrop to 0.21 (iOS 0.12). Tune that constant rather than adding a
  per-platform tint on top: the old `heroButtonTint` did that and pushed Android ~28/255 too dark.
  Measured on the WEIF emulator, whose software renderer is not proof of A56 output, the Android
  chip still reads ~15% lighter than iOS; confirm on a physical device before tuning further.
- `AvatarEditBadge` uses `FrostedSurface.androidBackdrop` with `AvatarBadgeBackdrop` on
  Android. Supply the same avatar URI, name and seed as the 120dp avatar, plus
  `page="onboarding"` on Onboarding. Skia composes only the avatar and page before
  blurring; the camera and shadow must stay outside this source. Keep iOS's native
  intensity-15 material unchanged. `avatarBadgeMaterial` owns the independently
  calibrated radius, tint and saturation. Legacy native blur callers (cover chip
  and Cover Picker) still capture the screen on Android; never describe shared
  intensity as guaranteed pixel parity. Capture both native platforms after changes.
- Action toasts use `ActionToastSurface` inside `EventActionBadge`. iOS keeps native dark blur at
  intensity 65 and the existing 40% black scrim. Android uses the local `modules/toast-blur`
  module: a fixed Gaussian approximation, density-aware sigma, and one calibrated material tint.
  Keep the label inside this native surface so the backdrop capture excludes the entire toast;
  a JS sibling scrim is captured again and makes Android darker. Do not tune generic
  `FrostedSurface` for the toast or add another Android overlay. Rebuild the native Android app
  after changing this module; store builds include it from 1.0.0.
  Native filter tests: `./android/gradlew -p android :toast-blur:testDebugUnitTest`.
- Never put Android `elevation` on a translucent surface. Android tessellates an elevation shadow
  into a polygon and draws it behind the caster, so it shows through as a visible octagon; it is
  invisible only while something opaque (such as a `BlurView`) covers the interior, which is why
  `AvatarEditBadge` only showed it once its blur became a flat 60%-opaque fill. Even `elevation: 1`
  showed it on an A56. Use `boxShadow` there instead: it renders a real Gaussian shadow and measured
  within 3/255 of the iOS shadow's darkest point. `AvatarEditBadge` keeps its original iOS shadow
  props and takes `boxShadow` on Android only, so iOS is untouched.
- Approving a join request advances the approver's read cursor server-side and the client marks that conversation read for a short grace window, so approval-generated messages (intro, join announcement) never surface as unread for the host.
- `POST /api/events/:id/chat/requests` answers 409 with `code: "request_exists"` (a pending request) or
  `code: "already_member"` (the host, or already in the chat). The app marks a plan requested only for
  `request_exists`, and never sends a request as the plan's host: signing in from a plan's join sheet
  keeps the plan open and drops the queued request if you turn out to be its host or a member (#217).
- Chat system-message copy: new rows say `<Name> joined the plan` and `Plan details updated`;
  `backfillSystemMessageCopy` in `server/repository_schema.go` idempotently rewrites legacy
  `joined the chat` / `Updated Event Detail` bodies (system kind only) so older threads and Messages
  previews match. The client still recognises both spellings for rows that predate the `kind` column.
- `ChatContext` exposes two error strings: `error` (socket or conversations-list failures, rendered by
  Messages) and `threadError` (socket, refresh, or send failures for the active thread, cleared when
  the active conversation changes). `ChatThreadScreen` renders `threadError`, never `error`, so a send
  failure cannot sit on top of the Messages list.
- When `ChatThreadScreen`'s active conversation is cleared underneath it (for example the conversations
  refresh no longer lists it), it leaves through `StackActions.pop(n)` sized to remove itself and every
  sheet stacked above it; a plain `goBack` would only dismiss the top sheet and strand the thread on its
  spinner.
- 1:1 chat member actions live in `useSingleEventMemberActions` and mirror the Event Details member
  flow: menu → `Report & block {name}?` confirmation → reason prompt with the shared placeholder and
  `getMemberReportError` copy. Never surface raw error text in that sheet; the remove failure keeps its
  specified alert copy.
- State: React Context providers for auth, events, chat, push, covers, and bloom state
- Event-card metadata in Discover, My Events, and Past Plans uses `formatEventCardMetaLine` (`src/utils/eventDisplay.ts`), normally through `toEventCardItem`. Pass `groupType`, `gender`, `minAge`, and `maxAge`; unrestricted gender/age are omitted. Keep verbose audience copy for Event Details.
- Past Plans uses `src/api/pastEvents.ts` and `usePastEvents` for 25-item cursor pages, request locking/cancellation, session resets, retries, de-duplication, and unchanged-item reconciliation. Focus returns reuse cached pages for 60 seconds, then reconcile the loaded depth; pull-to-refresh resets to page one. `EventSectionList` exposes `onEndReached`/`footer` and opt-in `entryAnimation="initial"` for bounded motion across date sections/pages. `/api/events/past?limit=25&cursor=...` returns `{data,next_cursor}` ordered by normalized UTC schedule/creation/ID with a viewer-bound cursor and fixed past cutoff; no-query requests preserve the legacy response.
- Read-only Event Details uses `ReadOnlyEventDetails`, one animated FlatList with a dynamic hero/info/member heading and shared member rows. Never nest its vertical member list inside a ScrollView; active-plan host pagers and overlay details keep their existing paths.
- `requestJson.signal` combines caller cancellation with its timeout through `createRequestTimeout`, which detaches the caller listener on cleanup.
- Analytics carries no advertising identifiers: weif shows no ads. `firebase.json` turns off the
  Android advertising ID and SSAID, iOS SKAdNetwork registration, and ad storage/user-data/personalisation
  consent defaults, plus the iOS vendor ID (IDFV), so an install is identified only by its Firebase app
  instance ID; `app.config.js` blocks the merged `com.google.android.gms.permission.AD_ID` permission
  and the Privacy Sandbox `ACCESS_ADSERVICES_AD_ID`/`ACCESS_ADSERVICES_ATTRIBUTION` permissions that
  `play-services-measurement-api` also merges; `plugins/withAnalyticsWithoutAdId.js` links the iOS analytics
  pod without IDFA support. All three only take effect in a new native build, and
  `src/__tests__/advertisingIdentifiers.test.ts` guards them. Keep them off: the privacy policy and store
  privacy labels assume no ad IDs. Event params never carry the user's own profile data (name, email,
  gender, age); a plan's audience settings (`gender_preference`, `age_range_bucket`) are fine.
- Android releases: `app.config.js` also blocks camera, microphone and draw-over-apps permissions
  (weif only opens the photo library; keep the storage permissions the picker requests on Android 12
  and lower), guarded by `src/__tests__/androidPermissions.test.ts`. Production builds carry the EAS
  Update channel `production`, and Play uploads go through the manual `Android Release` workflow
  (`eas build --auto-submit-with-profile <track>`); the Play service account key lives on EAS, never in
  this public repo. See `docs/play-store-release.md`.
- Server request logs (`requestLogger` in `server/router.go`) drop the query string: the chat socket
  sends its session token as `?token=` and place searches carry typed text. Keep `gin.New()` with this
  logger rather than `gin.Default()`.
- Push tokens that FCM reports as unregistered are deleted on send (`forgetIfUnregistered` in
  `server/push_firebase.go`); other failures, such as APNs credential errors, keep the token.
- Sign-in and sessions: Google, Apple (iOS) and email codes. Every sign-in response carries a 24h
  access `token` plus a rotating `refresh_token` (90 days from last use, stored as a SHA-256 hash in
  `refresh_tokens`; `server/refresh_tokens.go`). `/api/session/refresh` rotates it: a replaced key
  keeps working until its successor is used once (lost responses never sign anyone out), and a
  replaced key presented after that revokes the whole family. `/api/session/logout` revokes the
  device's family. Client persistence lives in `src/context/authSession.ts`; `AuthContext`
  refreshes through the key first and Google silent sign-in second, never signs out on a network
  error, and `ChatContext` passes `{ onlyIfExpired: true }` so socket reconnects do not rotate keys.
  Apple sessions check `getCredentialStateAsync` on restore/refresh and sign out when revoked. There
  is no user-facing "stay signed in" option. Email sign-in (`/api/email-login/start` and `/verify`,
  `server/email_login.go`, `EmailSignInForm` inside `SignInButtons`) sends a 6-digit code (10 min,
  single use, 5 attempts; in-memory per-minute/hour/IP limits plus 10 codes per address per day
  counted in the database; client IP comes only from `Fly-Client-IP`) through `emailSender` (`server/email_sender.go`,
  Cloudflare Email Sending REST); start answers identically for known and unknown addresses. The "Continue with email" button is
  hidden unless the build sets `EXPO_PUBLIC_EMAIL_SIGN_IN_ENABLED=true`; turn it on only once the
  server can send. Account
  lookups by email are case-insensitive and new emails are stored lowercased. Design and plan:
  `docs/superpowers/specs/2026-11-12-email-code-sign-in-design.md`.
- Startup permissions: Discover waits for `BloomContext.transitionComplete`, then serializes the location sheet (`useLocationPermissionPrompt().offer`) and, once that flow ends, `PushContext.requestPushPermission`. Location goes first because it changes Discover immediately, while a new user has nothing to be notified about yet. Do not prompt from `App.tsx`, provider mount effects, or the splash route; silent checks and token/location loading for existing grants may still run there.
- Location permission sheet: `LocationPermissionSheet` (`src/components/location-permission/`, map illustration drawn in SVG from `colors.locationMap*`) explains location before the system dialog, which cannot be styled. Continue closes the sheet and, on `onClosed`, calls `useViewerLocation().requestPermission`; Close (or backdrop/swipe) skips the system prompt. It is offered once per install (`whoelseisfree.locationPromptSeen` in SecureStore, which Android auto-backup excludes), only when location is not `granted`, and not on iOS when `canAskAgain` is false. Never decide from Android's derived denied status (see Runtime permissions). Apple can reject pre-permission screens that let people skip the system prompt; if App Review objects, remove Close and keep Continue. Discover has no Nearest sort: its header is the Nearby `StatusChip` and an `Upcoming ▾` `DropdownChip` whose "Sort by" sheet is the shared `SelectionModal` (Create Event's pill options without Done: omitting `onConfirm` hides it, and a tap applies the sort and closes) listing Upcoming and Newest; the pager keeps both lists mounted with swiping off. `useDiscoverLocation` measures "near" from the precise device location while the Nearby chip is on and permission is granted, else the approximate network area from `/api/location/approximate` (fetched only when precise is not in use, cached for an hour; pull-to-refresh retries a failed lookup), else none, which shows every plan. Both keep plans within `LOCAL_RADIUS_KM` plus the viewer's own. The Nearby `StatusChip` (accessibility role switch) is filled only in precise mode. It is an in-app switch: tapping it on turns it off and saves that (`whoelseisfree.discoverNearbyOff` in SecureStore), because the app cannot revoke its own permission and the next launch would otherwise switch it back on; tapping it off with permission granted turns it on. Without permission a `DiscoverLocationSheet` always stands in the way (`useDiscoverLocationSheet`): the shared `LocationSheetContent` permission copy, whose Continue asks the system only after the sheet closes, or, once `useViewerLocation().isPromptBlocked` (trusted on iOS, and on Android only after this session has asked), "Location is off for weif" with Open settings. Any change of permission to granted (chip sheet, startup sheet or Settings) switches Nearby on and clears the saved off. The chip ignores the once-per-install record. `useViewerLocation` re-checks silently whenever the app returns to the foreground, so a grant or a revoke in Settings shows up (iOS does not restart the app on revoke); while still granted it checks only the status, without reloading the position.
- Admin support: persistent authorization lives in `admin_users` by immutable user ID;
  `ADMIN_BOOTSTRAP_EMAILS` only provisions verified initial accounts. Support Inbox API mapping is
  in `src/api/adminHelp.ts`, feature state is in `useAdminAccess`/`useAdminHelpSubmissions`, and
  support content must stay out of analytics and routine logs.
- Safety reports outlive accounts and plans (#184): `event_reports.event_id`/`user_id`/`reviewed_by`
  are `ON DELETE SET NULL`; `reported_user_id` and `subject_user_id` have no FK and keep deleted ids.
  Create reports only through `insertReportWithSnapshot`, which copies the plan title, the subject
  (reported member, or host for plan reports), the subject's last 50 messages the reporter could see
  (`event_report_messages`) and identity HMACs (`user_identity_hashes` → `event_report_identities`,
  keyed by `REPORT_IDENTITY_HMAC_KEY`). `PurgeExpiredEventReports` deletes reports after 12 months
  unless `retention_hold = 1`. Startup cleanups must never delete reports because a reference is
  NULL. See `docs/safety-report-retention.md`.
- Notifications: historical content stays immutable except for explicit idempotent copy-correction
  migrations, while `action_state` separately tracks live
  action validity (`active`, `resolved`, or `unavailable`). `read` remains independent and unread
  counts include only active rows. The idempotent schema migration/backfill is in
  `server/repository_schema.go`; task types are `chat.message` and `join_request.created`.
  Lifecycle mutations eagerly invalidate related actions. Ended events (`isEventPast`) resolve chat,
  request-created (pending or already handled) and request-approved taps as `unavailable`/`event_ended` at tap time, routed to the
  same Discover `event_unavailable` notice as deleted events, because Messages no longer lists
  past-event conversations. Inbox and OS-push taps must both call
  the authenticated `POST /api/notifications/actions/resolve` boundary through
  `openNotification` in `src/context/pushRouting.ts`; never navigate from raw notification IDs or
  restore client-side entity access checks. Active request tasks land on the chat screen the request
  belongs to (the group `ChatThread`, or `OneToOneHub` for a 1:1 plan, keyed by the negative event id
  when no conversation exists yet) and then raise the `JoinRequest` Requests sheet over it once the
  push transition settles (`runAfterTransition` in `pushRouting.ts`); the server resolution carries
  `group_type` for this, and a resolution without it but with a conversation id is treated as Group
  (older servers only attach conversation ids to group requests). `JoinRequest` is the same transparent-modal sheet the in-chat requests badge
  opens (formerly `PendingRequests`); there is no full-page request review route, and `OneToOneHub`
  is 1:1-only. Inactive tasks remain as one muted historical group with no visible `Handled`/`Unavailable` status line. Read active tasks are
  never dropped from the inbox: they collapse into their own per-conversation/event group and render
  in the lighter read style, so opening a row or "Mark all as read" only lightens it. Push and inbox copy for
  known types is centralized in `notificationCopyFor` in `server/notification_payloads.go`; single
  inbox rows render stored bodies verbatim and collapsed join groups prefer structured
  `payload.senderName` over legacy body parsing.
  Every persisted inbox row is also emitted live as a `notification:new` frame over `/api/ws`
  (`emitNotificationNew` in `server/chat_hub.go`, carrying the same `NotificationView` the REST
  inbox returns); `NotificationsContext` subscribes through `useChat().subscribeToServerEvents`,
  prepends the row, bumps the unread count, and re-syncs the count on `socket:open`.
  Foreground in-app banners are removed; live inbox updates and device push notifications remain.
  Payloads carry `coverKey` for event-bearing types and `senderAvatar` only as a short remote URL
  (`payloadAvatar`), never inline base64, because the same map is the FCM data message.
  Inbox rows open through `useOpenNotifications` (`src/hooks/useOpenNotifications.ts`).
  `EventActionBadge` remains for local action confirmations.

## Working References

- Repo working rules: `AGENTS.md`
- Shared components and shared styling catalog: `report/shared-components-refactor-guide.md`
- Refactor roadmap: `report/code-refactoring-consistency-plan.md`
- Performance reports: `report/performance-consistency-audit.html` and `report/performance-baseline.html`
- Google EAS/Fly secret runbook: `docs/google-build-and-places-secrets.md`
- Google Play release (EAS build/submit, `Android Release` workflow, Play Console setup): `docs/play-store-release.md`

Before adding or refactoring UI, read `report/shared-components-refactor-guide.md`. It explains what each shared component is, where it is used, and which theme/style files act as shared CSS.

## Working Agreement

- Preserve behavior unless the task explicitly asks for behavior change.
- Prefer existing shared components, hooks, services, API helpers, and theme tokens before adding local code.
- Use `EVENT_INFO_SEPARATOR` for event cards and `EVENT_DETAILS_INFO_SEPARATOR` for Event Details rows; both live in `src/constants/display.ts`. Do not replace punctuation in normal prose or saved addresses.
- Keep screens focused on composition, state orchestration, and navigation.
- Move repeated UI, data mapping, payload construction, request helpers, haptics, and action behavior into shared components/helpers.
- Refactor one user-visible area at a time.
- Do not mix structural refactors with performance optimization unless explicitly asked.
- Do not remove user changes or unrelated untracked files.
- Update `AGENTS.md`, `CLAUDE.md`, and relevant report docs when conventions, validation commands, shared primitives, or architectural rules change.

## Shared Components And Styling

Treat "shared CSS" in React Native as:

- theme tokens in `src/theme`
- shared component-owned styles
- feature-level `.styles.ts` files for complex component-local styling

Use shared primitives before local UI:

- UI primitives: `src/components/ui`
- `IconButton` supports a compact visual size with an explicit shared hit-slop token when a header must keep the standard touch target without inheriting the button's layout height.
- Sheets: `src/components/sheets`, `BottomSheetHostProvider`, `BottomSheetModal`, `CreateEventBottomSheet`
- Event lists: `src/components/events`, including `EventListLoadState` for the shared full-page loading/error/retry presentation
- Tabbed pager wiring: `useTabbedPages` in `src/hooks/useTabbedPages.ts`. Spread `pagerProps` onto
  `AnimatedPager` and `tabsProps` onto `SegmentedControl` instead of passing `pageOffsetSV`,
  `selectedIndex` and `onChange` separately, so the tab indicator cannot stop tracking the pager.
  Selection is by value, not index, and the hook falls back to the first option when the selected one
  disappears. Discover keeps the hook for its pager but shows the sort as a `DropdownChip` + `SelectionModal`
  instead of tabs, with swiping off. `HostRequestTabs` keeps its own frozen pager and
  stays off this hook.
- Empty states: `EmptyState`
- Press motion and haptics: `ScalePressable`, `src/services/haptics.ts`
- Motion tokens: `src/theme/motion.ts`, layered beside the frozen `src/theme/springs.ts` (the four
  `Springs` preset values must not change; `src/navigation/transitions.ts` is tuned against them)
- Scrapbook entry motion: `Placed` in `src/components/motion`. It animates once per `id`, so
  `SectionList` recycling and pager page changes never replay an entry; pass a stable unique `id`.
  `tiltMode` is `entry` (settles square), `rest` (settles at a slight angle, for photo cards), or
  `none` (for overlapping avatars). Stagger is capped by `staggerDelayMs`; tilt angles come from
  `src/utils/seededRandom.ts`, shared with the confetti engine.
- Every animated primitive must honour Reanimated's `useReducedMotion()` and degrade to a static or
  opacity-only presentation. `ScalePressable`'s `tilt` and `AppTabs`' `pageOffsetSV` are opt-in;
  omitted, both components behave exactly as before.
- Dev-only logging: `logger` in `src/services/logger.ts` (use it instead of `console.*`)
- Runtime permissions: never gate a request on a derived status. `expo-modules-core` derives
  UNDETERMINED vs DENIED from its own `expo.modules.permissions.asked` SharedPreferences record, and
  Android auto-backup restores that record onto a fresh install, so a new install can report DENIED
  with `canAskAgain` false while the system has never asked. Check `granted`, otherwise request and
  let the OS decide — requesting a blocked permission is a no-op.
- User-profile gender values and onboarding order: `src/constants/profileOptions.ts` (`Male`, `Female`, `Other`); keep these separate from event-audience gender options.
- Create/Edit Event mapping: `src/screens/create-event/createEventForm.ts`
- Create/Edit Event structure: `useCreateEventForm`, `useCreateEventSheets`, and the
  `CreateEventHeader`/`CreateEventFormFields`/`CreateEventSubmitButton`/`CreateEventSheetContent`
  components in `src/screens/create-event/` (all share `CreateEventScreen.styles.ts`). Signed-in and
  signed-out creation share required-field/future-time validation and successful creation returns
  directly to My Events with its created badge/confetti.
- New Create sessions select a random loaded catalog cover through `getRandomCoverKey`; Edit keeps
  the event's existing cover.
- Create/Edit plan sheets are tinted from the cover instead of plain white: `useCoverSheetTint` samples the cover through Skia (16px downsample, saturation-weighted) and `src/utils/coverTint.ts` turns it into a near-white HSL colour (lightness 0.94, saturation capped at 0.28, neutral covers stay grey-white), passed as `surfaceColor` through `CreateEventBottomSheet` → `BottomSheetModal` → `BottomSheet`. Other sheets omit it and stay white. Tune the look with the constants in `coverTint.ts`, not per sheet. Content that paints the sheet colour itself (list fades, gaps around cover tiles, date wheel fades) reads it with `useSheetSurfaceColor()` from `@components/sheets` instead of hard-coding white, search fields use the translucent `colors.sheetFill`, and unselected option pills (Group type, Gender, cover categories) are outlined with the translucent `colors.sheetPillBorder`, the Age slider's unselected track uses `colors.sheetTrack`, so both show on white and tinted sheets alike.
- API requests: `requestJson`/`ApiError` in `src/api/client.ts`, error extraction in `src/api/errors.ts`
- API payload mappers: `src/api/mappers` (`events.ts`, `chat.ts`)
- API timeout helpers: `src/api/request.ts`
- Cover catalog search/filter: `searchCovers` in `src/utils/coverSearch.ts`; the
  catalog itself is generated by `server/cmd/covers-sync` from Drive into
  `server/assets/covers/` and `server/covers_catalog.json` — re-run it instead of
  hand-editing covers. Cover images are gitignored; deploys (and fresh clones)
  populate them with `cd server && go run ./cmd/covers-sync -fetch`

Create/Edit Event sheets open on `keyboardWillHide` (from `react-native-keyboard-controller`'s
`KeyboardEvents`), not React Native's `keyboardDidHide`. The did-event only fires once the keyboard
has finished animating, so waiting on it forced the keyboard's exit and the sheet's entry to run
back to back; the will-event fires as the dismissal starts, letting the two overlap. React Native
emits the will-events on iOS only, which is why the keyboard-controller version is used. Do not
reintroduce a settle delay after the event, and keep the fallback timer for devices that never emit
it. `BottomSheet.startOpenAnimation` resets `keyboardOffset` because entry can now begin while a
previous field's keyboard is still on screen.

Modal bottom sheets should use `BottomSheetModal` so they are coordinated by the shared host and do not stack sibling native modals on iOS. Shared keyboard avoidance keeps the home-indicator inset behind the iOS keyboard while preserving the shared base content spacing; do not add per-modal safe-area or keyboard offsets. Use `onOpened` for focus or heavy content that must wait for sheet entry. On Android 11+ (API 30), `BottomSheet` follows every IME frame through react-native-keyboard-controller's `useGenericKeyboardHandler` (never `useKeyboardHandler`, which switches the activity's soft input mode); inside a sheet's Modal window (edge-to-edge, translucent navigation bar) the reported height includes the navigation bar, so the lift is `getKeyboardTranslation({ keyboardHeight, safeAreaBottom })`. Frames move only a settled sheet (not during entry or after its close starts), a closing or shrinking keyboard only lowers it, and a hosted sheet that starts avoiding the keyboard while open (More actions → report prompt) catches up. Android 10 and older keep React Native's `keyboardDidShow`/`keyboardDidHide` lift with physical-screen keyboard-top coordinates: keyboard-controller only emulates frames there, from insets the `adjustPan` window never resizes (#190). Use `CreateEventBottomSheet` for Create/Edit Event sheet chrome so it stays on the same modal transition system.

When one open hosted sheet hands over to another (menu → confirmation), `BottomSheetHost` passes the owner id as `contentKey` and `BottomSheet` morphs instead of swapping: the old content stays mounted on top and fades out while the new fades in, and the body's height glides between them (shared values: the arriving layer mounts at opacity 0, the body's height is pinned to the old height and glides to the new content's measured height, and that glide's completion ends the morph; timings in `sheetMorph` in `src/theme/motion.ts`). Do not use Reanimated CSS `transition`s for the height: tried first, they did not animate the swap and the sheet snapped to its new size. The swap is decided in its own render: that same render pins the old height with a plain style and shows the old content, and the animated height attaches over the pin only once the glide starts. Pinning in a follow-up update, or with a freshly attached animated style, let one frame at the new height reach the screen on iOS. The outgoing layer is removed by React when the glide ends (`sheetMorph.totalMs` is only a safety net); do not use Reanimated `exiting` animations here (views held past React's removal are the Fabric "recycle a mounted view" crash pattern). Reduced motion, `snapHeight` sheets and sheets that have not settled swap instantly. Reanimated 4 leaves the last animated height on the body after the animated style detaches, so the glide ends by switching that height to `'auto'` (`morphHeightReleased`) and the body view is keyed per swap (`body-${morphSeq}`); without both, content that grew after a morph (an error line) spilled under the keyboard instead of raising the sheet, or a leftover value could override the next swap's pin. One owner can morph between its own steps the same way by passing `contentKey` to `BottomSheetModal` (the host combines it with the owner id). Both layers are fresh mounts, so keep the steps' state above the sheet, and skip mount-time effects such as `autoFocus` in the fading-out copy via `useIsOutgoingSheetLayer()` (`src/components/sheets/SheetLayerContext.ts`). The sign-in sheet does this: `SignInSheet` (Create Event passes its own flow) owns `useEmailSignInFlow` and keys the content by `providers`/`email`/`code`, resetting on `onClosed`.

Places autocomplete is country-restricted server-side only when the client supplies the ISO country
resolved from the already-granted viewer location; do not replace it with a client-only result
filter, and leave it unrestricted when the country is unavailable.

`GET /api/location/approximate` (`server/approximate_location.go`) resolves the caller's `Fly-Client-IP` to a city against a local DB-IP City Lite database (`server/assets/geoip/`, gitignored, about 130 MB, fetched by `cd server && go run ./cmd/geoip-sync`; the Dockerfile fetches it and the deploy workflow refreshes it monthly through `GEOIP_EDITION`). It answers `{city,country_code,latitude,longitude,source:"ip"}` with coordinates rounded to 2 decimals, or 204 when there is no database, a private address, or no city. The address is never logged or stored, and the IP country must not feed Places autocomplete. `APPROXIMATE_LOCATION_DEV_IP` substitutes for private addresses in local testing. The database is CC BY 4.0 and needs "IP Geolocation by DB-IP" attribution in the app.

Event Details enables an interactive stack back swipe from a left-side zone, `EVENT_DETAILS_BACK_EDGE_WIDTH` (100pt/dp, about a quarter of a phone screen); do not make it full-width or add a touch layer over the page `ScrollView` (both stopped vertical scrolling on Android). `patches/@react-navigation+stack+7.6.12.patch` (applied by the `postinstall` `patch-package` hook) maps the stack pan's gesture-handler v1 `minOffsetX`/`maxDeltaY` to v2 `activeOffsetX`/`failOffsetY`; gesture-handler v2 silently drops the v1 names, so without the patch the stack pan activates on any drag direction and cancels native scrolling wherever it receives the touch. Re-create the patch on every `@react-navigation/stack` upgrade (`stackGestureActivation.test.ts` fails if it is missing). `HostRequestTabs` `blocksExternalGesture`s the stack pan (from `GestureHandlerRefContext`), because Android also offers pager touches to the stack pan: RNGH measures its edge hitSlop against the overflowing two-page row. Inside the pager the tabs win: the only drag it releases to the stack (via `manager.fail()` in `onTouchesMove`, before its own activation) is a rightward drag on Requests that starts in the edge zone; leftward drags from the zone still page to Members, and on Members a rightward drag from anywhere returns to Requests. Vertical drags fail early to the outer `ScrollView`. Keep the stack gesture and pager boundary tied to `EVENT_DETAILS_BACK_EDGE_WIDTH`.

Do not import `expo-haptics` outside `src/services/haptics.ts`. Do not call `console.*` directly in app code; use `logger` from `src/services/logger.ts`. Hardcoded hex colors outside `src/theme` are only allowed in the documented artwork-palette files (`src/utils/avatar.ts`, `src/components/ConfettiOverlay.tsx`).

## Commands

Frontend:

```sh
npm start
npm run android
npm run ios
npm run web
npm test
npm run typecheck
npm run lint
npm run format:check
```

Backend:

```sh
cd server && go run .
cd server && go test ./...
```

`npm run lint` currently has an existing warning baseline. Do not add new warnings casually; reduce the baseline when touching files.

## Validation

For frontend changes:

- Run the narrowest relevant Jest test first: `npx jest <path-or-pattern> --runInBand --silent`
- Run `npm run typecheck` for TypeScript refactors.
- Run broader tests when changing shared components, navigation, contexts, or API helpers.
- Use Prettier on touched files rather than formatting the whole repo unless the task is a formatting pass.

For visual or interaction changes, smoke test on the connected mobile app/emulator:

- Discover
- My Events
- Create Event
- Messages
- Profile
- Event Details
- Bottom sheets and action menus
- Back navigation

For batch issue work that must be verified on the emulator, use the project-local `fix-issues-on-device` skill (`.pi/skills/`) with the `mobile-mcp` bridge tools (`.pi/extensions/mobile-mcp/`: `mobile_init`, `mobile_open_app`, `mobile_dump_ui`, `mobile_tap`, `mobile_swipe`, `mobile_type`, `mobile_key_press`, `mobile_screenshot`). Pi connects to MCP servers via extensions (no built-in MCP). The skill implements a fix → typecheck/test → `npm run android` → navigate → `mobile_screenshot` → visually verify → retry loop, using `ISSUES.md` as the status board. Verify issues sequentially (one shared emulator). After a fresh clone run `cd .pi/extensions/mobile-mcp && npm install`.

For single-change verification after implementing a feature/fix, use the `test-on-device` skill (`.pi/skills/test-on-device/`). It bypasses Google/Apple sign-in with the dev-login dummy user (backend `POST /api/dev-login` gated by `DEV_LOGIN_ENABLED=1`; client `DevLoginButton` mounted in `SignInButtons` with testID `dev-login-button`, hidden unless a `__DEV__` build's Metro sets `EXPO_PUBLIC_DEV_LOGIN_ENABLED=true` — issue #186) and drives the emulator with the `mobile_*` tools, recording pass/fail verdicts on `TEST_RUNS.md`. Full sign-in mode docs (normal vs dev-login, switching, three preset users) in `docs/dev-login.md`. Emulator-only — never targets a physical device. Per-session setup: launch `WEIF_API_36` AVD, run `cd server && DEV_LOGIN_ENABLED=1 go run .`, run Metro with `EXPO_PUBLIC_API_BASE_URL=http://10.0.2.2:8080 EXPO_PUBLIC_WS_BASE_URL=ws://10.0.2.2:8080 EXPO_PUBLIC_CHAT_ENABLED=true EXPO_PUBLIC_DEV_LOGIN_ENABLED=true npm start`, launch with `adb shell am start -n com.whoelseisfree.app/.MainActivity`, tap the `http://10.0.2.2:8081` row on the Dev Launcher screen, tap "Dev Login (tester)". If a physical device is also connected, unplug it OR start pi with `ANDROID_SERIAL=emulator-5554` (mobile-mcp runs bare `adb shell` with no `-s` and fails with `more than one device/emulator` otherwise). Never enable `DEV_LOGIN_ENABLED` in production.

## Import And Type Rules

Import order:

1. React imports.
2. React Native imports.
3. External library imports.
4. Internal alias imports.
5. Relative imports.

Prefer aliases when available:

- `@components/*`
- `@screens/*`
- `@navigation/*`
- `@theme/*`
- `@hooks/*`
- `@utils/*`
- `@context/*`
- `@api/*`
- `@services/*`
- `@assets/*`
- `@constants/*`

Keep route params typed in `src/navigation/types.ts`. Avoid `navigation as any`, `props: any`, and new broad `any` casts.

## Plan details and input-sheet contracts

- Leaving Event Details from a confirmation (Leave plan, Delete plan, plan report) closes the sheet,
  waits for its `onClosed` (forwarded by `EventActionOverlay` → `BottomSheetModal` → the shared host),
  and navigates one frame later via `exitAfterSheetCloses` in `useEventDetailsActions`; membership and
  conversation refreshes run after navigating. Resetting navigation while the sheet was still closing,
  with the page re-rendering under it, crashed Fabric on iOS ("Attempt to recycle a mounted view").
  Confirmation sheets take a `confirmLoadingLabel` ("Leaving plan...", "Deleting plan..."); there is no
  shared "Deleting..." default.
- Chat headers open the full-page `EventDetails` route with `hideBottomCTA: true`; this hides only the pinned CTA and its reserved space. Keep `readOnly` for historical/legacy read-only views. Chat plan details reuse normal host Requests/Members or Requests/Accepted tabs and plan actions, with Go back on the left and More actions on the right. A 1:1 host's person moderation menu remains a separate header IconButton.

- Group member presentation includes the host exactly once, first with a Host label and no moderation menu. Group headline counts and Members lists use the same roster. The 1:1 Accepted list remains requester-only.
- Event Details member reports carry an explicit person target; plan and person prompts must identify the same target as their submit handler. Accepted guests read their intro from More actions, not an inline Introduction section.
- `EventActionConfirm.headerAlign` defaults to left and every confirmation, removal included, uses that default; the prop is currently unused. Report-plan menu entries use normal text; destructive leaving/removal retains its warning color.
- Shared `BottomSheet` entry waits for native `onShow`, runs once per opening, and does not restart on content or viewport updates. Input sheets constrain height above the keyboard; `EventActionOverlay` keeps the CTA outside the scrollable text-entry body.

## Event Details transitions

- Event cards open Event Details through `eventDetailsScreenOptions` in `src/navigation/transitions.ts` (the standard right-slide stack transition). `EventSectionList` passes the event directly to its press handler; there is no shared cover or page overlay.
- `EventDetailsHero` renders a level, static cover with zero image-transition duration and no scroll parallax. Android has no hero shadow; iOS retains its existing shadow. Keep shared `Placed` motion on list rows and going avatars.
- `EventDetailsScreen` retains the last ready event snapshot until the route leaves, so a successful delete cannot replace the screen with the not-found fallback before its navigation reset completes.

## Animation lifecycle and measurement contracts

- Create success (including queued guest creation) uses `StackActions.popTo('Main', ...)` with MyEvents selected. Never push another Main above the submitted form: the pop owns the downward close and preserves one Main route. MyEvents consumes the created-badge parameter once while focused, after navigation interactions settle.
- `EventsContext.addUserEvent` returns after POST/local insertion; list reconciliation runs asynchronously. Confirmed creations remain in `unreconciledCreations` until observed in a list response, are cleared on session-token change, and are removed after successful deletion. A slow/stale refresh must not hold the form open or remove the just-created event.
- `EventActionBadge` mounts its animated body before entry. A single hold starts after entry finishes, and opacity/travel dismiss together; unmount cancels animation/timers and guards queued completion callbacks. The submit shimmer and delayed ScalePressable feedback must stop on cleanup and respect reduced motion.
- Expo public environment values must use static `process.env.EXPO_PUBLIC_*` access. Optional/computed access is not reliably inlined into a release bundle; verify the compiled build reaches the intended local server before emulator writes.
- Emulator performance capture lives in `scripts/performance/` (see its README). Preserve configuration and per-window traces/screenshots before/after; verify destination screenshots and distinguish app deadline misses from compositor jank. Emulator FPS is not physical-device performance. Mobile verdicts remain in `TEST_RUNS.md`.

- Create/Edit Event cover and blurred background use `expo-image` with `cachePolicy="memory-disk"`, matching card images so repeated opens can reuse decoded images. Keep their blur and image-transition design.
- `ConfettiOverlay` wraps `react-native-fast-confetti`'s `CannonConfetti` (two cannons in the bottom corners firing towards the top centre, drawn in one Skia batch on the UI thread). It mounts only while active and motion is allowed, so a hidden celebration allocates nothing; reduced motion shows nothing. The cannons fire after `motionTiming.confettiStartDelayMs` so it does not share its first frames with the "plan created" toast. Do not keep idle confetti mounted in every My Plans instance. The previous hand-rolled version (50 separately animated Skia nodes, new objects every frame) stuttered.

- Successful event creation uses `completeEventCreation`: pass the hydrated `navigationRef.getRootState()`, select My Events with a targeted tab `jumpTo` and await its render opportunity before popping the form, so dismissal reveals My Plans directly. The helper guards against a changed top route during that wait. A stack-only state snapshot can omit the child key; `navigate` also changes parent focus. Keep the nested pop destination as the unmounted-tab fallback.

- `usePrepareCreateEvent` prepares one hidden Create form after Main navigation interactions settle and the cover catalog finishes loading. Reuse an existing prepared route; never replace a live Create/Edit draft. Create-start analytics run on actual focus, and the initial default time is refreshed only when stale. Keep preparation cancellable on blur and keep hidden forms out of hit testing/accessibility through the stack preload mechanism. Create Event explicitly accepts horizontal back gestures across the screen width while retaining its upward opening/downward closing transition; short drags cancel and vertical form scrolling remains available.

- Android navigation sheet routes show their native modal only while focused, so opening an editor or chat hides the sheet and returning restores it.

Date/time picker wheels render when their sheet becomes active, with the current draft and bounds ready during entry. Do not gate these values on `onOpened` or another interaction/frame wait; keep `onOpened` for deferred focus and other content that needs settled entry. Wheel rows use fixed-height pressable text to avoid a native wrapper per value; the date, hour, and minute wheels use bounded FlatLists with fixed item layout; initial render windows include the two rows above selection, and content offsets center the selected values (middle-copy offsets for looping wheels). Cancel pending wheel-settle timers on close, reset, and unmount.

On Android, the Create/Edit Event date picker requests an inline presentation from the shared sheet host, avoiding native Dialog creation and the modal `onShow` round trip. Other sheets and iOS retain native modal presentation. The host preserves the shared spring, blocks underlying touches and accessibility until inline exit completes, and consumes hardware Back through closing. Without a host, `BottomSheetModal` remains a native modal.

### Prepared Android date picker

`CreateEventDateTimeSheet` prepares the bounded picker after Create/Edit navigation interactions settle, retains its native layout through close/reopen and other sheets, and releases it on screen blur/unmount. Early taps still open immediately. `BottomSheetModal.keepMounted` is opt-in and inline-only: the shared host retains an owner-keyed subtree, hides it from touch/accessibility while idle, and releases it with its adapter. Hidden prepared sheets must not intercept Back or keep the underlying screen disabled. Native modal owners continue sharing one modal instance. Prepared entry starts from measured sheet height, avoiding invisible full-screen travel while keeping the shared spring unchanged. Reset unconfirmed drafts and native wheel offsets while hidden; ignore late momentum callbacks after close. This trades a small bounded screen-scoped allocation for less tap-path mounting. Do not retain other sheets or add animation/frame waits to the prepared opening path without measurements.

Prepared picker rows are memoized by logical selection and calendar-day bounds. Keep visibility and late-event guards in a stable ref so opening does not replace FlatList props or reconcile every row. Hosted sheet registration and shared visibility/animation setup use layout effects; native modal entry still waits for `onShow`.

### Long text presentation

- `OverflowTextInput` (`src/components/ui/OverflowTextInput.tsx`) keeps a native single-line editor mounted: horizontal scrolling and full values while focused, a native tail-ellipsized preview at rest (the ellipsis sits at the field's end, not at a word boundary; issue 187). On Android the hidden editor is rewound to the start and its theme padding reset while unfocused, so focusing never shifts the text. Use it for Create/Edit plan titles and profile/onboarding names; never truncate saved values. The preview is hidden from accessibility so the native editor remains the sole accessible field.
- Displayed user names use one line with tail ellipsis in profiles, host lines, request/member rows, and chat. Join notices truncate only the name and keep the action suffix visible; conversation previews (Messages, 1:1 hub) shorten them to the first name through `shortenJoinNotice` (`src/utils/joinNotice.ts`, which also owns the `parseJoinNotice` pattern the thread uses); stored chat bodies stay unchanged.
- Invite/report sheets use one bounded native multiline input, without an enclosing scroll view competing for text gestures. Keep the CTA outside that input. Request to join focuses its intro field from the sheet's `onOpened` (`EventActionOverlay`), like the Create Event Description editor, so the keyboard rises right after entry and the sheet rides it (#190); the report prompt waits for a tap. Shared multiline/composer height and line-height limits live in `componentTokens.input`. Chat preserves its pill shape for one line and uses `radii.xl` for multiline drafts.

## Custom plan covers and age selection

- Create/Edit Age uses `AgeRangeContent` with the linked fast range slider over
  18–`PLAN_AGE_SLIDER_MAX` (50), integer steps and a minimum five-year gap. Most plans
  target ~20–40, so the slider stops at 50 and its end stop means "50+", saved as
  `PLAN_AGE_MAX` (99); `toAgeSliderRange`/`fromAgeSliderRange` in `planAgeRange.ts` map
  between them, and a saved max above 50 is kept unless a handle moves. Each thumb's
  value is drawn above it (a finger covers the thumb), positioned from the slider's
  thumb math, so the sheet shows no separate range summary. Temporary drafts stay in the sheet;
  only Done commits. Legacy separated/out-of-bounds ranges require deliberate
  replacement and survive unrelated edits.
- New selections carry `age_selection_mode: "range"`; preserve legacy presets and
  their original 18–60 bounds. Mode-aware display lives in `eventDisplay.ts`;
  range-mode full bounds show All ages in Event Details and are omitted from event
  cards (like legacy all-age plans), an open top (`PLAN_AGE_MAX`) shows `min+`,
  otherwise the actual min - max everywhere.
- `patches/react-native-fast-range-slider+0.4.0.patch` adds adjustable accessibility
  actions, canceled-gesture cleanup and width synchronization to the pinned slider.
  Recreate/check it on package upgrades. The slider's gap prop is pixels; convert
  the five-year gap using measured width. Size tokens live in `componentTokens.ageRange`
  and the opaque thumb shadow in `shadows.ageRangeThumb`.
- `CoverPickerContent` owns the optional pinned Add your own cover action and grid
  clearance. Defer the heavy grid via the shared sheet's onOpened readiness signal.
  Custom covers never highlight a catalog choice. Bottom-sheet native modals use
  translucent status/navigation bars and an unpadded full-window backdrop.
- Photo picking lives in `usePickEventCover`, multipart transport in
  `src/api/eventCovers.ts`, and upload preparation/retry caching in EventsContext.
  Prefer cover_url/coverUrl over catalog fallback on every display surface.
  Keep uploads separate from the generated catalog; see
  `docs/custom-plan-covers-and-age-groups.md` for storage and compatibility.
