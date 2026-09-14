// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package httpc

import (
	"context"
	"net"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateURLNotPrivate(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
		errMsg  string
	}{
		// Public IPs - should pass
		{name: "public IPv4", url: "http://8.8.8.8/path", wantErr: false},
		{name: "public IPv4 Google DNS", url: "https://1.1.1.1", wantErr: false},
		{name: "public hostname", url: "https://example.com/api", wantErr: false},
		{name: "public hostname with port", url: "https://example.com:8443/api", wantErr: false},

		// RFC 1918 - 10.0.0.0/8
		{name: "private 10.0.0.1", url: "http://10.0.0.1/", wantErr: true, errMsg: "private/reserved"},
		{name: "private 10.255.255.255", url: "http://10.255.255.255/", wantErr: true, errMsg: "private/reserved"},

		// RFC 1918 - 172.16.0.0/12
		{name: "private 172.16.0.1", url: "http://172.16.0.1/", wantErr: true, errMsg: "private/reserved"},
		{name: "private 172.31.255.255", url: "http://172.31.255.255/", wantErr: true, errMsg: "private/reserved"},
		{name: "public 172.32.0.1", url: "http://172.32.0.1/", wantErr: false},

		// RFC 1918 - 192.168.0.0/16
		{name: "private 192.168.0.1", url: "http://192.168.0.1/", wantErr: true, errMsg: "private/reserved"},
		{name: "private 192.168.255.255", url: "http://192.168.255.255/", wantErr: true, errMsg: "private/reserved"},

		// Loopback - 127.0.0.0/8
		{name: "loopback 127.0.0.1", url: "http://127.0.0.1/", wantErr: true, errMsg: "private/reserved"},
		{name: "loopback 127.0.0.2", url: "http://127.0.0.2/", wantErr: true, errMsg: "private/reserved"},
		{name: "loopback 127.255.255.255", url: "http://127.255.255.255/", wantErr: true, errMsg: "private/reserved"},

		// Link-local / cloud metadata - 169.254.0.0/16
		{name: "link-local 169.254.169.254", url: "http://169.254.169.254/", wantErr: true, errMsg: "cloud metadata"},
		{name: "link-local 169.254.0.1", url: "http://169.254.0.1/", wantErr: true, errMsg: "private/reserved"},

		// CGNAT - 100.64.0.0/10
		{name: "CGNAT 100.64.0.1", url: "http://100.64.0.1/", wantErr: true, errMsg: "private/reserved"},
		{name: "CGNAT 100.127.255.255", url: "http://100.127.255.255/", wantErr: true, errMsg: "private/reserved"},
		{name: "public 100.128.0.1", url: "http://100.128.0.1/", wantErr: false},

		// IPv6 loopback
		{name: "IPv6 loopback", url: "http://[::1]/", wantErr: true, errMsg: "private/reserved"},

		// IPv6 unique local
		{name: "IPv6 unique local fc00::", url: "http://[fc00::1]/", wantErr: true, errMsg: "private/reserved"},
		{name: "IPv6 unique local fd00::", url: "http://[fd00::1]/", wantErr: true, errMsg: "private/reserved"},

		// IPv6 link-local
		{name: "IPv6 link-local", url: "http://[fe80::1]/", wantErr: true, errMsg: "private/reserved"},

		// Blocked hostnames
		{name: "localhost", url: "http://localhost/", wantErr: true, errMsg: "blocked hostname"},
		{name: "localhost.localdomain", url: "http://localhost.localdomain/", wantErr: true, errMsg: "blocked hostname"},
		{name: "LOCALHOST uppercase", url: "http://LOCALHOST/", wantErr: true, errMsg: "blocked hostname"},
		{name: "metadata.google.internal", url: "http://metadata.google.internal/computeMetadata/v1/", wantErr: true, errMsg: "blocked hostname"},

		// Non-canonical IP forms
		{name: "hex IP 0x7f000001", url: "http://0x7f000001/", wantErr: true, errMsg: "non-canonical"},
		{name: "decimal IP 2130706433", url: "http://2130706433/", wantErr: true, errMsg: "non-canonical"},
		{name: "octal IP 0177.0.0.1", url: "http://0177.0.0.1/", wantErr: true, errMsg: "non-canonical"},
		{name: "hex prefix 0X", url: "http://0X7F000001/", wantErr: true, errMsg: "non-canonical"},

		// Edge cases
		{name: "empty host (relative URL)", url: "/relative/path", wantErr: true, errMsg: "scheme"},
		{name: "IP with port", url: "http://10.0.0.1:8080/", wantErr: true, errMsg: "private/reserved"},
		{name: "localhost with port", url: "http://localhost:3000/", wantErr: true, errMsg: "blocked hostname"},
		{name: "invalid URL", url: "://invalid", wantErr: true, errMsg: "invalid URL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateURLNotPrivate(tt.url)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestPrivateIPNets(t *testing.T) {
	nets := PrivateIPNets()
	assert.Len(t, nets, len(privateCIDRs))
	// The returned value is a deep copy: mutating it -- including through the
	// *net.IPNet pointers -- must not affect the package default.
	require.NoError(t, ValidateURLNotPrivate("http://93.184.216.34/"))
	for _, n := range nets {
		n.IP = net.IPv4zero
		n.Mask = net.CIDRMask(0, 32)
	}
	nets[0] = nil
	require.Error(t, ValidateURLNotPrivate("http://10.1.2.3/"))
	require.NoError(t, ValidateURLNotPrivate("http://93.184.216.34/"))
	assert.NotNil(t, PrivateIPNets()[0])
}

func TestNonCanonicalIPPattern(t *testing.T) {
	tests := []struct {
		name  string
		input string
		match bool
	}{
		{name: "hex lowercase", input: "0x7f000001", match: true},
		{name: "hex uppercase", input: "0XFF000001", match: true},
		{name: "decimal", input: "2130706433", match: true},
		{name: "octal single", input: "0177", match: true},
		{name: "octal dotted", input: "0177.0.0.01", match: true},
		{name: "normal hostname", input: "example.com", match: false},
		{name: "dotted decimal IP", input: "192.168.1.1", match: false},
		{name: "IPv6 string", input: "::1", match: false},
		{name: "empty", input: "", match: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.match, nonCanonicalIPPattern.MatchString(tt.input))
		})
	}
}

func TestBlockedHostnames(t *testing.T) {
	expected := []string{
		"localhost",
		"localhost.localdomain",
		"metadata.google.internal",
	}
	for _, h := range expected {
		_, ok := blockedHostnames[h]
		assert.True(t, ok, "expected %q to be in blockedHostnames", h)
	}
	// Non-blocked hostname
	_, ok := blockedHostnames["example.com"]
	assert.False(t, ok)
}

// TestValidateURLNotPrivateReservedRanges covers reserved ranges that were
// previously missing from the blocklist. 0.0.0.0 in particular routes to
// loopback on Linux, making it a direct bypass of the loopback block.
func TestValidateURLNotPrivateReservedRanges(t *testing.T) {
	blocked := []string{
		"http://0.0.0.0/",
		"http://0.1.2.3/",
		"http://240.0.0.1/",
		"http://255.255.255.255/",
		"http://192.0.0.1/",
		"http://198.18.0.1/",
		"http://[::]/",
	}
	for _, u := range blocked {
		t.Run(u, func(t *testing.T) {
			require.Error(t, ValidateURLNotPrivate(u))
		})
	}
}

func TestValidateURLNotPrivateSchemes(t *testing.T) {
	tests := []struct {
		url     string
		wantErr bool
	}{
		{url: "http://example.com/", wantErr: false},
		{url: "https://example.com/", wantErr: false},
		{url: "HTTPS://example.com/", wantErr: false},
		{url: "file:///etc/passwd", wantErr: true},
		{url: "gopher://example.com/", wantErr: true},
		{url: "ftp://example.com/", wantErr: true},
		{url: "example.com/path", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			err := ValidateURLNotPrivate(tt.url)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestNewIPPolicyExceptions(t *testing.T) {
	policy, err := NewIPPolicy("10.0.0.0/8")
	require.NoError(t, err)

	require.NoError(t, policy.ValidateURL("http://10.1.2.3/artifacts"))
	require.Error(t, policy.ValidateURL("http://192.168.1.1/"))
	require.Error(t, policy.ValidateURL("http://127.0.0.1/"))

	_, err = NewIPPolicy("not-a-cidr")
	require.Error(t, err)
}

// TestIPPolicyMetadataNonExemptible asserts the metadata endpoints cannot be
// re-enabled, no matter how permissive the policy is.
func TestIPPolicyMetadataNonExemptible(t *testing.T) {
	permissive, err := NewIPPolicy("169.254.0.0/16", "0.0.0.0/0", "::/0")
	require.NoError(t, err)

	policies := map[string]*IPPolicy{
		"exception covers IMDS": permissive,
		"allow all private":     AllowAllPrivateIPs(),
		"default":               {},
	}
	targets := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://169.254.170.2/v2/credentials",
		"http://[fd00:ec2::254]/latest/meta-data/",
	}
	for name, policy := range policies {
		for _, target := range targets {
			t.Run(name+" "+target, func(t *testing.T) {
				err := policy.ValidateURL(target)
				require.Error(t, err)
				assert.Contains(t, err.Error(), "cloud metadata")
			})
		}
	}

	// Link-local addresses that are not metadata remain exemptible.
	require.NoError(t, permissive.ValidateURL("http://169.254.1.1/"))
}

func TestAllowAllPrivateIPs(t *testing.T) {
	policy := AllowAllPrivateIPs()
	require.NoError(t, policy.ValidateURL("http://10.0.0.1/"))
	require.NoError(t, policy.ValidateURL("http://127.0.0.1:8080/"))
	require.Error(t, policy.ValidateURL("http://169.254.169.254/"))
	// Scheme and non-canonical checks still apply.
	require.Error(t, policy.ValidateURL("file:///etc/passwd"))
	require.Error(t, policy.ValidateURL("http://2130706433/"))
}

func TestValidateURLNotPrivateExcept(t *testing.T) {
	require.NoError(t, ValidateURLNotPrivateExcept("http://10.0.0.1/", "10.0.0.0/8"))
	require.Error(t, ValidateURLNotPrivateExcept("http://127.0.0.1/", "10.0.0.0/8"))
	require.Error(t, ValidateURLNotPrivateExcept("http://169.254.169.254/", "169.254.0.0/16"))
	require.Error(t, ValidateURLNotPrivateExcept("http://10.0.0.1/", "bogus"))
}

// TestIPPolicyControlFunc exercises the dial-time hook directly. This is the
// check that catches a hostname resolving to a private address, since it runs
// on the resolved sockaddr rather than on the URL.
func TestIPPolicyControlFunc(t *testing.T) {
	control := (&IPPolicy{}).ControlFunc()

	require.Error(t, control("tcp4", "127.0.0.1:80", nil))
	require.Error(t, control("tcp4", "169.254.169.254:80", nil))
	require.Error(t, control("tcp4", "0.0.0.0:80", nil))
	require.Error(t, control("tcp6", "[::1]:80", nil))
	require.NoError(t, control("tcp4", "93.184.216.34:443", nil))

	// A non-address string must fail closed rather than pass through.
	require.Error(t, control("tcp4", "example.com:80", nil))

	allowLoopback, err := NewIPPolicy("127.0.0.0/8")
	require.NoError(t, err)
	require.NoError(t, allowLoopback.ControlFunc()("tcp4", "127.0.0.1:80", nil))
	require.Error(t, allowLoopback.ControlFunc()("tcp4", "169.254.169.254:80", nil))
}

func TestIPPolicyCheckIPNilReceiverAndIP(t *testing.T) {
	var policy *IPPolicy
	require.Error(t, policy.CheckIP(net.ParseIP("127.0.0.1")))
	require.NoError(t, policy.CheckIP(net.ParseIP("93.184.216.34")))
	require.Error(t, policy.CheckIP(nil))
}

func TestMetadataIPNetsIsCopy(t *testing.T) {
	nets := MetadataIPNets()
	require.NotEmpty(t, nets)
	for _, n := range nets {
		n.IP = net.IPv4zero
		n.Mask = net.CIDRMask(0, 32)
	}
	nets[0] = nil
	// The non-exemptible guarantee must survive that mutation.
	require.Error(t, AllowAllPrivateIPs().ValidateURL("http://169.254.169.254/"))
	require.NoError(t, AllowAllPrivateIPs().ValidateURL("http://93.184.216.34/"))
	assert.NotNil(t, MetadataIPNets()[0])
}

// TestValidateURLNotPrivateTrailingDot covers the FQDN form of a blocked
// hostname, which resolves identically but misses a naive map lookup.
func TestValidateURLNotPrivateTrailingDot(t *testing.T) {
	for _, u := range []string{
		"http://localhost./",
		"http://LOCALHOST./",
		"http://metadata.google.internal./computeMetadata/v1/",
	} {
		t.Run(u, func(t *testing.T) {
			err := ValidateURLNotPrivate(u)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "blocked hostname")
		})
	}
}

// TestValidateURLNotPrivateEmbeddedIPv4 covers IPv6 encodings that carry an
// IPv4 address inside them and route to it where the transition mechanism is
// enabled -- classic SSRF bypass forms.
func TestValidateURLNotPrivateEmbeddedIPv4(t *testing.T) {
	blocked := []string{
		"http://[::ffff:127.0.0.1]/",         // IPv4-mapped loopback
		"http://[::169.254.169.254]/",        // IPv4-compatible
		"http://[64:ff9b::169.254.169.254]/", // NAT64 well-known prefix
		"http://[64:ff9b:1::a00:1]/",         // NAT64 local-use prefix (blocked wholesale)
		"http://[2002:a9fe:a9fe::1]/",        // 6to4 -> 169.254.169.254
	}
	for _, u := range blocked {
		t.Run("blocked "+u, func(t *testing.T) {
			err := ValidateURLNotPrivate(u)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrBlockedByPolicy)
		})
	}

	// Only the EMBEDDED address decides. Blanket-blocking these prefixes would
	// break every IPv4 destination on a DNS64/NAT64 or 6to4 network.
	allowed := []string{
		"http://[::ffff:8.8.8.8]/",
		"http://[64:ff9b::8.8.8.8]/",
		"http://[2002:0808:0808::1]/",
	}
	for _, u := range allowed {
		t.Run("allowed "+u, func(t *testing.T) {
			require.NoError(t, ValidateURLNotPrivate(u))
		})
	}
}

func TestValidateURLResolved(t *testing.T) {
	ctx := context.Background()
	policy := &IPPolicy{}

	// localhost resolves to loopback on every supported platform.
	err := policy.ValidateURLResolved(ctx, "http://localhost.test.invalid/")
	require.Error(t, err, "an unresolvable host must fail closed")

	// IP literals short-circuit without a DNS lookup.
	require.NoError(t, policy.ValidateURLResolved(ctx, "http://93.184.216.34/"))
	require.Error(t, policy.ValidateURLResolved(ctx, "http://127.0.0.1/"))
	require.Error(t, policy.ValidateURLResolved(ctx, "file:///etc/passwd"))
}

// TestBlockedErrorsWrapSentinel pins the sentinel that keeps deterministic
// refusals out of the retry loop.
func TestBlockedErrorsWrapSentinel(t *testing.T) {
	for _, u := range []string{
		"http://127.0.0.1/",
		"http://169.254.169.254/",
		"http://localhost/",
		"http://2130706433/",
		"file:///etc/passwd",
		"/relative",
	} {
		t.Run(u, func(t *testing.T) {
			err := ValidateURLNotPrivate(u)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrBlockedByPolicy)
		})
	}

	assert.ErrorIs(t, (&IPPolicy{}).CheckIP(nil), ErrBlockedByPolicy)
	assert.ErrorIs(t, (&IPPolicy{}).ControlFunc()("tcp", "not-an-ip:80", nil), ErrBlockedByPolicy)
}

// TestNonDecodablePrefixIsNonExemptible pins that the NAT64 local-use prefix,
// whose embedded IPv4 address cannot be located reliably, is blocked on the
// same terms as cloud metadata: no policy can allow it. Blocking it via the
// ordinary private list would let AllowPrivateIPs reach IMDS through it.
func TestNonDecodablePrefixIsNonExemptible(t *testing.T) {
	permissive, err := NewIPPolicy("64:ff9b:1::/48", "::/0")
	require.NoError(t, err)

	policies := map[string]*IPPolicy{
		"default":           {},
		"allow all private": AllowAllPrivateIPs(),
		"explicit CIDR":     permissive,
	}
	targets := []string{
		"http://[64:ff9b:1::a9fe:a9fe]/",
		"http://[64:ff9b:1:0:0:0:a9fe:a9fe]/",
		"http://[64:ff9b:1::a00:1]/",
	}
	for name, policy := range policies {
		for _, target := range targets {
			t.Run(name+" "+target, func(t *testing.T) {
				err := policy.ValidateURL(target)
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrBlockedByPolicy)
			})
		}
	}
}

// TestValidateURLJudgesZonedLiteral pins that a scoped IPv6 literal is judged
// as the address it is. net.ParseIP rejects the zoned spelling, so without the
// zone strip the literal falls through as if it were a hostname and is allowed.
func TestValidateURLJudgesZonedLiteral(t *testing.T) {
	tests := []struct {
		name    string
		policy  *IPPolicy
		rawURL  string
		blocked bool
	}{
		{"link-local blocked by default", defaultIPPolicy, "http://[fe80::1%25eth0]/", true},
		{"metadata blocked by default", defaultIPPolicy, "http://[fe80::a9fe%25eth0]/", true},
		{"allowed when policy permits", mustPolicy(t, "fe80::/10"), "http://[fe80::1%25eth0]/", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.policy.ValidateURL(tt.rawURL)
			if tt.blocked {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrBlockedByPolicy)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// TestValidateURLResolvedRejectsEmptyResolverResult pins that a resolver
// answering with no addresses and no error is a failure, not an allow: nothing
// was checked, so there is no basis for permitting the request.
func TestValidateURLResolvedRejectsEmptyResolverResult(t *testing.T) {
	policy := &IPPolicy{Resolver: stubResolver{addrs: nil}}

	err := policy.ValidateURLResolved(context.Background(), "http://example.test/")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no addresses")
}

// TestValidateURLResolvedSkipsZonedLiteral pins that a scoped literal is not
// handed to the resolver as though it were a hostname.
func TestValidateURLResolvedSkipsZonedLiteral(t *testing.T) {
	var calls atomic.Int64
	policy := &IPPolicy{AllowPrivate: true, Resolver: countingResolver{count: &calls}}

	require.NoError(t, policy.ValidateURLResolved(context.Background(), "http://[fe80::1%25eth0]/"))
	assert.Zero(t, calls.Load(), "a scoped literal must not be resolved")
}

func mustPolicy(t *testing.T, cidrs ...string) *IPPolicy {
	t.Helper()
	p, err := NewIPPolicy(cidrs...)
	require.NoError(t, err)
	return p
}

// TestIPPolicyFingerprint pins that the digest separates policies that decide
// differently and unites ones that decide identically. Cache keys are built
// from it, so a collision leaks entries across policies and a spurious
// difference silently halves the hit rate.
func TestIPPolicyFingerprint(t *testing.T) {
	base := mustPolicy(t, "10.0.0.0/8")

	t.Run("stable across calls", func(t *testing.T) {
		assert.Equal(t, base.fingerprint(), base.fingerprint())
	})

	t.Run("independent of CIDR order", func(t *testing.T) {
		a := mustPolicy(t, "10.0.0.0/8", "192.168.0.0/16")
		b := mustPolicy(t, "192.168.0.0/16", "10.0.0.0/8")
		assert.Equal(t, a.fingerprint(), b.fingerprint())
	})

	t.Run("differs from a different range", func(t *testing.T) {
		assert.NotEqual(t, base.fingerprint(), mustPolicy(t, "172.16.0.0/12").fingerprint())
	})

	t.Run("differs from the default policy", func(t *testing.T) {
		assert.NotEqual(t, base.fingerprint(), defaultIPPolicy.fingerprint())
	})

	t.Run("differs when private access differs", func(t *testing.T) {
		assert.NotEqual(t, defaultIPPolicy.fingerprint(), AllowAllPrivateIPs().fingerprint())
	})

	t.Run("differs when proxy trust differs", func(t *testing.T) {
		trusting := &IPPolicy{TrustProxyResolution: true}
		assert.NotEqual(t, (&IPPolicy{}).fingerprint(), trusting.fingerprint())
	})

	t.Run("nil differs from zero value", func(t *testing.T) {
		var nilPolicy *IPPolicy
		assert.NotEqual(t, nilPolicy.fingerprint(), (&IPPolicy{}).fingerprint())
	})

	t.Run("ignores the resolver", func(t *testing.T) {
		withResolver := &IPPolicy{Resolver: stubResolver{}}
		assert.Equal(t, (&IPPolicy{}).fingerprint(), withResolver.fingerprint())
	})
}

// TestCredentialEndpointsAreNonExemptible pins the metadata endpoints that sit
// inside otherwise-exemptible private ranges: EKS Pod Identity lives in
// 169.254.0.0/16 and Alibaba's ECS metadata in the CGNAT 100.64.0.0/10, so
// without an explicit entry a broad exception or AllowAllPrivateIPs would
// reach a credential endpoint the API promises can never be re-enabled.
func TestCredentialEndpointsAreNonExemptible(t *testing.T) {
	permissive, err := NewIPPolicy("169.254.0.0/16", "100.64.0.0/10", "0.0.0.0/0", "::/0")
	require.NoError(t, err)

	policies := map[string]*IPPolicy{
		"exception covers the range": permissive,
		"allow all private":          AllowAllPrivateIPs(),
		"default":                    {},
	}
	targets := []string{
		"http://169.254.170.23/v1/credentials",     // AWS EKS Pod Identity Agent
		"http://[fd00:ec2::23]/v1/credentials",     // ... over IPv6
		"http://100.100.100.200/latest/meta-data/", // Alibaba Cloud ECS
	}
	for name, policy := range policies {
		for _, target := range targets {
			t.Run(name+" "+target, func(t *testing.T) {
				err := policy.ValidateURL(target)
				require.Error(t, err)
				assert.Contains(t, err.Error(), "cloud metadata")
			})
		}
	}

	// Neighbouring addresses in the same ranges stay exemptible, so the new
	// entries are /32/128 host routes rather than a widened block.
	require.NoError(t, permissive.ValidateURL("http://169.254.170.24/"))
	require.NoError(t, permissive.ValidateURL("http://100.100.100.201/"))
}

// TestTrailingDotIsPreservedForResolution pins that the FQDN trailing dot is
// only insignificant for the static blocked-hostname aliases. It selects an
// absolute lookup, so "host." and "host" are different DNS queries and must
// not share a resolution path or a cache identity.
func TestTrailingDotIsPreservedForResolution(t *testing.T) {
	t.Run("alias matching still ignores the dot", func(t *testing.T) {
		require.Error(t, ValidateURLNotPrivate("http://localhost./"))
	})

	t.Run("lookup keeps the dot", func(t *testing.T) {
		stub := &recordingResolver{addrs: []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}}
		policy := &IPPolicy{Resolver: stub}
		require.NoError(t, policy.ValidateURLResolved(context.Background(), "http://Example.COM./"))
		// Lower-cased (DNS-insignificant) but the dot survives, so the
		// absolute query is the one actually checked.
		assert.Equal(t, "example.com.", stub.lastHost)
	})

	t.Run("dotted and undotted are distinct cache identities", func(t *testing.T) {
		assert.NotEqual(t, lowerHost("host."), lowerHost("host"))
	})
}

type recordingResolver struct {
	addrs    []net.IPAddr
	lastHost string
}

func (r *recordingResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	r.lastHost = host
	return r.addrs, nil
}

// TestAllowedCIDRsWithNilEntryDenies pins that a caller-built policy carrying a
// nil *net.IPNet -- possible because AllowedCIDRs is a public field -- produces
// a denial rather than panicking inside a library consumer.
func TestAllowedCIDRsWithNilEntryDenies(t *testing.T) {
	_, ten, err := net.ParseCIDR("10.0.0.0/8")
	require.NoError(t, err)
	policy := &IPPolicy{AllowedCIDRs: []*net.IPNet{nil, ten}}

	require.NotPanics(t, func() {
		assert.Error(t, policy.ValidateURL("http://127.0.0.1/"))
	})
	// The valid entry alongside the nil one still works.
	require.NoError(t, policy.ValidateURL("http://10.1.2.3/"))
}
