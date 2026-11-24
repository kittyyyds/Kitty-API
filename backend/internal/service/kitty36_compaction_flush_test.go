//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestKitty36NativeCompactionWSMissingAndValidItems(t *testing.T) {
	for _, tc := range []struct {
		name, output, usage string
		failover, failed    bool
	}{
		{"unmetered", `[]`, ``, true, true},
		{"metered", `[]`, `,"usage":{"input_tokens":120,"output_tokens":30}`, false, true},
		{"valid_item", `[{"type":"compaction","encrypted_content":"fixture-only"}]`, `,"usage":{"input_tokens":120,"output_tokens":30}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := &openAIWSCaptureConn{events: [][]byte{[]byte(`{"type":"response.completed","response":{"id":"resp_compaction","status":"completed","output":` + tc.output + tc.usage + `}}`)}}
			svc := wsSSETestService(t, &wsSSETestDialer{conn: conn}, &httpUpstreamRecorder{})
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			MarkOpenAINativeCompactionV2(c)
			result, err := forwardSemanticWSAdapter(t, svc, c)
			if !tc.failed {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Contains(t, w.Body.String(), `"type":"response.completed"`)
				return
			}
			require.Error(t, err)
			var failover *UpstreamFailoverError
			require.Equal(t, tc.failover, errors.As(err, &failover))
			require.NotContains(t, w.Body.String(), `"type":"response.completed"`)
			if tc.usage != "" {
				require.NotNil(t, result)
				require.Equal(t, 120, result.Usage.InputTokens)
				require.Equal(t, 30, result.Usage.OutputTokens)
			}
		})
	}
}

func TestKitty36NativeCompactionMissingItemPreservesMeteredWork(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, metered := range []bool{false, true} {
			name := map[bool]string{false: "transformed", true: "passthrough"}[passthrough] + map[bool]string{false: "_unmetered", true: "_metered"}[metered]
			t.Run(name, func(t *testing.T) {
				usage := ""
				if metered {
					usage = `,"usage":{"input_tokens":120,"output_tokens":30}`
				}
				wire := `data: {"type":"response.completed","response":{"id":"resp_cmp","status":"completed","output":[]` + usage + `}}` + "\n\n"
				up := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire))}}
				svc := openAIClientToolsTestService(up)
				a := &Account{ID: 300, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"access_token": "fixture", "chatgpt_account_id": "fixture-workspace"}, Extra: astraHealthyTestExtra(map[string]any{"openai_passthrough": passthrough})}
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				MarkOpenAINativeCompactionV2(c)
				result, err := svc.Forward(context.Background(), c, a, []byte(`{"model":"gpt-6-astra","stream":true,"input":[{"type":"message","role":"user","content":"fixture"},{"type":"compaction_trigger"}]}`))
				require.Error(t, err)
				var failover *UpstreamFailoverError
				require.Equal(t, !metered, errors.As(err, &failover), "有真实用量不能自动重放")
				if metered {
					require.NotNil(t, result)
					require.Equal(t, 120, result.Usage.InputTokens)
					require.Equal(t, 30, result.Usage.OutputTokens)
				}
				require.NotContains(t, w.Body.String(), `"type":"response.completed"`)
			})
		}
	}
}

func TestKitty36NativeCompactionTimeoutIsNarrow(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.Equal(t, time.Minute, openAINativeCompactionStreamInterval(c, time.Minute))
	MarkOpenAINativeCompactionV2(c)
	require.Equal(t, 600*time.Second, openAINativeCompactionStreamInterval(c, time.Minute))
	require.Zero(t, openAINativeCompactionStreamInterval(c, 0))
}

type kittyFlushWriter struct {
	*httptest.ResponseRecorder
	flushes int
}

func (w *kittyFlushWriter) Flush() { w.flushes++; w.ResponseRecorder.Flush() }

func TestKitty36RawChatCoalescesBufferedFlushWithoutWaiting(t *testing.T) {
	chunk := `data: {"id":"fixture","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"a"},"finish_reason":null}]}` + "\n\n"
	wire := strings.Repeat(chunk, 100) + "data: [DONE]\n\n"
	w := &kittyFlushWriter{ResponseRecorder: httptest.NewRecorder()}
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	read := &kittyAssertFlushedReader{reader: strings.NewReader(wire), writer: w, t: t}
	resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(read)}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
	result, err := svc.streamRawChatCompletions(c, resp, rawChatCompletionsTestAccount(), "fixture", "fixture", "fixture", nil, nil, time.Now(), 1)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, wire, w.Body.String())
	require.Positive(t, w.flushes)
	require.Less(t, w.flushes, 10, "一次已缓冲的100个事件应合并flush")
}

type kittyAssertFlushedReader struct {
	reader *strings.Reader
	writer *kittyFlushWriter
	t      *testing.T
	reads  int
}

func (r *kittyAssertFlushedReader) Read(p []byte) (int, error) {
	if r.reads > 0 {
		require.Positive(r.t, r.writer.flushes, "等下一次上游读取前必须刷出前一批数据")
	}
	r.reads++
	return r.reader.Read(p)
}
