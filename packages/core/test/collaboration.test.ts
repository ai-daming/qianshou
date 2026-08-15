import { describe, expect, it } from 'vitest';
import { buildAgentInputPackage, createConversationSchema } from '../src/index.js';

describe('collaboration handoff domain', () => {
  it('binds every new conversation to exactly one supported engine', () => {
    expect(createConversationSchema.parse({ role: 'DISCUSSION', engine: 'claude', title: '厘清生命周期' })).toEqual({
      role: 'DISCUSSION',
      engine: 'claude',
      title: '厘清生命周期'
    });
    expect(createConversationSchema.parse({ role: 'REVIEW', engine: 'codex' })).toEqual({ role: 'REVIEW', engine: 'codex' });
    expect(() => createConversationSchema.parse({ role: 'DISCUSSION', engine: 'zcode' })).toThrow();
  });

  it('builds a development input from the frozen brief instead of raw discussion history', () => {
    const input = buildAgentInputPackage({
      role: 'IMPLEMENTATION',
      issueNumber: 224,
      issueTitle: '服务关系生命周期',
      issueBody: '确认签单、上户、下户与历史事件',
      brief: { id: 'brief-1', version: 1, content: '# 目标\n升级生命周期' },
      workspace: { branch: 'qianshou/issue-224', path: '/tmp/issue-224', baseSha: 'abcdef1' }
    });
    expect(input.markdown).toContain('Task brief: brief-1 v1');
    expect(input.markdown).toContain('# 目标\n升级生命周期');
    expect(input.markdown).toContain('Worktree: /tmp/issue-224');
    expect(input.markdown).not.toContain('raw discussion');
  });

  it('refuses to build Review input without a frozen candidate SHA', () => {
    expect(() => buildAgentInputPackage({
      role: 'REVIEW',
      issueNumber: 224,
      issueTitle: '服务关系生命周期',
      issueBody: '',
      brief: { id: 'brief-1', version: 1, content: '# 验收\n人工确认' },
      workspace: { branch: 'qianshou/issue-224', path: '/tmp/issue-224', baseSha: 'abcdef1' },
      priorOutput: '开发完成'
    })).toThrow(/candidate SHA/i);
  });
});
