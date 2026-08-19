import type { DependencyJudgment, Issue } from "@qianshou/api-client";
import { useQuery } from "@tanstack/react-query";
import { type FormEvent, useState } from "react";
import type { FactsClient } from "./facts.js";

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

export function App({ facts }: { facts: FactsClient }) {
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
          READ ONLY · GITHUB LIVE
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
              <small>本机路径和凭据不会返回浏览器</small>
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
              <p>页面只展示当前 API 返回的 GitHub 事实，不保存 Scope，也不提供写操作。</p>
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
            <div className="issue-grid">
              {issues.map((issue) => (
                <IssueCard
                  key={issueIdentity(projectId, issue.number)}
                  issue={issue}
                  projectId={projectId}
                />
              ))}
            </div>
          ) : null}
        </section>
      </main>
    </div>
  );
}
