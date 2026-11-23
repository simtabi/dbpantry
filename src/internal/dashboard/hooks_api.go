package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/simtabi/dbpantry/src/internal/hooks"
	"github.com/simtabi/dbpantry/src/internal/jobs"
)

// controlJob is POST /api/v1/jobs/{id}/control: abort, pause, continue or restart.
func controlJob(o Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := principal(r)
		if p.Scope < ScopeOperate {
			apiError(w, http.StatusForbidden, "job control needs the operate scope")
			return
		}
		var req struct {
			Op string `json:"op"`
		}
		dec := json.NewDecoder(io.LimitReader(r.Body, 4*1024))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			apiError(w, http.StatusBadRequest, `body must be {"op":"abort"|"pause"|"continue"|"restart"}`)
			return
		}
		id := r.PathValue("id")
		var (
			j   jobs.Job
			err error
			typ string
		)
		switch req.Op {
		case "abort", "stop":
			j, err = o.Runner.Abort(id)
			typ = hooks.JobCancelled
		case "pause":
			j, err = o.Runner.Pause(id)
			typ = hooks.JobPaused
		case "continue", "resume":
			j, err = o.Runner.Continue(id)
			typ = hooks.JobContinued
		case "restart":
			j, err = o.Runner.Restart(id, p.Actor)
			typ = hooks.JobStarted
		default:
			apiError(w, http.StatusUnprocessableEntity, "op must be abort, pause, continue or restart")
			return
		}
		if errors.Is(err, jobs.ErrNoJob) {
			apiError(w, http.StatusNotFound, err.Error())
			return
		}
		if errors.Is(err, jobs.ErrNotActive) || errors.Is(err, jobs.ErrNotPaused) {
			apiError(w, http.StatusConflict, err.Error())
			return
		}
		if err != nil {
			apiError(w, http.StatusInternalServerError, err.Error())
			return
		}
		emitJob(o, typ, j, p.Actor)
		if req.Op == "restart" {
			w.Header().Set("Location", "/api/v1/jobs/"+j.ID)
			writeJSON(w, http.StatusAccepted, j)
			return
		}
		writeJSON(w, http.StatusOK, j)
	}
}

func emitJob(o Options, typ string, j jobs.Job, actor string) {
	if o.Hooks == nil || j.ID == "" {
		return
	}
	o.Hooks.Emit(context.Background(), hooks.Event{
		Type:   typ,
		Actor:  actor,
		Source: "jobs",
		Payload: map[string]any{
			"id":     j.ID,
			"action": j.Action,
			"state":  string(j.State),
			"args":   j.Args,
		},
	})
}

type hookCreate struct {
	URL    string   `json:"url"`
	Events []string `json:"events"`
}

// hookRoutes mounts webhook CRUD and the instance event stream (D-99).
func hookRoutes(mux *http.ServeMux, o Options) {
	if o.Hooks == nil {
		return
	}
	mux.HandleFunc("GET /api/v1/hooks", func(w http.ResponseWriter, r *http.Request) {
		if principal(r).Scope < ScopeAdmin {
			apiError(w, http.StatusForbidden, "listing webhooks needs the admin scope")
			return
		}
		writeJSON(w, http.StatusOK, o.Hooks.ListEndpoints())
	})
	mux.HandleFunc("POST /api/v1/hooks", func(w http.ResponseWriter, r *http.Request) {
		if principal(r).Scope < ScopeAdmin {
			apiError(w, http.StatusForbidden, "creating webhooks needs the admin scope")
			return
		}
		var req hookCreate
		dec := json.NewDecoder(io.LimitReader(r.Body, 16*1024))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			apiError(w, http.StatusBadRequest, "body must be {\"url\", \"events\"}: "+err.Error())
			return
		}
		ep, err := o.Hooks.CreateEndpoint(req.URL, req.Events)
		if err != nil {
			apiError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, ep)
	})
	mux.HandleFunc("DELETE /api/v1/hooks/{id}", func(w http.ResponseWriter, r *http.Request) {
		if principal(r).Scope < ScopeAdmin {
			apiError(w, http.StatusForbidden, "deleting webhooks needs the admin scope")
			return
		}
		if err := o.Hooks.DeleteEndpoint(r.PathValue("id")); errors.Is(err, hooks.ErrNotFound) {
			apiError(w, http.StatusNotFound, err.Error())
			return
		} else if err != nil {
			apiError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/v1/events", func(w http.ResponseWriter, r *http.Request) {
		n := 50
		if q := r.URL.Query().Get("limit"); q != "" {
			if v, err := strconv.Atoi(q); err == nil && v > 0 && v <= 500 {
				n = v
			}
		}
		writeJSON(w, http.StatusOK, o.Hooks.Recent(n))
	})
	mux.HandleFunc("GET /api/v1/events/stream", func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			apiError(w, http.StatusInternalServerError, "streaming is not supported")
			return
		}
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		fl.Flush()
		cancel := make(chan struct{})
		defer close(cancel)
		ch := o.Hooks.Watch(cancel)
		for {
			select {
			case <-r.Context().Done():
				return
			case e, ok := <-ch:
				if !ok {
					return
				}
				b, _ := json.Marshal(e)
				if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, b); err != nil {
					return
				}
				fl.Flush()
			}
		}
	})
}
