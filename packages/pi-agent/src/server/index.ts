import { Context, Effect, Layer, Stream, Data } from "effect"
import type { AppConfig } from "../config.ts"
import { TokenStore } from "../token/index.ts"
import { ConversationStore } from "../store/index.ts"
import { ChatClient } from "../chat/index.ts"

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

export class ServerError extends Data.TaggedError("ServerError")<{
  message: string
}> {}

// ---------------------------------------------------------------------------
// Request/response shapes
// ---------------------------------------------------------------------------

interface ChatRequest {
  message: string
  conversation_id?: string
}

// ---------------------------------------------------------------------------
// Handler helpers (run inside a per-request Effect)
// ---------------------------------------------------------------------------

const healthHandler = (): Response =>
  new Response(JSON.stringify({ status: "ok" }), {
    headers: { "Content-Type": "application/json" },
  })

const chatHandler = (
  config: AppConfig,
  body: ChatRequest,
  runtime: <A, E>(eff: Effect.Effect<A, E, TokenStore | ConversationStore | ChatClient>) => Promise<A>,
): Response => {
  const message = body.message?.trim()
  if (!message) {
    return new Response(JSON.stringify({ error: "message is required" }), {
      status: 400,
      headers: { "Content-Type": "application/json" },
    })
  }

  const conversationId = body.conversation_id?.trim() || config.conversationId

  const encoder = new TextEncoder()

  const readable = new ReadableStream({
    async start(controller) {
      try {
        await runtime(
          Effect.gen(function* () {
            const tokenStore = yield* TokenStore
            const convStore = yield* ConversationStore
            const chatClient = yield* ChatClient

            // Get valid access token (auto-refreshes if expired)
            const token = yield* tokenStore.accessToken()
            const accountId = tokenStore.accountId()

            // Persist user message
            yield* convStore.addMessage(conversationId, "user", message)

            // Load conversation history
            const history = yield* convStore.messages(conversationId)

            // Build messages list
            const chatMessages = [
              ...(config.systemPrompt
                ? [{ role: "system" as const, content: config.systemPrompt }]
                : []),
              ...history.map((m) => ({
                role: m.role as "system" | "user" | "assistant",
                content: m.content,
              })),
            ]

            // Stream response as SSE
            let fullResponse = ""

            const stream = chatClient.streamCompletion({
              token,
              accountId,
              model: config.model,
              messages: chatMessages,
            })

            yield* Stream.runForEach(stream, (delta) =>
              Effect.sync(() => {
                if (delta.done) {
                  controller.enqueue(encoder.encode("data: [DONE]\n\n"))
                } else {
                  fullResponse += delta.content
                  const chunk = JSON.stringify({ content: delta.content })
                  controller.enqueue(encoder.encode(`data: ${chunk}\n\n`))
                }
              }),
            )

            // Persist assistant response
            if (fullResponse) {
              yield* convStore.addMessage(conversationId, "assistant", fullResponse)
            }
          }),
        )
      } catch (e) {
        const errMsg = JSON.stringify({ error: String(e) })
        controller.enqueue(encoder.encode(`data: ${errMsg}\n\n`))
      } finally {
        controller.close()
      }
    },
  })

  return new Response(readable, {
    headers: {
      "Content-Type": "text/event-stream",
      "Cache-Control": "no-cache",
      "Connection": "keep-alive",
      "X-Accel-Buffering": "no",
    },
  })
}

// ---------------------------------------------------------------------------
// Service
// ---------------------------------------------------------------------------

export interface HttpServerService {
  readonly start: () => Effect.Effect<void, ServerError>
}

export class HttpServer extends Context.Tag("HttpServer")<
  HttpServer,
  HttpServerService
>() {}

export const HttpServerLive = (config: AppConfig) =>
  Layer.effect(
    HttpServer,
    Effect.gen(function* () {
      const tokenStore = yield* TokenStore
      const convStore = yield* ConversationStore
      const chatClient = yield* ChatClient

      // Build a runtime that has all service dependencies pre-provided
      const serviceContext = Context.empty().pipe(
        Context.add(TokenStore, tokenStore),
        Context.add(ConversationStore, convStore),
        Context.add(ChatClient, chatClient),
      )

      const runtime = <A, E>(
        eff: Effect.Effect<A, E, TokenStore | ConversationStore | ChatClient>,
      ): Promise<A> => Effect.runPromise(Effect.provide(eff, serviceContext))

      const start = (): Effect.Effect<void, ServerError> =>
        Effect.async((resume) => {
          const server = Bun.serve({
            port: config.port,
            async fetch(req) {
              const url = new URL(req.url)

              if (req.method === "GET" && url.pathname === "/health") {
                return healthHandler()
              }

              if (req.method === "POST" && url.pathname === "/chat") {
                let body: ChatRequest
                try {
                  body = (await req.json()) as ChatRequest
                } catch {
                  return new Response(
                    JSON.stringify({ error: "invalid JSON body" }),
                    {
                      status: 400,
                      headers: { "Content-Type": "application/json" },
                    },
                  )
                }
                return chatHandler(config, body, runtime)
              }

              return new Response(JSON.stringify({ error: "not found" }), {
                status: 404,
                headers: { "Content-Type": "application/json" },
              })
            },
          })

          console.log(`listening on ${config.addr}`)

          // Never resolves — server runs until process exits
          void resume
          return Effect.sync(() => server.stop())
        })

      return { start } satisfies HttpServerService
    }),
  )
