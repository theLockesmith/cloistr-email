package blobseal

import (
	"bytes"
	"errors"
	"testing"
)

// Audit on !212: the wrapped key must be bound to its attachment row, so a
// database writer cannot copy one row's key (and blob hash) onto another row
// and have it open there.
func TestKeyIsBoundToItsRow(t *testing.T) {
	s := mustSealer(t, testKEK(t))
	blob, key, err := s.Seal(plaintext, "row-a")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if got, err := s.Open(key, blob, "row-a"); err != nil || !bytes.Equal(got, plaintext) {
		t.Fatalf("own row does not open: %v", err)
	}
	got, err := s.Open(key, blob, "row-b")
	if !errors.Is(err, ErrOpen) || got != nil {
		t.Fatalf("key moved to another row opened: err=%v bytes=%d", err, len(got))
	}
}

func TestEmptyBindingIsRefused(t *testing.T) {
	s := mustSealer(t, testKEK(t))
	if _, _, err := s.Seal(plaintext, ""); err == nil {
		t.Fatal("Seal accepted an empty row binding")
	}
	blob, key, _ := s.Seal(plaintext, "row-a")
	if _, err := s.Open(key, blob, ""); !errors.Is(err, ErrOpen) {
		t.Fatalf("Open with empty binding: %v, want ErrOpen", err)
	}
}
