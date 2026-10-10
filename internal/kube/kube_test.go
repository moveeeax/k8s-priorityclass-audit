package kube

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestCollectWithFakeClientset(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api-1", Namespace: "team-a"},
			Spec: corev1.PodSpec{PriorityClassName: "batch", NodeName: "node-1"}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api-2", Namespace: "team-b"}},
		&schedulingv1.PriorityClass{ObjectMeta: metav1.ObjectMeta{Name: "batch"}, Value: 1000},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}},
	)

	in, err := NewWith(cs).Collect(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Pods) != 2 || len(in.PriorityClasses) != 1 || len(in.Nodes) != 1 {
		t.Fatalf("collected %d pods, %d classes, %d nodes", len(in.Pods), len(in.PriorityClasses), len(in.Nodes))
	}
}

func TestCollectNamespaceScoped(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api-1", Namespace: "team-a"}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api-2", Namespace: "team-b"}},
	)
	in, err := NewWith(cs).Collect(context.Background(), "team-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Pods) != 1 || in.Pods[0].Namespace != "team-a" {
		t.Fatalf("pods = %+v", in.Pods)
	}
}

func TestNewRejectsMissingKubeconfig(t *testing.T) {
	if _, err := New("/nonexistent/kubeconfig", ""); err == nil {
		t.Fatal("expected an error for a missing kubeconfig")
	}
}
