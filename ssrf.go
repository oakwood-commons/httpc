// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package httpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"syscall"
)

// privateCIDRs lists the CIDR blocks considered private or otherwise reserved.
// Requests to addresses within these ranges are blocked by the default policy.
var privateCIDRs = []string{ //nolint:gochecknoglobals
	"0.0.0.0/8",          // RFC 1122 "this network"; 0.0.0.0 routes to loopback on Linux
	"10.0.0.0/8",         // RFC 1918
	"172.16.0.0/12",      // RFC 1918
	"192.168.0.0/16",     // RFC 1918
	"127.0.0.0/8",        // IPv4 loopback
	"169.254.0.0/16",     // IPv4 link-local / cloud metadata (AWS, GCP, Azure IMDS)
	"100.64.0.0/10",      // RFC 6598 shared address space (CGNAT)
	"192.0.0.0/24",       // RFC 6890 IETF protocol assignments
	"198.18.0.0/15",      // RFC 2544 benchmarking
	"240.0.0.0/4",        // RFC 1112 reserved (class E)
	"255.255.255.255/32", // limited broadcast
	"::/128",             // IPv6 unspecified
	"::1/128",            // IPv6 loopback
	"fc00::/7",           // IPv6 unique local
	"fe80::/10",          // IPv6 link-local
}

// metadataCIDRs are the cloud instance-metadata endpoints. They are blocked
// unconditionally: no exception list can re-enable them, because the IMDS
// endpoint is the single highest-value SSRF target and a caller asking for
// "the 169.254.0.0/16 link-local range" almost never means "and also IMDS".
var metadataCIDRs = []string{ //nolint:gochecknoglobals
	"169.254.169.254/32", // AWS / GCP / Azure / DigitalOcean / OpenStack IMDS
	"169.254.170.2/32",   // AWS ECS task metadata
	"169.254.170.23/32",  // AWS EKS Pod Identity Agent
	"fd00:ec2::254/128",  // AWS IMDS over IPv6
	"fd00:ec2::23/128",   // AWS EKS Pod Identity Agent over IPv6
	"100.100.100.200/32", // Alibaba Cloud ECS metadata
}

// ErrBlockedByPolicy wraps every SSRF-policy denial, so callers (and the retry
// policy) can distinguish a deliberate refusal from a transient network error.
var ErrBlockedByPolicy = errors.New("blocked by SSRF policy")

// The prefixes below are IPv6 prefixes that carry an IPv4 address inside
// them and route to it where the transition mechanism is enabled. Rather than
// blocking these wholesale -- which would break every IPv4 destination on a
// DNS64/NAT64 network -- the embedded address is extracted and checked against
// the same rules as a native IPv4 address.
//
// Only prefixes with a fixed length are decoded. RFC 8215's 64:ff9b:1::/48 is
// explicitly variable-length, so the IPv4 payload could sit at any of five
// offsets; guessing one would both miss blocked targets and misread legitimate
// ones, so that prefix is blocked wholesale instead (see nonDecodableCIDRs).
var (
	nat64WellKnownNet = mustParseCIDRs([]string{"64:ff9b::/96"})[0] //nolint:gochecknoglobals
	ipv4CompatibleNet = mustParseCIDRs([]string{"::/96"})[0]        //nolint:gochecknoglobals
)

// embeddedIPv4 returns the IPv4 address carried inside an IPv6 address that
// uses a known transition encoding, or nil if there is none.
//
// IPv4-mapped addresses (::ffff:a.b.c.d) are not handled here: net.IPNet
// .Contains already normalises those via To4, so they are checked natively.
func embeddedIPv4(ip net.IP) net.IP {
	if ip.To4() != nil {
		return nil // already IPv4 (or IPv4-mapped), checked directly
	}
	ip16 := ip.To16()
	if ip16 == nil {
		return nil
	}
	switch {
	case ip16[0] == 0x20 && ip16[1] == 0x02: // RFC 3056 6to4: 2002:V4ADDR::/48
		return net.IPv4(ip16[2], ip16[3], ip16[4], ip16[5])
	case nat64WellKnownNet.Contains(ip16): // RFC 6052 well-known prefix, fixed at /96
		return net.IPv4(ip16[12], ip16[13], ip16[14], ip16[15])
	case ipv4CompatibleNet.Contains(ip16): // RFC 4291 ::a.b.c.d
		// Exclude exactly :: and ::1, which are not IPv4-compatible addresses
		// and are already covered by the blocklist. Anything else in the
		// prefix -- ::2 included -- is decoded, so it is judged on the
		// embedded address rather than slipping past as "not IPv4".
		if ip16[12] == 0 && ip16[13] == 0 && ip16[14] == 0 && ip16[15] <= 1 {
			return nil
		}
		return net.IPv4(ip16[12], ip16[13], ip16[14], ip16[15])
	}
	return nil
}

// nonDecodableCIDRs are IPv6 transition prefixes that embed an IPv4 address at
// an offset this package cannot determine. They are blocked on the same terms
// as metadataCIDRs -- no policy can allow them -- because allowing one would
// silently allow every IPv4 destination it can encode, cloud metadata included.
var nonDecodableCIDRs = []string{ //nolint:gochecknoglobals
	"64:ff9b:1::/48", // RFC 8215 NAT64 local-use prefix, variable prefix length
}

// nonDecodableIPNets holds the parsed form of nonDecodableCIDRs.
var nonDecodableIPNets = mustParseCIDRs(nonDecodableCIDRs) //nolint:gochecknoglobals

// privateIPNets holds the parsed form of privateCIDRs.
var privateIPNets = mustParseCIDRs(privateCIDRs) //nolint:gochecknoglobals

// metadataIPNets holds the parsed form of metadataCIDRs.
var metadataIPNets = mustParseCIDRs(metadataCIDRs) //nolint:gochecknoglobals

// defaultIPPolicy is the policy used when none is configured: block every
// private and reserved range, with no exceptions.
var defaultIPPolicy = &IPPolicy{} //nolint:gochecknoglobals

func mustParseCIDRs(cidrs []string) []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(fmt.Sprintf("httpc: invalid CIDR %q: %v", cidr, err))
		}
		nets = append(nets, network)
	}
	return nets
}

// PrivateIPNets returns the exemptible CIDR blocks blocked by the default
// policy -- those an IPPolicy can carve exceptions out of. Cloud metadata (see
// MetadataIPNets) and non-decodable IPv6 transition prefixes are blocked
// unconditionally and are NOT included here.
//
// The returned value is a deep copy, so callers may inspect or filter it
// without affecting the package defaults.
func PrivateIPNets() []*net.IPNet {
	return cloneIPNets(privateIPNets)
}

// MetadataIPNets returns the cloud instance-metadata CIDR blocks that are
// blocked unconditionally and cannot be allowed by an IPPolicy exception.
func MetadataIPNets() []*net.IPNet {
	return cloneIPNets(metadataIPNets)
}

// cloneIPNets deep-copies a CIDR list so that callers cannot mutate the
// package-level blocklists through the returned values.
func cloneIPNets(nets []*net.IPNet) []*net.IPNet {
	out := make([]*net.IPNet, len(nets))
	for i, n := range nets {
		out[i] = &net.IPNet{
			IP:   append(net.IP(nil), n.IP...),
			Mask: append(net.IPMask(nil), n.Mask...),
		}
	}
	return out
}

// allowedSchemes are the URL schemes httpc is willing to request.
var allowedSchemes = map[string]struct{}{ //nolint:gochecknoglobals
	"http":  {},
	"https": {},
}

// nonCanonicalIPPattern matches numeric-only, hex-prefixed, or octal-style
// host strings that some HTTP stacks interpret as IP addresses but that Go's
// net.ParseIP does not recognise. Blocking these prevents SSRF bypass via
// non-canonical IP representations (e.g. 2130706433 -> 127.0.0.1).
//
// Note: the decimal branch (`[0-9]+`) intentionally matches any purely numeric
// hostname. While this is conservative (it blocks legitimate numeric-only
// hostnames), it is the safe default because many HTTP stacks silently
// convert decimal integers to IP addresses.
var nonCanonicalIPPattern = regexp.MustCompile( //nolint:gochecknoglobals
	`^(?:0[xX][0-9a-fA-F]+|` + // hex: 0x7f000001
		`[0-9]+|` + // decimal: 2130706433 (any numeric-only host)
		`0[0-7]+(?:\.[0-7]+){0,3})$`, // octal: 0177.0.0.1
)

// blockedHostnames contains well-known hostnames that resolve to private or
// metadata IP addresses. These are rejected before any DNS lookup happens so
// that trivial bypasses such as http://localhost/... fail fast with a clear
// message; the dial-time check is what actually guarantees enforcement.
var blockedHostnames = map[string]struct{}{ //nolint:gochecknoglobals
	"localhost":                {},
	"localhost.localdomain":    {},
	"metadata.google.internal": {}, // GCP instance metadata
	"metadata.goog":            {}, // GCP instance metadata (short alias)
}

// lowerHost lower-cases a hostname, which DNS treats as insignificant. The
// FQDN trailing dot is deliberately PRESERVED: it is not cosmetic, it forces
// an absolute lookup, whereas the undotted spelling may be expanded through
// the resolver's search domains. "host." and "host" can therefore resolve to
// different addresses, so they must stay distinct wherever the value is used
// for a DNS lookup or as a cache identity.
func lowerHost(host string) string {
	return strings.ToLower(host)
}

// hostAlias normalises a hostname for comparison against the static
// blockedHostnames table, where the trailing dot IS insignificant: "LOCALHOST."
// and "localhost" name the same well-known target, and the table holds no
// entry whose meaning depends on search-domain expansion. Use this ONLY for
// such alias matching -- never for a lookup or a cache key (see lowerHost).
func hostAlias(host string) string {
	return strings.TrimSuffix(lowerHost(host), ".")
}

// IPPolicy decides which destination IP addresses a client may connect to.
//
// The zero value blocks every private, loopback, link-local, CGNAT, and
// otherwise reserved range (see PrivateIPNets). This is the secure default.
//
// Exceptions are expressed as CIDR blocks so that a caller who needs, say, an
// internal artifact server on 10.0.0.0/8 does not have to unblock the whole
// private address space. Cloud instance-metadata endpoints (see
// MetadataIPNets) are never reachable, regardless of AllowPrivate or
// AllowedCIDRs.
type IPPolicy struct {
	// AllowPrivate disables private/reserved-range blocking entirely, except
	// for the non-exemptible metadata ranges.
	AllowPrivate bool

	// AllowedCIDRs carves specific ranges out of the default blocklist.
	// Ignored when AllowPrivate is true (which already allows more).
	AllowedCIDRs []*net.IPNet

	// Resolver is used by ValidateURLResolved. Defaults to net.DefaultResolver.
	// Exposed primarily so the resolved path can be tested without DNS.
	Resolver Resolver

	// TrustProxyResolution allows a proxied request whose target hostname this
	// process cannot resolve ("no such host") to proceed, leaving egress
	// policy to the proxy.
	//
	// Defaults to false, which fails closed: a proxied target that does not
	// resolve here is refused, as is any other DNS failure. That is the secure
	// default, because an attacker who can make a name unresolvable for this
	// process -- by serving NXDOMAIN selectively, or by poisoning a local
	// resolver -- would otherwise obtain an unchecked egress path through the
	// proxy.
	//
	// Set it to true only in a proxy-only environment where the client
	// genuinely has no direct resolver and the proxy is trusted to enforce
	// egress policy itself. Even then it only relaxes "no such host"; every
	// other DNS error stays fatal.
	TrustProxyResolution bool
}

// Resolver is the subset of *net.Resolver that IPPolicy needs.
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// NewIPPolicy builds an IPPolicy that blocks all private and reserved ranges
// except the supplied CIDR blocks. It returns an error if any CIDR is invalid.
//
//	policy, err := httpc.NewIPPolicy("10.0.0.0/8")
//
// Cloud metadata endpoints remain blocked even if an exception would cover
// them, so NewIPPolicy("169.254.0.0/16") still denies 169.254.169.254.
func NewIPPolicy(allowedCIDRs ...string) (*IPPolicy, error) {
	nets := make([]*net.IPNet, 0, len(allowedCIDRs))
	for _, cidr := range allowedCIDRs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("invalid allowed CIDR %q: %w", cidr, err)
		}
		nets = append(nets, network)
	}
	return &IPPolicy{AllowedCIDRs: nets}, nil
}

// AllowAllPrivateIPs returns a policy that permits every private and reserved
// range. Cloud instance-metadata endpoints remain blocked.
func AllowAllPrivateIPs() *IPPolicy {
	return &IPPolicy{AllowPrivate: true}
}

// CheckIP reports whether the policy permits connecting to ip.
func (p *IPPolicy) CheckIP(ip net.IP) error {
	// net.IP is a byte slice, so "not nil" is not "well formed": an empty or
	// three-byte value would match no network and fall through to allowed.
	// Fail closed on anything To16 cannot make sense of.
	if len(ip) == 0 || ip.To16() == nil {
		return fmt.Errorf("%w: invalid IP address", ErrBlockedByPolicy)
	}

	// Non-exemptible: cloud instance metadata.
	for _, network := range metadataIPNets {
		if network.Contains(ip) {
			return fmt.Errorf(
				"%w: request to cloud metadata address %s is blocked and cannot be allowed by policy",
				ErrBlockedByPolicy, ip,
			)
		}
	}

	for _, network := range nonDecodableIPNets {
		if network.Contains(ip) {
			return fmt.Errorf(
				"%w: request to %s is blocked and cannot be allowed by policy: the prefix embeds "+
					"an IPv4 address at an indeterminate offset, so it cannot be checked",
				ErrBlockedByPolicy, ip,
			)
		}
	}

	// IPv6 encodings that carry an IPv4 address are checked on that address, so
	// e.g. 64:ff9b::169.254.169.254 is blocked while 64:ff9b::8.8.8.8 is not.
	if v4 := embeddedIPv4(ip); v4 != nil {
		if err := p.CheckIP(v4); err != nil {
			return fmt.Errorf("%s embeds a blocked IPv4 address: %w", ip, err)
		}
	}

	if p == nil {
		p = defaultIPPolicy
	}
	if p.AllowPrivate {
		return nil
	}

	for _, network := range privateIPNets {
		if !network.Contains(ip) {
			continue
		}
		if p.allows(ip) {
			return nil
		}
		return fmt.Errorf(
			"%w: request to private/reserved IP address %s is blocked; "+
				"set ClientConfig.IPPolicy to allow the ranges you need",
			ErrBlockedByPolicy, ip,
		)
	}

	return nil
}

// allows reports whether ip falls inside one of the policy's exceptions.
func (p *IPPolicy) allows(ip net.IP) bool {
	for _, network := range p.AllowedCIDRs {
		// AllowedCIDRs is a public field, so a caller-built policy can carry a
		// nil entry. Skip it rather than panicking: malformed input must
		// produce a denial, never a crash in a library consumer.
		if network == nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// ValidateURL checks rawURL's scheme and, when the host is an IP literal, the
// address itself. Hostnames are not resolved here; see IPPolicy.ControlFunc
// for the dial-time check that covers DNS results.
func (p *IPPolicy) ValidateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	scheme := strings.ToLower(u.Scheme)
	if _, ok := allowedSchemes[scheme]; !ok {
		return fmt.Errorf("%w: URL scheme %q is not allowed (only http and https are supported)",
			ErrBlockedByPolicy, u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("%w: URL %q has no host", ErrBlockedByPolicy, rawURL)
	}

	host = lowerHost(host)

	// Block well-known hostnames that always resolve to private/metadata IPs.
	// Matched on the alias form, where a trailing dot is insignificant.
	if _, blocked := blockedHostnames[hostAlias(host)]; blocked {
		return fmt.Errorf(
			"%w: request to blocked hostname %q is denied (resolves to a private/metadata address)",
			ErrBlockedByPolicy, host,
		)
	}

	// Reject non-canonical IP representations that net.ParseIP won't catch.
	// An IP literal's meaning does not depend on the trailing dot, so the
	// address checks below use the alias form.
	literal := hostAlias(host)
	if nonCanonicalIPPattern.MatchString(literal) {
		return fmt.Errorf(
			"%w: request to non-canonical IP literal %q is blocked (potential SSRF bypass); "+
				"use a standard dotted-decimal or bracketed IPv6 address instead",
			ErrBlockedByPolicy, host,
		)
	}

	// Only check IP literals; plain hostnames are enforced at dial time. The
	// zone is stripped for parsing so a scoped literal is judged as the IP it
	// is, rather than falling through as if it were a hostname.
	ip := net.ParseIP(stripZone(literal))
	if ip == nil {
		return nil
	}

	return p.CheckIP(ip)
}

// ValidateURLResolved performs ValidateURL and, for hostnames, additionally
// resolves the host and checks every returned address against the policy.
//
// This is a best-effort check with an inherent TOCTOU window: DNS may return
// different answers to the resolver that actually connects. Prefer
// ControlFunc, which runs on the socket address. ValidateURLResolved exists
// for the proxied case, where the proxy -- not this process -- resolves and
// connects to the target, so no dial-time hook can see it.
func (p *IPPolicy) ValidateURLResolved(ctx context.Context, rawURL string) error {
	if err := p.ValidateURL(rawURL); err != nil {
		return err
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	// The trailing dot is preserved for the lookup: it selects an absolute
	// query, so resolving the undotted spelling instead could check a
	// different name than the one the request (or a proxy) actually uses.
	host := lowerHost(u.Hostname())
	if net.ParseIP(stripZone(hostAlias(host))) != nil {
		return nil // already checked as a literal by ValidateURL
	}

	resolver := Resolver(net.DefaultResolver)
	if p != nil && p.Resolver != nil {
		resolver = p.Resolver
	}
	addrs, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("cannot resolve host %q: %w", host, err)
	}
	if len(addrs) == 0 {
		// No addresses and no error means nothing was checked. Returning nil
		// here would allow the request on the strength of an empty answer.
		return fmt.Errorf("cannot resolve host %q: resolver returned no addresses", host)
	}
	for _, addr := range addrs {
		if err := p.CheckIP(addr.IP); err != nil {
			return fmt.Errorf("host %q resolves to a blocked address: %w", host, err)
		}
	}
	return nil
}

// fingerprint returns a short, stable digest of the policy's decision-making
// fields. Cached responses are keyed on it so an entry fetched under one
// policy is never served to a client running a stricter one: the cache sits
// above the transport, so a hit returns without any policy check having run.
//
// Only fields that change a verdict are included. Resolver is deliberately
// excluded -- it is a function value with no stable identity, and it affects
// how a target is resolved rather than which addresses are permitted.
func (p *IPPolicy) fingerprint() string {
	h := sha256.New()
	if p == nil {
		// Distinct from a zero-valued policy, which denies rather than
		// deferring to the package default.
		_, _ = io.WriteString(h, "nil")
		return hex.EncodeToString(h.Sum(nil))[:16]
	}

	fmt.Fprintf(h, "allowPrivate=%t;trustProxyResolution=%t;", p.AllowPrivate, p.TrustProxyResolution)

	// Sort so two policies built from the same ranges in a different order
	// share a key rather than silently halving the cache hit rate.
	cidrs := make([]string, 0, len(p.AllowedCIDRs))
	for _, n := range p.AllowedCIDRs {
		if n == nil {
			continue
		}
		cidrs = append(cidrs, n.String())
	}
	sort.Strings(cidrs)
	for _, c := range cidrs {
		fmt.Fprintf(h, "cidr=%s;", c)
	}

	return hex.EncodeToString(h.Sum(nil))[:16]
}

// ControlFunc returns a net.Dialer.Control function that enforces the policy
// on the concrete address being connected to, after DNS resolution.
//
// This is the authoritative check: unlike URL validation it cannot be bypassed
// by a hostname that resolves to a private address, by DNS rebinding, or by a
// redirect chain, because it runs on the actual socket address.
func (p *IPPolicy) ControlFunc() func(network, address string, c syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			// Address is not host:port; fall back to treating it as a bare host.
			host = address
		}
		ip := net.ParseIP(stripZone(host))
		if ip == nil {
			return fmt.Errorf("%w: refusing to dial unresolvable address %q", ErrBlockedByPolicy, address)
		}
		return p.CheckIP(ip)
	}
}

// stripZone removes an IPv6 scope-zone suffix ("fe80::1%eth0" -> "fe80::1").
// net.ParseIP rejects a zoned address, which would otherwise deny a
// link-local dial even under a policy that explicitly allows fe80::/10. Only
// the parsed form is stripped; the address handed to the dialer keeps its
// zone.
func stripZone(host string) string {
	if i := strings.LastIndex(host, "%"); i >= 0 {
		return host[:i]
	}
	return host
}

// ValidateURLNotPrivate returns an error if rawURL uses a scheme other than
// http/https, or if its host is an IP literal that falls within a private,
// loopback, link-local, or otherwise reserved range, or is a well-known alias
// for a private/metadata address.
//
// Non-canonical IP forms (decimal, hex, octal) that net.ParseIP does not
// recognise are also rejected, because some HTTP stacks silently convert
// them to standard IPs.
//
// Arbitrary hostnames are not pre-resolved, because DNS lookups introduce
// TOCTOU races. Clients built by NewClient additionally enforce the policy at
// dial time, which closes that gap.
func ValidateURLNotPrivate(rawURL string) error {
	return defaultIPPolicy.ValidateURL(rawURL)
}

// ValidateURLNotPrivateExcept behaves like ValidateURLNotPrivate but permits
// addresses inside the supplied CIDR blocks. Cloud instance-metadata
// endpoints remain blocked regardless of the exceptions given.
func ValidateURLNotPrivateExcept(rawURL string, allowedCIDRs ...string) error {
	policy, err := NewIPPolicy(allowedCIDRs...)
	if err != nil {
		return err
	}
	return policy.ValidateURL(rawURL)
}
