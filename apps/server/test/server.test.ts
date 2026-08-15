import { writeFile, mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { AddressInfo } from "node:net";
import { afterEach, describe, expect, it } from "vitest";
import {
  createQianshouServer,
  parseGithubDependencySnapshot,
  type ExternalCollector,
} from "../src/server.js";
import type { AgentExecutionInput, AgentExecutionResult } from "../src/agent-runner.js";

const servers: Array<ReturnType<typeof createQianshouServer> extends Promise<infer T> ? T : never> =
  [];

afterEach(async () => {
  await Promise.all(
    servers.splice(0).map(
      (server) =>
        new Promise<void>((resolve, reject) => {
          server.close((error) => (error ? reject(error) : resolve()));
        }),
    ),
  );
});

describe("Qianshou HTTP boundary", () => {
  it("keeps Reviewer locked until an auditable candidate SHA is frozen", async () => {
    const directory = await mkdtemp(join(tmpdir(), "qianshou-http-"));
    const configPath = join(directory, "projects.json");
    const statePath = join(directory, "state.json");
    await writeFile(
      configPath,
      JSON.stringify({
        projects: [
          {
            id: "demo",
            repository: { slug: "owner/repo", path: "/tmp/repo" },
            milestone: { number: 7 },
            integration: { branch: "milestone-7", worktree: "/tmp/repo-m7", baseBranch: "main" },
            refreshSeconds: 30,
            defaults: { developerEngine: "codex", reviewerEngine: "claude" },
          },
        ],
      }),
    );
    const collector: ExternalCollector = async (project) => ({
      source: {
        github: "fake GitHub boundary",
        git: "fake Git boundary",
        collectedAt: "2026-08-14T00:00:00.000Z",
        stale: false,
        warning: null,
      },
      repository: {
        nameWithOwner: "owner/repo",
        url: "https://github.com/owner/repo",
        defaultBranch: "main",
      },
      milestone: { number: 7, title: "Demo M7", state: "open", open_issues: 1, closed_issues: 0 },
      githubIssues: [224, 19, 149].map((number) => ({
        number,
        title: number === 224 ? "Lifecycle" : "Poster",
        body: "Acceptance contract",
        state: "OPEN",
        url: `https://github.com/owner/repo/issues/${number}`,
        updatedAt: "2026-08-14T00:00:00.000Z",
        milestone: { title: "Demo M7" },
        labels: [],
        assignees: [],
        blockedBy: number === 19 ? [{ number: 224, state: "OPEN" }] : [],
      })),
      pulls: [],
      worktrees: [{ path: "/tmp/repo-m7-224", sha: "1234567", branch: "issue-224" }],
      integration: {
        ...project.integration,
        sha: "7654321",
        dirty: [],
        statusLine: "milestone-7",
        aheadMain: 0,
        behindMain: 0,
        hasRemoteBranch: true,
      },
    });
    const agentInputs: AgentExecutionInput[] = [];
    const agentRunner = {
      run: async (input: AgentExecutionInput): Promise<AgentExecutionResult> => {
        agentInputs.push(input);
        return {
          sessionId: input.sessionId ?? `${input.engine}-session-1`,
          text: input.prompt.includes("# 开发说明生成任务")
            ? "# 开发说明\n\n## 已确认决策\n\n上户必须人工确认。\n\n## 未决问题\n\n无。"
            : input.role === "DISCUSSION"
              ? "讨论结论：上户必须人工确认。"
              : `${input.role} 已收到交接包`,
          rawEvents: [],
        };
      },
    };
    const server = await createQianshouServer({
      configPath,
      statePath,
      collector,
      agentRunner,
      worktreeCreator: async (project, issueNumber) => ({
        issueNumber,
        branch: `qianshou/issue-${issueNumber}`,
        path: `/tmp/issue-${issueNumber}`,
        baseBranch: project.integration.branch,
        baseSha: "7654321",
        createdAt: "2026-08-14T00:01:00.000Z",
      }),
      refreshIntervalMs: 60_000,
    });
    servers.push(server);
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    const address = server.address() as AddressInfo;
    const baseUrl = `http://127.0.0.1:${address.port}`;

    const initialStatus = await fetch(`${baseUrl}/api/status`);
    expect(initialStatus.status).toBe(200);
    const initialPayload = await initialStatus.json();
    expect(initialPayload.issues.map((issue: { number: number }) => issue.number)).toEqual([
      224, 19, 149,
    ]);
    expect(initialPayload.issues[0].slots.reviewerStatus).toBe("LOCKED");
    expect(initialPayload.issues[1].github.blockedBy).toEqual([{ number: 224, state: "OPEN" }]);

    const invalidStop = await fetch(`${baseUrl}/api/issues/224/stop-conditions`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ category: "SCOPE_CHANGE", summary: "" }),
    });
    expect(invalidStop.status).toBe(400);

    const lockedContract = await fetch(`${baseUrl}/api/issues/224/contract?role=reviewer`);
    expect(lockedContract.status).toBe(409);

    const blockedImplementer = await fetch(`${baseUrl}/api/issues/19/contract?role=implementer`);
    expect(blockedImplementer.status).toBe(409);

    const blockedDiscussion = await fetch(`${baseUrl}/api/issues/19/conversations`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ role: "DISCUSSION", engine: "claude" }),
    });
    expect(blockedDiscussion.status).toBe(201);
    const blockedDiscussionPayload = await blockedDiscussion.json();

    const allowedDiscussionMessage = await fetch(
      `${baseUrl}/api/conversations/${blockedDiscussionPayload.id}/messages`,
      {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ text: "继续讨论，但不开始交付。" }),
      },
    );
    expect(allowedDiscussionMessage.status).toBe(202);

    const blockedDevelopmentBrief = await fetch(
      `${baseUrl}/api/conversations/${blockedDiscussionPayload.id}/development-brief`,
      {
        method: "POST",
      },
    );
    expect(blockedDevelopmentBrief.status).toBe(409);
    expect(await blockedDevelopmentBrief.json()).toMatchObject({ error: "dependency_blocked" });

    const blockedBriefFreeze = await fetch(
      `${baseUrl}/api/conversations/${blockedDiscussionPayload.id}/briefs`,
      {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ content: "# 不应被冻结的开发说明" }),
      },
    );
    expect(blockedBriefFreeze.status).toBe(409);
    expect(await blockedBriefFreeze.json()).toMatchObject({ error: "dependency_blocked" });

    const blockedImplementation = await fetch(`${baseUrl}/api/issues/19/conversations`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ role: "IMPLEMENTATION", engine: "claude" }),
    });
    expect(blockedImplementation.status).toBe(409);
    expect(await blockedImplementation.json()).toMatchObject({
      error: "dependency_blocked",
    });

    const blockedControlUpdate = await fetch(`${baseUrl}/api/issues/19/control`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ phase: "WORKTREE_READY", note: "不应绕过依赖门禁" }),
    });
    expect(blockedControlUpdate.status).toBe(409);
    expect(await blockedControlUpdate.json()).toMatchObject({ error: "dependency_blocked" });

    const update = await fetch(`${baseUrl}/api/issues/224/control`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ phase: "CANDIDATE_READY", candidateSha: "abcdef1" }),
    });
    expect(update.status).toBe(200);

    const reviewContract = await fetch(`${baseUrl}/api/issues/224/contract?role=reviewer`);
    expect(reviewContract.status).toBe(200);
    const reviewPayload = await reviewContract.json();
    expect(reviewPayload.contract).toContain("Candidate SHA: abcdef1");
    expect(reviewPayload.contract).toContain("Do not edit files");

    const createDiscussion = await fetch(`${baseUrl}/api/issues/224/conversations`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ role: "DISCUSSION", engine: "claude", title: "聊清楚生命周期" }),
    });
    expect(createDiscussion.status).toBe(201);
    const discussion = await createDiscussion.json();
    expect(discussion.engine).toBe("claude");

    const sendMessage = await fetch(`${baseUrl}/api/conversations/${discussion.id}/messages`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ text: "上户应该自动发生吗？" }),
    });
    expect(sendMessage.status).toBe(202);

    let collaboration: any;
    for (let attempt = 0; attempt < 20; attempt += 1) {
      const response = await fetch(`${baseUrl}/api/issues/224/collaboration`);
      collaboration = await response.json();
      if (collaboration.conversations[0]?.messages.length === 2) break;
      await new Promise((resolve) => setTimeout(resolve, 10));
    }
    expect(collaboration.conversations[0].sessionId).toBe("claude-session-1");
    expect(collaboration.conversations[0].messages[1].text).toContain("人工确认");

    const generateBrief = await fetch(
      `${baseUrl}/api/conversations/${discussion.id}/development-brief`,
      { method: "POST" },
    );
    expect(generateBrief.status).toBe(202);
    const generationTurn = await generateBrief.json();
    expect(generationTurn.intent).toBe("DEVELOPMENT_BRIEF");

    for (let attempt = 0; attempt < 20; attempt += 1) {
      const response = await fetch(`${baseUrl}/api/issues/224/collaboration`);
      collaboration = await response.json();
      if (
        collaboration.turns.find((turn: { id: string }) => turn.id === generationTurn.id)
          ?.status === "COMPLETED"
      )
        break;
      await new Promise((resolve) => setTimeout(resolve, 10));
    }
    const generatedMessage = collaboration.conversations[0].messages.find(
      (message: { turnId: string; role: string }) =>
        message.turnId === generationTurn.id && message.role === "AGENT",
    );
    expect(generatedMessage.text).toContain("# 开发说明");
    expect(agentInputs.at(-1)?.prompt).toContain("仅整理讨论中已经明确确认的结论");

    const continueDiscussion = await fetch(
      `${baseUrl}/api/conversations/${discussion.id}/messages`,
      {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ text: "继续讨论另一个边界" }),
      },
    );
    expect(continueDiscussion.status).toBe(202);

    const freezeBrief = await fetch(`${baseUrl}/api/conversations/${discussion.id}/briefs`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ content: "# 决策\n上户和下户都必须人工确认" }),
    });
    expect(freezeBrief.status).toBe(201);

    const createWorkspace = await fetch(`${baseUrl}/api/issues/224/workspace`, { method: "POST" });
    expect(createWorkspace.status).toBe(201);

    const createImplementation = await fetch(`${baseUrl}/api/issues/224/conversations`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ role: "IMPLEMENTATION", engine: "codex" }),
    });
    expect(createImplementation.status).toBe(201);
    const implementation = await createImplementation.json();
    expect(implementation.engine).toBe("codex");
    expect(implementation.initialContext).toContain("Task brief:");
    expect(implementation.initialContext).toContain("Worktree: /tmp/issue-224");

    const createReview = await fetch(`${baseUrl}/api/issues/224/conversations`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ role: "REVIEW", engine: "claude" }),
    });
    expect(createReview.status).toBe(201);
    const review = await createReview.json();
    expect(review.initialContext).toContain("Candidate SHA: abcdef1");
    expect(agentInputs[0]?.engine).toBe("claude");

    await fetch(`${baseUrl}/api/issues/224/control`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ phase: "REVIEWING" }),
    });
    const openStop = await fetch(`${baseUrl}/api/issues/224/stop-conditions`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        category: "REVIEW_FINDING",
        summary: "Review 发现需要回到业务讨论",
        detail: "候选保留，等待确认范围。",
      }),
    });
    expect(openStop.status).toBe(201);
    const stop = await openStop.json();
    expect(stop).toMatchObject({
      originPhase: "REVIEWING",
      candidateSha: "abcdef1",
      status: "OPEN",
    });

    const pausedStatus = await (await fetch(`${baseUrl}/api/status`)).json();
    expect(pausedStatus.issues[0].control.phase).toBe("REVIEWING");
    expect(pausedStatus.issues[0].state.key).toBe("NEEDS_DISCUSSION");
    expect(pausedStatus.issues[0].slots.reviewerStatus).toBe("PAUSED");

    const startRepairWhilePaused = await fetch(`${baseUrl}/api/issues/224/conversations`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ role: "REPAIR", engine: "codex" }),
    });
    expect(startRepairWhilePaused.status).toBe(409);

    const continueReviewWhilePaused = await fetch(
      `${baseUrl}/api/conversations/${review.id}/messages`,
      {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ text: "继续 Review" }),
      },
    );
    expect(continueReviewWhilePaused.status).toBe(409);

    const resolveStop = await fetch(`${baseUrl}/api/stop-conditions/${stop.id}/resolve`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ resolution: "确认按原范围继续 Review。", outcome: "RE_REVIEW" }),
    });
    expect(resolveStop.status).toBe(200);
    const resumedStatus = await (await fetch(`${baseUrl}/api/status`)).json();
    expect(resumedStatus.issues[0].control.phase).toBe("REVIEWING");
    expect(resumedStatus.issues[0].state.key).toBe("REVIEWING");
  });

  it("parses native blockedBy relationships returned by GitHub GraphQL", () => {
    const dependencies = parseGithubDependencySnapshot(
      JSON.stringify({
        data: {
          repository: {
            issue19: {
              number: 19,
              blockedBy: { nodes: [{ number: 224, state: "OPEN" }] },
            },
            issue224: {
              number: 224,
              blockedBy: { nodes: [] },
            },
          },
        },
      }),
    );

    expect(dependencies.get(19)).toEqual([{ number: 224, state: "OPEN" }]);
    expect(dependencies.get(224)).toEqual([]);
  });

  it("exposes live Agent progress and cancels the process owned by a running turn", async () => {
    const directory = await mkdtemp(join(tmpdir(), "qianshou-agent-control-"));
    const configPath = join(directory, "projects.json");
    const statePath = join(directory, "state.json");
    await writeFile(
      configPath,
      JSON.stringify({
        projects: [
          {
            id: "demo",
            repository: { slug: "owner/repo", path: "/tmp/repo" },
            milestone: { number: 7 },
            integration: { branch: "milestone-7", worktree: "/tmp/repo-m7", baseBranch: "main" },
            refreshSeconds: 30,
            defaults: { developerEngine: "codex", reviewerEngine: "claude" },
          },
        ],
      }),
    );
    let collectorCalls = 0;
    const collector: ExternalCollector = async (project) => {
      collectorCalls += 1;
      return {
        source: {
          github: "fake",
          git: "fake",
          collectedAt: new Date().toISOString(),
          stale: false,
          warning: null,
        },
        repository: {
          nameWithOwner: "owner/repo",
          url: "https://github.com/owner/repo",
          defaultBranch: "main",
        },
        milestone: { number: 7, title: "Demo M7", state: "open", open_issues: 1, closed_issues: 0 },
        githubIssues: [
          {
            number: 224,
            title: "Lifecycle",
            body: "",
            state: "OPEN",
            url: "https://github.com/owner/repo/issues/224",
            updatedAt: new Date().toISOString(),
            milestone: { title: "Demo M7" },
            labels: [],
            assignees: [],
            blockedBy: [],
          },
        ],
        pulls: [],
        worktrees: [],
        integration: {
          ...project.integration,
          sha: "7654321",
          dirty: [],
          statusLine: "milestone-7",
          aheadMain: 0,
          behindMain: 0,
          hasRemoteBranch: true,
        },
      };
    };
    let rejectRun: ((error: Error) => void) | null = null;
    const cancelled: string[] = [];
    const agentRunner = {
      run: (input: AgentExecutionInput) => {
        void input.onProgress?.({
          eventCount: 4,
          summary: "正在检查仓库",
          event: { sequence: 4, kind: "TOOL", title: "使用工具 Read", detail: "/tmp/domain.md" },
        });
        return new Promise<AgentExecutionResult>((_resolve, reject) => {
          rejectRun = reject;
        });
      },
      cancel: (runId: string) => {
        cancelled.push(runId);
        rejectRun?.(new Error("cancelled by test"));
        return true;
      },
    };
    const server = await createQianshouServer({
      configPath,
      statePath,
      collector,
      agentRunner,
      refreshIntervalMs: 0,
    });
    servers.push(server);
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    const address = server.address() as AddressInfo;
    const baseUrl = `http://127.0.0.1:${address.port}`;

    const discussion = await (
      await fetch(`${baseUrl}/api/issues/224/conversations`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ role: "DISCUSSION", engine: "claude" }),
      })
    ).json();
    const turn = await (
      await fetch(`${baseUrl}/api/conversations/${discussion.id}/messages`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ text: "检查现状" }),
      })
    ).json();

    let running: any;
    for (let attempt = 0; attempt < 20; attempt += 1) {
      running = await (await fetch(`${baseUrl}/api/issues/224/collaboration`)).json();
      if (running.turns[0]?.progress?.eventCount === 4) break;
      await new Promise((resolve) => setTimeout(resolve, 10));
    }
    expect(running.turns[0]).toMatchObject({
      status: "RUNNING",
      progress: {
        summary: "正在检查仓库",
        events: [{ sequence: 4, kind: "TOOL", title: "使用工具 Read", detail: "/tmp/domain.md" }],
      },
    });

    const cancel = await fetch(`${baseUrl}/api/turns/${turn.id}/cancel`, { method: "POST" });
    expect(cancel.status).toBe(200);
    expect(cancelled).toEqual([turn.id]);
    const stopped = await (await fetch(`${baseUrl}/api/issues/224/collaboration`)).json();
    expect(stopped.turns[0]).toMatchObject({
      status: "CANCELLED",
      progress: { summary: "运行已停止" },
    });
    expect(collectorCalls).toBe(1);
  });

  it("serves the last verified snapshot when gh exits without network diagnostics", async () => {
    const directory = await mkdtemp(join(tmpdir(), "qianshou-gh-transient-"));
    const configPath = join(directory, "projects.json");
    const statePath = join(directory, "state.json");
    await writeFile(
      configPath,
      JSON.stringify({
        projects: [
          {
            id: "demo",
            repository: { slug: "owner/repo", path: "/tmp/repo" },
            milestone: { number: 7 },
            integration: { branch: "milestone-7", worktree: "/tmp/repo-m7", baseBranch: "main" },
            refreshSeconds: 30,
            defaults: { developerEngine: "codex", reviewerEngine: "claude" },
          },
        ],
      }),
    );
    let calls = 0;
    const collector: ExternalCollector = async (project) => {
      calls += 1;
      if (calls > 1) {
        throw new Error(
          "Command failed: gh api repos/owner/repo/pulls?state=all&sort=updated&direction=desc&per_page=100",
        );
      }
      return {
        source: {
          github: "gh CLI",
          git: "git CLI",
          collectedAt: "2026-08-14T00:00:00.000Z",
          stale: false,
          warning: null,
        },
        repository: {
          nameWithOwner: "owner/repo",
          url: "https://github.com/owner/repo",
          defaultBranch: "main",
        },
        milestone: { number: 7, title: "Demo M7", state: "open", open_issues: 1, closed_issues: 0 },
        githubIssues: [
          {
            number: 224,
            title: "Lifecycle",
            body: "Acceptance contract",
            state: "OPEN",
            url: "https://github.com/owner/repo/issues/224",
            updatedAt: "2026-08-14T00:00:00.000Z",
            milestone: { title: "Demo M7" },
            labels: [],
            assignees: [],
            blockedBy: [],
          },
        ],
        pulls: [],
        worktrees: [],
        integration: {
          ...project.integration,
          sha: "7654321",
          dirty: [],
          statusLine: "milestone-7",
          aheadMain: 0,
          behindMain: 0,
          hasRemoteBranch: true,
        },
      };
    };
    const server = await createQianshouServer({
      configPath,
      statePath,
      collector,
      refreshIntervalMs: 0,
    });
    servers.push(server);
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    const address = server.address() as AddressInfo;
    const baseUrl = `http://127.0.0.1:${address.port}`;

    expect((await fetch(`${baseUrl}/api/status`)).status).toBe(200);
    const fallback = await fetch(`${baseUrl}/api/status`);
    expect(fallback.status).toBe(200);
    expect(await fallback.json()).toMatchObject({
      source: { stale: true, warning: expect.stringContaining("GitHub 暂时断线") },
      issues: [{ number: 224 }],
    });
  });
});
