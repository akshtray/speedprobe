package provider

import (
	"errors"
	"slices"
	"testing"

	"github.com/akshtray/speedprobe/internal/engine"
)

func TestResolve(t *testing.T) {
	r := New("test")
	for _, tokens := range [][]string{{"cloudflare"}, {"cf"}, {"CF"}, {"1"}, {"all"}, {"cf,cloudflare"}, {"cf", "1"}} {
		ps, err := r.Resolve(tokens)
		if err != nil || len(ps) != 1 || ps[0].Info().Name != "cloudflare" {
			t.Errorf("Resolve(%v) = %v, %v", tokens, ps, err)
		}
	}
	_, err := r.Resolve([]string{"nope"})
	var ue *UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("unknown provider should be a UsageError, got %v", err)
	}
}

func TestParseMeasurements(t *testing.T) {
	all := engine.AllMeasurements
	cases := map[string][]engine.Measurement{
		"":                        all,
		"all":                     all,
		"latency,download,upload": all,
		"upload,latency":          {engine.Latency, engine.Upload},
		" Download ":              {engine.Download},
	}
	for in, want := range cases {
		got, err := ParseMeasurements(in)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("ParseMeasurements(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := ParseMeasurements("ping"); err == nil {
		t.Fatal("unknown measurement should fail")
	}
}
