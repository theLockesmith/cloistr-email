package identity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

func TestCloistrMeClient_VerifyAddressOwnership(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	tests := []struct {
		name           string
		serverResponse func(w http.ResponseWriter, r *http.Request)
		pubkey         string
		address        string
		wantOwned      bool
		wantErr        bool
	}{
		{
			name: "address owned by pubkey",
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				// Verify auth header
				if r.Header.Get("Authorization") != "Bearer test-secret" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}

				// Verify query params
				if r.URL.Query().Get("pubkey") == "" || r.URL.Query().Get("address") == "" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}

				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(VerifyAddressResponse{
					Owned:   true,
					Address: "alice@cloistr.xyz",
					Pubkey:  "abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234",
				})
			},
			pubkey:    "abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234",
			address:   "alice@cloistr.xyz",
			wantOwned: true,
			wantErr:   false,
		},
		{
			name: "address not owned by pubkey",
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(VerifyAddressResponse{
					Owned:   false,
					Address: "alice@cloistr.xyz",
					Pubkey:  "abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234",
				})
			},
			pubkey:    "abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234",
			address:   "alice@cloistr.xyz",
			wantOwned: false,
			wantErr:   false,
		},
		{
			name: "address not found",
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			},
			pubkey:    "abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234",
			address:   "unknown@cloistr.xyz",
			wantOwned: false,
			wantErr:   false,
		},
		{
			name: "unauthorized - bad secret",
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			},
			pubkey:    "abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234",
			address:   "alice@cloistr.xyz",
			wantOwned: false,
			wantErr:   true,
		},
		{
			name: "server error",
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			pubkey:    "abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234",
			address:   "alice@cloistr.xyz",
			wantOwned: false,
			wantErr:   true,
		},
		{
			name: "error in response",
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(VerifyAddressResponse{
					Error: "internal error",
				})
			},
			pubkey:    "abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234",
			address:   "alice@cloistr.xyz",
			wantOwned: false,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(tt.serverResponse))
			defer server.Close()

			client := NewCloistrMeClient(server.URL, "test-secret", logger)

			owned, err := client.VerifyAddressOwnership(context.Background(), tt.pubkey, tt.address)

			if (err != nil) != tt.wantErr {
				t.Errorf("VerifyAddressOwnership() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if owned != tt.wantOwned {
				t.Errorf("VerifyAddressOwnership() owned = %v, want %v", owned, tt.wantOwned)
			}
		})
	}
}

func TestCloistrMeClient_NoURLConfigured(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	client := NewCloistrMeClient("", "test-secret", logger)

	owned, err := client.VerifyAddressOwnership(context.Background(), "pubkey", "address")

	if err != nil {
		t.Errorf("Expected no error when URL not configured, got %v", err)
	}

	if !owned {
		t.Errorf("Expected owned=true when URL not configured (backwards compat)")
	}
}

func TestCloistrMeClient_NoSecretConfigured(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	client := NewCloistrMeClient("http://localhost", "", logger)

	owned, err := client.VerifyAddressOwnership(context.Background(), "pubkey", "address")

	if err != nil {
		t.Errorf("Expected no error when secret not configured, got %v", err)
	}

	if !owned {
		t.Errorf("Expected owned=true when secret not configured (backwards compat)")
	}
}

func TestCloistrMeClient_RequestFormat(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	var capturedRequest *http.Request

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedRequest = r
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(VerifyAddressResponse{Owned: true})
	}))
	defer server.Close()

	client := NewCloistrMeClient(server.URL, "my-secret", logger)

	_, _ = client.VerifyAddressOwnership(context.Background(),
		"abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234",
		"test@cloistr.xyz",
	)

	// Verify request method
	if capturedRequest.Method != http.MethodGet {
		t.Errorf("Expected GET method, got %s", capturedRequest.Method)
	}

	// Verify authorization header
	if capturedRequest.Header.Get("Authorization") != "Bearer my-secret" {
		t.Errorf("Expected Bearer token auth, got %s", capturedRequest.Header.Get("Authorization"))
	}

	// Verify path
	if capturedRequest.URL.Path != "/internal/v1/addresses/verify" {
		t.Errorf("Expected /internal/v1/addresses/verify, got %s", capturedRequest.URL.Path)
	}

	// Verify query params
	if capturedRequest.URL.Query().Get("pubkey") != "abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234" {
		t.Errorf("Expected pubkey in query, got %s", capturedRequest.URL.Query().Get("pubkey"))
	}

	if capturedRequest.URL.Query().Get("address") != "test@cloistr.xyz" {
		t.Errorf("Expected address in query, got %s", capturedRequest.URL.Query().Get("address"))
	}
}
