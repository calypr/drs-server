package request

//go:generate mockgen -destination=../internal/testmocks/requester_mock.go -package=testmocks github.com/calypr/syfon/client/request Requester

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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

type Request struct {
	Logs        *logs.Gen3Logger
	RetryClient *retryablehttp.Client
	Auth        *AuthTransport

	BaseURL   string
	UserAgent string
}

type ResponseError struct {
	Method  string
	URL     string
	Status  int
	Body    string
	Headers http.Header
}

func (e *ResponseError) Error() string {
	return fmt.Sprintf("%s %s: status %d body=%s", e.Method, e.URL, e.Status, e.Body)
}

type RequestOption func(*RequestBuilder)

func WithQueryValues(v url.Values) RequestOption {
	return func(rb *RequestBuilder) {
		rb.WithQueryValues(v)
	}
}

func WithHeader(key, value string) RequestOption {
	return func(rb *RequestBuilder) {
		rb.WithHeader(key, value)
	}
}

func WithTimeout(d time.Duration) RequestOption {
	return func(rb *RequestBuilder) {
		rb.WithTimeout(d)
	}
}

func WithSkipAuth(skip bool) RequestOption {
	return func(rb *RequestBuilder) {
		rb.WithSkipAuth(skip)
	}
}

func WithNoRetry(noRetry bool) RequestOption {
	return func(rb *RequestBuilder) {
		rb.WithNoRetry(noRetry)
	}
}

func WithPartSize(size int64) RequestOption {
	return func(rb *RequestBuilder) {
		rb.PartSize = size
	}
}

type Requester interface {
	Do(ctx context.Context, method, path string, body, out any, opts ...RequestOption) error
}

func NewBasicAuthRequestor(
	logger *logs.Gen3Logger,
	cred *conf.Credential,
	manager conf.ManagerInterface,
	baseURL string,
	userAgent string,
	baseHTTPClient *http.Client,
) Requester {
	return newRequestor(logger, cred, manager, baseURL, userAgent, baseHTTPClient, AuthModeBasic)
}

func NewBearerTokenRequestor(
	logger *logs.Gen3Logger,
	cred *conf.Credential,
	manager conf.ManagerInterface,
	baseURL string,
	userAgent string,
	baseHTTPClient *http.Client,
) Requester {
	return newRequestor(logger, cred, manager, baseURL, userAgent, baseHTTPClient, AuthModeBearer)
}

func newRequestor(
	logger *logs.Gen3Logger,
	cred *conf.Credential,
	manager conf.ManagerInterface,
	baseURL string,
	userAgent string,
	baseHTTPClient *http.Client,
	mode AuthMode,
) Requester {
	if logger == nil {
		logger = logs.NewGen3Logger(nil, "", "")
	}
	retryClient := retryablehttp.NewClient()
	retryClient.RetryMax = 5
	retryClient.Logger = logger
	retryClient.RetryWaitMin = defaultRetryWaitMin
	retryClient.RetryWaitMax = defaultRetryWaitMax

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
		Mode:    mode,
	}
	authTransport.Cred = cred
	retryClient.HTTPClient = &http.Client{
		Timeout:   0,
		Transport: authTransport,
	}
	if baseHTTPClient != nil {
		retryClient.HTTPClient.Timeout = baseHTTPClient.Timeout
		retryClient.HTTPClient.Jar = baseHTTPClient.Jar
		retryClient.HTTPClient.CheckRedirect = baseHTTPClient.CheckRedirect
	}

	retryClient.CheckRetry = func(ctx context.Context, resp *http.Response, err error) (bool, error) {
		shouldRetry, retryErr :=
			retryablehttp.DefaultRetryPolicy(ctx, resp, err)

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
			err := authTransport.refreshIfCurrent(ctx, rejectedToken)
			if err != nil {
				return false, err
			}
			return true, nil
		}
		return shouldRetry, retryErr
	}

	r := &Request{
		RetryClient: retryClient,
		Auth:        authTransport,
		Logs:        logger,
		BaseURL:     strings.TrimRight(baseURL, "/"),
		UserAgent:   userAgent,
	}
	return r
}

// Do executes a request and decodes its response. Only bodyless GET, HEAD, and
// OPTIONS requests are retried; WithNoRetry disables those retries.
func (r *Request) Do(ctx context.Context, method, path string, body, out any, opts ...RequestOption) error {
	rb := r.newBuilder(method, path)
	if body != nil {
		if reader, ok := body.(io.Reader); ok {
			rb.WithBody(reader)
		} else {
			if _, err := rb.WithJSONBody(body); err != nil {
				return err
			}
		}
	}

	for _, opt := range opts {
		opt(rb)
	}

	if rb.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, rb.Timeout)
		defer cancel()
	}

	httpReq, err := http.NewRequestWithContext(ctx, rb.Method, rb.Url, rb.Body)
	if err != nil {
		return errors.New("failed to create HTTP request: " + err.Error())
	}

	// Apply default headers
	if r.UserAgent != "" {
		httpReq.Header.Set("User-Agent", r.UserAgent)
	}
	httpReq.Header.Set("Accept", "application/json")

	// Apply specific headers from builder
	for key, value := range rb.Headers {
		httpReq.Header.Set(key, value)
	}

	if rb.SkipAuth {
		httpReq.Header.Set("X-Skip-Auth", "true")
	}

	if !rb.SkipAuth && rb.Token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+rb.Token)
	}

	if rb.PartSize != 0 {
		httpReq.ContentLength = rb.PartSize
	}

	if rb.NoRetry || !requestCanRetry(httpReq) {
		resp, err := r.RetryClient.HTTPClient.Do(httpReq)
		if err != nil {
			if resp != nil {
				resp.Body.Close()
			}
			return errors.New("request failed: " + err.Error())
		}
		return r.handleResponse(method, resp, out)
	}

	retryReq, err := retryablehttp.FromRequest(httpReq)
	if err != nil {
		return err
	}

	resp, err := r.RetryClient.Do(retryReq)
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		return errors.New("request failed after retries: " + err.Error())
	}

	return r.handleResponse(method, resp, out)
}

func requestCanRetry(req *http.Request) bool {
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

func (r *Request) handleResponse(method string, resp *http.Response, out any) error {
	// Polymorphic Response Handling
	switch v := out.(type) {
	case **http.Response:
		// Raw Mode: Caller is responsible for closing the body.
		*v = resp
		return nil

	default:
		// JSON/Void Mode: We close the body.
		defer resp.Body.Close()

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("read response body: %w", err)
		}
		if resp.StatusCode >= 400 {
			return &ResponseError{
				Method:  method,
				URL:     resp.Request.URL.String(),
				Status:  resp.StatusCode,
				Body:    strings.TrimSpace(string(data)),
				Headers: resp.Header.Clone(),
			}
		}

		if out != nil && len(data) > 0 {
			if err := json.Unmarshal(data, out); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
		}
		return nil
	}
}

func (r *Request) newBuilder(method, path string) *RequestBuilder {
	fullURL := path
	if !strings.HasPrefix(path, "http") {
		fullURL = r.BaseURL + "/" + strings.TrimLeft(path, "/")
	}
	return &RequestBuilder{
		Method:  method,
		Url:     fullURL,
		Headers: make(map[string]string),
	}
}
