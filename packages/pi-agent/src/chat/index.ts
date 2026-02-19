import { Context, Effect, Layer, Stream, Data } from "effect"

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

export class ChatError extends Data.TaggedError("ChatError")<{
  message: string
}> {}

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface ChatMessage {
  readonly role: "system" | "user" | "assistant"
  readonly content: string
}

export interface StreamDelta {
  readonly content: string
  readonly done: boolean
}

// ---------------------------------------------------------------------------
// Constants (mirrors internal/chat/client.go)
// ---------------------------------------------------------------------------

const RESPONSES_URL = "https://chatgpt.com/backend-api/codex/responses"

// ---------------------------------------------------------------------------
// Service interface
// ---------------------------------------------------------------------------

export interface ChatClientService {
  readonly streamCompletion: (params: {
    token: string
    accountId: string
    model: string
    messages: ReadonlyArray<ChatMessage>
  }) => Stream.Stream<StreamDelta, ChatError>
}

export class ChatClient extends Context.Tag("ChatClient")<
  ChatClient,
  ChatClientService
>() {}

// ---------------------------------------------------------------------------
// SSE parser — yields only data-line payloads
// ---------------------------------------------------------------------------

function* parseSSEDataLines(text: string): Generator<string> {
  for (const line of text.split("\n")) {
    if (line.startsWith("data: ")) {
      yield line.slice(6)
    }
  }
}

// ---------------------------------------------------------------------------
// Live implementation
// ---------------------------------------------------------------------------

export const ChatClientLive = Layer.succeed(
  ChatClient,
  {
    streamCompletion: ({ token, accountId, model, messages }) =>
      Stream.asyncEffect((emit) =>
        Effect.tryPromise({
          try: async () => {
            const resp = await fetch(RESPONSES_URL, {
              method: "POST",
              headers: {
                "Content-Type": "application/json",
                "Authorization": `Bearer ${token}`,
                ...(accountId ? { "ChatGPT-Account-Id": accountId } : {}),
              },
              body: JSON.stringify({ model, input: messages, stream: true }),
            })

            if (!resp.ok) {
              const body = await resp.text().catch(() => resp.statusText)
              emit.fail(
                new ChatError({ message: `API error ${resp.status}: ${body}` }),
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

              // Process complete SSE blocks (separated by double newlines)
              const parts = buffer.split("\n\n")
              buffer = parts.pop() ?? ""

              for (const part of parts) {
                for (const data of parseSSEDataLines(part + "\n\n")) {
                  if (!data) continue

                  let event: {
                    type?: string
                    delta?: string
                    response?: unknown
                  }
                  try {
                    event = JSON.parse(data)
                  } catch {
                    continue
                  }

                  if (event.type === "response.output_text.delta" && event.delta) {
                    emit.single({ content: event.delta, done: false })
                  } else if (event.type === "response.completed") {
                    emit.single({ content: "", done: true })
                    emit.end()
                    return
                  }
                }
              }
            }

            emit.end()
          },
          catch: (e) =>
            new ChatError({ message: `stream error: ${e}` }),
        }).pipe(
          Effect.catchAll((e) =>
            Effect.sync(() => emit.fail(e as ChatError)),
          ),
        ),
      ),
  } satisfies ChatClientService,
)
