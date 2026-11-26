//go:build unit

package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Exercise ForwardNative, including Code Assist's real SSE-to-JSON branch,
// rather than proving consistency only on the projection helper.
func kittyForwardNativeUsage(t *testing.T, body string, stream, oauth bool) (*ForwardResult, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := newGeminiSignalService("text/event-stream; charset=utf-8", body)
	account := geminiSignalTestAccount()
	if oauth {
		svc.tokenProvider = &GeminiTokenProvider{}
		account = &Account{
			ID: 704, Platform: PlatformGemini, Type: AccountTypeOAuth,
			Credentials: map[string]any{"access_token": "ya29.test-token", "project_id": "project-1"},
			Concurrency: 1,
		}
	}
	c, rec := newGeminiNativeTestContext(t)
	action := "generateContent"
	if stream {
		action = "streamGenerateContent"
	}
	result, err := svc.ForwardNative(context.Background(), c, account,
		"gemini-3.7-flash", action, stream, geminiSignalTestRequest())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, stream, result.Stream)
	return result, rec
}

func kittyNativeUsageFrames(t *testing.T, body string) []string {
	t.Helper()
	var frames []string
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload != "" && payload != "[DONE]" {
			require.True(t, gjson.Valid(payload), payload)
			frames = append(frames, payload)
		}
	}
	return frames
}

func kittyRequireNativeUsage(t *testing.T, raw, path string, want map[string]int) {
	t.Helper()
	for _, field := range []string{"promptTokenCount", "candidatesTokenCount", "thoughtsTokenCount", "cachedContentTokenCount", "totalTokenCount"} {
		value := gjson.Get(raw, path+"."+field)
		expected, present := want[field]
		require.Equal(t, present, value.Exists(), "%s presence in %s", field, raw)
		if present {
			require.Equal(t, gjson.Number, value.Type, field)
			require.EqualValues(t, expected, value.Int(), field)
		}
	}
}

func TestKittyGeminiNative_StreamZeroPlaceholderKeepsClientBillingAligned(t *testing.T) {
	first := `{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"},{"functionCall":{"name":"lookup","args":{"big":9007199254740993}}}]},"candidateExtension":"kept"}],"rootExtension":{"big":9007199254740993},"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":10,"thoughtsTokenCount":3,"cachedContentTokenCount":80,"totalTokenCount":113,"unknownUsage":{"big":9007199254740993}}}`
	last := `{"candidates":[{"content":{},"finishReason":"STOP","finishMessage":"done"}],"tailExtension":true,"usageMetadata":{"promptTokenCount":0,"candidatesTokenCount":0,"thoughtsTokenCount":0,"cachedContentTokenCount":0,"totalTokenCount":0,"unknownUsage":{"tail":true}}}`
	for _, transport := range []string{"api key", "oauth", "api key wrapped"} {
		t.Run(transport, func(t *testing.T) {
			inputFirst, inputLast := first, last
			path := "usageMetadata"
			if transport != "api key" {
				inputFirst = `{"response":` + first + `,"wrapperExtension":123}`
				inputLast = `{"response":` + last + `,"wrapperExtension":456}`
			}
			if transport == "api key wrapped" {
				path = "response.usageMetadata"
			}
			body := ": keepalive\r\nevent: message\r\ndata:  " + inputFirst + "\r\n\r\ndata:\t" + inputLast + "\r\n\r\ndata: [DONE]\r\n\r\n"
			result, rec := kittyForwardNativeUsage(t, body, true, transport == "oauth")
			frames := kittyNativeUsageFrames(t, rec.Body.String())
			require.Len(t, frames, 2)
			wantFirst, wantLast := inputFirst, inputLast
			if transport == "oauth" {
				wantFirst, wantLast = first, last // Existing OAuth unwrap remains intact.
			}
			require.Equal(t, wantFirst, frames[0])
			before, err := sjson.Delete(wantLast, path)
			require.NoError(t, err)
			after, err := sjson.Delete(frames[1], path)
			require.NoError(t, err)
			require.Equal(t, before, after, "only usage counters may change")
			kittyRequireNativeUsage(t, frames[1], path, map[string]int{
				"promptTokenCount": 100, "candidatesTokenCount": 10, "thoughtsTokenCount": 3,
				"cachedContentTokenCount": 80, "totalTokenCount": 113,
			})
			require.JSONEq(t, `{"tail":true}`, gjson.Get(frames[1], path+".unknownUsage").Raw)
			require.Equal(t, 20, result.Usage.InputTokens)
			require.Equal(t, 13, result.Usage.OutputTokens)
			require.Equal(t, 80, result.Usage.CacheReadInputTokens)
			require.NotNil(t, result.FirstTokenMs)
			require.Equal(t, 2, result.Usage.geminiUsageEvidence.Frames, "projection must not re-merge usage")
			require.Equal(t, 1, result.Usage.geminiUsageEvidence.ZeroResetsIgnored)
			require.Equal(t, 1, strings.Count(rec.Body.String(), `"text":"hello"`), "content must not be replayed")
			require.True(t, strings.HasSuffix(rec.Body.String(), "data: [DONE]\r\n\r\n"))
			require.True(t, strings.HasPrefix(rec.Body.String(), ": keepalive\r\nevent: message\r\n"))
			if transport != "oauth" {
				require.Contains(t, rec.Body.String(), "data:\t"+frames[1]+"\r\n\r\n")
			}
		})
	}
}

func TestKittyGeminiNative_UsagePresenceAcrossStreamAndCollectedResponses(t *testing.T) {
	const content = `{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],"nativeExtension":{"kept":true}}`
	cases := []struct {
		name, initial, tail  string
		want                 map[string]int
		input, output, cache int
	}{
		{
			name: "usage only tail", tail: `{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":10,"thoughtsTokenCount":2,"cachedContentTokenCount":80,"tailExtension":"kept"}}`,
			want: map[string]int{"promptTokenCount": 100, "candidatesTokenCount": 10, "thoughtsTokenCount": 2, "cachedContentTokenCount": 80}, input: 20, output: 12, cache: 80,
		},
		{
			name:    "complete zero placeholder does not create unknown counters",
			initial: `{"promptTokenCount":100,"candidatesTokenCount":10}`,
			tail:    `{"usageMetadata":{"promptTokenCount":0,"candidatesTokenCount":0,"thoughtsTokenCount":0,"cachedContentTokenCount":0,"totalTokenCount":0}}`,
			want:    map[string]int{"promptTokenCount": 100, "candidatesTokenCount": 10}, input: 100, output: 10,
		},
		{
			name:    "all observed fields survive complete zero placeholder",
			initial: `{"promptTokenCount":100,"candidatesTokenCount":10,"thoughtsTokenCount":3,"cachedContentTokenCount":80,"totalTokenCount":113}`,
			tail:    `{"usageMetadata":{"promptTokenCount":0,"candidatesTokenCount":0,"thoughtsTokenCount":0,"cachedContentTokenCount":0,"totalTokenCount":0}}`,
			want:    map[string]int{"promptTokenCount": 100, "candidatesTokenCount": 10, "thoughtsTokenCount": 3, "cachedContentTokenCount": 80, "totalTokenCount": 113}, input: 20, output: 13, cache: 80,
		},
		{
			name:    "partial explicit cache zero remains authoritative",
			initial: `{"promptTokenCount":100,"candidatesTokenCount":10,"thoughtsTokenCount":3,"cachedContentTokenCount":80}`,
			tail:    `{"usageMetadata":{"cachedContentTokenCount":0}}`,
			want:    map[string]int{"promptTokenCount": 100, "candidatesTokenCount": 10, "thoughtsTokenCount": 3, "cachedContentTokenCount": 0}, input: 100, output: 13,
		},
		{
			name: "explicit complete zero is reported not missing",
			tail: `{"usageMetadata":{"promptTokenCount":0,"candidatesTokenCount":0,"thoughtsTokenCount":0,"cachedContentTokenCount":0,"totalTokenCount":0}}`,
			want: map[string]int{"promptTokenCount": 0, "candidatesTokenCount": 0, "thoughtsTokenCount": 0, "cachedContentTokenCount": 0, "totalTokenCount": 0},
		},
		{
			name: "partial zero does not create prompt candidate or total",
			tail: `{"usageMetadata":{"cachedContentTokenCount":0}}`, want: map[string]int{"cachedContentTokenCount": 0},
		},
		{name: "completely missing stays missing"},
		{name: "null stays missing", tail: `{"usageMetadata":null}`},
	}
	for _, stream := range []bool{true, false} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("stream=%t/%s", stream, tc.name), func(t *testing.T) {
				first := content
				if tc.initial != "" {
					var err error
					first, err = sjson.SetRaw(first, "usageMetadata", tc.initial)
					require.NoError(t, err)
				}
				body := "data: " + first + "\n\n"
				frameCount := 1
				if tc.tail != "" {
					frameCount++
					body += "data: " + tc.tail + "\n\n"
				}
				result, rec := kittyForwardNativeUsage(t, body, stream, !stream)
				client := rec.Body.String()
				if stream {
					frames := kittyNativeUsageFrames(t, client)
					require.Len(t, frames, frameCount)
					client = frames[len(frames)-1]
					if tc.initial == "" {
						require.Equal(t, first, frames[0], "no usage may be added to a content-only frame")
					}
				} else {
					require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
					require.Equal(t, "hello", gjson.Get(client, "candidates.0.content.parts.0.text").String())
					require.Equal(t, "STOP", gjson.Get(client, "candidates.0.finishReason").String())
					require.True(t, gjson.Get(client, "nativeExtension.kept").Bool())
				}
				kittyRequireNativeUsage(t, client, "usageMetadata", tc.want)
				if tc.name == "usage only tail" {
					require.Equal(t, "kept", gjson.Get(client, "usageMetadata.tailExtension").String())
				}
				if tc.name == "completely missing stays missing" {
					require.False(t, gjson.Get(client, "usageMetadata").Exists())
					if stream {
						require.Equal(t, body, rec.Body.String())
					}
				}
				if tc.name == "null stays missing" {
					require.Equal(t, "null", gjson.Get(client, "usageMetadata").Raw)
				}
				require.Equal(t, tc.input, result.Usage.InputTokens)
				require.Equal(t, tc.output, result.Usage.OutputTokens)
				require.Equal(t, tc.cache, result.Usage.CacheReadInputTokens)
				require.Equal(t, frameCount, result.Usage.geminiUsageEvidence.Frames)
				require.Equal(t, 1, strings.Count(rec.Body.String(), `"text":"hello"`))
			})
		}
	}
}

func TestKittyGeminiNative_CollectedResponseRetainsTerminalEnvelope(t *testing.T) {
	for _, withError := range []bool{false, true} {
		t.Run(fmt.Sprintf("error=%t", withError), func(t *testing.T) {
			frames := []string{
				`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"candidateExtension":{"big":9007199254740993}}],"rootExtension":{"big":9007199254740993},"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":3,"thoughtsTokenCount":2,"cachedContentTokenCount":80,"promptTokensDetails":[{"modality":"TEXT","tokenCount":100}],"unknownUsage":{"big":9007199254740993}}}}`,
				`{"response":{"candidates":[{"content":{"parts":[{"text":" world"},{"functionCall":{"name":"lookup","args":{"big":9007199254740993}}}]}}],"secondExtension":true}}`,
				`{"response":{"candidates":[{"content":{},"finishReason":"PROHIBITED_CONTENT","finishMessage":"original finish message","terminalExtension":"kept"}],"tailExtension":{"big":9007199254740993},"usageMetadata":{"candidatesTokenCount":5,"totalTokenCount":107,"tailUsageExtension":"kept"}}}`,
			}
			if withError {
				frames = append(frames, `{"response":{"error":{"code":503,"status":"UNAVAILABLE","message":"original error","details":[{"extension":9007199254740993}]}}}`)
			}
			body := "data: " + strings.Join(frames, "\n\ndata: ") + "\n\ndata: [DONE]\n\n"
			result, rec := kittyForwardNativeUsage(t, body, false, true)
			client := rec.Body.String()
			require.False(t, gjson.Get(client, "response").Exists(), "OAuth wrapper must stay unwrapped")
			require.Equal(t, "hello world", gjson.Get(client, "candidates.0.content.parts.0.text").String())
			require.Len(t, gjson.Get(client, "candidates.0.content.parts").Array(), 2, "do not duplicate text or tool content")
			require.Equal(t, "9007199254740993", gjson.Get(client, "candidates.0.content.parts.1.functionCall.args.big").Raw)
			require.Equal(t, "PROHIBITED_CONTENT", gjson.Get(client, "candidates.0.finishReason").String())
			require.Equal(t, "original finish message", gjson.Get(client, "candidates.0.finishMessage").String())
			require.Equal(t, "kept", gjson.Get(client, "candidates.0.terminalExtension").String())
			for _, path := range []string{"rootExtension.big", "tailExtension.big", "candidates.0.candidateExtension.big", "usageMetadata.unknownUsage.big"} {
				require.Equal(t, "9007199254740993", gjson.Get(client, path).Raw, path)
			}
			require.True(t, gjson.Get(client, "secondExtension").Bool())
			require.Equal(t, "kept", gjson.Get(client, "usageMetadata.tailUsageExtension").String())
			require.JSONEq(t, `[{"modality":"TEXT","tokenCount":100}]`, gjson.Get(client, "usageMetadata.promptTokensDetails").Raw)
			kittyRequireNativeUsage(t, client, "usageMetadata", map[string]int{"promptTokenCount": 100, "candidatesTokenCount": 5, "thoughtsTokenCount": 2, "cachedContentTokenCount": 80, "totalTokenCount": 107})
			require.Equal(t, 20, result.Usage.InputTokens)
			require.Equal(t, 7, result.Usage.OutputTokens)
			require.Equal(t, 80, result.Usage.CacheReadInputTokens)
			require.Equal(t, len(frames), result.Usage.geminiUsageEvidence.Frames)
			if withError {
				require.JSONEq(t, `{"code":503,"status":"UNAVAILABLE","message":"original error","details":[{"extension":9007199254740993}]}`, gjson.Get(client, "error").Raw)
			} else {
				require.False(t, gjson.Get(client, "error").Exists())
			}
		})
	}
}

func TestKittyGeminiNative_StreamKeepsErrorFinishAndEOFBytes(t *testing.T) {
	initial := `{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":10}}`
	reset := `{"usageMetadata":{"promptTokenCount":0,"candidatesTokenCount":0,"cachedContentTokenCount":0,"totalTokenCount":0}}`
	projected := `{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":10}}`
	finish := `{"candidates":[{"content":{"parts":[{"text":"hello"}]},"finishReason":"STOP","extension":true}]}`
	errorFrame := `{"error":{"code":503,"message":"original error","extension":9007199254740993}}`
	for _, suffix := range []string{"", "\r\n\r\ndata: [DONE]\r\n\r\n"} {
		t.Run(fmt.Sprintf("done=%t", suffix != ""), func(t *testing.T) {
			prefix := "retry: 25\r\n: keepalive\r\ndata: " + initial + "\r\n\r\ndata: " + finish + "\r\n\r\ndata: " + errorFrame + "\r\n\r\nevent: message\r\ndata:  "
			result, rec := kittyForwardNativeUsage(t, prefix+reset+suffix, true, false)
			require.Equal(t, prefix+projected+suffix, rec.Body.String(), "only reset counters change; control lines, error, finish and EOF remain intact")
			require.Equal(t, 100, result.Usage.InputTokens)
			require.Equal(t, 10, result.Usage.OutputTokens)
			require.Len(t, kittyNativeUsageFrames(t, rec.Body.String()), 4)
		})
	}
}

func TestKittyGeminiNative_ProjectionWithoutEvidenceDoesNotManufactureUsage(t *testing.T) {
	for _, raw := range []string{`{"candidates":[]}`, `{"usageMetadata":null}`, `{"usageMetadata":{"unknown":true}}`, `not-json`, `[]`} {
		for _, usage := range []*ClaudeUsage{nil, {}, {InputTokens: 12, OutputTokens: 3}, {geminiUsageEvidence: &GeminiUsageEvidence{}}} {
			for _, attach := range []bool{false, true} {
				require.Equal(t, raw, string(projectGeminiNativeUsage([]byte(raw), usage, attach)))
			}
		}
	}
}

func TestKittyGeminiNative_CollectedPartsAreNotReplayedByUsageTail(t *testing.T) {
	// Distinct text/thought parts and an earlier tool call must not be folded
	// into a duplicated final text part, or lost when the last chunk is usage.
	first := `{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"reason","thought":true,"thoughtSignature":"original-signature"},{"text":"hello"},{"text":" world"},{"functionCall":{"name":"lookup","args":{"big":9007199254740993}},"partExtension":"kept"}]}}]}}`
	second := `{"response":{"candidates":[{"content":{"parts":[{"text":"!","textExtension":"kept"}]},"finishReason":"STOP"}]}}`
	tail := `{"response":{"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3}}}`
	result, rec := kittyForwardNativeUsage(t, "data: "+first+"\n\ndata: "+second+"\n\ndata: "+tail+"\n\n", false, true)
	parts := gjson.Get(rec.Body.String(), "candidates.0.content.parts").Array()
	require.Len(t, parts, 5)
	var text strings.Builder
	for _, part := range parts {
		text.WriteString(part.Get("text").String())
	}
	require.Equal(t, "reasonhello world!", text.String(), "every text delta appears exactly once")
	require.Equal(t, "original-signature", parts[0].Get("thoughtSignature").String())
	require.True(t, parts[0].Get("thought").Bool())
	require.Equal(t, "9007199254740993", parts[3].Get("functionCall.args.big").Raw)
	require.Equal(t, "kept", parts[3].Get("partExtension").String())
	require.Equal(t, "kept", parts[4].Get("textExtension").String())
	require.Equal(t, "STOP", gjson.Get(rec.Body.String(), "candidates.0.finishReason").String())
	kittyRequireNativeUsage(t, rec.Body.String(), "usageMetadata", map[string]int{"promptTokenCount": 10, "candidatesTokenCount": 3})
	require.Equal(t, 10, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
}
