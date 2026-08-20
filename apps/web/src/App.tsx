import type {
  Criterion,
  DependencyJudgment,
  Issue,
  ResolveStopConditionRequest,
} from "@qianshou/api-client";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { type FormEvent, useEffect, useState } from "react";
import type { FactsClient } from "./facts.js";
import type { WorkflowClient } from "./workflow.js";

type Scope = { type: "milestone"; number: number } | { type: "issue"; number: number };

const CONTROL_LABEL = "type:milestone-control";

function issueIdentity(projectId: string, issueNumber: number) {
  return `${projectId}:${issueNumber}`;
}

function errorMessage(error: unknown) {
  if (error instanceof Error) return error.message;
  if (typeof error === "string") return error;
  if (typeof error !== "object" || error === null) return "读取当前事实失败。";

  const candidate = error as {
    message?: unknown;
    error?: { message?: unknown; code?: unknown };
  };
  if (typeof candidate.error?.message === "string") return candidate.error.message;
  if (typeof candidate.message === "string") return candidate.message;
  return "读取当前事实失败。";
}

function DependencyState({ dependency }: { dependency: DependencyJudgment }) {
  if (dependency.status === "READY") {
    return (
      <div className="dependency dependency-ready">
        <strong>依赖已满足</strong>
        <span>仅表示 GitHub 前置依赖已关闭，不代表全部交付门禁完成。</span>
      </div>
    );
  }

  if (dependency.status === "BLOCKED") {
    return (
      <div className="dependency dependency-blocked">
        <strong>被 {dependency.blockedBy.map((number) => `#${number}`).join("、")} 阻塞</strong>
        <span>前置 Issue 尚未全部关闭。</span>
      </div>
    );
  }

  return (
    <div className="dependency dependency-error">
      <strong>依赖判断失败</strong>
      <span>{dependency.error.message}</span>
      <code>{dependency.error.code}</code>
    </div>
  );
}

function IssueCard({ issue, projectId }: { issue: Issue; projectId: string }) {
  const isControl = issue.labels.includes(CONTROL_LABEL);
  return (
    <article className="issue-card" data-issue-key={issueIdentity(projectId, issue.number)}>
      <header>
        <div>
          <span className="issue-number">#{issue.number}</span>
          {isControl ? <span className="control-badge">MILESTONE CONTROL</span> : null}
        </div>
        <span className={`state state-${issue.state.toLowerCase()}`}>{issue.state}</span>
      </header>
      <h3>{issue.title}</h3>
      <ul className="labels" aria-label="标签">
        {issue.labels.length > 0 ? (
          issue.labels.map((label) => <li key={label}>{label}</li>)
        ) : (
          <li className="muted-label">无标签</li>
        )}
      </ul>
      <DependencyState dependency={issue.dependency} />
    </article>
  );
}

function ScopeError({ error }: { error: unknown }) {
  return (
    <section className="error-panel" role="alert">
      <span className="eyebrow">FACTS UNAVAILABLE</span>
      <h2>没有足够证据显示当前 Scope</h2>
      <p>{errorMessage(error)}</p>
      <small>页面不会把读取失败解释成“无依赖”或空列表。</small>
    </section>
  );
}

let actionSequence = 0;

function actionKey(prefix: string) {
  actionSequence += 1;
  return `${prefix}-${Date.now()}-${actionSequence}`;
}

function terminalResult(value: unknown) {
  if (typeof value !== "object" || value === null) return null;
  const result = (value as { result?: unknown }).result;
  return typeof result === "string" ? result : null;
}

function IssueWorkbench({
  projectId,
  issueNumber,
  workflow,
}: {
  projectId: string;
  issueNumber: number;
  workflow: WorkflowClient;
}) {
  const queryClient = useQueryClient();
  const queryKey = ["projects", projectId, "issues", issueNumber, "workspace"] as const;
  const workspaceQuery = useQuery({
    queryKey,
    queryFn: () => workflow.getWorkspace(projectId, issueNumber),
    refetchInterval: (query) =>
      query.state.data?.runs.some((run) => run.state === "QUEUED" || run.state === "RUNNING")
        ? 1000
        : false,
  });
  const workspace = workspaceQuery.data;
  const [mainCheckoutPath, setMainCheckoutPath] = useState("");
  const [engineId, setEngineId] = useState("");
  const [conversationId, setConversationId] = useState("");
  const [prompt, setPrompt] = useState("");
  const [briefContent, setBriefContent] = useState("");
  const [briefVersionId, setBriefVersionId] = useState("");
  const [issueDoDText, setIssueDoDText] = useState("[]");
  const [actionError, setActionError] = useState<string | null>(null);
  const [bindingNote, setBindingNote] = useState<string | null>(null);
  const [stopResolutions, setStopResolutions] = useState<
    Record<string, ResolveStopConditionRequest["resolution"]>
  >({});
  const [stopOutcomes, setStopOutcomes] = useState<Record<string, string>>({});

  useEffect(() => {
    if (!workspace) return;
    if (!engineId && workspace.engines[0]) setEngineId(workspace.engines[0].id);
    if (!conversationId && workspace.conversations[0]) {
      setConversationId(workspace.conversations[0].id);
    }
    const draft = workspace.briefVersions.find((brief) => brief.status === "DRAFT");
    if (!briefVersionId && draft) setBriefVersionId(draft.id);
  }, [workspace, engineId, conversationId, briefVersionId]);

  async function refreshWorkspace() {
    await queryClient.invalidateQueries({ queryKey });
  }

  const bindingMutation = useMutation({
    mutationFn: () => workflow.establishBinding(projectId, mainCheckoutPath),
    onSuccess: async () => {
      setBindingNote("main checkout 已验证并绑定。后续每次运行仍会重新校验。");
      setActionError(null);
      await refreshWorkspace();
    },
    onError: (error) => setActionError(errorMessage(error)),
  });
  const conversationMutation = useMutation({
    mutationFn: () =>
      workflow.createConversation(projectId, issueNumber, {
        engineId,
        idempotencyKey: actionKey("conversation"),
      }),
    onSuccess: async (conversation) => {
      setConversationId(conversation.id);
      setActionError(null);
      await refreshWorkspace();
    },
    onError: (error) => setActionError(errorMessage(error)),
  });
  const runMutation = useMutation({
    mutationFn: () =>
      workflow.startRun(projectId, issueNumber, conversationId, {
        prompt,
        idempotencyKey: actionKey("turn"),
      }),
    onSuccess: async () => {
      setPrompt("");
      setActionError(null);
      await refreshWorkspace();
    },
    onError: (error) => setActionError(errorMessage(error)),
  });
  const cancelMutation = useMutation({
    mutationFn: (runId: string) => workflow.cancelRun(projectId, issueNumber, runId),
    onSuccess: async () => {
      setActionError(null);
      await refreshWorkspace();
    },
    onError: (error) => setActionError(errorMessage(error)),
  });
  const briefMutation = useMutation({
    mutationFn: (content: string) => {
      if (!workspace?.issue?.updatedAt || !workspace.currentIssueBodySha256) {
        throw new Error("当前 GitHub Issue 证据不可用，不能冻结 BriefVersion。");
      }
      return workflow.createBrief(projectId, issueNumber, {
        content,
        sourceConversationId: conversationId,
        idempotencyKey: actionKey("brief"),
        expectedIssueUpdatedAt: workspace.issue.updatedAt,
        expectedIssueBodySha256: workspace.currentIssueBodySha256,
      });
    },
    onSuccess: async (brief) => {
      setBriefVersionId(brief.id);
      setActionError(null);
      await refreshWorkspace();
    },
    onError: (error) => setActionError(errorMessage(error)),
  });
  const adoptionMutation = useMutation({
    mutationFn: () => {
      if (!workspace?.issue?.updatedAt || !workspace.currentIssueBodySha256) {
        throw new Error("当前 GitHub Issue 证据不可用，不能采用 DeliveryBaseline。");
      }
      let issueDoD: Criterion[];
      try {
        const parsed = JSON.parse(issueDoDText) as unknown;
        if (!Array.isArray(parsed)) throw new Error("not an array");
        issueDoD = parsed as Criterion[];
      } catch {
        throw new Error("Issue DoD 必须是结构化 JSON 数组。");
      }
      return workflow.adoptBaseline(projectId, issueNumber, {
        briefVersionId,
        adoptionKey: actionKey("adoption"),
        expectedIssueUpdatedAt: workspace.issue.updatedAt,
        expectedIssueBodySha256: workspace.currentIssueBodySha256,
        issueDoD,
      });
    },
    onSuccess: async () => {
      setActionError(null);
      await refreshWorkspace();
    },
    onError: (error) => setActionError(errorMessage(error)),
  });
  const resolveMutation = useMutation({
    mutationFn: ({
      stopId,
      resolution,
      note,
    }: {
      stopId: string;
      resolution: ResolveStopConditionRequest["resolution"];
      note: string;
    }) =>
      workflow.resolveStop(projectId, issueNumber, stopId, {
        resolution,
        outcome: { note },
      }),
    onSuccess: refreshWorkspace,
    onError: (error) => setActionError(errorMessage(error)),
  });

  const selectedRun = workspace?.runs.filter((run) => run.conversationId === conversationId).at(-1);
  const selectedBrief = workspace?.briefVersions.find((brief) => brief.id === briefVersionId);
  const eventsQuery = useQuery({
    queryKey: ["projects", projectId, "issues", issueNumber, "runs", selectedRun?.id, "events"],
    queryFn: () => {
      if (!selectedRun) throw new Error("Run 缺失。");
      return workflow.listRunEvents(projectId, issueNumber, selectedRun.id, 0, 1000);
    },
    enabled: Boolean(selectedRun),
    refetchInterval:
      selectedRun?.state === "QUEUED" || selectedRun?.state === "RUNNING" ? 1000 : false,
  });

  if (workspaceQuery.isPending) {
    return (
      <section className="loading-state workbench-loading">
        <span />
        <p>正在重建 Discussion 与 DeliveryBaseline 证据…</p>
      </section>
    );
  }
  if (workspaceQuery.isError) return <ScopeError error={workspaceQuery.error} />;
  if (!workspace) return null;

  const githubIssueEvidence = workspace.evidenceSources.find(
    (source) => source.source === "GITHUB" && source.kind === "ISSUE_AND_DEPENDENCIES",
  );
  const primaryActions = workspace.allowedActions.filter(
    (action) => !["VIEW_DISCUSSION", "START_DISCUSSION", "RESOLVE_STOP"].includes(action),
  );
  const discussionAllowed = workspace.allowedActions.includes("START_DISCUSSION");
  const stopResolutionAllowed = workspace.allowedActions.includes("RESOLVE_STOP");

  return (
    <section className="workbench" aria-label="Discussion 与 DeliveryBaseline 工作台">
      <header className="workbench-title">
        <div>
          <span className="eyebrow">03 / ISSUE WORKSPACE</span>
          <h2>Discussion 始终开放</h2>
          <p>讨论可以持续；只有你明确采用 BriefVersion 后，交付基线才会冻结。</p>
        </div>
        <span
          className={`evidence-state evidence-${(githubIssueEvidence?.state ?? "missing").toLowerCase()}`}
        >
          GITHUB · {githubIssueEvidence?.state ?? "MISSING"}
        </span>
      </header>

      <section className="delivery-authority" aria-label="Server Delivery Capability">
        <div>
          <span className="eyebrow">SERVER CAPABILITY</span>
          <h3>{workspace.derivedStage ?? "当前没有可靠的 Delivery Stage"}</h3>
          {primaryActions.length > 0 ? (
            <ul className="allowed-action-list" aria-label="Server 允许的主要交付动作">
              {primaryActions.map((action) => (
                <li key={action}>
                  <code>{action}</code>
                </li>
              ))}
            </ul>
          ) : (
            <p>Server 当前未允许主要交付动作</p>
          )}
        </div>
        <ul className="evidence-source-list" aria-label="当前证据来源">
          {workspace.evidenceSources.map((source) => (
            <li key={`${source.source}:${source.kind}`}>
              <strong>
                {source.source} · {source.state}
              </strong>
              <small>{source.kind}</small>
              <time dateTime={source.observedAt}>{source.observedAt}</time>
            </li>
          ))}
        </ul>
      </section>

      {workspace.blockedReasons.map((reason) => (
        <div className="workbench-alert" role="alert" key={reason.code}>
          <strong>{reason.code}</strong>
          <span>{reason.message}</span>
        </div>
      ))}
      {workspace.delivery.deliveryPaused ? (
        <div className="workbench-alert pause-alert" role="alert">
          <strong>交付已暂停</strong>
          <span>Discussion 仍可使用；先处理下面的 StopCondition，再恢复交付动作。</span>
        </div>
      ) : null}
      {actionError ? (
        <div className="workbench-alert" role="alert">
          <strong>动作未完成</strong>
          <span>{actionError}</span>
        </div>
      ) : null}

      <div className="workbench-grid">
        <section className="workbench-card discussion-card">
          <div className="card-index">A / DISCUSS</div>
          <h3>持续对话</h3>
          <p>
            每个 Conversation 固定一个 Engine。切换 Engine 会新建 Conversation，不伪造会话迁移。
          </p>

          <details className="binding-disclosure">
            <summary>首次绑定本机 main checkout</summary>
            <label className="field compact-field">
              <span>Main checkout 绝对路径</span>
              <input
                value={mainCheckoutPath}
                onChange={(event) => setMainCheckoutPath(event.target.value)}
                placeholder="/Users/operator/work/qianshou"
              />
            </label>
            <button
              className="secondary-action"
              type="button"
              disabled={!mainCheckoutPath || bindingMutation.isPending}
              onClick={() => bindingMutation.mutate()}
            >
              验证并绑定
            </button>
            {bindingNote ? <p className="binding-note">{bindingNote}</p> : null}
          </details>

          <div className="inline-controls">
            <label>
              <span>Engine</span>
              <select value={engineId} onChange={(event) => setEngineId(event.target.value)}>
                {workspace.engines.map((engine) => (
                  <option value={engine.id} key={engine.id}>
                    {engine.id} · {engine.adapter}
                  </option>
                ))}
              </select>
            </label>
            <button
              className="secondary-action"
              type="button"
              disabled={!engineId || !discussionAllowed || conversationMutation.isPending}
              onClick={() => conversationMutation.mutate()}
            >
              新建 Discussion
            </button>
          </div>

          {workspace.conversations.length > 0 ? (
            <label className="field compact-field">
              <span>Conversation</span>
              <select
                value={conversationId}
                onChange={(event) => setConversationId(event.target.value)}
              >
                {workspace.conversations.map((conversation) => (
                  <option value={conversation.id} key={conversation.id}>
                    {conversation.engineId} · {conversation.status} · {conversation.id.slice(-8)}
                  </option>
                ))}
              </select>
            </label>
          ) : (
            <p className="empty-copy">还没有 Discussion Conversation。</p>
          )}

          <label className="field compact-field">
            <span>本轮讨论</span>
            <textarea
              value={prompt}
              onChange={(event) => setPrompt(event.target.value)}
              placeholder="继续澄清目标、约束、验收或风险…"
            />
          </label>
          <button
            className="primary-action"
            type="button"
            disabled={
              !conversationId || !prompt.trim() || !discussionAllowed || runMutation.isPending
            }
            onClick={() => runMutation.mutate()}
          >
            继续对话
          </button>

          {selectedRun ? (
            <div className="run-strip">
              <span>{selectedRun.state}</span>
              <code>{selectedRun.id}</code>
              {terminalResult(selectedRun.terminalDetail) ? (
                <>
                  <p>{terminalResult(selectedRun.terminalDetail)}</p>
                  <button
                    className="text-action"
                    type="button"
                    disabled={briefMutation.isPending}
                    onClick={() =>
                      briefMutation.mutate(terminalResult(selectedRun.terminalDetail) ?? "")
                    }
                  >
                    生成 BriefVersion
                  </button>
                </>
              ) : null}
              {selectedRun.state === "RUNNING" ? (
                <button
                  className="danger-action"
                  type="button"
                  disabled={cancelMutation.isPending}
                  onClick={() => cancelMutation.mutate(selectedRun.id)}
                >
                  取消本轮运行
                </button>
              ) : null}
            </div>
          ) : null}
          {eventsQuery.data?.events.length ? (
            <ol className="event-list">
              {eventsQuery.data.events.map((event) => (
                <li key={event.sequence}>
                  <span>{event.kind}</span>
                  <code>{JSON.stringify(event.payload)}</code>
                </li>
              ))}
            </ol>
          ) : null}
        </section>

        <section className="workbench-card brief-card">
          <div className="card-index">B / BRIEF</div>
          <h3>冻结开发说明</h3>
          <p>
            BriefVersion 是不可变版本，并绑定生成时的 GitHub Issue 证据。旧 Brief 不能套用到新需求。
          </p>
          <label className="field compact-field">
            <span>开发说明</span>
            <textarea
              className="brief-editor"
              value={briefContent}
              onChange={(event) => setBriefContent(event.target.value)}
              placeholder="目标、决策、验收、Non-goals、约束与开放问题"
            />
          </label>
          <button
            className="secondary-action"
            type="button"
            disabled={!conversationId || !briefContent.trim() || briefMutation.isPending}
            onClick={() => briefMutation.mutate(briefContent)}
          >
            冻结 BriefVersion
          </button>
          <div className="artifact-stack">
            {workspace.briefVersions.map((brief) => (
              <button
                type="button"
                className={`artifact ${brief.id === briefVersionId ? "artifact-selected" : ""}`}
                key={brief.id}
                onClick={() => {
                  setBriefVersionId(brief.id);
                  setBriefContent(brief.content);
                }}
              >
                <span>{brief.status}</span>
                <strong>{brief.content}</strong>
                <code>{brief.contentSha256.slice(0, 12)}</code>
              </button>
            ))}
          </div>
        </section>

        <section className="workbench-card adoption-card">
          <div className="card-index">C / ADOPT</div>
          <h3>人工采用</h3>
          <p>
            这个动作会现场重读 GitHub，再冻结 Issue、BriefVersion 与 Issue-specific DoD。没有
            Project Policy。
          </p>
          <label className="field compact-field">
            <span>Issue-specific DoD（结构化 JSON 数组）</span>
            <textarea
              value={issueDoDText}
              onChange={(event) => setIssueDoDText(event.target.value)}
            />
          </label>
          <button
            className="adopt-action"
            type="button"
            disabled={
              !briefVersionId ||
              selectedBrief?.status !== "DRAFT" ||
              githubIssueEvidence?.state !== "COMPLETE" ||
              adoptionMutation.isPending ||
              workspace.delivery.deliveryPaused
            }
            onClick={() => adoptionMutation.mutate()}
          >
            采用为 DeliveryBaseline
          </button>
          <div className="baseline-stack">
            {workspace.delivery.baselines.map((baseline) => (
              <article key={baseline.id}>
                <span>BASELINE · {baseline.sequence}</span>
                <strong>{baseline.briefVersionId}</strong>
                <code>{baseline.issueBodySha256.slice(0, 12)}</code>
              </article>
            ))}
          </div>
        </section>
      </div>

      {workspace.stopConditions.length > 0 ? (
        <section className="stop-board">
          <span className="eyebrow">STOP CONDITIONS</span>
          {workspace.stopConditions.map((stop) => (
            <article key={stop.id}>
              <div>
                <strong>{stop.kind}</strong>
                <p>{stop.reason}</p>
              </div>
              <span>{stop.state}</span>
              {stop.state === "OPEN" ? (
                <div className="stop-decision">
                  <label>
                    <span>处理结果</span>
                    <select
                      value={stopResolutions[stop.id] ?? "CONTINUE"}
                      onChange={(event) =>
                        setStopResolutions((current) => ({
                          ...current,
                          [stop.id]: event.target
                            .value as ResolveStopConditionRequest["resolution"],
                        }))
                      }
                    >
                      <option value="CONTINUE">CONTINUE</option>
                      <option value="ADOPT_NEW_BASELINE">ADOPT_NEW_BASELINE</option>
                      <option value="REPAIR">REPAIR</option>
                      <option value="REREVIEW">REREVIEW</option>
                      <option value="SPLIT">SPLIT</option>
                      <option value="SUPERSEDE">SUPERSEDE</option>
                      <option value="ABANDON">ABANDON</option>
                    </select>
                  </label>
                  <label>
                    <span>处理说明</span>
                    <input
                      value={stopOutcomes[stop.id] ?? ""}
                      onChange={(event) =>
                        setStopOutcomes((current) => ({
                          ...current,
                          [stop.id]: event.target.value,
                        }))
                      }
                      placeholder="记录决定依据或恢复结果"
                    />
                  </label>
                  <button
                    type="button"
                    disabled={
                      resolveMutation.isPending ||
                      !stopResolutionAllowed ||
                      !(stopOutcomes[stop.id] ?? "").trim()
                    }
                    onClick={() =>
                      resolveMutation.mutate({
                        stopId: stop.id,
                        resolution: stopResolutions[stop.id] ?? "CONTINUE",
                        note: stopOutcomes[stop.id] ?? "",
                      })
                    }
                  >
                    记录处理决定
                  </button>
                </div>
              ) : null}
            </article>
          ))}
        </section>
      ) : null}
    </section>
  );
}

export function App({ facts, workflow }: { facts: FactsClient; workflow: WorkflowClient }) {
  const [projectId, setProjectId] = useState("");
  const [scope, setScope] = useState<Scope | null>(null);
  const [issueInput, setIssueInput] = useState("");
  const [inputError, setInputError] = useState<string | null>(null);

  const projectsQuery = useQuery({
    queryKey: ["projects"],
    queryFn: () => facts.listProjects(),
    staleTime: 0,
  });
  const milestonesQuery = useQuery({
    queryKey: ["projects", projectId, "milestones"],
    queryFn: () => facts.listMilestones(projectId),
    enabled: projectId !== "",
    staleTime: 0,
  });
  const milestoneIssuesQuery = useQuery({
    queryKey: ["projects", projectId, "milestones", scope?.number, "issues"],
    queryFn: () => {
      if (scope?.type !== "milestone") throw new Error("Milestone Scope 缺失。");
      return facts.listMilestoneIssues(projectId, scope.number);
    },
    enabled: projectId !== "" && scope?.type === "milestone",
    staleTime: 0,
  });
  const issueQuery = useQuery({
    queryKey: ["projects", projectId, "issues", scope?.number],
    queryFn: () => {
      if (scope?.type !== "issue") throw new Error("Issue Scope 缺失。");
      return facts.getIssue(projectId, scope.number);
    },
    enabled: projectId !== "" && scope?.type === "issue",
    staleTime: 0,
  });

  const selectedProject = projectsQuery.data?.projects.find((project) => project.id === projectId);
  const activeQuery = scope?.type === "milestone" ? milestoneIssuesQuery : issueQuery;
  const issues =
    scope?.type === "milestone"
      ? (milestoneIssuesQuery.data?.issues ?? [])
      : issueQuery.data
        ? [issueQuery.data.issue]
        : [];
  const controlIssues =
    scope?.type === "milestone"
      ? issues.filter((issue) => issue.labels.includes(CONTROL_LABEL))
      : [];

  function selectProject(nextProjectId: string) {
    setProjectId(nextProjectId);
    setScope(null);
    setIssueInput("");
    setInputError(null);
  }

  function openIssue(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const issueNumber = Number(issueInput);
    if (!Number.isInteger(issueNumber) || issueNumber <= 0) {
      setInputError("Issue 编号必须是正整数。");
      return;
    }
    setInputError(null);
    setScope({ type: "issue", number: issueNumber });
  }

  function refreshScope() {
    if (scope) void activeQuery.refetch();
  }

  return (
    <div className="app-shell">
      <header className="topbar">
        <a className="brand" href="/" aria-label="Qianshou 首页">
          <span className="brand-mark">千</span>
          <span>
            <strong>QIɅNSHOU</strong>
            <small>LOCAL DELIVERY FACTS</small>
          </span>
        </a>
        <div className="topbar-status">
          <span className="live-dot" />
          LOOPBACK · EXPLICIT ACTIONS
        </div>
      </header>

      <main>
        <aside className="scope-panel" aria-label="Project 和 Scope 选择">
          <div className="panel-heading">
            <span className="eyebrow">01 / PROJECT</span>
            <h1>选择事实边界</h1>
            <p>Project 是仓库；Milestone 和 Issue 只是当前查看范围。</p>
          </div>

          {projectsQuery.isError ? <ScopeError error={projectsQuery.error} /> : null}
          <label className="field">
            <span>Project</span>
            <select value={projectId} onChange={(event) => selectProject(event.target.value)}>
              <option value="">选择中央 Project</option>
              {projectsQuery.data?.projects.map((project) => (
                <option key={project.id} value={project.id}>
                  {project.id} · {project.repository.creationSlug}
                </option>
              ))}
            </select>
          </label>

          {selectedProject ? (
            <div className="repository-card">
              <span className="eyebrow">REPOSITORY</span>
              <strong>{selectedProject.repository.creationSlug}</strong>
              <small>凭据不会返回浏览器；路径只在你明确绑定时回显</small>
            </div>
          ) : null}

          <div className="scope-divider">
            <span>选择 Scope</span>
          </div>

          <label className="field">
            <span>Milestone</span>
            <select
              value={scope?.type === "milestone" ? String(scope.number) : ""}
              disabled={!projectId || milestonesQuery.isPending || milestonesQuery.isError}
              onChange={(event) => {
                const value = Number(event.target.value);
                setScope(value > 0 ? { type: "milestone", number: value } : null);
              }}
            >
              <option value="">选择当前 Milestone</option>
              {milestonesQuery.data?.milestones.map((milestone) => (
                <option key={milestone.number} value={milestone.number}>
                  #{milestone.number} · {milestone.title} · {milestone.state}
                </option>
              ))}
            </select>
          </label>
          {milestonesQuery.isError ? (
            <p className="field-error" role="alert">
              {errorMessage(milestonesQuery.error)}
            </p>
          ) : null}

          <form className="issue-form" onSubmit={openIssue}>
            <label className="field">
              <span>Issue 编号</span>
              <input
                inputMode="numeric"
                placeholder="例如 31"
                value={issueInput}
                disabled={!projectId}
                onChange={(event) => setIssueInput(event.target.value)}
              />
            </label>
            <button type="submit" disabled={!projectId}>
              打开 Issue
            </button>
            {inputError ? (
              <p className="field-error" role="alert">
                {inputError}
              </p>
            ) : null}
          </form>
        </aside>

        <section className="content-panel" aria-live="polite">
          <header className="content-header">
            <div>
              <span className="eyebrow">02 / CURRENT SCOPE</span>
              <h2>
                {scope
                  ? `${scope.type === "milestone" ? "Milestone" : "Issue"} #${scope.number}`
                  : "等待选择"}
              </h2>
              <p>
                {selectedProject
                  ? `${selectedProject.id} / ${selectedProject.repository.creationSlug}`
                  : "先选择一个中央 Project。"}
              </p>
            </div>
            <button
              className="refresh-button"
              type="button"
              disabled={!scope || activeQuery.isFetching}
              onClick={refreshScope}
            >
              {activeQuery.isFetching ? "正在刷新…" : "刷新当前 Scope"}
            </button>
          </header>

          {!scope ? (
            <section className="empty-state">
              <span>PROJECT → SCOPE → FACTS</span>
              <h2>选择 Milestone，或按编号打开 Issue</h2>
              <p>Milestone 保持只读；按编号打开 Issue 后，才会出现需要明确点击的本机工作流动作。</p>
            </section>
          ) : null}

          {scope && activeQuery.isPending ? (
            <section className="loading-state" aria-label="正在读取当前 Scope">
              <span />
              <p>正在从 GitHub 刷新当前事实…</p>
            </section>
          ) : null}

          {scope && activeQuery.isError ? <ScopeError error={activeQuery.error} /> : null}

          {scope && activeQuery.isSuccess && controlIssues.length > 1 ? (
            <section className="error-panel" role="alert">
              <span className="eyebrow">GOVERNANCE CONFLICT</span>
              <h2>发现多个 Milestone Control Issue</h2>
              <p>
                {controlIssues.map((issue) => `#${issue.number}`).join("、")} 同时带有{" "}
                {CONTROL_LABEL}。
              </p>
              <small>证据互相矛盾，页面不会静默选择其中一个。</small>
            </section>
          ) : null}

          {scope && activeQuery.isSuccess && controlIssues.length <= 1 && issues.length === 0 ? (
            <section className="empty-state">
              <span>EMPTY · VERIFIED RESPONSE</span>
              <h2>当前 Scope 没有 Issue</h2>
              <p>这是 API 成功返回的空结果，不是读取失败。</p>
            </section>
          ) : null}

          {scope && activeQuery.isSuccess && controlIssues.length <= 1 && issues.length > 0 ? (
            <>
              <div className="issue-grid">
                {issues.map((issue) => (
                  <IssueCard
                    key={issueIdentity(projectId, issue.number)}
                    issue={issue}
                    projectId={projectId}
                  />
                ))}
              </div>
              {scope.type === "issue" ? (
                <IssueWorkbench
                  projectId={projectId}
                  issueNumber={scope.number}
                  workflow={workflow}
                />
              ) : null}
            </>
          ) : null}
        </section>
      </main>
    </div>
  );
}
