//go:build integrationtest
// +build integrationtest

package integrationtest

import (
	"testing"
	"time"

	"github.com/portworx/sched-ops/k8s/apps"
	"github.com/portworx/sched-ops/k8s/core"
	"github.com/portworx/sched-ops/k8s/storage"
	"github.com/portworx/torpedo/drivers/scheduler"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	apps_api "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	storage_api "k8s.io/api/storage/v1"
)

const (
	annotationStorageProvisioner = "volume.beta.kubernetes.io/storage-provisioner"
)

func TestExtender(t *testing.T) {
	err := setSourceKubeConfig()
	require.NoError(t, err, "failed to set kubeconfig to source cluster: %v", err)

	t.Run("pvcOwnershipTest", pvcOwnershipTest)
	t.Run("noPVCTest", noPVCTest)
	t.Run("singlePVCTest", singlePVCTest)
	t.Run("statefulsetTest", statefulsetTest)
	t.Run("multiplePVCTest", multiplePVCTest)
	t.Run("driverNodeErrorTest", driverNodeErrorTest)

	err = setRemoteConfig("")
	require.NoError(t, err, "setting kubeconfig to default failed")
}

func noPVCTest(t *testing.T) {
	ctxs, err := schedulerDriver.Schedule(generateInstanceID(t, "nopvctest"),
		scheduler.ScheduleOptions{AppKeys: []string{"mysql-nopvc"}})
	require.NoError(t, err, "Error scheduling task")
	require.Equal(t, 1, len(ctxs), "Only one task should have started")

	err = schedulerDriver.WaitForRunning(ctxs[0], defaultWaitTimeout, defaultWaitInterval)
	require.NoError(t, err, "Error waiting for pod to get to running state")

	destroyAndWait(t, ctxs)
}

func singlePVCTest(t *testing.T) {
	ctxs, err := schedulerDriver.Schedule(generateInstanceID(t, "singlepvctest"),
		scheduler.ScheduleOptions{AppKeys: []string{"mysql-1-pvc"}})
	require.NoError(t, err, "Error scheduling task")
	require.Equal(t, 1, len(ctxs), "Only one task should have started")

	err = schedulerDriver.WaitForRunning(ctxs[0], defaultWaitTimeout, defaultWaitInterval)
	require.NoError(t, err, "Error waiting for pod to get to running state")

	scheduledNodes, err := schedulerDriver.GetNodesForApp(ctxs[0])
	require.NoError(t, err, "Error getting node for app")
	require.Equal(t, 1, len(scheduledNodes), "App should be scheduled on one node")

	volumeNames := getVolumeNames(t, ctxs[0])
	require.Equal(t, 1, len(volumeNames), "Should only have one volume")

	verifyScheduledNode(t, scheduledNodes[0], volumeNames)

	destroyAndWait(t, ctxs)
}

func statefulsetTest(t *testing.T) {
	ctxs, err := schedulerDriver.Schedule(generateInstanceID(t, "sstest"),
		scheduler.ScheduleOptions{AppKeys: []string{"elasticsearch"}})
	require.NoError(t, err, "Error scheduling task")
	require.Equal(t, 1, len(ctxs), "Only one task should have started")

	err = schedulerDriver.WaitForRunning(ctxs[0], defaultWaitTimeout, defaultWaitInterval)
	require.NoError(t, err, "Error waiting for elasticsearch statefulset to get to running state")

	scheduledNodes, err := schedulerDriver.GetNodesForApp(ctxs[0])
	require.NoError(t, err, "Error getting node for app")
	// TODO: There is no qurantee that all pods will be scheduled on
	// different nodes, we should restrict pvc to repl1 and one px node
	// use preferLocalNode flag to ensure pod is getting scheduled on node
	// where replicas exist
	// require.Equal(t, 3, len(scheduledNodes), "App should be scheduled on one node")
	logrus.Infof("sts pod scheduled on %d nodes", len(scheduledNodes))
	// TODO: torpedo doesn't return correct volumes here
	volumeNames := getVolumeNames(t, ctxs[0])
	require.Equal(t, 3, len(volumeNames), "Should have 3 volumes")

	// TODO: Add verification for node where it was scheduled
	// torpedo doesn't return the pod->pvc mapping, so we can't validate that it
	// got scheduled on a prioritized node
	verifyScheduledNode(t, scheduledNodes[0], volumeNames)

	destroyAndWait(t, ctxs)
}

func multiplePVCTest(t *testing.T) {
	ctxs, err := schedulerDriver.Schedule(generateInstanceID(t, "multipvctest"),
		scheduler.ScheduleOptions{AppKeys: []string{"mysql-2-pvc"}})
	require.NoError(t, err, "Error scheduling task")
	require.Equal(t, 1, len(ctxs), "Only one task should have started")

	err = schedulerDriver.WaitForRunning(ctxs[0], defaultWaitTimeout, defaultWaitInterval)
	require.NoError(t, err, "Error waiting for pod to get to running state")

	scheduledNodes, err := schedulerDriver.GetNodesForApp(ctxs[0])
	require.NoError(t, err, "Error getting node for app")
	require.Equal(t, 1, len(scheduledNodes), "App should be scheduled on one node")

	volumeNames := getVolumeNames(t, ctxs[0])
	require.Equal(t, 2, len(volumeNames), "Should have two volumes")

	verifyScheduledNode(t, scheduledNodes[0], volumeNames)
	destroyAndWait(t, ctxs)
}

func driverNodeErrorTest(t *testing.T) {
	ctxs, err := schedulerDriver.Schedule(generateInstanceID(t, "drivererrtest"),
		scheduler.ScheduleOptions{AppKeys: []string{"mysql-1-pvc"}})
	require.NoError(t, err, "Error scheduling task")
	require.Equal(t, 1, len(ctxs), "Only one task should have started")

	err = schedulerDriver.WaitForRunning(ctxs[0], defaultWaitTimeout, defaultWaitInterval)
	require.NoError(t, err, "Error waiting for pod to get to running state")

	scheduledNodes, err := schedulerDriver.GetNodesForApp(ctxs[0])
	require.NoError(t, err, "Error getting node for app")
	require.Equal(t, 1, len(scheduledNodes), "App should be scheduled on one node")

	volumeNames := getVolumeNames(t, ctxs[0])
	require.Equal(t, 1, len(volumeNames), "Should have only one volume")

	verifyScheduledNode(t, scheduledNodes[0], volumeNames)

	time.Sleep(1 * time.Minute)

	err = volumeDriver.StopDriver(scheduledNodes, false, nil)
	require.NoError(t, err, "Error stopping driver on scheduled Node %+v", scheduledNodes[0])
	stoppedNode := scheduledNodes[0]
	// node timeout bumped to 4 mins from stork 2.9.0
	// ref: https://github.com/libopenstorage/stork/pull/1028
	time.Sleep(5 * time.Minute)

	err = schedulerDriver.WaitForRunning(ctxs[0], defaultWaitTimeout, defaultWaitInterval)
	require.NoError(t, err, "Error waiting for pod to get to running state after deletion")

	scheduledNodes, err = schedulerDriver.GetNodesForApp(ctxs[0])
	require.NoError(t, err, "Error getting node for app")
	require.Equal(t, 1, len(scheduledNodes), "App should be scheduled on one node")
	require.NotEqual(t, stoppedNode.Name, scheduledNodes[0].Name, "Task restarted on stopped node")

	volumeNames = getVolumeNames(t, ctxs[0])
	require.Equal(t, 1, len(volumeNames), "Should have only one volume")

	verifyScheduledNode(t, scheduledNodes[0], volumeNames)

	err = volumeDriver.StartDriver(stoppedNode)
	require.NoError(t, err, "Error starting driver on Node %+v", scheduledNodes[0])

	err = volumeDriver.WaitDriverUpOnNode(stoppedNode, defaultWaitTimeout)
	require.NoError(t, err, "Error waiting for Node to start %+v", scheduledNodes[0])

	destroyAndWait(t, ctxs)
}

func pvcOwnershipTest(t *testing.T) {
	ctxs, err := schedulerDriver.Schedule(generateInstanceID(t, "ownershiptest"),
		scheduler.ScheduleOptions{AppKeys: []string{"mysql-repl-1"}})
	require.NoError(t, err, "Error scheduling task")
	require.Equal(t, 1, len(ctxs), "Only one task should have started")

	err = schedulerDriver.WaitForRunning(ctxs[0], defaultWaitTimeout, defaultWaitInterval)
	require.NoError(t, err, "Error waiting for pod to get to running state")

	scheduledNodes, err := schedulerDriver.GetNodesForApp(ctxs[0])
	require.NoError(t, err, "Error getting node for app")
	require.Equal(t, 1, len(scheduledNodes), "App should be scheduled on one node")

	volumeNames := getVolumeNames(t, ctxs[0])
	require.Equal(t, 1, len(volumeNames), "Should have only one volume")

	verifyScheduledNode(t, scheduledNodes[0], volumeNames)

	for _, spec := range ctxs[0].App.SpecList {
		if obj, ok := spec.(*storage_api.StorageClass); ok {
			err := storage.Instance().DeleteStorageClass(obj.Name)
			require.NoError(t, err, "Error deleting storage class for mysql.")
		}
		if obj, ok := spec.(*v1.PersistentVolumeClaim); ok {
			updatePVC, err := core.Instance().GetPersistentVolumeClaim(obj.Name, obj.Namespace)
			require.NoError(t, err, "Error getting persistent volume claim.")
			delete(updatePVC.Annotations, annotationStorageProvisioner)
			_, err = core.Instance().UpdatePersistentVolumeClaim(updatePVC)
			require.NoError(t, err, "Error updating annotations in PVC.")
		}
	}

	err = volumeDriver.StopDriver(scheduledNodes, false, nil)
	require.NoError(t, err, "Error stopping driver on scheduled Node %+v", scheduledNodes[0])
	// make sure to start driver if test failed
	defer func() {
		err = volumeDriver.StartDriver(scheduledNodes[0])
		require.NoError(t, err, "Error starting driver on scheduled Node %+v", scheduledNodes[0])
	}()
	// node timeout bumped to 4.5 mins from stork 2.9.0
	// ref: https://github.com/libopenstorage/stork/pull/1028
	// volumeDriver.StopDriver waits for 10 seconds for driver
	// to go down gracefully
	// lets wait for at least 2.5 mins for PX to go down
	time.Sleep(7 * time.Minute)

	var errUnscheduledPod bool
	for _, spec := range ctxs[0].App.SpecList {
		if obj, ok := spec.(*apps_api.Deployment); ok {
			if obj.Name == "mysql" {
				depPods, err := apps.Instance().GetDeploymentPods(obj)
				require.NoError(t, err, "Error getting pods for deployment ,mysql.")
				for _, pod := range depPods {
					for _, cond := range pod.Status.Conditions {
						if cond.Type == v1.PodScheduled && cond.Status == v1.ConditionFalse {
							errUnscheduledPod = true
						}
					}
				}
			}
		}
	}
	require.Equal(t, true, errUnscheduledPod, "Pod should not have been schedule.")

	err = volumeDriver.StartDriver(scheduledNodes[0])
	require.NoError(t, err, "Error starting driver on scheduled Node %+v", scheduledNodes[0])

	err = volumeDriver.WaitDriverUpOnNode(scheduledNodes[0], defaultWaitTimeout)
	require.NoError(t, err, "Volume driver is not up on Node %+v", scheduledNodes[0])

	destroyAndWait(t, ctxs)
}
