import { describe, expect, it } from "vitest";
import {
  controlPatchSchema,
  createStopConditionSchema,
  deriveIssueState,
  deriveSlots,
  dependencyState,
  issueKindFromGithub,
  nextAction,
  projectConfigSchema,
  resolveStopConditionSchema,
  shellCommand,
  type GithubIssue,
  type IssueControl,
  type ProjectConfig,
  type IssueDescriptor,
} from "../src/index.js";

const projectIssue: IssueDescriptor = { number: 19, label: "签单喜报", kind: "delivery" };
const githubIssue = {
  number: 19,
  state: "OPEN",
  blockedBy: [{ number: 224, state: "OPEN" }],
} as GithubIssue;
const control = { phase: "PLANNED", candidateSha: null } as IssueControl;
const project = {
  repository: { path: "/tmp/repo" },
  integration: { branch: "milestone-7" },
} as ProjectConfig;

describe("delivery domain", () => {
  it("keeps a planned Issue blocked until the GitHub dependency closes", () => {
    expect(dependencyState(githubIssue)).toEqual({ ready: false, blockers: [224] });
    expect(deriveIssueState({ projectIssue, githubIssue, control })).toMatchObject({
      key: "WAITING_DEPENDENCY",
      blockers: [224],
    });
    expect(nextAction({ project, projectIssue, githubIssue, control })).toMatchObject({
      label: "等待前置 Issue",
      command: null,
      shellCommand: null,
    });
    expect(deriveSlots(control, false).developerStatus).toBe("LOCKED");
  });

  it("locks every new delivery slot while a GitHub dependency remains open", () => {
    expect(deriveSlots({ ...control, phase: "WORKTREE_READY" }, false)).toEqual({
      developerStatus: "LOCKED",
      reviewerStatus: "LOCKED",
    });
    expect(
      deriveSlots({ ...control, phase: "CANDIDATE_READY", candidateSha: "abcdef1" }, false),
    ).toEqual({
      developerStatus: "LOCKED",
      reviewerStatus: "LOCKED",
    });
  });

  it("keeps manual phase distinct from GitHub state", () => {
    expect(
      deriveIssueState({ projectIssue, githubIssue, control: { ...control, phase: "REVIEWING" } })
        .key,
    ).toBe("REVIEWING");
  });

  it("pauses delivery for discussion without replacing the preserved delivery phase", () => {
    const reviewing = { ...control, phase: "REVIEWING", candidateSha: "abcdef1" } as IssueControl;
    expect(
      deriveIssueState({
        projectIssue,
        githubIssue,
        control: reviewing,
        openStopConditionCount: 1,
      }),
    ).toMatchObject({
      key: "NEEDS_DISCUSSION",
      label: "需要讨论",
    });
    expect(reviewing.phase).toBe("REVIEWING");
    expect(deriveSlots(reviewing, true, true)).toEqual({
      developerStatus: "PAUSED",
      reviewerStatus: "PAUSED",
    });
    expect(
      nextAction({
        project,
        projectIssue,
        githubIssue,
        control: reviewing,
        openStopConditionCount: 1,
      }),
    ).toMatchObject({
      label: "回到 Discussion",
      command: null,
    });
  });

  it("accepts only connection/runtime configuration and rejects duplicated GitHub facts", () => {
    const minimalConfig = {
      id: "demo",
      repository: { slug: "owner/repo", path: "/tmp/repo" },
      milestone: { number: 7 },
      integration: { branch: "milestone", worktree: "/tmp/repo-m7", baseBranch: "main" },
      refreshSeconds: 30,
      defaults: { developerEngine: "codex", reviewerEngine: "claude" },
    };
    expect(projectConfigSchema.safeParse(minimalConfig).success).toBe(true);
    expect(
      projectConfigSchema.safeParse({
        ...minimalConfig,
        issues: [{ number: 19, kind: "delivery" }],
      }).success,
    ).toBe(false);
    expect(
      projectConfigSchema.safeParse({ ...minimalConfig, name: "Demo", subtitle: "M7" }).success,
    ).toBe(false);
    expect(
      projectConfigSchema.safeParse({
        ...minimalConfig,
        milestone: { number: 7, title: "Demo M7" },
      }).success,
    ).toBe(false);
  });

  it("derives the Control Issue role from the native GitHub label", () => {
    expect(
      issueKindFromGithub({ labels: [{ name: "type:milestone-control", color: "000000" }] }),
    ).toBe("control");
    expect(issueKindFromGithub({ labels: [{ name: "enhancement", color: "000000" }] })).toBe(
      "delivery",
    );
  });

  it("classifies OPERATION labels as operation instead of delivery", () => {
    expect(issueKindFromGithub({ labels: [{ name: "workflow:operation", color: "000000" }] })).toBe(
      "operation",
    );
    expect(issueKindFromGithub({ labels: [{ name: "type:operation", color: "000000" }] })).toBe(
      "operation",
    );
  });

  it("does not route OPERATION issues into the delivery state machine", () => {
    const operationIssue = { ...projectIssue, kind: "operation" } as IssueDescriptor;
    expect(deriveIssueState({ projectIssue: operationIssue, githubIssue, control })).toMatchObject({
      key: "OPERATION",
    });
    expect(
      nextAction({
        project,
        projectIssue: operationIssue,
        githubIssue: { ...githubIssue, blockedBy: [] },
        control,
      }),
    ).toMatchObject({ command: null, shellCommand: null });
    expect(deriveSlots(control, true, false, "operation")).toEqual({
      developerStatus: "LOCKED",
      reviewerStatus: "LOCKED",
    });
  });

  it("does not offer delivery actions for CONTROL issues", () => {
    const controlIssue = { ...projectIssue, kind: "control" } as IssueDescriptor;
    const action = nextAction({
      project,
      projectIssue: controlIssue,
      githubIssue: { ...githubIssue, blockedBy: [] },
      control,
    });
    expect(action.label).not.toContain("worktree");
    expect(action).toMatchObject({ command: null, shellCommand: null });
    expect(deriveSlots(control, true, false, "control")).toEqual({
      developerStatus: "LOCKED",
      reviewerStatus: "LOCKED",
    });
  });

  it("locks Reviewer until a candidate is frozen", () => {
    expect(deriveSlots({ ...control, phase: "WORKTREE_READY" }).reviewerStatus).toBe("LOCKED");
    expect(
      deriveSlots({ ...control, phase: "CANDIDATE_READY", candidateSha: "abcdef1" }).reviewerStatus,
    ).toBe("READY");
  });

  it("validates control payloads at the boundary", () => {
    expect(
      controlPatchSchema.parse({
        phase: "CANDIDATE_READY",
        candidateSha: "abcdef1",
        reviewerEngine: "claude",
      }),
    ).toEqual({ phase: "CANDIDATE_READY", candidateSha: "abcdef1", reviewerEngine: "claude" });
    expect(() => controlPatchSchema.parse({ candidateSha: "not-a-sha" })).toThrow(/candidateSha/);
    expect(() => controlPatchSchema.parse({ phase: "DONE" })).toThrow();
  });

  it("validates structured Stop Conditions and explicit resolution outcomes", () => {
    expect(
      createStopConditionSchema.parse({
        category: "SCOPE_CHANGE",
        summary: "当前 Issue 的范围不足以承载新发现的业务规则",
        detail: "需要讨论拆分 Issue 或提升为新的 Milestone。",
      }),
    ).toMatchObject({ category: "SCOPE_CHANGE" });
    expect(
      resolveStopConditionSchema.parse({
        resolution: "拆出基础状态机 Issue，当前 Issue 保持等待。",
        outcome: "SPLIT_ISSUE",
      }),
    ).toMatchObject({ outcome: "SPLIT_ISSUE" });
    expect(() =>
      resolveStopConditionSchema.parse({ resolution: "", outcome: "CONTINUE" }),
    ).toThrow();
  });

  it("quotes unsafe shell arguments", () => {
    expect(shellCommand(["git", "worktree", "add", "--detach", "it's-ready"])).toBe(
      "git worktree add --detach 'it'\\''s-ready'",
    );
  });
});
