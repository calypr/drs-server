package client

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/client/bucketapi"
	"github.com/calypr/syfon/apigen/client/drs"
	"github.com/calypr/syfon/apigen/client/internalapi"
	"github.com/calypr/syfon/apigen/client/lfsapi"
	"github.com/calypr/syfon/apigen/client/metricsapi"
	conf "github.com/calypr/syfon/client/config"
	"github.com/calypr/syfon/client/logs"
	"github.com/calypr/syfon/client/request"
	syfonclient "github.com/calypr/syfon/client/services"
)

const (
	defaultAddress = "http://127.0.0.1:8080"
	defaultUA      = "syfon-client/0"
)

type Config struct {
	Address    string
	HTTPClient *http.Client
	UserAgent  string
	BasicAuth  *BasicAuth
	Token      string
}

type BasicAuth struct {
	Username string
	Password string
}

// Client implements syfonclient.SyfonClient
type Client struct {
	requestor request.Requester
	baseURL   string

	health  *syfonclient.HealthService
	data    *syfonclient.DataService
	index   *syfonclient.IndexService
	drs     *syfonclient.DRSService
	buckets *syfonclient.BucketsService
	metrics *syfonclient.MetricsService
	lfs     *syfonclient.LFSService

	// Generated schema-specific clients
	drsGen      *drs.ClientWithResponses
	lfsGen      *lfsapi.ClientWithResponses
	internalGen *internalapi.ClientWithResponses
	bucketGen   *bucketapi.ClientWithResponses
	metricsGen  *metricsapi.ClientWithResponses
}

type Option func(*Config)



func WithBasicAuth(user, pass string) Option {
	return func(c *Config) {
		user = strings.TrimSpace(user)
		if user == "" {
			c.BasicAuth = nil
			return
		}
		c.BasicAuth = &BasicAuth{Username: user, Password: pass}
	}
}

func WithBearerToken(token string) Option {
	return func(c *Config) {
		c.Token = strings.TrimSpace(token)
	}
}

func DefaultConfig() *Config {
	return &Config{
		Address: defaultAddress,
		// SECURITY FIX INFO-3: Set reasonable timeout for overall client (10 minutes for large transfers)
		HTTPClient: &http.Client{Timeout: 10 * time.Minute},
		UserAgent:  defaultUA,
	}
}

func New(baseURL string, opts ...Option) (syfonclient.SyfonClient, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8080"
	}
	if !strings.Contains(baseURL, "://") {
		baseURL = "http://" + baseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")

	cfg := DefaultConfig()
	cfg.Address = baseURL
	for _, opt := range opts {
		opt(cfg)
	}
	return NewClient(cfg)
}

func NewClient(cfg *Config) (*Client, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	bu, err := parseBaseURL(cfg.Address)
	if err != nil {
		return nil, err
	}

	userAgent := strings.TrimSpace(cfg.UserAgent)
	if userAgent == "" {
		userAgent = defaultUA
	}

	// Initialize the hardened requestor
	cred := &conf.Credential{
		AccessToken: cfg.Token,
	}
	if cfg.BasicAuth != nil {
		cred.KeyID = cfg.BasicAuth.Username
		cred.APIKey = cfg.BasicAuth.Password
	}
	var req request.Requester
	if cfg.Token != "" {
		req = request.NewBearerTokenRequestor(nil, cred, nil, bu, userAgent, cfg.HTTPClient)
	} else {
		req = request.NewBasicAuthRequestor(nil, cred, nil, bu, userAgent, cfg.HTTPClient)
	}

	client := &Client{
		requestor: req,
		baseURL:   bu,
	}
	if err := client.initServices(); err != nil {
		return nil, err
	}
	return client, nil
}

func parseBaseURL(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		addr = defaultAddress
	}
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil {
		return "", fmt.Errorf("parse address: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid address %q", addr)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func (c *Client) initServices() error {
	l := c.Logger()

	server := c.baseURL
	drsServer := strings.TrimRight(server+"/ga4gh/drs/v1", "/")
	httpClient := c.HTTPClient()

	var err error
	if c.drsGen, err = drs.NewClientWithResponses(drsServer, drs.WithHTTPClient(httpClient)); err != nil {
		return fmt.Errorf("initialize drs client: %w", err)
	}
	if c.lfsGen, err = lfsapi.NewClientWithResponses(server, lfsapi.WithHTTPClient(httpClient)); err != nil {
		return fmt.Errorf("initialize lfs client: %w", err)
	}
	if c.internalGen, err = internalapi.NewClientWithResponses(server, internalapi.WithHTTPClient(httpClient)); err != nil {
		return fmt.Errorf("initialize internal client: %w", err)
	}
	if c.bucketGen, err = bucketapi.NewClientWithResponses(server, bucketapi.WithHTTPClient(httpClient)); err != nil {
		return fmt.Errorf("initialize bucket client: %w", err)
	}
	if c.metricsGen, err = metricsapi.NewClientWithResponses(server, metricsapi.WithHTTPClient(httpClient)); err != nil {
		return fmt.Errorf("initialize metrics client: %w", err)
	}

	c.health = syfonclient.NewHealthService(c.requestor)
	c.index = syfonclient.NewIndexService(c.internalGen, c.requestor)
	c.drs = syfonclient.NewDRSService(c.drsGen, c.index)
	c.lfs = syfonclient.NewLFSService(c.lfsGen)
	c.data = syfonclient.NewDataService(c.internalGen, c.requestor, l, c.drs)
	c.buckets = syfonclient.NewBucketsService(c.bucketGen)
	c.metrics = syfonclient.NewMetricsService(c.metricsGen)
	return nil
}

func (c *Client) HTTPClient() *http.Client {
	if r, ok := c.requestor.(*request.Request); ok {
		return r.RetryClient.HTTPClient
	}
	return http.DefaultClient
}

func (c *Client) Address() string { return c.baseURL }

func (c *Client) Health() *syfonclient.HealthService   { return c.health }
func (c *Client) Data() *syfonclient.DataService       { return c.data }
func (c *Client) Index() *syfonclient.IndexService     { return c.index }
func (c *Client) DRS() *syfonclient.DRSService         { return c.drs }
func (c *Client) Buckets() *syfonclient.BucketsService { return c.buckets }
func (c *Client) Metrics() *syfonclient.MetricsService { return c.metrics }
func (c *Client) LFS() *syfonclient.LFSService         { return c.lfs }

// Schema-specific generated clients
func (c *Client) LFSAPI() *lfsapi.ClientWithResponses           { return c.lfsGen }
func (c *Client) InternalAPI() *internalapi.ClientWithResponses { return c.internalGen }
func (c *Client) BucketAPI() *bucketapi.ClientWithResponses     { return c.bucketGen }
func (c *Client) MetricsAPI() *metricsapi.ClientWithResponses   { return c.metricsGen }
func (c *Client) DRSAPI() *drs.ClientWithResponses              { return c.drsGen }

func (c *Client) Requestor() request.Requester { return c.requestor }

func (c *Client) Logger() *logs.Gen3Logger {
	if r, ok := c.requestor.(*request.Request); ok {
		return r.Logs
	}
	return logs.NewGen3Logger(nil, "", "")
}
