import {
  dependencyState,
  PHASE_LABELS,
  type AgentConversation,
  type CollaborationView,
  type ConversationRole,
  type Engine,
  type IssueStopCondition,
  type IssueView,
  type Phase,
  type StatusSnapshot,
} from "@qianshou/core";
import "../../../public/styles.css";
import { renderMarkdown } from "./markdown.js";

let snapshot: StatusSnapshot | null = null;
let collaboration: CollaborationView | null = null;
let selectedIssueNumber = 224;
let activeConversationId: string | null = null;
let activeStopConditionId: string | null = null;
let activeWorkbenchView: "discussion" | "delivery" = "discussion";
let pendingConversationRole: ConversationRole = "DISCUSSION";
let loading = false;
let collaborationLoading = false;
let toastTimer: ReturnType<typeof setTimeout> | null = null;
const briefDrafts = new Map<number, string>();
const appliedDevelopmentBriefTurnIds = new Map<number, string>();

const roleLabels: Record<ConversationRole, string> = {
  DISCUSSION: "讨论",
  IMPLEMENTATION: "开发",
  REVIEW: "Review",
  REPAIR: "返修",
  INTEGRATION: "集成检查",
};

const stopCategoryLabels: Record<IssueStopCondition["category"], string> = {
  BUSINESS_AMBIGUITY: "业务定义不清",
  SCOPE_CHANGE: "范围变化",
  TECHNICAL_CONTRADICTION: "技术约束冲突",
  ENVIRONMENT: "环境问题",
  REVIEW_FINDING: "Review 讨论项",
  DELIVERY_CONFLICT: "交付冲突",
  OTHER: "其他",
};

const stopOutcomeLabels: Record<NonNullable<IssueStopCondition["outcome"]>, string> = {
  CONTINUE: "从保留阶段继续",
  REVISE_BRIEF: "修订新版简报",
  REPAIR: "返回开发返修",
  RE_REVIEW: "重新独立 Review",
  SPLIT_ISSUE: "拆分 Issue",
  SUPERSEDE: "由更大的工作替代",
  CANCEL: "取消本次交付",
};

const runEventKindLabels = {
  SYSTEM: "系统",
  THOUGHT: "思考",
  TOOL: "工具",
  TOOL_RESULT: "结果",
  MESSAGE: "回答",
  ERROR: "错误",
  RESULT: "完成",
} as const;

function element<T extends HTMLElement>(id: string): T {
  const value = document.getElementById(id);
  if (!value) throw new Error(`Missing element #${id}`);
  return value as T;
}

const escapeHtml = (value: unknown) =>
  String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
const shortSha = (value: string | null | undefined) => (value ? value.slice(0, 8) : "NOT SET");
const formatTime = (value: string | null | undefined) =>
  value
    ? new Intl.DateTimeFormat("zh-CN", {
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
      }).format(new Date(value))
    : "—";
const formatDuration = (startedAt: string) => {
  const seconds = Math.max(0, Math.floor((Date.now() - new Date(startedAt).getTime()) / 1_000));
  const minutes = Math.floor(seconds / 60);
  return minutes ? `${minutes} 分 ${String(seconds % 60).padStart(2, "0")} 秒` : `${seconds} 秒`;
};

function selectedIssue(): IssueView | null {
  return (
    snapshot?.issues.find((issue) => issue.number === selectedIssueNumber) ??
    snapshot?.issues[0] ??
    null
  );
}

function activeConversation(): AgentConversation | null {
  return collaboration?.conversations.find((item) => item.id === activeConversationId) ?? null;
}

function latestConversation(role: ConversationRole) {
  return collaboration?.conversations.filter((item) => item.role === role).at(-1) ?? null;
}

function selectedDiscussionConversation() {
  const active = activeConversation();
  return active?.role === "DISCUSSION" ? active : latestConversation("DISCUSSION");
}

function toneClass(tone: IssueView["state"]["tone"]) {
  return `tone-${tone}`;
}

function showToast(message: string) {
  const toast = element<HTMLDivElement>("toast");
  toast.textContent = message;
  toast.classList.add("is-visible");
  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = setTimeout(() => toast.classList.remove("is-visible"), 2600);
}

function requireSnapshot(): StatusSnapshot {
  if (!snapshot) throw new Error("Status has not loaded");
  return snapshot;
}

async function api<T>(url: string, options?: RequestInit): Promise<T> {
  const response = await fetch(url, { cache: "no-store", ...options });
  const payload = (await response.json()) as T & { message?: string; error?: string };
  if (!response.ok)
    throw new Error(payload.message ?? payload.error ?? `请求失败 (${response.status})`);
  return payload;
}

function renderOverview() {
  const current = requireSnapshot();
  element("project-name").textContent = current.project.name;
  element("project-subtitle").textContent = current.project.subtitle;
  const next = current.issues.find((issue) =>
    [
      "READY",
      "WORKTREE_READY",
      "IMPLEMENTING",
      "CANDIDATE_READY",
      "REVIEWING",
      "CHANGES_REQUESTED",
      "APPROVED",
    ].includes(issue.state.key),
  );
  element("handoff-title").textContent = next
    ? `#${next.number} · ${next.state.label}`
    : "当前没有可推进工作包";
  element("handoff-detail").textContent =
    next?.nextAction.detail ?? "检查依赖、GitHub 状态和本地账本。";
  const needsYou = current.issues.filter((issue) =>
    ["NEEDS_DISCUSSION", "NEEDS_HUMAN", "APPROVED"].includes(issue.state.key),
  ).length;
  element("needs-you-count").textContent = String(needsYou).padStart(2, "0");
  element("milestone-count").textContent = current.milestone
    ? `${current.milestone.open_issues}/${current.milestone.closed_issues}`
    : "N/A";
  element("milestone-state").textContent = current.milestone
    ? `${current.milestone.state.toUpperCase()} · OPEN/CLOSED`
    : "Milestone 未找到";
  element("integration-sha").textContent = shortSha(current.integration.sha);
  element("integration-state").textContent =
    `${current.integration.aheadMain} ahead · ${current.integration.behindMain} behind · ${current.integration.dirty.length ? `${current.integration.dirty.length} dirty` : "clean"}`;
  element("github-fact").textContent = current.source.stale ? "STALE" : "LIVE";
  element("git-fact").textContent = current.integration.sha ? "LIVE" : "MISSING";
  element("worktree-fact").textContent = `${current.worktrees.length} DETECTED`;
  element("collected-at").textContent = `COLLECTED ${formatTime(current.source.collectedAt)}`;
  element("signal-dot").className = `signal-dot ${current.source.stale ? "is-error" : "is-live"}`;
  element("sync-label").textContent = current.source.stale ? "STALE SNAPSHOT" : "FACTS LIVE";
  const strip = element<HTMLElement>("incident-strip");
  if (current.source.warning || !current.integration.hasRemoteBranch) {
    strip.textContent =
      current.source.warning ?? `注意：集成分支 ${current.integration.branch} 尚无远程同名分支。`;
    strip.classList.remove("is-hidden");
  } else {
    strip.classList.add("is-hidden");
  }
}

function renderIssueList() {
  const current = requireSnapshot();
  element("issue-counter").textContent = `${current.issues.length} PACKAGES`;
  element("issue-list").innerHTML = current.issues
    .map((issue, index) => {
      const dependencies = issue.github
        ? dependencyState(issue.github)
        : { ready: false, blockers: [] };
      const blockerText =
        dependencies.blockers.map((number) => `#${number}`).join("、") || "GitHub 状态未知";
      return `
      <li><button class="issue-card ${dependencies.ready ? "" : "is-delivery-locked"}" type="button" data-issue="${issue.number}" data-delivery-ready="${dependencies.ready}" aria-pressed="${issue.number === selectedIssueNumber}" title="${dependencies.ready ? "可查看并按当前门禁推进" : `交付锁定：等待 ${escapeHtml(blockerText)}；仍可查看和讨论`}">
        <span class="issue-index">${issue.kind === "control" ? "HQ" : String(index).padStart(2, "0")}</span>
        <span><strong>#${issue.number} · ${escapeHtml(issue.label)}</strong><small>${escapeHtml(issue.github?.state ?? "MISSING")} · ${escapeHtml(PHASE_LABELS[issue.control.phase])}</small><span class="mini-state ${toneClass(issue.state.tone)}">${escapeHtml(issue.state.label).toUpperCase()}</span>${dependencies.ready ? "" : `<span class="dependency-lock">DELIVERY LOCKED · ${escapeHtml(blockerText)}</span>`}</span>
      </button></li>`;
    })
    .join("");
  document.querySelectorAll<HTMLButtonElement>("[data-issue]").forEach((button) => {
    button.addEventListener("click", () => {
      selectedIssueNumber = Number(button.dataset.issue);
      collaboration = null;
      activeConversationId = null;
      activeStopConditionId = null;
      activeWorkbenchView = "discussion";
      renderIssueList();
      renderSelectedIssue();
      void refreshCollaboration();
    });
  });
}

function relationRow(left: string, right: string) {
  return `<div class="relation-row"><span>${escapeHtml(left)}</span><code>${escapeHtml(right)}</code></div>`;
}

function renderSelectedIssue() {
  const current = requireSnapshot();
  const issue = selectedIssue();
  if (!issue) return;
  element<HTMLAnchorElement>("issue-link").textContent = `ISSUE #${issue.number} / GITHUB`;
  element<HTMLAnchorElement>("issue-link").href = issue.github?.url ?? "#";
  element("issue-title").textContent = issue.github?.title ?? issue.label;
  element("issue-description").textContent =
    issue.github?.body
      .split("\n")
      .find((line) => line.trim())
      ?.slice(0, 180) ?? "查看外部事实、讨论与 Agent 接力。";
  element("issue-status").textContent = issue.state.label.toUpperCase();
  element("issue-status").className = `status-chip ${toneClass(issue.state.tone)}`;
  element("github-state").textContent = issue.github?.state ?? "MISSING";
  element("github-meta").textContent = issue.github
    ? `更新 ${formatTime(issue.github.updatedAt)}`
    : "项目配置与 GitHub 不一致";
  const detectedWorktree = issue.worktrees[0];
  const boundWorkspace = collaboration?.workspace;
  element("worktree-state").textContent = boundWorkspace || detectedWorktree ? "DETECTED" : "NONE";
  element("worktree-meta").textContent = boundWorkspace
    ? `${boundWorkspace.branch} · ${shortSha(boundWorkspace.baseSha)}`
    : detectedWorktree
      ? `${detectedWorktree.branch ?? "detached"} · ${shortSha(detectedWorktree.sha)}`
      : "尚未识别 Issue worktree";
  element("candidate-state").textContent = shortSha(issue.control.candidateSha);
  element("review-state").textContent = issue.slots.reviewerStatus;
  element("review-meta").textContent = issue.control.candidateSha
    ? `审 ${shortSha(issue.control.candidateSha)}`
    : "候选未冻结";
  element<HTMLSelectElement>("phase-input").value = issue.control.phase;
  element<HTMLTextAreaElement>("note-input").value = issue.control.note;
  element<HTMLInputElement>("candidate-input").value = issue.control.candidateSha ?? "";
  element("delivery-phase").textContent = issue.control.phase;
  const dependencies = issue.github?.blockedBy ?? [];
  element("dependency-list").innerHTML = dependencies.length
    ? dependencies
        .map(({ number, state }) => {
          const dependency = current.issues.find((item) => item.number === number);
          return relationRow(`#${number} ${dependency?.label ?? ""}`, state);
        })
        .join("")
    : '<p class="empty">GitHub 未设置 Blocked by</p>';
  element("pull-list").innerHTML = issue.pulls.length
    ? issue.pulls
        .map(
          (pull) =>
            `<div class="relation-row"><a href="${escapeHtml(pull.url)}" target="_blank" rel="noreferrer">PR #${pull.number} · ${escapeHtml(pull.title)}</a><code>${escapeHtml(pull.state)}</code></div>`,
        )
        .join("")
    : '<p class="empty">未识别到关联 PR</p>';
  renderCollaboration();
}

function setWorkbenchView(view: "discussion" | "delivery") {
  activeWorkbenchView = view;
  element("discussion-view").classList.toggle("is-hidden", view !== "discussion");
  element("delivery-view").classList.toggle("is-hidden", view !== "delivery");
  document.querySelectorAll<HTMLButtonElement>("[data-workbench-view]").forEach((button) => {
    button.setAttribute("aria-pressed", String(button.dataset.workbenchView === view));
  });
}

function renderCollaboration() {
  const issue = selectedIssue();
  if (!issue) return;
  const brief = collaboration?.briefs[0] ?? null;
  const workspace = collaboration?.workspace ?? null;
  const discussion = selectedDiscussionConversation();
  const implementation = latestConversation("IMPLEMENTATION") ?? latestConversation("REPAIR");
  const review = latestConversation("REVIEW");
  const developmentBriefTurn =
    collaboration?.turns.find(
      (turn) => turn.conversationId === discussion?.id && turn.intent === "DEVELOPMENT_BRIEF",
    ) ?? null;
  const generatedDevelopmentBrief =
    developmentBriefTurn?.status === "COMPLETED"
      ? (discussion?.messages.find(
          (message) => message.turnId === developmentBriefTurn.id && message.role === "AGENT",
        )?.text ?? null)
      : null;
  const paused = issue.attention.required;
  const dependencies = issue.github
    ? dependencyState(issue.github)
    : { ready: false, blockers: [] };
  const deliveryBlocked = !dependencies.ready;
  const blockerText =
    dependencies.blockers.map((number) => `#${number}`).join("、") || "GitHub 状态未知";
  element("delivery-view").classList.toggle("is-delivery-locked", deliveryBlocked);

  const phaseStages: Partial<Record<Phase, number>> = {
    PLANNED: 0,
    WORKTREE_READY: 2,
    IMPLEMENTING: 2,
    CANDIDATE_READY: 3,
    REVIEWING: 4,
    CHANGES_REQUESTED: 2,
    APPROVED: 5,
    MERGED_TO_MILESTONE: 6,
    VERIFIED: 6,
  };
  const artifactStage = !brief ? 0 : !workspace ? 1 : !issue.control.candidateSha ? 2 : 3;
  const currentStage = Math.max(artifactStage, phaseStages[issue.control.phase] ?? artifactStage);
  document.querySelectorAll<HTMLElement>("[data-pipeline-stage]").forEach((stage) => {
    const index = Number(stage.dataset.pipelineStage);
    stage.classList.toggle("is-current", index === currentStage);
    stage.classList.toggle("is-complete", index < currentStage);
  });

  if (paused || issue.kind !== "delivery") {
    element("next-action-title").textContent = issue.nextAction.label;
    element("next-action-detail").textContent = issue.nextAction.detail;
  } else if (deliveryBlocked) {
    element("next-action-title").textContent = "等待前置 Issue";
    element("next-action-detail").textContent =
      `${blockerText} 尚未关闭。当前只能继续 Discussion 和查看已有记录，不能产生或修改任何交付内容。`;
  } else if (!brief) {
    element("next-action-title").textContent = "确认开发说明";
    element("next-action-detail").textContent = discussion
      ? "根据 Discussion 生成开发说明，检查无误后确认为开发输入。"
      : "先在 Discussion 建立对话并形成当前有效理解，再生成开发说明。";
  } else if (!workspace) {
    element("next-action-title").textContent = "创建开发环境";
    element("next-action-detail").textContent =
      "使用 Git 从 Milestone 集成分支创建 Issue 分支和 worktree。";
  } else if (!implementation) {
    element("next-action-title").textContent = "开始编码";
    element("next-action-detail").textContent =
      "选择引擎，新的开发会话将收到已确认的开发说明和 worktree。";
  } else if (!issue.control.candidateSha) {
    element("next-action-title").textContent = "验证并冻结候选 SHA";
    element("next-action-detail").textContent =
      "开发输出不是证据；检查 Git 和测试后记录 candidate SHA。";
  } else if (!review) {
    element("next-action-title").textContent = "开始独立 Review";
    element("next-action-detail").textContent =
      "选择引擎，新会话只审冻结 SHA，不继承开发者主观上下文。";
  } else {
    element("next-action-title").textContent = issue.nextAction.label;
    element("next-action-detail").textContent = issue.nextAction.detail;
  }
  element("copy-command").classList.add("is-hidden");
  element("discussion-tab-meta").textContent =
    `${collaboration?.conversations.filter((item) => item.role === "DISCUSSION").length ?? 0} 个对话`;
  element("delivery-tab-meta").textContent = deliveryBlocked
    ? `LOCKED · 等待 ${blockerText}`
    : `${PHASE_LABELS[issue.control.phase]}${paused ? " · 已暂停" : ""}`;

  const draft = element<HTMLTextAreaElement>("brief-draft");
  const generationIsNewerThanFrozen = Boolean(
    generatedDevelopmentBrief &&
      developmentBriefTurn?.completedAt &&
      (!brief || developmentBriefTurn.completedAt > brief.frozenAt),
  );
  if (
    generatedDevelopmentBrief &&
    developmentBriefTurn &&
    generationIsNewerThanFrozen &&
    appliedDevelopmentBriefTurnIds.get(issue.number) !== developmentBriefTurn.id
  ) {
    briefDrafts.set(issue.number, generatedDevelopmentBrief);
    appliedDevelopmentBriefTurnIds.set(issue.number, developmentBriefTurn.id);
  }
  if (!briefDrafts.has(issue.number) && collaboration) {
    briefDrafts.set(issue.number, brief?.content ?? "");
  }
  if (document.activeElement !== draft) draft.value = briefDrafts.get(issue.number) ?? "";
  draft.disabled = deliveryBlocked;
  element("brief-version").textContent = brief ? `FROZEN v${brief.version}` : "DRAFT";
  const generateBrief = element<HTMLButtonElement>("generate-development-brief");
  generateBrief.disabled =
    deliveryBlocked ||
    !discussion ||
    discussion.messages.length === 0 ||
    discussion.status === "RUNNING";
  generateBrief.textContent =
    developmentBriefTurn?.status === "RUNNING" ? "正在生成开发说明…" : "根据讨论生成开发说明";
  element<HTMLButtonElement>("freeze-brief").disabled =
    deliveryBlocked || !discussion || !draft.value.trim();
  element<HTMLButtonElement>("freeze-brief").textContent = brief ? "确认为新版本" : "确认开发说明";
  element("brief-message").textContent = deliveryBlocked
    ? `等待 ${blockerText} 关闭后才能生成、编辑或确认开发说明。`
    : "";

  element("workspace-binding").textContent = workspace?.branch ?? "尚未创建";
  element("workspace-binding-meta").textContent = workspace
    ? `${workspace.path} · base ${shortSha(workspace.baseSha)}`
    : brief
      ? "已具备创建条件"
      : "确认开发说明后才能创建";
  const createWorkspace = element<HTMLButtonElement>("create-workspace");
  createWorkspace.disabled =
    Boolean(workspace) || !brief || deliveryBlocked || paused || issue.kind !== "delivery";
  createWorkspace.textContent = workspace ? "环境已创建" : "创建分支与 Worktree";

  const developerReady = Boolean(
    brief && workspace && !paused && !deliveryBlocked && issue.kind === "delivery",
  );
  element("developer-slot").classList.toggle("is-locked", !developerReady);
  element("developer-status").textContent =
    implementation?.status ?? (developerReady ? "READY" : "LOCKED");
  element("developer-engine").textContent = implementation
    ? `${implementation.engine.toUpperCase()} · ${roleLabels[implementation.role]}`
    : "选择引擎";
  const startImplementation = element<HTMLButtonElement>("start-implementation");
  startImplementation.disabled = !developerReady;
  startImplementation.textContent = implementation ? "新开开发对话" : "开始编码";

  const reviewerReady = Boolean(
    brief &&
      workspace &&
      issue.control.candidateSha &&
      !paused &&
      !deliveryBlocked &&
      issue.kind === "delivery",
  );
  element("reviewer-slot").classList.toggle("is-locked", !reviewerReady);
  element("reviewer-status").textContent = review?.status ?? (reviewerReady ? "READY" : "LOCKED");
  element("reviewer-engine").textContent = review
    ? `${review.engine.toUpperCase()} · REVIEW`
    : "选择引擎";
  const startReview = element<HTMLButtonElement>("start-review");
  startReview.disabled = !reviewerReady;
  startReview.textContent = review ? "新开 Review 对话" : "开始 Review";

  const controlLocked = deliveryBlocked || issue.kind !== "delivery";
  element<HTMLInputElement>("candidate-input").disabled = controlLocked;
  element<HTMLSelectElement>("phase-input").disabled = controlLocked;
  element<HTMLTextAreaElement>("note-input").disabled = controlLocked;
  element<HTMLButtonElement>("save-control").disabled = controlLocked;
  element("control-form").classList.toggle("is-delivery-locked", controlLocked);
  element("form-message").textContent = controlLocked
    ? issue.kind !== "delivery"
      ? "非 DELIVERY 工作流不开放交付状态写入。"
      : `等待 ${blockerText} 关闭后才能修改交付状态。`
    : "";

  updateCandidateButton();

  renderStopConditions();
  renderConversationTabs();
  renderChat();
  renderActivity();
  setWorkbenchView(activeWorkbenchView);
}

function updateCandidateButton() {
  const value = element<HTMLInputElement>("candidate-input").value.trim();
  const issue = selectedIssue();
  const dependenciesReady = issue?.github ? dependencyState(issue.github).ready : false;
  element<HTMLButtonElement>("freeze-candidate").disabled =
    !/^[0-9a-f]{7,40}$/i.test(value) || Boolean(issue?.attention.required) || !dependenciesReady;
}

function renderConversationTabs() {
  const conversations = collaboration?.conversations ?? [];
  const tabs = element("conversation-tabs");
  tabs.innerHTML = conversations.length
    ? conversations
        .map(
          (conversation) => `
    <button type="button" data-conversation="${escapeHtml(conversation.id)}" aria-pressed="${conversation.id === activeConversationId}" title="${escapeHtml(conversation.sessionId ? `Session ${conversation.sessionId}` : "Session 尚未由 Agent 返回")}">
      <span>${escapeHtml(roleLabels[conversation.role])}</span><strong>${escapeHtml(conversation.engine === "claude" ? "CLAUDE" : "CODEX")}</strong><small>${escapeHtml(conversation.sessionId ? `SESSION ${conversation.sessionId.slice(0, 8)}` : conversation.status)}</small>
    </button>`,
        )
        .join("")
    : "";
  document.querySelectorAll<HTMLButtonElement>("[data-conversation]").forEach((button) => {
    button.addEventListener("click", () => {
      activeConversationId = button.dataset.conversation ?? null;
      renderConversationTabs();
      renderChat();
    });
  });
}

function renderChat() {
  const conversation = activeConversation();
  const input = element<HTMLTextAreaElement>("chat-input");
  const send = element<HTMLButtonElement>("send-message");
  const cancel = element<HTMLButtonElement>("cancel-turn");
  if (!conversation) {
    element("active-conversation-role").textContent = "尚未选择对话";
    element("active-conversation-title").textContent = "从讨论开始";
    element("active-conversation-engine").textContent = "ENGINE LOCKED";
    element("chat-messages").innerHTML =
      '<p class="empty">点击“新建”，选择 Claude Code 或 Codex 开始讨论。</p>';
    element("chat-status").textContent = "NO CONVERSATION";
    input.disabled = true;
    send.disabled = true;
    cancel.classList.add("is-hidden");
    return;
  }
  element("active-conversation-role").textContent = roleLabels[conversation.role];
  element("active-conversation-title").textContent =
    conversation.engine === "claude" ? "Claude Code" : "Codex";
  const sessionLabel = conversation.sessionId
    ? `SESSION ${conversation.sessionId}`
    : "SESSION PENDING";
  element("active-conversation-engine").textContent = sessionLabel;
  element("active-conversation-engine").title = sessionLabel;
  element("chat-status").textContent = conversation.status;
  const messages = element("chat-messages");
  const latestTurn = collaboration?.turns.find((turn) => turn.conversationId === conversation.id);
  const runningTurn = latestTurn?.status === "RUNNING" ? latestTurn : null;
  const messageMarkup = conversation.messages.length
    ? conversation.messages
        .map((message) => {
          const turn = collaboration?.turns.find((item) => item.id === message.turnId);
          const generatedPrompt = message.role === "USER" && turn?.intent === "DEVELOPMENT_BRIEF";
          const author = generatedPrompt
            ? "QIANSHOU"
            : message.role === "USER"
              ? "YOU"
              : message.role === "AGENT"
                ? conversation.engine.toUpperCase()
                : "SYSTEM";
          const body =
            message.role === "AGENT"
              ? `<div class="markdown-body">${renderMarkdown(message.text)}</div>`
              : generatedPrompt
                ? `<details class="generated-brief-prompt"><summary>查看发送给 Discuss Agent 的提示词</summary><pre>${escapeHtml(message.text)}</pre></details>`
                : `<pre>${escapeHtml(message.text)}</pre>`;
          return `<article class="chat-message is-${message.role.toLowerCase()}"><header><strong>${author}</strong><time>${formatTime(message.at)}</time></header>${body}</article>`;
        })
        .join("")
    : `<div class="conversation-empty"><strong>${escapeHtml(roleLabels[conversation.role])}对话已创建</strong><p>引擎已固定为 ${escapeHtml(conversation.engine === "claude" ? "Claude Code" : "Codex")}。发送第一条消息后不可切换。</p></div>`;
  const runEvents = latestTurn?.progress?.events ?? [];
  const eventTimeline = runEvents.length
    ? `<ol class="agent-event-list">${runEvents
        .map(
          (event) => `
    <li class="is-${event.kind.toLowerCase()}">
      <header><span>${String(event.sequence).padStart(2, "0")} · ${escapeHtml(runEventKindLabels[event.kind])}</span><time>${escapeHtml(formatTime(event.at))}</time></header>
      <strong>${escapeHtml(event.title)}</strong>
      ${event.detail ? `<pre>${escapeHtml(event.detail)}</pre>` : ""}
    </li>`,
        )
        .join("")}</ol>`
    : '<p class="agent-event-empty">等待 Agent 输出第一个事件……</p>';
  const runSummary = latestTurn?.progress
    ? `${latestTurn.status === "RUNNING" ? `已运行 ${formatDuration(latestTurn.startedAt)}` : `运行状态 ${latestTurn.status}`} · 收到 ${latestTurn.progress.eventCount} 条 Agent 事件`
    : "";
  const progressMarkup = latestTurn?.progress
    ? runningTurn
      ? `
    <article class="agent-run-progress" aria-live="polite">
      <header><span class="run-pulse" aria-hidden="true"></span><strong>${escapeHtml(latestTurn.progress.summary)}</strong></header>
      <p>${escapeHtml(runSummary)}</p>
      ${eventTimeline}
      <small>最后活动 ${escapeHtml(formatTime(latestTurn.progress.updatedAt))}。事件已结构化并脱敏。</small>
    </article>`
      : runEvents.length
        ? `
    <details class="agent-run-progress is-finished">
      <summary>${escapeHtml(runSummary)} · 查看运行事件</summary>
      ${eventTimeline}
    </details>`
        : ""
    : "";
  messages.innerHTML = `${messageMarkup}${progressMarkup}`;
  messages.scrollTop = messages.scrollHeight;
  const running = Boolean(runningTurn);
  input.disabled = running;
  send.disabled = running;
  send.textContent = running ? "Agent 运行中…" : "发送";
  cancel.classList.toggle("is-hidden", !running);
}

function renderStopConditions() {
  const conditions = collaboration?.stopConditions ?? [];
  const list = element("stop-condition-list");
  list.innerHTML = conditions.length
    ? conditions
        .map(
          (stop) => `
    <article class="stop-condition-card ${stop.status === "OPEN" ? "is-open" : "is-resolved"}">
      <header><span>${escapeHtml(stop.status)}</span><h5>${escapeHtml(stop.summary)}</h5></header>
      ${stop.status === "OPEN" ? `<button class="secondary-button" type="button" data-resolve-stop="${escapeHtml(stop.id)}">记录结论</button>` : ""}
      <p>${escapeHtml(stop.detail || stop.resolution || "未填写补充说明")}</p>
      <small>${escapeHtml(stopCategoryLabels[stop.category])} · 恢复点 ${escapeHtml(PHASE_LABELS[stop.originPhase])} · Candidate ${escapeHtml(shortSha(stop.candidateSha))} · ${formatTime(stop.createdAt)}${stop.outcome ? ` · ${escapeHtml(stopOutcomeLabels[stop.outcome])}` : ""}</small>
    </article>`,
        )
        .join("")
    : '<p class="empty stop-empty">当前没有 Stop Condition。开发或 Review 遇到需要判断的问题时，从这里带着恢复点回流讨论。</p>';
  document.querySelectorAll<HTMLButtonElement>("[data-resolve-stop]").forEach((button) => {
    button.addEventListener("click", () => {
      activeStopConditionId = button.dataset.resolveStop ?? null;
      const stop = conditions.find((item) => item.id === activeStopConditionId);
      element("stop-resolution-title").textContent = stop
        ? `解决：${stop.summary}`
        : "解决 Stop Condition";
      element<HTMLFormElement>("stop-resolution-form").classList.remove("is-hidden");
      element<HTMLTextAreaElement>("stop-resolution").focus();
    });
  });
}

function renderActivity() {
  const issue = selectedIssue();
  if (!issue) return;
  const brief = collaboration?.briefs[0];
  element("checkpoint-phase").textContent = PHASE_LABELS[issue.control.phase];
  element("checkpoint-brief").textContent = brief ? `v${brief.version}` : "DRAFT";
  element("checkpoint-stops").textContent = String(issue.attention.openStopConditionCount).padStart(
    2,
    "0",
  );
  element("checkpoint-conversations").textContent = String(
    collaboration?.conversations.length ?? 0,
  ).padStart(2, "0");

  const activities: Array<{ at: string; label: string; detail: string; tone?: string }> = [];
  for (const stop of collaboration?.stopConditions ?? []) {
    activities.push({
      at: stop.resolvedAt ?? stop.createdAt,
      label: stop.status === "OPEN" ? `Stop Condition：${stop.summary}` : `已解决：${stop.summary}`,
      detail:
        stop.status === "OPEN"
          ? `保留在 ${PHASE_LABELS[stop.originPhase]}`
          : stopOutcomeLabels[stop.outcome ?? "CONTINUE"],
      tone: stop.status === "OPEN" ? "is-attention" : "is-success",
    });
  }
  for (const item of collaboration?.briefs ?? []) {
    activities.push({
      at: item.frozenAt,
      label: `确认开发说明 v${item.version}`,
      detail: `来自 Discussion ${item.sourceConversationId.slice(0, 8)}`,
      tone: "is-success",
    });
  }
  for (const conversation of collaboration?.conversations ?? []) {
    activities.push({
      at: conversation.createdAt,
      label: `新建${roleLabels[conversation.role]}对话`,
      detail: `${conversation.engine === "claude" ? "Claude Code" : "Codex"} · ${conversation.status}`,
    });
  }
  for (const event of requireSnapshot().events.filter(
    (item) => item.issueNumber === issue.number,
  )) {
    activities.push({
      at: event.at,
      label: `交付状态：${PHASE_LABELS[event.after.phase]}`,
      detail: event.note || `Candidate ${shortSha(event.after.candidateSha)}`,
    });
  }
  activities.sort((left, right) => right.at.localeCompare(left.at));
  element("activity-list").innerHTML = activities.length
    ? activities
        .slice(0, 30)
        .map(
          (activity) => `
    <li class="${activity.tone ?? ""}"><strong>${escapeHtml(activity.label)}</strong><span>${escapeHtml(activity.detail)}</span><small>${formatTime(activity.at)}</small></li>`,
        )
        .join("")
    : '<li class="empty">尚无本地协作记录</li>';
}

async function createStopCondition(event: SubmitEvent) {
  event.preventDefault();
  const issue = selectedIssue();
  if (!issue) return;
  const message = element("stop-condition-message");
  message.textContent = "";
  try {
    await api(`/api/issues/${issue.number}/stop-conditions`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        category: element<HTMLSelectElement>("stop-category").value,
        summary: element<HTMLInputElement>("stop-summary").value.trim(),
        detail: element<HTMLTextAreaElement>("stop-detail").value.trim(),
        ...(activeConversationId ? { originConversationId: activeConversationId } : {}),
      }),
    });
    element<HTMLFormElement>("stop-condition-form").reset();
    element<HTMLFormElement>("stop-condition-form").classList.add("is-hidden");
    activeWorkbenchView = "discussion";
    await refresh(true);
    showToast(`交付现场 ${PHASE_LABELS[issue.control.phase]} 已保留，问题进入 Discussion`);
  } catch (error: unknown) {
    message.textContent = error instanceof Error ? error.message : "记录失败";
  }
}

async function resolveStopCondition(event: SubmitEvent) {
  event.preventDefault();
  if (!activeStopConditionId) return;
  const message = element("stop-resolution-message");
  message.textContent = "";
  try {
    await api(`/api/stop-conditions/${encodeURIComponent(activeStopConditionId)}/resolve`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        resolution: element<HTMLTextAreaElement>("stop-resolution").value.trim(),
        outcome: element<HTMLSelectElement>("stop-outcome").value,
      }),
    });
    activeStopConditionId = null;
    element<HTMLFormElement>("stop-resolution-form").reset();
    element<HTMLFormElement>("stop-resolution-form").classList.add("is-hidden");
    await refresh(true);
    showToast("讨论结论已记录；原交付阶段未被改写");
  } catch (error: unknown) {
    message.textContent = error instanceof Error ? error.message : "解决失败";
  }
}

function openConversationForm(role: ConversationRole = "DISCUSSION") {
  const issue = selectedIssue();
  const dependencies = issue?.github
    ? dependencyState(issue.github)
    : { ready: false, blockers: [] };
  if (role !== "DISCUSSION" && !dependencies.ready) {
    const blockers =
      dependencies.blockers.map((number) => `#${number}`).join("、") || "GitHub 状态未知";
    showToast(`等待 ${blockers}：当前只能讨论，不能开始交付`);
    return;
  }
  setWorkbenchView("discussion");
  pendingConversationRole = role;
  const form = element<HTMLFormElement>("conversation-create-form");
  form.classList.remove("is-hidden");
  element("conversation-create-title").textContent = `新建${roleLabels[role]}对话`;
  element<HTMLSelectElement>("conversation-engine").focus();
}

async function createConversation(event: SubmitEvent) {
  event.preventDefault();
  const issue = selectedIssue();
  if (!issue) return;
  const role = pendingConversationRole;
  const engine = element<HTMLSelectElement>("conversation-engine").value as Engine;
  const message = element("conversation-create-message");
  message.textContent = "";
  try {
    const conversation = await api<AgentConversation>(`/api/issues/${issue.number}/conversations`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ role, engine }),
    });
    activeConversationId = conversation.id;
    element<HTMLFormElement>("conversation-create-form").classList.add("is-hidden");
    await refreshCollaboration();
    showToast(
      `${roleLabels[role]}对话已创建，引擎固定为 ${engine === "claude" ? "Claude Code" : "Codex"}`,
    );
  } catch (error: unknown) {
    message.textContent = error instanceof Error ? error.message : "创建失败";
  }
}

async function sendMessage(event: SubmitEvent) {
  event.preventDefault();
  const conversation = activeConversation();
  const input = element<HTMLTextAreaElement>("chat-input");
  const text = input.value.trim();
  if (!conversation || !text) return;
  input.value = "";
  try {
    await api(`/api/conversations/${encodeURIComponent(conversation.id)}/messages`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ text }),
    });
    await refreshCollaboration();
  } catch (error: unknown) {
    input.value = text;
    showToast(error instanceof Error ? error.message : "发送失败");
  }
}

async function cancelActiveTurn() {
  const conversation = activeConversation();
  const turn = collaboration?.turns.find(
    (item) => item.conversationId === conversation?.id && item.status === "RUNNING",
  );
  if (!turn || !window.confirm("确定停止本次 Agent 运行吗？已经产生的对话和运行记录会保留。"))
    return;
  const cancel = element<HTMLButtonElement>("cancel-turn");
  cancel.disabled = true;
  try {
    await api(`/api/turns/${encodeURIComponent(turn.id)}/cancel`, { method: "POST" });
    await refreshCollaboration();
    showToast("本次 Agent 运行已停止，可以继续这个对话");
  } catch (error: unknown) {
    showToast(error instanceof Error ? error.message : "停止失败");
  } finally {
    cancel.disabled = false;
  }
}

async function freezeBrief() {
  const discussion = selectedDiscussionConversation();
  const issue = selectedIssue();
  const content = element<HTMLTextAreaElement>("brief-draft").value.trim();
  if (!discussion || !issue || !content) return;
  const message = element("brief-message");
  message.textContent = "";
  try {
    await api(`/api/conversations/${encodeURIComponent(discussion.id)}/briefs`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ content }),
    });
    briefDrafts.set(issue.number, content);
    await refreshCollaboration();
    showToast("开发说明已确认，新开发和 Review 对话将引用这个版本");
  } catch (error: unknown) {
    message.textContent = error instanceof Error ? error.message : "冻结失败";
  }
}

async function generateDevelopmentBrief() {
  const discussion = selectedDiscussionConversation();
  if (!discussion) return;
  const button = element<HTMLButtonElement>("generate-development-brief");
  const message = element("brief-message");
  button.disabled = true;
  button.textContent = "正在发送给 Discuss Agent…";
  message.textContent = "";
  try {
    await api(`/api/conversations/${encodeURIComponent(discussion.id)}/development-brief`, {
      method: "POST",
    });
    await refreshCollaboration();
    showToast("已发送给 Discuss Agent；完成后会自动填入开发说明");
  } catch (error: unknown) {
    message.textContent = error instanceof Error ? error.message : "生成失败";
    renderCollaboration();
  }
}

async function createWorkspace() {
  const issue = selectedIssue();
  if (!issue) return;
  const button = element<HTMLButtonElement>("create-workspace");
  button.disabled = true;
  button.textContent = "正在创建…";
  try {
    await api(`/api/issues/${issue.number}/workspace`, { method: "POST" });
    await refresh(true);
    showToast("Git Issue 分支和 worktree 已创建");
  } catch (error: unknown) {
    showToast(error instanceof Error ? error.message : "创建失败");
    renderCollaboration();
  }
}

async function freezeCandidate(event: SubmitEvent) {
  event.preventDefault();
  const issue = selectedIssue();
  if (!issue) return;
  const candidateSha = element<HTMLInputElement>("candidate-input").value.trim();
  const message = element("candidate-message");
  message.textContent = "";
  try {
    await api(`/api/issues/${issue.number}/control`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        phase: "CANDIDATE_READY",
        candidateSha,
        note: "Candidate SHA frozen after human evidence check",
      }),
    });
    await refresh(true);
    showToast("Candidate SHA 已冻结，Review 已解锁");
  } catch (error: unknown) {
    message.textContent = error instanceof Error ? error.message : "冻结失败";
  }
}

async function saveControl(event: SubmitEvent) {
  event.preventDefault();
  const issue = selectedIssue();
  if (!issue) return;
  const message = element("form-message");
  message.textContent = "";
  try {
    await api(`/api/issues/${issue.number}/control`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        phase: element<HTMLSelectElement>("phase-input").value as Phase,
        note: element<HTMLTextAreaElement>("note-input").value,
      }),
    });
    await refresh(true);
    showToast("人工状态已更新");
  } catch (error: unknown) {
    message.textContent = error instanceof Error ? error.message : "保存失败";
  }
}

async function refreshCollaboration() {
  if (collaborationLoading || !snapshot) return;
  collaborationLoading = true;
  try {
    const next = await api<CollaborationView>(`/api/issues/${selectedIssueNumber}/collaboration`);
    collaboration = next;
    if (
      !activeConversationId ||
      !next.conversations.some((item) => item.id === activeConversationId)
    ) {
      activeConversationId = next.conversations.at(-1)?.id ?? null;
    }
    renderSelectedIssue();
  } catch (error: unknown) {
    showToast(error instanceof Error ? error.message : "协作状态读取失败");
  } finally {
    collaborationLoading = false;
  }
}

async function refresh(force = false) {
  if (loading) return;
  loading = true;
  element("sync-label").textContent = "SYNCING";
  try {
    snapshot = await api<StatusSnapshot>(`/api/status${force ? `?t=${Date.now()}` : ""}`);
    if (!snapshot.issues.some((issue) => issue.number === selectedIssueNumber))
      selectedIssueNumber = snapshot.issues[0]?.number ?? 0;
    renderOverview();
    renderIssueList();
    renderSelectedIssue();
    await refreshCollaboration();
  } catch (error: unknown) {
    element("signal-dot").className = "signal-dot is-error";
    element("sync-label").textContent = "SYNC FAILED";
    const strip = element("incident-strip");
    strip.textContent = error instanceof Error ? error.message : "同步失败";
    strip.classList.remove("is-hidden");
  } finally {
    loading = false;
  }
}

element<HTMLButtonElement>("refresh-button").addEventListener("click", () => void refresh(true));
document.querySelectorAll<HTMLButtonElement>("[data-workbench-view]").forEach((button) => {
  button.addEventListener("click", () => {
    setWorkbenchView(button.dataset.workbenchView === "delivery" ? "delivery" : "discussion");
  });
});
element<HTMLButtonElement>("new-conversation").addEventListener("click", () =>
  openConversationForm("DISCUSSION"),
);
element<HTMLButtonElement>("cancel-conversation").addEventListener("click", () =>
  element("conversation-create-form").classList.add("is-hidden"),
);
element<HTMLFormElement>("conversation-create-form").addEventListener(
  "submit",
  (event) => void createConversation(event),
);
element<HTMLFormElement>("chat-form").addEventListener(
  "submit",
  (event) => void sendMessage(event),
);
element<HTMLButtonElement>("cancel-turn").addEventListener("click", () => void cancelActiveTurn());
element<HTMLButtonElement>("generate-development-brief").addEventListener(
  "click",
  () => void generateDevelopmentBrief(),
);
element<HTMLButtonElement>("freeze-brief").addEventListener("click", () => void freezeBrief());
element<HTMLTextAreaElement>("brief-draft").addEventListener("input", (event) =>
  briefDrafts.set(selectedIssueNumber, (event.target as HTMLTextAreaElement).value),
);
element<HTMLButtonElement>("create-workspace").addEventListener(
  "click",
  () => void createWorkspace(),
);
element<HTMLButtonElement>("start-implementation").addEventListener("click", () =>
  openConversationForm("IMPLEMENTATION"),
);
element<HTMLButtonElement>("start-review").addEventListener("click", () =>
  openConversationForm("REVIEW"),
);
element<HTMLFormElement>("candidate-form").addEventListener(
  "submit",
  (event) => void freezeCandidate(event),
);
element<HTMLInputElement>("candidate-input").addEventListener("input", updateCandidateButton);
element<HTMLFormElement>("control-form").addEventListener(
  "submit",
  (event) => void saveControl(event),
);
element<HTMLButtonElement>("open-stop-condition").addEventListener("click", () => {
  setWorkbenchView("discussion");
  element<HTMLFormElement>("stop-condition-form").classList.remove("is-hidden");
  element<HTMLInputElement>("stop-summary").focus();
});
element<HTMLButtonElement>("cancel-stop-condition").addEventListener("click", () =>
  element<HTMLFormElement>("stop-condition-form").classList.add("is-hidden"),
);
element<HTMLFormElement>("stop-condition-form").addEventListener(
  "submit",
  (event) => void createStopCondition(event),
);
element<HTMLButtonElement>("cancel-stop-resolution").addEventListener("click", () => {
  activeStopConditionId = null;
  element<HTMLFormElement>("stop-resolution-form").classList.add("is-hidden");
});
element<HTMLFormElement>("stop-resolution-form").addEventListener(
  "submit",
  (event) => void resolveStopCondition(event),
);

void refresh();
setInterval(() => {
  if (collaboration?.turns.some((turn) => turn.status === "RUNNING")) void refreshCollaboration();
}, 2_000);
setInterval(() => void refresh(), 30_000);
