package audit

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// WriteJSON prints the whole report as one JSON object.
func WriteJSON(w io.Writer, rep *Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

// WriteText prints the human report: the class ladder, the findings, and the
// per-node preemption order.
func WriteText(w io.Writer, rep *Report, maxLadder int) error {
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)

	fmt.Fprintln(tw, "PRIORITY CLASSES")
	fmt.Fprintln(tw, "NAME\tVALUE\tGLOBAL-DEFAULT\tPODS\tPREEMPTION")
	if len(rep.Classes) == 0 {
		fmt.Fprintln(tw, "<none>\t-\t-\t-\t-")
	}
	for _, c := range rep.Classes {
		policy := c.PreemptionPolicy
		if policy == "" {
			policy = "PreemptLowerPriority"
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%d\t%s\n", c.Name, c.Value, yesNo(c.GlobalDefault), c.Pods, policy)
	}
	fmt.Fprintln(tw)

	fmt.Fprintf(tw, "PRIORITY LADDER (%d pods)\n", len(rep.Ladder))
	fmt.Fprintln(tw, "PRIORITY\tNAMESPACE/POD\tCLASS\tQOS\tCRITICAL\tSOURCE")
	shown := rep.Ladder
	if maxLadder > 0 && len(shown) > maxLadder {
		shown = shown[:maxLadder]
	}
	for _, p := range shown {
		fmt.Fprintf(tw, "%d\t%s/%s\t%s\t%s\t%s\t%s\n",
			p.Priority, p.Namespace, p.Pod, classOrNone(p.PriorityClass), p.QOS, yesNo(p.Critical), p.Source)
	}
	if len(shown) < len(rep.Ladder) {
		fmt.Fprintf(tw, "... %d more (use --format json for all)\n", len(rep.Ladder)-len(shown))
	}
	fmt.Fprintln(tw)

	fmt.Fprintln(tw, "PREEMPTION ORDER (first victim first)")
	if len(rep.Eviction) == 0 {
		fmt.Fprintln(tw, "<no scheduled pods>")
	}
	for _, n := range rep.Eviction {
		parts := make([]string, 0, len(n.Pods))
		for _, p := range n.Pods {
			parts = append(parts, fmt.Sprintf("%s/%s(%d,%s)", p.Namespace, p.Pod, p.Priority, shortQOS(p.QOS)))
		}
		fmt.Fprintf(tw, "%s\t%s\n", n.Node, strings.Join(parts, " -> "))
	}
	fmt.Fprintln(tw)

	fmt.Fprintf(tw, "FINDINGS (%d)\n", len(rep.Findings))
	if len(rep.Findings) == 0 {
		fmt.Fprintln(tw, "none")
	}
	for _, f := range rep.Findings {
		fmt.Fprintf(tw, "[%s] %s\t%s\n", strings.ToUpper(f.Severity), f.Rule, f.Message)
	}
	if len(rep.Findings) > 0 {
		fmt.Fprintln(tw)
		counts := make([]string, 0, len(rep.Counts))
		for _, rule := range AllRules {
			if rep.Counts[rule] > 0 {
				counts = append(counts, fmt.Sprintf("%s=%d", rule, rep.Counts[rule]))
			}
		}
		fmt.Fprintf(tw, "summary\t%s\n", strings.Join(counts, " "))
	}
	return tw.Flush()
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func shortQOS(q string) string {
	switch q {
	case "BestEffort":
		return "BE"
	case "Burstable":
		return "Bu"
	case "Guaranteed":
		return "G"
	default:
		return "?"
	}
}
