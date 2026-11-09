package web

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"weirdstats/internal/storage"
)

func TestDiscoveryRemainsVisibleAlongsideOldMapFailures(t *testing.T) {
	s, store := newAutomationTestServer(t)
	for _, job := range []storage.Job{{Type: "enrich_activity", UserID: 11, ActivityID: 42, Status: "failed", ErrorCode: "map_unavailable"}, {Type: "sync_latest", UserID: 11, Payload: `{"user_id":11}`}} {
		if _, err := store.CreateJob(context.Background(), job); err != nil {
			t.Fatal(err)
		}
	}
	view, err := s.syncView(context.Background(), 11)
	if err != nil {
		t.Fatal(err)
	}
	if view.State != "working" || view.Discovering != 1 || view.MapFailed != 1 {
		t.Fatalf("sync obscured by optional failure: %+v", view)
	}
}

func TestSyncPanelDistinguishesOptionalErrorsAndReloadSemantics(t *testing.T) {
	s, store := newAutomationTestServer(t)
	if _, err := store.CreateJob(context.Background(), storage.Job{Type: "enrich_activity", UserID: 11, ActivityID: 42, Status: "failed", ErrorCode: "map_unavailable"}); err != nil {
		t.Fatal(err)
	}
	view, err := s.syncView(context.Background(), 11)
	if err != nil {
		t.Fatal(err)
	}
	var html bytes.Buffer
	if err = s.templates["profile"].ExecuteTemplate(&html, "sync-status", struct{ Sync SyncView }{view}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"1 map details unavailable", "Reload imported activities", "Reloading does not fetch new activities from Strava."} {
		if !strings.Contains(html.String(), want) {
			t.Fatalf("missing honest status %q: %s", want, html.String())
		}
	}
	if strings.Contains(html.String(), `id="retry-import" hidden`) {
		t.Fatal("map retry incorrectly hidden")
	}
}
