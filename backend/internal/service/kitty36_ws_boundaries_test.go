//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestKitty36WS403EvidenceClassification(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{"html_challenge", `<!doctype html><html><title>Just a moment...</title></html>`, "edge_challenge_403", 403},
		{"structured_denial", `{"error":{"type":"permission_error","message":"denied"}}`, "permission_denied", 403},
		{"truncated_json", `{"error":`, "forbidden_unknown", 403},
		{"empty", "", "forbidden_unknown", 403},
		{"unknown_text", "forbidden", "forbidden_unknown", 403},
		{"unauthorized", `{"error":{"type":"invalid_token"}}`, "auth_failed", 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := &openAIWSDialError{StatusCode: tc.status, ResponseBody: []byte(tc.body), ResponseHeaders: http.Header{"Cf-Mitigated": {"challenge"}}, Err: errors.New("fixture rejection")}
			require.Equal(t, tc.want, classifyOpenAIWSAcquireError(err))
			closeErr := (&OpenAIGatewayService{}).mapOpenAIWSPassthroughDialError(err, tc.status, err.ResponseHeaders)
			require.Error(t, closeErr)
			if tc.status == 403 {
				require.NotContains(t, closeErr.Error(), "authentication failed")
			}
			require.Equal(t, tc.want == "edge_challenge_403", openAIWSEdgeBackoffEligibleFailure(err))
		})
	}
}

func TestKitty36WSFinalSerializedFramePreflightBeforeSubmission(t *testing.T) {
	for _, tc := range []struct {
		name, previous   string
		transport        OpenAIClientTransport
		enabled, blocked bool
	}{
		{"http_first_turn", "", OpenAIClientTransportHTTP, true, true},
		{"explicit_continuation", "resp_prior", OpenAIClientTransportHTTP, true, false},
		{"native_ws", "", OpenAIClientTransportWS, true, false},
		{"feature_off", "", OpenAIClientTransportHTTP, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := &openAIWSCaptureConn{events: [][]byte{[]byte(`{"type":"response.completed","response":{"id":"resp_ok","usage":{"input_tokens":1,"output_tokens":1}}}`)}}
			svc := wsSSETestService(t, &wsSSETestDialer{conn: conn}, &httpUpstreamRecorder{})
			svc.cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = tc.enabled
			svc.cfg.Gateway.OpenAIWS.HTTPBridgeThresholdBytes = 300
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			SetOpenAIClientTransport(c, tc.transport)
			var body map[string]any
			require.NoError(t, json.Unmarshal([]byte(wsSSETestRequest), &body))
			// The model input alone fits; final client metadata pushes the actual frame over the boundary.
			body["client_metadata"] = map[string]any{"metadata_padding": strings.Repeat("中", 200)}
			if tc.previous != "" {
				body["previous_response_id"] = tc.previous
			}
			recovered := false
			_, err := svc.forwardOpenAIWSV2(context.Background(), c, wsSSETestAccount(), body, "", "", "test-oauth-token", OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2}, false, true, "gpt-5.1", "gpt-5.1", time.Now(), 1, "", &recovered)
			if tc.blocked {
				require.ErrorIs(t, err, errOpenAIWSOutgoingFrameTooLarge)
				require.Empty(t, conn.lastWrite)
				require.False(t, w.Flushed)
				return
			}
			require.NotErrorIs(t, err, errOpenAIWSOutgoingFrameTooLarge)
			require.NotEmpty(t, conn.lastWrite)
		})
	}
}
