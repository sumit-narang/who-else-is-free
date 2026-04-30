# Delete Profile / Account Functionality Plan

## Current Issue

The Profile screen shows a `Delete` action, but it is not functional. It currently only shows a placeholder message. There is no backend route for deleting the authenticated user, and the database has multiple user-dependent tables that must be cleaned up before the `users` row can be deleted safely.

This should be implemented as full account deletion because the Profile screen action represents deleting the user's account, not only clearing profile fields.

## Product Behavior

When a user deletes their account:

- Their local session should be cleared after the server confirms deletion.
- Their account should disappear from future authenticated API calls.
- Their push tokens should be removed.
- Their provider links, such as Apple account links, should be removed.
- Their hosted events should be deleted for everyone.
- Their joined events should remain for other users, but the deleting user should be removed from those events and chats.
- Their pending join requests should be removed.
- Existing JWTs from other devices should stop working after the account row is deleted.

## Scenarios To Cover

### User Has No Event Or Chat Data

- Delete `push_tokens`.
- Delete `apple_accounts`.
- Delete `user_blocks` involving the user.
- Delete `event_reports` involving the user.
- Delete the `users` row last.
- Clear local auth state on the client.

### User Has Pending Join Requests

- Delete pending join requests where `conversation_join_requests.user_id` is the deleting user.
- The host should no longer see that request.
- The event itself remains unchanged.

### User Joined A Single Event

For `group_type = 'Single'`:

- Delete the private conversation between host and joiner.
- Delete messages, membership, and read state through conversation cleanup.
- Delete the approved join request for that event/user.
- The event remains visible to the host and other potential joiners.

### User Joined A Group Event

For `group_type = 'Group'`:

- Keep the event.
- Keep the shared group conversation for remaining members.
- Remove the deleting user from `conversation_members`.
- Remove the deleting user's `conversation_read_state`.
- Delete the deleting user's messages from the group chat for privacy.
- Delete the approved join request for that event/user.
- Notify remaining clients to refresh membership/conversation state.

### User Created Events

For every event where `events.user_id` is the deleting user:

- Delete the event.
- Delete conversations attached to the event.
- Delete messages attached to those conversations.
- Delete conversation members attached to those conversations.
- Delete read state attached to those conversations.
- Delete join requests attached to the event.
- Delete event reports attached to the event.
- Notify affected members like normal event deletion.
- Send optional push notification using the existing `event.deleted` pattern.

This is required because events must have a valid host. Keeping a hosted event after deleting the host account would create broken UI and invalid data.

### User Has Reports Or Blocks

- Delete reports submitted by the user.
- Delete reports where the user is the reported member.
- Clear or delete reviewed references involving the user, depending on schema constraints.
- Delete blocks where the user is blocker or blocked user.

Recommended implementation: delete reports involving the user unless there is a product/legal need to retain moderation records. Current app data model does not support anonymized deleted-user report retention cleanly.

### User Has Push Tokens

- Delete all `push_tokens` for the user.
- The deleted account should not receive future event or chat pushes.

### User Has Apple Account Link

- Delete `apple_accounts` rows for the user.
- This prevents a deleted account link from remaining attached to a missing user.

### User Is Logged In On Another Device

Current middleware verifies token signature and expiry only. It does not check that the user still exists.

Required fix:

- Protected REST middleware should verify the `claims.UserID` still maps to an existing user.
- WebSocket authentication should also reject deleted users.
- If the user no longer exists, return `401`.
- Existing clients should then clear session through the current auth-expiry flow.

## Backend Plan

### 1. Add Route

Add a protected route:

```go
protected.DELETE("/profile", profileHandler.DeleteProfile)
```

Expected responses:

- `204 No Content` or `200 {"message":"account deleted"}` on success.
- `401` if unauthenticated or the user no longer exists.
- `500` if deletion fails.

### 2. Add Handler

Add `ProfileHandler.DeleteProfile`.

Responsibilities:

- Read current session claims.
- Collect affected conversation/member/event data needed for WebSocket and push notifications.
- Call repository deletion inside a transaction.
- Emit membership removals and event-deleted pushes after commit.
- Return success only after the transaction commits.

### 3. Add Repository Method

Add a repository method such as:

```go
func (r *EventRepository) DeleteUserAccount(ctx context.Context, userID int64) (*DeleteUserAccountResult, error)
```

`DeleteUserAccountResult` should include enough post-commit data for notifications:

- Conversations where the deleted user should receive a `removed` membership update before sockets become invalid.
- Remaining users affected by hosted event deletion.
- Event IDs/titles deleted because the user was host.
- Remaining group conversations that should be refreshed.

### 4. Transaction Cleanup Order

Inside one transaction:

1. Verify the user exists.
2. Collect hosted events and affected members before deleting.
3. Collect joined event conversations before deleting memberships.
4. Delete hosted events using event deletion semantics.
5. Delete Single-event private conversations where the user joined.
6. Remove the user from Group-event conversations.
7. Delete the user's messages in remaining conversations.
8. Delete the user's `conversation_read_state`.
9. Delete join requests where `user_id = deletingUserID`.
10. Delete or clean reports where the user is reporter, reported user, or reviewer.
11. Delete user blocks where the user is blocker or blocked user.
12. Delete push tokens.
13. Delete Apple account links.
14. Delete direct or legacy conversations created by the user that are not event-backed.
15. Delete remaining conversation memberships for the user.
16. Delete the `users` row last.

The exact SQL can be optimized, but the order must avoid foreign key failures.

### 5. Auth Invalidity After Deletion

Update protected route auth behavior:

- After token verification, check `repo.GetUserByID`.
- If not found, return `401`.
- Avoid doing this for public routes.

Update WebSocket auth:

- After token verification, check user existence before upgrading or subscribing.
- If missing, return `401`.

## Frontend Plan

### 1. AuthContext

Add:

```ts
deleteAccount: () => Promise<void>;
```

Behavior:

- Require a token.
- Call `DELETE /api/profile` using `authFetch`.
- If success, clear `user`, `token`, and auth provider from SecureStore.
- If failure, throw an `ApiError` and keep the current session intact.

### 2. ProfileScreen

Replace the placeholder with the existing modal pattern:

- Use `EventActionOverlay` with `type="confirm"`.
- Use `confirmTone="destructive"`.
- Show loading state while delete is in progress.
- Disable backdrop/cancel while deleting.
- Prevent duplicate submissions.
- Show inline `errorMessage` if deletion fails.

Suggested copy:

- Title: `Delete your account?`
- Description: `This will permanently delete your profile, hosted events, event memberships, and chats. This can't be undone.`
- Confirm: `Delete account`
- Cancel: `Keep account`

### 3. Context Cleanup

Existing `ChatContext` already clears conversations/messages when `user` or `token` becomes null.

Existing `EventsContext` already derives `userEvents` as empty when `user` is null and resets requested events when signed out. No special frontend state wipe should be needed beyond clearing auth state, but tests should verify this.

## Tests

### Backend Tests

Add integration tests for:

- Delete account with no related data.
- Delete account without token returns `401`.
- Delete account twice: second request with old token returns `401`.
- Deleted user cannot access protected REST routes with old token.
- Deleted user cannot open WebSocket with old token.
- Pending join requester deletion removes join request.
- Approved Single-event member deletion removes the private conversation and approved request.
- Approved Group-event member deletion removes membership/read state/request and keeps event/group conversation for others.
- Hosted event owner deletion deletes hosted events and associated conversations/requests/messages.
- Push tokens are deleted.
- Apple account links are deleted.
- Blocks and reports involving the user are deleted or cleaned.

### Frontend Tests

Add or update tests for:

- Profile screen opens destructive confirm modal when `Delete` is pressed.
- Cancel closes modal and does not call API.
- Confirm calls `deleteAccount`.
- Loading state prevents duplicate delete calls.
- Successful delete clears auth state and SecureStore.
- Failed delete keeps user signed in and shows modal error.
- Guest users do not see the delete action.
- Chat/events state clears after auth user becomes null.

## Verification Commands

Run targeted backend tests:

```bash
cd server && go test ./...
```

Run targeted frontend tests:

```bash
npm test -- ProfileScreen.rendering.test.tsx
npm test -- AuthContext.rendering.test.tsx
```

If account deletion affects chat/event context behavior, also run:

```bash
npm test -- ChatContext.rendering.test.tsx
npm test -- EventsContext.rendering.test.tsx
```

## Risks And Decisions

- Message deletion versus anonymization: plan uses deletion for privacy and simpler data integrity.
- Moderation report retention: plan deletes reports involving the user unless retention is required later.
- Other-device logout: requires user-existence checks because JWTs are stateless.
- Notifications should be emitted after transaction commit only, otherwise clients could refresh before data is actually deleted.

## Implementation Status

- Branch created: `feature-delete-profile`.
- Plan file created.
- Implemented backend route: `DELETE /api/profile`.
- Implemented `ProfileHandler.DeleteProfile`.
- Implemented transactional repository cleanup via `DeleteUserAccount`.
- Implemented cleanup for hosted events, pending requests, approved Single-event chats, approved Group-event memberships, user messages, read state, join requests, reports, blocks, push tokens, Apple links, direct conversations, and the final `users` row.
- Implemented post-commit membership notifications and hosted-event deletion pushes using the existing chat hub behavior.
- Implemented deleted-user rejection for protected REST routes by validating the user row after JWT verification.
- Implemented deleted-user rejection for new WebSocket connections before socket upgrade.
- Implemented frontend `deleteAccount` in `AuthContext`.
- Implemented Profile screen delete confirmation using the existing `EventActionOverlay` destructive confirm modal.
- Implemented loading, cancel, duplicate-submit guard, and inline error handling in the Profile screen.
- Added backend integration coverage for no-related-data deletion, stale-token REST rejection, pending request cleanup, approved Group-member cleanup, approved Single-member cleanup, hosted event owner deletion, push token cleanup, and Apple link cleanup.
- Added frontend coverage for Profile delete modal open/cancel/confirm/failure and AuthContext successful/failed account deletion behavior.
- Not yet covered by automated tests: deleted-user WebSocket rejection, report/block cleanup assertions, and duplicate-submit prevention assertion. The implementation code covers these paths, but dedicated tests should still be added if this area changes again.
- Local `app.config.js` changes should remain uncommitted.
