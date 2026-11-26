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
