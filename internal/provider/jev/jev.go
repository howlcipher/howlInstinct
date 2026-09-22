package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"github.com/howlcipher/howlinstinct/pkg/instinct"
)

// Name is the adapter's stable identifier, recorded in receipts.
const Name = "jev"

// statusOverloaded is the non-standard overload status used by this contract.
const statusOverloaded = 529

// Provider speaks the Jev-compatible System One contract over HTTP.
type Provider struct {
	cfg    Config
	client *http.Client

	// sleep is injectable so retry behaviour can be tested without waiting.
	sleep func(ctx context.Context, d time.Duration) error
}

// New builds a Provider. The endpoint is validated eagerly so that a
// misconfiguration surfaces at startup rather than at the first decision.
func New(cfg Config) (*Provider, error) {
	if cfg.Path == "" {
		cfg.Path = DefaultPath
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultConfig().Timeout
	}
	if cfg.MaxResponseBytes <= 0 {
		cfg.MaxResponseBytes = DefaultConfig().MaxResponseBytes
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	if _, err := cfg.endpoint(); err != nil {
		return nil, err
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	}
	return &Provider{cfg: cfg, client: client, sleep: sleepCtx}, nil
}

// Name identifies the adapter.
func (p *Provider) Name() string { return Name }

// Endpoint returns the credential-free endpoint this provider will call.
func (p *Provider) Endpoint() string {
	ep, err := p.cfg.endpoint()
	if err != nil {
		return ""
	}
	return ep
}

// Config returns the adapter's configuration. It contains no secrets.
func (p *Provider) Config() Config { return p.cfg }

// Decide asks the endpoint every question in req.
func (p *Provider) Decide(ctx context.Context, req instinct.Request) (instinct.Response, error) {
	endpoint, err := p.cfg.endpoint()
	if err != nil {
		return instinct.Response{}, err
	}
	apiKey, err := p.cfg.apiKey()
	if err != nil {
		return instinct.Response{}, err
	}

	payload, err := json.Marshal(buildRequest(req, p.cfg.Model))
	if err != nil {
		return instinct.Response{}, instinct.Wrap(instinct.KindInvalidInput, "jev", err,
			"encoding request")
	}

	start := time.Now()
	body, err := p.send(ctx, endpoint, apiKey, payload)
	if err != nil {
		return instinct.Response{}, err
	}

	resp, err := parseResponse(body, req, p.cfg.StrictSchema)
	if err != nil {
		return instinct.Response{}, err
	}
	resp.Provider = Name
	resp.Endpoint = endpoint
	resp.LatencyMS = time.Since(start).Milliseconds()
	if resp.Model == "" {
		resp.Model = p.cfg.Model
	}
	return resp, nil
}

// send performs the HTTP call, retrying only what is safe to retry.
func (p *Provider) send(ctx context.Context, endpoint, apiKey string, payload []byte) ([]byte, error) {
	var lastErr error

	for attempt := 0; attempt <= p.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			if err := p.sleep(ctx, backoff(attempt, lastRetryAfter(lastErr))); err != nil {
				return nil, instinct.Wrap(instinct.KindTimeout, "jev", err,
					"deadline elapsed while backing off before retry %d", attempt)
			}
		}

		body, err := p.attempt(ctx, endpoint, apiKey, payload)
		if err == nil {
			return body, nil
		}
		lastErr = err

		// Anything not explicitly marked transient is final. Retrying a
		// rejected request, a bad credential, or a response that failed
		// validation cannot succeed, and only multiplies load on a service
		// that is often already struggling.
		if !isRetryable(err) {
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, instinct.Wrap(instinct.KindTimeout, "jev", ctx.Err(),
				"deadline elapsed before retrying")
		}
	}
	return nil, lastErr
}

// attempt performs exactly one HTTP request.
func (p *Provider) attempt(ctx context.Context, endpoint, apiKey string, payload []byte) ([]byte, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, instinct.Wrap(instinct.KindConfiguration, "jev", err, "building request")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, instinct.Wrap(instinct.KindTimeout, "jev", ctx.Err(), "request deadline elapsed")
		}
		// A transport failure is retried because the request may never have
		// reached the provider at all.
		//
		// The error text can contain the request URL. It cannot contain the
		// credential: the credential only ever travels in a header, and the
		// endpoint has already been stripped of userinfo and query string.
		return nil, retryable(0,
			instinct.Wrap(instinct.KindProviderUnavailable, "jev", err, "calling provider"))
	}
	defer func() {
		// Drain a bounded amount so the connection can be reused, then close.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()

	// Bound the read before trusting any of it. A provider is untrusted
	// network input and must not be able to exhaust memory by replying with
	// an arbitrarily large body.
	body, err := io.ReadAll(io.LimitReader(resp.Body, p.cfg.MaxResponseBytes+1))
	if err != nil {
		return nil, retryable(0,
			instinct.Wrap(instinct.KindProviderUnavailable, "jev", err, "reading response"))
	}
	if int64(len(body)) > p.cfg.MaxResponseBytes {
		return nil, instinct.Errorf(instinct.KindProviderResponseInvalid, "jev",
			"response body exceeds the %d byte limit", p.cfg.MaxResponseBytes)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp, body)
	}
	return body, nil
}

// statusError maps an HTTP status onto a classified error, and decides
// whether that status is worth repeating.
func statusError(resp *http.Response, body []byte) error {
	detail := describeError(body)
	after := parseRetryAfter(resp.Header.Get("Retry-After"))

	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		// Framed as configuration rather than availability: the service is
		// reachable and working, the credential is the problem, and the
		// person who can fix it is the operator.
		return instinct.Errorf(instinct.KindConfiguration, "jev",
			"provider rejected the credential (HTTP %d)%s", resp.StatusCode, detail)

	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return instinct.Errorf(instinct.KindInvalidInput, "jev",
			"provider rejected the request as invalid (HTTP %d)%s", resp.StatusCode, detail)

	case http.StatusTooManyRequests:
		return retryable(after, instinct.Errorf(instinct.KindProviderUnavailable, "jev",
			"provider is rate limiting (HTTP 429)%s", detail))

	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, statusOverloaded:
		return retryable(after, instinct.Errorf(instinct.KindProviderUnavailable, "jev",
			"provider is unavailable (HTTP %d)%s", resp.StatusCode, detail))

	default:
		// Including 500: an unqualified internal error may well be
		// deterministic, and repeating it is as likely to hurt as help.
		return instinct.Errorf(instinct.KindProviderUnavailable, "jev",
			"provider returned HTTP %d%s", resp.StatusCode, detail)
	}
}

// retryableError marks a failure as plausibly transient.
//
// Retryability is carried by an explicit wrapper rather than inferred from an
// error's kind. Inference would mean any future error classified as "provider
// unavailable" silently becomes retryable, which is the kind of accident that
// turns a provider wobble into an outage. A failure is repeated only because
// a specific code path decided it should be.
type retryableError struct {
	err        *instinct.Error
	retryAfter time.Duration
}

func (e *retryableError) Error() string { return e.err.Error() }

// Unwrap keeps the classified error reachable by errors.As, so a retryable
// failure still reports its kind to the exit-code mapping.
func (e *retryableError) Unwrap() error { return e.err }

func retryable(d time.Duration, err *instinct.Error) error {
	return &retryableError{err: err, retryAfter: d}
}

func isRetryable(err error) bool {
	var r *retryableError
	return errors.As(err, &r)
}

func lastRetryAfter(err error) time.Duration {
	var r *retryableError
	if errors.As(err, &r) {
		return r.retryAfter
	}
	return 0
}

// backoff computes the delay before a retry.
//
// Full jitter, not plain exponential: clients retrying on identical schedules
// are what turns a brief provider wobble into a thundering herd. The cap
// bounds the worst case, and the caller's context bounds it absolutely
// regardless of what is computed here.
func backoff(attempt int, retryAfter time.Duration) time.Duration {
	const (
		base    = 200 * time.Millisecond
		ceiling = 2 * time.Second
		// A hostile or broken provider could ask us to wait for hours.
		// Honour Retry-After only as far as is operationally sane.
		maxRetryAfter = 5 * time.Second
	)
	if retryAfter > 0 {
		if retryAfter > maxRetryAfter {
			return maxRetryAfter
		}
		return retryAfter
	}
	window := base << uint(attempt-1)
	if window > ceiling {
		window = ceiling
	}
	// #nosec G404 -- jitter only needs to decorrelate retries between
	// processes, not resist an adversary; a cryptographic source buys nothing.
	return time.Duration(rand.Int63n(int64(window))) + base/2
}

func parseRetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(h); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// describeError extracts the upstream error envelope when there is one.
//
// It returns a bounded, sanitized excerpt rather than the whole body, because
// an error body is attacker-influenced text that ends up in logs and
// terminals.
func describeError(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var we wireError
	if err := json.Unmarshal(body, &we); err == nil && we.Detail.Message != "" {
		return ": " + sanitize(we.Detail.ErrorType+" "+we.Detail.Message, 200)
	}
	return ": " + sanitize(string(body), 200)
}

// sanitize strips control characters and truncates. Control characters are
// removed because provider text reaches log files and terminals, where they
// can forge log structure or drive escape sequences.
func sanitize(s string, n int) string {
	cleaned := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			cleaned = append(cleaned, ' ')
		case r < 0x20 || r == 0x7f:
			// dropped
		default:
			cleaned = append(cleaned, r)
		}
	}
	if len(cleaned) > n {
		return string(cleaned[:n]) + "..."
	}
	return string(cleaned)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
