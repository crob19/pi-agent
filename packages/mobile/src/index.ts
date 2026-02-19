/**
 * @pi-agent/mobile
 *
 * Client SDK for the pi-agent server, designed for use in mobile applications
 * (React Native, Expo, or mobile web). Uses Effect for typed error handling
 * and composable async streams.
 *
 * Usage example:
 *
 *   import { createClient } from "@pi-agent/mobile"
 *   import { Effect, Stream } from "effect"
 *
 *   const client = createClient("http://raspberrypi.local:8080")
 *
 *   // Stream the assistant's reply token-by-token
 *   const program = client
 *     .chat({ message: "What is the weather like?" })
 *     .pipe(
 *       Stream.tap((delta) => Effect.sync(() => process.stdout.write(delta.content))),
 *       Stream.runDrain,
 *     )
 *
 *   Effect.runPromise(program)
 */

export { createClient, PiAgentClient } from "./api/client.ts"
export type { ChatOptions, ChatDelta, HealthStatus, ApiError } from "./api/client.ts"
