import { Effect, Layer } from "effect"
import { loadConfig, Config } from "./config.ts"
import { authenticate, authenticateDevice } from "./oauth/index.ts"
import { TokenStore, TokenStoreLive } from "./token/index.ts"
import { ConversationStore, ConversationStoreLive } from "./store/index.ts"
import { ChatClient, ChatClientLive } from "./chat/index.ts"
import { HttpServer, HttpServerLive } from "./server/index.ts"

// ---------------------------------------------------------------------------
// Main program
// ---------------------------------------------------------------------------

const program = Effect.gen(function* () {
  const config = yield* Config
  const tokenStore = yield* TokenStore

  // Run OAuth flow if we have no saved credentials
  if (!tokenStore.hasCredentials()) {
    if (config.headless) {
      console.log("No saved credentials found. Starting device code authentication...")
      const cred = yield* authenticateDevice()
      yield* tokenStore.save(cred)
    } else {
      console.log("No saved credentials found. Starting authentication...")
      const cred = yield* authenticate()
      yield* tokenStore.save(cred)
    }
    console.log("Authentication successful!")
  }

  const httpServer = yield* HttpServer
  yield* httpServer.start()
})

// ---------------------------------------------------------------------------
// Layer wiring
// ---------------------------------------------------------------------------

const config = loadConfig()

const ConfigLive = Layer.succeed(Config, config)

// Base services have no inter-dependencies — build them first.
const BaseServicesLive = Layer.mergeAll(
  ConfigLive,
  TokenStoreLive(config.dataDir),
  ConversationStoreLive(config.dataDir),
  ChatClientLive,
)

// HttpServerLive requires TokenStore | ConversationStore | ChatClient from
// the context (it yields them during layer construction), so we provide
// BaseServicesLive before merging it with the rest.
const HttpServerLayer = HttpServerLive(config).pipe(
  Layer.provide(BaseServicesLive),
)

// AppLive exposes every service the program needs.
const AppLive = Layer.mergeAll(BaseServicesLive, HttpServerLayer)

// ---------------------------------------------------------------------------
// Run
// ---------------------------------------------------------------------------

Effect.runPromise(Effect.provide(program, AppLive)).catch((e) => {
  console.error("Fatal error:", e)
  process.exit(1)
})
