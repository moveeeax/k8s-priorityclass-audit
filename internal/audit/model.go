// Package audit resolves Kubernetes pod priority and reports the exposure it
// creates. It is deliberately free of any Kubernetes client: callers hand it
// plain object slices, which makes every rule table-driven testable.
package audit

import (
	corev1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
)

// Rule names. These strings are the contract: they appear in JSON output and
// in --fail-on, so they must stay stable.
const (
	RuleMissingPriorityClass   = "missing-priorityclass"
	RuleNoGlobalDefault        = "no-global-default"
	RuleMultipleGlobalDefaults = "multiple-global-defaults"
	RuleUnprotectedCritical    = "unprotected-critical"
)

// AllRules is every rule the auditor can emit, in report order.
var AllRules = []string{
	RuleMissingPriorityClass,
	RuleNoGlobalDefault,
	RuleMultipleGlobalDefaults,
	RuleUnprotectedCritical,
}

// Severity levels, highest first.
const (
	SeverityHigh   = "high"
	SeverityMedium = "medium"
	SeverityLow    = "low"
)

// Input is the cluster state the rules run against.
type Input struct {
	Pods            []corev1.Pod
	PriorityClasses []schedulingv1.PriorityClass
	Nodes           []corev1.Node

	// CriticalNamespaces are namespaces whose pods must outrank ordinary
	// workloads. DaemonSet pods are treated as critical regardless.
	CriticalNamespaces []string
}

// Finding is one problem worth a human's attention.
type Finding struct {
	Rule          string `json:"rule"`
	Severity      string `json:"severity"`
	Namespace     string `json:"namespace,omitempty"`
	Pod           string `json:"pod,omitempty"`
	Node          string `json:"node,omitempty"`
	PriorityClass string `json:"priorityClass,omitempty"`
	Priority      *int32 `json:"priority,omitempty"`
	Message       string `json:"message"`
}

// PodPriority is a pod's resolved place in the priority ladder.
type PodPriority struct {
	Namespace     string `json:"namespace"`
	Pod           string `json:"pod"`
	Node          string `json:"node,omitempty"`
	PriorityClass string `json:"priorityClass,omitempty"`
	Priority      int32  `json:"priority"`
	QOS           string `json:"qos"`
	Critical      bool   `json:"critical"`
	// Source records how the priority was resolved: "pod-spec", "class",
	// "global-default", "none" or "missing-class".
	Source string `json:"source"`
}

// NodeEvictionOrder is the order pods on one node would be preempted in.
type NodeEvictionOrder struct {
	Node string        `json:"node"`
	Pods []PodPriority `json:"pods"`
}

// ClassSummary describes one PriorityClass as the cluster sees it.
type ClassSummary struct {
	Name             string `json:"name"`
	Value            int32  `json:"value"`
	GlobalDefault    bool   `json:"globalDefault"`
	System           bool   `json:"system"`
	Pods             int    `json:"pods"`
	PreemptionPolicy string `json:"preemptionPolicy,omitempty"`
}

// Report is the full result of one audit run.
type Report struct {
	Classes       []ClassSummary      `json:"classes"`
	Ladder        []PodPriority       `json:"ladder"`
	Findings      []Finding           `json:"findings"`
	Eviction      []NodeEvictionOrder `json:"preemptionOrder"`
	GlobalDefault string              `json:"globalDefault,omitempty"`
	Counts        map[string]int      `json:"counts"`
}

// RulesFired lists, in AllRules order, every rule with at least one finding.
func (r *Report) RulesFired() []string {
	fired := make([]string, 0, len(AllRules))
	for _, rule := range AllRules {
		if r.Counts[rule] > 0 {
			fired = append(fired, rule)
		}
	}
	return fired
}
