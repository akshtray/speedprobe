package output

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/akshtray/speedprobe/internal/engine"
)

// ProviderList writes the provider table shown by `speedprobe list`.
func ProviderList(w io.Writer, providers []engine.Provider) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tALIASES\tTYPE\tSTATUS\tMEASURES")
	var notes []string
	for _, p := range providers {
		info := p.Info()
		kind := string(info.Kind)
		if info.Experimental {
			kind += ", experimental"
		}
		_, status := p.Available()
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", info.Name, strings.Join(info.Aliases, ", "), kind, status, info.Blurb)
		if info.DataPolicy != "" {
			notes = append(notes, info.Name+": "+info.DataPolicy)
		}
	}
	tw.Flush()
	for _, n := range notes {
		fmt.Fprintln(w, "\n"+n)
	}
}
