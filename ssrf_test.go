// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package httpc

import (
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
		{name: "link-local 169.254.169.254", url: "http://169.254.169.254/", wantErr: true, errMsg: "private/reserved"},
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
		{name: "empty host (relative URL)", url: "/relative/path", wantErr: false},
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

func TestBuildPrivateIPNets(t *testing.T) {
	nets := buildPrivateIPNets()
	// Should contain all 9 CIDR blocks defined in the source
	assert.Len(t, nets, 9)
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
