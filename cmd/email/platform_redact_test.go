package main

import (
	"strings"
	"testing"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/config"
)

// main logs platform.NewClient's error at startup, and that error can repeat
// the database DSN. It must not carry the password.
func TestNewPlatformClientErrorHasNoPassword(t *testing.T) {
	for _, dsn := range []string{
		"host=127.0.0.1 port=1 password=Sup3rSecret junk",
		"postgres://u:ab/Cd+Ef9z==@127.0.0.1:notaport/x",
	} {
		_, err := newPlatformClient(&config.Config{PlatformMode: "platform", DatabaseURL: dsn})
		if err == nil {
			t.Fatalf("%s: expected an error", dsn)
		}
		for _, p := range []string{"Sup3rSecret", "Cd+Ef9z"} {
			if strings.Contains(err.Error(), p) {
				t.Errorf("platform client error carries password fragment %q: %v", p, err)
			}
		}
	}
}
