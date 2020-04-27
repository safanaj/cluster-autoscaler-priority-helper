package scorer

import (
	"context"
	"fmt"
	"os"

	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/klog"
)

const leaderLeaseName = "cluster-autoscaler-priority-helper-leader-lease"

func getIdentity() string {
	identity := os.Getenv("POD_NAME")
	if identity == "" {
		identity, _ = os.Hostname()
		if identity != "" {
			identity = fmt.Sprintf("%s-%d", identity, os.Getpid())
		}
	}
	if identity == "" {
		identity = fmt.Sprintf("fake-identity-%d", os.Getpid())
	}
	return identity
}

func getLeaderLock(s *Scorer) resourcelock.Interface {
	lock, err := resourcelock.New(
		s.lec.ResourceLock,
		s.namespace,
		leaderLeaseName,
		s.clientset.CoreV1(),
		s.clientset.CoordinationV1(),
		resourcelock.ResourceLockConfig{
			Identity: getIdentity(),
		},
	)
	if err != nil {
		klog.Fatalf("Unable to create leader election lock: %v", err)
	}

	return lock
}

func getLeaderElectionConfig(s *Scorer, lock resourcelock.Interface) leaderelection.LeaderElectionConfig {
	return leaderelection.LeaderElectionConfig{
		Lock: lock,
		// IMPORTANT: you MUST ensure that any code you have that
		// is protected by the lease must terminate **before**
		// you call cancel. Otherwise, you could have a background
		// loop still running and another process could
		// get elected before your background loop finished, violating
		// the stated goal of the lease.
		ReleaseOnCancel: true,
		LeaseDuration:   s.lec.LeaseDuration.Duration,
		RenewDeadline:   s.lec.RenewDeadline.Duration,
		RetryPeriod:     s.lec.RetryPeriod.Duration,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(_ context.Context) {
				// we're notified when we start - this is where you would
				// usually put your code
				err := s.Start()
				if err != nil {
					panic(err.Error())
				}
			},
			OnStoppedLeading: func() {
				// we can do cleanup here
				klog.Infof("leader lost: %s", getIdentity())
				s.Stop()
			},
			OnNewLeader: func(identity string) {
				// we're notified when new leader elected
				if identity == getIdentity() {
					// I just got the lock
					return
				}
				klog.Infof("new leader elected: %s", identity)
			},
		},
	}
}

func runLeaderElection(s *Scorer, lock resourcelock.Interface) {
	klog.V(2).Infof("Starting leader election")
	leaderelection.RunOrDie(s.ctx, getLeaderElectionConfig(s, lock))
}
