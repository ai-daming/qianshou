import {
  buildTaskContract,
  buildAgentInputPackage,
  controlPatchSchema,
  createConversationSchema,
  createStopConditionSchema,
  dependencyState,
  deriveIssueState,
  deriveSlots,
  nextAction,
  freezeBriefSchema,
  issueKindFromGithub,
  projectsConfigSchema,
  resolveStopConditionSchema,
  sendConversationMessageSchema,
  type GithubIssue,
  type IssueWorkspaceBinding,
  type ProjectConfig,
  type PullRequest,
  type StatusSnapshot,
  type Worktree,
} from "@qianshou/core";
import { execFile } from "node:child_process";
import http, { type IncomingMessage, type ServerResponse } from "node:http";
import { readFile } from "node:fs/promises";
import { dirname, extname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { JsonStateStore } from "./state-store.js";
import { CliAgentRunner, type AgentRunner } from "./agent-runner.js";

const execFileAsync = promisify(execFile);
const moduleDirectory = dirname(fileURLToPath(import.meta.url));
const repositoryRoot = resolve(moduleDirectory, "../../..");
const defaultConfigPath = join(repositoryRoot, "config", "projects.json");
const defaultStatePath = join(repositoryRoot, ".qianshou", "state.json");
const webDistribution = join(repositoryRoot, "apps", "web", "dist");
const contentTypes: Record<string, string> = {
  ".html": "text/html; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".svg": "image/svg+xml; charset=utf-8",
};

export const DEVELOPMENT_BRIEF_PROMPT = `# 开发说明生成任务

请基于这个 Issue 在当前 Session 中截至目前的完整讨论，生成一份可以直接交给开发 Agent 执行、也可以交给 Review Agent 验收的开发说明。

必须遵守：

1. 仅整理讨论中已经明确确认的结论，不得自行补充或猜测业务决定。
2. 对存在冲突、尚未回答或没有达成一致的内容，统一放入“未决问题”。
3. 区分目标、范围和非范围，不要把背景材料当成实现要求。
4. 验收标准必须可检查；无法确认的验收项放入“未决问题”。
5. 直接输出开发说明 Markdown，不要解释生成过程，也不要向用户提问。

使用以下结构：

# 开发说明
## 目标
## 现状与证据
## 已确认决策
## 实现范围
## 非范围
## 验收标准
## 实现约束
## 未决问题`;

interface RawRepository {
  full_name: string;
  html_url: string;
  default_branch: string;
}
interface RawMilestone {
  number: number;
  title: string;
  state: string;
  open_issues: number;
  closed_issues: number;
}
interface RawIssue {
  number: number;
  title: string;
  body: string | null;
  state: string;
  html_url: string;
  updated_at: string;
  pull_request?: unknown;
  milestone: { title: string } | null;
  labels: Array<{ name: string; color: string }>;
  assignees: Array<{ login: string }>;
}
interface RawPull {
  number: number;
  title: string;
  body: string | null;
  state: string;
  html_url: string;
  updated_at: string;
  merged_at: string | null;
  draft: boolean;
  base: { ref: string } | null;
  head: { ref: string } | null;
}
interface RawDependencyIssue {
  number: number;
  blockedBy: { nodes: Array<{ number: number; state: string }> };
}
interface RawDependencySnapshot {
  data?: { repository?: Record<string, RawDependencyIssue | null> | null };
  errors?: Array<{ message?: string }>;
}
interface ExternalStatus {
  source: StatusSnapshot["source"];
  repository: StatusSnapshot["repository"];
  milestone: StatusSnapshot["milestone"];
  githubIssues: GithubIssue[];
  pulls: PullRequest[];
  worktrees: Worktree[];
  integration: StatusSnapshot["integration"];
}

export type ExternalCollector = (project: ProjectConfig) => Promise<ExternalStatus>;
export type WorktreeCreator = (
  project: ProjectConfig,
  issueNumber: number,
) => Promise<IssueWorkspaceBinding>;

export async function loadProjects(path = defaultConfigPath): Promise<ProjectConfig[]> {
  return projectsConfigSchema.parse(JSON.parse(await readFile(path, "utf8"))).projects;
}

function delay(milliseconds: number) {
  return new Promise((resolvePromise) => setTimeout(resolvePromise, milliseconds));
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function isTransientNetworkError(error: unknown) {
  const message = errorMessage(error).trim();
  if (
    /\bEOF\b|socket hang up|ECONNRESET|ETIMEDOUT|TLS|network|GraphQL request failed/i.test(message)
  )
    return true;
  // gh occasionally exits after a transport interruption without writing stderr.
  // Keep this narrow so explicit auth, validation, and GitHub API errors remain visible.
  return /^Command failed: gh api [^\r\n]+$/i.test(message);
}

async function runCommand(
  file: string,
  args: string[],
  cwd: string,
  options: { retries?: number; timeout?: number } = {},
) {
  const retries = options.retries ?? (file === "gh" ? 2 : 0);
  let lastError: unknown;
  for (let attempt = 0; attempt <= retries; attempt += 1) {
    try {
      const { stdout } = await execFileAsync(file, args, {
        cwd,
        encoding: "utf8",
        maxBuffer: 6 * 1024 * 1024,
        timeout: options.timeout ?? 25_000,
      });
      return stdout.trim();
    } catch (error) {
      lastError = error;
      if (attempt >= retries || !isTransientNetworkError(error)) throw error;
      await delay(350 * (attempt + 1));
    }
  }
  throw lastError;
}

function parseJson<T>(value: string): T {
  return JSON.parse(value) as T;
}

export function parseGitWorktrees(porcelain: string): Worktree[] {
  return porcelain
    .split(/\n\n+/)
    .filter(Boolean)
    .map((block) => {
      const fields = Object.fromEntries(
        block.split("\n").map((line) => {
          const space = line.indexOf(" ");
          return space === -1 ? [line, "true"] : [line.slice(0, space), line.slice(space + 1)];
        }),
      );
      return {
        path: fields.worktree ?? "",
        sha: fields.HEAD ?? null,
        branch: fields.branch?.replace("refs/heads/", "") ?? null,
        detached: fields.detached === "true",
      };
    });
}

function normalizeIssue(issue: RawIssue, blockedBy: GithubIssue["blockedBy"]): GithubIssue {
  return {
    number: issue.number,
    title: issue.title,
    body: issue.body ?? "",
    state: issue.state.toUpperCase(),
    url: issue.html_url,
    updatedAt: issue.updated_at,
    milestone: issue.milestone,
    labels: issue.labels.map(({ name, color }) => ({ name, color })),
    assignees: issue.assignees.map(({ login }) => ({ login })),
    blockedBy,
  };
}

export function parseGithubDependencySnapshot(
  value: string,
): Map<number, GithubIssue["blockedBy"]> {
  const payload = parseJson<RawDependencySnapshot>(value);
  if (payload.errors?.length) {
    throw new Error(
      `GitHub dependency query failed: ${payload.errors.map((error) => error.message ?? "unknown error").join("; ")}`,
    );
  }
  if (!payload.data?.repository)
    throw new Error("GitHub dependency query returned no repository data");
  const result = new Map<number, GithubIssue["blockedBy"]>();
  for (const issue of Object.values(payload.data.repository)) {
    if (!issue) continue;
    result.set(
      issue.number,
      issue.blockedBy.nodes.map(({ number, state }) => ({ number, state: state.toUpperCase() })),
    );
  }
  return result;
}

function githubDependencyQuery(project: ProjectConfig, issueNumbers: number[]) {
  const [owner, name, ...extra] = project.repository.slug.split("/");
  if (!owner || !name || extra.length)
    throw new Error(`invalid GitHub repository slug: ${project.repository.slug}`);
  const issues = issueNumbers
    .map((issueNumber) =>
      [
        `issue${issueNumber}: issue(number: ${issueNumber}) {`,
        "  number",
        "  blockedBy(first: 100) { nodes { number state } }",
        "}",
      ].join("\n"),
    )
    .join("\n");
  return `query { repository(owner: ${JSON.stringify(owner)}, name: ${JSON.stringify(name)}) {\n${issues}\n} }`;
}

function normalizePull(pull: RawPull): PullRequest {
  return {
    number: pull.number,
    title: pull.title,
    body: pull.body ?? "",
    state: pull.merged_at ? "MERGED" : pull.state.toUpperCase(),
    url: pull.html_url,
    baseRefName: pull.base?.ref ?? null,
    headRefName: pull.head?.ref ?? null,
    updatedAt: pull.updated_at,
    isDraft: pull.draft,
  };
}

function issuePulls(issueNumber: number, pulls: PullRequest[]) {
  const pattern = new RegExp(`(?:#|issue[-_/ ]?)${issueNumber}(?!\\d)`, "i");
  return pulls.filter((pull) =>
    pattern.test(`${pull.title}\n${pull.body ?? ""}\n${pull.headRefName ?? ""}`),
  );
}

function issueWorktrees(issueNumber: number, worktrees: Worktree[]) {
  const pattern = new RegExp(`(?:issue|m7)[-_/ ]?${issueNumber}(?!\\d)`, "i");
  return worktrees.filter((item) => pattern.test(`${item.branch ?? ""} ${item.path}`));
}

export const createGitWorktree: WorktreeCreator = async (project, issueNumber) => {
  const branch = `codex/m7-issue-${issueNumber}`;
  const path = join(dirname(project.integration.worktree), `m7-issue-${issueNumber}`);
  const baseSha = await runCommand(
    "git",
    ["rev-parse", project.integration.branch],
    project.integration.worktree,
  );

  await runCommand(
    "git",
    ["worktree", "add", "-b", branch, path, project.integration.branch],
    project.repository.path,
    { timeout: 120_000 },
  );

  const [worktreesRaw, createdBranch, createdSha] = await Promise.all([
    runCommand("git", ["worktree", "list", "--porcelain"], project.repository.path),
    runCommand("git", ["branch", "--show-current"], path),
    runCommand("git", ["rev-parse", "HEAD"], path),
  ]);
  const created = parseGitWorktrees(worktreesRaw).find((item) => item.branch === branch);
  if (!created || createdBranch !== branch || createdSha !== baseSha || created.sha !== baseSha) {
    throw new Error(`Git created ${path}, but Qianshou could not verify its branch and base SHA`);
  }
  return {
    issueNumber,
    branch,
    path: created.path,
    baseBranch: project.integration.branch,
    baseSha,
    createdAt: new Date().toISOString(),
  };
};

export const collectExternalStatus: ExternalCollector = async (project) => {
  const repositoryApi = `repos/${project.repository.slug}`;
  const [
    repoRaw,
    milestonesRaw,
    issuesRaw,
    pullsRaw,
    gitWorktreesRaw,
    branchRaw,
    divergenceRaw,
    remoteBranchRaw,
  ] = await Promise.all([
    runCommand("gh", ["api", repositoryApi], project.repository.path),
    runCommand(
      "gh",
      ["api", `${repositoryApi}/milestones?state=all&per_page=100`],
      project.repository.path,
    ),
    runCommand(
      "gh",
      [
        "api",
        `${repositoryApi}/issues?milestone=${project.milestone.number}&state=all&per_page=100`,
      ],
      project.repository.path,
    ),
    runCommand(
      "gh",
      ["api", `${repositoryApi}/pulls?state=all&sort=updated&direction=desc&per_page=100`],
      project.repository.path,
    ),
    runCommand("git", ["worktree", "list", "--porcelain"], project.repository.path),
    runCommand("git", ["status", "--short", "--branch"], project.integration.worktree),
    runCommand(
      "git",
      [
        "rev-list",
        "--left-right",
        "--count",
        `origin/${project.integration.baseBranch}...${project.integration.branch}`,
      ],
      project.integration.worktree,
    ),
    runCommand(
      "git",
      ["branch", "-r", "--list", `origin/${project.integration.branch}`],
      project.integration.worktree,
    ),
  ]);
  const repository = parseJson<RawRepository>(repoRaw);
  const milestones = parseJson<RawMilestone[]>(milestonesRaw);
  const rawIssues = parseJson<RawIssue[]>(issuesRaw).filter((issue) => !issue.pull_request);
  const issueNumbers = rawIssues.map((issue) => issue.number);
  const dependencySnapshot = issueNumbers.length
    ? parseGithubDependencySnapshot(
        await runCommand(
          "gh",
          ["api", "graphql", "-f", `query=${githubDependencyQuery(project, issueNumbers)}`],
          project.repository.path,
        ),
      )
    : new Map<number, GithubIssue["blockedBy"]>();
  for (const issueNumber of issueNumbers) {
    if (!dependencySnapshot.has(issueNumber))
      throw new Error(`GitHub dependency snapshot missing Issue #${issueNumber}`);
  }
  const githubIssues = rawIssues.map((issue) =>
    normalizeIssue(issue, dependencySnapshot.get(issue.number) ?? []),
  );
  const pulls = parseJson<RawPull[]>(pullsRaw).map(normalizePull);
  const worktrees = parseGitWorktrees(gitWorktreesRaw);
  const milestone = milestones.find((item) => item.number === project.milestone.number) ?? null;
  const divergence = divergenceRaw.split(/\s+/).map(Number);
  const branchLines = branchRaw.split("\n");
  const integrationWorktree = worktrees.find((item) => item.branch === project.integration.branch);
  return {
    source: {
      github: "gh CLI → GitHub REST + GraphQL APIs",
      git: "git CLI",
      collectedAt: new Date().toISOString(),
      stale: false,
      warning: null,
    },
    repository: {
      nameWithOwner: repository.full_name,
      url: repository.html_url,
      defaultBranch: repository.default_branch,
    },
    milestone,
    githubIssues,
    pulls,
    worktrees,
    integration: {
      ...project.integration,
      sha: integrationWorktree?.sha ?? null,
      dirty: branchLines.slice(1).filter(Boolean),
      statusLine: branchLines[0]?.replace(/^##\s*/, "") ?? "",
      behindMain: divergence[0] ?? 0,
      aheadMain: divergence[1] ?? 0,
      hasRemoteBranch: Boolean(remoteBranchRaw),
    },
  };
};

function redactError(error: unknown) {
  return errorMessage(error)
    .replace(/ghp_[A-Za-z0-9_]+/g, "[redacted]")
    .replace(/github_pat_[A-Za-z0-9_]+/g, "[redacted]");
}

async function readBody(request: IncomingMessage, limit = 64 * 1024): Promise<unknown> {
  const chunks: Buffer[] = [];
  let size = 0;
  for await (const rawChunk of request) {
    const chunk = Buffer.isBuffer(rawChunk) ? rawChunk : Buffer.from(rawChunk);
    size += chunk.length;
    if (size > limit) throw new Error("request body too large");
    chunks.push(chunk);
  }
  return chunks.length ? JSON.parse(Buffer.concat(chunks).toString("utf8")) : {};
}

function sendJson(response: ServerResponse, status: number, payload: unknown) {
  response.writeHead(status, {
    "Content-Type": "application/json; charset=utf-8",
    "Cache-Control": "no-store",
  });
  response.end(JSON.stringify(payload));
}

function projectView(project: ProjectConfig) {
  return {
    id: project.id,
    name: `${project.repository.slug} · M${project.milestone.number}`,
    subtitle: "GitHub Milestone",
  };
}

export async function createQianshouServer(
  options: {
    configPath?: string;
    statePath?: string;
    collector?: ExternalCollector;
    agentRunner?: AgentRunner;
    worktreeCreator?: WorktreeCreator;
    refreshIntervalMs?: number;
  } = {},
) {
  const projects = await loadProjects(options.configPath ?? defaultConfigPath);
  const store = new JsonStateStore(options.statePath ?? defaultStatePath);
  const collector = options.collector ?? collectExternalStatus;
  const agentRunner = options.agentRunner ?? new CliAgentRunner();
  const worktreeCreator = options.worktreeCreator ?? createGitWorktree;
  const refreshIntervalMs = options.refreshIntervalMs ?? 10_000;
  const cache = new Map<string, ExternalStatus>();
  const inflight = new Map<string, Promise<ExternalStatus>>();
  const getProject = (id: string | null) =>
    projects.find((project) => project.id === id) ?? projects[0];

  for (const project of projects) await store.recoverOrphanedTurns(project);

  async function getExternal(project: ProjectConfig) {
    const cached = cache.get(project.id);
    if (cached && Date.now() - new Date(cached.source.collectedAt).getTime() < refreshIntervalMs)
      return cached;
    if (!inflight.has(project.id)) {
      const current = collector(project)
        .then((result) => {
          cache.set(project.id, result);
          return result;
        })
        .catch((error: unknown) => {
          const previous = cache.get(project.id);
          if (!previous || !isTransientNetworkError(error)) throw error;
          return {
            ...previous,
            source: {
              ...previous.source,
              stale: true,
              warning: `GitHub 暂时断线，显示最近快照：${redactError(error)}`,
            },
          };
        })
        .finally(() => inflight.delete(project.id));
      inflight.set(project.id, current);
    }
    const result = inflight.get(project.id);
    if (!result) throw new Error("external status collection was not created");
    return result;
  }

  async function buildStatus(project: ProjectConfig): Promise<StatusSnapshot> {
    const external = await getExternal(project);
    const state = await store.read(
      project,
      external.githubIssues.map((issue) => issue.number),
    );
    const projectState = state.projects[project.id];
    if (!projectState) throw new Error(`missing project state: ${project.id}`);
    const issues = external.githubIssues.map((githubIssue) => {
      const projectIssue = {
        number: githubIssue.number,
        label: githubIssue.title,
        kind: issueKindFromGithub(githubIssue),
      } as const;
      const control = projectState.issues[String(projectIssue.number)];
      if (!control) throw new Error(`missing Issue control: ${projectIssue.number}`);
      const worktrees = issueWorktrees(projectIssue.number, external.worktrees);
      const worktree = worktrees[0];
      const dependenciesReady = githubIssue ? dependencyState(githubIssue).ready : false;
      const openStopConditionCount = Object.values(projectState.stopConditions).filter(
        (item) => item.issueNumber === projectIssue.number && item.status === "OPEN",
      ).length;
      return {
        ...projectIssue,
        github: githubIssue,
        pulls: issuePulls(projectIssue.number, external.pulls).map(
          ({ body: _body, ...pull }) => pull,
        ),
        worktrees,
        control,
        attention: { required: openStopConditionCount > 0, openStopConditionCount },
        state: deriveIssueState({ projectIssue, githubIssue, control, openStopConditionCount }),
        slots: deriveSlots(
          control,
          dependenciesReady,
          openStopConditionCount > 0,
          projectIssue.kind,
        ),
        nextAction: nextAction({
          project,
          projectIssue,
          githubIssue,
          control,
          openStopConditionCount,
          ...(worktree ? { worktree } : {}),
        }),
      };
    });
    return {
      project: {
        id: project.id,
        name: `${external.repository.nameWithOwner} · M${project.milestone.number}`,
        subtitle: external.milestone?.title ?? `GitHub Milestone #${project.milestone.number}`,
      },
      source: external.source,
      repository: external.repository,
      milestone: external.milestone,
      integration: external.integration,
      worktrees: external.worktrees,
      issues,
      events: projectState.events,
    };
  }

  return http.createServer(async (request, response) => {
    const url = new URL(request.url ?? "/", `http://${request.headers.host ?? "127.0.0.1"}`);
    const project = getProject(url.searchParams.get("project"));
    if (!project) {
      sendJson(response, 500, { error: "no_project_configured" });
      return;
    }
    try {
      if (request.method === "GET" && url.pathname === "/api/projects") {
        sendJson(response, 200, { projects: projects.map(projectView) });
        return;
      }
      if (request.method === "GET" && url.pathname === "/api/status") {
        sendJson(response, 200, await buildStatus(project));
        return;
      }
      const collaborationMatch = url.pathname.match(/^\/api\/issues\/(\d+)\/collaboration$/);
      if (request.method === "GET" && collaborationMatch) {
        const issueNumber = Number(collaborationMatch[1]);
        const cached = cache.get(project.id);
        const issueExists = cached
          ? cached.githubIssues.some((issue) => issue.number === issueNumber)
          : (await buildStatus(project)).issues.some((issue) => issue.number === issueNumber);
        if (!issueExists) {
          sendJson(response, 404, { error: "issue_not_configured" });
          return;
        }
        sendJson(response, 200, await store.getCollaboration(project, issueNumber));
        return;
      }
      const createConversationMatch = url.pathname.match(/^\/api\/issues\/(\d+)\/conversations$/);
      if (request.method === "POST" && createConversationMatch) {
        const issueNumber = Number(createConversationMatch[1]);
        const status = await buildStatus(project);
        const issue = status.issues.find((item) => item.number === issueNumber);
        if (!issue) {
          sendJson(response, 404, { error: "issue_not_configured" });
          return;
        }
        const input = createConversationSchema.parse(await readBody(request));
        const dependencies = issue.github
          ? dependencyState(issue.github)
          : { ready: false, blockers: [] };
        if (input.role !== "DISCUSSION" && !dependencies.ready) {
          sendJson(response, 409, {
            error: "dependency_blocked",
            message: `前置 Issue ${dependencies.blockers.map((number) => `#${number}`).join("、")} 尚未关闭，不能开始交付；Discussion 仍然可用。`,
          });
          return;
        }
        if (input.role !== "DISCUSSION" && issue.attention.required) {
          sendJson(response, 409, {
            error: "delivery_paused",
            message:
              "Resolve the open Stop Conditions in Discussion before starting another delivery conversation.",
          });
          return;
        }
        const collaboration = await store.getCollaboration(project, issueNumber);
        const brief = collaboration.briefs[0];
        const latestImplementation = collaboration.conversations
          .filter((item) => ["IMPLEMENTATION", "REPAIR"].includes(item.role))
          .flatMap((item) => item.messages)
          .filter((message) => message.role === "AGENT")
          .at(-1)?.text;
        let initialContext: string;
        try {
          initialContext = buildAgentInputPackage({
            role: input.role,
            issueNumber,
            issueTitle: issue.github?.title ?? issue.label,
            issueBody: issue.github?.body ?? "",
            ...(brief ? { brief } : {}),
            ...(collaboration.workspace ? { workspace: collaboration.workspace } : {}),
            ...(issue.control.candidateSha ? { candidateSha: issue.control.candidateSha } : {}),
            ...(latestImplementation ? { priorOutput: latestImplementation } : {}),
          }).markdown;
        } catch (error: unknown) {
          sendJson(response, 409, { error: "handoff_not_ready", message: errorMessage(error) });
          return;
        }
        const conversation = await store.createConversation(project, issueNumber, {
          ...input,
          initialContext,
        });
        sendJson(response, 201, conversation);
        return;
      }
      const messageMatch = url.pathname.match(/^\/api\/conversations\/([^/]+)\/messages$/);
      const developmentBriefMatch = url.pathname.match(
        /^\/api\/conversations\/([^/]+)\/development-brief$/,
      );
      if (request.method === "POST" && (messageMatch || developmentBriefMatch)) {
        const generatingDevelopmentBrief = Boolean(developmentBriefMatch);
        const conversationId = decodeURIComponent(
          (messageMatch ?? developmentBriefMatch)?.[1] ?? "",
        );
        const conversation = await store.findConversation(project, conversationId);
        if (!conversation) {
          sendJson(response, 404, { error: "conversation_not_found" });
          return;
        }
        if (generatingDevelopmentBrief && conversation.role !== "DISCUSSION") {
          sendJson(response, 409, {
            error: "development_brief_requires_discussion",
            message: "开发说明只能从 Discuss 对话生成。",
          });
          return;
        }
        if (generatingDevelopmentBrief || conversation.role !== "DISCUSSION") {
          const status = await buildStatus(project);
          const issue = status.issues.find((item) => item.number === conversation.issueNumber);
          const dependencies = issue?.github
            ? dependencyState(issue.github)
            : { ready: false, blockers: [] };
          if (!issue || !dependencies.ready) {
            sendJson(response, 409, {
              error: "dependency_blocked",
              message: `前置 Issue ${dependencies.blockers.map((number) => `#${number}`).join("、") || "状态未知"} 尚未关闭，不能启动 Agent。`,
            });
            return;
          }
        }
        const collaboration = await store.getCollaboration(project, conversation.issueNumber);
        if (
          conversation.role !== "DISCUSSION" &&
          collaboration.stopConditions.some((item) => item.status === "OPEN")
        ) {
          sendJson(response, 409, {
            error: "delivery_paused",
            message:
              "This delivery conversation is paused by an open Stop Condition. Continue in Discussion first.",
          });
          return;
        }
        const message = generatingDevelopmentBrief
          ? DEVELOPMENT_BRIEF_PROMPT
          : sendConversationMessageSchema.parse(await readBody(request)).text;
        const turn = await store.beginConversationTurn(
          project,
          conversationId,
          message,
          generatingDevelopmentBrief ? "DEVELOPMENT_BRIEF" : "MESSAGE",
        );
        if (conversation.role === "IMPLEMENTATION" || conversation.role === "REPAIR") {
          await store.updateIssue(project, conversation.issueNumber, {
            phase: "IMPLEMENTING",
            note: `${conversation.engine} ${conversation.role} run started`,
          });
        } else if (conversation.role === "REVIEW") {
          await store.updateIssue(project, conversation.issueNumber, {
            phase: "REVIEWING",
            note: `${conversation.engine} independent Review started`,
          });
        }
        const cwd =
          conversation.role === "DISCUSSION"
            ? project.integration.worktree
            : (collaboration.workspace?.path ?? project.integration.worktree);
        const prompt = conversation.sessionId
          ? message
          : `${conversation.initialContext}\n\n# Human message\n\n${message}`;
        void agentRunner
          .run({
            runId: turn.id,
            engine: conversation.engine,
            role: conversation.role,
            cwd,
            prompt,
            sessionId: conversation.sessionId,
            onProgress: async (progress) => {
              await store.updateConversationTurnProgress(project, turn.id, progress);
            },
          })
          .then(async (result) => {
            await store.completeConversationTurn(project, conversationId, turn.id, result);
          })
          .catch(async (error: unknown) => {
            await store.failConversationTurn(project, conversationId, turn.id, redactError(error));
          });
        sendJson(response, 202, turn);
        return;
      }
      const cancelTurnMatch = url.pathname.match(/^\/api\/turns\/([^/]+)\/cancel$/);
      if (request.method === "POST" && cancelTurnMatch) {
        const turnId = decodeURIComponent(cancelTurnMatch[1] ?? "");
        const turn = await store.findTurn(project, turnId);
        if (!turn) {
          sendJson(response, 404, { error: "turn_not_found" });
          return;
        }
        if (turn.status !== "RUNNING") {
          sendJson(response, 409, {
            error: "turn_not_running",
            message: `Agent turn is already ${turn.status}`,
          });
          return;
        }
        const reason = "用户停止了本次 Agent 运行，可以在同一对话中重新发送。";
        const conversation = await store.cancelConversationTurn(
          project,
          turn.conversationId,
          turn.id,
          reason,
        );
        const processStopped = agentRunner.cancel?.(turn.id) ?? false;
        sendJson(response, 200, { turnId, processStopped, conversation });
        return;
      }
      const briefMatch = url.pathname.match(/^\/api\/conversations\/([^/]+)\/briefs$/);
      if (request.method === "POST" && briefMatch) {
        const conversationId = decodeURIComponent(briefMatch[1] ?? "");
        const conversation = await store.findConversation(project, conversationId);
        if (!conversation) {
          sendJson(response, 404, { error: "conversation_not_found" });
          return;
        }
        const status = await buildStatus(project);
        const issue = status.issues.find((item) => item.number === conversation.issueNumber);
        const dependencies = issue?.github
          ? dependencyState(issue.github)
          : { ready: false, blockers: [] };
        if (!issue || !dependencies.ready) {
          sendJson(response, 409, {
            error: "dependency_blocked",
            message: `前置 Issue ${dependencies.blockers.map((number) => `#${number}`).join("、") || "状态未知"} 尚未关闭，不能确认开发说明。`,
          });
          return;
        }
        const { content } = freezeBriefSchema.parse(await readBody(request));
        sendJson(response, 201, await store.freezeBrief(project, conversationId, content));
        return;
      }
      const workspaceMatch = url.pathname.match(/^\/api\/issues\/(\d+)\/workspace$/);
      if (request.method === "POST" && workspaceMatch) {
        const issueNumber = Number(workspaceMatch[1]);
        const status = await buildStatus(project);
        const issue = status.issues.find((item) => item.number === issueNumber);
        if (!issue) {
          sendJson(response, 404, { error: "issue_not_configured" });
          return;
        }
        if (issue.attention.required) {
          sendJson(response, 409, {
            error: "delivery_paused",
            message: "Resolve the open Stop Conditions in Discussion before creating a worktree.",
          });
          return;
        }
        const collaboration = await store.getCollaboration(project, issueNumber);
        if (!collaboration.briefs[0]) {
          sendJson(response, 409, {
            error: "brief_required",
            message: "Freeze a task brief before creating the Issue workspace.",
          });
          return;
        }
        if (collaboration.workspace) {
          sendJson(response, 409, {
            error: "workspace_exists",
            message: "This Issue already has a Qianshou workspace binding.",
          });
          return;
        }
        if (issue.state.key === "WAITING_DEPENDENCY") {
          sendJson(response, 409, { error: "dependency_blocked", message: issue.state.label });
          return;
        }
        const workspace = await worktreeCreator(project, issueNumber);
        await store.attachWorkspace(project, workspace);
        await store.updateIssue(project, issueNumber, {
          phase: "WORKTREE_READY",
          note: `${workspace.branch} at ${workspace.path}`,
        });
        sendJson(response, 201, workspace);
        return;
      }
      const createStopConditionMatch = url.pathname.match(
        /^\/api\/issues\/(\d+)\/stop-conditions$/,
      );
      if (request.method === "POST" && createStopConditionMatch) {
        const issueNumber = Number(createStopConditionMatch[1]);
        const status = await buildStatus(project);
        if (!status.issues.some((issue) => issue.number === issueNumber)) {
          sendJson(response, 404, { error: "issue_not_configured" });
          return;
        }
        const input = createStopConditionSchema.parse(await readBody(request));
        sendJson(response, 201, await store.createStopCondition(project, issueNumber, input));
        return;
      }
      const resolveStopConditionMatch = url.pathname.match(
        /^\/api\/stop-conditions\/([^/]+)\/resolve$/,
      );
      if (request.method === "POST" && resolveStopConditionMatch) {
        const stopConditionId = decodeURIComponent(resolveStopConditionMatch[1] ?? "");
        const stopCondition = await store.findStopCondition(project, stopConditionId);
        if (!stopCondition) {
          sendJson(response, 404, { error: "stop_condition_not_found" });
          return;
        }
        const input = resolveStopConditionSchema.parse(await readBody(request));
        sendJson(response, 200, await store.resolveStopCondition(project, stopConditionId, input));
        return;
      }
      const controlMatch = url.pathname.match(/^\/api\/issues\/(\d+)\/control$/);
      if (request.method === "POST" && controlMatch) {
        const issueNumber = Number(controlMatch[1]);
        const status = await buildStatus(project);
        const issue = status.issues.find((item) => item.number === issueNumber);
        if (!issue) {
          sendJson(response, 404, { error: "issue_not_configured" });
          return;
        }
        const dependencies = issue.github
          ? dependencyState(issue.github)
          : { ready: false, blockers: [] };
        if (!dependencies.ready) {
          sendJson(response, 409, {
            error: "dependency_blocked",
            message: `前置 Issue ${dependencies.blockers.map((number) => `#${number}`).join("、") || "状态未知"} 尚未关闭，不能修改交付状态。`,
          });
          return;
        }
        const patch = controlPatchSchema.parse(await readBody(request));
        sendJson(response, 200, await store.updateIssue(project, issueNumber, patch));
        return;
      }
      const contractMatch = url.pathname.match(/^\/api\/issues\/(\d+)\/contract$/);
      if (request.method === "GET" && contractMatch) {
        const status = await buildStatus(project);
        const issue = status.issues.find((item) => item.number === Number(contractMatch[1]));
        if (!issue) {
          sendJson(response, 404, { error: "issue_not_configured" });
          return;
        }
        const role = url.searchParams.get("role") === "reviewer" ? "reviewer" : "implementer";
        if (role === "implementer" && issue.slots.developerStatus === "LOCKED") {
          sendJson(response, 409, {
            error: "implementer_locked",
            message:
              "Close the GitHub Blocked by Issues before creating a development conversation.",
          });
          return;
        }
        if (role === "reviewer" && issue.slots.reviewerStatus === "LOCKED") {
          sendJson(response, 409, {
            error: "reviewer_locked",
            message: "Freeze a candidate SHA before creating a Review conversation.",
          });
          return;
        }
        const worktree = issue.worktrees[0];
        sendJson(response, 200, {
          role,
          contract: buildTaskContract({
            role,
            project,
            projectIssue: issue,
            githubIssue: issue.github,
            control: issue.control,
            ...(worktree ? { worktree } : {}),
          }),
        });
        return;
      }
      if (request.method === "GET" && url.pathname === "/health") {
        sendJson(response, 200, { ok: true, mode: "manual", projects: projects.length });
        return;
      }
      if (request.method === "GET") {
        const relativePath = url.pathname === "/" ? "index.html" : url.pathname.replace(/^\//, "");
        if (!/^[A-Za-z0-9._/-]+$/.test(relativePath) || relativePath.includes("..")) {
          sendJson(response, 404, { error: "not_found" });
          return;
        }
        try {
          const contents = await readFile(join(webDistribution, relativePath));
          response.writeHead(200, {
            "Content-Type": contentTypes[extname(relativePath)] ?? "application/octet-stream",
            "Cache-Control": "no-store",
            "Content-Security-Policy":
              "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; font-src 'self'",
          });
          response.end(contents);
          return;
        } catch (error: unknown) {
          if (!(error instanceof Error) || !("code" in error) || error.code !== "ENOENT")
            throw error;
        }
      }
      sendJson(response, 404, { error: "not_found" });
    } catch (error: unknown) {
      const message = errorMessage(error);
      const badRequest =
        error instanceof SyntaxError ||
        /invalid|unsupported|body too large|supported control|candidateSha|Invalid input|too_small|too_big|invalid_type/.test(
          message,
        );
      sendJson(response, badRequest ? 400 : 502, {
        error: badRequest ? "invalid_request" : "request_failed",
        message: redactError(error),
        at: new Date().toISOString(),
      });
    }
  });
}

const isEntrypoint = process.argv[1]
  ? fileURLToPath(import.meta.url) === resolve(process.argv[1])
  : false;
if (isEntrypoint) {
  const host = "127.0.0.1";
  const port = Number(process.env.QIANSHOU_PORT ?? 41727);
  const server = await createQianshouServer();
  server.listen(port, host, () => process.stdout.write(`Qianshou API: http://${host}:${port}\n`));
}
