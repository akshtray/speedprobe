package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/akshtray/speedprobe/internal/engine"
)

// Table writes the results as an aligned table. title maps provider names to
// display names.
func Table(w io.Writer, results []*engine.Result, title func(string) string, now time.Time) {
	fmt.Fprintf(w, "speedprobe · %s\n\n", now.Format("2006-01-02 15:04 MST"))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PROVIDER\tSERVER\tPING\tJITTER\tLOADED ↓ / ↑\tDOWNLOAD\tUPLOAD\tNOTES")
	for _, r := range results {
		loaded := "—"
		if r.LoadedLatencyDownMs != nil || r.LoadedLatencyUpMs != nil {
			loaded = fmtInt(r.LoadedLatencyDownMs) + " / " + fmtInt(r.LoadedLatencyUpMs) + " ms"
		}
		var notes []string
		if r.Error != "" {
			notes = append(notes, r.Error)
		}
		if r.Note != "" {
			notes = append(notes, r.Note)
		}
		if r.PacketLossPct != nil {
			notes = append(notes, fmt.Sprintf("loss %.1f%%", *r.PacketLossPct))
		}
		if u, ok := r.Extra["result_url"].(string); ok && u != "" {
			notes = append(notes, u)
		}
		server := r.Server
		if server == "" {
			server = "—"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			title(r.Provider), server, fmtUnit(r.LatencyMs, "ms"), fmtUnit(r.JitterMs, "ms"), loaded,
			fmtUnit(r.DownloadMbps, "Mbps"), fmtUnit(r.UploadMbps, "Mbps"), strings.Join(notes, "; "))
	}
	tw.Flush()
}

func fmtInt(v *float64) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%.0f", *v)
}

func fmtUnit(v *float64, unit string) string {
	if v == nil {
		return "—"
	}
	return thousands(*v) + " " + unit
}

// thousands formats v with one decimal and comma separators: 1234.56 -> "1,234.6".
func thousands(v float64) string {
	s := fmt.Sprintf("%.1f", v)
	intPart, frac, _ := strings.Cut(s, ".")
	neg := strings.HasPrefix(intPart, "-")
	intPart = strings.TrimPrefix(intPart, "-")
	var b strings.Builder
	for i, c := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	out := b.String() + "." + frac
	if neg {
		out = "-" + out
	}
	return out
}

// JSON writes the results as a JSON array, pretty-printed when indent is set.
func JSON(w io.Writer, results []*engine.Result, indent bool) error {
	enc := json.NewEncoder(w)
	if indent {
		enc.SetIndent("", "  ")
	}
	if results == nil {
		results = []*engine.Result{}
	}
	return enc.Encode(results)
}

// AppendLog appends one JSON line per result to path, creating it if needed.
func AppendLog(path string, results []*engine.Result) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range results {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return nil
}
