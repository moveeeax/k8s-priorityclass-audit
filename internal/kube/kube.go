// Package kube is the thin adapter between a Kubernetes API server and the
// audit rules. It only lists; nothing here writes.
package kube

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/moveeeax/k8s-priorityclass-audit/internal/audit"
)

// Client wraps the typed clientset so tests can pass a fake one.
type Client struct {
	cs kubernetes.Interface
}

// New builds a client from a kubeconfig path (empty = default rules) and an
// optional context name (empty = current context).
func New(kubeconfig, contextName string) (*Client, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		rules.ExplicitPath = kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{}
	if contextName != "" {
		overrides.CurrentContext = contextName
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build client: %w", err)
	}
	return &Client{cs: cs}, nil
}

// NewWith wraps an existing clientset (used by the tests with a fake).
func NewWith(cs kubernetes.Interface) *Client { return &Client{cs: cs} }

// Collect lists everything the rules need: pods, PriorityClasses, nodes.
func (c *Client) Collect(ctx context.Context, namespace string) (audit.Input, error) {
	var in audit.Input

	pods, err := c.cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return in, fmt.Errorf("list pods: %w", err)
	}
	in.Pods = pods.Items

	classes, err := c.cs.SchedulingV1().PriorityClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return in, fmt.Errorf("list priorityclasses: %w", err)
	}
	in.PriorityClasses = classes.Items

	nodes, err := c.cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		// A namespace-scoped token can still produce a useful report; the
		// per-node section just falls back to the node names on the pods.
		return in, fmt.Errorf("list nodes: %w", err)
	}
	in.Nodes = nodes.Items

	return in, nil
}
