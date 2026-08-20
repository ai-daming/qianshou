import {
  adoptDeliveryBaseline,
  cancelDiscussionRun,
  createBriefVersion,
  createDiscussionConversation,
  establishRunnerBinding,
  getIssueWorkspace,
  listDiscussionRunEvents,
  resolveStopCondition,
  startDiscussionRun,
  type AdoptDeliveryBaselineRequest,
  type AgentRun,
  type BriefVersion,
  type CancelRunResponse,
  type Conversation,
  type CreateBriefVersionRequest,
  type CreateConversationRequest,
  type DeliveryBaseline,
  type IssueWorkspace,
  type ResolveStopConditionRequest,
  type RunEventPage,
  type RunnerBinding,
  type StartDiscussionRunRequest,
  type StopCondition,
} from "@qianshou/api-client";

export interface WorkflowClient {
  getWorkspace(projectId: string, issueNumber: number): Promise<IssueWorkspace>;
  establishBinding(projectId: string, mainCheckoutPath: string): Promise<RunnerBinding>;
  createConversation(
    projectId: string,
    issueNumber: number,
    request: CreateConversationRequest,
  ): Promise<Conversation>;
  startRun(
    projectId: string,
    issueNumber: number,
    conversationId: string,
    request: StartDiscussionRunRequest,
  ): Promise<AgentRun>;
  cancelRun(projectId: string, issueNumber: number, runId: string): Promise<CancelRunResponse>;
  listRunEvents(
    projectId: string,
    issueNumber: number,
    runId: string,
    after?: number,
    limit?: number,
  ): Promise<RunEventPage>;
  createBrief(
    projectId: string,
    issueNumber: number,
    request: CreateBriefVersionRequest,
  ): Promise<BriefVersion>;
  adoptBaseline(
    projectId: string,
    issueNumber: number,
    request: AdoptDeliveryBaselineRequest,
  ): Promise<DeliveryBaseline>;
  resolveStop(
    projectId: string,
    issueNumber: number,
    stopId: string,
    request: ResolveStopConditionRequest,
  ): Promise<StopCondition>;
}

const browserOptions = {
  baseUrl: "",
  throwOnError: true as const,
};

export const generatedWorkflowClient: WorkflowClient = {
  getWorkspace: async (projectId, issueNumber) =>
    (
      await getIssueWorkspace({
        ...browserOptions,
        path: { projectId, issueNumber },
      })
    ).data,
  establishBinding: async (projectId, mainCheckoutPath) =>
    (
      await establishRunnerBinding({
        ...browserOptions,
        path: { projectId },
        body: { mainCheckoutPath },
      })
    ).data,
  createConversation: async (projectId, issueNumber, request) =>
    (
      await createDiscussionConversation({
        ...browserOptions,
        path: { projectId, issueNumber },
        body: request,
      })
    ).data,
  startRun: async (projectId, issueNumber, conversationId, request) =>
    (
      await startDiscussionRun({
        ...browserOptions,
        path: { projectId, issueNumber, conversationId },
        body: request,
      })
    ).data,
  cancelRun: async (projectId, issueNumber, runId) =>
    (
      await cancelDiscussionRun({
        ...browserOptions,
        path: { projectId, issueNumber, runId },
      })
    ).data,
  listRunEvents: async (projectId, issueNumber, runId, after = 0, limit = 1000) =>
    (
      await listDiscussionRunEvents({
        ...browserOptions,
        path: { projectId, issueNumber, runId },
        query: { after, limit },
      })
    ).data,
  createBrief: async (projectId, issueNumber, request) =>
    (
      await createBriefVersion({
        ...browserOptions,
        path: { projectId, issueNumber },
        body: request,
      })
    ).data,
  adoptBaseline: async (projectId, issueNumber, request) =>
    (
      await adoptDeliveryBaseline({
        ...browserOptions,
        path: { projectId, issueNumber },
        body: request,
      })
    ).data,
  resolveStop: async (projectId, issueNumber, stopId, request) =>
    (
      await resolveStopCondition({
        ...browserOptions,
        path: { projectId, issueNumber, stopId },
        body: request,
      })
    ).data,
};
