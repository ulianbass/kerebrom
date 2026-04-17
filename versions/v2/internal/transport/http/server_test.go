package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/ulianbass/kerebrom/internal/store/sqlite"
)

func TestServerLifecycleAndSearch(t *testing.T) {
	t.Parallel()

	store, err := sqlite.Open(sqlite.Config{Path: filepath.Join(t.TempDir(), "kerebrom.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	if err := InitStore(context.Background(), store); err != nil {
		t.Fatalf("init store: %v", err)
	}

	server := NewServer(store)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/sessions", bytes.NewBufferString(`{
		"id":"session-1",
		"project":"Proyecto Kerebrom",
		"directory":"/tmp/project"
	}`))
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create session status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/observations", bytes.NewBufferString(`{
		"session_id":"session-1",
		"type":"decision",
		"title":"Shared local memory",
		"content":"Codex and Claude should share one local store.",
		"project":"Proyecto Kerebrom"
	}`))
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create observation status=%d body=%s", rec.Code, rec.Body.String())
	}

	var createdObservation sqlite.Observation
	if err := json.Unmarshal(rec.Body.Bytes(), &createdObservation); err != nil {
		t.Fatalf("decode created observation: %v", err)
	}
	if createdObservation.ID == 0 {
		t.Fatalf("expected created observation id, got %+v", createdObservation)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/search?q=shared+local+store&project=Proyecto+Kerebrom", nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("search status=%d body=%s", rec.Code, rec.Body.String())
	}

	var searchPayload struct {
		Count   int `json:"count"`
		Results []struct {
			Title string `json:"title"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &searchPayload); err != nil {
		t.Fatalf("decode search payload: %v", err)
	}
	if searchPayload.Count != 1 || len(searchPayload.Results) != 1 {
		t.Fatalf("unexpected search payload: %+v", searchPayload)
	}
	if searchPayload.Results[0].Title != "Shared local memory" {
		t.Fatalf("unexpected observation title: %+v", searchPayload)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/observations/"+strconv.FormatInt(createdObservation.ID, 10), nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get observation status=%d body=%s", rec.Code, rec.Body.String())
	}

	var fetchedObservation sqlite.Observation
	if err := json.Unmarshal(rec.Body.Bytes(), &fetchedObservation); err != nil {
		t.Fatalf("decode fetched observation: %v", err)
	}
	if fetchedObservation.ID != createdObservation.ID {
		t.Fatalf("unexpected fetched observation: %+v", fetchedObservation)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPatch, "/observations/"+strconv.FormatInt(createdObservation.ID, 10), bytes.NewBufferString(`{
		"content":"Codex and Claude should share one local store exactly."
	}`))
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update observation status=%d body=%s", rec.Code, rec.Body.String())
	}

	var updatedObservation sqlite.Observation
	if err := json.Unmarshal(rec.Body.Bytes(), &updatedObservation); err != nil {
		t.Fatalf("decode updated observation: %v", err)
	}
	if updatedObservation.RevisionCount == 0 {
		t.Fatalf("expected revision count after update: %+v", updatedObservation)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/prompts", bytes.NewBufferString(`{
		"session_id":"session-1",
		"content":"Close HTTP parity for Kerebrom.",
		"project":"Proyecto Kerebrom"
	}`))
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create prompt status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/prompts", bytes.NewBufferString(`{
		"session_id":"session-1",
		"content":"Gracias.",
		"project":"Proyecto Kerebrom"
	}`))
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("casual prompt status=%d body=%s", rec.Code, rec.Body.String())
	}
	var skippedPromptPayload struct {
		Saved  bool   `json:"saved"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &skippedPromptPayload); err != nil {
		t.Fatalf("decode skipped prompt payload: %v", err)
	}
	if skippedPromptPayload.Saved || skippedPromptPayload.Reason != "casual_prompt_noise" {
		t.Fatalf("expected casual prompt skip, got %+v", skippedPromptPayload)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/prompts/search?q=HTTP+parity&project=Proyecto+Kerebrom", nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("search prompts status=%d body=%s", rec.Code, rec.Body.String())
	}

	var promptSearchPayload struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &promptSearchPayload); err != nil {
		t.Fatalf("decode prompt search payload: %v", err)
	}
	if promptSearchPayload.Count != 1 {
		t.Fatalf("unexpected prompt search payload: %+v", promptSearchPayload)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/timeline?project=Proyecto+Kerebrom&limit=5", nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("timeline status=%d body=%s", rec.Code, rec.Body.String())
	}

	var timelinePayload struct {
		Count   int                  `json:"count"`
		Results []sqlite.Observation `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &timelinePayload); err != nil {
		t.Fatalf("decode timeline payload: %v", err)
	}
	if timelinePayload.Count != 1 || len(timelinePayload.Results) != 1 {
		t.Fatalf("unexpected timeline payload: %+v", timelinePayload)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/timeline?observation_id="+strconv.FormatInt(createdObservation.ID, 10)+"&before=2&after=2", nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("centered timeline status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/context?project=Proyecto+Kerebrom&q=shared+local+store&limit=5", nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("context status=%d body=%s", rec.Code, rec.Body.String())
	}

	var contextPayload struct {
		Query              string               `json:"query"`
		Stats              sqlite.Stats         `json:"stats"`
		RecentObservations []sqlite.Observation `json:"recent_observations"`
		Matches            []sqlite.Observation `json:"matches"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &contextPayload); err != nil {
		t.Fatalf("decode context payload: %v", err)
	}
	if contextPayload.Query != "shared local store" || len(contextPayload.Matches) != 1 || len(contextPayload.RecentObservations) != 1 {
		t.Fatalf("unexpected context payload: %+v", contextPayload)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/sessions/session-1", nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get session status=%d body=%s", rec.Code, rec.Body.String())
	}

	var session sqlite.Session
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode session payload: %v", err)
	}
	if session.ID != "session-1" || session.Status != "active" {
		t.Fatalf("unexpected session: %+v", session)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/stats?project=Proyecto+Kerebrom", nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stats status=%d body=%s", rec.Code, rec.Body.String())
	}

	var stats sqlite.Stats
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode stats payload: %v", err)
	}
	if stats.SessionCount != 1 || stats.ActiveSessionCount != 1 || stats.ObservationCount != 1 || stats.PromptCount != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/export?project=Proyecto+Kerebrom", nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("export status=%d body=%s", rec.Code, rec.Body.String())
	}

	var exportPayload sqlite.ExportData
	if err := json.Unmarshal(rec.Body.Bytes(), &exportPayload); err != nil {
		t.Fatalf("decode export payload: %v", err)
	}
	if len(exportPayload.Observations) != 1 || len(exportPayload.Prompts) != 1 {
		t.Fatalf("unexpected export payload: %+v", exportPayload)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/projects", nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("projects status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/sessions/session-1/end", bytes.NewBufferString(`{"summary":"done"}`))
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("end session status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/sessions/session-1/summary?limit=5", nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("session summary status=%d body=%s", rec.Code, rec.Body.String())
	}

	var summaryPayload struct {
		Session          sqlite.Session       `json:"session"`
		ObservationCount int                  `json:"observation_count"`
		Recent           []sqlite.Observation `json:"recent_observations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &summaryPayload); err != nil {
		t.Fatalf("decode session summary payload: %v", err)
	}
	if summaryPayload.Session.Status != "completed" || summaryPayload.ObservationCount != 1 || len(summaryPayload.Recent) != 1 {
		t.Fatalf("unexpected session summary: %+v", summaryPayload)
	}
}

func TestHTTPContextAndTimelineTreatWeakProjectAsCrossProject(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := sqlite.Open(sqlite.Config{Path: filepath.Join(t.TempDir(), "kerebrom.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	if err := InitStore(ctx, store); err != nil {
		t.Fatalf("init store: %v", err)
	}

	observation, err := store.SaveObservation(ctx, sqlite.ObservationInput{
		Type:    "decision",
		Title:   "NQ PreLondon post-2020 0 PASS",
		Content: "**What**: Proyecto Falage confirma NQ PreLondon post-2020 con 0 PASS.",
		Project: "Proyecto Falage",
		Scope:   "project",
	})
	if err != nil {
		t.Fatalf("save observation: %v", err)
	}

	server := NewServer(store)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/timeline?project=/&limit=5", nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("timeline status=%d body=%s", rec.Code, rec.Body.String())
	}
	var timelinePayload struct {
		Results []sqlite.Observation `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &timelinePayload); err != nil {
		t.Fatalf("decode timeline payload: %v", err)
	}
	if len(timelinePayload.Results) == 0 || timelinePayload.Results[0].ID != observation.ID {
		t.Fatalf("weak project timeline should include cross-project observation %d, got %+v", observation.ID, timelinePayload.Results)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/context?project=/&q=NQ+PreLondon+post-2020+0+PASS&limit=5", nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("context status=%d body=%s", rec.Code, rec.Body.String())
	}
	var contextPayload struct {
		ProjectFilter        string               `json:"project_filter"`
		ProjectFilterRelaxed bool                 `json:"project_filter_relaxed"`
		Matches              []sqlite.Observation `json:"matches"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &contextPayload); err != nil {
		t.Fatalf("decode context payload: %v", err)
	}
	if contextPayload.ProjectFilter != "" || !contextPayload.ProjectFilterRelaxed {
		t.Fatalf("weak project context should use relaxed cross-project lookup: %+v", contextPayload)
	}
	if len(contextPayload.Matches) == 0 || contextPayload.Matches[0].ID != observation.ID {
		t.Fatalf("weak project context should include cross-project match %d, got %+v", observation.ID, contextPayload.Matches)
	}
}
