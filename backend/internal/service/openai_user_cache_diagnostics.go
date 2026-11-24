package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tidwall/gjson"
)

const (
	openAICacheDiagnosticMaxSamples = 64
	openAICacheDiagnosticMaxMinutes = 30
	openAICacheDiagnosticMaxBody    = 256 << 10
	openAICacheDiagnosticMaxEgress  = 4
)

var openAICacheDiagnosticModel = regexp.MustCompile("^[a-zA-Z0-9][a-zA-Z0-9_.:/+-]{0,99}$")
var ErrInvalidOpenAICacheDiagnostic = errors.New("require one user ID, one exact model, 1–30 minutes and 1–64 samples")

type OpenAICacheDiagnosticConfig struct {
	UserID     int64  `json:"user_id"`
	Model      string `json:"model"`
	Minutes    int    `json:"minutes"`
	MaxSamples int    `json:"max_samples"`
}

type OpenAICacheDiagnosticStatus struct {
	Active    bool                          `json:"active"`
	Config    *OpenAICacheDiagnosticConfig  `json:"config,omitempty"`
	ExpiresAt *time.Time                    `json:"expires_at,omitempty"`
	Admitted  int                           `json:"admitted"`
	Samples   []openAICacheDiagnosticSample `json:"samples,omitempty"`
}

type openAICacheDiagnosticBody struct {
	Bytes          int    `json:"bytes"`
	Digest         string `json:"digest,omitempty"`
	CacheKeyDigest string `json:"cache_key_digest,omitempty"`
}

type openAICacheDiagnosticEgress struct {
	AccountID int64                      `json:"account_id"`
	Body      *openAICacheDiagnosticBody `json:"body,omitempty"`
}

type openAICacheDiagnosticSample struct {
	StartedAt        time.Time                     `json:"started_at"`
	Protocol         string                        `json:"protocol"`
	Ingress          openAICacheDiagnosticBody     `json:"ingress"`
	Egress           []openAICacheDiagnosticEgress `json:"http_egress,omitempty"`
	ForwardAttempts  int                           `json:"forward_attempts"`
	LastAccountID    int64                         `json:"last_account_id,omitempty"`
	Outcome          string                        `json:"outcome"`
	FirstTokenMS     *int                          `json:"first_token_ms,omitempty"`
	Usage            *OpenAIUsage                  `json:"forward_usage,omitempty"`
	CacheReadKnown   bool                          `json:"cache_read_known"`
	UsageUnavailable bool                          `json:"usage_unavailable"`
}

type openAICacheDiagnosticSession struct {
	config    OpenAICacheDiagnosticConfig
	expiresAt time.Time
	hashKey   [32]byte
	admitted  int
	samples   []openAICacheDiagnosticSample
}

type openAICacheDiagnosticStore struct {
	mu      sync.Mutex
	current atomic.Pointer[openAICacheDiagnosticSession]
	timer   *time.Timer
}

func (s *OpenAIGatewayService) SetUserCacheDiagnostic(cfg OpenAICacheDiagnosticConfig) error {
	if cfg.UserID <= 0 || !openAICacheDiagnosticModel.MatchString(cfg.Model) ||
		cfg.Minutes < 1 || cfg.Minutes > openAICacheDiagnosticMaxMinutes ||
		cfg.MaxSamples < 1 || cfg.MaxSamples > openAICacheDiagnosticMaxSamples {
		return ErrInvalidOpenAICacheDiagnostic
	}
	next := &openAICacheDiagnosticSession{config: cfg}
	if _, err := rand.Read(next.hashKey[:]); err != nil {
		return err
	}
	d := &s.userCacheDiagnostic
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopLocked()
	ttl := time.Duration(cfg.Minutes) * time.Minute
	next.expiresAt = time.Now().Add(ttl)
	d.current.Store(next)
	d.timer = time.AfterFunc(ttl, func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.current.Load() == next {
			d.stopLocked()
		}
	})
	return nil
}

func (d *openAICacheDiagnosticStore) stopLocked() {
	if current := d.current.Swap(nil); current != nil {
		current.samples = nil
	}
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
}

func (s *OpenAIGatewayService) StopUserCacheDiagnostic() {
	d := &s.userCacheDiagnostic
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopLocked()
}

func (s *OpenAIGatewayService) UserCacheDiagnosticStatus() OpenAICacheDiagnosticStatus {
	d := &s.userCacheDiagnostic
	d.mu.Lock()
	defer d.mu.Unlock()
	session := d.current.Load()
	if session == nil || !time.Now().Before(session.expiresAt) {
		return OpenAICacheDiagnosticStatus{}
	}
	cfg, expires := session.config, session.expiresAt
	return OpenAICacheDiagnosticStatus{
		Active: session.admitted < cfg.MaxSamples,
		Config: &cfg, ExpiresAt: &expires, Admitted: session.admitted,
		Samples: append([]openAICacheDiagnosticSample(nil), session.samples...),
	}
}

type openAICacheDiagnosticContextKey struct{}

// OpenAICacheDiagnostic is owned by one authenticated HTTP request. It keeps
// only fixed-size metadata, hashed request material, and bounded usage samples.
type OpenAICacheDiagnostic struct {
	store   *openAICacheDiagnosticStore
	session *openAICacheDiagnosticSession
	mu      sync.Mutex
	sample  openAICacheDiagnosticSample
	done    bool
}

func (s *OpenAIGatewayService) BeginUserCacheDiagnostic(ctx context.Context, userID int64, model, protocol string, body []byte) (context.Context, *OpenAICacheDiagnostic) {
	if s == nil {
		return ctx, nil
	}
	d := &s.userCacheDiagnostic
	session := d.current.Load()
	if session == nil || session.config.UserID != userID || session.config.Model != model {
		return ctx, nil
	}
	now := time.Now()
	d.mu.Lock()
	if d.current.Load() != session || !now.Before(session.expiresAt) || session.admitted >= session.config.MaxSamples {
		d.mu.Unlock()
		return ctx, nil
	}
	session.admitted++
	d.mu.Unlock()
	observation := &OpenAICacheDiagnostic{
		store: d, session: session, sample: openAICacheDiagnosticSample{
			StartedAt: now, Protocol: protocol, Ingress: session.summarize(body), Outcome: "no_forward_attempt",
		},
	}
	return context.WithValue(ctx, openAICacheDiagnosticContextKey{}, observation), observation
}

func (s *openAICacheDiagnosticSession) summarize(body []byte) openAICacheDiagnosticBody {
	out := openAICacheDiagnosticBody{Bytes: len(body)}
	if len(body) > openAICacheDiagnosticMaxBody {
		return out
	}
	digest := func(domain string, raw []byte) string {
		mac := hmac.New(sha256.New, s.hashKey[:])
		_, _ = mac.Write([]byte(domain))
		_, _ = mac.Write(raw)
		return hex.EncodeToString(mac.Sum(nil))
	}
	out.Digest = digest("body\x00", body)
	if cacheKey := gjson.GetBytes(body, "prompt_cache_key"); cacheKey.Type == gjson.String && len(cacheKey.Raw) <= 4096 {
		out.CacheKeyDigest = digest("cache-key\x00", []byte(cacheKey.String()))
	}
	return out
}

func observeUserCacheDiagnosticHTTP(request *http.Request, accountID int64) {
	d, _ := request.Context().Value(openAICacheDiagnosticContextKey{}).(*OpenAICacheDiagnostic)
	if d == nil || d.store.current.Load() != d.session || !time.Now().Before(d.session.expiresAt) {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.done || len(d.sample.Egress) >= openAICacheDiagnosticMaxEgress {
		return
	}
	entry := openAICacheDiagnosticEgress{AccountID: accountID}
	// Reading GetBody is optional and never consumes the actual request body.
	if request.GetBody != nil && request.ContentLength > 0 && request.ContentLength <= openAICacheDiagnosticMaxBody && request.Header.Get("Content-Encoding") == "" {
		if clone, err := request.GetBody(); err == nil {
			body, readErr := io.ReadAll(io.LimitReader(clone, openAICacheDiagnosticMaxBody+1))
			_ = clone.Close()
			if readErr == nil && len(body) <= openAICacheDiagnosticMaxBody {
				summary := d.session.summarize(body)
				entry.Body = &summary
			}
		}
	}
	d.sample.Egress = append(d.sample.Egress, entry)
}

func (d *OpenAICacheDiagnostic) ObserveResult(accountID int64, result *OpenAIForwardResult, forwardErr error) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.done {
		return
	}
	s := &d.sample
	s.ForwardAttempts++
	s.LastAccountID = accountID
	s.Usage, s.FirstTokenMS = nil, nil
	s.CacheReadKnown, s.UsageUnavailable = false, false
	s.Outcome = "forward_failed"
	if result == nil {
		return
	}
	usage := result.Usage
	s.Usage, s.CacheReadKnown, s.UsageUnavailable = &usage, usage.CacheReadKnown, result.UsageUnavailable
	if result.FirstTokenMs != nil {
		ms := *result.FirstTokenMs
		s.FirstTokenMS = &ms
	}
	switch {
	case result.ClientDisconnect:
		s.Outcome = "client_disconnected"
	case result.streamReadIncomplete:
		s.Outcome = "incomplete_stream"
	case forwardErr == nil && !result.upstreamFailed:
		s.Outcome = "forward_completed"
	}
}

func (d *OpenAICacheDiagnostic) Finish(canceled bool) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.done {
		return
	}
	d.done = true
	d.store.mu.Lock()
	defer d.store.mu.Unlock()
	if d.store.current.Load() != d.session || !time.Now().Before(d.session.expiresAt) {
		return
	}
	if canceled {
		d.sample.Outcome = "request_canceled"
	}
	d.session.samples = append(d.session.samples, d.sample)
}
