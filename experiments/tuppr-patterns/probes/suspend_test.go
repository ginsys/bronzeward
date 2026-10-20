package talosupgrade

import (
	"context"
	"testing"

	tupprv1alpha1 "github.com/home-operations/tuppr/api/v1alpha1"
	"github.com/home-operations/tuppr/internal/constants"
	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Test the proposed use of suspension as a stop mechanism. Even deleting a Job
// would not account for an already dispatched remote request under Bronzeward ER.
func TestBronzewardSuspendMustStopActiveJob(t *testing.T) {
	scheme := newTestScheme()
	tu := newTalosUpgrade("suspended-upgrade", withFinalizer,
		withPhase(tupprv1alpha1.JobPhaseUpgrading),
		withAnnotation(constants.SuspendAnnotation, "Bronzeward owns the scope"))
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name: "already-running", Namespace: testNamespace,
			Labels: map[string]string{
				appLabelKey: talosUpgradeAppName, appInstanceLabelKey: tu.Name,
				appPartOfLabelKey: appPartOfTuppr, targetNodeLabelKey: "node-a",
			},
		},
		Status: batchv1.JobStatus{Active: 1},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tu, job).
		WithStatusSubresource(tu).Build()
	r := newTalosReconciler(cl, scheme, &mockTalosClient{}, &mockHealthChecker{})
	reconcileTalos(t, r, tu.Name)
	var after batchv1.Job
	err := cl.Get(context.Background(), client.ObjectKeyFromObject(job), &after)
	if apierrors.IsNotFound(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if after.DeletionTimestamp == nil && (after.Spec.Suspend == nil || !*after.Spec.Suspend) {
		t.Fatal("BW_JOB_STILL_ENABLED: reconciliation suspension did not stop the existing Job")
	}
}
