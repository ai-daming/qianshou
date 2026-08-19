// Package server hosts the central Qianshou control server. Per ADR 0001
// it is the sole owner of SQLite and the versioned HTTP/JSON API defined
// by protocol/openapi.yaml. This skeleton proves the binary shape and the
// CI pipeline only; the domain arrives with the M1 delivery issues.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ai-daming/qianshou/apps/control/internal/config"
	"github.com/ai-daming/qianshou/apps/control/internal/deps"
	"github.com/ai-daming/qianshou/apps/control/internal/githubapi"
)

const githubFactsTimeout = 90 * time.Second

const githubFactsDeadlineMessage = "Current GitHub facts could not be read completely before the request deadline."

func Serve(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := flags.String("addr", "127.0.0.1:41727", "listen address")
	configPath := flags.String("config", config.DefaultPath(), "Qianshou config path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := validateListenAddress(*addr); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
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
	server := newHTTPServer(handler(cfg, githubapi.New(token)))
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

type factsReader interface {
	ListMilestones(context.Context, string) ([]githubapi.Milestone, error)
	ListMilestoneIssues(context.Context, string, int) ([]githubapi.Issue, error)
	GetIssue(context.Context, string, int) (githubapi.Issue, error)
}

func handler(cfg config.Config, facts factsReader) http.Handler {
	return handlerWithFactsTimeout(cfg, facts, githubFactsTimeout)
}

func handlerWithFactsTimeout(cfg config.Config, facts factsReader, factsTimeout time.Duration) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		projects := make([]publicProject, 0, len(cfg.Projects))
		for _, project := range cfg.Projects {
			projects = append(projects, publicProject{
				ID:         project.ID,
				Repository: publicRepository{Provider: project.Repository.Provider, Slug: project.Repository.Slug},
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"projects": projects})
	})
	mux.HandleFunc("GET /api/v1/projects/{projectID}/milestones", func(w http.ResponseWriter, r *http.Request) {
		project, ok := findProject(cfg, r.PathValue("projectID"))
		if !ok {
			writeError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "The configured Project was not found.")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), factsTimeout)
		defer cancel()
		milestones, err := facts.ListMilestones(ctx, project.Repository.Slug)
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
		project, ok := findProject(cfg, r.PathValue("projectID"))
		if !ok {
			writeError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "The configured Project was not found.")
			return
		}
		milestone, err := positiveNumber(r.PathValue("milestoneNumber"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_MILESTONE_NUMBER", "Milestone number must be a positive integer.")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), factsTimeout)
		defer cancel()
		issues, err := facts.ListMilestoneIssues(ctx, project.Repository.Slug, milestone)
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
		project, ok := findProject(cfg, r.PathValue("projectID"))
		if !ok {
			writeError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "The configured Project was not found.")
			return
		}
		issueNumber, err := positiveNumber(r.PathValue("issueNumber"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_ISSUE_NUMBER", "Issue number must be a positive integer.")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), factsTimeout)
		defer cancel()
		issue, err := facts.GetIssue(ctx, project.Repository.Slug, issueNumber)
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

type publicProject struct {
	ID         string           `json:"id"`
	Repository publicRepository `json:"repository"`
}

type publicRepository struct {
	Provider string `json:"provider"`
	Slug     string `json:"slug"`
}

func findProject(cfg config.Config, id string) (config.Project, bool) {
	for _, project := range cfg.Projects {
		if project.ID == id {
			return project, true
		}
	}
	return config.Project{}, false
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
