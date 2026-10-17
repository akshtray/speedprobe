package provider

import (
	"errors"
	"slices"
	"testing"

	"github.com/akshtray/speedprobe/internal/engine"
)

func TestResolve(t *testing.T) {
	r := New("test", Config{})
	for _, tokens := range [][]string{{"cloudflare"}, {"cf"}, {"CF"}, {"1"}, {"cf,cloudflare"}, {"cf", "1"}} {
		ps, err := r.Resolve(tokens)
		if err != nil || len(ps) != 1 || ps[0].Info().Name != "cloudflare" {
			t.Errorf("Resolve(%v) = %v, %v", tokens, ps, err)
		}
	}
	for _, tokens := range [][]string{{"mlab,cf"}, {"ndt7", "1"}, {"2,1"}} {
		ps, err := r.Resolve(tokens)
		if err != nil || len(ps) != 2 {
			t.Errorf("Resolve(%v) = %v, %v", tokens, ps, err)
		}
	}
	if ps, err := r.Resolve([]string{"all"}); err != nil || len(ps) != 4 {
		t.Errorf("all should give every provider, got %d %v", len(ps), err)
	}
	if ps, err := r.Resolve([]string{"speedtest", "netflix"}); err != nil || ps[0].Info().Name != "ookla" || ps[1].Info().Name != "fast" {
		t.Errorf("aliases: %v %v", ps, err)
	}
	if ps, _ := r.Resolve([]string{"mlab,cf"}); ps[0].Info().Name != "mlab" {
		t.Errorf("order must follow the command line, got %s first", ps[0].Info().Name)
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
