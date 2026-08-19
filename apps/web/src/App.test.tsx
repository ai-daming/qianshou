import type { Issue, Milestone, Project } from "@qianshou/api-client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { App } from "./App.js";
import type { FactsClient } from "./facts.js";

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

function renderApp(facts: FactsClient) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <App facts={facts} />
    </QueryClientProvider>,
  );
}

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
