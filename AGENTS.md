# AGENTS.md

Project guide for coding agents and contributors working in this repository.

## Keep This File Updated

- Update this file whenever project conventions, folder structure, validation commands, shared primitives, or architectural rules change.
- If a refactor introduces a new preferred component, hook, service, API helper, or theme token, document the new rule here in the same change.
- If a command stops working or a new validation gate is added, update the commands below.
- Keep this file concise and practical. It should describe how to work in the repo, not every implementation detail.

## Working References

- Shared components and shared styling catalog: `report/shared-components-refactor-guide.md`.
- Refactor roadmap and rationale: `report/code-refactoring-consistency-plan.md`.
- Performance reports: `report/performance-consistency-audit.html` and `report/performance-baseline.html`.
- Google EAS/Fly secret runbook: `docs/google-build-and-places-secrets.md`.
- Google Play release (EAS build/submit, `Android Release` workflow, Play Console setup): `docs/play-store-release.md`.
- Sign-in modes (Google/Apple vs dev-login bypass): `docs/dev-login.md`.
- Admin provisioning, verification, and revocation: `docs/admin-support-operations.md`.
- Mobile QA history reports belong in `report/`; keep QA history separate from evergreen component/style references.

Read `report/shared-components-refactor-guide.md` before adding or refactoring UI. It explains what each shared component is, where it is used, and which theme/style files act as shared CSS.

## Working Agreement

- Preserve behavior unless the task explicitly asks for behavior change.
- Prefer the existing shared component, hook, service, API helper, or theme token before creating a local implementation.
- Treat "shared CSS" in React Native as `src/theme` tokens plus shared component-owned styles.
- Keep screens focused on composition, state orchestration, and navigation. Move repeated UI, data mapping, and side-effect helpers into shared components/hooks/helpers.
- Refactor one user-visible area at a time and avoid mixing structural refactors with performance optimization unless the task asks for both.
- When a change introduces or changes a shared primitive, token, helper, validation command, or architectural rule, update `AGENTS.md`, `CLAUDE.md`, and the relevant report reference in the same change.
- Do not remove user changes or unrelated untracked files.

## Build And Run Commands

- Frontend dev server: `npm start`
- Android app: `npm run android`
- iOS app: `npm run ios`
- Web app: `npm run web`
- Frontend tests: `npm test`
- Frontend typecheck: `npm run typecheck`
- Frontend lint: `npm run lint`
- Format all files: `npm run format`
- Check formatting: `npm run format:check`
- Backend server: `cd server && go run .`
- Backend tests: `cd server && go test ./...`

`npm run lint` currently exits green with a warning baseline for existing import order, hardcoded colors, `any`, hook, and unused-code debt. Do not add new warnings casually; prefer reducing the baseline as files are touched.

## Architecture

- Admin Support Inbox: Contact Us and Feedback submissions remain in `help_submissions`. Sensitive
  reads and status changes go through the session- and database-role-protected `/api/admin` group;
  runtime authorization checks `admin_users.user_id`, never a client-provided email or embedded admin
  secret. `ADMIN_BOOTSTRAP_EMAILS` is provisioning-only: matching verified accounts receive a
  persistent user-ID grant and the secret can then be removed. Client transport/mapping lives in
  `src/api/adminHelp.ts`, list state in `useAdminHelpSubmissions`, access discovery in
  `useAdminAccess`, and the Profile-only routes are `AdminSupportInbox` and
  `AdminSupportSubmission`. Keep support content out of analytics and routine logs; retention rules
  are in `docs/support-data-retention.md`.
- Notifications inbox: server-side notification history is persisted in the `notifications` table (one row per recipient per push) by `recordAndSendPushToUser`/`recordAndSendPushToUsers` on `*ChatHub` (`server/notification_recorder.go`). Persistence is best-effort — a row-insert failure logs and still sends the FCM push, so push delivery is never degraded. Historical content (`type`/title/body/payload) stays immutable except for explicit idempotent copy-correction migrations; live action validity is tracked separately by `action_state` (`active`, `resolved`, `unavailable`), nullable reason/resolution time, and optional `join_request_id`. `read` remains independent, and unread counts include only active rows. Task types are `chat.message` and `join_request.created`; the other four current types are outcomes. The idempotent startup migration/backfill lives in `repository_schema.go`, and lifecycle mutations eagerly invalidate related actions before their live records disappear. The authenticated `POST /api/notifications/actions/resolve` endpoint is the authorization and destination boundary for both inbox and OS-push taps; new pushes include recipient-specific `notificationId` values when persistence succeeds, while validated type/entity hints remain the permanent no-ID fallback. Client opening is centralized in `openNotification` (`src/context/pushRouting.ts`): active request tasks land on the chat screen the request belongs to (the group `ChatThread`, or `OneToOneHub` for a 1:1 plan, keyed by the negative event id when no conversation exists yet) and then raise the `JoinRequest` Requests sheet over it after the push transition settles (`runAfterTransition`, defaulting to `InteractionManager.runAfterInteractions`); the resolution carries `group_type` for this (a resolution without `group_type` but with a conversation id is treated as Group, matching older servers). `JoinRequest` is the transparent-modal sheet the in-chat requests badge opens (formerly `PendingRequests`); there is no full-page request review route. Unavailable actions route safely to Discover with a typed one-shot `EventActionOverlay` notice. `NotificationsContext` (`src/context/NotificationsContext.tsx`) holds the paginated list + unread count and applies successful resolutions optimistically; inactive tasks render as one muted history group with no visible `Handled`/`Unavailable` status line (the row stays lighter; only the accessibility label explains the Discover redirect). Push and inbox copy for known types is owned by `notificationCopyFor` in `server/notification_payloads.go`; single inbox rows render the stored body verbatim while collapsed groups use structured payload fields. The bell `IconButton` + `CountBadge` live in the Profile header and are hidden for signed-out users. Live delivery: every persisted row is also emitted as a `notification:new` WebSocket frame (`emitNotificationNew` in `server/chat_hub.go`, routed through the hub's `direct` channel so `clientsByUser` is only read on the hub goroutine; payload is the REST `NotificationView`). `ChatContext` exposes `subscribeToServerEvents` (plus a synthetic `socket:open` event) so `NotificationsContext` can prepend the row, bump the unread count, and re-sync the count after reconnects without owning the socket. Foreground in-app banners are removed; live inbox updates and device push notifications remain. Inbox taps use `useOpenNotifications` (`src/hooks/useOpenNotifications.ts`). Payload decoration: every event-bearing push/inbox payload carries `coverKey`, and chat/join-request payloads carry `senderAvatar` only when `payloadAvatar` accepts it (short `http(s)` URL; inline base64 avatars are dropped so the FCM data message stays under 4 KB). Inbox rows (`resolveNotificationCoverUri`) prefer these payload fields, then the conversation roster / loaded events, then a seeded monogram.

- Frontend: React Native Expo app in `src/`, with root app setup in `App.tsx` and root registration in `index.ts`.
- Installed app display name is `weif` (all lowercase, issue #176), owned by `expo.name` in
  `app.config.js`. Keep the Expo slug, EAS project ID, package/bundle identifiers, and persisted
  storage keys stable during display-name changes. Native rebuilds and separate store-title
  updates are required; see `docs/app-display-name.md`.
- Android system navigation keeps `androidNavigationBar.enforceContrast: true` in
  `app.config.js`, preserving the original appearance on other screens. Only the focused
  Onboarding route opts out via `useOnboardingNavigationBar` and the local
  `modules/system-navigation` Expo module; blur/unmount restores protection, and the native
  module reapplies the route policy after activity resume/recreation. Keep safe-area clearance.
  Native changes require prebuild/rebuild; store builds include this module from 1.0.0.
- Onboarding backgrounds fill an unpadded page; safe-area/header padding belongs on the
  pager. Use the page's `onLayout` size for the radial gradient and pass that `pageSize`
  to `AvatarEditBadge` so Android's explicit backdrop matches. Do not size background
  coverage from `useWindowDimensions`: Android can report a shorter app window than
  the edge-to-edge page. Verify the compiled APK's default contrast setting and onboarding's runtime override;
  source config and version name alone do not prove an installed binary has the fix.
- Backend: Go Gin HTTP server in `server/` using SQLite.
- API: REST endpoints plus WebSocket chat at `/api/ws`.
- Navigation: React Navigation stack and bottom tabs in `src/navigation`.
- Bottom-tab chrome lives in `src/navigation/TabBarBackground.tsx`: `FrostedSurface` with real blur so scrolling content is softened on both platforms. iOS keeps intensity 54 beneath the 60% white overlay. Android draws no overlay view, because Dimezis captures every view drawn above the blur (overlay, top border, tab icons) and blurred the overlay into the material before drawing it again; it passes `tabBarAndroidMaterial` (`src/theme/materials.ts`: LIGHT tint intensity 90, alpha 0.70, which carries the overlay's white after the blur, and a 19.5dp sigma converted for the screen density by `androidBlurReductionFactor`). The captured icons leave a faint glow around the active one (5.6/255); a weaker tint shows more of the list and more glow (#190).
- Inactive bottom-tab scenes always use `pointerEvents` and accessibility props. On Android, hide them with `opacity: 0` rather than `display: 'none'` so event covers and empty-state art remain laid out and do not repaint on return; `importantForAccessibility="no-hide-descendants"` keeps hidden controls out of the accessibility tree. iOS keeps its existing native scene visibility. Pagers receive navigation focus through `AnimatedPager.isActive` and rebuild their gesture on the next frame for Android re-registration without remounting list content.
- `EventsContext.hasLoadedEvents` records when the first events request settles, including an empty or failed result. Discover shows its full-page loader only until then; routine focus refreshes keep resolved empty event pages mounted. Messages waits for its first focus refresh for the current user before showing an empty state, then keeps that state mounted during later refreshes.
- Bottom obstructions are normalized by `src/utils/bottomObstruction.ts`. When a surface already reserves the system bottom safe area, subtract that inset from keyboard movement or absolute tab-bar clearance instead of counting it twice. Use keyboard-top coordinates when available; do not add device-size or navigation-mode spacing branches in screens.
- `ChatThreadScreen` leaves via `StackActions.pop(n)` when its active conversation is cleared underneath it (e.g. the conversations refresh no longer lists it), sized to remove the thread plus every sheet stacked above it; a plain `goBack` only dismisses the top sheet and strands the thread on its spinner. Past-event conversations are excluded from `selectConversationsForUser`, so the resolver treats an ended event (`isEventPast`) like a deleted one for `chat.message`, `join_request.created` (pending or already handled) and `join_request.approved`: status `unavailable`, reason `event_ended`, destination `events`, and the client shows the same Discover `event_unavailable` notice as `event_deleted`. This is applied at tap time only; rows stay active in the inbox until opened.
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
- State: React Context providers currently handle auth, events, chat, push, covers, bloom, and notifications state.
- First-launch notification and location prompts are owned by Discover after `BloomContext.transitionComplete`; never request system permissions from `App.tsx`, provider mount effects, or the splash route. `PushContext.requestPushPermission` serializes notification authorization and token registration only observes existing grants; `useViewerLocation` inspects permission silently on mount and exposes `requestPermission`, which Discover reaches only through the location sheet (`useLocationPermissionPrompt` + `LocationPermissionSheet` in `src/components/location-permission/`): offered once per install (`whoelseisfree.locationPromptSeen` in SecureStore) when location is not `granted` (and, on iOS, `canAskAgain`); Continue closes the sheet and requests on `onClosed`, Close skips the system prompt. Keep the prompts serialized so native dialogs cannot overlap: the location sheet (and any system prompt it leads to) first, then notifications. Discover has no Nearest sort: its header is the Nearby `StatusChip` and an `Upcoming ▾` `DropdownChip` whose "Sort by" sheet is the shared `SelectionModal` (Create Event's pill options without Done: omitting `onConfirm` hides it, and a tap applies the sort and closes) listing Upcoming and Newest; the pager keeps both lists mounted with swiping off. `useDiscoverLocation` measures "near" from the precise device location while the Nearby chip is on and permission is granted, else the approximate network area from `/api/location/approximate` (fetched only when precise is not in use, cached for an hour; pull-to-refresh retries a failed lookup), else none, which shows every plan. Both keep plans within `LOCAL_RADIUS_KM` plus the viewer's own. The Nearby `StatusChip` (accessibility role switch) is filled only in precise mode. It is an in-app switch: tapping it on turns it off and saves that (`whoelseisfree.discoverNearbyOff` in SecureStore), because the app cannot revoke its own permission and the next launch would otherwise switch it back on; tapping it off with permission granted turns it on. Without permission a `DiscoverLocationSheet` always stands in the way (`useDiscoverLocationSheet`): the shared `LocationSheetContent` permission copy, whose Continue asks the system only after the sheet closes, or, once `useViewerLocation().isPromptBlocked` (trusted on iOS, and on Android only after this session has asked), "Location is off for weif" with Open settings. Any change of permission to granted (chip sheet, startup sheet or Settings) switches Nearby on and clears the saved off. The chip ignores the once-per-install record. `useViewerLocation` re-checks silently whenever the app returns to the foreground, so a grant or a revoke in Settings shows up (iOS does not restart the app on revoke); while still granted it checks only the status, without reloading the position.
- Theme: shared tokens live in `src/theme`, including colors, spacing, typography, springs, radii, shadows, layout, and component tokens.
- Event metadata punctuation: use `EVENT_INFO_SEPARATOR` for event cards and `EVENT_DETAILS_INFO_SEPARATOR` for Event Details rows; both live in `src/constants/display.ts`. Keep normal prose and saved address punctuation unchanged.
- Event-card metadata in Discover, My Events, and Past Plans uses `formatEventCardMetaLine` (`src/utils/eventDisplay.ts`), normally through `toEventCardItem`. Pass `groupType`, `gender`, `minAge`, and `maxAge`; unrestricted gender/age are omitted. Keep verbose audience copy for Event Details.
- User-profile gender values and their onboarding order live in `src/constants/profileOptions.ts`; keep them ordered as `Male`, `Female`, `Other` and separate from the event-audience options in `src/constants/eventOptions.ts`.
- Shared UI primitives live in `src/components/ui`; use them before adding local button, icon button, text field, checkbox, separator, section header, or tab implementations. `IconButton` may use a compact visual size with an explicit shared hit-slop token when header alignment must stay independent of its touch target.
- Shared sheet primitives live in `src/components/sheets`; use `BottomSheetHostProvider` for modal-sheet coordination and `BottomSheet`, `SheetHeader`, and `SheetActionList` for sheet surfaces and action menus before adding local sheet chrome.
- Create/Edit Event sheets open on `keyboardWillHide` (from
  `react-native-keyboard-controller`'s `KeyboardEvents`), not React Native's `keyboardDidHide`.
  The did-event only fires once the keyboard has finished animating, so waiting on it forced the
  keyboard's exit and the sheet's entry to run back to back; the will-event fires as the dismissal
  starts, letting the two overlap. React Native emits the will-events on iOS only, which is why the
  keyboard-controller version is used. Do not reintroduce a settle delay after the event, and keep
  the fallback timer for devices that never emit it. `BottomSheet.startOpenAnimation` resets
  `keyboardOffset` because entry can now begin while a previous field's keyboard is still on screen.
- Modal bottom sheets should go through `BottomSheetModal` so iOS uses the shared host instead of stacking sibling native modals. Shared keyboard avoidance keeps the home-indicator inset behind the iOS keyboard while preserving the shared base content spacing; do not add per-modal safe-area or keyboard offsets. `onOpened` is the single entry-settled signal for deferred keyboard focus or heavy sheet content. On Android 11+ (API 30), `BottomSheet` follows every IME frame through react-native-keyboard-controller's `useGenericKeyboardHandler` (never `useKeyboardHandler`, which switches the activity's soft input mode); inside a sheet's Modal window (edge-to-edge, translucent navigation bar) the reported height includes the navigation bar, so the lift is `getKeyboardTranslation({ keyboardHeight, safeAreaBottom })`. Frames move only a settled sheet (not during entry or after its close starts), a closing or shrinking keyboard only lowers it, and a hosted sheet that starts avoiding the keyboard while open (More actions → report prompt) catches up. Android 10 and older keep React Native's `keyboardDidShow`/`keyboardDidHide` lift with physical-screen keyboard-top coordinates: keyboard-controller only emulates frames there, from insets the `adjustPan` window never resizes (#190). Use `CreateEventBottomSheet` for Create/Edit Event sheet chrome so it stays on the same modal transition system.
- When one open hosted sheet hands over to another (menu → confirmation), `BottomSheetHost` passes the owner id as `contentKey` and `BottomSheet` morphs instead of swapping: the old content stays mounted on top and fades out while the new fades in, and the body's height glides between them (shared values: the arriving layer mounts at opacity 0, the body's height is pinned to the old height and glides to the new content's measured height, and that glide's completion ends the morph; timings in `sheetMorph` in `src/theme/motion.ts`). Do not use Reanimated CSS `transition`s for the height: tried first, they did not animate the swap and the sheet snapped to its new size. The swap is decided in its own render: that same render pins the old height with a plain style and shows the old content, and the animated height attaches over the pin only once the glide starts. Pinning in a follow-up update, or with a freshly attached animated style, let one frame at the new height reach the screen on iOS. The outgoing layer is removed by React when the glide ends (`sheetMorph.totalMs` is only a safety net); do not use Reanimated `exiting` animations here (views held past React's removal are the Fabric "recycle a mounted view" crash pattern). Reduced motion, `snapHeight` sheets and sheets that have not settled swap instantly. Reanimated 4 leaves the last animated height on the body after the animated style detaches, so the glide ends by switching that height to `'auto'` (`morphHeightReleased`) and the body view is keyed per swap (`body-${morphSeq}`); without both, content that grew after a morph (an error line) spilled under the keyboard instead of raising the sheet, or a leftover value could override the next swap's pin. One owner can morph between its own steps the same way by passing `contentKey` to `BottomSheetModal` (the host combines it with the owner id). Both layers are fresh mounts, so keep the steps' state above the sheet, and skip mount-time effects such as `autoFocus` in the fading-out copy via `useIsOutgoingSheetLayer()` (`src/components/sheets/SheetLayerContext.ts`). The sign-in sheet does this: `SignInSheet` (Create Event passes its own flow) owns `useEmailSignInFlow` and keys the content by `providers`/`email`/`code`, resetting on `onClosed`.
- Event Details enables an interactive stack back swipe from a left-side zone, `EVENT_DETAILS_BACK_EDGE_WIDTH` (100pt/dp, about a quarter of a phone screen); do not make it full-width or add a touch layer over the page `ScrollView` (both stopped vertical scrolling on Android). `patches/@react-navigation+stack+7.6.12.patch` (applied by the `postinstall` `patch-package` hook) maps the stack pan's gesture-handler v1 `minOffsetX`/`maxDeltaY` to v2 `activeOffsetX`/`failOffsetY`; gesture-handler v2 silently drops the v1 names, so without the patch the stack pan activates on any drag direction and cancels native scrolling wherever it receives the touch. Re-create the patch on every `@react-navigation/stack` upgrade (`stackGestureActivation.test.ts` fails if it is missing). `HostRequestTabs` `blocksExternalGesture`s the stack pan (from `GestureHandlerRefContext`), because Android also offers pager touches to the stack pan: RNGH measures its edge hitSlop against the overflowing two-page row. Inside the pager the tabs win: the only drag it releases to the stack (via `manager.fail()` in `onTouchesMove`, before its own activation) is a rightward drag on Requests that starts in the edge zone; leftward drags from the zone still page to Members, and on Members a rightward drag from anywhere returns to Requests. Vertical drags fail early to the outer `ScrollView`. Keep the stack gesture and pager boundary tied to `EVENT_DETAILS_BACK_EDGE_WIDTH`.
- Tabbed pager screens wire their tab strip and pager through `useTabbedPages` (`src/hooks/useTabbedPages.ts`). Spread its `pagerProps` onto `AnimatedPager` and `tabsProps` onto `SegmentedControl` rather than passing `pageOffsetSV`, `selectedIndex` and `onChange` by hand: Discover and My Events already shared both components and the indicator still drifted apart, because the offset can be given to the pager and forgotten on the tabs. Selection is tracked by value, not index; the hook falls back to the first option when the selected one leaves the list. Discover keeps the hook for its pager but shows the sort as a `DropdownChip` + `SelectionModal` instead of tabs, with swiping off. Event Details' `HostRequestTabs` keeps its own direction-locked pager and is deliberately not on this hook.
- Shared event-list primitives live in `src/components/events`; use `EventSectionList`, `EventListPage`, `EventListLoadState`, and `eventListSections` helpers before duplicating event card lists, loading/error presentation, or date grouping in screens.
- Past Plans pages through `src/api/pastEvents.ts` and `usePastEvents` (25 items/page, synchronous request lock, cancellation, session reset, de-duplication, retry, and unchanged-item reconciliation). Immediate focus returns reuse loaded pages; after 60 seconds, focus refresh reconciles the loaded depth. Pull-to-refresh starts a new first page. `EventSectionList.onEndReached`/`footer` own pagination rendering; opt into `entryAnimation="initial"` to bound entry motion across date sections and later pages. `/api/events/past?limit=25&cursor=...` returns `{data,next_cursor}` with a viewer-bound cursor ordered by normalized UTC schedule, creation time, and ID; a fixed cutoff excludes newly ended plans until refresh. No-query requests retain the legacy full-history response for installed clients. Ownership/membership indexes avoid scanning unrelated history for eligibility.
- Read-only Event Details uses `ReadOnlyEventDetails` (`src/screens/event-details/`) with one animated FlatList: hero/info/member heading in a dynamic header and shared `EventMemberRow` rows. Do not nest a vertical member list inside a ScrollView. Active-plan host pagers and overlay details retain their existing scroll paths.
- Shared component/style documentation lives in `report/shared-components-refactor-guide.md`; update it when shared components, shared style files, or theme-token ownership changes.
- Create/Edit Event form mapping lives in `src/screens/create-event/createEventForm.ts`; keep payload construction, edit hydration, guest draft mapping, and date normalization there instead of rebuilding them in `CreateEventScreen`. New Create sessions choose a random loaded catalog cover through `getRandomCoverKey`; Edit always preserves the event cover.
- Create/Edit plan sheets are tinted from the cover instead of plain white: `useCoverSheetTint` samples the cover through Skia (16px downsample, saturation-weighted) and `src/utils/coverTint.ts` turns it into a near-white HSL colour (lightness 0.94, saturation capped at 0.28, neutral covers stay grey-white), passed as `surfaceColor` through `CreateEventBottomSheet` → `BottomSheetModal` → `BottomSheet`. Other sheets omit it and stay white. Tune the look with the constants in `coverTint.ts`, not per sheet. Content that paints the sheet colour itself (list fades, gaps around cover tiles, date wheel fades) reads it with `useSheetSurfaceColor()` from `@components/sheets` instead of hard-coding white, search fields use the translucent `colors.sheetFill`, and unselected option pills (Group type, Gender, cover categories) are outlined with the translucent `colors.sheetPillBorder`, the Age slider's unselected track uses `colors.sheetTrack`, so both show on white and tinted sheets alike.
- Create/Edit Event screen structure lives in `src/screens/create-event/`: form state in `useCreateEventForm` (reducer over `CreateEventFormState`), sheet routing/keyboard handoff in `useCreateEventSheets`, and render pieces in `CreateEventHeader`, `CreateEventFormFields`, `CreateEventSubmitButton`, and `CreateEventSheetContent`. `CreateEventScreen` stays composition plus submission/navigation orchestration; the pieces share `CreateEventScreen.styles.ts` and the `createField*`/`createText*` theme tokens.
- Create-plan validation is shared by signed-in and signed-out flows: name, description, and location are required, and the scheduled time must be in the future. Successful creation returns directly to My Events with its existing created badge/confetti; do not add an intermediate success route.
- Shared request helpers live in `src/api`: `requestJson` and `ApiError` in `src/api/client.ts` (base-URL prefixing, auth header, timeout, JSON fallback, typed errors), error extraction in `src/api/errors.ts`, timeout/abort primitives in `src/api/request.ts`, and API payload mappers in `src/api/mappers` (`events.ts`, `chat.ts`, `notifications.ts`). Use them instead of duplicating fetch/auth-header/error boilerplate inside contexts and hooks.
- `requestJson` accepts an optional caller `signal`; `createRequestTimeout` combines it with the timeout and removes the cancellation listener on cleanup. Use this for screen/session request lifetimes instead of replacing the timeout signal.
- Session-expired navigation is wired via the `onSessionExpired` prop on `AuthProvider` (set in `App.tsx`); contexts must not import navigation directly. Inbox-tab and bell routing goes through `routeFromNotification` in `src/context/pushRouting.ts` (inbox-only; the OS push-tap `handleNotificationTap` is a separate, frozen path).
- Motion tokens live in `src/theme/motion.ts` beside the frozen `src/theme/springs.ts`; keep the four `Springs` presets' values unchanged because `src/navigation/transitions.ts` is tuned against them. Scrapbook entry motion goes through `Placed` in `src/components/motion` (animates once per `id`, so list recycling and pager page changes never replay an entry), with capped stagger from `staggerDelayMs` and deterministic tilt from `src/utils/seededRandom.ts`. Every animated primitive must honour Reanimated's `useReducedMotion()` and degrade to a static or opacity-only presentation. `ScalePressable`'s `tilt` prop and `AppTabs`' `pageOffsetSV` prop are both opt-in: omitted, those components behave exactly as before.
- Runtime permissions: never gate a request on a derived permission status. `expo-modules-core` computes UNDETERMINED vs DENIED from its own `expo.modules.permissions.asked` SharedPreferences record, and Android auto-backup restores that record onto a fresh install (the generated backup rules include every sharedpref except `SecureStore`), so a new install can report DENIED with `canAskAgain` false while the system has never actually asked. Check `granted`, and otherwise call the request and let the OS decide; requesting a genuinely blocked permission is a no-op. `useViewerLocation` and both `ImagePicker.requestMediaLibraryPermissionsAsync` call sites follow this.
- Haptics are centralized in `src/services/haptics.ts`; no other source file should import `expo-haptics`.
- Logging goes through the dev-only `logger` in `src/services/logger.ts`; do not call `console.*` directly in app code (tests may still spy on `console`).
- Hardcoded hex colors are only allowed in `src/theme` and the documented artwork-palette exceptions: `src/utils/avatar.ts` and `src/components/ConfettiOverlay.tsx`.
- The event cover catalog is generated: `server/cmd/covers-sync` downloads the team Drive folder into `server/assets/covers/` and writes `server/covers_catalog.json` (embedded into the server binary). Re-run `cd server && go run ./cmd/covers-sync` when Drive contents change; do not hand-edit the catalog or assets. The images themselves are gitignored (public repo) — only the catalog is committed, and the Docker build runs `go run ./cmd/covers-sync -fetch` to download the cataloged images at deploy time. After a fresh clone, run that fetch command once to populate `server/assets/covers/`. Cover search/filtering in the app goes through `searchCovers` in `src/utils/coverSearch.ts`.
- Places autocomplete is country-restricted in `server/places_handler.go` only when the client supplies the ISO country resolved from the already-granted viewer location. Pass it through to Google Places `includedRegionCodes`; leave search unrestricted when permission or reverse geocoding is unavailable.
- `GET /api/location/approximate` (`server/approximate_location.go`) resolves the caller's `Fly-Client-IP` to a city against a local DB-IP City Lite database (`server/assets/geoip/`, gitignored, about 130 MB, fetched by `cd server && go run ./cmd/geoip-sync`; the Dockerfile fetches it and the deploy workflow refreshes it monthly through `GEOIP_EDITION`). It answers `{city,country_code,latitude,longitude,source:"ip"}` with coordinates rounded to 2 decimals, or 204 when there is no database, a private address, or no city. The address is never logged or stored, and the IP country must not feed Places autocomplete. `APPROXIMATE_LOCATION_DEV_IP` substitutes for private addresses in local testing. The database is CC BY 4.0 and needs "IP Geolocation by DB-IP" attribution in the app.
- Keep navigation route params typed in `src/navigation/types.ts`; use `NavigatorScreenParams` for nested navigators and avoid `navigation as any` casts for route jumps. The `Notifications` route (`NotificationsScreen`) is a top-level stack screen reachable from the Profile header bell.
- Navigation-specific surfaces and status colors should use named tokens from `src/theme/colors.ts`, not local hex or rgba literals in `AppNavigator`.
- Tests: Jest tests live near source files under `__tests__`.

## Import Rules

Use this order:

1. React imports.
2. React Native imports.
3. External library imports.
4. Internal alias imports.
5. Relative imports.

Prefer aliases over deep relative paths when an alias exists:

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

Keep aliases aligned across `tsconfig.json`, `babel.config.js`, and `jest.config.js`.

## TypeScript Rules

- Strict TypeScript is expected.
- Use interfaces or named types for component props, hook return values, context values, and API payloads.
- Avoid `any`. If external data is unknown, isolate parsing/casting in adapter or mapper files.
- Keep navigation params typed in `src/navigation/types.ts`.
- Do not spread `navigation as any` or `props: any` into new code. Add proper route/screen types instead.
- Prefer pure mapper functions for API payload conversion.

## React Native Component Rules

- Use functional components and hooks.
- Keep screens as composition layers. Move reusable UI, data derivation, and actions into components/hooks.
- Prefer shared primitives over duplicating local UI:
  - buttons
  - icon buttons
  - text fields
  - checkbox rows
  - empty states
  - list separators
  - section headers
  - sheets
  - tabs/segmented controls
  - event rows and member/request rows
- If two UI elements look like the same interaction, they should use the same component or hook so styling, animation, haptics, accessibility, loading, and disabled behavior stay consistent.
- Local one-off animation is allowed only for genuinely unique screen moments, not for common buttons, cards, tabs, sheets, menus, or CTAs.

## Styling And Theme Rules

- Prefer tokens from `src/theme` over raw values.
- Do not introduce new hardcoded colors, radii, shadows, spacing, button heights, or overlay opacity unless there is a specific reason.
- Add missing tokens before spreading a new repeated value across screens.
- Use `src/theme/radii.ts`, `shadows.ts`, `layout.ts`, and `components.ts` for repeated radius, shadow, screen padding, hit slop, z-index, button, input, icon, avatar, overlay, and segmented-control values.
- Shared components should own their visual states: default, pressed, disabled, loading, selected, error, and destructive.
- Keep brand-specific or screen-specific tokens named and centralized.
- Treat "shared CSS" in React Native as shared theme tokens plus shared components.
- Use feature-level `.styles.ts` files for complex component-local styling that is not broadly reusable yet. If a style pattern repeats across features, promote it to a theme token or shared component.

## Motion And Haptics Rules

- Shared UI components should own their motion and feedback behavior.
- Use semantic helpers from `src/services/haptics.ts` for feedback such as `selection`, `light`, `submit`, `success`, `warning`, `error`, and `destructive`.
- Do not import `expo-haptics` outside `src/services/haptics.ts`.
- Repeated interactions should share animation constants or component-level motion tokens.
- Bottom sheets, action menus, buttons, cards, tabs, and CTAs should feel consistent across the app.

## State And API Rules

- Keep API transport and payload normalization out of screen components.
- Prefer shared API helpers for auth headers, JSON parsing, timeout handling, and error handling once they exist.
- Keep context providers focused on state orchestration, not large transport implementations.
- Split large contexts by data/actions when high-churn state causes broad rerenders.
- Avoid navigation side effects inside contexts unless there is no practical alternative.
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

## Refactoring Rules

- Preserve behavior unless the task explicitly asks for behavior change.
- Refactor one user-visible area at a time.
- Start with low-risk shared foundations before large screen rewrites.
- Keep route names and navigation params stable during UI extraction.
- Do not combine structural refactors with performance optimizations unless the task asks for both.
- When extracting a component, move only the code required for that component and keep tests passing.
- Prefer mechanical extraction first, then cleanup.
- Do not remove user changes or unrelated untracked files.

## Current Refactor Plan

The working refactor roadmap is documented in:

- `report/code-refactoring-consistency-plan.md`
- `report/shared-components-refactor-guide.md`

Use that plan for ordering:

1. Guardrails and tooling.
2. Theme tokens.
3. Shared UI primitives.
4. Semantic haptics and pressables.
5. Sheet and overlay consolidation.
6. Event list refactor.
7. Event Details decomposition.
8. Create/Edit Event form refactor.
9. API client and context boundaries.
10. Navigation cleanup.
11. Final consistency pass.

Completed so far: guardrails/tooling, expanded theme tokens, shared UI primitives, semantic haptics/pressables, sheet/action overlay foundations, shared event-list foundations, Event Details overlay-route extraction, Create/Edit Event form mapping helpers, shared API request timeout helpers, typed nested navigation params, navigation color token cleanup, and mobile layout/accessibility fixes for the shared foundations.

## Testing And Validation

For frontend changes, run the narrowest relevant test first, then broader validation when the change has shared impact.

Recommended commands:

- Targeted Jest test: `npx jest <path-or-pattern> --runInBand --silent`
- Full frontend tests: `npm test -- --runInBand --silent`
- Frontend typecheck: `npm run typecheck`
- Frontend lint: `npm run lint`
- Format check: `npm run format:check`
- Backend tests: `cd server && go test ./...`

Run `npm run typecheck` for TypeScript refactors. For formatting, the full repo still has a legacy formatting baseline; use Prettier on touched files and avoid whole-repo formatting churn unless the task is explicitly a formatting pass.

For visual or interaction refactors, manually smoke test on the connected mobile app/emulator:

- Discover
- My Events
- Create Event
- Messages
- Profile
- Event Details
- Bottom sheets and action menus
- Back navigation

### Emulator Verification With mobile-mcp

Pi's documented way to connect to MCP servers is via **extensions** (pi has no built-in MCP support; see its README philosophy: "Build CLI tools with READMEs (Skills), or build an extension that adds MCP support"). Both pieces below are **project-local** under `.pi/` so they travel with the repo and load after project trust:

- `.pi/extensions/mobile-mcp/` — a bridge extension that spawns the `mobile-mcp` server as a stdio subprocess and exposes the Android emulator/device as pi tools: `mobile_init`, `mobile_open_app`, `mobile_dump_ui`, `mobile_tap`, `mobile_swipe`, `mobile_type`, `mobile_key_press`, `mobile_screenshot`, `mobile_list_packages`. `mobile_screenshot` returns a real PNG that vision-capable models can see, so fixes can be visually verified. After a fresh clone, run `cd .pi/extensions/mobile-mcp && npm install` once (its `node_modules` is gitignored).
- `.pi/skills/fix-issues-on-device/SKILL.md` — the batch workflow: implement a fix, run typecheck/tests, rebuild and install via `npm run android`, navigate to the issue's screen with `mobile_dump_ui`/`mobile_tap`, capture `mobile_screenshot`, inspect it, and retry up to 5 times per issue, using `ISSUES.md` as the live status board.
- `.pi/skills/test-on-device/SKILL.md` — the single-change verification workflow: after implementing a feature/fix, prove it works on the Android emulator by bypassing Google/Apple sign-in with a dev-login dummy user and driving the app with the `mobile_*` tools. Covers starting an AVD, env wiring for emulator builds (`EXPO_PUBLIC_API_BASE_URL=http://10.0.2.2:8080`), Metro preconditions, handling the Expo Dev Launcher screen, signing in via the `DevLoginButton` (testID `dev-login-button`, mounted inside `SignInButtons`, shown only in `__DEV__` builds started with `EXPO_PUBLIC_DEV_LOGIN_ENABLED=true`), reproducing the changed flow, and recording a pass/fail verdict on `TEST_RUNS.md`. Emulator-only — never targets a physical device.

### Dev-Login Bypass For Emulator Testing

For testing on the emulator without going through Google/Apple sign-in, the backend exposes a dev-login route and the client an opt-in, dev-build-only button. **See [`docs/dev-login.md`](docs/dev-login.md) for the full setup, the three preset test users, switching between normal and dev-login modes, and the end-to-end emulator recipe.** Quick recap:

- **Server** (`server/auth_handler.go`): `POST /api/dev-login` registered ONLY when `DEV_LOGIN_ENABLED=1` (env flag, off by default, logs a loud `WARNING` when on). It reuses `getOrCreateUserByEmail` + `respondWithIssuedSession`, so the returned token is a real session JWT accepted by every authenticated endpoint and the WebSocket. Request body: `{"email":"tester@who-else-is-free.test","name":"Tester","profile_complete":true}`. The fixed email keeps a stable, reproducible seed user across sessions. **Never enable `DEV_LOGIN_ENABLED` in production or commit it to `.env` / `Dockerfile` / EAS build config.**
- **Client** (`src/components/DevLoginButton.tsx`): preset buttons that call `useAuth().signInWithDevUser(...)` on tap, mounted inside `SignInButtons` so, when enabled, they appear on every unauthenticated surface. They are hidden by default (issue #186) and render only when `DEV_LOGIN_ENABLED` in `src/constants/featureFlags.ts` is true: a `__DEV__` build whose Metro was started with `EXPO_PUBLIC_DEV_LOGIN_ENABLED=true`. In release builds (`__DEV__ === false`) the flag is always false, so the buttons can never render regardless of the env or the backend flag.
- **Independent switches, all off in production:** the server env flag, the client `__DEV__` gate, and the client opt-in flag. Any one alone keeps dev-login out of release builds.

### Emulator Test Session Setup

When running the `test-on-device` skill, the per-session environment is:

- **Emulator AVD:** `WEIF_API_36` (launched via `~/Library/Android/sdk/emulator/emulator -avd WEIF_API_36 -no-snapshot-load`). The `emulator` binary is not on `$PATH` by default; use the absolute path.
- **Targeting:** if a physical device is also connected over USB, `mobile-mcp` fails with `adb: more than one device/emulator` (it runs bare `adb shell` with no `-s <serial>`). Either unplug the physical device, OR start pi with `ANDROID_SERIAL=emulator-5554` so the mobile-mcp subprocess inherits it. Verify the target is an emulator (`emulator-<port>` + `product:sdk_gphone*`) via `adb devices -l`.
- **Backend:** `cd server && DEV_LOGIN_ENABLED=1 go run .` (listens on `:8080`).
- **Metro:** `EXPO_PUBLIC_API_BASE_URL=http://10.0.2.2:8080 EXPO_PUBLIC_WS_BASE_URL=ws://10.0.2.2:8080 EXPO_PUBLIC_CHAT_ENABLED=true EXPO_PUBLIC_DEV_LOGIN_ENABLED=true npm start`. Without `EXPO_PUBLIC_DEV_LOGIN_ENABLED=true` the Dev Login buttons stay hidden. The `10.0.2.2` alias is the emulator's special route to the host loopback — it does NOT exist on physical devices.
- **App launch:** `adb shell am start -n com.whoelseisfree.app/.MainActivity` (the `mobile_open_app` `monkey` invocation silently no-ops on this Expo Dev Launcher build). If the app lands on the Dev Launcher screen listing dev servers, tap the `http://10.0.2.2:8081` row to connect to Metro.
- **Status board:** keep `TEST_RUNS.md` in the project root as the durable log of every device-test verdict.

One emulator is a shared device, so verify changes sequentially, never call `mobile_*` tools in parallel. Trust the project with `/trust` (or `pi -a`) so `.pi/` resources load.

## Known Quality Gates To Improve

- Reduce lint warning baseline.
- Establish a green full-repo format baseline without mixing it into behavior refactors.
- Keep direct haptic imports restricted to `src/services/haptics.ts`.
- Reduce hardcoded style values outside theme.
- Reduce large screen/context files.
- Remove avoidable `any` casts.
- Consolidate repeated sheet, tab, button, list, and action-menu implementations.

## Backend Notes

- Keep backend changes inside `server/` unless frontend API types must be updated too.
- Run `cd server && go test ./...` after backend changes.
- Keep endpoint behavior and payload shape documented in frontend mappers when they are introduced.
- Notification push and inbox text is owned server-side by `notificationCopyFor` in `server/notification_payloads.go`. New copy changes must update that contract and its push/inbox tests together. Single rows render stored bodies verbatim; collapsed join groups use structured `payload.senderName` first and retain body parsing only for legacy rows.
- Safety reports outlive accounts and plans (#184): `event_reports.event_id`/`user_id`/`reviewed_by`
  are `ON DELETE SET NULL`; `reported_user_id` and `subject_user_id` have no FK and keep deleted ids.
  Create reports only through `insertReportWithSnapshot`, which copies the plan title, the subject
  (reported member, or host for plan reports), the subject's last 50 messages the reporter could see
  (`event_report_messages`) and identity HMACs (`user_identity_hashes` → `event_report_identities`,
  keyed by `REPORT_IDENTITY_HMAC_KEY`). `PurgeExpiredEventReports` deletes reports after 12 months
  unless `retention_hold = 1`. Startup cleanups must never delete reports because a reference is
  NULL. See `docs/safety-report-retention.md`.

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
