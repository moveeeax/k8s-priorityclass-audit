package audit

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func pc(name string, value int32, globalDefault bool) schedulingv1.PriorityClass {
	return schedulingv1.PriorityClass{
		ObjectMeta:    metav1.ObjectMeta{Name: name},
		Value:         value,
		GlobalDefault: globalDefault,
	}
}

type podOpt func(*corev1.Pod)

func withClass(name string) podOpt { return func(p *corev1.Pod) { p.Spec.PriorityClassName = name } }
func withPriority(v int32) podOpt  { return func(p *corev1.Pod) { p.Spec.Priority = &v } }
func onNode(n string) podOpt       { return func(p *corev1.Pod) { p.Spec.NodeName = n } }
func withQOS(q corev1.PodQOSClass) podOpt {
	return func(p *corev1.Pod) { p.Status.QOSClass = q }
}
func ownedByDaemonSet(name string) podOpt {
	return func(p *corev1.Pod) {
		p.OwnerReferences = append(p.OwnerReferences, metav1.OwnerReference{Kind: "DaemonSet", Name: name})
	}
}
func withResources(req, lim string) podOpt {
	return func(p *corev1.Pod) {
		c := corev1.Container{Name: "app", Resources: corev1.ResourceRequirements{}}
		if req != "" {
			c.Resources.Requests = corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(req),
				corev1.ResourceMemory: resource.MustParse("64Mi"),
			}
		}
		if lim != "" {
			c.Resources.Limits = corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(lim),
				corev1.ResourceMemory: resource.MustParse("64Mi"),
			}
		}
		p.Spec.Containers = append(p.Spec.Containers, c)
	}
}

func pod(ns, name string, opts ...podOpt) corev1.Pod {
	p := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
	for _, o := range opts {
		o(&p)
	}
	return p
}

func findingsFor(rep *Report, rule string) []Finding {
	var out []Finding
	for _, f := range rep.Findings {
		if f.Rule == rule {
			out = append(out, f)
		}
	}
	return out
}

func TestRules(t *testing.T) {
	cases := []struct {
		name  string
		in    Input
		want  map[string]int
		check func(t *testing.T, rep *Report)
	}{
		{
			name: "missing priorityclass is named with its class",
			in: Input{
				PriorityClasses: []schedulingv1.PriorityClass{pc("default-low", 0, true)},
				Pods:            []corev1.Pod{pod("team-a", "api-1", withClass("gone-batch"))},
			},
			want: map[string]int{RuleMissingPriorityClass: 1},
			check: func(t *testing.T, rep *Report) {
				f := findingsFor(rep, RuleMissingPriorityClass)[0]
				if f.Pod != "api-1" || f.Namespace != "team-a" {
					t.Fatalf("finding does not name the pod: %+v", f)
				}
				if f.PriorityClass != "gone-batch" || !strings.Contains(f.Message, "gone-batch") {
					t.Fatalf("finding does not name the class: %+v", f)
				}
				if rep.Ladder[0].Source != "missing-class" {
					t.Fatalf("source = %q, want missing-class", rep.Ladder[0].Source)
				}
			},
		},
		{
			name: "existing class resolves and fires nothing",
			in: Input{
				PriorityClasses: []schedulingv1.PriorityClass{pc("batch", 1000, false), pc("default-low", 0, true)},
				Pods:            []corev1.Pod{pod("team-a", "job-1", withClass("batch"))},
			},
			want: map[string]int{},
			check: func(t *testing.T, rep *Report) {
				if rep.Ladder[0].Priority != 1000 {
					t.Fatalf("priority = %d, want 1000", rep.Ladder[0].Priority)
				}
			},
		},
		{
			name: "two global defaults are reported once, naming both",
			in: Input{
				PriorityClasses: []schedulingv1.PriorityClass{pc("a-default", 100, true), pc("b-default", 200, true)},
				Pods:            []corev1.Pod{pod("team-a", "api-1"), pod("team-a", "api-2")},
			},
			want: map[string]int{RuleMultipleGlobalDefaults: 1},
			check: func(t *testing.T, rep *Report) {
				msg := findingsFor(rep, RuleMultipleGlobalDefaults)[0].Message
				if !strings.Contains(msg, "a-default") || !strings.Contains(msg, "b-default") {
					t.Fatalf("message does not list both: %q", msg)
				}
			},
		},
		{
			name: "no global default is reported once for the cluster, not per pod",
			in: Input{
				PriorityClasses: []schedulingv1.PriorityClass{pc("batch", 1000, false)},
				Pods:            []corev1.Pod{pod("a", "p1"), pod("b", "p2"), pod("c", "p3")},
			},
			want: map[string]int{RuleNoGlobalDefault: 1},
			check: func(t *testing.T, rep *Report) {
				for _, p := range rep.Ladder {
					if p.Priority != 0 || p.Source != "none" {
						t.Fatalf("pod %s/%s = %d/%s, want 0/none", p.Namespace, p.Pod, p.Priority, p.Source)
					}
				}
			},
		},
		{
			name: "global default applies to pods without a class",
			in: Input{
				PriorityClasses: []schedulingv1.PriorityClass{pc("baseline", 500, true)},
				Pods:            []corev1.Pod{pod("team-a", "api-1")},
			},
			want: map[string]int{},
			check: func(t *testing.T, rep *Report) {
				if rep.Ladder[0].Priority != 500 || rep.Ladder[0].Source != "global-default" {
					t.Fatalf("got %d/%s, want 500/global-default", rep.Ladder[0].Priority, rep.Ladder[0].Source)
				}
				if rep.GlobalDefault != "baseline" {
					t.Fatalf("GlobalDefault = %q", rep.GlobalDefault)
				}
			},
		},
		{
			name: "daemonset pod at 0 under a batch job at 1000 is unprotected",
			in: Input{
				PriorityClasses: []schedulingv1.PriorityClass{pc("batch", 1000, false)},
				Pods: []corev1.Pod{
					pod("kube-system", "cni-agent-xyz", ownedByDaemonSet("cni-agent")),
					pod("team-a", "big-batch-1", withClass("batch")),
				},
				CriticalNamespaces: nil,
			},
			want: map[string]int{RuleNoGlobalDefault: 1, RuleUnprotectedCritical: 1},
			check: func(t *testing.T, rep *Report) {
				msg := findingsFor(rep, RuleUnprotectedCritical)[0].Message
				if !strings.Contains(msg, "cni-agent-xyz") || !strings.Contains(msg, "big-batch-1") {
					t.Fatalf("message must name both sides: %q", msg)
				}
			},
		},
		{
			name: "critical namespace pod is protected once it outranks the batch job",
			in: Input{
				PriorityClasses: []schedulingv1.PriorityClass{pc("batch", 1000, false), pc("platform", 2000, false), pc("base", 0, true)},
				Pods: []corev1.Pod{
					pod("ingress-nginx", "controller-1", withClass("platform")),
					pod("team-a", "big-batch-1", withClass("batch")),
				},
				CriticalNamespaces: []string{"ingress-nginx"},
			},
			want: map[string]int{},
		},
		{
			name: "critical namespace pod below the batch job is unprotected",
			in: Input{
				PriorityClasses: []schedulingv1.PriorityClass{pc("batch", 1000, false), pc("base", 0, true)},
				Pods: []corev1.Pod{
					pod("ingress-nginx", "controller-1", withClass("base")),
					pod("team-a", "big-batch-1", withClass("batch")),
				},
				CriticalNamespaces: []string{"ingress-nginx"},
			},
			want: map[string]int{RuleUnprotectedCritical: 1},
		},
		{
			name: "a flat priority-0 cluster is not an unprotected-critical finding",
			in: Input{
				PriorityClasses: []schedulingv1.PriorityClass{pc("base", 0, true)},
				Pods: []corev1.Pod{
					pod("kube-system", "cni-agent-xyz", ownedByDaemonSet("cni-agent")),
					pod("team-a", "web-1"),
				},
			},
			want: map[string]int{},
		},
		{
			name: "system-critical workloads do not count as the preempting workload",
			in: Input{
				PriorityClasses: []schedulingv1.PriorityClass{pc("base", 0, true)},
				Pods: []corev1.Pod{
					pod("kube-system", "cni-agent-xyz", ownedByDaemonSet("cni-agent")),
					pod("kube-system", "kube-apiserver-1", withClass("system-node-critical")),
				},
			},
			want: map[string]int{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := Run(tc.in)
			for _, rule := range AllRules {
				if got, want := rep.Counts[rule], tc.want[rule]; got != want {
					t.Errorf("rule %s fired %d times, want %d (findings: %+v)", rule, got, want, rep.Findings)
				}
			}
			if tc.check != nil {
				tc.check(t, rep)
			}
		})
	}
}

func TestSystemClassResolvesWithoutBeingListed(t *testing.T) {
	rep := Run(Input{
		PriorityClasses: []schedulingv1.PriorityClass{pc("base", 0, true)},
		Pods:            []corev1.Pod{pod("kube-system", "kubelet-helper", withClass("system-node-critical"))},
	})
	if n := rep.Counts[RuleMissingPriorityClass]; n != 0 {
		t.Fatalf("built-in class flagged as missing: %+v", rep.Findings)
	}
	if got := rep.Ladder[0].Priority; got != 2000001000 {
		t.Fatalf("priority = %d, want 2000001000", got)
	}
}

func TestEvictionOrderIsPriorityThenQOS(t *testing.T) {
	rep := Run(Input{
		PriorityClasses: []schedulingv1.PriorityClass{pc("base", 0, true), pc("batch", 1000, false)},
		Nodes:           []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}}},
		Pods: []corev1.Pod{
			pod("a", "guaranteed-0", onNode("node-1"), withQOS(corev1.PodQOSGuaranteed)),
			pod("a", "besteffort-0", onNode("node-1"), withQOS(corev1.PodQOSBestEffort)),
			pod("a", "burstable-0", onNode("node-1"), withQOS(corev1.PodQOSBurstable)),
			pod("a", "batch-1000", onNode("node-1"), withClass("batch"), withQOS(corev1.PodQOSBestEffort)),
		},
	})
	if len(rep.Eviction) != 1 || rep.Eviction[0].Node != "node-1" {
		t.Fatalf("eviction = %+v", rep.Eviction)
	}
	var got []string
	for _, p := range rep.Eviction[0].Pods {
		got = append(got, p.Pod)
	}
	want := []string{"besteffort-0", "burstable-0", "guaranteed-0", "batch-1000"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestEvictionSkipsUnscheduledPods(t *testing.T) {
	rep := Run(Input{
		PriorityClasses: []schedulingv1.PriorityClass{pc("base", 0, true)},
		Pods:            []corev1.Pod{pod("a", "pending-1")},
	})
	if len(rep.Eviction) != 0 {
		t.Fatalf("unscheduled pod appeared in eviction order: %+v", rep.Eviction)
	}
}

func TestDeriveQOS(t *testing.T) {
	cases := []struct {
		name string
		pod  corev1.Pod
		want corev1.PodQOSClass
	}{
		{"no resources", pod("a", "p"), corev1.PodQOSBestEffort},
		{"requests only", pod("a", "p", withResources("100m", "")), corev1.PodQOSBurstable},
		{"requests below limits", pod("a", "p", withResources("100m", "200m")), corev1.PodQOSBurstable},
		{"requests equal limits", pod("a", "p", withResources("200m", "200m")), corev1.PodQOSGuaranteed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deriveQOS(&tc.pod); got != tc.want {
				t.Fatalf("deriveQOS = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStatusQOSWins(t *testing.T) {
	p := pod("a", "p", withResources("200m", "200m"), withQOS(corev1.PodQOSBurstable))
	if got := qosOf(&p); got != corev1.PodQOSBurstable {
		t.Fatalf("qosOf = %q, want the kubelet's Burstable", got)
	}
}

func TestLadderIsDescendingAndStable(t *testing.T) {
	rep := Run(Input{
		PriorityClasses: []schedulingv1.PriorityClass{pc("base", 0, true), pc("mid", 100, false), pc("high", 900, false)},
		Pods: []corev1.Pod{
			pod("b", "p", withClass("mid")),
			pod("a", "p", withClass("mid")),
			pod("z", "p", withClass("high")),
			pod("a", "q"),
		},
	})
	var got []string
	for _, p := range rep.Ladder {
		got = append(got, p.Namespace+"/"+p.Pod)
	}
	want := "z/p,a/p,b/p,a/q"
	if strings.Join(got, ",") != want {
		t.Fatalf("ladder = %v, want %s", got, want)
	}
}

func TestRulesFired(t *testing.T) {
	rep := Run(Input{
		Pods: []corev1.Pod{pod("a", "p", withClass("nope"))},
	})
	fired := rep.RulesFired()
	want := []string{RuleMissingPriorityClass, RuleNoGlobalDefault}
	if strings.Join(fired, ",") != strings.Join(want, ",") {
		t.Fatalf("RulesFired = %v, want %v", fired, want)
	}
}

func TestWriteJSONRoundTrips(t *testing.T) {
	rep := Run(Input{
		PriorityClasses: []schedulingv1.PriorityClass{pc("batch", 1000, false)},
		Nodes:           []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}}},
		Pods:            []corev1.Pod{pod("a", "p", withClass("batch"), onNode("node-1"))},
	})
	var buf bytes.Buffer
	if err := WriteJSON(&buf, rep); err != nil {
		t.Fatal(err)
	}
	var back Report
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if len(back.Findings) != len(rep.Findings) || len(back.Eviction) != 1 {
		t.Fatalf("round trip lost data: %+v", back)
	}
}

func TestWriteTextMentionsEveryFinding(t *testing.T) {
	rep := Run(Input{
		PriorityClasses: []schedulingv1.PriorityClass{pc("batch", 1000, false)},
		Pods: []corev1.Pod{
			pod("a", "broken", withClass("ghost")),
			pod("kube-system", "ds-1", ownedByDaemonSet("ds")),
			pod("a", "batch-1", withClass("batch")),
		},
	})
	var buf bytes.Buffer
	if err := WriteText(&buf, rep, 0); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"PRIORITY CLASSES", "PRIORITY LADDER", "PREEMPTION ORDER", "FINDINGS", "ghost", "ds-1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("text output missing %q:\n%s", want, out)
		}
	}
}

func TestWriteTextTruncatesLadder(t *testing.T) {
	in := Input{PriorityClasses: []schedulingv1.PriorityClass{pc("base", 0, true)}}
	for i := 0; i < 10; i++ {
		in.Pods = append(in.Pods, pod("a", string(rune('a'+i))))
	}
	var buf bytes.Buffer
	if err := WriteText(&buf, Run(in), 3); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "7 more") {
		t.Fatalf("expected a truncation note:\n%s", buf.String())
	}
}
