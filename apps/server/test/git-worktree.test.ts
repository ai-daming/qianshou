import { execFile } from "node:child_process";
import { mkdtemp, mkdir, realpath, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";
import type { ProjectConfig } from "@qianshou/core";
import { createGitWorktree } from "../src/server.js";

const execFileAsync = promisify(execFile);

async function git(cwd: string, ...args: string[]) {
  const { stdout } = await execFileAsync("git", args, { cwd });
  return stdout.trim();
}

describe("Git-native Issue worktree creation", () => {
  it("creates an Issue branch and worktree from the configured Milestone integration branch", async () => {
    const directory = await mkdtemp(join(tmpdir(), "qianshou-git-worktree-"));
    const repository = join(directory, "repository");
    const integrationWorktree = join(directory, "worktrees", "milestone-7");
    await mkdir(repository, { recursive: true });

    try {
      await git(repository, "init", "-b", "main");
      await git(repository, "config", "user.name", "Qianshou Test");
      await git(repository, "config", "user.email", "qianshou@example.invalid");
      await writeFile(join(repository, "README.md"), "baseline\n");
      await git(repository, "add", "README.md");
      await git(repository, "commit", "-m", "baseline");
      await mkdir(dirname(integrationWorktree), { recursive: true });
      await git(
        repository,
        "worktree",
        "add",
        "-b",
        "codex/milestone-7-poster-engine-baseline",
        integrationWorktree,
        "main",
      );

      const project: ProjectConfig = {
        id: "demo",
        repository: { slug: "owner/repo", path: repository },
        milestone: { number: 7 },
        integration: {
          branch: "codex/milestone-7-poster-engine-baseline",
          worktree: integrationWorktree,
          baseBranch: "main",
        },
        refreshSeconds: 30,
        defaults: { developerEngine: "codex", reviewerEngine: "claude" },
      };

      const workspace = await createGitWorktree(project, 224);

      expect(workspace.branch).toBe("codex/m7-issue-224");
      expect(workspace.path).toBe(await realpath(join(directory, "worktrees", "m7-issue-224")));
      expect(workspace.baseBranch).toBe(project.integration.branch);
      expect(await git(workspace.path, "branch", "--show-current")).toBe(workspace.branch);
      expect(await git(workspace.path, "rev-parse", "HEAD")).toBe(workspace.baseSha);
      expect(await git(repository, "worktree", "list", "--porcelain")).toContain(
        `worktree ${workspace.path}`,
      );
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
});
