package controllers

import (
	"fmt"
	"sync"
	"time"

	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/client"
	snapshotcontroller "github.com/kubernetes-incubator/external-storage/snapshot/pkg/controller/snapshot-controller"
	snapshotvolume "github.com/kubernetes-incubator/external-storage/snapshot/pkg/volume"
	"github.com/libopenstorage/stork/drivers/volume"
	"github.com/libopenstorage/stork/pkg/version"
	schederrors "github.com/portworx/sched-ops/k8s/errors"
	log "github.com/sirupsen/logrus"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	provisionerIDAnn                  = "snapshotProvisionerIdentity"
	defaultSyncDuration time.Duration = 60 * time.Second
	validateCrdInterval time.Duration = 5 * time.Second
	validateCrdTimeout  time.Duration = 1 * time.Minute
)

// Snapshotter Snapshot Controller
type Snapshotter struct {
	Driver  volume.Driver
	lock    sync.Mutex
	started bool
}

// Start Starts the snapshot controller
func (s *Snapshotter) Start(stopChannel <-chan struct{}) error {
	s.lock.Lock()
	defer s.lock.Unlock()

	if s.started {
		return fmt.Errorf("Snapshotter has already been started")
	}
	config, err := rest.InClusterConfig()
	if err != nil {
		return err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}

	if clientset == nil {
		return schederrors.ErrK8SApiAccountNotSet
	}

	aeclientset, err := apiextensionsclient.NewForConfig(config)
	if err != nil {
		return err
	}

	snapshotClient, snapshotScheme, err := client.NewClient(config)
	if err != nil {
		return err
	}

	log.Infof("Registering CRDs")
	ok, err := version.RequiresV1Registration()
	if err != nil {
		return err
	}
	if ok {
		err = client.CreateCRDV1(aeclientset)
		if err != nil {
			return err
		}
	} else {
		err = client.CreateCRD(aeclientset)
		if err != nil {
			return err
		}
	}

	err = client.WaitForSnapshotResource(snapshotClient)
	if err != nil {
		return err
	}

	plugins := make(map[string]snapshotvolume.Plugin)
	plugins[s.Driver.String()] = s.Driver.GetSnapshotPlugin()

	snapController := snapshotcontroller.NewSnapshotController(snapshotClient, snapshotScheme,
		clientset, &plugins, defaultSyncDuration)

	snapController.Run(stopChannel)

	s.started = true
	return nil
}
