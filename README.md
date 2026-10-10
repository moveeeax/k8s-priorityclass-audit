# k8s-priorityclass-audit
Audit Kubernetes pod priority: pods pointing at a PriorityClass that does not exist, a missing or duplicated globalDefault, and critical workloads sitting at priority 0 under a batch job that can preempt them — plus the per-node eviction order under pressure.
