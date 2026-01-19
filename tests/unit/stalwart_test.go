package unit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/coldforge/coldforge-email/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestNewStalwartClient tests client creation
func TestNewStalwartClient(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	tests := []struct {
		name      string
		baseURL   string
		apiKey    string
		expectErr bool
		errMsg    string
	}{
		{
			name:      "valid configuration",
			baseURL:   "https://mail.example.com",
			apiKey:    "api_test_key",
			expectErr: false,
		},
		{
			name:      "missing base URL",
			baseURL:   "",
			apiKey:    "api_test_key",
			expectErr: true,
			errMsg:    "base URL is required",
		},
		{
			name:      "missing API key",
			baseURL:   "https://mail.example.com",
			apiKey:    "",
			expectErr: true,
			errMsg:    "API key is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := auth.NewStalwartClient(tt.baseURL, tt.apiKey, logger)

			if tt.expectErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Nil(t, client)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, client)
			}
		})
	}
}

// TestStalwartCreateAccount tests account creation
func TestStalwartCreateAccount(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	tests := []struct {
		name           string
		principal      *auth.Principal
		serverResponse func(w http.ResponseWriter, r *http.Request)
		expectErr      bool
		expectedID     int64
	}{
		{
			name: "successful account creation",
			principal: &auth.Principal{
				Name:        "john.doe",
				Description: "John Doe",
				Emails:      []string{"john.doe@example.com"},
				Quota:       10737418240,
			},
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "POST", r.Method)
				assert.Equal(t, "/api/principal", r.URL.Path)
				assert.Equal(t, "Bearer test_api_key", r.Header.Get("Authorization"))
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

				var principal auth.Principal
				json.NewDecoder(r.Body).Decode(&principal)
				assert.Equal(t, "john.doe", principal.Name)
				assert.Equal(t, "individual", principal.Type)

				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]int64{"data": 123})
			},
			expectErr:  false,
			expectedID: 123,
		},
		{
			name: "account creation with conflict",
			principal: &auth.Principal{
				Name:   "existing.user",
				Emails: []string{"existing@example.com"},
			},
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"type":   "about:blank",
					"status": 409,
					"title":  "Conflict",
					"detail": "Principal already exists",
				})
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(tt.serverResponse))
			defer server.Close()

			client, err := auth.NewStalwartClient(server.URL, "test_api_key", logger)
			require.NoError(t, err)

			id, err := client.CreateAccount(context.Background(), tt.principal)

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedID, id)
			}
		})
	}
}

// TestStalwartGetAccount tests account retrieval
func TestStalwartGetAccount(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	tests := []struct {
		name           string
		idOrName       string
		serverResponse func(w http.ResponseWriter, r *http.Request)
		expectErr      bool
		isNotFound     bool
	}{
		{
			name:     "get existing account",
			idOrName: "john.doe",
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "GET", r.Method)
				assert.Equal(t, "/api/principal/john.doe", r.URL.Path)

				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"data": map[string]interface{}{
						"id":          123,
						"type":        "individual",
						"name":        "john.doe",
						"description": "John Doe",
						"emails":      []string{"john.doe@example.com"},
						"quota":       10737418240,
						"roles":       []string{"user"},
					},
				})
			},
			expectErr: false,
		},
		{
			name:     "get non-existent account",
			idOrName: "unknown.user",
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"type":   "about:blank",
					"status": 404,
					"title":  "Not Found",
					"detail": "The requested resource does not exist on this server.",
				})
			},
			expectErr:  true,
			isNotFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(tt.serverResponse))
			defer server.Close()

			client, err := auth.NewStalwartClient(server.URL, "test_api_key", logger)
			require.NoError(t, err)

			principal, err := client.GetAccount(context.Background(), tt.idOrName)

			if tt.expectErr {
				assert.Error(t, err)
				assert.Nil(t, principal)
				if tt.isNotFound {
					stalwartErr, ok := err.(*auth.StalwartError)
					require.True(t, ok)
					assert.True(t, stalwartErr.IsNotFound())
				}
			} else {
				assert.NoError(t, err)
				require.NotNil(t, principal)
				assert.Equal(t, "john.doe", principal.Name)
				assert.Equal(t, "individual", principal.Type)
			}
		})
	}
}

// TestStalwartUpdateAccount tests account updates
func TestStalwartUpdateAccount(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	tests := []struct {
		name           string
		idOrName       string
		operations     []auth.UpdateOperation
		serverResponse func(w http.ResponseWriter, r *http.Request)
		expectErr      bool
	}{
		{
			name:     "update account quota",
			idOrName: "john.doe",
			operations: []auth.UpdateOperation{
				{Action: "set", Field: "quota", Value: int64(21474836480)},
			},
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "PATCH", r.Method)
				assert.Equal(t, "/api/principal/john.doe", r.URL.Path)

				var ops []auth.UpdateOperation
				json.NewDecoder(r.Body).Decode(&ops)
				assert.Len(t, ops, 1)
				assert.Equal(t, "set", ops[0].Action)
				assert.Equal(t, "quota", ops[0].Field)

				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]interface{}{"data": nil})
			},
			expectErr: false,
		},
		{
			name:     "add email to account",
			idOrName: "john.doe",
			operations: []auth.UpdateOperation{
				{Action: "addItem", Field: "emails", Value: "john.alias@example.com"},
			},
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "PATCH", r.Method)
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]interface{}{"data": nil})
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(tt.serverResponse))
			defer server.Close()

			client, err := auth.NewStalwartClient(server.URL, "test_api_key", logger)
			require.NoError(t, err)

			err = client.UpdateAccount(context.Background(), tt.idOrName, tt.operations)

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestStalwartDeleteAccount tests account deletion
func TestStalwartDeleteAccount(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	tests := []struct {
		name           string
		idOrName       string
		serverResponse func(w http.ResponseWriter, r *http.Request)
		expectErr      bool
	}{
		{
			name:     "delete existing account",
			idOrName: "john.doe",
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "DELETE", r.Method)
				assert.Equal(t, "/api/principal/john.doe", r.URL.Path)

				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]interface{}{"data": nil})
			},
			expectErr: false,
		},
		{
			name:     "delete non-existent account",
			idOrName: "unknown.user",
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"type":   "about:blank",
					"status": 404,
					"title":  "Not Found",
					"detail": "The requested resource does not exist on this server.",
				})
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(tt.serverResponse))
			defer server.Close()

			client, err := auth.NewStalwartClient(server.URL, "test_api_key", logger)
			require.NoError(t, err)

			err = client.DeleteAccount(context.Background(), tt.idOrName)

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestStalwartListAccounts tests listing accounts
func TestStalwartListAccounts(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	tests := []struct {
		name           string
		page           int
		limit          int
		accountType    string
		serverResponse func(w http.ResponseWriter, r *http.Request)
		expectErr      bool
		expectedTotal  int
	}{
		{
			name:        "list all accounts",
			page:        1,
			limit:       100,
			accountType: "individual",
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "GET", r.Method)
				assert.Equal(t, "1", r.URL.Query().Get("page"))
				assert.Equal(t, "100", r.URL.Query().Get("limit"))
				assert.Equal(t, "individual", r.URL.Query().Get("types"))

				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"data": map[string]interface{}{
						"items": []map[string]interface{}{
							{"id": 1, "name": "user1", "type": "individual"},
							{"id": 2, "name": "user2", "type": "individual"},
						},
						"total": 2,
					},
				})
			},
			expectErr:     false,
			expectedTotal: 2,
		},
		{
			name:        "list with no filters",
			page:        0,
			limit:       0,
			accountType: "",
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "GET", r.Method)
				assert.Equal(t, "/api/principal", r.URL.Path)

				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"data": map[string]interface{}{
						"items": []map[string]interface{}{},
						"total": 0,
					},
				})
			},
			expectErr:     false,
			expectedTotal: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(tt.serverResponse))
			defer server.Close()

			client, err := auth.NewStalwartClient(server.URL, "test_api_key", logger)
			require.NoError(t, err)

			list, err := client.ListAccounts(context.Background(), tt.page, tt.limit, tt.accountType)

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				require.NotNil(t, list)
				assert.Equal(t, tt.expectedTotal, list.Total)
			}
		})
	}
}

// TestStalwartVerifyAccount tests account verification
func TestStalwartVerifyAccount(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	tests := []struct {
		name           string
		idOrName       string
		serverResponse func(w http.ResponseWriter, r *http.Request)
		expectErr      bool
		expectExists   bool
	}{
		{
			name:     "verify existing account",
			idOrName: "john.doe",
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"data": map[string]interface{}{
						"id":   123,
						"name": "john.doe",
						"type": "individual",
					},
				})
			},
			expectErr:    false,
			expectExists: true,
		},
		{
			name:     "verify non-existent account",
			idOrName: "unknown.user",
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"type":   "about:blank",
					"status": 404,
					"title":  "Not Found",
					"detail": "The requested resource does not exist on this server.",
				})
			},
			expectErr:    false,
			expectExists: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(tt.serverResponse))
			defer server.Close()

			client, err := auth.NewStalwartClient(server.URL, "test_api_key", logger)
			require.NoError(t, err)

			exists, err := client.VerifyAccount(context.Background(), tt.idOrName)

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectExists, exists)
			}
		})
	}
}

// TestStalwartHealth tests health check
func TestStalwartHealth(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	tests := []struct {
		name           string
		serverResponse func(w http.ResponseWriter, r *http.Request)
		expectErr      bool
	}{
		{
			name: "healthy server",
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "GET", r.Method)
				assert.Equal(t, "/healthz/live", r.URL.Path)
				w.WriteHeader(http.StatusOK)
			},
			expectErr: false,
		},
		{
			name: "unhealthy server",
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(tt.serverResponse))
			defer server.Close()

			client, err := auth.NewStalwartClient(server.URL, "test_api_key", logger)
			require.NoError(t, err)

			err = client.Health(context.Background())

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestStalwartConvenienceMethods tests convenience methods
func TestStalwartConvenienceMethods(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	t.Run("SetPassword", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "PATCH", r.Method)

			var ops []auth.UpdateOperation
			json.NewDecoder(r.Body).Decode(&ops)
			assert.Len(t, ops, 1)
			assert.Equal(t, "set", ops[0].Action)
			assert.Equal(t, "secrets", ops[0].Field)

			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{"data": nil})
		}))
		defer server.Close()

		client, _ := auth.NewStalwartClient(server.URL, "test_api_key", logger)
		err := client.SetPassword(context.Background(), "john.doe", "newpassword123")
		assert.NoError(t, err)
	})

	t.Run("SetQuota", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var ops []auth.UpdateOperation
			json.NewDecoder(r.Body).Decode(&ops)
			assert.Equal(t, "set", ops[0].Action)
			assert.Equal(t, "quota", ops[0].Field)

			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{"data": nil})
		}))
		defer server.Close()

		client, _ := auth.NewStalwartClient(server.URL, "test_api_key", logger)
		err := client.SetQuota(context.Background(), "john.doe", 10*1024*1024*1024)
		assert.NoError(t, err)
	})

	t.Run("AddEmail", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var ops []auth.UpdateOperation
			json.NewDecoder(r.Body).Decode(&ops)
			assert.Equal(t, "addItem", ops[0].Action)
			assert.Equal(t, "emails", ops[0].Field)
			assert.Equal(t, "alias@example.com", ops[0].Value)

			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{"data": nil})
		}))
		defer server.Close()

		client, _ := auth.NewStalwartClient(server.URL, "test_api_key", logger)
		err := client.AddEmail(context.Background(), "john.doe", "alias@example.com")
		assert.NoError(t, err)
	})

	t.Run("RemoveEmail", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var ops []auth.UpdateOperation
			json.NewDecoder(r.Body).Decode(&ops)
			assert.Equal(t, "removeItem", ops[0].Action)
			assert.Equal(t, "emails", ops[0].Field)

			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{"data": nil})
		}))
		defer server.Close()

		client, _ := auth.NewStalwartClient(server.URL, "test_api_key", logger)
		err := client.RemoveEmail(context.Background(), "john.doe", "old@example.com")
		assert.NoError(t, err)
	})
}

// TestStalwartEnsureAccount tests the ensure account method
func TestStalwartEnsureAccount(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	t.Run("account already exists", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Return existing account on GET
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{
					"id":   123,
					"name": "existing.user",
					"type": "individual",
				},
			})
		}))
		defer server.Close()

		client, _ := auth.NewStalwartClient(server.URL, "test_api_key", logger)
		info := &auth.AccountInfo{
			Name:  "existing.user",
			Email: "existing@example.com",
		}

		principal, err := client.EnsureAccount(context.Background(), info)
		assert.NoError(t, err)
		require.NotNil(t, principal)
		assert.Equal(t, "existing.user", principal.Name)
	})

	t.Run("account needs to be created", func(t *testing.T) {
		callCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++
			if callCount == 1 {
				// First call: GET returns 404
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"type":   "about:blank",
					"status": 404,
					"title":  "Not Found",
					"detail": "The requested resource does not exist on this server.",
				})
			} else if callCount == 2 {
				// Second call: POST creates account
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]int64{"data": 456})
			} else {
				// Third call: GET returns the created account
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"data": map[string]interface{}{
						"id":     456,
						"name":   "new.user",
						"type":   "individual",
						"emails": []string{"new@example.com"},
					},
				})
			}
		}))
		defer server.Close()

		client, _ := auth.NewStalwartClient(server.URL, "test_api_key", logger)
		info := &auth.AccountInfo{
			Name:  "new.user",
			Email: "new@example.com",
		}

		principal, err := client.EnsureAccount(context.Background(), info)
		assert.NoError(t, err)
		require.NotNil(t, principal)
		assert.Equal(t, int64(456), principal.ID)
	})
}

// TestStalwartErrorTypes tests error type assertions
func TestStalwartErrorTypes(t *testing.T) {
	t.Run("IsNotFound", func(t *testing.T) {
		err := &auth.StalwartError{StatusCode: 404, Message: "not found"}
		assert.True(t, err.IsNotFound())

		err2 := &auth.StalwartError{StatusCode: 500, Message: "server error"}
		assert.False(t, err2.IsNotFound())
	})

	t.Run("IsUnauthorized", func(t *testing.T) {
		err := &auth.StalwartError{StatusCode: 401, Message: "unauthorized"}
		assert.True(t, err.IsUnauthorized())

		err2 := &auth.StalwartError{StatusCode: 403, Message: "forbidden"}
		assert.False(t, err2.IsUnauthorized())
	})

	t.Run("Error with APIError", func(t *testing.T) {
		err := &auth.StalwartError{
			StatusCode: 404,
			APIError: &auth.APIError{
				Type:   "about:blank",
				Status: 404,
				Title:  "Not Found",
				Detail: "Resource not found",
			},
		}
		assert.Contains(t, err.Error(), "Not Found")
		assert.Contains(t, err.Error(), "Resource not found")
	})

	t.Run("Error without APIError", func(t *testing.T) {
		err := &auth.StalwartError{
			StatusCode: 500,
			Message:    "internal error details",
		}
		assert.Contains(t, err.Error(), "500")
		assert.Contains(t, err.Error(), "internal error details")
	})
}
