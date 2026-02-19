import { Effect, Data } from "effect"

// ---------------------------------------------------------------------------
// Constants (mirrors internal/oauth/chatgpt.go)
// ---------------------------------------------------------------------------

const AUTH_ENDPOINT = "https://auth.openai.com/oauth/authorize"
const TOKEN_ENDPOINT = "https://auth.openai.com/oauth/token"
const DEVICE_AUTH_ENDPOINT =
  "https://auth.openai.com/api/accounts/deviceauth/usercode"
const DEVICE_TOKEN_ENDPOINT =
  "https://auth.openai.com/api/accounts/deviceauth/token"
const DEVICE_VERIFY_URL = "https://auth.openai.com/codex/device"
const CLIENT_ID = "app_EMoamEEZ73f0CkXaXp7hrann"
const REDIRECT_URI = "http://localhost:1455/auth/callback"
const SCOPES = "openid profile email offline_access"
const CALLBACK_PORT = 1455

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

export class OAuthError extends Data.TaggedError("OAuthError")<{
  message: string
}> {}

// ---------------------------------------------------------------------------
// Credentials
// ---------------------------------------------------------------------------

export interface Credentials {
  readonly accessToken: string
  readonly refreshToken: string
  /** Unix timestamp (seconds) after which the access token is considered expired. */
  readonly expiresAt: number
  readonly accountId: string
}

export function isExpired(cred: Credentials): boolean {
  return Date.now() / 1000 > cred.expiresAt - 300
}

// ---------------------------------------------------------------------------
// Token response shape
// ---------------------------------------------------------------------------

interface TokenResponse {
  access_token: string
  refresh_token: string
  id_token?: string
  expires_in: number
  token_type: string
}

// ---------------------------------------------------------------------------
// PKCE helpers
// ---------------------------------------------------------------------------

function generateCodeVerifier(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(32))
  return btoa(String.fromCharCode(...bytes))
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=/g, "")
}

async function generateCodeChallenge(verifier: string): Promise<string> {
  const encoder = new TextEncoder()
  const data = encoder.encode(verifier)
  const digest = await crypto.subtle.digest("SHA-256", data)
  return btoa(String.fromCharCode(...new Uint8Array(digest)))
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=/g, "")
}

function generateState(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  return Array.from(bytes)
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("")
}

// ---------------------------------------------------------------------------
// Browser open helper
// ---------------------------------------------------------------------------

function openBrowser(url: string): void {
  const platform = process.platform
  if (platform === "darwin") {
    Bun.spawn(["open", url])
  } else if (platform === "linux") {
    Bun.spawn(["xdg-open", url])
  } else if (platform === "win32") {
    Bun.spawn(["rundll32", "url.dll,FileProtocolHandler", url])
  } else {
    console.error(`Cannot open browser on platform: ${platform}`)
  }
}

// ---------------------------------------------------------------------------
// Token exchange
// ---------------------------------------------------------------------------

async function exchangeCodeForTokens(
  code: string,
  codeVerifier: string,
): Promise<TokenResponse> {
  const body = new URLSearchParams({
    grant_type: "authorization_code",
    client_id: CLIENT_ID,
    code,
    redirect_uri: REDIRECT_URI,
    code_verifier: codeVerifier,
  })

  const resp = await fetch(TOKEN_ENDPOINT, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: body.toString(),
    signal: AbortSignal.timeout(30_000),
  })

  if (!resp.ok) {
    const err = (await resp.json().catch(() => ({}))) as {
      error?: string
      error_description?: string
    }
    throw new Error(
      err.error_description ??
        err.error ??
        `token exchange failed: ${resp.status}`,
    )
  }

  return resp.json() as Promise<TokenResponse>
}

// ---------------------------------------------------------------------------
// Token refresh (exported for TokenStore)
// ---------------------------------------------------------------------------

export const refreshToken = (
  refreshTok: string,
): Effect.Effect<TokenResponse, OAuthError> =>
  Effect.tryPromise({
    try: async () => {
      const body = new URLSearchParams({
        grant_type: "refresh_token",
        client_id: CLIENT_ID,
        refresh_token: refreshTok,
      })

      const resp = await fetch(TOKEN_ENDPOINT, {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded" },
        body: body.toString(),
        signal: AbortSignal.timeout(30_000),
      })

      if (!resp.ok) {
        const err = (await resp.json().catch(() => ({}))) as {
          error?: string
          error_description?: string
        }
        if (
          err.error === "invalid_grant" ||
          (err.error_description ?? "").includes("revoked")
        ) {
          throw new Error("refresh token expired or revoked: please re-authenticate")
        }
        throw new Error(
          err.error_description ??
            err.error ??
            `token refresh failed: ${resp.status}`,
        )
      }

      return resp.json() as Promise<TokenResponse>
    },
    catch: (e) => new OAuthError({ message: String(e) }),
  })

// ---------------------------------------------------------------------------
// JWT account ID extraction
// ---------------------------------------------------------------------------

export function extractAccountIdFromJWT(token: string): string {
  const parts = token.split(".")
  if (parts.length !== 3) return ""

  try {
    const payload = atob(
      (parts[1] ?? "").replace(/-/g, "+").replace(/_/g, "/"),
    )
    const claims = JSON.parse(payload) as {
      chatgpt_account_id?: string
      "https://api.openai.com/auth"?: { chatgpt_account_id?: string }
      organizations?: Array<{ id: string }>
    }

    if (claims.chatgpt_account_id) return claims.chatgpt_account_id
    const authClaims = claims["https://api.openai.com/auth"]
    if (authClaims?.chatgpt_account_id) return authClaims.chatgpt_account_id
    if (claims.organizations && claims.organizations.length > 0) {
      return claims.organizations[0]?.id ?? ""
    }
  } catch {
    // ignore malformed JWT
  }
  return ""
}

function credentialsFromTokenResponse(tok: TokenResponse): Credentials {
  const idToken = tok.id_token ?? ""
  let accountId = idToken ? extractAccountIdFromJWT(idToken) : ""
  if (!accountId) accountId = extractAccountIdFromJWT(tok.access_token)

  return {
    accessToken: tok.access_token,
    refreshToken: tok.refresh_token,
    expiresAt: Math.floor(Date.now() / 1000) + tok.expires_in,
    accountId,
  }
}

// ---------------------------------------------------------------------------
// Browser-based PKCE flow
// ---------------------------------------------------------------------------

export const authenticate = (): Effect.Effect<Credentials, OAuthError> =>
  Effect.tryPromise({
    try: async () => {
      const codeVerifier = generateCodeVerifier()
      const codeChallenge = await generateCodeChallenge(codeVerifier)
      const state = generateState()

      const params = new URLSearchParams({
        client_id: CLIENT_ID,
        redirect_uri: REDIRECT_URI,
        scope: SCOPES,
        code_challenge: codeChallenge,
        code_challenge_method: "S256",
        response_type: "code",
        state,
      })
      const authURL = `${AUTH_ENDPOINT}?${params.toString()}`

      return await new Promise<Credentials>((resolve, reject) => {
        const server = Bun.serve({
          port: CALLBACK_PORT,
          fetch(req) {
            const url = new URL(req.url)
            if (url.pathname !== "/auth/callback") {
              return new Response("Not found", { status: 404 })
            }

            const returnedState = url.searchParams.get("state")
            if (returnedState !== state) {
              reject(new Error("state mismatch: possible CSRF attack"))
              return new Response("Invalid state", { status: 400 })
            }

            const errMsg = url.searchParams.get("error")
            if (errMsg) {
              const desc = url.searchParams.get("error_description") ?? errMsg
              reject(new Error(`oauth error: ${desc}`))
              return new Response(desc, { status: 400 })
            }

            const code = url.searchParams.get("code")
            if (!code) {
              reject(new Error("no authorization code received"))
              return new Response("No code", { status: 400 })
            }

            // Exchange code in background, resolve/reject the promise
            exchangeCodeForTokens(code, codeVerifier)
              .then((tok) => resolve(credentialsFromTokenResponse(tok)))
              .catch(reject)
              .finally(() => server.stop())

            return new Response(
              "<html><body><h1>Authentication successful!</h1><p>You can close this window.</p></body></html>",
              { headers: { "Content-Type": "text/html" } },
            )
          },
        })

        console.log("Opening browser for authentication...")
        try {
          openBrowser(authURL)
        } catch {
          console.log(`Could not open browser. Please visit:\n${authURL}`)
        }

        // Timeout after 5 minutes
        setTimeout(() => {
          server.stop()
          reject(new Error("authentication timed out after 5 minutes"))
        }, 5 * 60 * 1000)
      })
    },
    catch: (e) => new OAuthError({ message: String(e) }),
  })

// ---------------------------------------------------------------------------
// Device code flow (headless)
// ---------------------------------------------------------------------------

export const authenticateDevice = (): Effect.Effect<Credentials, OAuthError> =>
  Effect.tryPromise({
    try: async () => {
      // Step 1: Request device/user code
      const body = new URLSearchParams({ client_id: CLIENT_ID })
      const resp = await fetch(DEVICE_AUTH_ENDPOINT, {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded" },
        body: body.toString(),
        signal: AbortSignal.timeout(30_000),
      })
      if (!resp.ok) {
        throw new Error(`device auth request failed: ${resp.status}`)
      }

      const deviceResp = (await resp.json()) as {
        device_auth_id: string
        user_code: string
        interval: number
        expires_in: number
      }

      // Step 2: Display instructions
      console.log()
      console.log("  To authenticate, visit:")
      console.log(`    ${DEVICE_VERIFY_URL}`)
      console.log()
      console.log(`  And enter code: ${deviceResp.user_code}`)
      console.log()
      console.log("  Waiting for authentication...")

      // Step 3: Poll
      const pollIntervalMs = (deviceResp.interval + 3) * 1000
      const deadline = Date.now() + deviceResp.expires_in * 1000

      while (Date.now() < deadline) {
        await new Promise((r) => setTimeout(r, pollIntervalMs))

        const pollBody = new URLSearchParams({
          client_id: CLIENT_ID,
          device_auth_id: deviceResp.device_auth_id,
        })
        const pollResp = await fetch(DEVICE_TOKEN_ENDPOINT, {
          method: "POST",
          headers: { "Content-Type": "application/x-www-form-urlencoded" },
          body: pollBody.toString(),
          signal: AbortSignal.timeout(30_000),
        })

        const pollData = (await pollResp.json()) as {
          authorization_code?: string
          code_verifier?: string
          error?: string
        }

        if (
          pollData.error === "authorization_pending" ||
          !pollData.authorization_code
        ) {
          continue
        }
        if (pollData.error) {
          throw new Error(`device auth error: ${pollData.error}`)
        }

        const tok = await exchangeCodeForTokens(
          pollData.authorization_code,
          pollData.code_verifier ?? "",
        )
        return credentialsFromTokenResponse(tok)
      }

      throw new Error("device authentication timed out")
    },
    catch: (e) => new OAuthError({ message: String(e) }),
  })
