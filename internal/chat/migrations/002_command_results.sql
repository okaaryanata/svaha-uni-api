ALTER TABLE messages ADD COLUMN version INTEGER NOT NULL DEFAULT 1;
CREATE TABLE command_results (
 room_id TEXT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
 actor_id TEXT NOT NULL REFERENCES users(id),
 command_id TEXT NOT NULL,
 fingerprint TEXT NOT NULL,
 result_json TEXT NOT NULL,
 PRIMARY KEY(room_id,actor_id,command_id)
);
INSERT INTO schema_migrations VALUES(2);
