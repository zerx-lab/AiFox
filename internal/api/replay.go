// Replay endpoint — re-issue a previously captured request with optional
// parameter overrides. Returns the new entry's id; the SSE stream will
// surface it like any other entry.

package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// ReplayInput carries the original entry id and the user-selected overrides.
// All override fields are optional; absent fields mean "leave the original
// value alone". JSON shape uses snake_case under "overrides" to stay
// readable against the underlying provider bodies.
type ReplayInput struct {
	ID   string `path:"id" maxLength:"64"`
	Body struct {
		Overrides ReplayOverridesBody `json:"overrides"`
	}
}

// ReplayOverridesBody is the on-wire shape of the override knobs. We keep
// it separate from the internal ReplayOverrides so the OpenAPI schema is
// self-documenting (pointer fields can't carry doc tags).
type ReplayOverridesBody struct {
	Model       *string  `json:"model,omitempty"        doc:"Override the upstream model identifier."`
	Temperature *float64 `json:"temperature,omitempty"  doc:"Override sampling temperature."`
	TopP        *float64 `json:"topP,omitempty"         doc:"Override nucleus sampling threshold."`
	TopK        *int     `json:"topK,omitempty"         doc:"Override top-k sampling."`
	MaxTokens   *int     `json:"maxTokens,omitempty"    doc:"Override max output tokens."`
	Stream      *bool    `json:"stream,omitempty"       doc:"Force streaming on / off."`
}

type ReplayOutput struct {
	Body struct {
		EntryID string `json:"entryId" doc:"ID of the newly captured entry."`
	}
}

func registerReplay(api huma.API, rep Replayer) {
	if rep == nil {
		return
	}
	huma.Register(api, huma.Operation{
		OperationID: "replay-entry",
		Method:      http.MethodPost,
		Path:        "/v1/traffic/{id}/replay",
		Summary:     "Re-issue a captured request with optional overrides.",
		Tags:        []string{"traffic"},
	}, func(ctx context.Context, in *ReplayInput) (*ReplayOutput, error) {
		overrides := ReplayOverrides{
			Model:       in.Body.Overrides.Model,
			Temperature: in.Body.Overrides.Temperature,
			TopP:        in.Body.Overrides.TopP,
			TopK:        in.Body.Overrides.TopK,
			MaxTokens:   in.Body.Overrides.MaxTokens,
			Stream:      in.Body.Overrides.Stream,
		}
		newID, err := rep.Replay(ctx, in.ID, overrides)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		out := &ReplayOutput{}
		out.Body.EntryID = newID
		return out, nil
	})
}
