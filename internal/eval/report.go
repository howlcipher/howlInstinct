package eval

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// WriteHuman renders a report for a person.
//
// Unavailable metrics are printed with their reason rather than omitted. A
// missing line invites the reader to assume the metric was fine; a line
// saying why it could not be computed does not.
func (r Report) WriteHuman(w io.Writer) {
	fmt.Fprintf(w, "Evaluation: %s (v%d, %d cases)\n", r.Dataset.Name, r.Dataset.Version, r.Dataset.Cases)
	fmt.Fprintf(w, "Dataset:    %s\n", r.Dataset.Path)
	fmt.Fprintf(w, "Digest:     %s\n", r.Dataset.SHA256)
	fmt.Fprintf(w, "Provider:   %s", r.Provider)
	if r.Model != "" {
		fmt.Fprintf(w, "  model %s", r.Model)
	}
	fmt.Fprintf(w, "\nStarted:    %s  (%dms)\n\n", r.StartedAt, r.DurationMS)

	m := r.Metrics
	fmt.Fprintf(w, "  scored            %d of %d\n", m.Scored, m.Cases)
	if m.Errored > 0 {
		fmt.Fprintf(w, "  errored           %d\n", m.Errored)
	}
	fmt.Fprintf(w, "  accuracy          %s\n", metricText(m.Accuracy, m.AccuracyUnavailable, "%.4f"))
	fmt.Fprintf(w, "  brier             %s\n", metricText(m.Brier, m.BrierUnavailable, "%.4f"))
	fmt.Fprintf(w, "  log loss          %s\n", metricText(m.LogLoss, m.LogLossUnavailable, "%.4f"))
	fmt.Fprintf(w, "  mean abs error    %s\n", metricText(m.MAE, m.MAEUnavailable, "%.4f"))

	if r.EscalationRuleSupplied {
		fmt.Fprintf(w, "  escalation rate   %.4f\n", m.EscalationRate)
	} else {
		fmt.Fprintf(w, "  escalation rate   0 (no caller threshold was supplied, so nothing could escalate)\n")
	}
	fmt.Fprintf(w, "  latency           p50 %dms  p95 %dms  max %dms\n",
		m.Latency.P50MS, m.Latency.P95MS, m.Latency.MaxMS)

	if len(m.Calibration) > 0 {
		fmt.Fprintln(w, "\n  calibration (predicted vs observed)")
		for _, b := range m.Calibration {
			fmt.Fprintf(w, "    [%.1f-%.1f)  n=%-4d predicted %.3f  observed %.3f\n",
				b.LowerBound, b.UpperBound, b.Count, deref(b.MeanScore), deref(b.Accuracy))
		}
	} else if m.CalibrationUnavailable != "" {
		fmt.Fprintf(w, "\n  calibration       unavailable: %s\n", m.CalibrationUnavailable)
	}

	if len(m.Confusion) > 0 {
		fmt.Fprintln(w, "\n  confusion (expected -> predicted)")
		for _, expected := range sortedKeys(m.Confusion) {
			row := m.Confusion[expected]
			parts := make([]string, 0, len(row))
			for _, predicted := range sortedKeys(row) {
				parts = append(parts, fmt.Sprintf("%s=%d", predicted, row[predicted]))
			}
			fmt.Fprintf(w, "    %-24s %s\n", expected, strings.Join(parts, "  "))
		}
	}

	if len(r.Failures) > 0 {
		fmt.Fprintln(w, "\n  failures")
		for _, f := range r.Failures {
			fmt.Fprintf(w, "    %-24s %s\n", f.CaseID, f.Reason)
		}
	}

	fmt.Fprintf(w, "\n%s\n", wrap(r.Caveat, 78))
}

// metricText prints a value, or the reason there is no value. It never
// substitutes zero for unknown.
func metricText[T any](v *T, reason Unavailable, format string) string {
	if v != nil {
		return fmt.Sprintf(format, *v)
	}
	if reason != "" {
		return "unavailable: " + string(reason)
	}
	return "unavailable"
}

func deref(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func wrap(s string, width int) string {
	var b strings.Builder
	line := 0
	for i, word := range strings.Fields(s) {
		if line+len(word)+1 > width && i > 0 {
			b.WriteString("\n")
			line = 0
		} else if i > 0 {
			b.WriteString(" ")
			line++
		}
		b.WriteString(word)
		line += len(word)
	}
	return b.String()
}
