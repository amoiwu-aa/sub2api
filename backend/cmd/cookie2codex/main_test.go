package main

import (
	"errors"
	"flag"
	"io"
	"strings"
	"testing"
)

func TestParseFlagsRequiresExplicitInput(t *testing.T) {
	if _, err := parseFlags(nil, io.Discard); err == nil {
		t.Fatal("missing -i should fail")
	}
	opts, err := parseFlags([]string{"-i", "-", "-o", "-"}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if opts.inputPath != "-" || opts.output != "-" {
		t.Fatalf("opts = %#v", opts)
	}
}

func TestParseFlagsHelp(t *testing.T) {
	var output strings.Builder
	_, err := parseFlags([]string{"-h"}, &output)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("-h error = %v, want flag.ErrHelp", err)
	}
	if !strings.Contains(output.String(), "cookie2codex") ||
		!strings.Contains(output.String(), "-user-agent") {
		t.Fatalf("help output is incomplete:\n%s", output.String())
	}
}

func TestBuildHTTPClientRejectsUnsupportedProxy(t *testing.T) {
	if _, err := buildHTTPClient("ftp://127.0.0.1:21"); err == nil {
		t.Fatal("unsupported proxy should fail")
	}
	if _, err := buildHTTPClient("socks5://127.0.0.1:1080"); err != nil {
		t.Fatalf("SOCKS5 proxy rejected: %v", err)
	}
}
