package model

import "testing"

func TestParseMCPTransport(t *testing.T) {
	cases := []struct {
		in   string
		want MCPTransport
		ok   bool
	}{
		{"stdio", MCPTransportStdio, true},
		{"SSE", MCPTransportSSE, true},
		{" Streamable ", MCPTransportStreamable, true},
		{"http", "", false},
		{"", "", false},
		{"STDIO", MCPTransportStdio, true},
	}
	for _, c := range cases {
		got, ok := ParseMCPTransport(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("ParseMCPTransport(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestMCPServer_Validate(t *testing.T) {
	validStdio := &MCPServer{Name: "srv", Transport: MCPTransportStdio, Command: "npx"}
	if err := validStdio.Validate(); err != nil {
		t.Fatalf("valid stdio should pass: %v", err)
	}
	validSSE := &MCPServer{Name: "srv", Transport: MCPTransportSSE, URL: "http://x"}
	if err := validSSE.Validate(); err != nil {
		t.Fatalf("valid sse should pass: %v", err)
	}
	validStreamable := &MCPServer{Name: "srv", Transport: MCPTransportStreamable, URL: "http://x"}
	if err := validStreamable.Validate(); err != nil {
		t.Fatalf("valid streamable should pass: %v", err)
	}

	cases := []struct {
		name string
		in   *MCPServer
	}{
		{"missing name", &MCPServer{Transport: MCPTransportStdio, Command: "npx"}},
		{"stdio without command", &MCPServer{Name: "s", Transport: MCPTransportStdio}},
		{"sse without url", &MCPServer{Name: "s", Transport: MCPTransportSSE}},
		{"streamable without url", &MCPServer{Name: "s", Transport: MCPTransportStreamable}},
		{"invalid transport", &MCPServer{Name: "s", Transport: "foo", Command: "x"}},
	}
	for _, c := range cases {
		if err := c.in.Validate(); err == nil {
			t.Errorf("%s: expected validation error, got nil", c.name)
		}
	}
}
