import { spawn } from "node:child_process";
import type { AgentRunEvent, ConversationRole, Engine } from "@qianshou/core";

export interface AgentExecutionInput {
  runId?: string;
  engine: Engine;
  role: ConversationRole;
  cwd: string;
  prompt: string;
  sessionId: string | null;
  onProgress?: (progress: AgentExecutionProgress) => void | Promise<void>;
}

export interface AgentExecutionProgress {
  eventCount: number;
  summary: string;
  event: Omit<AgentRunEvent, "at">;
}

export interface AgentExecutionResult {
  sessionId: string;
  text: string;
  rawEvents: unknown[];
}

interface ExecutableConfig {
  file: string;
  prefixArgs?: string[];
}

interface RunnerConfig {
  claude: ExecutableConfig;
  codex: ExecutableConfig;
  timeoutMs?: number;
}

export interface AgentRunner {
  run(input: AgentExecutionInput): Promise<AgentExecutionResult>;
  cancel?(runId: string): boolean;
}

const writableRoles: ConversationRole[] = ["IMPLEMENTATION", "REPAIR"];

function safeError(value: string) {
  return value
    .replace(/sk-ant-[A-Za-z0-9_-]+/g, "[redacted]")
    .replace(/ghp_[A-Za-z0-9_]+/g, "[redacted]")
    .replace(/github_pat_[A-Za-z0-9_]+/g, "[redacted]");
}

const sensitiveKey = /token|secret|password|passwd|authorization|api[_-]?key|credential|cookie/i;

function scrubValue(value: unknown, depth = 0): unknown {
  if (depth > 3) return "[truncated]";
  if (typeof value === "string") {
    return safeError(value)
      .replace(/Bearer\s+[A-Za-z0-9._~+/-]+=*/gi, "Bearer [redacted]")
      .replace(/\b[A-Z][A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|API_KEY)=\S+/g, "[redacted]");
  }
  if (Array.isArray(value)) return value.slice(0, 8).map((item) => scrubValue(item, depth + 1));
  const object = objectValue(value);
  if (!object) return value;
  return Object.fromEntries(
    Object.entries(object)
      .slice(0, 16)
      .map(([key, item]) => [
        key,
        sensitiveKey.test(key) ? "[redacted]" : scrubValue(item, depth + 1),
      ]),
  );
}

function safePreview(value: unknown, limit = 800): string | null {
  if (value === undefined || value === null || value === "") return null;
  const scrubbed = scrubValue(value);
  const text = typeof scrubbed === "string" ? scrubbed : JSON.stringify(scrubbed, null, 2);
  return text.length > limit ? `${text.slice(0, limit)}…` : text;
}

function parseJsonLines(value: string): unknown[] {
  return value
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      try {
        return JSON.parse(line) as unknown;
      } catch {
        return { type: "unparsed", text: line };
      }
    });
}

function parseJsonLine(line: string): unknown {
  try {
    return JSON.parse(line) as unknown;
  } catch {
    return { type: "unparsed", text: line };
  }
}

function objectValue(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}

function stringValue(value: unknown) {
  return typeof value === "string" ? value : null;
}

function progressEvent(engine: Engine, raw: unknown, sequence: number): Omit<AgentRunEvent, "at"> {
  const event = objectValue(raw);
  const build = (kind: AgentRunEvent["kind"], title: string, detail: unknown = null) => ({
    sequence,
    kind,
    title,
    detail: safePreview(detail),
  });
  if (!event) return build("SYSTEM", `${engine === "claude" ? "Claude Code" : "Codex"} 正在处理`);
  if (engine === "claude") {
    if (event.type === "system" && event.subtype === "init") {
      return build("SYSTEM", "Claude Code 已连接", {
        model: event.model,
        tools: Array.isArray(event.tools) ? event.tools.length : undefined,
      });
    }
    if (event.type === "result") {
      return build(
        event.is_error === true ? "ERROR" : "RESULT",
        event.is_error === true ? "Claude Code 运行失败" : "Claude Code 完成本轮",
        {
          duration_ms: event.duration_ms,
          num_turns: event.num_turns,
          total_cost_usd: event.total_cost_usd,
        },
      );
    }
    if (event.type === "assistant") {
      const message = objectValue(event.message);
      const content = Array.isArray(message?.content) ? message.content : [];
      const tool = content.map(objectValue).find((item) => item?.type === "tool_use");
      const name = stringValue(tool?.name);
      if (name) return build("TOOL", `使用工具 ${name}`, tool?.input);
      const text = content
        .map((item) => stringValue(objectValue(item)?.text))
        .filter(Boolean)
        .join("\n");
      return build("THOUGHT", "Claude Code 正在整理回答", text);
    }
    if (event.type === "user") {
      const message = objectValue(event.message);
      const content = Array.isArray(message?.content) ? message.content : [];
      const result = content.map(objectValue).find((item) => item?.type === "tool_result");
      return build("TOOL_RESULT", "工具返回结果", result?.content);
    }
    return build("SYSTEM", "Claude Code 正在处理", { type: event.type, subtype: event.subtype });
  }
  if (event.type === "thread.started")
    return build("SYSTEM", "Codex 已连接", { thread_id: event.thread_id });
  if (event.type === "turn.started") return build("SYSTEM", "Codex 开始本轮");
  if (event.type === "turn.completed") return build("RESULT", "Codex 完成本轮", event.usage);
  if (event.type === "error") return build("ERROR", "Codex 报告错误", event.message);
  if (event.type === "item.started" || event.type === "item.completed") {
    const item = objectValue(event.item);
    if (item?.type === "command_execution") {
      const completed = event.type === "item.completed";
      return build(
        completed ? "TOOL_RESULT" : "TOOL",
        completed ? "命令执行完成" : "执行命令",
        completed ? { exit_code: item.exit_code, output: item.aggregated_output } : item.command,
      );
    }
    if (item?.type === "mcp_tool_call") {
      return build(
        event.type === "item.completed" ? "TOOL_RESULT" : "TOOL",
        `${event.type === "item.completed" ? "工具调用完成" : "调用工具"} ${stringValue(item.server) ?? ""} ${stringValue(item.tool) ?? ""}`.trim(),
        event.type === "item.completed" ? item.result : item.arguments,
      );
    }
    if (item?.type === "reasoning") return build("THOUGHT", "Codex 正在推理", item.text);
    if (item?.type === "agent_message") return build("MESSAGE", "Codex 正在生成回答", item.text);
    return build(
      "SYSTEM",
      `Codex ${event.type === "item.completed" ? "完成" : "开始"} ${stringValue(item?.type) ?? "事件"}`,
      item,
    );
  }
  return build("SYSTEM", "Codex 正在处理", { type: event.type });
}

function parseClaude(events: unknown[]): AgentExecutionResult {
  let sessionId: string | null = null;
  let finalText: string | null = null;
  let assistantText: string | null = null;
  let isError = false;
  for (const raw of events) {
    const event = objectValue(raw);
    if (!event) continue;
    sessionId = stringValue(event.session_id) ?? sessionId;
    if (event.type === "result") {
      finalText = stringValue(event.result) ?? finalText;
      isError = event.is_error === true || event.subtype === "error";
    }
    if (event.type === "assistant") {
      const message = objectValue(event.message);
      const content = Array.isArray(message?.content) ? message.content : [];
      const text = content
        .map((item) => stringValue(objectValue(item)?.text))
        .filter((item): item is string => Boolean(item))
        .join("\n");
      assistantText = text || assistantText;
    }
  }
  if (isError) throw new Error(finalText ?? "Claude Code returned an error result");
  if (!sessionId) throw new Error("Claude Code output did not include a session_id");
  const text = finalText ?? assistantText;
  if (!text) throw new Error("Claude Code output did not include an Agent response");
  return { sessionId, text, rawEvents: events };
}

function parseCodex(events: unknown[]): AgentExecutionResult {
  let sessionId: string | null = null;
  let finalText: string | null = null;
  for (const raw of events) {
    const event = objectValue(raw);
    if (!event) continue;
    if (event.type === "thread.started") sessionId = stringValue(event.thread_id) ?? sessionId;
    if (event.type === "item.completed") {
      const item = objectValue(event.item);
      if (item?.type === "agent_message") finalText = stringValue(item.text) ?? finalText;
    }
    if (event.type === "error")
      throw new Error(stringValue(event.message) ?? "Codex returned an error event");
  }
  if (!sessionId) throw new Error("Codex output did not include a thread_id");
  if (!finalText) throw new Error("Codex output did not include an Agent response");
  return { sessionId, text: finalText, rawEvents: events };
}

export class CliAgentRunner implements AgentRunner {
  private readonly timeoutMs: number;
  private readonly active = new Map<string, ReturnType<typeof spawn>>();
  private readonly cancelled = new Set<string>();

  constructor(
    private readonly config: RunnerConfig = {
      claude: { file: "claude" },
      codex: { file: "codex" },
    },
  ) {
    this.timeoutMs = config.timeoutMs ?? 30 * 60 * 1000;
  }

  async run(input: AgentExecutionInput): Promise<AgentExecutionResult> {
    const executable = this.config[input.engine];
    const args = [...(executable.prefixArgs ?? []), ...this.arguments(input)];
    const output = await new Promise<{ stdout: string; stderr: string }>(
      (resolvePromise, reject) => {
        const child = spawn(executable.file, args, {
          cwd: input.cwd,
          env: process.env,
          shell: false,
          stdio: ["pipe", "pipe", "pipe"],
        });
        if (input.runId) this.active.set(input.runId, child);
        let stdoutText = "";
        let stdoutLineBuffer = "";
        let eventCount = 0;
        const stderr: Buffer[] = [];
        let settled = false;
        const timer = setTimeout(() => {
          if (settled) return;
          settled = true;
          child.kill("SIGTERM");
          if (input.runId) this.active.delete(input.runId);
          reject(new Error(`${input.engine} Agent timed out after ${this.timeoutMs}ms`));
        }, this.timeoutMs);
        const emitProgress = (line: string) => {
          const normalized = line.trim();
          if (!normalized) return;
          eventCount += 1;
          const event = progressEvent(input.engine, parseJsonLine(normalized), eventCount);
          const progress = { eventCount, summary: event.title, event };
          try {
            void Promise.resolve(input.onProgress?.(progress)).catch(() => undefined);
          } catch {
            // Progress reporting must never terminate the owned Agent process.
          }
        };
        child.stdout.on("data", (chunk: Buffer) => {
          const text = chunk.toString("utf8");
          stdoutText += text;
          stdoutLineBuffer += text;
          const lines = stdoutLineBuffer.split(/\r?\n/);
          stdoutLineBuffer = lines.pop() ?? "";
          for (const line of lines) emitProgress(line);
        });
        child.stderr.on("data", (chunk: Buffer) => stderr.push(Buffer.from(chunk)));
        child.once("error", (error) => {
          settled = true;
          clearTimeout(timer);
          if (input.runId) this.active.delete(input.runId);
          reject(error);
        });
        child.once("close", (code, signal) => {
          if (settled) return;
          settled = true;
          clearTimeout(timer);
          if (stdoutLineBuffer) emitProgress(stdoutLineBuffer);
          const wasCancelled = Boolean(input.runId && this.cancelled.delete(input.runId));
          if (input.runId) this.active.delete(input.runId);
          const stderrText = safeError(Buffer.concat(stderr).toString("utf8"));
          if (wasCancelled) {
            reject(new Error(`${input.engine} Agent cancelled by user`));
            return;
          }
          if (code !== 0) {
            reject(
              new Error(
                `${input.engine} Agent exited with ${code ?? signal}: ${stderrText.slice(-4000)}`,
              ),
            );
            return;
          }
          resolvePromise({ stdout: stdoutText, stderr: stderrText });
        });
        child.stdin.end(input.prompt);
      },
    );
    const events = parseJsonLines(output.stdout);
    return input.engine === "claude" ? parseClaude(events) : parseCodex(events);
  }

  cancel(runId: string): boolean {
    const child = this.active.get(runId);
    if (!child) return false;
    this.cancelled.add(runId);
    child.kill("SIGTERM");
    return true;
  }

  private arguments(input: AgentExecutionInput): string[] {
    if (input.engine === "claude") {
      return [
        "-p",
        ...(input.sessionId ? ["--resume", input.sessionId] : []),
        "--output-format",
        "stream-json",
        "--verbose",
        "--permission-mode",
        writableRoles.includes(input.role) ? "acceptEdits" : "plan",
      ];
    }
    if (input.sessionId) {
      return ["exec", "resume", "--json", input.sessionId, "-"];
    }
    return [
      "exec",
      "--json",
      "--sandbox",
      writableRoles.includes(input.role) ? "workspace-write" : "read-only",
      "-C",
      input.cwd,
      "-",
    ];
  }
}
