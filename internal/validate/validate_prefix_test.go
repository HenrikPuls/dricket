// SPDX-License-Identifier: GPL-3.0-or-later
package validate

import (
	"context"
	"testing"

	"github.com/lariscus/dricket/internal/service"
)

func TestValidatePrefixRegister(t *testing.T) {
	bare := setupBare(t)
	cfgPushMain(t, bare, "prefixes.toml", "prefixes = [\"app\"]\n")

	svc := openSvc(t, bare, "alice")
	ctx := context.Background()
	if _, err := svc.Add(ctx, service.AddRequest{Prefix: "app", Title: "registered", Unclaimed: true}); err != nil {
		t.Fatalf("add app: %v", err)
	}
	if _, err := svc.Add(ctx, service.AddRequest{Prefix: "ops", Title: "unregistered", Unclaimed: true}); err != nil {
		t.Fatalf("add ops: %v", err)
	}

	findings, err := Store(ctx, svc.Store)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	var opsWarn, appWarn bool
	for _, f := range findings {
		switch {
		case f.Kind == "prefix-not-registered" && f.Ticket == "ops-1":
			opsWarn = true
		case f.Kind == "prefix-not-registered" && f.Ticket == "app-1":
			appWarn = true
		}
	}
	if !opsWarn {
		t.Errorf("ops-1 missing prefix warning; findings:")
		for _, f := range findings {
			t.Logf("finding: %s", f)
		}
	}
	if appWarn {
		t.Error("app-1 is registered and must not warn")
	}
	// Warnings never fail a run.
	if HasErrors(findings) {
		t.Error("prefix warnings must not be error level")
	}
}

func TestValidateReportsBrokenPrefixRegister(t *testing.T) {
	bare := setupBare(t)
	cfgPushMain(t, bare, "prefixes.toml", "whatever = 5\n")

	svc := openSvc(t, bare, "auditor")
	findings, err := Store(context.Background(), svc.Store)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !kindsOf(findings, "")["prefixes-invalid"] {
		t.Fatalf("no prefixes-invalid finding: %+v", findings)
	}
}
