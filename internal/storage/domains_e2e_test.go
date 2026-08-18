package storage

import (
	"context"
	"os"
	"testing"

	"go.uber.org/zap"
)

func TestE2EDomainsCRUD(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("set DATABASE_URL to run the domains CRUD e2e test")
	}
	db, err := NewPostgres(dbURL, zap.NewNop())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	key := "-----BEGIN RSA PRIVATE KEY-----\nMIIfake\n-----END RSA PRIVATE KEY-----"
	d := &Domain{Domain: "e2e-test.example", DKIMSelector: "mail", DKIMPrivateKey: &key, Verified: true, Active: true}

	if err := db.UpsertDomain(ctx, d); err != nil {
		t.Fatalf("upsert insert: %v", err)
	}
	if d.ID == "" {
		t.Fatal("upsert did not set ID")
	}

	got, err := db.GetDomain(ctx, "e2e-test.example")
	if err != nil || got == nil {
		t.Fatalf("get: %v (got %v)", err, got)
	}
	if got.DKIMSelector != "mail" || got.DKIMPrivateKey == nil || *got.DKIMPrivateKey != key || !got.Verified || !got.Active {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	// Upsert again (update path): change selector + deactivate.
	d.DKIMSelector = "s2"
	d.Active = false
	if err := db.UpsertDomain(ctx, d); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	got2, _ := db.GetDomain(ctx, "e2e-test.example")
	if got2.DKIMSelector != "s2" || got2.Active {
		t.Errorf("update not applied: %+v", got2)
	}

	// Inactive domains are excluded from ListActiveDomains.
	active, err := db.ListActiveDomains(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, a := range active {
		if a.Domain == "e2e-test.example" {
			t.Error("inactive domain should not appear in ListActiveDomains")
		}
	}
}
