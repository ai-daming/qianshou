import { z } from "zod";

export const phaseSchema = z.enum([
  "PLANNED",
  "WORKTREE_READY",
  "IMPLEMENTING",
  "CANDIDATE_READY",
  "REVIEWING",
  "CHANGES_REQUESTED",
  "APPROVED",
  "MERGED_TO_MILESTONE",
  "VERIFIED",
  "BLOCKED",
  "NEEDS_HUMAN",
]);

export const engineSchema = z.enum(["codex", "claude"]);

export const conversationRoleSchema = z.enum([
  "DISCUSSION",
  "IMPLEMENTATION",
  "REVIEW",
  "REPAIR",
  "INTEGRATION",
]);

export type Phase = z.infer<typeof phaseSchema>;
export type Engine = z.infer<typeof engineSchema>;
export type ConversationRole = z.infer<typeof conversationRoleSchema>;
export type Tone = "neutral" | "active" | "success" | "warning" | "danger";
export type SlotStatus =
  | "LOCKED"
  | "PAUSED"
  | "READY"
  | "ACTIVE"
  | "DONE"
  | "APPROVED"
  | "CHANGES_REQUESTED";

export const createConversationSchema = z.object({
  role: conversationRoleSchema,
  engine: engineSchema,
  title: z.string().trim().min(1).max(120).optional(),
});

export const sendConversationMessageSchema = z.object({
  text: z.string().trim().min(1).max(100_000),
});

export const freezeBriefSchema = z.object({
  content: z.string().trim().min(1).max(200_000),
});

export const stopConditionCategorySchema = z.enum([
  "BUSINESS_AMBIGUITY",
  "SCOPE_CHANGE",
  "TECHNICAL_CONTRADICTION",
  "ENVIRONMENT",
  "REVIEW_FINDING",
  "DELIVERY_CONFLICT",
  "OTHER",
]);

export const stopConditionOutcomeSchema = z.enum([
  "CONTINUE",
  "REVISE_BRIEF",
  "REPAIR",
  "RE_REVIEW",
  "SPLIT_ISSUE",
  "SUPERSEDE",
  "CANCEL",
]);

export const createStopConditionSchema = z.object({
  category: stopConditionCategorySchema,
  summary: z.string().trim().min(1).max(240),
  detail: z.string().trim().max(10_000).default(""),
  originConversationId: z.string().trim().min(1).optional(),
});

export const resolveStopConditionSchema = z.object({
  resolution: z.string().trim().min(1).max(10_000),
  outcome: stopConditionOutcomeSchema,
});

export type CreateConversation = z.infer<typeof createConversationSchema>;
export type CreateStopCondition = z.infer<typeof createStopConditionSchema>;
export type ResolveStopCondition = z.infer<typeof resolveStopConditionSchema>;
export type StopConditionCategory = z.infer<typeof stopConditionCategorySchema>;
export type StopConditionOutcome = z.infer<typeof stopConditionOutcomeSchema>;

export type ConversationStatus = "READY" | "RUNNING" | "ACTIVE" | "FAILED" | "ARCHIVED";
export type AgentTurnStatus = "QUEUED" | "RUNNING" | "COMPLETED" | "FAILED" | "CANCELLED";
export type AgentTurnIntent = "MESSAGE" | "DEVELOPMENT_BRIEF";
export type AgentRunEventKind =
  | "SYSTEM"
  | "THOUGHT"
  | "TOOL"
  | "TOOL_RESULT"
  | "MESSAGE"
  | "ERROR"
  | "RESULT";

export interface AgentRunEvent {
  sequence: number;
  kind: AgentRunEventKind;
  title: string;
  detail: string | null;
  at: string;
}

export interface ConversationMessage {
  id: string;
  role: "USER" | "AGENT" | "SYSTEM";
  text: string;
  at: string;
  turnId: string | null;
}

export interface AgentConversation {
  id: string;
  issueNumber: number;
  role: ConversationRole;
  engine: Engine;
  title: string;
  status: ConversationStatus;
  sessionId: string | null;
  initialContext: string;
  messages: ConversationMessage[];
  createdAt: string;
  updatedAt: string;
}

export interface AgentTurn {
  id: string;
  conversationId: string;
  issueNumber: number;
  role: ConversationRole;
  engine: Engine;
  intent: AgentTurnIntent;
  status: AgentTurnStatus;
  prompt: string;
  error: string | null;
  progress: {
    eventCount: number;
    summary: string;
    updatedAt: string;
    events: AgentRunEvent[];
  } | null;
  startedAt: string;
  completedAt: string | null;
}

export interface TaskBriefSnapshot {
  id: string;
  issueNumber: number;
  sourceConversationId: string;
  version: number;
  content: string;
  frozenAt: string;
}

export interface IssueWorkspaceBinding {
  issueNumber: number;
  branch: string;
  path: string;
  baseBranch: string;
  baseSha: string;
  createdAt: string;
}

export interface IssueStopCondition {
  id: string;
  issueNumber: number;
  category: StopConditionCategory;
  summary: string;
  detail: string;
  originPhase: Phase;
  originConversationId: string | null;
  candidateSha: string | null;
  status: "OPEN" | "RESOLVED";
  resolution: string | null;
  outcome: StopConditionOutcome | null;
  createdAt: string;
  resolvedAt: string | null;
}

export interface CollaborationView {
  issueNumber: number;
  conversations: AgentConversation[];
  turns: AgentTurn[];
  briefs: TaskBriefSnapshot[];
  workspace: IssueWorkspaceBinding | null;
  stopConditions: IssueStopCondition[];
}

export const PHASE_LABELS: Record<Phase, string> = {
  PLANNED: "待准备",
  WORKTREE_READY: "Worktree 就绪",
  IMPLEMENTING: "开发中",
  CANDIDATE_READY: "候选已冻结",
  REVIEWING: "独立 Review",
  CHANGES_REQUESTED: "需要返修",
  APPROVED: "Review 通过",
  MERGED_TO_MILESTONE: "已合入 M7",
  VERIFIED: "已验证",
  BLOCKED: "阻塞",
  NEEDS_HUMAN: "等待人工判断",
};

export type IssueKind = "control" | "delivery";

export interface IssueDescriptor {
  number: number;
  label: string;
  kind: IssueKind;
}

export interface ProjectConfig {
  id: string;
  repository: { slug: string; path: string };
  milestone: { number: number };
  integration: { branch: string; worktree: string; baseBranch: string };
  refreshSeconds: number;
  defaults: { developerEngine: Engine; reviewerEngine: Engine };
}

export const projectConfigSchema: z.ZodType<ProjectConfig> = z
  .object({
    id: z.string().min(1),
    repository: z.object({ slug: z.string().min(3), path: z.string().startsWith("/") }).strict(),
    milestone: z.object({ number: z.number().int().positive() }).strict(),
    integration: z
      .object({
        branch: z.string().min(1),
        worktree: z.string().startsWith("/"),
        baseBranch: z.string().min(1),
      })
      .strict(),
    refreshSeconds: z.number().int().positive(),
    defaults: z.object({ developerEngine: engineSchema, reviewerEngine: engineSchema }).strict(),
  })
  .strict();

export const projectsConfigSchema = z.object({ projects: z.array(projectConfigSchema).min(1) });

export interface IssueControl {
  issueNumber: number;
  phase: Phase;
  candidateSha: string | null;
  developerEngine: Engine;
  reviewerEngine: Engine;
  note: string;
  updatedAt: string;
}

export const controlPatchSchema = z
  .object({
    phase: phaseSchema.optional(),
    candidateSha: z
      .union([
        z.literal(""),
        z.string().regex(/^[0-9a-f]{7,40}$/i, "candidateSha must be a 7-40 character Git SHA"),
        z.null(),
      ])
      .transform((value) => value || null)
      .optional(),
    developerEngine: engineSchema.optional(),
    reviewerEngine: engineSchema.optional(),
    note: z.string().max(2000).optional(),
  })
  .refine(
    (value) => Object.values(value).some((item) => item !== undefined),
    "no supported control fields",
  );

export type ControlPatch = z.infer<typeof controlPatchSchema>;

export interface GithubIssue {
  number: number;
  title: string;
  body: string;
  state: string;
  url: string;
  updatedAt: string;
  milestone: { title: string } | null;
  labels: Array<{ name: string; color: string }>;
  assignees: Array<{ login: string }>;
  blockedBy: Array<{ number: number; state: string }>;
}

export interface PullRequest {
  number: number;
  title: string;
  body?: string;
  state: string;
  url: string;
  baseRefName: string | null;
  headRefName: string | null;
  updatedAt: string;
  isDraft: boolean;
}

export interface Worktree {
  path: string;
  sha: string | null;
  branch: string | null;
  detached?: boolean;
}

export interface IssueState {
  key: string;
  label: string;
  tone: Tone;
  blockers: number[];
}

export interface ControlEvent {
  id: string;
  issueNumber: number;
  type: "CONTROL_UPDATED";
  at: string;
  before: { phase: Phase; candidateSha: string | null };
  after: { phase: Phase; candidateSha: string | null };
  note: string;
}

export interface IssueView extends IssueDescriptor {
  github: GithubIssue | null;
  pulls: PullRequest[];
  worktrees: Worktree[];
  control: IssueControl;
  attention: { required: boolean; openStopConditionCount: number };
  state: IssueState;
  slots: { developerStatus: SlotStatus; reviewerStatus: SlotStatus };
  nextAction: {
    label: string;
    detail: string;
    command: string[] | null;
    shellCommand: string | null;
  };
}

export interface StatusSnapshot {
  project: { id: string; name: string; subtitle: string };
  source: {
    github: string;
    git: string;
    collectedAt: string;
    stale: boolean;
    warning: string | null;
  };
  repository: { nameWithOwner: string; url: string; defaultBranch: string };
  milestone: {
    number: number;
    title: string;
    state: string;
    open_issues: number;
    closed_issues: number;
  } | null;
  integration: ProjectConfig["integration"] & {
    sha: string | null;
    dirty: string[];
    statusLine: string;
    aheadMain: number;
    behindMain: number;
    hasRemoteBranch: boolean;
  };
  worktrees: Worktree[];
  issues: IssueView[];
  events: ControlEvent[];
}

export function initialIssueControl(
  project: ProjectConfig,
  issueNumber: number,
  now = new Date().toISOString(),
): IssueControl {
  return {
    issueNumber,
    phase: "PLANNED",
    candidateSha: null,
    developerEngine: project.defaults.developerEngine,
    reviewerEngine: project.defaults.reviewerEngine,
    note: "",
    updatedAt: now,
  };
}

export function dependencyState(githubIssue: Pick<GithubIssue, "blockedBy">) {
  const blockers = githubIssue.blockedBy
    .filter((issue) => issue.state !== "CLOSED")
    .map((issue) => issue.number);
  return { ready: blockers.length === 0, blockers };
}

export function issueKindFromGithub(githubIssue: Pick<GithubIssue, "labels">): IssueKind {
  return githubIssue.labels.some((label) => label.name.toLowerCase() === "type:milestone-control")
    ? "control"
    : "delivery";
}

export function deriveSlots(
  control: IssueControl,
  dependenciesReady = true,
  deliveryPaused = false,
): IssueView["slots"] {
  if (deliveryPaused) return { developerStatus: "PAUSED", reviewerStatus: "PAUSED" };
  if (!dependenciesReady) return { developerStatus: "LOCKED", reviewerStatus: "LOCKED" };
  const candidateReady =
    Boolean(control.candidateSha) &&
    [
      "CANDIDATE_READY",
      "REVIEWING",
      "CHANGES_REQUESTED",
      "APPROVED",
      "MERGED_TO_MILESTONE",
      "VERIFIED",
    ].includes(control.phase);
  const developerStatus: SlotStatus = ["IMPLEMENTING", "CHANGES_REQUESTED"].includes(control.phase)
    ? "ACTIVE"
    : ["CANDIDATE_READY", "REVIEWING", "APPROVED", "MERGED_TO_MILESTONE", "VERIFIED"].includes(
          control.phase,
        )
      ? "DONE"
      : "READY";
  const reviewerStatus: SlotStatus = !candidateReady
    ? "LOCKED"
    : control.phase === "REVIEWING"
      ? "ACTIVE"
      : ["APPROVED", "MERGED_TO_MILESTONE", "VERIFIED"].includes(control.phase)
        ? "APPROVED"
        : control.phase === "CHANGES_REQUESTED"
          ? "CHANGES_REQUESTED"
          : "READY";
  return { developerStatus, reviewerStatus };
}

function phaseTone(phase: Phase): Tone {
  if (["APPROVED", "MERGED_TO_MILESTONE", "VERIFIED"].includes(phase)) return "success";
  if (phase === "CHANGES_REQUESTED") return "warning";
  return "active";
}

export function deriveIssueState(input: {
  projectIssue: IssueDescriptor;
  githubIssue: GithubIssue | null;
  control: IssueControl;
  openStopConditionCount?: number;
}): IssueState {
  const { projectIssue, githubIssue, control, openStopConditionCount = 0 } = input;
  if (!githubIssue) return { key: "MISSING", label: "GitHub 未找到", tone: "danger", blockers: [] };
  if (projectIssue.kind === "control")
    return { key: "CONTROL", label: "总控", tone: "neutral", blockers: [] };
  if (openStopConditionCount > 0)
    return { key: "NEEDS_DISCUSSION", label: "需要讨论", tone: "danger", blockers: [] };
  if (control.phase === "BLOCKED")
    return { key: "BLOCKED", label: PHASE_LABELS.BLOCKED, tone: "warning", blockers: [] };
  if (control.phase === "NEEDS_HUMAN")
    return { key: "NEEDS_HUMAN", label: PHASE_LABELS.NEEDS_HUMAN, tone: "danger", blockers: [] };
  if (control.phase !== "PLANNED")
    return {
      key: control.phase,
      label: PHASE_LABELS[control.phase],
      tone: phaseTone(control.phase),
      blockers: [],
    };
  if (githubIssue.state === "CLOSED")
    return { key: "CLOSED", label: "GitHub 已关闭", tone: "success", blockers: [] };
  const dependencies = dependencyState(githubIssue);
  if (!dependencies.ready)
    return {
      key: "WAITING_DEPENDENCY",
      label: `等待 ${dependencies.blockers.map((number) => `#${number}`).join("、")}`,
      tone: "warning",
      blockers: dependencies.blockers,
    };
  return { key: "READY", label: "可准备", tone: "success", blockers: [] };
}

export function nextAction(input: {
  project: ProjectConfig;
  projectIssue: IssueDescriptor;
  githubIssue: GithubIssue | null;
  control: IssueControl;
  worktree?: Worktree;
  openStopConditionCount?: number;
}): IssueView["nextAction"] {
  const {
    project,
    projectIssue,
    githubIssue,
    control,
    worktree,
    openStopConditionCount = 0,
  } = input;
  const issueRef = `#${projectIssue.number}`;
  if (!githubIssue)
    return {
      label: "刷新 GitHub Milestone",
      detail: `${issueRef} 不在本次 GitHub Milestone 快照中`,
      command: null,
      shellCommand: null,
    };
  if (openStopConditionCount > 0)
    return {
      label: "回到 Discussion",
      detail: `${openStopConditionCount} 个 Stop Condition 尚未解决；交付阶段 ${PHASE_LABELS[control.phase]} 和现有证据保持不变`,
      command: null,
      shellCommand: null,
    };
  switch (control.phase) {
    case "PLANNED": {
      const dependencies = dependencyState(githubIssue);
      if (!dependencies.ready)
        return {
          label: "等待前置 Issue",
          detail: `${dependencies.blockers.map((number) => `#${number}`).join("、")} 尚未关闭，不能创建本工作包`,
          command: null,
          shellCommand: null,
        };
      return {
        label: "准备 Issue worktree",
        detail: `使用 Git 从最新 ${project.integration.branch} 创建`,
        command: null,
        shellCommand: null,
      };
    }
    case "WORKTREE_READY":
      return {
        label: "启动开发会话",
        detail: `把冻结任务简报交给 ${worktree?.path ?? "Issue worktree"} 中的新开发会话`,
        command: null,
        shellCommand: null,
      };
    case "IMPLEMENTING":
      return {
        label: "等待候选提交",
        detail: "开发者必须返回测试证据和 candidate SHA",
        command: null,
        shellCommand: null,
      };
    case "CANDIDATE_READY":
      return {
        label: "派发独立 Review",
        detail: `Reviewer 只能审 ${control.candidateSha ?? "尚未填写的 candidate SHA"}`,
        command: null,
        shellCommand: null,
      };
    case "REVIEWING":
      return {
        label: "等待 Review verdict",
        detail: "Reviewer 不得直接修改候选代码",
        command: null,
        shellCommand: null,
      };
    case "CHANGES_REQUESTED":
      return {
        label: "返给原开发者修复",
        detail: "修复后必须生成新的 candidate SHA",
        command: null,
        shellCommand: null,
      };
    case "APPROVED":
      return {
        label: "执行集成门禁",
        detail: `确认 Review、测试和 ${control.candidateSha ?? "candidate"} 后再请求人工合并`,
        command: null,
        shellCommand: null,
      };
    case "MERGED_TO_MILESTONE":
      return {
        label: "验证集成分支",
        detail: "确认合入 SHA、回归测试和后继 Issue 依赖",
        command: null,
        shellCommand: null,
      };
    case "VERIFIED":
      return {
        label: "工作包已完成",
        detail: "选择下一个依赖已满足的 Issue",
        command: null,
        shellCommand: null,
      };
    default:
      return {
        label: "等待人工处理",
        detail: control.note || PHASE_LABELS[control.phase],
        command: null,
        shellCommand: null,
      };
  }
}

export function shellCommand(args: string[] | null): string | null {
  if (!args) return null;
  return args
    .map((value) =>
      /^[A-Za-z0-9_./:@-]+$/.test(value) ? value : `'${value.replaceAll("'", "'\\''")}'`,
    )
    .join(" ");
}

export function buildAgentInputPackage(input: {
  role: ConversationRole;
  issueNumber: number;
  issueTitle: string;
  issueBody: string;
  brief?: Pick<TaskBriefSnapshot, "id" | "version" | "content">;
  workspace?: Pick<IssueWorkspaceBinding, "branch" | "path" | "baseSha">;
  candidateSha?: string;
  priorOutput?: string;
}): { role: ConversationRole; title: string; markdown: string } {
  const { role } = input;
  if (role !== "DISCUSSION" && !input.brief)
    throw new Error(`${role} requires a frozen task brief`);
  if (["IMPLEMENTATION", "REVIEW", "REPAIR", "INTEGRATION"].includes(role) && !input.workspace) {
    throw new Error(`${role} requires an Issue worktree`);
  }
  if (["REVIEW", "REPAIR", "INTEGRATION"].includes(role) && !input.candidateSha) {
    throw new Error(`${role} requires a frozen candidate SHA`);
  }
  const roleInstruction: Record<ConversationRole, string> = {
    DISCUSSION:
      "Help the human clarify the problem, decisions, acceptance criteria, non-goals, constraints, and unresolved questions. Do not edit files.",
    IMPLEMENTATION:
      "Implement the frozen task brief in the assigned worktree. Use repository instructions and TDD. Do not merge, push, deploy, or broaden scope.",
    REVIEW:
      "Independently review only the frozen candidate SHA. Do not edit or repair code. Return findings with evidence and a verdict.",
    REPAIR:
      "Repair accepted Review findings in the original worktree, rerun tests, and produce a new candidate for independent re-review.",
    INTEGRATION:
      "Verify the approved candidate and integration evidence. Do not merge or push without explicit human authority.",
  };
  const lines = [
    `# Qianshou ${role} work package`,
    "",
    `Issue: #${input.issueNumber} ${input.issueTitle}`,
    "",
    input.issueBody,
    "",
    roleInstruction[role],
  ];
  if (input.brief)
    lines.push(
      "",
      `Task brief: ${input.brief.id} v${input.brief.version}`,
      "",
      input.brief.content,
    );
  if (input.workspace)
    lines.push(
      "",
      `Base SHA: ${input.workspace.baseSha}`,
      `Branch: ${input.workspace.branch}`,
      `Worktree: ${input.workspace.path}`,
    );
  if (input.candidateSha) lines.push(`Candidate SHA: ${input.candidateSha}`);
  if (input.priorOutput) lines.push("", "Previous handoff output:", input.priorOutput);
  lines.push(
    "",
    "Treat Git, tests, and repository state as evidence. Do not treat another Agent completion claim as proof.",
  );
  return { role, title: `#${input.issueNumber} ${role}`, markdown: lines.join("\n") };
}

export function buildTaskContract(input: {
  role: "implementer" | "reviewer";
  project: ProjectConfig;
  projectIssue: IssueDescriptor;
  githubIssue: GithubIssue | null;
  control: IssueControl;
  worktree?: Worktree;
}): string {
  const { role, project, projectIssue, githubIssue, control, worktree } = input;
  const common = [
    `Project: ${project.repository.slug} · M${project.milestone.number}`,
    `Issue: #${projectIssue.number} ${githubIssue?.title ?? projectIssue.label}`,
    `Integration branch: ${project.integration.branch}`,
    `Worktree: ${worktree?.path ?? "not discovered"}`,
    `Candidate SHA: ${control.candidateSha ?? "not frozen"}`,
    "",
    "Read the repository instructions and the complete GitHub Issue before acting.",
    "Report external facts with commands and SHAs; do not substitute a completion claim for evidence.",
  ];
  if (role === "reviewer")
    return [
      "# Qianshou Candidate Reviewer Contract",
      "",
      ...common,
      "",
      "Review only the frozen candidate SHA. Do not edit files, commit, push, merge, or broaden scope.",
      "Check requirement coverage, regressions, tests, security, state semantics, and scope drift.",
      "Return blocking and non-blocking findings with exact file/line evidence, then give a verdict.",
    ].join("\n");
  return [
    "# Qianshou Issue Implementer Contract",
    "",
    ...common,
    "",
    "Use TDD and the repository-required real boundaries. Preserve unrelated user changes.",
    "Do not merge to the integration branch. Stop after producing a clean candidate commit.",
    "Return changed files, test evidence, candidate SHA, known gaps, and the exact review handoff.",
  ].join("\n");
}
