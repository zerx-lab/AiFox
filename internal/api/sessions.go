// Sessions API — exposes the rollups produced by internal/session.
//
// SSE: the existing /v1/traffic/stream broadcasts an extra event ("session")
// each time the aggregator updates so the renderer can keep its sidebar
// fresh without polling. See registerTrafficStream for the wiring.

package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/zerx-lab/ai-fox/internal/session"
)

// SessionSummaryBody is the on-wire shape. Keep field set in sync with
// session.Summary so the renderer doesn't see a different schema.
type SessionSummaryBody struct {
	ID            string    `json:"id" doc:"Opaque session ID."`
	Name          string    `json:"name,omitempty" doc:"User-supplied label. Empty when the user has not renamed this session."`
	Fingerprint   string    `json:"fingerprint" doc:"Hash of the conversation anchor."`
	Provider      string    `json:"provider" doc:"anthropic | openai | gemini"`
	Model         string    `json:"model,omitempty" doc:"Primary (most recent non-utility) model."`
	Models        []string  `json:"models,omitempty" doc:"Every distinct model used, primary first."`
	EntryIDs      []string  `json:"entryIds" doc:"Captured traffic entries belonging to this session, oldest first."`
	StartedAt     time.Time `json:"startedAt"`
	LastAt        time.Time `json:"lastAt"`
	TurnCount     int       `json:"turnCount"`
	InputTokens   int       `json:"inputTokens"`
	OutputTokens  int       `json:"outputTokens"`
	CacheRead     int       `json:"cacheRead"`
	CacheCreate   int       `json:"cacheCreate"`
	Status        string    `json:"status" enum:"active,completed,failed"`
	HasError      bool      `json:"hasError"`
	HasStreaming  bool      `json:"hasStreaming"`
	HasUnfinished bool      `json:"hasUnfinished"`
}

func toSessionBody(s *session.Summary) SessionSummaryBody {
	return SessionSummaryBody{
		ID:            s.ID,
		Name:          s.Name,
		Fingerprint:   s.Fingerprint,
		Provider:      s.Provider,
		Model:         s.Model,
		Models:        s.Models,
		EntryIDs:      s.EntryIDs,
		StartedAt:     s.StartedAt,
		LastAt:        s.LastAt,
		TurnCount:     s.TurnCount,
		InputTokens:   s.InputTokens,
		OutputTokens:  s.OutputTokens,
		CacheRead:     s.CacheRead,
		CacheCreate:   s.CacheCreate,
		Status:        s.Status,
		HasError:      s.HasError,
		HasStreaming:  s.HasStreaming,
		HasUnfinished: s.HasUnfinished,
	}
}

type ListSessionsOutput struct {
	Body struct {
		Items []SessionSummaryBody `json:"items"`
	}
}

func registerSessions(api huma.API, agg *session.Aggregator) {
	if agg == nil {
		return
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-sessions",
		Method:      http.MethodGet,
		Path:        "/v1/sessions",
		Summary:     "List aggregated sessions, newest first.",
		Tags:        []string{"sessions"},
	}, func(_ context.Context, _ *struct{}) (*ListSessionsOutput, error) {
		out := &ListSessionsOutput{}
		for _, s := range agg.List() {
			out.Body.Items = append(out.Body.Items, toSessionBody(s))
		}
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-session",
		Method:      http.MethodGet,
		Path:        "/v1/sessions/{id}",
		Summary:     "Fetch one session by id.",
		Tags:        []string{"sessions"},
	}, func(_ context.Context, in *struct {
		ID string `path:"id" maxLength:"64"`
	}) (*struct{ Body SessionSummaryBody }, error) {
		s, ok := agg.Get(in.ID)
		if !ok {
			return nil, huma.Error404NotFound("no such session")
		}
		return &struct{ Body SessionSummaryBody }{Body: toSessionBody(s)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "rename-session",
		Method:      http.MethodPatch,
		Path:        "/v1/sessions/{id}",
		Summary:     "Rename a session. An empty name clears the label.",
		Tags:        []string{"sessions"},
	}, func(_ context.Context, in *struct {
		ID   string `path:"id" maxLength:"64"`
		Body struct {
			Name string `json:"name" maxLength:"128" doc:"Display label; empty string clears the label."`
		}
	}) (*struct{ Body SessionSummaryBody }, error) {
		name := strings.TrimSpace(in.Body.Name)
		if err := agg.SetName(in.ID, name); err != nil {
			if errors.Is(err, session.ErrNotFound) {
				return nil, huma.Error404NotFound("no such session")
			}
			return nil, huma.Error500InternalServerError("rename failed", err)
		}
		s, ok := agg.Get(in.ID)
		if !ok {
			return nil, huma.Error404NotFound("no such session")
		}
		return &struct{ Body SessionSummaryBody }{Body: toSessionBody(s)}, nil
	})
}
