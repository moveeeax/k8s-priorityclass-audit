package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moveeeax/k8s-priorityclass-audit/internal/audit"
)

const clusterFixture = `
apiVersion: scheduling.k8s.io/v1
kind: PriorityClass
metadata:
  name: batch
value: 1000
---
apiVersion: v1
kind: Pod
metadata:
  name: big-batch-1
  namespace: team-a
spec:
  priorityClassName: batch
  nodeName: node-1
---
apiVersion: v1
kind: Pod
metadata:
  name: cni-agent-xyz
  namespace: kube-system
  ownerReferences:
    - apiVersion: apps/v1
      kind: DaemonSet
      name: cni-agent
      uid: "1"
spec:
  nodeName: node-1
---
apiVersion: v1
kind: Pod
metadata:
  name: broken-1
  namespace: team-a
spec:
  priorityClassName: deleted-class
  nodeName: node-1
---
apiVersion: v1
kind: Node
metadata:
  name: node-1
`

func writeFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cluster.yaml")
	if err := os.WriteFile(path, []byte(clusterFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func exit(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	ec, ok := err.(exitCode)
	if !ok {
		t.Fatalf("unexpected error: %v", err)
	}
	return int(ec)
}

func TestRunTextOutput(t *testing.T) {
	var out, errBuf bytes.Buffer
	err := run([]string{"--from-file", writeFixture(t)}, &out, &errBuf)
	if code := exit(t, err); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errBuf.String())
	}
	for _, want := range []string{"deleted-class", "cni-agent-xyz", "PREEMPTION ORDER", "node-1"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunJSONOutput(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := exit(t, run([]string{"--from-file", writeFixture(t), "--format", "json"}, &out, &errBuf)); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var rep audit.Report
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if rep.Counts[audit.RuleMissingPriorityClass] != 1 {
		t.Fatalf("counts = %+v", rep.Counts)
	}
	if rep.Counts[audit.RuleUnprotectedCritical] != 1 {
		t.Fatalf("daemonset pod should be unprotected: %+v", rep.Findings)
	}
}

func TestFailOnExitCodes(t *testing.T) {
	path := writeFixture(t)
	cases := []struct {
		args []string
		want int
	}{
		{[]string{"--fail-on", "missing-priorityclass"}, 1},
		{[]string{"--fail-on", "any"}, 1},
		{[]string{"--fail-on", "multiple-global-defaults"}, 0},
		{[]string{}, 0},
	}
	for _, tc := range cases {
		var out, errBuf bytes.Buffer
		args := append([]string{"--from-file", path, "--format", "json"}, tc.args...)
		if code := exit(t, run(args, &out, &errBuf)); code != tc.want {
			t.Fatalf("args %v: exit = %d, want %d (stderr %s)", tc.args, code, tc.want, errBuf.String())
		}
	}
}

func TestCriticalNamespaceFlag(t *testing.T) {
	path := writeFixture(t)
	var out, errBuf bytes.Buffer
	// team-a declared critical: the batch job itself becomes critical, so the
	// only remaining ordinary workload is at priority 0 and nothing preempts.
	if code := exit(t, run([]string{"--from-file", path, "--format", "json", "--critical-ns", "team-a"}, &out, &errBuf)); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var rep audit.Report
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Counts[audit.RuleUnprotectedCritical] != 0 {
		t.Fatalf("unexpected findings: %+v", rep.Findings)
	}
}

func TestUnknownFailOnRuleIsAnError(t *testing.T) {
	var out, errBuf bytes.Buffer
	err := run([]string{"--from-file", writeFixture(t), "--fail-on", "no-such-rule"}, &out, &errBuf)
	if err == nil || !strings.Contains(err.Error(), "unknown rule") {
		t.Fatalf("err = %v, want unknown rule", err)
	}
}

func TestBadFormatIsAnError(t *testing.T) {
	var out, errBuf bytes.Buffer
	if err := run([]string{"--format", "yaml"}, &out, &errBuf); err == nil {
		t.Fatal("expected an error for --format yaml")
	}
}

func TestMissingFixtureIsAnError(t *testing.T) {
	var out, errBuf bytes.Buffer
	if err := run([]string{"--from-file", "/nonexistent.yaml"}, &out, &errBuf); err == nil {
		t.Fatal("expected an error for a missing fixture")
	}
}

func TestVersion(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := exit(t, run([]string{"--version"}, &out, &errBuf)); code != 0 {
		t.Fatal("version should exit 0")
	}
	if strings.TrimSpace(out.String()) == "" {
		t.Fatal("version printed nothing")
	}
}

func TestParseFailOn(t *testing.T) {
	got, err := parseFailOn("any")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(audit.AllRules) {
		t.Fatalf("any = %v", got)
	}
	if got, err := parseFailOn(""); err != nil || len(got) != 0 {
		t.Fatalf("empty = %v, %v", got, err)
	}
}
