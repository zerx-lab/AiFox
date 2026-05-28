// Breakpoint API — surfaces internal/proxy.Registry to the renderer.
//
// CRUD on breakpoint definitions plus the two affordances on held requests
// (continue / abort). The SSE stream additionally broadcasts a "breakpoints"
// event whenever the registry changes so the UI can keep its list fresh.

package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// BreakpointController is the surface api/ needs from internal/proxy. Like
// ProxyController, defined here to dodge an import cycle.
type BreakpointController interface {
	List() []Breakpoint
	Add(Breakpoint) (Breakpoint, error)
	Update(id string, enabled bool) error
	Delete(id string)
	PausedSnapshot() []Paused
	Continue(entryID string) error
	Abort(entryID string) error
	// Subscribe returns a channel that fires after any breakpoint or paused-
	// request change. The SSE stream uses this to rebroadcast.
	Subscribe() (<-chan struct{}, func())
}

// Breakpoint is the wire shape. Mirrors proxy.Breakpoint field-for-field;
// using a separate struct keeps OpenAPI authoritative for the schema.
type Breakpoint struct {
	ID      string `json:"id"`
	Match   string `json:"match" enum:"endpoint,path" doc:"How the pattern is matched."`
	Pattern string `json:"pattern" doc:"Match pattern, e.g. \"POST /v1/messages\" or \"/v1/messages\"."`
	Enabled bool   `json:"enabled"`
}

// Paused is the wire shape of one held request.
type Paused struct {
	EntryID      string    `json:"entryId"`
	BreakpointID string    `json:"breakpointId"`
	Method       string    `json:"method"`
	URL          string    `json:"url"`
	PausedAt     time.Time `json:"pausedAt"`
}

type ListBreakpointsOutput struct {
	Body struct {
		Items  []Breakpoint `json:"items"`
		Paused []Paused     `json:"paused"`
	}
}

type CreateBreakpointInput struct {
	Body struct {
		Match   string `json:"match" enum:"endpoint,path"`
		Pattern string `json:"pattern" minLength:"1"`
		Enabled bool   `json:"enabled"`
	}
}

type CreateBreakpointOutput struct {
	Body Breakpoint
}

type UpdateBreakpointInput struct {
	ID   string `path:"id" maxLength:"64"`
	Body struct {
		Enabled bool `json:"enabled"`
	}
}

type DeleteBreakpointInput struct {
	ID string `path:"id" maxLength:"64"`
}

type ResolvePausedInput struct {
	EntryID string `path:"entryId" maxLength:"64"`
}

func registerBreakpoints(api huma.API, bp BreakpointController) {
	if bp == nil {
		return
	}

	huma.Register(api, huma.Operation{
		OperationID: "list-breakpoints",
		Method:      http.MethodGet,
		Path:        "/v1/breakpoints",
		Summary:     "List configured breakpoints and currently-paused requests.",
		Tags:        []string{"breakpoints"},
	}, func(_ context.Context, _ *struct{}) (*ListBreakpointsOutput, error) {
		out := &ListBreakpointsOutput{}
		out.Body.Items = bp.List()
		out.Body.Paused = bp.PausedSnapshot()
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-breakpoint",
		Method:      http.MethodPost,
		Path:        "/v1/breakpoints",
		Summary:     "Add a breakpoint.",
		Tags:        []string{"breakpoints"},
	}, func(_ context.Context, in *CreateBreakpointInput) (*CreateBreakpointOutput, error) {
		created, err := bp.Add(Breakpoint{
			Match:   in.Body.Match,
			Pattern: in.Body.Pattern,
			Enabled: in.Body.Enabled,
		})
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		return &CreateBreakpointOutput{Body: created}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "update-breakpoint",
		Method:        http.MethodPut,
		Path:          "/v1/breakpoints/{id}",
		Summary:       "Enable / disable a breakpoint.",
		Tags:          []string{"breakpoints"},
		DefaultStatus: http.StatusNoContent,
	}, func(_ context.Context, in *UpdateBreakpointInput) (*struct{}, error) {
		if err := bp.Update(in.ID, in.Body.Enabled); err != nil {
			return nil, huma.Error404NotFound(err.Error())
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-breakpoint",
		Method:        http.MethodDelete,
		Path:          "/v1/breakpoints/{id}",
		Summary:       "Delete a breakpoint (and continue any held request).",
		Tags:          []string{"breakpoints"},
		DefaultStatus: http.StatusNoContent,
	}, func(_ context.Context, in *DeleteBreakpointInput) (*struct{}, error) {
		bp.Delete(in.ID)
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "continue-paused",
		Method:        http.MethodPost,
		Path:          "/v1/breakpoints/paused/{entryId}/continue",
		Summary:       "Resume a paused request — forward it upstream.",
		Tags:          []string{"breakpoints"},
		DefaultStatus: http.StatusNoContent,
	}, func(_ context.Context, in *ResolvePausedInput) (*struct{}, error) {
		if err := bp.Continue(in.EntryID); err != nil {
			return nil, huma.Error404NotFound(err.Error())
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "abort-paused",
		Method:        http.MethodPost,
		Path:          "/v1/breakpoints/paused/{entryId}/abort",
		Summary:       "Drop a paused request — client receives 503.",
		Tags:          []string{"breakpoints"},
		DefaultStatus: http.StatusNoContent,
	}, func(_ context.Context, in *ResolvePausedInput) (*struct{}, error) {
		if err := bp.Abort(in.EntryID); err != nil {
			return nil, huma.Error404NotFound(err.Error())
		}
		return nil, nil
	})
}
