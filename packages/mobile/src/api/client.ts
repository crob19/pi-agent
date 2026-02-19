import { Effect, Data, Stream } from "effect"

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

export class ApiError extends Data.TaggedError("ApiError")<{
  message: string
  status?: number
}> {}

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface ChatOptions {
  /** The message to send. */
  message: string
  /** Conversation ID (defaults to "default" on the server). */
  conversationId?: string
}

export interface ChatDelta {
  /** Incremental text content from the assistant. */
  content: string
}

export interface HealthStatus {
  status: "ok"
}

// ---------------------------------------------------------------------------
// Pi-Agent API client
// ---------------------------------------------------------------------------

export interface PiAgentClientConfig {
  /** Base URL of the pi-agent server, e.g. "http://raspberrypi.local:8080" */
  readonly baseUrl: string
}

export class PiAgentClient {
  constructor(private readonly config: PiAgentClientConfig) {}

  /** Check if the pi-agent server is reachable. */
  health(): Effect.Effect<HealthStatus, ApiError> {
    return Effect.tryPromise({
      try: async () => {
        const resp = await fetch(`${this.config.baseUrl}/health`)
        if (!resp.ok) {
          throw new ApiError({
            message: `health check failed: ${resp.status}`,
            status: resp.status,
          })
        }
        return resp.json() as Promise<HealthStatus>
      },
      catch: (e) =>
        e instanceof ApiError
          ? e
          : new ApiError({ message: `health check error: ${e}` }),
    })
  }

  /**
   * Send a chat message and receive the assistant's response as a stream of
   * text deltas. The stream completes when the server sends `[DONE]`.
   */
  chat(options: ChatOptions): Stream.Stream<ChatDelta, ApiError> {
    return Stream.asyncEffect((emit) =>
      Effect.tryPromise({
        try: async () => {
          const resp = await fetch(`${this.config.baseUrl}/chat`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              message: options.message,
              ...(options.conversationId
                ? { conversation_id: options.conversationId }
                : {}),
            }),
          })

          if (!resp.ok) {
            const body = await resp.text().catch(() => resp.statusText)
            emit.fail(
              new ApiError({
                message: `chat request failed: ${body}`,
                status: resp.status,
              }),
            )
            return
          }

          const reader = resp.body!.getReader()
          const decoder = new TextDecoder()
          let buffer = ""

          while (true) {
            const { done, value } = await reader.read()
            if (done) break

            buffer += decoder.decode(value, { stream: true })
            const lines = buffer.split("\n")
            buffer = lines.pop() ?? ""

            for (const line of lines) {
              if (!line.startsWith("data: ")) continue
              const data = line.slice(6)
              if (data === "[DONE]") {
                emit.end()
                return
              }
              try {
                const parsed = JSON.parse(data) as { content?: string; error?: string }
                if (parsed.error) {
                  emit.fail(new ApiError({ message: parsed.error }))
                  return
                }
                if (parsed.content) {
                  emit.single({ content: parsed.content })
                }
              } catch {
                // ignore malformed chunks
              }
            }
          }

          emit.end()
        },
        catch: (e) =>
          e instanceof ApiError
            ? e
            : new ApiError({ message: `stream error: ${e}` }),
      }).pipe(
        Effect.catchAll((e) => Effect.sync(() => emit.fail(e as ApiError))),
      ),
    )
  }

  /**
   * Convenience helper: collects the full chat response as a single string.
   */
  chatText(options: ChatOptions): Effect.Effect<string, ApiError> {
    return this.chat(options).pipe(
      Stream.map((d) => d.content),
      Stream.runFold("", (acc, chunk) => acc + chunk),
    )
  }
}

/** Create a PiAgentClient bound to the given server URL. */
export const createClient = (baseUrl: string): PiAgentClient =>
  new PiAgentClient({ baseUrl })
