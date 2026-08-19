import {
  getProjectIssue,
  listMilestoneIssues,
  listProjectMilestones,
  listProjects,
  type IssueResponse,
  type MilestoneIssuesResponse,
  type MilestonesResponse,
  type ProjectsResponse,
} from "@qianshou/api-client";

export interface FactsClient {
  listProjects(): Promise<ProjectsResponse>;
  listMilestones(projectId: string): Promise<MilestonesResponse>;
  listMilestoneIssues(projectId: string, milestoneNumber: number): Promise<MilestoneIssuesResponse>;
  getIssue(projectId: string, issueNumber: number): Promise<IssueResponse>;
}

const browserOptions = {
  baseUrl: "",
  throwOnError: true as const,
};

export const generatedFactsClient: FactsClient = {
  listProjects: async () => (await listProjects(browserOptions)).data,
  listMilestones: async (projectId) =>
    (
      await listProjectMilestones({
        ...browserOptions,
        path: { projectId },
      })
    ).data,
  listMilestoneIssues: async (projectId, milestoneNumber) =>
    (
      await listMilestoneIssues({
        ...browserOptions,
        path: { projectId, milestoneNumber },
      })
    ).data,
  getIssue: async (projectId, issueNumber) =>
    (
      await getProjectIssue({
        ...browserOptions,
        path: { projectId, issueNumber },
      })
    ).data,
};
