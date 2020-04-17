package scorer

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"gopkg.in/yaml.v2"
	//"sigs.k8s.io/yaml"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	listers_v1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/safanaj/cluster-autoscaler-priority-helper/pkg/aws"
	"github.com/safanaj/cluster-autoscaler-priority-helper/pkg/nodes"
	"github.com/safanaj/cluster-autoscaler-priority-helper/pkg/spotadvisor"

	"k8s.io/klog"
)

const (
	watchNamespace = "kube-system"
)

type Patch struct {
	Op    string      `json:"op,inline"`
	Path  string      `json:"path,inline"`
	Value interface{} `json:"value"`
}

type Scorer struct {
	clientset        clientset.Interface
	factory          informers.SharedInformerFactory
	outConfigMapName string
	cmInformer       cache.SharedIndexInformer
	cmLister         listers_v1.ConfigMapLister

	// stopCh          <-chan struct{}
	// changesCh       <-chan struct{}
	refreshInterval time.Duration

	spotAdvisor       *spotadvisor.SpotAdvisor
	asgDiscoverer     *aws.ASGDiscoverer
	pricer            *aws.Pricer
	nodesDistribution *nodes.NodesDistribution

	spotAdvisorLastChanges       time.Time
	nodesDistribusionLastChanges time.Time
	asgDiscovererLastChanges     time.Time
	pricerLastChanges            time.Time

	lastChange time.Time
}

func NewScorer(
	clientset clientset.Interface,
	outConfigMapName string,
	refreshInterval time.Duration,
	// stopCh <-chan struct{},
	// changesCh <-chan struct{},
	spotAdvisor *spotadvisor.SpotAdvisor,
	asgDiscoverer *aws.ASGDiscoverer,
	nodesDistribution *nodes.NodesDistribution,
	pricer *aws.Pricer,
) *Scorer {
	factory := informers.NewSharedInformerFactoryWithOptions(clientset, 0, informers.WithNamespace(watchNamespace))

	scorer := &Scorer{
		clientset:        clientset,
		factory:          factory,
		outConfigMapName: outConfigMapName,
		cmInformer:       factory.Core().V1().ConfigMaps().Informer(),
		cmLister:         factory.Core().V1().ConfigMaps().Lister(),
		// stopCh:            stopCh,
		// changesCh:         changesCh,
		refreshInterval:   refreshInterval,
		spotAdvisor:       spotAdvisor,
		asgDiscoverer:     asgDiscoverer,
		nodesDistribution: nodesDistribution,
		pricer:            pricer,
	}

	// cmEventHandler := cache.FilteringResourceEventHandler{
	// 	FilterFunc: func(obj interface{}) bool {
	// 		if cm, ok := obj.(*corev1.ConfigMap); ok {
	// 			return cm.ObjectMeta.Name == scorer.outConfigMapName
	// 		}
	// 		return false
	// 	},
	// 	Handler: cache.ResourceEventHandlerFuncs{
	// 		UpdateFunc: func(_, obj interface{}) {
	// 			klog.V(3).Infof("Updating config map because it was changed, last update was at %s", scorer.lastChange)
	// 			if err := scorer.updateConfigMap(); err != nil {
	// 				klog.Errorf("Error udating config map because of configmap changes: %v", err)
	// 			}
	// 		},
	// 	},
	// }
	// scorer.cmInformer.AddEventHandler(cmEventHandler)

	// factory.Start(stopCh)
	// for _, ok := range factory.WaitForCacheSync(stopCh) {
	// 	if !ok {
	// 		klog.Errorf("config map informer did not sync")
	// 		return nil
	// 	}
	// }

	// klog.V(3).Infof("Updating config map, last update was at %s", scorer.lastChange)
	// if err := scorer.updateConfigMap(); err != nil {
	// 	klog.Errorf("Error udating config map because of initial setup: %v", err)
	// }

	// ticker := time.NewTicker(refreshInterval)
	// go func() {
	// 	klog.V(2).Infof("Scorer go routine started, changes channel at %p", changesCh)
	// 	defer ticker.Stop()
	// 	for {
	// 		select {
	// 		case <-stopCh:
	// 			return
	// 		case <-changesCh:
	// 			klog.V(3).Infof("Updating config map because of changes, last update was at %s", scorer.lastChange)
	// 			if err := scorer.updateConfigMap(); err != nil {
	// 				klog.Errorf("Error udating config map because of changes: %v", err)
	// 			}
	// 		case <-ticker.C:
	// 			klog.V(3).Infof("Updating config map because of refresh interval, last update was at %s", scorer.lastChange)
	// 			if err := scorer.updateConfigMap(); err != nil {
	// 				klog.Errorf("Error udating config map because of refresh interval: %v", err)
	// 			}
	// 		}
	// 	}
	// }()

	return scorer
}

func (s *Scorer) Start(stopCh <-chan struct{}) error {

	cmEventHandler := cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			if cm, ok := obj.(*corev1.ConfigMap); ok {
				return cm.ObjectMeta.Name == s.outConfigMapName
			}
			return false
		},
		Handler: cache.ResourceEventHandlerFuncs{
			UpdateFunc: func(_, obj interface{}) {
				klog.V(3).Infof("Updating config map because it was changed, last update was at %s", s.lastChange)
				if err := s.updateConfigMap(); err != nil {
					klog.Errorf("Error udating config map because of configmap changes: %v", err)
				}
			},
		},
	}
	s.cmInformer.AddEventHandler(cmEventHandler)
	s.factory.Start(stopCh)
	for _, ok := range s.factory.WaitForCacheSync(stopCh) {
		if !ok {
			return fmt.Errorf("config map informer did not sync")
		}
	}

	changesCh := make(chan struct{})
	ticker := time.NewTicker(s.refreshInterval)
	go func() {
		klog.V(2).Infof("Scorer go routine started, changes channel at %p", changesCh)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-changesCh:
				klog.V(3).Infof("Updating config map because of changes, last update was at %s", s.lastChange)
				if err := s.updateConfigMap(); err != nil {
					klog.Errorf("Error udating config map because of changes: %v", err)
				}
			case <-ticker.C:
				klog.V(3).Infof("Updating config map because of refresh interval, last update was at %s", s.lastChange)
				if err := s.updateConfigMap(); err != nil {
					klog.Errorf("Error udating config map because of refresh interval: %v", err)
				}
			}
		}
	}()

	if err := s.spotAdvisor.Start(stopCh, changesCh); err != nil {
		return err
	}
	if err := s.nodesDistribution.Start(stopCh, changesCh); err != nil {
		return err
	}
	if err := s.pricer.Start(stopCh, changesCh); err != nil {
		return err
	}
	if err := s.asgDiscoverer.Start(stopCh, changesCh); err != nil {
		return err
	}
	return nil
}

func (s *Scorer) updateConfigMap() error {
	var oldChecksum string
	var patchBytes, yamlData []byte
	var err error
	var cm *corev1.ConfigMap

	priorities := s.computeScores()
	if len(priorities) == 0 {
		// return fmt.Errorf("update config map skipped because no data yet to compute priorities")
		klog.Warningf("update config map skipped because no data yet to compute priorities")
		return nil
	}
	if yamlData, err = yaml.Marshal(priorities); err != nil {
		return err
	}
	checksum := fmt.Sprintf("%x", sha256.Sum256(yamlData))

	cm, err = s.cmLister.ConfigMaps(watchNamespace).Get(s.outConfigMapName)
	if err == nil {
		currentPrioritiesStr, ok := cm.Data["priorities"]
		if ok {
			oldChecksum = fmt.Sprintf("%x", sha256.Sum256([]byte(currentPrioritiesStr)))
			klog.V(4).Infof("old data(%s):\n%s", oldChecksum, currentPrioritiesStr)
			klog.V(4).Infof("new data(%s):\n%s", checksum, string(yamlData))
		} else {
			cm.Data["priorities"] = string(yamlData)
		}
	} else {
		statusErr, ok := err.(*errors.StatusError)
		if !ok {
			return err
		}
		if statusErr.Status().Reason == metav1.StatusReasonNotFound {
			_, err := s.clientset.CoreV1().ConfigMaps(watchNamespace).
				Create(&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: watchNamespace,
						Name:      s.outConfigMapName,
					},
					Data: map[string]string{
						"priorities": string(yamlData),
					},
				})
			if err != nil {
				klog.Errorf("Error creating %s/%s config map: %v", watchNamespace, s.outConfigMapName, err)
				return err
			}
			s.lastChange = time.Now()
			return nil
		} else {
			klog.Errorf("Error gettting %s/%s config map: %v", watchNamespace, s.outConfigMapName, err)
			return err
		}
	}

	klog.V(3).Infof("Update config map checking checksums %s == %s : %t", checksum, oldChecksum, oldChecksum == checksum)
	if oldChecksum == checksum {
		klog.V(1).Infof("Update config map skipped because of checksum (%s), last update was at %s", checksum, s.lastChange)
		return nil
	}

	if patchBytes, err = json.Marshal([]Patch{{
		Op:   "replace",
		Path: "/data",
		Value: map[string]string{
			"priorities": string(yamlData),
		},
	}}); err != nil {
		return err
	}

	if _, err := s.clientset.CoreV1().ConfigMaps(watchNamespace).
		Patch(s.outConfigMapName, types.JSONPatchType, patchBytes); err != nil {
		return err
	}

	s.lastChange = time.Now()
	klog.V(1).Infof("Updated config map at %s", s.lastChange)
	return nil
}

func (s *Scorer) computeScores() map[int][]string {
	var priorities map[int][]string
	if asgsData, err := s.asgDiscoverer.GetASGsData(); err != nil {
		klog.Errorf("Error computing scores: %v", err)
	} else {
		priorities = make(map[int][]string)
		for asgName, _ := range asgsData {
			prio, err := s.computeScoreForASG(asgName)
			if err != nil {
				continue
			}
			if asgs, found := priorities[prio]; found {
				priorities[prio] = append(asgs, asgName)
			} else {
				priorities[prio] = append([]string{}, asgName)
			}
		}
	}
	for _, asgs := range priorities {
		sort.Strings(asgs)
	}
	return priorities
}

const (
	malusForOnDemand               = 500
	bonusForSpot                   = 100
	malusForProbability            = 150
	malusForNodeDistribution       = 10
	malusForNodeDistributionAZOnly = 10
	malusForPrice                  = 100
)

func (s *Scorer) computeScoreForASG(asgName string) (int, error) {
	prio := 1000
	klog.V(4).Infof("Scorer compute priority for %s\t initial prio=%d", asgName, prio)
	iDetails, err := s.asgDiscoverer.GetInstanceDetailsFor(asgName)
	if err != nil {
		klog.Errorf(err.Error())
		return -1, err
	}
	// logic to score an instance type in a zone
	if iDetails.IsSpot {
		prio += bonusForSpot
		klog.V(4).Infof("Scorer compute priority for %s\t prio+=%d because is spot (prio=%d)", asgName, bonusForSpot, prio)
	} else {
		prio -= malusForOnDemand
		klog.V(4).Infof("Scorer compute priority for %s\t prio-=%d because is ondemand (prio=%d)", asgName, malusForOnDemand, prio)
	}

	saving := s.spotAdvisor.GetSavingFor(iDetails.GetRegion(), "Linux", iDetails.InstanceType)
	probability := s.spotAdvisor.GetProbabilityFor(iDetails.GetRegion(), "Linux", iDetails.InstanceType)
	if saving >= 0 && probability >= 0 && iDetails.IsSpot {
		// prio += saving
		prio -= (probability * malusForProbability)
		klog.V(4).Infof("Scorer compute priority for %s\t (probability is %d) prio-=%d*%d (prio=%d)",
			asgName, probability, probability, malusForProbability, prio)
		count := s.nodesDistribution.GetCountFor(iDetails.InstanceType, iDetails.AvailabilityZone, "spot")
		prio -= (count * malusForNodeDistribution)
		klog.V(4).Infof("Scorer compute priority for %s\t (node distribution, same type in same zone) prio-=%d*%d (prio=%d)",
			asgName, count, malusForNodeDistribution, prio)
	} else {
		count := s.nodesDistribution.GetCountForAZ(iDetails.AvailabilityZone)
		prio -= (count * malusForNodeDistributionAZOnly)
		klog.V(4).Infof("Scorer compute priority for %s\t (node distribution, same zone) prio-=%d*%d (prio=%d)", asgName, count, malusForNodeDistributionAZOnly, prio)
	}

	// prefer smaller instances
	// cores := s.spotAdvisor.GetCoresFor(iDetails.InstanceType)
	// ramgb := s.spotAdvisor.GetRamGbFor(iDetails.InstanceType)
	// if cores > 0 && ramgb > 0 {
	// 	prio -= ((cores * 2) + ramgb)
	// }

	// TODO consider prices
	if price, found := s.pricer.GetPriceFor(iDetails.InstanceType, iDetails.AvailabilityZone, iDetails.IsSpot); found {
		prio -= int(price * malusForPrice)
		klog.V(4).Infof("Scorer compute priority for %s\t (price) prio-=int(%f*%d) (prio=%d)", asgName, price, malusForPrice, prio)
	} else {
		klog.Warningf("no price information for %s", asgName)
	}

	if prio < 0 {
		klog.V(4).Infof("Scorer compute priority for %s\t (prio=%d) return zero as lowest priority", asgName, prio)
		return 0, nil
	}
	return prio, nil
}
