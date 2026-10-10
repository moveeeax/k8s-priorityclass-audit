# k8s-priorityclass-audit

[![ci](https://github.com/moveeeax/k8s-priorityclass-audit/actions/workflows/ci.yml/badge.svg)](https://github.com/moveeeax/k8s-priorityclass-audit/actions/workflows/ci.yml)
[![go](https://img.shields.io/badge/go-1.22%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

`kubectl get priorityclass` tells you which classes exist. It does not tell you who is
exposed. This does: it resolves every pod's effective priority, prints the ladder, and
names the pods the scheduler is allowed to evict first.

Three failures it catches:

1. A pod pointing at a `PriorityClass` that no longer exists. It keeps running, and dies
   permanently the next time it is rescheduled, because admission rejects it.
2. No `globalDefault` in the cluster (everything silently lands on priority 0), or more
   than one, in which case which default applies is undefined.
3. A DaemonSet or a pod in a namespace you declared critical sitting at or below some
   team's batch job — your CNI agent, your log shipper, your ingress controller.

## Install

```sh
go install github.com/moveeeax/k8s-priorityclass-audit/cmd/k8s-priorityclass-audit@latest
```

Or build from source: `go build -o k8s-priorityclass-audit ./cmd/k8s-priorityclass-audit`.

## Usage

Against the current context (read-only: `get`/`list` on pods, priorityclasses, nodes):

```sh
k8s-priorityclass-audit --critical-ns kube-system,ingress-nginx
```

Against a file, no cluster needed — this is also how the test fixtures run:

```console
$ k8s-priorityclass-audit --from-file examples/exposed-cluster.yaml \
    --critical-ns kube-system,ingress-nginx
PRIORITY CLASSES
NAME      VALUE    GLOBAL-DEFAULT  PODS  PREEMPTION
ml-batch  1000000  no              1     PreemptLowerPriority

PRIORITY LADDER (5 pods)
PRIORITY  NAMESPACE/POD                                       CLASS          QOS         CRITICAL  SOURCE
1000000   ml/trainer-0                                        ml-batch       Guaranteed  no        class
500       shop/checkout-api-5d9c7b-2kk8l                      platform-tier  Burstable   no        missing-class
0         ingress-nginx/ingress-nginx-controller-6b4c9-wm2dr  <none>         Burstable   yes       none
0         kube-system/cilium-agent-7fxk2                      <none>         Burstable   yes       none
0         kube-system/fluent-bit-99zqd                        <none>         BestEffort  yes       none

PREEMPTION ORDER (first victim first)
node-1  kube-system/cilium-agent-7fxk2(0,Bu) -> ml/trainer-0(1000000,G)
node-2  kube-system/fluent-bit-99zqd(0,BE) -> ingress-nginx/ingress-nginx-controller-6b4c9-wm2dr(0,Bu) -> shop/checkout-api-5d9c7b-2kk8l(500,Bu)

FINDINGS (5)
[HIGH] missing-priorityclass  pod shop/checkout-api-5d9c7b-2kk8l references PriorityClass "platform-tier" which does not exist; it will be rejected at admission on the next reschedule
[MEDIUM] no-global-default    no PriorityClass has globalDefault=true; every pod without an explicit class lands on priority 0
[HIGH] unprotected-critical   critical pod ingress-nginx/ingress-nginx-controller-6b4c9-wm2dr runs at priority 0, at or below workload ml/trainer-0 at priority 1000000 (class ml-batch), which may preempt it
[HIGH] unprotected-critical   critical pod kube-system/cilium-agent-7fxk2 runs at priority 0, at or below workload ml/trainer-0 at priority 1000000 (class ml-batch), which may preempt it
[HIGH] unprotected-critical   critical pod kube-system/fluent-bit-99zqd runs at priority 0, at or below workload ml/trainer-0 at priority 1000000 (class ml-batch), which may preempt it

summary  missing-priorityclass=1 no-global-default=1 unprotected-critical=3
```

JSON for anything downstream:

```sh
k8s-priorityclass-audit --format json | jq '.findings[] | select(.rule=="missing-priorityclass")'
```

In CI, pick the rules that should break the build:

```sh
k8s-priorityclass-audit --critical-ns kube-system,ingress-nginx \
  --fail-on unprotected-critical,missing-priorityclass
```

Exit codes: `0` clean (or nothing in `--fail-on` matched), `1` a selected rule fired,
`2` the tool itself failed (bad flag, no kubeconfig, unreadable fixture). An unknown rule
name in `--fail-on` is an error, not a silent pass.

## Rules

| rule | severity | fires when |
| --- | --- | --- |
| `missing-priorityclass` | high | a pod's `spec.priorityClassName` is not a class in the cluster |
| `no-global-default` | medium | no class has `globalDefault: true` (once per cluster) |
| `multiple-global-defaults` | high | more than one class has `globalDefault: true` (once per cluster) |
| `unprotected-critical` | high | a critical pod is at or below the highest ordinary workload |

Critical means: owned by a DaemonSet, or in a namespace passed to `--critical-ns`
(default `kube-system`). Ordinary means everything else that is not on a `system-*`
class — the control plane's own classes never count as the aggressor.

## How it works

Priority resolution follows the scheduler: `spec.priority` if the API server already
stamped one, else the named class's value, else the `globalDefault` value, else `0`. The
`SOURCE` column says which path was taken, so `missing-class` with a non-zero priority
means the pod was admitted and the class was deleted afterwards.

Eviction order per node is ascending priority, tie-broken by QoS: `BestEffort`, then
`Burstable`, then `Guaranteed` — the order the kubelet and the preemption logic pick
victims in. QoS comes from `status.qosClass` when the kubelet set it, and is derived from
requests and limits otherwise.

All of that lives in `internal/audit`, a package that takes `[]corev1.Pod` plus
`[]schedulingv1.PriorityClass` plus `[]corev1.Node` and returns findings. `internal/kube`
is the only code that talks to an API server, which is why the rules are tested against
fake objects and `go test ./...` needs no cluster.

## Flags

```
--kubeconfig    path to kubeconfig (default: KUBECONFIG, then ~/.kube/config)
--context       kubeconfig context (default: current)
--namespace     limit pods to one namespace (default: all)
--from-file     read Pods/PriorityClasses/Nodes from YAML instead of a cluster
--format        text | json (default text)
--fail-on       comma-separated rules, or "any", that make the exit code 1
--critical-ns   namespaces whose pods must outrank ordinary workloads (default kube-system)
--max-ladder    ladder rows in text mode, 0 for all (default 25)
--timeout       API request timeout (default 30s)
--version       print version
```

## RBAC

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: priorityclass-audit
rules:
  - apiGroups: [""]
    resources: ["pods", "nodes"]
    verbs: ["get", "list"]
  - apiGroups: ["scheduling.k8s.io"]
    resources: ["priorityclasses"]
    verbs: ["get", "list"]
```

## License

MIT
