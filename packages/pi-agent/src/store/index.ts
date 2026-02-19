import { Context, Effect, Layer, Data } from "effect"
import { Database } from "bun:sqlite"
import * as path from "node:path"
import * as fs from "node:fs"

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

export class StoreError extends Data.TaggedError("StoreError")<{
  message: string
}> {}

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export type Role = "system" | "user" | "assistant"

export interface Message {
  readonly id: number
  readonly conversationId: string
  readonly role: Role
  readonly content: string
  readonly createdAt: string
}

// ---------------------------------------------------------------------------
// Service interface
// ---------------------------------------------------------------------------

export interface ConversationStoreService {
  readonly addMessage: (
    conversationId: string,
    role: Role,
    content: string,
  ) => Effect.Effect<void, StoreError>
  readonly messages: (
    conversationId: string,
  ) => Effect.Effect<ReadonlyArray<Message>, StoreError>
}

export class ConversationStore extends Context.Tag("ConversationStore")<
  ConversationStore,
  ConversationStoreService
>() {}

// ---------------------------------------------------------------------------
// Schema
// ---------------------------------------------------------------------------

const SCHEMA = `
  CREATE TABLE IF NOT EXISTS messages (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id TEXT    NOT NULL,
    role            TEXT    NOT NULL,
    content         TEXT    NOT NULL,
    created_at      TEXT    NOT NULL DEFAULT (datetime('now'))
  );
  CREATE INDEX IF NOT EXISTS idx_messages_conversation
    ON messages(conversation_id, id);
`

// ---------------------------------------------------------------------------
// Live implementation
// ---------------------------------------------------------------------------

export const ConversationStoreLive = (dataDir: string) =>
  Layer.effect(
    ConversationStore,
    Effect.gen(function* () {
      const dbPath = path.join(dataDir, "conversations.db")

      const db = yield* Effect.try({
        try: () => {
          fs.mkdirSync(dataDir, { recursive: true })
          const database = new Database(dbPath, { create: true })
          database.exec("PRAGMA journal_mode = WAL")
          database.exec(SCHEMA)
          return database
        },
        catch: (e) => new StoreError({ message: `opening database: ${e}` }),
      })

      const addMessage = (
        conversationId: string,
        role: Role,
        content: string,
      ): Effect.Effect<void, StoreError> =>
        Effect.try({
          try: () => {
            const stmt = db.prepare(
              "INSERT INTO messages (conversation_id, role, content) VALUES (?, ?, ?)",
            )
            stmt.run(conversationId, role, content)
          },
          catch: (e) =>
            new StoreError({ message: `inserting message: ${e}` }),
        })

      const messages = (
        conversationId: string,
      ): Effect.Effect<ReadonlyArray<Message>, StoreError> =>
        Effect.try({
          try: () => {
            const stmt = db.prepare(
              "SELECT id, conversation_id, role, content, created_at FROM messages WHERE conversation_id = ? ORDER BY id",
            )
            return stmt.all(conversationId) as Message[]
          },
          catch: (e) =>
            new StoreError({ message: `querying messages: ${e}` }),
        })

      return { addMessage, messages } satisfies ConversationStoreService
    }),
  )
