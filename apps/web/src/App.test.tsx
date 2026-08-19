import type { Issue, IssueWorkspace, Milestone, Project } from "@qianshou/api-client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { App } from "./App.js";
import type { FactsClient } from "./facts.js";
import type { WorkflowClient } from "./workflow.js";

const projects: Project[] = [
  {
    id: "qianshou",
    repository: { provider: "github", id: 101, creationSlug: "ai-daming/qianshou" },
  },
  {
    id: "mamamate",
    repository: { provider: "github", id: 202, creationSlug: "ai-daming/mamamate" },
  },
];

const milestones: Record<string, Milestone[]> = {
  qianshou: [{ number: 1, title: "Qianshou M1", state: "OPEN" }],
  mamamate: [{ number: 7, title: "Mamamate M7", state: "OPEN" }],
};

const readyIssue: Issue = {
  number: 31,
  title: "Qianshou React Scope",
  state: "OPEN",
  labels: ["type:feature"],
  dependency: { status: "READY" },
};

function makeFacts(overrides: Partial<FactsClient> = {}): FactsClient {
  return {
    listProjects: vi.fn(async () => ({ projects })),
    listMilestones: vi.fn(async (projectId) => ({
      projectId,
      milestones: milestones[projectId] ?? [],
    })),
    listMilestoneIssues: vi.fn(async (projectId, milestoneNumber) => ({
      projectId,
      milestoneNumber,
      issues:
        projectId === "qianshou"
          ? [readyIssue]
          : [{ ...readyIssue, title: "Mamamate issue with the same number" }],
    })),
    getIssue: vi.fn(async (projectId, issueNumber) => ({
      projectId,
      issue: { ...readyIssue, number: issueNumber },
    })),
    ...overrides,
  };
}

const workspace: IssueWorkspace = {
  projectId: "qianshou",
  issueNumber: 31,
  githubStatus: "CURRENT",
  issue: {
    ...readyIssue,
    body: "Goal v1",
    updatedAt: "2026-08-19T15:09:52Z",
  },
  currentIssueBodySha256: "a".repeat(64),
  engines: [{ id: "codex", adapter: "codex" }],
  conversations: [
    {
      id: "conversation-1",
      role: "discussion",
      engineId: "codex",
      runnerProjectBindingId: "binding-1",
      vendorSessionEstablished: true,
      status: "COMPLETED",
      createdAt: "2026-08-19T15:10:00Z",
      archivedAt: null,
    },
  ],
  briefVersions: [
    {
      id: "brief-1",
      content: "Adopt this development brief",
      contentSha256: "b".repeat(64),
      sourceIssueUpdatedAt: "2026-08-19T15:09:52Z",
      sourceIssueBodySha256: "a".repeat(64),
      status: "DRAFT",
      createdAt: "2026-08-19T15:12:00Z",
    },
  ],
  runs: [
    {
      id: "run-1",
      conversationId: "conversation-1",
      state: "COMPLETED",
      queuedAt: "2026-08-19T15:11:00Z",
      startedAt: "2026-08-19T15:11:01Z",
      terminalAt: "2026-08-19T15:11:02Z",
      terminalDetail: { result: "Agent answer" },
    },
  ],
  delivery: { activeTrack: null, baselines: [], deliveryPaused: false },
  stopConditions: [],
  blockedReasons: [],
};

function makeWorkflow(overrides: Partial<WorkflowClient> = {}): WorkflowClient {
  return {
    getWorkspace: vi.fn(async () => workspace),
    establishBinding: vi.fn(async () => ({
      id: "binding-1",
      runnerId: "runner-1",
      projectId: "qianshou",
      mainCheckoutPath: "/work/qianshou",
      repositoryIdAtBinding: 101,
      createdAt: "2026-08-19T15:00:00Z",
    })),
    createConversation: vi.fn(async () => workspace.conversations[0]!),
    startRun: vi.fn(async () => workspace.runs[0]!),
    cancelRun: vi.fn(async () => ({
      runId: "run-1",
      issueNumber: 31,
      cancellationRequested: true as const,
    })),
    listRunEvents: vi.fn(async () => ({ runId: "run-1", events: [], nextCursor: null })),
    createBrief: vi.fn(async () => workspace.briefVersions[0]!),
    adoptBaseline: vi.fn(async () => ({
      id: "baseline-1",
      trackId: "track-1",
      sequence: 1,
      adoptionKey: "adopt-1",
      issueUpdatedAt: "2026-08-19T15:09:52Z",
      issueBody: "Goal v1",
      issueBodySha256: "a".repeat(64),
      briefVersionId: "brief-1",
      issueDoD: [],
      payloadSha256: "c".repeat(64),
      adoptedAt: "2026-08-19T15:13:00Z",
    })),
    resolveStop: vi.fn(),
    ...overrides,
  };
}

function renderApp(facts: FactsClient, workflow = makeWorkflow()) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <App facts={facts} workflow={workflow} />
    </QueryClientProvider>,
  );
}

describe("Discussion and DeliveryBaseline workbench", () => {
  it("keeps Discussion visible and requires explicit actions for runs, briefs, and adoption", async () => {
    const facts = makeFacts();
    const workflow = makeWorkflow();
    const user = userEvent.setup();
    renderApp(facts, workflow);

    await chooseProject(user, "qianshou");
    await user.type(screen.getByLabelText("Issue 编号"), "31");
    await user.click(screen.getByRole("button", { name: "打开 Issue" }));

    expect(await screen.findByRole("heading", { name: "Discussion 始终开放" })).toBeInTheDocument();
    expect(screen.getByText("Adopt this development brief")).toBeInTheDocument();

    await user.type(screen.getByLabelText("本轮讨论"), "Continue with this question");
    await user.click(screen.getByRole("button", { name: "继续对话" }));
    expect(workflow.startRun).toHaveBeenCalledWith("qianshou", 31, "conversation-1", {
      prompt: "Continue with this question",
      idempotencyKey: expect.any(String),
    });

    await user.clear(screen.getByLabelText("开发说明"));
    await user.type(screen.getByLabelText("开发说明"), "A newly frozen brief");
    await user.click(screen.getByRole("button", { name: "冻结 BriefVersion" }));
    expect(workflow.createBrief).toHaveBeenCalledWith(
      "qianshou",
      31,
      expect.objectContaining({
        content: "A newly frozen brief",
        sourceConversationId: "conversation-1",
        expectedIssueUpdatedAt: "2026-08-19T15:09:52Z",
        expectedIssueBodySha256: "a".repeat(64),
      }),
    );

    await user.click(screen.getByRole("button", { name: "采用为 DeliveryBaseline" }));
    expect(workflow.adoptBaseline).toHaveBeenCalledWith(
      "qianshou",
      31,
      expect.objectContaining({ briefVersionId: "brief-1", issueDoD: [] }),
    );
  });

  it("can cancel the selected running Discussion run", async () => {
    const runningWorkspace: IssueWorkspace = {
      ...workspace,
      runs: [
        {
          ...workspace.runs[0]!,
          state: "RUNNING",
          terminalAt: null,
          terminalDetail: null,
        },
      ],
    };
    const workflow = makeWorkflow({
      getWorkspace: vi.fn(async () => runningWorkspace),
    });
    const user = userEvent.setup();
    renderApp(makeFacts(), workflow);

    await chooseProject(user, "qianshou");
    await user.type(screen.getByLabelText("Issue 编号"), "31");
    await user.click(screen.getByRole("button", { name: "打开 Issue" }));

    await user.click(await screen.findByRole("button", { name: "取消本轮运行" }));
    expect(workflow.cancelRun).toHaveBeenCalledWith("qianshou", 31, "run-1");
  });

  it("keeps Discussion open while a Stop is paused and records the chosen one-shot outcome", async () => {
    const pausedWorkspace: IssueWorkspace = {
      ...workspace,
      briefVersions: [{ ...workspace.briefVersions[0]!, status: "ADOPTED" }],
      delivery: { ...workspace.delivery, deliveryPaused: true },
      stopConditions: [
        {
          id: "stop-1",
          trackId: "track-1",
          baselineId: "baseline-1",
          kind: "SCOPE_CHANGE",
          reason: "GitHub Issue changed after adoption.",
          evidence: { old: "a", current: "b" },
          state: "OPEN",
          createdAt: "2026-08-19T16:00:00Z",
          resolution: null,
          outcome: null,
          resolvedAt: null,
        },
      ],
    };
    const workflow = makeWorkflow({
      getWorkspace: vi.fn(async () => pausedWorkspace),
      resolveStop: vi.fn(async () => ({
        ...pausedWorkspace.stopConditions[0]!,
        state: "RESOLVED" as const,
        resolution: "ADOPT_NEW_BASELINE",
        outcome: { note: "Adopt a new baseline." },
        resolvedAt: "2026-08-19T16:01:00Z",
      })),
    });
    const user = userEvent.setup();
    renderApp(makeFacts(), workflow);

    await chooseProject(user, "qianshou");
    await user.type(screen.getByLabelText("Issue 编号"), "31");
    await user.click(screen.getByRole("button", { name: "打开 Issue" }));

    expect(await screen.findByRole("heading", { name: "Discussion 始终开放" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "采用为 DeliveryBaseline" })).toBeDisabled();
    await user.selectOptions(screen.getByLabelText("处理结果"), "ADOPT_NEW_BASELINE");
    expect(screen.getByRole("button", { name: "记录处理决定" })).toBeDisabled();
    await user.type(
      screen.getByLabelText("处理说明"),
      "Adopt a new baseline after discussing changed scope.",
    );
    await user.click(screen.getByRole("button", { name: "记录处理决定" }));
    expect(workflow.resolveStop).toHaveBeenCalledWith("qianshou", 31, "stop-1", {
      resolution: "ADOPT_NEW_BASELINE",
      outcome: { note: "Adopt a new baseline after discussing changed scope." },
    });
  });

  it("can explicitly generate an immutable BriefVersion from the terminal Agent result", async () => {
    const workflow = makeWorkflow();
    const user = userEvent.setup();
    renderApp(makeFacts(), workflow);

    await chooseProject(user, "qianshou");
    await user.type(screen.getByLabelText("Issue 编号"), "31");
    await user.click(screen.getByRole("button", { name: "打开 Issue" }));

    await user.click(await screen.findByRole("button", { name: "生成 BriefVersion" }));
    expect(workflow.createBrief).toHaveBeenCalledWith(
      "qianshou",
      31,
      expect.objectContaining({
        content: "Agent answer",
        sourceConversationId: "conversation-1",
        expectedIssueUpdatedAt: "2026-08-19T15:09:52Z",
        expectedIssueBodySha256: "a".repeat(64),
      }),
    );
  });
});

async function chooseProject(user: ReturnType<typeof userEvent.setup>, projectId: string) {
  await screen.findByRole("option", { name: new RegExp(`^${projectId} ·`) });
  await user.selectOptions(screen.getByLabelText("Project"), projectId);
}

async function chooseMilestone(user: ReturnType<typeof userEvent.setup>, number: number) {
  await screen.findByRole("option", { name: new RegExp(`^#${number} ·`) });
  await user.selectOptions(screen.getByLabelText("Milestone"), String(number));
}

describe("dynamic Project and Scope navigation", () => {
  it("keeps the same Issue number isolated by Project", async () => {
    const facts = makeFacts();
    const user = userEvent.setup();
    renderApp(facts);

    await chooseProject(user, "qianshou");
    await chooseMilestone(user, 1);
    expect(await screen.findByText("Qianshou React Scope")).toBeInTheDocument();

    await chooseProject(user, "mamamate");
    expect(screen.queryByText("Qianshou React Scope")).not.toBeInTheDocument();
    await chooseMilestone(user, 7);
    expect(await screen.findByText("Mamamate issue with the same number")).toBeInTheDocument();

    expect(facts.listMilestoneIssues).toHaveBeenCalledWith("qianshou", 1);
    expect(facts.listMilestoneIssues).toHaveBeenCalledWith("mamamate", 7);
  });

  it("opens a direct Issue and refreshes the active Scope", async () => {
    const facts = makeFacts();
    const user = userEvent.setup();
    renderApp(facts);

    await chooseProject(user, "qianshou");
    await user.type(screen.getByLabelText("Issue 编号"), "31");
    await user.click(screen.getByRole("button", { name: "打开 Issue" }));
    expect(await screen.findByText("Qianshou React Scope")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "刷新当前 Scope" }));
    expect(facts.getIssue).toHaveBeenCalledTimes(2);
    expect(facts.getIssue).toHaveBeenLastCalledWith("qianshou", 31);
  });
});

describe("truthful dependency and failure states", () => {
  it("renders READY, BLOCKED, and ERROR without claiming full start authorization", async () => {
    const issues: Issue[] = [
      readyIssue,
      {
        ...readyIssue,
        number: 32,
        title: "Blocked item",
        dependency: { status: "BLOCKED", blockedBy: [29, 30] },
      },
      {
        ...readyIssue,
        number: 33,
        title: "Unknown item",
        dependency: {
          status: "ERROR",
          error: { code: "DEPENDENCY_EVIDENCE_INCOMPLETE", message: "依赖证据不完整" },
        },
      },
    ];
    const facts = makeFacts({
      listMilestoneIssues: vi.fn(async (projectId, milestoneNumber) => ({
        projectId,
        milestoneNumber,
        issues,
      })),
    });
    const user = userEvent.setup();
    renderApp(facts);

    await chooseProject(user, "qianshou");
    await chooseMilestone(user, 1);

    expect(await screen.findByText("依赖已满足")).toBeInTheDocument();
    expect(screen.getByText("被 #29、#30 阻塞")).toBeInTheDocument();
    expect(screen.getByText("依赖证据不完整")).toBeInTheDocument();
    expect(screen.queryByText("能开工")).not.toBeInTheDocument();
  });

  it("shows request failure instead of an empty-list message", async () => {
    const facts = makeFacts({
      listMilestoneIssues: vi.fn(async () => {
        throw new Error("Current GitHub facts could not be read completely.");
      }),
    });
    const user = userEvent.setup();
    renderApp(facts);

    await chooseProject(user, "qianshou");
    await chooseMilestone(user, 1);

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Current GitHub facts could not be read completely.");
    expect(screen.queryByText("当前 Scope 没有 Issue")).not.toBeInTheDocument();
  });

  it("preserves a plain-text request failure instead of replacing it with a fallback", async () => {
    const facts = makeFacts({
      listMilestoneIssues: vi.fn(async () => {
        throw "proxy error: connect ECONNREFUSED 127.0.0.1:41727";
      }),
    });
    const user = userEvent.setup();
    renderApp(facts);

    await chooseProject(user, "qianshou");
    await chooseMilestone(user, 1);

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("proxy error: connect ECONNREFUSED 127.0.0.1:41727");
    expect(alert).not.toHaveTextContent("读取当前事实失败。");
  });

  it("distinguishes a verified empty response from a request failure", async () => {
    const facts = makeFacts({
      listMilestoneIssues: vi.fn(async (projectId, milestoneNumber) => ({
        projectId,
        milestoneNumber,
        issues: [],
      })),
    });
    const user = userEvent.setup();
    renderApp(facts);

    await chooseProject(user, "qianshou");
    await chooseMilestone(user, 1);

    expect(await screen.findByText("当前 Scope 没有 Issue")).toBeInTheDocument();
    expect(screen.getByText("这是 API 成功返回的空结果，不是读取失败。")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("fails closed when one Milestone contains multiple control Issues", async () => {
    const facts = makeFacts({
      listMilestoneIssues: vi.fn(async (projectId, milestoneNumber) => ({
        projectId,
        milestoneNumber,
        issues: [
          { ...readyIssue, labels: ["type:milestone-control"] },
          {
            ...readyIssue,
            number: 1,
            title: "Another control",
            labels: ["type:milestone-control"],
          },
        ],
      })),
    });
    const user = userEvent.setup();
    renderApp(facts);

    await chooseProject(user, "qianshou");
    await chooseMilestone(user, 1);

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("发现多个 Milestone Control Issue");
    expect(within(alert).getByText(/#31/)).toBeInTheDocument();
    expect(within(alert).getByText(/#1/)).toBeInTheDocument();
  });
});
