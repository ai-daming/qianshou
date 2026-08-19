import {
  getProjectIssue,
  listMilestoneIssues,
  listProjectMilestones,
  listProjects,
} from "@qianshou/api-client";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { generatedFactsClient } from "./facts.js";

vi.mock("@qianshou/api-client", () => ({
  getProjectIssue: vi.fn(),
  listMilestoneIssues: vi.fn(),
  listProjectMilestones: vi.fn(),
  listProjects: vi.fn(),
}));

const response = new Response();
const request = new Request("http://localhost");

describe("generated OpenAPI client adapter", () => {
  beforeEach(() => vi.clearAllMocks());

  it("uses the same-origin Vite proxy and throws API failures", async () => {
    vi.mocked(listProjects).mockResolvedValue({ data: { projects: [] }, request, response });

    await expect(generatedFactsClient.listProjects()).resolves.toEqual({ projects: [] });
    expect(listProjects).toHaveBeenCalledWith({
      baseUrl: "",
      throwOnError: true,
    });
  });

  it("passes Project and Scope identities only through generated path parameters", async () => {
    vi.mocked(listProjectMilestones).mockResolvedValue({
      data: { projectId: "qianshou", milestones: [] },
      request,
      response,
    });
    vi.mocked(listMilestoneIssues).mockResolvedValue({
      data: { projectId: "qianshou", milestoneNumber: 1, issues: [] },
      request,
      response,
    });
    vi.mocked(getProjectIssue).mockResolvedValue({
      data: {
        projectId: "qianshou",
        issue: {
          number: 31,
          title: "Scope UI",
          state: "OPEN",
          labels: [],
          dependency: { status: "READY" },
        },
      },
      request,
      response,
    });

    await generatedFactsClient.listMilestones("qianshou");
    await generatedFactsClient.listMilestoneIssues("qianshou", 1);
    await generatedFactsClient.getIssue("qianshou", 31);

    const common = { baseUrl: "", throwOnError: true };
    expect(listProjectMilestones).toHaveBeenCalledWith({
      ...common,
      path: { projectId: "qianshou" },
    });
    expect(listMilestoneIssues).toHaveBeenCalledWith({
      ...common,
      path: { projectId: "qianshou", milestoneNumber: 1 },
    });
    expect(getProjectIssue).toHaveBeenCalledWith({
      ...common,
      path: { projectId: "qianshou", issueNumber: 31 },
    });
  });
});
