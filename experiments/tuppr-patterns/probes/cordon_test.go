package drain

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// This is a Bronzeward compatibility requirement, not an upstream promise.
// It is expected to fail against the pinned, unmodified tuppr implementation.
func TestBronzewardForeignCordonMustSurvive(t *testing.T) {
	ctx := context.Background()
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "already-cordoned"},
		Spec:       corev1.NodeSpec{Unschedulable: true},
	}
	cl := fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(node).Build()
	drainer := NewDrainer(cl)
	if err := drainer.CordonNode(ctx, node.Name); err != nil {
		t.Fatal(err)
	}
	if err := drainer.UncordonNode(ctx, node.Name); err != nil {
		t.Fatal(err)
	}
	var after corev1.Node
	if err := cl.Get(ctx, client.ObjectKey{Name: node.Name}, &after); err != nil {
		t.Fatal(err)
	}
	if !after.Spec.Unschedulable {
		t.Fatal("BW_FOREIGN_CORDON_CLEARED: cleanup removed a pre-existing cordon")
	}
}
