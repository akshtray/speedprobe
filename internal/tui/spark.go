package tui

import "strings"

var sparkChars = []rune("▁▂▃▄▅▆▇█")

// spark draws the last width values as a block-character sparkline scaled
// to their maximum. It returns "" when there is nothing to draw.
func spark(values []float64, width int) string {
	if width <= 0 || len(values) == 0 {
		return ""
	}
	if len(values) > width {
		values = values[len(values)-width:]
	}
	var hi float64
	for _, v := range values {
		hi = max(hi, v)
	}
	var b strings.Builder
	for _, v := range values {
		idx := 0
		if hi > 0 {
			idx = int(v / hi * float64(len(sparkChars)-1))
		}
		b.WriteRune(sparkChars[max(0, min(idx, len(sparkChars)-1))])
	}
	return b.String()
}
