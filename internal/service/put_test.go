// SPDX-License-Identifier: GPL-3.0-or-later
package service

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/lariscus/dricket/internal/ticket"
)

const app1 = "tickets/app/0000/app-1/ticket.md"

func commitCount(t *testing.T, bare string) int {
	t.Helper()
	n, err := strconv.Atoi(strings.TrimSpace(runGit(t, bare, "rev-list", "--count", "tickets")))
	if err != nil {
		t.Fatalf("rev-list: %v", err)
	}
	return n
}

func wantCode(t *testing.T, what string, err error, code int) {
	t.Helper()
	if err == nil || ExitCode(err) != code {
		t.Fatalf("%s: err = %v (code %d), want exit %d", what, err, ExitCode(err), code)
	}
}

func ver(n int) *int { return &n }

func tkRev(t *testing.T, bare string) int {
	t.Helper()
	return mustTicket(t, bareShow(t, bare, app1)).Rev
}

// putEdit applies edit to the current document of id and publishes it with
// put: under a short claim when the ticket is unclaimed, as the claim
// holder otherwise (ADR-0013). It fails the test on any error.
func putEdit(t *testing.T, s *Service, bare, id string, edit func(string) string) PutResult {
	t.Helper()
	path, err := PathFor(id)
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	content := bareShow(t, bare, path)
	tk := mustTicket(t, content)
	opts := PutOptions{IfVersion: ver(tk.Rev)}
	if tk.Owner == "" {
		opts.Claim = true
	}
	res, err := s.Put(context.Background(), id, edit(content), opts)
	if err != nil {
		t.Fatalf("put %s: %v", id, err)
	}
	return res
}

// putField rewrites one header line of id (or inserts it before the
// front-matter end) and publishes it via putEdit.
func putField(t *testing.T, s *Service, bare, id, key, value string) PutResult {
	t.Helper()
	return putEdit(t, s, bare, id, func(content string) string {
		lines := strings.Split(content, "\n")
		for i, l := range lines {
			if strings.HasPrefix(l, key+":") {
				lines[i] = key + ": " + value
				return strings.Join(lines, "\n")
			}
		}
		return strings.Replace(content, "\n---\n", "\n"+key+": "+value+"\n---\n", 1)
	})
}

// putStage changes the stage header line via putEdit.
func putStage(t *testing.T, s *Service, bare, id string, stage string) PutResult {
	t.Helper()
	return putField(t, s, bare, id, "stage", stage)
}

// ------------------------------------------------------------- R-01 ----

func TestExtensionFieldsAreSetPreservedAndRemoved(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	s := open(t, bare, "alice")
	id, err := s.Add(ctx, AddRequest{Prefix: "app", Title: "ext", Unclaimed: true,
		Extensions: []FieldValue{{Key: "x-persona", Value: "ben"}}})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if v, _ := mustTicket(t, bareShow(t, bare, app1)).Extension("x-persona"); v != "ben" {
		t.Fatalf("x-persona after add = %q", v)
	}

	before := commitCount(t, bare)
	res := putEdit(t, s, bare, id, func(content string) string {
		content = strings.Replace(content, "x-persona: ben", "x-persona: paul\nx-rank: 5000", 1)
		return strings.Replace(content, "title: ext", "title: renamed", 1)
	})
	if commitCount(t, bare) != before+1 {
		t.Error("one put must be one commit")
	}
	tk := mustTicket(t, bareShow(t, bare, app1))
	if tk.Rev != 1 || res.Rev != 1 || tk.Title != "renamed" {
		t.Errorf("rev %d / %d, title %q", tk.Rev, res.Rev, tk.Title)
	}
	if got := tk.ExtensionFields(); got["x-persona"] != "paul" || got["x-rank"] != "5000" {
		t.Errorf("extensions = %v", got)
	}
	if strings.Join(res.ChangedFields, ",") != "title,x-persona,x-rank" {
		t.Errorf("changed_fields = %v", res.ChangedFields)
	}

	// Claim and release keep the extension fields untouched.
	if _, err := s.Claim(ctx, id, ticket.SizeM, ticket.KindAgent); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := s.Release(ctx, id, ticket.StageReview, ""); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := mustTicket(t, bareShow(t, bare, app1)).ExtensionFields(); got["x-persona"] != "paul" || got["x-rank"] != "5000" {
		t.Errorf("extensions after claim/release = %v", got)
	}

	// Dropping the line removes the key.
	putEdit(t, s, bare, id, func(content string) string {
		return strings.Replace(content, "x-rank: 5000\n", "", 1)
	})
	if _, ok := mustTicket(t, bareShow(t, bare, app1)).Extension("x-rank"); ok {
		t.Error("x-rank still present after removal")
	}
}

func TestExtensionFieldValidation(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	alice := open(t, bare, "alice")
	id, _ := alice.Add(ctx, AddRequest{Prefix: "app", Title: "v", Unclaimed: true})
	content := bareShow(t, bare, app1)
	put := func(doc string) error {
		_, err := alice.Put(ctx, id, doc, PutOptions{IfVersion: ver(tkRev(t, bare)), Claim: true})
		return err
	}

	wantCode(t, "oversized value", put(strings.Replace(content, "labels: ", "x-big: "+strings.Repeat("a", ticket.MaxExtensionValue+1)+"\nlabels: ", 1)), 14)
	wantCode(t, "duplicate key", put(strings.Replace(content, "title: v", "title: v\ntitle: w", 1)), 14)
	wantCode(t, "secret in value", put(strings.Replace(content, "labels: ", "x-token: ghp_"+strings.Repeat("a1B2", 10)+"\nlabels: ", 1)), 19)
	_, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "t", Unclaimed: true, Extensions: []FieldValue{{Key: "title", Value: "x"}}})
	wantCode(t, "add --set of a non-extension", err, 14)
	if n := commitCount(t, bare); n != 2 { // seed + add
		t.Errorf("rejected writes left %d commits", n)
	}

	// Someone else's live claim blocks every write.
	bob := open(t, bare, "bob")
	if _, err := bob.Claim(ctx, id, ticket.SizeM, ticket.KindAgent); err != nil {
		t.Fatalf("claim: %v", err)
	}
	_, err = alice.Put(ctx, id, content, PutOptions{IfVersion: ver(1), Claim: true})
	wantCode(t, "put on foreign claim", err, 11)
}

// ------------------------------------------------------------- R-02 ----

func TestPutUnclaimedNeedsShortClaim(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	alice := open(t, bare, "alice")
	id, _ := alice.Add(ctx, AddRequest{Prefix: "app", Title: "put me", Unclaimed: true})
	content := bareShow(t, bare, app1)
	edited := strings.Replace(content, "(no description)", "A real description.", 1)
	edited = strings.Replace(edited, "labels: ", "x-persona: ben\nlabels: ", 1)

	// No write without a claim (ADR-0013): unclaimed needs --claim.
	_, err := alice.Put(ctx, id, edited, PutOptions{IfVersion: ver(0)})
	wantCode(t, "claim-free put on an unclaimed ticket", err, 17)
	_, err = alice.Put(ctx, id, edited, PutOptions{Claim: true})
	wantCode(t, "short claim without if-version", err, 14)
	_, err = alice.Put(ctx, id, edited, PutOptions{Claim: true, IfVersion: ver(7)})
	wantCode(t, "wrong if-version", err, 12)

	before := commitCount(t, bare)
	res, err := alice.Put(ctx, id, strings.ReplaceAll(edited, "\n", "\r\n"), PutOptions{IfVersion: ver(tkRev(t, bare)), Claim: true})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if res.Rev != 1 || res.Commit == "" || commitCount(t, bare) != before+1 {
		t.Errorf("result = %+v, commits %d -> %d", res, before, commitCount(t, bare))
	}
	if strings.Join(res.ChangedFields, ",") != "x-persona,body" {
		t.Errorf("changed_fields = %v", res.ChangedFields)
	}
	got := bareShow(t, bare, app1)
	if strings.Contains(got, "\r") || !strings.Contains(got, "A real description.") || !strings.Contains(got, "x-persona: ben") {
		t.Errorf("stored ticket:\n%s", got)
	}
	tk := mustTicket(t, got)
	if tk.Epoch != 0 || tk.Rev != 1 || tk.Owner != "" {
		t.Errorf("claim state changed: %+v", tk)
	}
	raw, _ := json.Marshal(res)
	if !strings.HasPrefix(string(raw), `{"id":"app-1","rev":1,"commit":"`) ||
		!strings.Contains(string(raw), `"changed_fields":["x-persona","body"],"short_claim":true}`) {
		t.Errorf("JSON = %s", raw)
	}

	// The same document again is a no-op: no commit, no rev bump.
	before = commitCount(t, bare)
	again, err := alice.Put(ctx, id, got, PutOptions{IfVersion: ver(1), Claim: true})
	if err != nil || again.Rev != 1 || len(again.ChangedFields) != 0 || commitCount(t, bare) != before {
		t.Errorf("no-op put = %+v, %v, commits %d -> %d", again, err, before, commitCount(t, bare))
	}
}

func TestPutProtectsIdentityAndClaimFields(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	alice := open(t, bare, "alice")
	id, _ := alice.Add(ctx, AddRequest{Prefix: "app", Title: "guarded", Unclaimed: true})
	content := bareShow(t, bare, app1)
	opts := PutOptions{IfVersion: ver(tkRev(t, bare)), Claim: true}

	for name, bad := range map[string]string{
		"identity":    strings.Replace(content, "created_by: alice", "created_by: mallory", 1),
		"owner":       strings.Replace(content, "owner: \n", "owner: mallory\n", 1),
		"epoch":       strings.Replace(content, "epoch: 0", "epoch: 5", 1),
		"stage":       strings.Replace(content, "stage: open", "stage: shipped", 1),
		"priority":    strings.Replace(content, "priority: normal", "priority: bogus", 1),
		"unparseable": "no front matter",
		"secret":      content + "\nghp_" + strings.Repeat("a1B2", 10) + "\n",
		"too big":     content + strings.Repeat("x", MaxDocument),
	} {
		_, err := alice.Put(ctx, id, bad, opts)
		if err == nil {
			t.Errorf("%s: put accepted", name)
			continue
		}
		if want := map[string]int{"secret": 19}[name]; want != 0 && ExitCode(err) != want {
			t.Errorf("%s: exit %d, want %d", name, ExitCode(err), want)
		}
	}
	if tkRev(t, bare) != 0 {
		t.Error("a rejected put changed the ticket")
	}

	// rev in the document is ignored: the service sets fresh + 1.
	forged := strings.Replace(content, "rev: 0", "rev: 99", 1)
	forged = strings.Replace(forged, "(no description)", "ok", 1)
	if res, err := alice.Put(ctx, id, forged, opts); err != nil || res.Rev != 1 {
		t.Errorf("put with forged rev = %+v, %v", res, err)
	}
}

func TestPutClaimantAndAdminOverride(t *testing.T) {
	bare := setupBare(t)
	pushMainFile(t, bare, "dricket.toml", "[security]\nadmins = [\"root\"]\n")
	ctx := context.Background()
	alice := open(t, bare, "alice")
	bob := open(t, bare, "bob")
	root := open(t, bare, "root")
	id, _ := alice.Add(ctx, AddRequest{Prefix: "app", Title: "claimed"}) // add claims it

	content := bareShow(t, bare, app1)
	edited := strings.Replace(content, "(no description)", "claimant text", 1)

	// The claimant needs no if-version and no --claim.
	res, err := alice.Put(ctx, id, edited, PutOptions{})
	if err != nil || res.Rev != 2 {
		t.Fatalf("claimant put = %+v, %v", res, err)
	}
	// Others are refused: 11 for a plain write, 17 for --force without admin.
	other := strings.Replace(edited, "claimant text", "bob text", 1)
	_, err = bob.Put(ctx, id, other, PutOptions{IfVersion: ver(2)})
	wantCode(t, "put on a foreign claim", err, 11)
	_, err = bob.Put(ctx, id, other, PutOptions{IfVersion: ver(2), Force: true})
	wantCode(t, "force without admin", err, 17)
	_, err = alice.Put(ctx, id, other, PutOptions{Force: true})
	wantCode(t, "force on own claim", err, 14)
	_, err = alice.Put(ctx, id, other, PutOptions{Claim: true})
	wantCode(t, "short claim on own claim", err, 14)

	// An admin overrides; the claim fields stay, the claimant's next
	// exact-base commit fails because the content moved on.
	before := mustTicket(t, bareShow(t, bare, app1))
	res, err = root.Put(ctx, id, other, PutOptions{IfVersion: ver(2), Force: true})
	if err != nil || res.Rev != 3 {
		t.Fatalf("forced put = %+v, %v", res, err)
	}
	after := mustTicket(t, bareShow(t, bare, app1))
	if after.Owner != "alice" || after.Epoch != before.Epoch || !strings.Contains(after.Body, "bob text") {
		t.Errorf("after forced put: %+v", after)
	}
	if msg := runGit(t, bare, "log", "-1", "--format=%s", "tickets"); !strings.Contains(msg, "admin override") {
		t.Errorf("commit message = %q", msg)
	}
	// The old claimant still holds the claim and can write again.
	if _, err := alice.Put(ctx, id, bareShow(t, bare, app1)+"\nmore\n", PutOptions{}); err != nil {
		t.Errorf("claimant put after override: %v", err)
	}
}

// ------------------------------------------------------------- R-03 ----

const driveToml = `[security]
admins = ["root"]

[stages]
values    = ["inbox", "prioritized", "build", "commit", "done"]
initial   = "inbox"
terminal  = ["done"]
claim     = ["build", "commit"]
cancelled = "cancelled"
`

func TestConfiguredStagesDriveTheLifecycle(t *testing.T) {
	bare := setupBare(t)
	pushMainFile(t, bare, "dricket.toml", driveToml)
	ctx := context.Background()
	alice := open(t, bare, "alice")

	id, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "first", Unclaimed: true})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if tk := mustTicket(t, bareShow(t, bare, app1)); tk.Stage != "inbox" {
		t.Fatalf("initial stage = %q", tk.Stage)
	}
	// Stage changes are puts of the whole document; a stranger stage is refused.
	putStage(t, alice, bare, id, "prioritized")
	_, err = alice.Put(ctx, id, strings.Replace(bareShow(t, bare, app1), "stage: prioritized", "stage: open", 1),
		PutOptions{IfVersion: ver(tkRev(t, bare)), Claim: true})
	wantCode(t, "stage outside the vocabulary", err, 14)

	// next offers only tickets in a claim stage; a claim from prioritized
	// lands in the first claim stage.
	if _, err := alice.Next(ctx, NextOptions{}); ExitCode(err) != 10 {
		t.Errorf("next with nothing in a claim stage: %v", err)
	}
	putStage(t, alice, bare, id, "build")
	next, err := alice.Next(ctx, NextOptions{Claim: true})
	if err != nil || next.ID != id || next.Stage != "build" || next.Claim == nil {
		t.Fatalf("next --claim = %+v, %v", next, err)
	}
	if tk := mustTicket(t, bareShow(t, bare, app1)); tk.Stage != "build" || tk.Owner != "alice" {
		t.Errorf("after claim: %+v", tk)
	}

	// Release needs an explicit stage; the agent may reach commit, not done.
	_, err = alice.Release(ctx, id, "", "")
	wantCode(t, "release without stage", err, 14)
	_, err = alice.Release(ctx, id, "done", "")
	wantCode(t, "agent release to a terminal stage", err, 14)
	_, err = alice.Release(ctx, id, ticket.StageReview, "")
	wantCode(t, "release to a built-in stage", err, 14)
	if res, err := alice.Release(ctx, id, "commit", "built"); err != nil || res.Stage != "commit" {
		t.Fatalf("release = %+v, %v", res, err)
	}

	// A claim of a ticket outside the claim stages is moved into one.
	id2, _ := alice.Add(ctx, AddRequest{Prefix: "app", Title: "second", Unclaimed: true})
	if _, err := alice.Claim(ctx, id2, ticket.SizeM, ticket.KindHuman); err != nil {
		t.Fatalf("claim from inbox: %v", err)
	}
	if tk := mustTicket(t, bareShow(t, bare, "tickets/app/0000/app-2/ticket.md")); tk.Stage != "build" {
		t.Errorf("claim from inbox gave stage %q", tk.Stage)
	}
	// While claimed the stage must stay a claim stage (lint).
	_, err = alice.Put(ctx, id2, strings.Replace(bareShow(t, bare, "tickets/app/0000/app-2/ticket.md"), "stage: build", "stage: inbox", 1), PutOptions{})
	wantCode(t, "owned ticket moved out of the claim stages", err, 14)

	// depends is satisfied by the terminal stage only.
	id3, _ := alice.Add(ctx, AddRequest{Prefix: "app", Title: "third", Unclaimed: true, Depends: []string{id}})
	putStage(t, alice, bare, id3, "build")
	if n, err := alice.Next(ctx, NextOptions{}); err != nil || n.ID != id {
		t.Errorf("next = %+v, %v; the unclaimed commit-stage ticket %s is the only eligible one", n, err, id)
	}
	putStage(t, alice, bare, id, "done")
	if n, err := alice.Next(ctx, NextOptions{}); err != nil || n.ID != id3 {
		t.Errorf("next after the dependency is done = %+v, %v", n, err)
	}
}

func TestConfiguredStagesApplyToPutAndForceRelease(t *testing.T) {
	bare := setupBare(t)
	pushMainFile(t, bare, "dricket.toml", driveToml)
	ctx := context.Background()
	root := open(t, bare, "root")
	id, _ := root.Add(ctx, AddRequest{Prefix: "app", Title: "p", Unclaimed: true})
	content := bareShow(t, bare, app1)

	_, err := root.Put(ctx, id, strings.Replace(content, "stage: inbox", "stage: open", 1), PutOptions{IfVersion: ver(0), Claim: true})
	wantCode(t, "put with a stage outside the vocabulary", err, 14)
	res, err := root.Put(ctx, id, strings.Replace(content, "stage: inbox", "stage: prioritized", 1), PutOptions{IfVersion: ver(tkRev(t, bare)), Claim: true})
	if err != nil || strings.Join(res.ChangedFields, ",") != "stage" {
		t.Fatalf("put with a new stage = %+v, %v", res, err)
	}

	if _, err := root.Claim(ctx, id, ticket.SizeM, ticket.KindAgent); err != nil {
		t.Fatalf("claim: %v", err)
	}
	fr, err := root.ForceRelease(ctx, id, "dead", "")
	if err != nil || fr.Stage != "inbox" {
		t.Fatalf("force-release default stage = %+v, %v", fr, err)
	}
	_, err = root.ForceRelease(ctx, id, "dead", "open")
	if err == nil {
		t.Error("force-release accepted a stage outside the vocabulary")
	}
}
