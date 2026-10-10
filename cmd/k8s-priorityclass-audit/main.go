// Command k8s-priorityclass-audit reports who the Kubernetes scheduler will
// preempt first, and which pods are already broken by a missing PriorityClass.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/moveeeax/k8s-priorityclass-audit/internal/audit"
	"github.com/moveeeax/k8s-priorityclass-audit/internal/fixture"
	"github.com/moveeeax/k8s-priorityclass-audit/internal/kube"
)

// version is overridden at release time with -ldflags "-X main.version=...".
var version = "0.1.0"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if ec, ok := err.(exitCode); ok {
			os.Exit(int(ec))
		}
		fmt.Fprintf(os.Stderr, "k8s-priorityclass-audit: %v\n", err)
		os.Exit(2)
	}
}

// exitCode is a sentinel error carrying a process exit status, so --fail-on
// does not have to call os.Exit from the middle of the flow.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("k8s-priorityclass-audit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		kubeconfig  = fs.String("kubeconfig", "", "path to kubeconfig (default: KUBECONFIG, then ~/.kube/config)")
		contextName = fs.String("context", "", "kubeconfig context to use (default: current context)")
		namespace   = fs.String("namespace", "", "limit pods to one namespace (default: all namespaces)")
		fromFile    = fs.String("from-file", "", "read Pods/PriorityClasses/Nodes from a YAML file instead of a cluster")
		format      = fs.String("format", "text", "output format: text or json")
		failOn      = fs.String("fail-on", "", "comma-separated rules that make the exit code 1 (or 'any')")
		criticalNS  = fs.String("critical-ns", "kube-system", "comma-separated namespaces whose pods must outrank ordinary workloads")
		maxLadder   = fs.Int("max-ladder", 25, "rows of the priority ladder to print in text mode (0 = all)")
		timeout     = fs.Duration("timeout", 30*time.Second, "API request timeout")
		showVersion = fs.Bool("version", false, "print version and exit")
	)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: k8s-priorityclass-audit [flags]\n\nrules: %s\n\nflags:\n", strings.Join(audit.AllRules, ", "))
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return exitCode(2)
	}
	if *showVersion {
		fmt.Fprintln(stdout, version)
		return nil
	}
	if *format != "text" && *format != "json" {
		return fmt.Errorf("--format must be text or json, got %q", *format)
	}
	failRules, err := parseFailOn(*failOn)
	if err != nil {
		return err
	}

	var in audit.Input
	if *fromFile != "" {
		if in, err = fixture.Load(*fromFile); err != nil {
			return fmt.Errorf("read %s: %w", *fromFile, err)
		}
	} else {
		client, err := kube.New(*kubeconfig, *contextName)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		if in, err = client.Collect(ctx, *namespace); err != nil {
			return err
		}
	}
	in.CriticalNamespaces = splitList(*criticalNS)

	rep := audit.Run(in)
	if *format == "json" {
		if err := audit.WriteJSON(stdout, rep); err != nil {
			return err
		}
	} else if err := audit.WriteText(stdout, rep, *maxLadder); err != nil {
		return err
	}

	if hit := matchedRules(rep, failRules); len(hit) > 0 {
		fmt.Fprintf(stderr, "failing on: %s\n", strings.Join(hit, ", "))
		return exitCode(1)
	}
	return nil
}

// parseFailOn validates the rule names up front: a typo in CI must not read
// as "nothing to fail on".
func parseFailOn(spec string) ([]string, error) {
	items := splitList(spec)
	if len(items) == 1 && items[0] == "any" {
		return append([]string{}, audit.AllRules...), nil
	}
	for _, item := range items {
		if !containsString(audit.AllRules, item) {
			return nil, fmt.Errorf("unknown rule %q in --fail-on (known: %s, any)", item, strings.Join(audit.AllRules, ", "))
		}
	}
	return items, nil
}

func matchedRules(rep *audit.Report, failRules []string) []string {
	var hit []string
	for _, rule := range audit.AllRules {
		if rep.Counts[rule] > 0 && containsString(failRules, rule) {
			hit = append(hit, rule)
		}
	}
	return hit
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
