import { Context, Effect, Layer, Data } from "effect"
import * as path from "node:path"
import * as fs from "node:fs"
import {
  type Credentials,
  isExpired,
  refreshToken,
  OAuthError,
} from "../oauth/index.ts"

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

export class TokenStoreError extends Data.TaggedError("TokenStoreError")<{
  message: string
}> {}

// ---------------------------------------------------------------------------
// Service interface
// ---------------------------------------------------------------------------

export interface TokenStoreService {
  readonly hasCredentials: () => boolean
  readonly save: (cred: Credentials) => Effect.Effect<void, TokenStoreError>
  readonly accountId: () => string
  /** Returns a valid access token, auto-refreshing if expired. */
  readonly accessToken: () => Effect.Effect<string, TokenStoreError | OAuthError>
}

export class TokenStore extends Context.Tag("TokenStore")<
  TokenStore,
  TokenStoreService
>() {}

// ---------------------------------------------------------------------------
// Live implementation
// ---------------------------------------------------------------------------

export const TokenStoreLive = (dataDir: string) =>
  Layer.effect(
    TokenStore,
    Effect.gen(function* () {
      const tokenPath = path.join(dataDir, "token.json")

      // Ensure directory exists
      yield* Effect.try({
        try: () => fs.mkdirSync(dataDir, { recursive: true }),
        catch: (e) =>
          new TokenStoreError({ message: `creating data dir: ${e}` }),
      })

      // Load from disk (best effort)
      let cred: Credentials | null = null
      try {
        const raw = fs.readFileSync(tokenPath, "utf8")
        cred = JSON.parse(raw) as Credentials
      } catch {
        // file may not exist yet
      }

      const save = (next: Credentials): Effect.Effect<void, TokenStoreError> =>
        Effect.try({
          try: () => {
            cred = next
            fs.writeFileSync(tokenPath, JSON.stringify(next, null, 2), {
              mode: 0o600,
            })
          },
          catch: (e) =>
            new TokenStoreError({ message: `saving token: ${e}` }),
        })

      const accessToken = (): Effect.Effect<
        string,
        TokenStoreError | OAuthError
      > =>
        Effect.gen(function* () {
          if (!cred) {
            return yield* Effect.fail(
              new TokenStoreError({
                message: "no credentials stored; authenticate first",
              }),
            )
          }

          if (!isExpired(cred)) {
            return cred.accessToken
          }

          // Refresh
          const tokResp = yield* refreshToken(cred.refreshToken)
          const updated: Credentials = {
            accessToken: tokResp.access_token,
            refreshToken: tokResp.refresh_token ?? cred.refreshToken,
            expiresAt:
              Math.floor(Date.now() / 1000) + tokResp.expires_in,
            accountId: cred.accountId,
          }
          yield* save(updated)
          return updated.accessToken
        })

      return {
        hasCredentials: () => cred !== null,
        save,
        accountId: () => cred?.accountId ?? "",
        accessToken,
      } satisfies TokenStoreService
    }),
  )
