package logging

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewJSONLogger(t *testing.T) {
	var out bytes.Buffer
	logger, err := New(&out, Config{Level: "debug", Format: "json"})
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("watch started", "component", "watch")
	text := out.String()
	if !strings.Contains(text, `"msg":"watch started"`) || !strings.Contains(text, `"component":"watch"`) {
		t.Fatalf("log output = %s", text)
	}
}

func TestNewLogfmtLogger(t *testing.T) {
	var out bytes.Buffer
	logger, err := New(&out, Config{Level: "info", Format: "logfmt"})
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("watch started", "component", "watch")
	text := out.String()
	for _, want := range []string{"msg=\"watch started\"", "component=watch"} {
		if !strings.Contains(text, want) {
			t.Fatalf("log output missing %q: %s", want, text)
		}
	}
}

func TestNewRejectsBadFormat(t *testing.T) {
	_, err := New(&bytes.Buffer{}, Config{Level: "info", Format: "xml"})
	if err == nil {
		t.Fatal("expected error")
	}
}
