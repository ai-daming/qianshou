// Package server hosts the central Qianshou control server. It is the only
// process that opens the SQLite ledger.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ai-daming/qianshou/apps/control/internal/config"
	"github.com/ai-daming/qianshou/apps/control/internal/deps"
	"github.com/ai-daming/qianshou/apps/control/internal/githubapi"
	"github.com/ai-daming/qianshou/apps/control/internal/ledger"
	"github.com/ai-daming/qianshou/apps/control/internal/strictjson"
)

const requestBodyLimit = 64 << 10

var (
	projectIDPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	repositorySlugPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
)

const githubFactsTimeout = 90 * time.Second

const githubFactsDeadlineMessage = "Current GitHub facts could not be read completely before the request deadline."

func Serve(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := flags.String("addr", "127.0.0.1:41727", "listen address")
	configPath := flags.String("config", config.DefaultPath(), "Runner-local Qianshou config path")
	home := flags.String("home", config.DefaultHome(), "central Qianshou home")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := validateListenAddress(*addr); err != nil {
		return err
	}
	if _, err := config.Load(*configPath); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := ledger.Open(ctx, *home)
	if err != nil {
		return err
	}
	defer store.Close()
	token, err := deps.ResolveToken(ctx)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	// Loopback only. Remote operation arrives with M2-05 behind
	// authentication and TLS; that boundary is deliberate.
	server := newHTTPServer(handler(store, githubapi.New(token)))
	return server.Serve(listener)
}

func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

type projectCatalog interface {
	ListProjects(context.Context) ([]ledger.Project, error)
	GetProject(context.Context, string) (ledger.Project, error)
	CreateProject(context.Context, ledger.NewProject) (ledger.Project, error)
}

type factsReader interface {
	ResolveRepository(context.Context, string) (githubapi.Repository, error)
	GetRepositoryByID(context.Context, int64) (githubapi.Repository, error)
	ListMilestones(context.Context, string) ([]githubapi.Milestone, error)
	ListMilestoneIssues(context.Context, string, int) ([]githubapi.Issue, error)
	GetIssue(context.Context, string, int) (githubapi.Issue, error)
}

func handler(catalog projectCatalog, facts factsReader) http.Handler {
	return handlerWithFactsTimeout(catalog, facts, githubFactsTimeout)
}

func handlerWithFactsTimeout(catalog projectCatalog, facts factsReader, factsTimeout time.Duration) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		stored, err := catalog.ListProjects(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "LEDGER_UNAVAILABLE", "The central Project Catalog could not be read.")
			return
		}
		projects := make([]publicProject, 0, len(stored))
		for _, project := range stored {
			projects = append(projects, publicProjectFromLedger(project))
		}
		writeJSON(w, http.StatusOK, map[string]any{"projects": projects})
	})
	mux.HandleFunc("POST /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID             string `json:"id"`
			RepositorySlug string `json:"repositorySlug"`
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, requestBodyLimit+1))
		if err != nil || len(body) > requestBodyLimit {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "The Project request is invalid or too large.")
			return
		}
		if err := strictjson.Decode(body, &request, true); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "The Project request is invalid.")
			return
		}
		if !projectIDPattern.MatchString(request.ID) || !repositorySlugPattern.MatchString(request.RepositorySlug) {
			writeError(w, http.StatusBadRequest, "INVALID_PROJECT", "The Project ID or repository slug is invalid.")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), factsTimeout)
		defer cancel()
		repository, err := facts.ResolveRepository(ctx, request.RepositorySlug)
		if err != nil {
			writeGitHubFactsError(w, ctx, err, "The GitHub repository identity could not be resolved completely.")
			return
		}
		project, err := catalog.CreateProject(r.Context(), ledger.NewProject{
			ID: request.ID, RepositoryID: repository.ID, CreationSlug: repository.NameWithOwner,
		})
		if errors.Is(err, ledger.ErrConflict) {
			writeError(w, http.StatusConflict, "PROJECT_IDENTITY_CONFLICT", "The Project ID or GitHub repository ID is already owned.")
			return
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_PROJECT", "The Project identity is invalid.")
			return
		}
		writeJSON(w, http.StatusCreated, publicProjectFromLedger(project))
	})
	mux.HandleFunc("GET /api/v1/projects/{projectID}/milestones", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), factsTimeout)
		defer cancel()
		project, currentSlug, ok := resolveProject(w, r, ctx, catalog, facts)
		if !ok {
			return
		}
		milestones, err := facts.ListMilestones(ctx, currentSlug)
		if err != nil {
			writeGitHubFactsError(w, ctx, err, "Current GitHub Milestone facts could not be read completely.")
			return
		}
		if milestones == nil {
			milestones = []githubapi.Milestone{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"projectId": project.ID, "milestones": milestones})
	})
	mux.HandleFunc("GET /api/v1/projects/{projectID}/milestones/{milestoneNumber}/issues", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), factsTimeout)
		defer cancel()
		project, currentSlug, ok := resolveProject(w, r, ctx, catalog, facts)
		if !ok {
			return
		}
		milestone, err := positiveNumber(r.PathValue("milestoneNumber"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_MILESTONE_NUMBER", "Milestone number must be a positive integer.")
			return
		}
		issues, err := facts.ListMilestoneIssues(ctx, currentSlug, milestone)
		if err != nil {
			writeGitHubFactsError(w, ctx, err, "Current GitHub Milestone Issue facts could not be read completely.")
			return
		}
		if issues == nil {
			issues = []githubapi.Issue{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"projectId": project.ID, "milestoneNumber": milestone, "issues": issues})
	})
	mux.HandleFunc("GET /api/v1/projects/{projectID}/issues/{issueNumber}", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), factsTimeout)
		defer cancel()
		project, currentSlug, ok := resolveProject(w, r, ctx, catalog, facts)
		if !ok {
			return
		}
		issueNumber, err := positiveNumber(r.PathValue("issueNumber"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_ISSUE_NUMBER", "Issue number must be a positive integer.")
			return
		}
		issue, err := facts.GetIssue(ctx, currentSlug, issueNumber)
		if err != nil {
			writeGitHubFactsError(w, ctx, err, "Current GitHub Issue facts could not be read completely.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"projectId": project.ID, "issue": issue})
	})
	return mux
}

func writeGitHubFactsError(w http.ResponseWriter, ctx context.Context, err error, fallbackMessage string) {
	message := fallbackMessage
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		message = githubFactsDeadlineMessage
	}
	writeError(w, http.StatusBadGateway, "GITHUB_FACTS_UNAVAILABLE", message)
}

func resolveProject(w http.ResponseWriter, r *http.Request, factsContext context.Context, catalog projectCatalog, facts factsReader) (ledger.Project, string, bool) {
	project, err := catalog.GetProject(r.Context(), r.PathValue("projectID"))
	if errors.Is(err, ledger.ErrNotFound) || project.ArchivedAt != nil {
		writeError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "The central Project was not found.")
		return ledger.Project{}, "", false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LEDGER_UNAVAILABLE", "The central Project Catalog could not be read.")
		return ledger.Project{}, "", false
	}
	repository, err := facts.GetRepositoryByID(factsContext, project.RepositoryID)
	if err != nil || repository.ID != project.RepositoryID {
		writeGitHubFactsError(w, factsContext, err, "Current GitHub repository identity could not be verified.")
		return ledger.Project{}, "", false
	}
	return project, repository.NameWithOwner, true
}

type publicProject struct {
	ID         string           `json:"id"`
	Repository publicRepository `json:"repository"`
}

type publicRepository struct {
	Provider     string `json:"provider"`
	ID           int64  `json:"id"`
	CreationSlug string `json:"creationSlug"`
}

func publicProjectFromLedger(project ledger.Project) publicProject {
	return publicProject{ID: project.ID, Repository: publicRepository{
		Provider: "github", ID: project.RepositoryID, CreationSlug: project.CreationSlug,
	}}
}

func positiveNumber(raw string) (int, error) {
	number, err := strconv.Atoi(raw)
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("not a positive integer")
	}
	return number, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func validateListenAddress(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || strings.TrimSpace(port) == "" {
		return fmt.Errorf("serve address must be an IP and port on loopback")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("serve address must use a loopback IP")
	}
	return nil
}
