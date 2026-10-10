package audit

import (
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
)

// systemClassPrefix marks the two classes the control plane ships
// (system-cluster-critical, system-node-critical). They always exist, even on
// a cluster that lists nothing, and they are never "the batch job that
// preempts your CNI".
const systemClassPrefix = "system-"

// Run executes every rule against the cluster state and returns the report.
func Run(in Input) *Report {
	rep := &Report{Counts: map[string]int{}}

	byName := make(map[string]schedulingv1.PriorityClass, len(in.PriorityClasses))
	var defaults []schedulingv1.PriorityClass
	for _, pc := range in.PriorityClasses {
		byName[pc.Name] = pc
		if pc.GlobalDefault {
			defaults = append(defaults, pc)
		}
	}
	sort.Slice(defaults, func(i, j int) bool { return defaults[i].Name < defaults[j].Name })

	var defaultValue int32
	if len(defaults) > 0 {
		// With more than one globalDefault the scheduler's choice is
		// undefined; the lowest value is the conservative read.
		defaultValue = defaults[0].Value
		for _, pc := range defaults[1:] {
			if pc.Value < defaultValue {
				defaultValue = pc.Value
			}
		}
		rep.GlobalDefault = defaults[0].Name
	}

	critNS := make(map[string]bool, len(in.CriticalNamespaces))
	for _, ns := range in.CriticalNamespaces {
		if ns = strings.TrimSpace(ns); ns != "" {
			critNS[ns] = true
		}
	}

	podCount := map[string]int{}
	ladder := make([]PodPriority, 0, len(in.Pods))

	for i := range in.Pods {
		pod := &in.Pods[i]
		prio, source := resolve(pod, byName, defaults, defaultValue)
		entry := PodPriority{
			Namespace:     pod.Namespace,
			Pod:           pod.Name,
			Node:          pod.Spec.NodeName,
			PriorityClass: pod.Spec.PriorityClassName,
			Priority:      prio,
			QOS:           string(qosOf(pod)),
			Critical:      isCritical(pod, critNS),
			Source:        source,
		}
		ladder = append(ladder, entry)
		if pod.Spec.PriorityClassName != "" {
			podCount[pod.Spec.PriorityClassName]++
		}

		if source == "missing-class" {
			rep.add(Finding{
				Rule:          RuleMissingPriorityClass,
				Severity:      SeverityHigh,
				Namespace:     pod.Namespace,
				Pod:           pod.Name,
				Node:          pod.Spec.NodeName,
				PriorityClass: pod.Spec.PriorityClassName,
				Priority:      ptr(prio),
				Message: fmt.Sprintf("pod %s/%s references PriorityClass %q which does not exist; it will be rejected at admission on the next reschedule",
					pod.Namespace, pod.Name, pod.Spec.PriorityClassName),
			})
		}
	}

	sort.Slice(ladder, func(i, j int) bool {
		if ladder[i].Priority != ladder[j].Priority {
			return ladder[i].Priority > ladder[j].Priority
		}
		if ladder[i].Namespace != ladder[j].Namespace {
			return ladder[i].Namespace < ladder[j].Namespace
		}
		return ladder[i].Pod < ladder[j].Pod
	})
	rep.Ladder = ladder

	// globalDefault rules fire once per cluster, never per pod.
	switch {
	case len(defaults) == 0:
		rep.add(Finding{
			Rule:     RuleNoGlobalDefault,
			Severity: SeverityMedium,
			Message:  "no PriorityClass has globalDefault=true; every pod without an explicit class lands on priority 0",
		})
	case len(defaults) > 1:
		names := make([]string, 0, len(defaults))
		for _, pc := range defaults {
			names = append(names, fmt.Sprintf("%s(%d)", pc.Name, pc.Value))
		}
		rep.add(Finding{
			Rule:     RuleMultipleGlobalDefaults,
			Severity: SeverityHigh,
			Message: fmt.Sprintf("%d PriorityClasses declare globalDefault=true (%s); which one applies is undefined",
				len(defaults), strings.Join(names, ", ")),
		})
	}

	rep.Findings = append(rep.Findings, unprotectedCritical(ladder)...)
	rep.recount()

	rep.Classes = classSummaries(in.PriorityClasses, podCount)
	rep.Eviction = evictionOrder(in.Nodes, ladder)
	return rep
}

// resolve returns the pod's effective priority and how it was derived.
func resolve(pod *corev1.Pod, byName map[string]schedulingv1.PriorityClass, defaults []schedulingv1.PriorityClass, defaultValue int32) (int32, string) {
	name := pod.Spec.PriorityClassName
	if name != "" {
		if pc, ok := byName[name]; ok {
			return pc.Value, "class"
		}
		if strings.HasPrefix(name, systemClassPrefix) {
			// Built-in control-plane classes exist whether or not the
			// caller listed them.
			if pod.Spec.Priority != nil {
				return *pod.Spec.Priority, "pod-spec"
			}
			return systemClassValue(name), "class"
		}
		if pod.Spec.Priority != nil {
			// The pod was admitted with a stamped value and the class was
			// deleted afterwards: still broken, and we know the old value.
			return *pod.Spec.Priority, "missing-class"
		}
		return 0, "missing-class"
	}
	if pod.Spec.Priority != nil {
		return *pod.Spec.Priority, "pod-spec"
	}
	if len(defaults) > 0 {
		return defaultValue, "global-default"
	}
	return 0, "none"
}

// systemClassValue returns the well-known values of the two built-in classes.
func systemClassValue(name string) int32 {
	switch name {
	case "system-node-critical":
		return 2000001000
	case "system-cluster-critical":
		return 2000000000
	default:
		return 0
	}
}

// unprotectedCritical flags critical pods that an ordinary workload is allowed
// to preempt. "Ordinary" means: not critical, not on a system-* class.
func unprotectedCritical(ladder []PodPriority) []Finding {
	var topPrio int32
	var topPod *PodPriority
	for i := range ladder {
		p := &ladder[i]
		if p.Critical || strings.HasPrefix(p.PriorityClass, systemClassPrefix) {
			continue
		}
		if topPod == nil || p.Priority > topPrio {
			topPrio, topPod = p.Priority, p
		}
	}
	if topPod == nil || topPrio <= 0 {
		// Nothing can preempt anything: equal priority does not preempt.
		return nil
	}

	var out []Finding
	for i := range ladder {
		p := &ladder[i]
		if !p.Critical || p.Priority > topPrio {
			continue
		}
		out = append(out, Finding{
			Rule:          RuleUnprotectedCritical,
			Severity:      SeverityHigh,
			Namespace:     p.Namespace,
			Pod:           p.Pod,
			Node:          p.Node,
			PriorityClass: p.PriorityClass,
			Priority:      ptr(p.Priority),
			Message: fmt.Sprintf("critical pod %s/%s runs at priority %d, at or below workload %s/%s at priority %d (class %s), which may preempt it",
				p.Namespace, p.Pod, p.Priority, topPod.Namespace, topPod.Pod, topPrio, classOrNone(topPod.PriorityClass)),
		})
	}
	return out
}

// evictionOrder sorts each node's pods the way the kubelet and the scheduler
// pick victims: lowest priority first, then BestEffort before Burstable
// before Guaranteed.
func evictionOrder(nodes []corev1.Node, ladder []PodPriority) []NodeEvictionOrder {
	byNode := map[string][]PodPriority{}
	for _, p := range ladder {
		if p.Node == "" {
			continue
		}
		byNode[p.Node] = append(byNode[p.Node], p)
	}
	names := make([]string, 0, len(byNode))
	for _, n := range nodes {
		if _, ok := byNode[n.Name]; ok {
			names = append(names, n.Name)
		}
	}
	for name := range byNode {
		if !contains(names, name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	out := make([]NodeEvictionOrder, 0, len(names))
	for _, name := range names {
		pods := byNode[name]
		sort.SliceStable(pods, func(i, j int) bool {
			if pods[i].Priority != pods[j].Priority {
				return pods[i].Priority < pods[j].Priority
			}
			if qosRank(pods[i].QOS) != qosRank(pods[j].QOS) {
				return qosRank(pods[i].QOS) < qosRank(pods[j].QOS)
			}
			if pods[i].Namespace != pods[j].Namespace {
				return pods[i].Namespace < pods[j].Namespace
			}
			return pods[i].Pod < pods[j].Pod
		})
		out = append(out, NodeEvictionOrder{Node: name, Pods: pods})
	}
	return out
}

func classSummaries(classes []schedulingv1.PriorityClass, podCount map[string]int) []ClassSummary {
	out := make([]ClassSummary, 0, len(classes))
	for _, pc := range classes {
		policy := ""
		if pc.PreemptionPolicy != nil {
			policy = string(*pc.PreemptionPolicy)
		}
		out = append(out, ClassSummary{
			Name:             pc.Name,
			Value:            pc.Value,
			GlobalDefault:    pc.GlobalDefault,
			System:           strings.HasPrefix(pc.Name, systemClassPrefix),
			Pods:             podCount[pc.Name],
			PreemptionPolicy: policy,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Value != out[j].Value {
			return out[i].Value > out[j].Value
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// qosRank orders QoS classes by how early the kubelet evicts them.
func qosRank(q string) int {
	switch corev1.PodQOSClass(q) {
	case corev1.PodQOSBestEffort:
		return 0
	case corev1.PodQOSBurstable:
		return 1
	case corev1.PodQOSGuaranteed:
		return 2
	default:
		return 3
	}
}

// qosOf prefers the value the kubelet already computed and falls back to
// deriving it, so fixtures without status still sort correctly.
func qosOf(pod *corev1.Pod) corev1.PodQOSClass {
	if pod.Status.QOSClass != "" {
		return pod.Status.QOSClass
	}
	return deriveQOS(pod)
}

func deriveQOS(pod *corev1.Pod) corev1.PodQOSClass {
	containers := append([]corev1.Container{}, pod.Spec.InitContainers...)
	containers = append(containers, pod.Spec.Containers...)

	anyRequestOrLimit := false
	guaranteed := len(containers) > 0
	for _, c := range containers {
		for _, res := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
			req, hasReq := c.Resources.Requests[res]
			lim, hasLim := c.Resources.Limits[res]
			if hasReq && !req.IsZero() {
				anyRequestOrLimit = true
			}
			if hasLim && !lim.IsZero() {
				anyRequestOrLimit = true
			}
			if !hasLim || lim.IsZero() {
				guaranteed = false
				continue
			}
			if !hasReq {
				// An omitted request defaults to the limit: still Guaranteed.
				continue
			}
			if req.Cmp(lim) != 0 {
				guaranteed = false
			}
		}
	}
	switch {
	case !anyRequestOrLimit:
		return corev1.PodQOSBestEffort
	case guaranteed:
		return corev1.PodQOSGuaranteed
	default:
		return corev1.PodQOSBurstable
	}
}

// isCritical is true for DaemonSet pods and for pods in a declared critical
// namespace: the things that must not be the first victim.
func isCritical(pod *corev1.Pod, critNS map[string]bool) bool {
	if critNS[pod.Namespace] {
		return true
	}
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

func (r *Report) add(f Finding) { r.Findings = append(r.Findings, f) }

func (r *Report) recount() {
	r.Counts = map[string]int{}
	for _, f := range r.Findings {
		r.Counts[f.Rule]++
	}
	sort.SliceStable(r.Findings, func(i, j int) bool {
		if ri, rj := ruleRank(r.Findings[i].Rule), ruleRank(r.Findings[j].Rule); ri != rj {
			return ri < rj
		}
		if r.Findings[i].Namespace != r.Findings[j].Namespace {
			return r.Findings[i].Namespace < r.Findings[j].Namespace
		}
		return r.Findings[i].Pod < r.Findings[j].Pod
	})
}

func ruleRank(rule string) int {
	for i, r := range AllRules {
		if r == rule {
			return i
		}
	}
	return len(AllRules)
}

func classOrNone(name string) string {
	if name == "" {
		return "<none>"
	}
	return name
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func ptr(v int32) *int32 { return &v }
