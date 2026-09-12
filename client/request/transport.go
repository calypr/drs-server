package request

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	conf "github.com/calypr/syfon/client/config"
	"github.com/calypr/syfon/client/logs"
	"github.com/hashicorp/go-retryablehttp"
)

var (
	defaultRetryWaitMin = 5 * time.Second
	defaultRetryWaitMax = 15 * time.Second
)

type Client struct {
	standard  *http.Client
	retry     *retryablehttp.Client
	userAgent string
	logger    *logs.Gen3Logger
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type redactingLogger struct {
	logger *logs.Gen3Logger
}

func (l redactingLogger) Error(msg string, keysAndValues ...interface{}) {
	if l.logger == nil || l.logger.Logger == nil {
		return
	}
	l.logger.Error(redactLogText(msg), redactLogValues(keysAndValues)...)
}

func (l redactingLogger) Info(msg string, keysAndValues ...interface{}) {
	if l.logger == nil || l.logger.Logger == nil {
		return
	}
	l.logger.Info(redactLogText(msg), redactLogValues(keysAndValues)...)
}

func (l redactingLogger) Debug(msg string, keysAndValues ...interface{}) {
	if l.logger == nil || l.logger.Logger == nil {
		return
	}
	l.logger.Debug(redactLogText(msg), redactLogValues(keysAndValues)...)
}

func (l redactingLogger) Warn(msg string, keysAndValues ...interface{}) {
	if l.logger == nil || l.logger.Logger == nil {
		return
	}
	l.logger.Warn(redactLogText(msg), redactLogValues(keysAndValues)...)
}

func (l redactingLogger) Printf(format string, args ...interface{}) {
	if l.logger == nil || l.logger.Logger == nil {
		return
	}
	l.logger.Printf("%s", redactLogText(fmt.Sprintf(format, args...)))
}

func redactLogValues(values []interface{}) []interface{} {
	redacted := make([]interface{}, len(values))
	for i, value := range values {
		switch value := value.(type) {
		case string:
			redacted[i] = redactLogText(value)
		case *url.URL:
			if value == nil {
				redacted[i] = value
				continue
			}
			redacted[i] = redactURL(value)
		case url.URL:
			redacted[i] = redactURL(&value)
		case error:
			if safe := redactLogText(value.Error()); safe != value.Error() {
				redacted[i] = redactedError{err: value, message: safe}
				continue
			}
			redacted[i] = value
		default:
			redacted[i] = value
		}
	}
	return redacted
}

type redactedError struct {
	err     error
	message string
}

func (e redactedError) Error() string {
	return e.message
}

func (e redactedError) Unwrap() error {
	return e.err
}

var logURLPattern = regexp.MustCompile(`(?i)https?://[^\s]+`)

func redactLogText(text string) string {
	return logURLPattern.ReplaceAllStringFunc(text, func(candidate string) string {
		suffix := ""
		for len(candidate) > 0 && strings.ContainsRune(".,;:!?)]}\"'", rune(candidate[len(candidate)-1])) {
			suffix = candidate[len(candidate)-1:] + suffix
			candidate = candidate[:len(candidate)-1]
		}
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.Host == "" {
			return candidate + suffix
		}
		return redactURL(parsed) + suffix
	})
}

func redactURL(raw *url.URL) string {
	if raw == nil {
		return ""
	}
	redacted := *raw
	if _, hasPassword := redacted.User.Password(); hasPassword {
		redacted.User = url.UserPassword(redacted.User.Username(), "xxxxx")
	}
	redacted.RawQuery = ""
	redacted.Fragment = ""
	return redacted.String()
}

func NewClient(
	logger *logs.Gen3Logger,
	cred *conf.Credential,
	manager conf.ManagerInterface,
	userAgent string,
	baseHTTPClient *http.Client,
	mode AuthMode,
) *Client {
	if logger == nil {
		logger = logs.NewGen3Logger(nil)
	}

	var baseTransport http.RoundTripper
	if baseHTTPClient != nil && baseHTTPClient.Transport != nil {
		baseTransport = baseHTTPClient.Transport
	} else {
		baseTransport = &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   100,
			TLSHandshakeTimeout:   30 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
		}
	}

	authTransport := &AuthTransport{
		Base:    baseTransport,
		Manager: manager,
		Cred:    cred,
		Mode:    mode,
	}
	standard := &http.Client{
		Timeout:   0,
		Transport: authTransport,
	}
	if baseHTTPClient != nil {
		standard.Timeout = baseHTTPClient.Timeout
		standard.Jar = baseHTTPClient.Jar
		standard.CheckRedirect = baseHTTPClient.CheckRedirect
	}

	retry := retryablehttp.NewClient()
	retry.ErrorHandler = func(resp *http.Response, err error, _ int) (*http.Response, error) {
		if err != nil {
			if resp != nil {
				_ = resp.Body.Close()
			}
			return nil, err
		}
		return resp, nil
	}
	retry.RetryMax = 5
	retry.Logger = redactingLogger{logger: logger}
	retry.RetryWaitMin = defaultRetryWaitMin
	retry.RetryWaitMax = defaultRetryWaitMax
	retry.HTTPClient = standard
	retry.CheckRetry = func(ctx context.Context, resp *http.Response, err error) (bool, error) {
		shouldRetry, retryErr := retryablehttp.DefaultRetryPolicy(ctx, resp, err)
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			if authTransport.Mode != AuthModeBearer || authTransport.Cred == nil || strings.TrimSpace(authTransport.Cred.APIEndpoint) == "" || strings.TrimSpace(authTransport.Cred.APIKey) == "" {
				return shouldRetry, retryErr
			}
			if resp.Request == nil {
				return shouldRetry, retryErr
			}
			if authState, ok := resp.Request.Context().Value(authRequestContextKey{}).(authRequestContext); ok && (authState.skipAuth || authState.explicitAuth) {
				return shouldRetry, retryErr
			}
			rejectedToken := bearerToken(resp.Request.Header.Get("Authorization"))
			if refreshErr := authTransport.refreshIfCurrent(ctx, rejectedToken); refreshErr != nil {
				return false, refreshErr
			}
			return true, nil
		}
		return shouldRetry, retryErr
	}

	return &Client{
		standard:  standard,
		retry:     retry,
		userAgent: strings.TrimSpace(userAgent),
		logger:    logger,
	}
}

// Do performs an HTTP request, retrying only bodyless GET, HEAD, and OPTIONS
// requests. The caller owns the response body.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}

	clone := req.Clone(req.Context())
	if c.userAgent != "" && clone.Header.Get("User-Agent") == "" {
		clone.Header.Set("User-Agent", c.userAgent)
	}
	if clone.Header.Get("Accept") == "" {
		clone.Header.Set("Accept", "application/json")
	}

	if !canRetry(clone) {
		return c.standard.Do(clone)
	}
	retryReq, err := retryablehttp.FromRequest(clone)
	if err != nil {
		return nil, err
	}
	return c.retry.Do(retryReq)
}

func (c *Client) StandardClient() *http.Client {
	return c.standard
}

func (c *Client) Logger() *logs.Gen3Logger {
	return c.logger
}

func canRetry(req *http.Request) bool {
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
	default:
		return false
	}
	return req.Body == nil || req.Body == http.NoBody
}

func bearerToken(header string) string {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
