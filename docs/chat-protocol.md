# Chat protocol v1

## Rooms and automatic demo login

`POST /api/v1/chat/demos` with `Content-Type: application/json` and `{}` returns 201:

```json
{"room_id":"opaque-id","recovery_token":"opaque-secret","accounts":[{"id":"a","name":"Demo A"},{"id":"b","name":"Demo B"}],"retention_hours":24}
```

Persist only the room ID/recovery capability in browser storage. To restore after a refresh, reuse it for both logins:

`POST /api/v1/chat/sessions`:

```json
{"room_id":"opaque-id","recovery_token":"opaque-secret","account_id":"a"}
```

Returns `{"token":"opaque-session","account_id":"a"}`. Repeat with `b` for an independent session. Invalid or expired room credentials return 401; create a new demo. Each session is bound to its room and account. Authentication never accepts a client-specified sender on a message operation.

## WebSocket

Connect to `/api/v1/chat/ws` with an allowed browser Origin. Within five seconds, send:

```json
{"type":"auth","token":"opaque-session"}
```

Never include credentials in URL parameters. The first event is a viewer-specific snapshot. Every subsequent durable mutation broadcasts a current snapshot to each subscriber in that room, followed by an acknowledgment to the caller. Snapshots contain an ordered revision and ordered messages; clients discard older revisions. Subscription and snapshot creation serialize with mutations, preventing a missing-event gap. After reconnect, authenticate again and replace local authoritative state from the first snapshot.

```json
{"type":"command","id":"request-uuid","action":"send","client_message_id":"stable-send-uuid","text":"Hello","reply_to":""}
```

```json
{"type":"ack","id":"request-uuid","message_id":"server-id"}
```

Errors are `{"type":"error","id":"request-uuid","message":"..."}`. Keep unacknowledged commands for reconnect retries. Reuse the original `client_message_id` and original send payload; the database uniqueness constraint and request fingerprint prevent duplicate inserts, even after edit/revoke. Reusing a send key with different content is rejected. Other mutations use explicit target state rather than toggles so retries preserve state.

## Actions

| Action | Additional fields | Meaning |
| --- | --- | --- |
| `send` | `client_message_id`, `text`, optional `reply_to` | Create a text message; reply target must be visible and active in this room. |
| `edit` | `message_id`, `text` | Sender-only; rejected after revoke. |
| `revoke` | `message_id` | Sender-only; clear body, pin and stars; render a tombstone for both. |
| `delete` | `message_id` | Hide only for this account; no restore action in v1. |
| `star` | `message_id`, `enabled` | Private star/unstar. |
| `pin` | `message_id`, `enabled` | Shared pin/unpin. |
| `delivered` | `message_id` | Receiver acknowledges messages through this message in room order. |
| `read` | `message_id` | Receiver marks messages through this message read, also implying delivered. |
| `typing` | `enabled` | Ephemeral peer notification; no durable write or acknowledgment. |

Receipts target received messages only. UI delivery is acknowledged after a snapshot reaches the receiver; read requires the all-messages view at the bottom, visible in an active document. Typing is throttled and expires in the UI after three seconds without another event.

Each snapshot message includes `id`, `sender_id`, `client_message_id`, `text`, `created_at` (Unix milliseconds), optional `reply_to`, viewer-safe `reply_text`, optional `edited_at`, and Boolean `revoked`, `delivered`, `read`, `starred`, `pinned`. A hidden or revoked reply source is represented as `Message unavailable`; its text is not leaked through reply previews.

## Lifecycle

Reconnect with exponential backoff capped at 30 seconds. Policy-close code 1008 requires a fresh session; an expired room requires a new demo. Ping/pong checks connection health without extending room retention. Messages and session capabilities survive service restart. Typing does not.

## Conversation lists and unread summaries

Every snapshot also contains a viewer-specific `conversation` value:

```json
{"peer":{"id":"b","name":"Demo B"},"unread_count":2,"last_message":{"id":"message-id","sender_id":"b","text":"Latest message","created_at":1700000000000,"revoked":false}}
```

`last_message` is null if no messages are visible. It includes `edited_at` when edited. Summaries use the same ordered, account-filtered history as `messages`: personal deletions cannot leak through previews. Unread counts include only visible incoming messages that are neither read nor revoked. Delivery alone does not clear unread state. Editing changes the preview text without resetting receipts or reordering the conversation; pin/star operations do not change its latest message. A revoked latest message remains the preview entry with empty text and `revoked:true`, rendered as Message revoked. Hiding it selects the previous visible entry.

The frontend initially shows conversation lists. It sends read commands only while detail is open, all-messages is selected, the conversation is scrolled to the bottom, the latest messages are visible, and the document is active. Being online is independent of viewing detail.

## Presence and offline recovery

After the initial snapshot, and whenever authenticated connection membership changes, the server sends a room-scoped event:

```json
{"type":"presence","accounts":{"a":true,"b":false}}
```

Presence aggregates all authenticated sockets for each account in this room. Closing one of several sockets does not make an account offline; closing the last does. Presence events are transient and ordered on each connection, independent of durable message revisions. The frontend discards events from superseded sockets and shows Unknown for peer presence when its own connection is down.

Clean disconnects update presence promptly. Abrupt network loss is detected by the 20-second heartbeat and 5-second ping timeout. Presence, typing, and heartbeat traffic do not extend room retention.

Go offline is client-side connection intent: it closes that pane’s socket and suppresses reconnect until Go online. No database flag is necessary. Sends from another connected account still commit and acknowledge successfully, even with no recipient socket. They remain Sent until a receiving client acknowledges delivery. The reconnect snapshot includes all retained messages and the latest edits, revocations, visibility, preview, and unread summary; the UI must not mark them read merely because reconnect completed. This recovery also works after a backend restart, within the existing 24-hour room retention policy.

These additive fields/events require no schema migration or history reset. Older clients can ignore them.

## Durable local-first extension

Snapshots include `version` on each message (initially 1). New edit commands include `expected_version`; successful edits/revocations advance the message version. Omitted expected versions remain accepted for legacy clients, but the local-first UI always supplies one.

```json
{"type":"command","id":"new-operation-id","action":"edit","message_id":"server-message-id","text":"reviewed edit","expected_version":3}
```

All durable commands have server-side outcome deduplication keyed by room, actor, and command ID, with a fingerprint of the action payload. Replaying an identical command returns its original outcome, including its original revision, even after later mutations or a restart. A changed payload under the same command ID returns `idempotency_conflict`. A stale edit returns `version_conflict`. Permanent errors are replayable; transient storage errors have `retryable:true` and retain pending work for retry.

```json
{"type":"ack","id":"operation-id","message_id":"server-message-id","revision":12}
```

```json
{"type":"error","id":"operation-id","code":"version_conflict","message":"message changed on the server; review your edit","revision":13,"retryable":false}
```

Clients must serialize incoming events into their local database, retain the latest confirmed revision separately from queued projections, and settle an acknowledgment only once at least its revision has committed locally. An ACK can precede a locally applied snapshot if processing is interrupted; both facts remain durable until reconciled.

A local message ID can remain stable while its server ID is assigned. Dependent actions resolve that mapping before their first network attempt. Once attempted, the wire payload and operation ID never change. Conflicting edits remain available for review; explicit retry uses a fresh command ID and the current version. Unknown or expired rooms do not trigger automatic resubmission into a new room.

The receiver queues delivered receipts only after its local snapshot transaction commits. Read receipts are also durable local actions and retain the existing eligible-view requirements. Presence and typing are deliberately not persisted or replayed.
