package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/ai-daming/qianshou/apps/control/internal/githubapi"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	legacyrouter "github.com/getkin/kin-openapi/routers/legacy"
)

func loadOpenAPIContract(t *testing.T) routers.Router {
	t.Helper()
	contract := filepath.Join("..", "..", "..", "..", "protocol", "openapi.yaml")
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(contract)
	if err != nil {
		t.Fatalf("load OpenAPI contract: %v", err)
	}
	if err := doc.Validate(t.Context()); err != nil {
		t.Fatalf("validate OpenAPI contract: %v", err)
	}
	router, err := legacyrouter.NewRouter(doc)
	if err != nil {
		t.Fatalf("build OpenAPI router: %v", err)
	}
	return router
}

func validateRecordedResponse(ctx context.Context, router routers.Router, req *http.Request, rr *httptest.ResponseRecorder) error {
	route, pathParams, err := router.FindRoute(req)
	if err != nil {
		return err
	}
	input := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{
			Request:    req,
			PathParams: pathParams,
			Route:      route,
		},
		Status:  rr.Code,
		Header:  rr.Header(),
		Options: &openapi3filter.Options{IncludeResponseStatus: true},
	}
	input.SetBodyBytes(rr.Body.Bytes())
	return openapi3filter.ValidateResponse(ctx, input)
}

func TestOpenAPIValidatesEveryDeclaredHandlerResponse(t *testing.T) {
	ready := githubapi.Dependency{Status: githubapi.DependencyReady}
	blocked := githubapi.Dependency{Status: githubapi.DependencyBlocked, BlockedBy: []int{35}}
	dependencyError := githubapi.Dependency{
		Status: githubapi.DependencyError,
		Error: &githubapi.DependencyErrorDetail{
			Code:    "DEPENDENCY_FACTS_UNAVAILABLE",
			Message: "GitHub dependency facts are incomplete or unavailable.",
		},
	}
	successFacts := func() *fakeFacts {
		return &fakeFacts{
			milestones: []githubapi.Milestone{{Number: 1, Title: "M1", State: "OPEN"}},
			issues: []githubapi.Issue{
				{Number: 34, Title: "Ready", State: "OPEN", Labels: []string{}, Dependency: ready},
				{Number: 35, Title: "Blocked", State: "OPEN", Labels: []string{"type:technical"}, Dependency: blocked},
				{Number: 36, Title: "Unknown", State: "OPEN", Labels: []string{}, Dependency: dependencyError},
			},
			issue: githubapi.Issue{Number: 36, Title: "Issue", State: "OPEN", Labels: []string{}, Dependency: dependencyError},
		}
	}
	failingFacts := func() *fakeFacts { return &fakeFacts{err: errors.New("facts unavailable")} }

	tests := []struct {
		name        string
		path        string
		operationID string
		facts       *fakeFacts
		status      int
	}{
		{name: "health 200", path: "/healthz", operationID: "getHealth", facts: successFacts(), status: http.StatusOK},
		{name: "projects 200", path: "/api/v1/projects", operationID: "listProjects", facts: successFacts(), status: http.StatusOK},
		{name: "milestones 200", path: "/api/v1/projects/qianshou/milestones", operationID: "listProjectMilestones", facts: successFacts(), status: http.StatusOK},
		{name: "milestones 404", path: "/api/v1/projects/missing/milestones", operationID: "listProjectMilestones", facts: successFacts(), status: http.StatusNotFound},
		{name: "milestones 502", path: "/api/v1/projects/qianshou/milestones", operationID: "listProjectMilestones", facts: failingFacts(), status: http.StatusBadGateway},
		{name: "milestone issues 200", path: "/api/v1/projects/qianshou/milestones/1/issues", operationID: "listMilestoneIssues", facts: successFacts(), status: http.StatusOK},
		{name: "milestone issues 400", path: "/api/v1/projects/qianshou/milestones/invalid/issues", operationID: "listMilestoneIssues", facts: successFacts(), status: http.StatusBadRequest},
		{name: "milestone issues 404", path: "/api/v1/projects/missing/milestones/1/issues", operationID: "listMilestoneIssues", facts: successFacts(), status: http.StatusNotFound},
		{name: "milestone issues 502", path: "/api/v1/projects/qianshou/milestones/1/issues", operationID: "listMilestoneIssues", facts: failingFacts(), status: http.StatusBadGateway},
		{name: "issue 200", path: "/api/v1/projects/qianshou/issues/36", operationID: "getProjectIssue", facts: successFacts(), status: http.StatusOK},
		{name: "issue 400", path: "/api/v1/projects/qianshou/issues/invalid", operationID: "getProjectIssue", facts: successFacts(), status: http.StatusBadRequest},
		{name: "issue 404", path: "/api/v1/projects/missing/issues/36", operationID: "getProjectIssue", facts: successFacts(), status: http.StatusNotFound},
		{name: "issue 502", path: "/api/v1/projects/qianshou/issues/36", operationID: "getProjectIssue", facts: failingFacts(), status: http.StatusBadGateway},
	}
	router := loadOpenAPIContract(t)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:41727"+tc.path, nil)
			route, _, err := router.FindRoute(req)
			if err != nil {
				t.Fatalf("match OpenAPI operation: %v", err)
			}
			if got := route.Operation.OperationID; got != tc.operationID {
				t.Fatalf("operationId = %q, want %q", got, tc.operationID)
			}
			rr := httptest.NewRecorder()
			handler(testConfig(), tc.facts).ServeHTTP(rr, req)
			if rr.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rr.Code, tc.status, rr.Body.String())
			}
			if got := rr.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", got)
			}
			if err := validateRecordedResponse(t.Context(), router, req, rr); err != nil {
				t.Fatalf("response does not match OpenAPI: %v\n%s", err, rr.Body.String())
			}
		})
	}
}

func TestOpenAPIValidatorRejectsResponseDrift(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
	}{
		{name: "missing required field", status: http.StatusOK, contentType: "application/json", body: `{}`},
		{name: "additional field", status: http.StatusOK, contentType: "application/json", body: `{"status":"ok","extra":true}`},
		{name: "invalid enum", status: http.StatusOK, contentType: "application/json", body: `{"status":"not-ok"}`},
		{name: "undeclared status", status: http.StatusCreated, contentType: "application/json", body: `{"status":"ok"}`},
	}
	router := loadOpenAPIContract(t)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:41727/healthz", nil)
			rr := httptest.NewRecorder()
			rr.Header().Set("Content-Type", tc.contentType)
			rr.WriteHeader(tc.status)
			_, _ = rr.WriteString(tc.body)
			if err := validateRecordedResponse(t.Context(), router, req, rr); err == nil {
				t.Fatalf("schema drift was accepted: status=%d body=%s", tc.status, tc.body)
			}
		})
	}
}
