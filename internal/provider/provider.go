// Package provider is the registry of providers speedprobe knows about and
// the parsing of provider and measurement selections from the command line.
package provider

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/akshtray/speedprobe/internal/engine"
	"github.com/akshtray/speedprobe/internal/provider/cloudflare"
	"github.com/akshtray/speedprobe/internal/provider/mlab"
)

// Config carries the provider-specific choices from the command line.
type Config struct {
	MLabServer int // index into M-Lab's nearest-first server list; 0 = closest
}

// UsageError means the user asked for something that does not exist.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// Registry holds the providers in display order.
type Registry struct {
	list []engine.Provider
}

// New builds the registry. version identifies speedprobe to providers.
func New(version string, cfg Config) *Registry {
	client := engine.NewHTTPClient("speedprobe/" + version)
	return &Registry{list: []engine.Provider{
		cloudflare.New(client),
		mlab.New(client, version, mlab.Config{ServerIndex: cfg.MLabServer}),
	}}
}

// All returns every provider in display order.
func (r *Registry) All() []engine.Provider { return slices.Clone(r.list) }

// Names returns the canonical names in display order.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.list))
	for _, p := range r.list {
		out = append(out, p.Info().Name)
	}
	return out
}

// Title returns the display name for a provider name, or the name itself.
func (r *Registry) Title(name string) string {
	for _, p := range r.list {
		if info := p.Info(); info.Name == name {
			return info.Title
		}
	}
	return name
}

// Resolve turns command-line tokens into providers, in the order given and
// without duplicates. A token is a name, an alias, a 1-based position in the
// list, or "all"/"a". Tokens may themselves be comma-separated.
func (r *Registry) Resolve(tokens []string) ([]engine.Provider, error) {
	var picked []engine.Provider
	add := func(p engine.Provider) {
		if !slices.Contains(picked, p) {
			picked = append(picked, p)
		}
	}
	for _, raw := range tokens {
		for tok := range strings.SplitSeq(raw, ",") {
			tok = strings.ToLower(strings.TrimSpace(tok))
			if tok == "" {
				continue
			}
			if tok == "a" || tok == "all" {
				for _, p := range r.list {
					add(p)
				}
				continue
			}
			if n, err := strconv.Atoi(tok); err == nil && n >= 1 && n <= len(r.list) {
				add(r.list[n-1])
				continue
			}
			found := false
			for _, p := range r.list {
				info := p.Info()
				if tok == info.Name || slices.Contains(info.Aliases, tok) {
					add(p)
					found = true
					break
				}
			}
			if !found {
				return nil, &UsageError{fmt.Sprintf("unknown provider %q (choose from: %s)", tok, strings.Join(r.Names(), ", "))}
			}
		}
	}
	return picked, nil
}

// ParseMeasurements parses a comma-separated list such as "latency,download".
// Empty or "all" means every measurement, in canonical order.
func ParseMeasurements(spec string) ([]engine.Measurement, error) {
	want := map[engine.Measurement]bool{}
	for item := range strings.SplitSeq(spec, ",") {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "" {
			continue
		}
		if item == "all" {
			return slices.Clone(engine.AllMeasurements), nil
		}
		m := engine.Measurement(item)
		if !slices.Contains(engine.AllMeasurements, m) {
			names := make([]string, 0, len(engine.AllMeasurements))
			for _, a := range engine.AllMeasurements {
				names = append(names, string(a))
			}
			return nil, &UsageError{fmt.Sprintf("unknown measurement %q (choose from: %s)", item, strings.Join(names, ", "))}
		}
		want[m] = true
	}
	if len(want) == 0 {
		return slices.Clone(engine.AllMeasurements), nil
	}
	out := make([]engine.Measurement, 0, len(want))
	for _, m := range engine.AllMeasurements { // canonical order regardless of input order
		if want[m] {
			out = append(out, m)
		}
	}
	return out, nil
}
