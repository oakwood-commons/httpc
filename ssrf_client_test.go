// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package httpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ssrfTestConfig returns a config suitable for talking to an httptest server
// while keeping caching and retries out of the way.
func ssrfTestConfig() *ClientConfig {
	cfg := DefaultConfig()
	cfg.EnableCache = false
	cfg.RetryMax = 0
	return cfg
}

func TestClientBlocksPrivateURLByDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(ssrfTestConfig())
	resp, err := client.Get(context.Background(), server.URL)
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
	assert.Contains(t, err.Error(), "private/reserved")
}

func TestClientIPPolicyAllowsExceptedRange(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	policy, err := NewIPPolicy("127.0.0.0/8", "::1/128")
	require.NoError(t, err)

	cfg := ssrfTestConfig()
	cfg.IPPolicy = policy
	client := NewClient(cfg)

	resp, err := client.Get(context.Background(), server.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestClientEnforcesPolicyAtDialTime bypasses Client.Do (and therefore the URL
// check) by using the underlying *http.Client directly. The request must still
// fail, proving the policy is wired into the dialer and not just into URL
// validation -- this is what protects against a hostname that resolves to a
// private address.
func TestClientEnforcesPolicyAtDialTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(ssrfTestConfig())

	// A hostname, not the listener's IP literal: URL-level validation is
	// bypassed here, so only resolution-time enforcement can catch this. If the
	// dialer hook were removed, "localhost" would resolve to 127.0.0.1 and the
	// request would succeed.
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	require.NoError(t, err)
	target := "http://" + net.JoinHostPort("localhost", port) + "/"

	resp, err := client.StandardClient().Get(target) //nolint:noctx // exercising the dialer, not context handling
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
	assert.Contains(t, err.Error(), "private/reserved")
}

// TestClientDialTimePolicyAllowsException is the positive counterpart: an
// allowed range must still connect once the dialer hook is installed.
func TestClientDialTimePolicyAllowsException(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := ssrfTestConfig()
	cfg.AllowPrivateIPs = true
	client := NewClient(cfg)

	resp, err := client.StandardClient().Get(server.URL) //nolint:noctx // exercising the dialer, not context handling
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestClientBlocksRedirectToMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer server.Close()

	cfg := ssrfTestConfig()
	cfg.AllowPrivateIPs = true // loopback is reachable; metadata must not be
	client := NewClient(cfg)

	resp, err := client.Get(context.Background(), server.URL)
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cloud metadata")
}

func TestClientConfigIPPolicyPrecedence(t *testing.T) {
	// Explicit policy wins over the deprecated boolean.
	policy, err := NewIPPolicy("10.0.0.0/8")
	require.NoError(t, err)
	cfg := &ClientConfig{AllowPrivateIPs: true, IPPolicy: policy}
	require.Error(t, cfg.ipPolicy().ValidateURL("http://127.0.0.1/"))
	require.NoError(t, cfg.ipPolicy().ValidateURL("http://10.0.0.1/"))

	// The boolean is still honoured when no policy is set.
	legacy := &ClientConfig{AllowPrivateIPs: true}
	require.NoError(t, legacy.ipPolicy().ValidateURL("http://127.0.0.1/"))

	// Zero value denies.
	require.Error(t, (&ClientConfig{}).ipPolicy().ValidateURL("http://127.0.0.1/"))
}

func TestAppConfigAllowedPrivateCIDRs(t *testing.T) {
	cfg := &AppConfig{AllowedPrivateCIDRs: []string{"10.0.0.0/8"}}
	client, err := NewClientFromAppConfig(cfg, logr.Discard())
	require.NoError(t, err)
	require.NotNil(t, client.config.IPPolicy)
	require.NoError(t, client.config.ipPolicy().ValidateURL("http://10.0.0.1/"))
	require.Error(t, client.config.ipPolicy().ValidateURL("http://127.0.0.1/"))

	_, err = NewClientFromAppConfig(&AppConfig{AllowedPrivateCIDRs: []string{"nope"}}, logr.Discard())
	require.Error(t, err)
}

func TestAppConfigMergeAllowedPrivateCIDRs(t *testing.T) {
	base := &AppConfig{AllowedPrivateCIDRs: []string{"10.0.0.0/8"}}
	override := &AppConfig{AllowedPrivateCIDRs: []string{"192.168.0.0/16"}}
	merged := MergeAppConfig(base, override)
	assert.Equal(t, []string{"192.168.0.0/16"}, merged.AllowedPrivateCIDRs)

	unchanged := MergeAppConfig(base, &AppConfig{})
	assert.Equal(t, []string{"10.0.0.0/8"}, unchanged.AllowedPrivateCIDRs)

	// A non-nil empty override means "no exceptions" and must clear the base
	// list rather than read as "not specified".
	cleared := MergeAppConfig(base, &AppConfig{AllowedPrivateCIDRs: []string{}})
	assert.Empty(t, cleared.AllowedPrivateCIDRs)
	assert.NotNil(t, cleared.AllowedPrivateCIDRs)
}

func TestAppConfigEmptyAllowedPrivateCIDRsWinsOverLegacyBoolean(t *testing.T) {
	allow := true
	cfg := &AppConfig{AllowPrivateIPs: &allow, AllowedPrivateCIDRs: []string{}}

	client, err := NewClientFromAppConfig(cfg, logr.Discard())
	require.NoError(t, err)
	require.NotNil(t, client.config.IPPolicy, "an explicit empty CIDR list must still set a policy")
	assert.Error(t, client.config.ipPolicy().ValidateURL("http://127.0.0.1/"))
	assert.Error(t, client.config.ipPolicy().ValidateURL("http://10.0.0.1/"))
}

// TestAppConfigTrustProxyResolution pins both postures through the config
// surface, including that the flag applies without any CIDR list being set.
func TestAppConfigTrustProxyResolution(t *testing.T) {
	// Absent: secure default, fail closed.
	client, err := NewClientFromAppConfig(&AppConfig{}, logr.Discard())
	require.NoError(t, err)
	assert.False(t, client.config.ipPolicy().TrustProxyResolution)

	trust := true
	client, err = NewClientFromAppConfig(&AppConfig{TrustProxyResolution: &trust}, logr.Discard())
	require.NoError(t, err)
	assert.True(t, client.config.ipPolicy().TrustProxyResolution)
	// The implicit policy must not have been mutated in place.
	assert.False(t, defaultIPPolicy.TrustProxyResolution)

	deny := false
	client, err = NewClientFromAppConfig(
		&AppConfig{TrustProxyResolution: &deny, AllowedPrivateCIDRs: []string{"10.0.0.0/8"}},
		logr.Discard(),
	)
	require.NoError(t, err)
	assert.False(t, client.config.ipPolicy().TrustProxyResolution)
	assert.NoError(t, client.config.ipPolicy().ValidateURL("http://10.0.0.1/"),
		"the CIDR exceptions must survive the flag being applied")

	merged := MergeAppConfig(&AppConfig{}, &AppConfig{TrustProxyResolution: &trust})
	require.NotNil(t, merged.TrustProxyResolution)
	assert.True(t, *merged.TrustProxyResolution)
	assert.Nil(t, MergeAppConfig(&AppConfig{}, &AppConfig{}).TrustProxyResolution)
}

// newFakeProxy returns a server that answers any proxied request itself, plus
// the count of requests it saw.
func newFakeProxy(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var seen atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Add(1)
		// A proxied request carries an absolute URI.
		assert.True(t, r.URL.IsAbs(), "expected absolute-URI proxy request, got %q", r.URL)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	return server, &seen
}

func proxiedConfig(t *testing.T, proxy *httptest.Server) *ClientConfig {
	t.Helper()
	proxyURL, err := url.Parse(proxy.URL)
	require.NoError(t, err)

	cfg := ssrfTestConfig()
	cfg.Transport = &http.Transport{
		Proxy: func(*http.Request) (*url.URL, error) { return proxyURL, nil },
	}
	return cfg
}

// TestClientAllowsPrivateProxy is the regression test for the common corporate
// case: the proxy itself lives on a private address. Dialing the proxy must not
// be blocked, even though the default policy denies private addresses.
func TestClientAllowsPrivateProxy(t *testing.T) {
	proxy, seen := newFakeProxy(t)
	client := NewClient(proxiedConfig(t, proxy))

	// Public target IP literal, so no DNS lookup is needed.
	resp, err := client.Get(context.Background(), "http://93.184.216.34/")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int64(1), seen.Load(), "request should have gone through the proxy")
}

// TestClientBlocksPrivateTargetThroughProxy covers the other half: a proxy must
// not become a laundering service for blocked targets. The dial hook cannot see
// the target here, so the Proxy hook has to reject it.
func TestClientBlocksPrivateTargetThroughProxy(t *testing.T) {
	proxy, seen := newFakeProxy(t)
	client := NewClient(proxiedConfig(t, proxy))

	resp, err := client.Get(context.Background(), "http://169.254.169.254/latest/meta-data/")
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cloud metadata")
	assert.Equal(t, int64(0), seen.Load(), "proxy must not be contacted for a blocked target")
}

func TestCustomTransportWithOwnDialerIsUsedVerbatim(t *testing.T) {
	custom := &http.Transport{
		DialContext: (&net.Dialer{}).DialContext,
	}
	cfg := ssrfTestConfig()
	cfg.Transport = custom

	got := newBaseTransport(cfg, defaultIPPolicy, nil)
	assert.Same(t, custom, got, "a transport with its own dialer must not be silently rewrapped")
}

func TestCustomTransportWithTLSDialHookIsUsedVerbatim(t *testing.T) {
	// net/http prefers DialTLSContext over DialContext for non-proxied HTTPS,
	// so wrapping such a transport would advertise enforcement it cannot do.
	tlsCtx := &http.Transport{
		DialTLSContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, fmt.Errorf("unused")
		},
	}
	cfg := ssrfTestConfig()
	cfg.Transport = tlsCtx
	assert.Same(t, tlsCtx, newBaseTransport(cfg, defaultIPPolicy, nil))

	//nolint:staticcheck // DialTLS is deprecated but still honoured by net/http
	tlsLegacy := &http.Transport{
		DialTLS: func(string, string) (net.Conn, error) {
			return nil, fmt.Errorf("unused")
		},
	}
	cfg2 := ssrfTestConfig()
	cfg2.Transport = tlsLegacy
	assert.Same(t, tlsLegacy, newBaseTransport(cfg2, defaultIPPolicy, nil))
}

func TestNewBaseTransportDoesNotMutateDefaultTransport(t *testing.T) {
	def, ok := http.DefaultTransport.(*http.Transport)
	require.True(t, ok)
	before := def.DialContext

	wrapped, ok := newBaseTransport(ssrfTestConfig(), defaultIPPolicy, nil).(*markingTransport)
	require.True(t, ok, "the default transport should be wrapped for policy enforcement")
	assert.NotSame(t, def, wrapped.base, "the client must own a clone, not the shared default")
	assert.Equal(t,
		reflect.ValueOf(before).Pointer(),
		reflect.ValueOf(def.DialContext).Pointer(),
		"http.DefaultTransport must not be mutated",
	)
}

// TestProxyExemptionDoesNotLaunderDirectRequests is the regression test for a
// bypass found in review: when the proxy exemption was keyed on the proxy's
// address, any address ever used as a proxy stayed exempt from the dial-time
// check forever, so a later DIRECT request to that same address slipped past
// the policy. The exemption is now scoped to the single proxied request.
func TestProxyExemptionDoesNotLaunderDirectRequests(t *testing.T) {
	proxy, seen := newFakeProxy(t)
	proxyURL, err := url.Parse(proxy.URL)
	require.NoError(t, err)

	// Proxy only the first (public) target; everything else goes direct.
	cfg := ssrfTestConfig()
	cfg.Transport = &http.Transport{
		Proxy: func(req *http.Request) (*url.URL, error) {
			if req.URL.Hostname() == "93.184.216.34" {
				return proxyURL, nil
			}
			return nil, nil
		},
	}
	client := NewClient(cfg)

	// Prime the proxy path.
	resp, err := client.Get(context.Background(), "http://93.184.216.34/")
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, int64(1), seen.Load())

	// Now request the proxy's own loopback address directly. It must be
	// blocked: this dial is not a proxy dial.
	resp, err = client.StandardClient().Get(proxy.URL) //nolint:noctx // exercising the dialer
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrBlockedByPolicy)
}

// stubResolver returns fixed answers so the resolved path can be tested
// without depending on real DNS.
type stubResolver struct {
	addrs []net.IPAddr
	err   error
}

func (s stubResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return s.addrs, s.err
}

// TestProxiedHostnameTargetIsResolved covers the target validation the Proxy
// hook performs: the dial hook cannot see a proxied target, so a hostname that
// RESOLVES to a blocked address has to be caught by the lookup. The hostname
// used here is deliberately absent from blockedHostnames, so the test fails if
// the resolution step is removed.
func TestProxiedHostnameTargetIsResolved(t *testing.T) {
	proxy, seen := newFakeProxy(t)
	cfg := proxiedConfig(t, proxy)
	cfg.IPPolicy = &IPPolicy{
		Resolver: stubResolver{addrs: []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}},
	}
	client := NewClient(cfg)

	resp, err := client.Get(context.Background(), "http://internal-looking-name.example/")
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrBlockedByPolicy)
	assert.Contains(t, err.Error(), "cloud metadata")
	assert.Equal(t, int64(0), seen.Load(), "proxy must not be contacted for a blocked target")
}

// TestProxiedTargetResolutionIsCached pins the memoisation: the Proxy hook runs
// on every round trip, so an uncached lookup would be a DNS round trip per
// request even on a pooled connection.
func TestProxiedTargetResolutionIsCached(t *testing.T) {
	proxy, _ := newFakeProxy(t)
	var lookups atomic.Int64
	cfg := proxiedConfig(t, proxy)
	cfg.IPPolicy = &IPPolicy{Resolver: countingResolver{
		count: &lookups,
		addrs: []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}},
	}}
	client := NewClient(cfg)

	for i := range 3 {
		resp, err := client.Get(context.Background(), fmt.Sprintf("http://example.test/page-%d", i))
		require.NoError(t, err)
		_ = resp.Body.Close()
	}
	assert.Equal(t, int64(1), lookups.Load(), "the per-host verdict should be reused")
}

type countingResolver struct {
	count *atomic.Int64
	addrs []net.IPAddr
}

func (c countingResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	c.count.Add(1)
	return c.addrs, nil
}

// TestProxiedTargetResolveTimeoutIsFatal is the counterpart to the NXDOMAIN
// soft-fail: a temporary resolver failure must NOT open an unchecked egress
// path, since the name does resolve -- just not for us right now.
func TestProxiedTargetResolveTimeoutIsFatal(t *testing.T) {
	proxy, seen := newFakeProxy(t)
	cfg := proxiedConfig(t, proxy)
	cfg.IPPolicy = &IPPolicy{Resolver: stubResolver{
		err: &net.DNSError{Err: "i/o timeout", Name: "example.test", IsTimeout: true},
	}}
	client := NewClient(cfg)

	resp, err := client.Get(context.Background(), "http://example.test/")
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
	assert.Equal(t, int64(0), seen.Load())
}

// TestUnresolvableProxiedTargetFailsClosedByDefault pins the secure default:
// a proxied target this process cannot resolve is refused, because the name
// may still resolve for the proxy.
func TestUnresolvableProxiedTargetFailsClosedByDefault(t *testing.T) {
	proxy, seen := newFakeProxy(t)
	cfg := proxiedConfig(t, proxy)
	cfg.IPPolicy = &IPPolicy{Resolver: stubResolver{
		err: &net.DNSError{Err: "no such host", Name: "example.test", IsNotFound: true},
	}}
	client := NewClient(cfg)

	resp, err := client.Get(context.Background(), "http://example.test/")
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err, "an unresolvable proxied target must fail closed by default")
	assert.Equal(t, int64(0), seen.Load(), "the request must not reach the proxy")
}

// TestUnresolvableProxiedTargetAllowedWhenProxyTrusted pins the opt-in
// posture: with TrustProxyResolution set, an unresolvable target defers to the
// proxy -- the proxy-only-environment case.
func TestUnresolvableProxiedTargetAllowedWhenProxyTrusted(t *testing.T) {
	proxy, seen := newFakeProxy(t)
	cfg := proxiedConfig(t, proxy)
	cfg.IPPolicy = &IPPolicy{
		TrustProxyResolution: true,
		Resolver: stubResolver{
			err: &net.DNSError{Err: "no such host", Name: "example.test", IsNotFound: true},
		},
	}
	client := NewClient(cfg)

	resp, err := client.Get(context.Background(), "http://example.test/")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int64(1), seen.Load())
}

// TestTrustProxyResolutionDoesNotRelaxOtherDNSErrors pins that the opt-in
// covers "no such host" only: a resolver timeout stays fatal either way.
func TestTrustProxyResolutionDoesNotRelaxOtherDNSErrors(t *testing.T) {
	proxy, seen := newFakeProxy(t)
	cfg := proxiedConfig(t, proxy)
	cfg.IPPolicy = &IPPolicy{
		TrustProxyResolution: true,
		Resolver: stubResolver{
			err: &net.DNSError{Err: "i/o timeout", Name: "example.test", IsTimeout: true},
		},
	}
	client := NewClient(cfg)

	resp, err := client.Get(context.Background(), "http://example.test/")
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
	assert.Equal(t, int64(0), seen.Load())
}

// TestBlockedRequestIsNotRetried guards against a policy denial being treated
// as a transient error: with the default retry settings that cost seconds of
// backoff per blocked request.
func TestBlockedRequestIsNotRetried(t *testing.T) {
	var attempts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := DefaultConfig()
	cfg.EnableCache = false
	cfg.RetryMax = 3
	cfg.RetryWaitMin = 10 * time.Millisecond
	cfg.RetryWaitMax = 20 * time.Millisecond
	client := NewClient(cfg)

	start := time.Now()
	resp, err := client.StandardClient().Get(server.URL) //nolint:noctx // exercising the retry policy
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrBlockedByPolicy)
	assert.Contains(t, err.Error(), "giving up after 1 attempt(s)", "exactly one attempt should be made")
	assert.Less(t, time.Since(start), time.Second, "a policy denial must not be retried with backoff")
	assert.Equal(t, int64(0), attempts.Load())
}

// TestProxiedTargetTransientFailureIsNotCached guards against a cancelled or
// slow lookup poisoning a host's verdict for the whole TTL.
func TestProxiedTargetTransientFailureIsNotCached(t *testing.T) {
	proxy, _ := newFakeProxy(t)

	flaky := &flakyResolver{
		err:   &net.DNSError{Err: "i/o timeout", Name: "example.test", IsTimeout: true},
		addrs: []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}},
	}
	flaky.failing.Store(true)
	cfg := proxiedConfig(t, proxy)
	cfg.IPPolicy = &IPPolicy{Resolver: flaky}
	client := NewClient(cfg)

	resp, err := client.Get(context.Background(), "http://example.test/")
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err, "a resolver timeout must fail the request")

	// The next request must be evaluated afresh, not served the cached failure.
	flaky.failing.Store(false)
	resp, err = client.Get(context.Background(), "http://example.test/")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

type flakyResolver struct {
	failing atomic.Bool
	err     error
	addrs   []net.IPAddr
}

func (f *flakyResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	if f.failing.Load() {
		return nil, f.err
	}
	return f.addrs, nil
}

func TestTargetVerdictCacheEvictsWhenFull(t *testing.T) {
	cache := &targetVerdictCache{}
	now := time.Now()

	for i := range maxProxiedTargetEntries {
		host := fmt.Sprintf("host-%d.test", i)
		require.NoError(t, cache.check(host, now, func() error { return nil }))
	}
	require.Equal(t, int64(maxProxiedTargetEntries), cache.size.Load())

	// With every entry still fresh there is nothing to sweep, so the cache
	// clears itself rather than growing past the cap.
	require.NoError(t, cache.check("fresh.test", now, func() error { return nil }))
	require.Less(t, cache.size.Load(), int64(maxProxiedTargetEntries))

	// Past the TTL the expired entries are swept instead.
	later := now.Add(2 * proxiedTargetDenyTTL)
	for i := range maxProxiedTargetEntries {
		host := fmt.Sprintf("host-%d.test", i)
		require.NoError(t, cache.check(host, now, func() error { return nil }))
	}
	require.NoError(t, cache.check("later.test", later, func() error { return nil }))
	assert.Less(t, cache.size.Load(), int64(maxProxiedTargetEntries))
}

// TestProxiedTargetCacheKeyIsNormalised pins that host spellings which DNS
// treats as one name share a cache entry.
func TestProxiedTargetCacheKeyIsNormalised(t *testing.T) {
	proxy, _ := newFakeProxy(t)
	var lookups atomic.Int64
	cfg := proxiedConfig(t, proxy)
	cfg.IPPolicy = &IPPolicy{Resolver: countingResolver{
		count: &lookups,
		addrs: []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}},
	}}
	client := NewClient(cfg)

	for _, target := range []string{
		"http://example.test/",
		"http://EXAMPLE.test/",
		"http://example.test./",
	} {
		resp, err := client.Get(context.Background(), target)
		require.NoError(t, err, target)
		_ = resp.Body.Close()
	}
	assert.Equal(t, int64(1), lookups.Load(), "host spellings of one name must share a verdict")
}

func TestTargetVerdictCacheReusesDeterministicVerdicts(t *testing.T) {
	cache := &targetVerdictCache{}
	now := time.Now()
	calls := 0
	blocked := func() error { calls++; return ErrBlockedByPolicy }

	require.ErrorIs(t, cache.check("blocked.test", now, blocked), ErrBlockedByPolicy)
	require.ErrorIs(t, cache.check("blocked.test", now, blocked), ErrBlockedByPolicy)
	assert.Equal(t, 1, calls, "a policy denial is deterministic and should be cached")

	// Once the TTL lapses the verdict is recomputed.
	require.ErrorIs(t, cache.check("blocked.test", now.Add(2*proxiedTargetDenyTTL), blocked), ErrBlockedByPolicy)
	assert.Equal(t, 2, calls)
}

// TestTargetVerdictTTLsAreSplit pins the two lifetimes independently: a
// success expires quickly, so the DNS-rebind window a cached allow opens stays
// small, while a denial is held for the longer window. Collapsing the two back
// into one shared TTL fails this test whichever value were kept.
func TestTargetVerdictTTLsAreSplit(t *testing.T) {
	require.Less(t, proxiedTargetAllowTTL, proxiedTargetDenyTTL,
		"a cached success must not outlive a cached denial")

	t.Run("success expires at the short TTL", func(t *testing.T) {
		cache := &targetVerdictCache{}
		now := time.Now()
		calls := 0
		allow := func() error { calls++; return nil }

		require.NoError(t, cache.check("allowed.test", now, allow))
		require.NoError(t, cache.check("allowed.test", now.Add(proxiedTargetAllowTTL/2), allow))
		assert.Equal(t, 1, calls, "within the allow TTL the verdict is reused")

		// Past the allow TTL but well inside the deny TTL: recomputed. If both
		// verdicts shared the 30s TTL this would still be cached.
		require.NoError(t, cache.check("allowed.test", now.Add(2*proxiedTargetAllowTTL), allow))
		assert.Equal(t, 2, calls, "a success must not be reused past the allow TTL")
	})

	t.Run("denial survives the short TTL", func(t *testing.T) {
		cache := &targetVerdictCache{}
		now := time.Now()
		calls := 0
		deny := func() error { calls++; return ErrBlockedByPolicy }

		require.ErrorIs(t, cache.check("blocked.test", now, deny), ErrBlockedByPolicy)

		// Past the allow TTL, inside the deny TTL: still cached. If both
		// verdicts shared the 1s TTL this would have been recomputed.
		require.ErrorIs(t, cache.check("blocked.test", now.Add(2*proxiedTargetAllowTTL), deny), ErrBlockedByPolicy)
		assert.Equal(t, 1, calls, "a denial must be held for the full deny TTL")
	})
}

// TestTargetVerdictCacheStaysBoundedUnderConcurrency pins that admission is
// serialised: a burst of distinct hosts must not race past the cap.
func TestTargetVerdictCacheStaysBoundedUnderConcurrency(t *testing.T) {
	cache := &targetVerdictCache{}
	now := time.Now()

	var wg sync.WaitGroup
	for i := range maxProxiedTargetEntries * 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = cache.check(fmt.Sprintf("burst-%d.test", i), now, func() error { return nil })
		}()
	}
	wg.Wait()

	assert.LessOrEqual(t, cache.size.Load(), int64(maxProxiedTargetEntries),
		"cache grew past its cap under concurrent admission")
}

func TestCheckIPFailsClosedOnMalformedAddress(t *testing.T) {
	policy := AllowAllPrivateIPs()
	for name, ip := range map[string]net.IP{
		"nil":        nil,
		"empty":      {},
		"three byte": {1, 2, 3},
		"five byte":  {1, 2, 3, 4, 5},
	} {
		t.Run(name, func(t *testing.T) {
			err := policy.CheckIP(ip)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrBlockedByPolicy)
		})
	}
}

// TestIPv4CompatibleDecodesAllButUnspecifiedAndLoopback pins that the
// exclusion covers exactly :: and ::1, so ::2 is judged as 0.0.0.2 -- which
// 0.0.0.0/8 blocks.
func TestIPv4CompatibleDecodesAllButUnspecifiedAndLoopback(t *testing.T) {
	assert.Nil(t, embeddedIPv4(net.ParseIP("::")))
	assert.Nil(t, embeddedIPv4(net.ParseIP("::1")))

	require.Equal(t, net.IPv4(0, 0, 0, 2).String(), embeddedIPv4(net.ParseIP("::2")).String())
	assert.Error(t, defaultIPPolicy.CheckIP(net.ParseIP("::2")))
}

// TestControlFuncStripsIPv6Zone pins that a scoped link-local address is
// judged on its address, not denied for carrying a zone.
func TestControlFuncStripsIPv6Zone(t *testing.T) {
	policy, err := NewIPPolicy("fe80::/10")
	require.NoError(t, err)
	assert.NoError(t, policy.ControlFunc()("tcp6", "[fe80::1%eth0]:80", nil))

	// The zone must not launder a blocked address either.
	assert.Error(t, defaultIPPolicy.ControlFunc()("tcp6", "[fe80::1%eth0]:80", nil))
}

// TestCloseReleasesIdleConnections pins that Client.Close releases the client's
// own idle connections. Each client now owns a cloned transport rather than
// sharing http.DefaultTransport, and none of the outer wrappers (otelhttp,
// metrics, compression, cache) forward CloseIdleConnections, so
// http.Client.CloseIdleConnections never reaches the inner transport.
func TestCloseReleasesIdleConnections(t *testing.T) {
	var idle atomic.Int64

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		switch state {
		case http.StateIdle:
			idle.Add(1)
		case http.StateClosed, http.StateHijacked:
			idle.Add(-1)
		case http.StateNew, http.StateActive:
		}
	}
	server.Start()
	defer server.Close()

	policy, err := NewIPPolicy("127.0.0.0/8", "::1/128")
	require.NoError(t, err)

	cfg := ssrfTestConfig()
	cfg.IPPolicy = policy
	client := NewClient(cfg)

	resp, err := client.Get(context.Background(), server.URL)
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	require.Eventually(t, func() bool { return idle.Load() == 1 }, time.Second, 10*time.Millisecond,
		"connection should be pooled as idle before Close")

	require.NoError(t, client.Close())

	assert.Eventually(t, func() bool { return idle.Load() == 0 }, time.Second, 10*time.Millisecond,
		"Close should release the client's idle connections")
}

// TestReplacedDefaultTransportWithTLSHookIsNotWrapped pins that the
// dialer-ownership check applies to http.DefaultTransport too. An application
// may replace it, and net/http prefers DialTLSContext over DialContext for
// non-proxied HTTPS, so wrapping such a transport would leave HTTPS
// unenforced while looking protected.
func TestReplacedDefaultTransportWithTLSHookIsNotWrapped(t *testing.T) {
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })

	replacement := original.(*http.Transport).Clone()
	replacement.DialTLSContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("unused")
	}
	http.DefaultTransport = replacement

	got := newBaseTransport(DefaultConfig(), defaultIPPolicy, nil)

	assert.Same(t, replacement, got, "a TLS-dialing default transport must be used verbatim, not wrapped")
}

// TestDefaultTransportWithoutTLSHookIsWrapped is the positive counterpart, so
// the check above cannot pass by rejecting everything.
func TestDefaultTransportWithoutTLSHookIsWrapped(t *testing.T) {
	got := newBaseTransport(DefaultConfig(), defaultIPPolicy, nil)

	wrapped, ok := got.(*markingTransport)
	require.True(t, ok, "expected the default transport to be wrapped, got %T", got)
	assert.NotSame(t, http.DefaultTransport, wrapped.base)
}

// TestCacheIsNotSharedAcrossPolicies reproduces an SSRF bypass through the
// cache layer. The cache sits above the transport, so a hit returns without
// the URL check or the dial hook running. Two clients sharing a CacheDir used
// to share entries, letting a restrictive client read a response a permissive
// one had fetched from an address the restrictive policy forbids.
func TestCacheIsNotSharedAcrossPolicies(t *testing.T) {
	var serverHits atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		serverHits.Add(1)
		w.Header().Set("Cache-Control", "max-age=3600")
		_, _ = w.Write([]byte("INTERNAL"))
	}))
	defer server.Close()

	cacheDir := t.TempDir()

	cachedConfig := func(policy *IPPolicy) *ClientConfig {
		cfg := ssrfTestConfig()
		cfg.IPPolicy = policy
		cfg.EnableCache = true
		cfg.CacheType = CacheTypeFilesystem
		cfg.CacheDir = cacheDir
		return cfg
	}

	// A permissive client populates the shared cache directory.
	permissive := NewClient(cachedConfig(mustPolicy(t, "127.0.0.0/8", "::1/128")))
	defer func() { _ = permissive.Close() }()

	resp, err := permissive.Get(context.Background(), server.URL)
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, int64(1), serverHits.Load())

	// A restrictive client on the same directory must not be served that entry.
	// StandardClient bypasses Client.Do, so URL validation never runs and only
	// the dial hook can refuse -- which a cache hit would skip entirely.
	restrictive := NewClient(cachedConfig(nil))
	defer func() { _ = restrictive.Close() }()

	blocked, err := restrictive.StandardClient().Get(server.URL) //nolint:noctx // exercising the cache/dial path
	if blocked != nil {
		_ = blocked.Body.Close()
	}
	require.Error(t, err, "a restrictive client must not read the permissive client's cached response")
	assert.Contains(t, err.Error(), "private/reserved")
	assert.Equal(t, int64(1), serverHits.Load(), "the blocked request must not reach the server either")
}
