import {
  initialIssueControl,
  type AgentConversation,
  type AgentTurn,
  type CollaborationView,
  type ControlEvent,
  type ControlPatch,
  type CreateStopCondition,
  type CreateConversation,
  type IssueControl,
  type IssueStopCondition,
  type IssueWorkspaceBinding,
  type ProjectConfig,
  type ResolveStopCondition,
  type TaskBriefSnapshot
} from '@qianshou/core';
import { mkdir, readFile, rename, writeFile } from 'node:fs/promises';
import { dirname } from 'node:path';
import { randomUUID } from 'node:crypto';

export interface ProjectState {
  issues: Record<string, IssueControl>;
  events: ControlEvent[];
  conversations: Record<string, AgentConversation>;
  turns: Record<string, AgentTurn>;
  briefs: Record<string, TaskBriefSnapshot>;
  workspaces: Record<string, IssueWorkspaceBinding>;
  stopConditions: Record<string, IssueStopCondition>;
}

export interface LedgerState {
  version: 1;
  projects: Record<string, ProjectState>;
}

export class JsonStateStore {
  private writeChain: Promise<unknown> = Promise.resolve();

  constructor(
    private readonly path: string,
    private readonly clock: () => string = () => new Date().toISOString(),
    private readonly idFactory: () => string = randomUUID
  ) {}

  async read(project: ProjectConfig, observedIssueNumbers: number[] = []): Promise<LedgerState> {
    let state: LedgerState;
    try {
      state = JSON.parse(await readFile(this.path, 'utf8')) as LedgerState;
    } catch (error: unknown) {
      if (!(error instanceof Error) || !('code' in error) || error.code !== 'ENOENT') throw error;
      state = { version: 1, projects: {} };
    }
    const projectState = state.projects[project.id] ?? {
      issues: {},
      events: [],
      conversations: {},
      turns: {},
      briefs: {},
      workspaces: {},
      stopConditions: {}
    };
    projectState.conversations ??= {};
    projectState.turns ??= {};
    projectState.briefs ??= {};
    projectState.workspaces ??= {};
    projectState.stopConditions ??= {};
    for (const turn of Object.values(projectState.turns)) {
      turn.intent ??= 'MESSAGE';
      turn.progress ??= null;
      if (turn.progress) turn.progress.events ??= [];
    }
    for (const issueNumber of observedIssueNumbers) {
      projectState.issues[String(issueNumber)] ??= initialIssueControl(project, issueNumber, this.clock());
    }
    state.projects[project.id] = projectState;
    return state;
  }

  async updateIssue(project: ProjectConfig, issueNumber: number, patch: ControlPatch) {
    const operation = this.writeChain.then(async () => {
      const state = await this.read(project);
      const projectState = state.projects[project.id];
      if (!projectState) throw new Error(`missing project state: ${project.id}`);
      const key = String(issueNumber);
      const before = projectState.issues[key] ?? initialIssueControl(project, issueNumber, this.clock());
      const updatedAt = this.clock();
      const after: IssueControl = {
        ...before,
        ...(patch.phase !== undefined ? { phase: patch.phase } : {}),
        ...(patch.candidateSha !== undefined ? { candidateSha: patch.candidateSha } : {}),
        ...(patch.developerEngine !== undefined ? { developerEngine: patch.developerEngine } : {}),
        ...(patch.reviewerEngine !== undefined ? { reviewerEngine: patch.reviewerEngine } : {}),
        ...(patch.note !== undefined ? { note: patch.note } : {}),
        issueNumber,
        updatedAt
      };
      projectState.issues[key] = after;
      projectState.events.unshift({
        id: `${updatedAt}:${issueNumber}`,
        issueNumber,
        type: 'CONTROL_UPDATED',
        at: updatedAt,
        before: { phase: before.phase, candidateSha: before.candidateSha },
        after: { phase: after.phase, candidateSha: after.candidateSha },
        note: after.note
      });
      projectState.events = projectState.events.slice(0, 300);
      await this.write(state);
      return { control: after, events: projectState.events };
    });
    this.writeChain = operation.catch(() => undefined);
    return operation;
  }

  async createConversation(
    project: ProjectConfig,
    issueNumber: number,
    input: CreateConversation & { initialContext: string }
  ): Promise<AgentConversation> {
    return this.mutate(project, async (state) => {
      const projectState = this.requireProjectState(state, project);
      const id = this.idFactory();
      const now = this.clock();
      const conversation: AgentConversation = {
        id,
        issueNumber,
        role: input.role,
        engine: input.engine,
        title: input.title ?? `${input.role} · #${issueNumber}`,
        status: 'READY',
        sessionId: null,
        initialContext: input.initialContext,
        messages: [],
        createdAt: now,
        updatedAt: now
      };
      projectState.conversations[id] = conversation;
      return conversation;
    });
  }

  async beginConversationTurn(
    project: ProjectConfig,
    conversationId: string,
    text: string,
    intent: AgentTurn['intent'] = 'MESSAGE'
  ): Promise<AgentTurn> {
    const prompt = text.trim();
    if (!prompt) throw new Error('message must not be empty');
    return this.mutate(project, async (state) => {
      const projectState = this.requireProjectState(state, project);
      const conversation = projectState.conversations[conversationId];
      if (!conversation) throw new Error(`conversation not found: ${conversationId}`);
      if (conversation.status === 'RUNNING') throw new Error('conversation already has a running turn');
      if (conversation.status === 'ARCHIVED') throw new Error('conversation is archived');
      const now = this.clock();
      const turnId = this.idFactory();
      const messageId = this.idFactory();
      const turn: AgentTurn = {
        id: turnId,
        conversationId,
        issueNumber: conversation.issueNumber,
        role: conversation.role,
        engine: conversation.engine,
        intent,
        status: 'RUNNING',
        prompt,
        error: null,
        progress: { eventCount: 0, summary: '正在启动 Agent', updatedAt: now, events: [] },
        startedAt: now,
        completedAt: null
      };
      conversation.messages.push({ id: messageId, role: 'USER', text: prompt, at: now, turnId });
      conversation.status = 'RUNNING';
      conversation.updatedAt = now;
      projectState.turns[turnId] = turn;
      return turn;
    });
  }

  async completeConversationTurn(
    project: ProjectConfig,
    conversationId: string,
    turnId: string,
    result: { sessionId: string; text: string; rawEvents: unknown[] }
  ): Promise<AgentConversation> {
    return this.mutate(project, async (state) => {
      const projectState = this.requireProjectState(state, project);
      const conversation = projectState.conversations[conversationId];
      const turn = projectState.turns[turnId];
      if (!conversation || !turn || turn.conversationId !== conversationId) throw new Error('conversation turn not found');
      if (turn.status !== 'RUNNING') throw new Error(`conversation turn is already ${turn.status}`);
      const now = this.clock();
      conversation.messages.push({ id: this.idFactory(), role: 'AGENT', text: result.text, at: now, turnId });
      conversation.sessionId = result.sessionId;
      conversation.status = 'ACTIVE';
      conversation.updatedAt = now;
      turn.status = 'COMPLETED';
      turn.progress = { eventCount: turn.progress?.eventCount ?? 0, summary: '运行完成', updatedAt: now, events: turn.progress?.events ?? [] };
      turn.completedAt = now;
      return conversation;
    });
  }

  async failConversationTurn(project: ProjectConfig, conversationId: string, turnId: string, error: string): Promise<AgentConversation> {
    return this.mutate(project, async (state) => {
      const projectState = this.requireProjectState(state, project);
      const conversation = projectState.conversations[conversationId];
      const turn = projectState.turns[turnId];
      if (!conversation || !turn || turn.conversationId !== conversationId) throw new Error('conversation turn not found');
      if (turn.status === 'CANCELLED') return conversation;
      if (turn.status !== 'RUNNING') throw new Error(`conversation turn is already ${turn.status}`);
      const now = this.clock();
      conversation.status = 'FAILED';
      conversation.updatedAt = now;
      conversation.messages.push({ id: this.idFactory(), role: 'SYSTEM', text: error, at: now, turnId });
      turn.status = 'FAILED';
      turn.error = error;
      turn.progress = { eventCount: turn.progress?.eventCount ?? 0, summary: '运行失败', updatedAt: now, events: turn.progress?.events ?? [] };
      turn.completedAt = now;
      return conversation;
    });
  }

  async updateConversationTurnProgress(
    project: ProjectConfig,
    turnId: string,
    progress: { eventCount: number; summary: string; event: Omit<NonNullable<AgentTurn['progress']>['events'][number], 'at'> }
  ): Promise<AgentTurn | null> {
    return this.mutate(project, async (state) => {
      const projectState = this.requireProjectState(state, project);
      const turn = projectState.turns[turnId];
      if (!turn || turn.status !== 'RUNNING') return null;
      const now = this.clock();
      const previousEvents = turn.progress?.events ?? [];
      turn.progress = {
        eventCount: progress.eventCount,
        summary: progress.summary,
        updatedAt: now,
        events: [...previousEvents, { ...progress.event, at: now }]
      };
      const conversation = projectState.conversations[turn.conversationId];
      if (conversation) conversation.updatedAt = now;
      return turn;
    });
  }

  async cancelConversationTurn(
    project: ProjectConfig,
    conversationId: string,
    turnId: string,
    reason: string
  ): Promise<AgentConversation> {
    return this.mutate(project, async (state) => {
      const projectState = this.requireProjectState(state, project);
      const conversation = projectState.conversations[conversationId];
      const turn = projectState.turns[turnId];
      if (!conversation || !turn || turn.conversationId !== conversationId) throw new Error('conversation turn not found');
      if (turn.status !== 'RUNNING') throw new Error(`conversation turn is already ${turn.status}`);
      const now = this.clock();
      turn.status = 'CANCELLED';
      turn.error = reason;
      turn.completedAt = now;
      turn.progress = { eventCount: turn.progress?.eventCount ?? 0, summary: '运行已停止', updatedAt: now, events: turn.progress?.events ?? [] };
      conversation.status = 'ACTIVE';
      conversation.updatedAt = now;
      conversation.messages.push({ id: this.idFactory(), role: 'SYSTEM', text: reason, at: now, turnId });
      return conversation;
    });
  }

  async recoverOrphanedTurns(project: ProjectConfig): Promise<number> {
    return this.mutate(project, async (state) => {
      const projectState = this.requireProjectState(state, project);
      const orphaned = Object.values(projectState.turns).filter((turn) => turn.status === 'RUNNING');
      for (const turn of orphaned) {
        const conversation = projectState.conversations[turn.conversationId];
        if (!conversation) continue;
        const now = this.clock();
        const reason = 'Qianshou 服务已重启，无法恢复上一次 Agent 进程；本次运行已标记为取消，可以重新发送。';
        turn.status = 'CANCELLED';
        turn.error = reason;
        turn.completedAt = now;
        turn.progress = { eventCount: turn.progress?.eventCount ?? 0, summary: '运行随服务重启中断', updatedAt: now, events: turn.progress?.events ?? [] };
        conversation.status = 'ACTIVE';
        conversation.updatedAt = now;
        conversation.messages.push({ id: this.idFactory(), role: 'SYSTEM', text: reason, at: now, turnId: turn.id });
      }
      return orphaned.length;
    });
  }

  async freezeBrief(project: ProjectConfig, conversationId: string, content: string): Promise<TaskBriefSnapshot> {
    const normalized = content.trim();
    if (!normalized) throw new Error('task brief must not be empty');
    return this.mutate(project, async (state) => {
      const projectState = this.requireProjectState(state, project);
      const conversation = projectState.conversations[conversationId];
      if (!conversation) throw new Error(`conversation not found: ${conversationId}`);
      if (conversation.role !== 'DISCUSSION') throw new Error('task briefs can only be frozen from a discussion');
      const version = Object.values(projectState.briefs).filter((brief) => brief.issueNumber === conversation.issueNumber).length + 1;
      const id = this.idFactory();
      const brief: TaskBriefSnapshot = {
        id,
        issueNumber: conversation.issueNumber,
        sourceConversationId: conversationId,
        version,
        content: normalized,
        frozenAt: this.clock()
      };
      projectState.briefs[id] = brief;
      return brief;
    });
  }

  async attachWorkspace(project: ProjectConfig, workspace: IssueWorkspaceBinding): Promise<IssueWorkspaceBinding> {
    return this.mutate(project, async (state) => {
      const projectState = this.requireProjectState(state, project);
      projectState.workspaces[String(workspace.issueNumber)] = workspace;
      return workspace;
    });
  }

  async createStopCondition(project: ProjectConfig, issueNumber: number, input: CreateStopCondition): Promise<IssueStopCondition> {
    return this.mutate(project, async (state) => {
      const projectState = this.requireProjectState(state, project);
      const control = projectState.issues[String(issueNumber)];
      if (!control) throw new Error(`missing Issue control: ${issueNumber}`);
      if (input.originConversationId) {
        const conversation = projectState.conversations[input.originConversationId];
        if (!conversation || conversation.issueNumber !== issueNumber) {
          throw new Error('origin conversation does not belong to this Issue');
        }
      }
      const now = this.clock();
      const stopCondition: IssueStopCondition = {
        id: this.idFactory(),
        issueNumber,
        category: input.category,
        summary: input.summary,
        detail: input.detail,
        originPhase: control.phase,
        originConversationId: input.originConversationId ?? null,
        candidateSha: control.candidateSha,
        status: 'OPEN',
        resolution: null,
        outcome: null,
        createdAt: now,
        resolvedAt: null
      };
      projectState.stopConditions[stopCondition.id] = stopCondition;
      return stopCondition;
    });
  }

  async resolveStopCondition(project: ProjectConfig, stopConditionId: string, input: ResolveStopCondition): Promise<IssueStopCondition> {
    return this.mutate(project, async (state) => {
      const projectState = this.requireProjectState(state, project);
      const stopCondition = projectState.stopConditions[stopConditionId];
      if (!stopCondition) throw new Error(`Stop Condition not found: ${stopConditionId}`);
      if (stopCondition.status === 'RESOLVED') throw new Error('Stop Condition is already resolved');
      stopCondition.status = 'RESOLVED';
      stopCondition.resolution = input.resolution;
      stopCondition.outcome = input.outcome;
      stopCondition.resolvedAt = this.clock();
      return stopCondition;
    });
  }

  async getCollaboration(project: ProjectConfig, issueNumber: number): Promise<CollaborationView> {
    const state = await this.read(project);
    const projectState = this.requireProjectState(state, project);
    return {
      issueNumber,
      conversations: Object.values(projectState.conversations).filter((item) => item.issueNumber === issueNumber).sort((left, right) => left.createdAt.localeCompare(right.createdAt)),
      turns: Object.values(projectState.turns).filter((item) => item.issueNumber === issueNumber).sort((left, right) => right.startedAt.localeCompare(left.startedAt)),
      briefs: Object.values(projectState.briefs).filter((item) => item.issueNumber === issueNumber).sort((left, right) => right.version - left.version),
      workspace: projectState.workspaces[String(issueNumber)] ?? null,
      stopConditions: Object.values(projectState.stopConditions)
        .filter((item) => item.issueNumber === issueNumber)
        .sort((left, right) => right.createdAt.localeCompare(left.createdAt))
    };
  }

  async findConversation(project: ProjectConfig, conversationId: string): Promise<AgentConversation | null> {
    const state = await this.read(project);
    return this.requireProjectState(state, project).conversations[conversationId] ?? null;
  }

  async findTurn(project: ProjectConfig, turnId: string): Promise<AgentTurn | null> {
    const state = await this.read(project);
    return this.requireProjectState(state, project).turns[turnId] ?? null;
  }

  async findStopCondition(project: ProjectConfig, stopConditionId: string): Promise<IssueStopCondition | null> {
    const state = await this.read(project);
    return this.requireProjectState(state, project).stopConditions[stopConditionId] ?? null;
  }

  private requireProjectState(state: LedgerState, project: ProjectConfig): ProjectState {
    const projectState = state.projects[project.id];
    if (!projectState) throw new Error(`missing project state: ${project.id}`);
    return projectState;
  }

  private async mutate<T>(project: ProjectConfig, action: (state: LedgerState) => Promise<T>): Promise<T> {
    const operation = this.writeChain.then(async () => {
      const state = await this.read(project);
      const result = await action(state);
      await this.write(state);
      return result;
    });
    this.writeChain = operation.catch(() => undefined);
    return operation;
  }

  private async write(state: LedgerState) {
    await mkdir(dirname(this.path), { recursive: true });
    const temporary = `${this.path}.${process.pid}.tmp`;
    await writeFile(temporary, `${JSON.stringify(state, null, 2)}\n`, { mode: 0o600 });
    await rename(temporary, this.path);
  }
}
