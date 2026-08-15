import { mkdtemp, readFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { type ProjectConfig } from '@qianshou/core';
import { JsonStateStore } from '../src/state-store.js';

const project: ProjectConfig = {
  id: 'demo',
  repository: { slug: 'owner/repo', path: '/tmp/repo' },
  milestone: { number: 7 },
  integration: { branch: 'milestone', worktree: '/tmp/repo-m7', baseBranch: 'main' },
  refreshSeconds: 30,
  defaults: { developerEngine: 'codex', reviewerEngine: 'claude' }
};

describe('JSON handoff ledger', () => {
  it('initializes controls and persists an auditable event atomically', async () => {
    const directory = await mkdtemp(join(tmpdir(), 'qianshou-store-'));
    const path = join(directory, 'state.json');
    const timestamps = ['2026-08-14T00:00:00.000Z', '2026-08-14T00:01:00.000Z'];
    const store = new JsonStateStore(path, () => timestamps.shift() ?? '2026-08-14T00:02:00.000Z');
    const initial = await store.read(project, [7]);
    expect(initial.projects.demo?.issues['7']?.phase).toBe('PLANNED');
    const result = await store.updateIssue(project, 7, { phase: 'IMPLEMENTING', note: 'started manually' });
    expect(result.control.phase).toBe('IMPLEMENTING');
    expect(result.events[0]?.before.phase).toBe('PLANNED');
    expect(result.events[0]?.after.phase).toBe('IMPLEMENTING');
    const persisted = JSON.parse(await readFile(path, 'utf8')) as { projects: { demo: { issues: Record<string, { note: string }> } } };
    expect(persisted.projects.demo.issues['7']?.note).toBe('started manually');
  });

  it('persists immutable-engine conversations, resumable turns, and versioned briefs', async () => {
    const directory = await mkdtemp(join(tmpdir(), 'qianshou-collaboration-'));
    const path = join(directory, 'state.json');
    let sequence = 0;
    const store = new JsonStateStore(
      path,
      () => `2026-08-14T00:0${sequence}:00.000Z`,
      () => `id-${++sequence}`
    );
    const conversation = await store.createConversation(project, 7, {
      role: 'DISCUSSION',
      engine: 'claude',
      title: '先把问题聊清楚',
      initialContext: 'Issue #7'
    });
    expect(conversation.engine).toBe('claude');

    const turn = await store.beginConversationTurn(project, conversation.id, '上户是否自动发生？');
    await store.completeConversationTurn(project, conversation.id, turn.id, {
      sessionId: 'claude-session-1',
      text: '上户必须人工确认。',
      rawEvents: []
    });
    const brief = await store.freezeBrief(project, conversation.id, '# 决策\n上户必须人工确认');
    expect(brief.version).toBe(1);

    const state = await store.read(project);
    const saved = state.projects.demo?.conversations[conversation.id];
    expect(saved?.engine).toBe('claude');
    expect(saved?.sessionId).toBe('claude-session-1');
    expect(saved?.messages.map((message) => message.role)).toEqual(['USER', 'AGENT']);
    expect(state.projects.demo?.briefs[String(brief.id)]?.sourceConversationId).toBe(conversation.id);
  });

  it('persists live progress, supports explicit cancellation, and ignores the late process failure', async () => {
    const directory = await mkdtemp(join(tmpdir(), 'qianshou-running-turn-'));
    const path = join(directory, 'state.json');
    let sequence = 0;
    const store = new JsonStateStore(
      path,
      () => `2026-08-14T00:0${sequence}:00.000Z`,
      () => `id-${++sequence}`
    );
    const conversation = await store.createConversation(project, 7, {
      role: 'DISCUSSION', engine: 'claude', initialContext: 'Issue #7'
    });
    const turn = await store.beginConversationTurn(project, conversation.id, '继续讨论', 'DEVELOPMENT_BRIEF');
    expect(turn.intent).toBe('DEVELOPMENT_BRIEF');

    await store.updateConversationTurnProgress(project, turn.id, {
      eventCount: 3,
      summary: '正在检查仓库',
      event: { sequence: 3, kind: 'TOOL', title: '使用工具 Read', detail: '/tmp/domain.md' }
    });
    await store.cancelConversationTurn(project, conversation.id, turn.id, '用户停止了本次运行');
    await store.failConversationTurn(project, conversation.id, turn.id, 'process exited with SIGTERM');

    const collaboration = await store.getCollaboration(project, 7);
    expect(collaboration.turns[0]).toMatchObject({
      status: 'CANCELLED',
      progress: {
        eventCount: 3,
        summary: '运行已停止',
        events: [{ sequence: 3, kind: 'TOOL', title: '使用工具 Read', detail: '/tmp/domain.md' }]
      }
    });
    expect(collaboration.conversations[0]).toMatchObject({ status: 'ACTIVE' });
    expect(collaboration.conversations[0]?.messages.at(-1)?.text).toBe('用户停止了本次运行');
  });

  it('recovers persisted RUNNING turns as cancelled when a new server owns no process', async () => {
    const directory = await mkdtemp(join(tmpdir(), 'qianshou-orphaned-turn-'));
    const path = join(directory, 'state.json');
    let sequence = 0;
    const store = new JsonStateStore(
      path,
      () => `2026-08-14T00:0${sequence}:00.000Z`,
      () => `id-${++sequence}`
    );
    const conversation = await store.createConversation(project, 7, {
      role: 'DISCUSSION', engine: 'codex', initialContext: 'Issue #7'
    });
    const turn = await store.beginConversationTurn(project, conversation.id, '分析现状');

    expect(await store.recoverOrphanedTurns(project)).toBe(1);
    const collaboration = await store.getCollaboration(project, 7);
    expect(collaboration.turns.find((item) => item.id === turn.id)).toMatchObject({ status: 'CANCELLED' });
    expect(collaboration.conversations[0]?.messages.at(-1)?.text).toContain('服务已重启');
  });

  it('keeps the complete normalized event timeline instead of silently truncating old evidence', async () => {
    const directory = await mkdtemp(join(tmpdir(), 'qianshou-complete-events-'));
    const path = join(directory, 'state.json');
    let sequence = 0;
    const store = new JsonStateStore(path, () => new Date(1_723_600_000_000 + sequence * 1_000).toISOString(), () => `id-${++sequence}`);
    const conversation = await store.createConversation(project, 7, {
      role: 'DISCUSSION', engine: 'claude', initialContext: 'Issue #7'
    });
    const turn = await store.beginConversationTurn(project, conversation.id, '长时间分析');

    for (let index = 1; index <= 101; index += 1) {
      await store.updateConversationTurnProgress(project, turn.id, {
        eventCount: index,
        summary: `事件 ${index}`,
        event: { sequence: index, kind: 'THOUGHT', title: `事件 ${index}`, detail: null }
      });
    }

    const saved = (await store.getCollaboration(project, 7)).turns[0]?.progress?.events ?? [];
    expect(saved).toHaveLength(101);
    expect(saved[0]?.sequence).toBe(1);
    expect(saved.at(-1)?.sequence).toBe(101);
  });

  it('records a Stop Condition with a recovery snapshot without overwriting delivery state', async () => {
    const directory = await mkdtemp(join(tmpdir(), 'qianshou-stop-condition-'));
    const path = join(directory, 'state.json');
    let sequence = 0;
    const store = new JsonStateStore(
      path,
      () => `2026-08-14T00:0${sequence}:00.000Z`,
      () => `id-${++sequence}`
    );
    await store.read(project, [7]);
    await store.updateIssue(project, 7, { phase: 'REVIEWING', candidateSha: 'abcdef1' });

    const stop = await store.createStopCondition(project, 7, {
      category: 'SCOPE_CHANGE',
      summary: 'Review 发现当前 Issue 定义过小',
      detail: '需要回到讨论决定拆分还是扩大范围。'
    });

    expect(stop).toMatchObject({
      originPhase: 'REVIEWING',
      candidateSha: 'abcdef1',
      status: 'OPEN'
    });
    let state = await store.read(project);
    expect(state.projects.demo?.issues['7']?.phase).toBe('REVIEWING');
    expect(state.projects.demo?.stopConditions[stop.id]?.status).toBe('OPEN');

    const resolved = await store.resolveStopCondition(project, stop.id, {
      resolution: '拆出新的基础 Issue，当前 Review 保持原候选。',
      outcome: 'SPLIT_ISSUE'
    });
    state = await store.read(project);
    expect(resolved.status).toBe('RESOLVED');
    expect(resolved.outcome).toBe('SPLIT_ISSUE');
    expect(state.projects.demo?.issues['7']?.phase).toBe('REVIEWING');
  });
});
