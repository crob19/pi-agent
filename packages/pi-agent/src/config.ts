import { Context, Data } from "effect"
import * as path from "node:path"
import * as os from "node:os"

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

export class ConfigError extends Data.TaggedError("ConfigError")<{
  message: string
}> {}

// ---------------------------------------------------------------------------
// Config shape
// ---------------------------------------------------------------------------

export interface AppConfig {
  readonly addr: string
  readonly port: number
  readonly model: string
  readonly dataDir: string
  readonly systemPrompt: string
  readonly conversationId: string
  readonly headless: boolean
}

export class Config extends Context.Tag("Config")<Config, AppConfig>() {}

// ---------------------------------------------------------------------------
// CLI parsing
// ---------------------------------------------------------------------------

function defaultDataDir(): string {
  return path.join(os.homedir(), ".pi-agent")
}

function parseArgs(argv: string[]): AppConfig {
  const args = argv.slice(2)
  const get = (flag: string, fallback: string): string => {
    const idx = args.findIndex((a) => a === flag || a.startsWith(`${flag}=`))
    if (idx === -1) return fallback
    const arg = args[idx]
    if (arg !== undefined && arg.includes("=")) return arg.split("=").slice(1).join("=")
    const next = args[idx + 1]
    return next ?? fallback
  }
  const getBool = (flag: string): boolean => args.includes(flag)

  const addr = get("--addr", ":8080")
  const portStr = addr.startsWith(":") ? addr.slice(1) : addr.split(":").pop() ?? "8080"
  const port = parseInt(portStr, 10)

  return {
    addr,
    port: isNaN(port) ? 8080 : port,
    model: get("--model", "gpt-4o"),
    dataDir: get("--data-dir", defaultDataDir()),
    systemPrompt: get(
      "--system-prompt",
      "You are a helpful assistant running on a Raspberry Pi.",
    ),
    conversationId: get("--conversation", "default"),
    headless: getBool("--headless"),
  }
}

export function loadConfig(): AppConfig {
  return parseArgs(process.argv)
}
