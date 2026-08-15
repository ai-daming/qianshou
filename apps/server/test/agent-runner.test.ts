import { chmod, mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { CliAgentRunner } from "../src/agent-runner.js";

async function waitUntil(predicate: () => boolean, timeoutMs = 1_000) {
  const deadline = Date.now() + timeoutMs;
  while (!predicate()) {
    if (Date.now() >= deadline) throw new Error("condition was not met before timeout");
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
}

async function fakeAgentExecutable() {
  const directory = await mkdtemp(join(tmpdir(), "qianshou-agent-"));
  const script = join(directory, "fake-agent.mjs");
  await writeFile(
    script,
    `#!/usr/bin/env node
const chunks = [];
for await (const chunk of process.stdin) chunks.push(chunk);
const input = Buffer.concat(chunks).toString('utf8');
const args = process.argv.slice(2);
const engine = args.shift();
if (engine === 'claude') {
  process.stdout.write(JSON.stringify({type:'system', subtype:'init', session_id:'claude-session-1'}) + '\\n');
  process.stdout.write(JSON.stringify({type:'assistant', session_id:'claude-session-1', message:{content:[{type:'tool_use', name:'Read', input:{file_path:'/tmp/domain.md', api_key:'must-not-leak'}}]}}) + '\\n');
  if (input === 'hang') setInterval(() => {}, 1000);
  process.stdout.write(JSON.stringify({type:'result', subtype:'success', is_error:false, session_id:'claude-session-1', result:JSON.stringify({args,input})}) + '\\n');
} else {
  process.stdout.write(JSON.stringify({type:'thread.started', thread_id:'codex-session-1'}) + '\\n');
  process.stdout.write(JSON.stringify({type:'item.completed', item:{type:'agent_message', text:JSON.stringify({args,input})}}) + '\\n');
}
`,
  );
  await chmod(script, 0o755);
  return script;
}

describe("CLI JSON agent adapters", () => {
  it("runs Claude print mode and resumes the immutable Claude session", async () => {
    const script = await fakeAgentExecutable();
    const runner = new CliAgentRunner({
      claude: { file: process.execPath, prefixArgs: [script, "claude"] },
      codex: { file: process.execPath, prefixArgs: [script, "codex"] },
    });
    const result = await runner.run({
      engine: "claude",
      role: "DISCUSSION",
      cwd: "/tmp",
      prompt: "先讨论业务边界",
      sessionId: "existing-claude-session",
    });
    const invocation = JSON.parse(result.text) as { args: string[]; input: string };
    expect(result.sessionId).toBe("claude-session-1");
    expect(invocation.args).toContain("--resume");
    expect(invocation.args).toContain("existing-claude-session");
    expect(invocation.args).toContain("stream-json");
    expect(invocation.args).toContain("--verbose");
    expect(invocation.input).toBe("先讨论业务边界");
  });

  it("reports JSONL progress before the Agent exits and can cancel the owned process", async () => {
    const script = await fakeAgentExecutable();
    const runner = new CliAgentRunner({
      claude: { file: process.execPath, prefixArgs: [script, "claude"] },
      codex: { file: process.execPath, prefixArgs: [script, "codex"] },
      timeoutMs: 5_000,
    });
    const progress: Array<{
      eventCount: number;
      summary: string;
      event: { kind: string; title: string; detail: string | null };
    }> = [];
    const running = runner.run({
      runId: "turn-to-cancel",
      engine: "claude",
      role: "DISCUSSION",
      cwd: "/tmp",
      prompt: "hang",
      sessionId: null,
      onProgress: (event) => {
        progress.push(event);
      },
    });

    await waitUntil(() => progress.length > 0);
    expect(progress[0]).toMatchObject({
      eventCount: 1,
      summary: "Claude Code 已连接",
      event: { kind: "SYSTEM", title: "Claude Code 已连接" },
    });
    await waitUntil(() => progress.some((item) => item.event.kind === "TOOL"));
    const toolEvent = progress.find((item) => item.event.kind === "TOOL");
    expect(toolEvent).toMatchObject({ event: { title: "使用工具 Read" } });
    expect(toolEvent?.event.detail).toContain("/tmp/domain.md");
    expect(toolEvent?.event.detail).not.toContain("must-not-leak");
    expect(runner.cancel("turn-to-cancel")).toBe(true);
    await expect(running).rejects.toThrow(/cancel/i);
    expect(runner.cancel("turn-to-cancel")).toBe(false);
  });

  it("runs Codex exec in read-only mode and resumes by session id", async () => {
    const script = await fakeAgentExecutable();
    const runner = new CliAgentRunner({
      claude: { file: process.execPath, prefixArgs: [script, "claude"] },
      codex: { file: process.execPath, prefixArgs: [script, "codex"] },
    });
    const result = await runner.run({
      engine: "codex",
      role: "REVIEW",
      cwd: "/tmp",
      prompt: "只审 candidate",
      sessionId: "existing-codex-session",
    });
    const invocation = JSON.parse(result.text) as { args: string[]; input: string };
    expect(result.sessionId).toBe("codex-session-1");
    expect(invocation.args.slice(0, 3)).toEqual(["exec", "resume", "--json"]);
    expect(invocation.args).toContain("existing-codex-session");
    expect(invocation.input).toBe("只审 candidate");
  });
});
