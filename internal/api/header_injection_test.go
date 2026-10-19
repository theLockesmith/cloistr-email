package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// A signed-in sender could put CR/LF in header-bound fields and append their
// own headers: a second From (DKIM then signs that one), Reply-To, or a blank
// line that replaces the body. The API refuses these with 400 before anything
// reaches the email service.
func TestSendEmailV2_RejectsHeaderBreaks(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{"to": []string{"bob@example.com"}, "subject": "hi", "body": "hello"}
	}
	cases := map[string]func(m map[string]any){
		"subject forged From":  func(m map[string]any) { m["subject"] = "hi\r\nFrom: ceo@bank.example" },
		"subject body replace": func(m map[string]any) { m["subject"] = "hi\r\n\r\nreplaced body" },
		"subject bare LF":      func(m map[string]any) { m["subject"] = "hi\nReply-To: a@evil.example" },
		"subject bare CR":      func(m map[string]any) { m["subject"] = "hi\rX: y" },
		"in_reply_to":          func(m map[string]any) { m["in_reply_to"] = "x@y>\r\nX-Injected: 1\r\nZ: <z" },
		"references":           func(m map[string]any) { m["references"] = []string{"a@b>\r\nX: 1"} },
		"from":                 func(m map[string]any) { m["from"] = "alice@cloistr.xyz\r\nBcc: x@evil.example" },
		"to":                   func(m map[string]any) { m["to"] = []string{"bob@example.com\r\nX: 1"} },
		"cc":                   func(m map[string]any) { m["cc"] = []string{"c@example.com\nX: 1"} },
		"bcc":                  func(m map[string]any) { m["bcc"] = []string{"d@example.com\rX: 1"} },
		"attachment filename": func(m map[string]any) {
			m["attachments"] = []map[string]string{{"filename": "a\"\r\nX: 1", "data_base64": "eA=="}}
		},
		"attachment contenttype": func(m map[string]any) {
			m["attachments"] = []map[string]string{{"filename": "a", "content_type": "text/plain\r\nX: 1", "data_base64": "eA=="}}
		},
	}
	// emailSvc is nil: reaching it would panic, so a 400 proves the request
	// was refused before the service saw it.
	h := &EmailHandler{logger: zap.NewNop()}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := base()
			mutate(m)
			b, _ := json.Marshal(m)
			req := httptest.NewRequest(http.MethodPost, "/api/v2/email/send", bytes.NewReader(b))
			req = req.WithContext(context.WithValue(req.Context(), contextKeyUserID, "npub-test"))
			rec := httptest.NewRecorder()
			h.SendEmailV2(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), "HEADER_INJECTION") {
				t.Errorf("body %s does not name HEADER_INJECTION", rec.Body)
			}
		})
	}
}
