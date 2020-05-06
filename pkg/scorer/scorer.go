package scorer

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"gopkg.in/yaml.v2"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	listers_v1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	componentbaseconfig "k8s.io/component-base/config"

	"github.com/safanaj/cluster-autoscaler-priority-helper/pkg/scorer/config"

	"github.com/safanaj/cluster-autoscaler-priority-helper/pkg/aws"
	"github.com/safanaj/cluster-autoscaler-priority-helper/pkg/nodes"
	"github.com/safanaj/cluster-autoscaler-priority-helper/pkg/spotadvisor"
	"github.com/safanaj/cluster-autoscaler-priority-helper/pkg/utils"

	"k8s.io/klog"
)

type Patch struct {
	Op    string      `json:"op,inline"`
	Path  string      `json:"path,inline"`
	Value interface{} `json:"value"`
}

type Scorer struct {
	ctx               context.Context
	ctxCancel         context.CancelFunc
	internalCtx       context.Context
	internalCtxCancel context.CancelFunc
	mu                sync.Mutex
	lec               componentbaseconfig.LeaderElectionConfiguration

	clientset        clientset.Interface
	factory          informers.SharedInformerFactory
	outConfigMapName string
	cmInformer       cache.SharedIndexInformer
	cmLister         listers_v1.ConfigMapLister
	namespace        string
	refreshInterval  time.Duration

	spotAdvisor       *spotadvisor.SpotAdvisor
	asgDiscoverer     *aws.ASGDiscoverer
	pricer            *aws.Pricer
	nodesDistribution *nodes.NodesDistribution

	spotAdvisorLastChanges       time.Time
	nodesDistribusionLastChanges time.Time
	asgDiscovererLastChanges     time.Time
	pricerLastChanges            time.Time

	lastChange time.Time

	config config.ScorerConfiguration
}

func NewScorer(
	parentCtx context.Context,
	lec componentbaseconfig.LeaderElectionConfiguration,
	clientset clientset.Interface,
	outConfigMapName string,
	namespace string,
	refreshInterval time.Duration,
	spotAdvisor *spotadvisor.SpotAdvisor,
	asgDiscoverer *aws.ASGDiscoverer,
	nodesDistribution *nodes.NodesDistribution,
	pricer *aws.Pricer,
	config config.ScorerConfiguration,
) *Scorer {
	factory := informers.NewSharedInformerFactoryWithOptions(clientset, 0, informers.WithNamespace(namespace))

	ctx, ctxCancel := context.WithCancel(parentCtx)
	return &Scorer{
		lec:               lec,
		ctx:               ctx,
		ctxCancel:         ctxCancel,
		clientset:         clientset,
		factory:           factory,
		outConfigMapName:  outConfigMapName,
		namespace:         namespace,
		cmInformer:        factory.Core().V1().ConfigMaps().Informer(),
		cmLister:          factory.Core().V1().ConfigMaps().Lister(),
		refreshInterval:   refreshInterval,
		spotAdvisor:       spotAdvisor,
		asgDiscoverer:     asgDiscoverer,
		nodesDistribution: nodesDistribution,
		pricer:            pricer,
		config:            config,
	}
}

func (s *Scorer) Run() {
	if s.lec.LeaderElect {
		lock := getLeaderLock(s)

		for {
			select {
			case <-s.ctx.Done():
				klog.Infof("Context canceled, exiting")
				return
			default:
				// start the leader election code loop
				runLeaderElection(s, lock)
			}
		}
	} else {
		for {
			select {
			case <-s.ctx.Done():
				klog.Infof("Context canceled, exiting")
				return
			default:
				err := s.Start()
				if err != nil {
					panic(err.Error())
				}
				<-s.internalCtx.Done()
			}
		}

	}
}

func (s *Scorer) Exit() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctxCancel()
	<-s.internalCtx.Done()
	time.Sleep(3 * time.Second)
}

func (s *Scorer) Stop() {
	if s.internalCtxCancel == nil {
		klog.Errorf("Try to stop Scorer but it was not started!")
		return
	}
	s.internalCtxCancel()
	s.internalCtx = nil
	s.internalCtxCancel = nil
}

func (s *Scorer) Start() error {
	stopCh := s.ctx.Done()
	if stopCh == nil {
		return fmt.Errorf("Context canceled, exiting")
	}

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
	s.internalCtx, s.internalCtxCancel = context.WithCancel(s.ctx)
	internalStopCh := s.internalCtx.Done()
	ticker := time.NewTicker(s.refreshInterval)
	go func() {
		klog.V(2).Infof("Scorer go routine started, changes channel at %p", changesCh)
		defer ticker.Stop()
		defer close(changesCh)
		for {
			select {
			case <-stopCh:
				klog.V(1).Infof("Context canceled, exiting")
				return
			case <-internalStopCh:
				klog.V(1).Infof("Stopping Scorer")
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

	if err := s.spotAdvisor.Start(internalStopCh, changesCh); err != nil {
		return err
	}
	if err := s.nodesDistribution.Start(internalStopCh, changesCh); err != nil {
		return err
	}
	if err := s.pricer.Start(internalStopCh, changesCh); err != nil {
		return err
	}
	if err := s.asgDiscoverer.Start(internalStopCh, changesCh); err != nil {
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

	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.ctx.Done():
		return fmt.Errorf("Context canceled, exiting")
	case <-s.internalCtx.Done():
		return fmt.Errorf("Scorer was stopped, skipping current update")
	default:
	}

	cm, err = s.cmLister.ConfigMaps(s.namespace).Get(s.outConfigMapName)
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
			_, err := s.clientset.CoreV1().ConfigMaps(s.namespace).
				Create(&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: s.namespace,
						Name:      s.outConfigMapName,
					},
					Data: map[string]string{
						"priorities": string(yamlData),
					},
				})
			if err != nil {
				klog.Errorf("Error creating %s/%s config map: %v", s.namespace, s.outConfigMapName, err)
				return err
			}
			s.lastChange = time.Now()
			return nil
		} else {
			klog.Errorf("Error gettting %s/%s config map: %v", s.namespace, s.outConfigMapName, err)
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

	if _, err := s.clientset.CoreV1().ConfigMaps(s.namespace).
		Patch(s.outConfigMapName, types.JSONPatchType, patchBytes); err != nil {
		return err
	}

	s.lastChange = time.Now()
	klog.V(1).Infof("Updated config map at %s", s.lastChange)
	return nil
}

func (s *Scorer) computeScores() map[int][]string {
	var priorities map[int][]string
	if asgNames, err := s.asgDiscoverer.GetASGNames(); err != nil {
		klog.Errorf("Error computing scores: %v", err)
	} else {
		priorities = make(map[int][]string)
		klog.V(2).Infof("computeScores GetASGNames() => %v\n", asgNames)
		for _, asgName := range asgNames {
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

func (s *Scorer) computeScoreForASG(asgName string) (int, error) {
	var iDetails utils.InstanceDetails
	prio := s.config.BasePriority
	klog.V(3).Infof("Scorer compute priority for %s\t initial prio=%d", asgName, prio)
	rDetails, err := s.asgDiscoverer.GetDetailsFor(asgName)
	if err != nil {
		klog.Errorf(err.Error())
		return -1, err
	}
	if rDetails.IsMixedInstanceTypes() {
		mDetails := rDetails.(utils.MixedInstanceTypesDetails)
		iDetails.IsSpot = mDetails.InstanceDetails.IsSpot
		iDetails.AvailabilityZone = mDetails.InstanceDetails.AvailabilityZone
		// figure out which instance type will be bringed up assuming that
		// on the ASG mixed instance type policy has strategy == capacity-optimized
		// and that implies that AWS will choose the instance type with lower spot termination probability
		// This assumption is speculative, be warned
		prob := 10
		for _, it := range mDetails.InstanceTypes {
			itProb := s.spotAdvisor.GetProbabilityFor(mDetails.GetRegion(), "Linux", it)
			if prob > itProb {
				prob = itProb
				iDetails.InstanceType = it
			}
		}
	} else {
		iDetails = rDetails.(utils.InstanceDetails)
	}

	// logic to score an instance type in a zone
	if iDetails.IsSpot {
		prio += s.config.BonusForSpot
		klog.V(3).Infof("Scorer compute priority for %s\t prio+=%d because is spot (prio=%d)", asgName, s.config.BonusForSpot, prio)
	} else {
		prio -= s.config.MalusForOnDemand
		klog.V(3).Infof("Scorer compute priority for %s\t prio-=%d because is ondemand (prio=%d)", asgName, s.config.MalusForOnDemand, prio)
	}

	saving := s.spotAdvisor.GetSavingFor(iDetails.GetRegion(), "Linux", iDetails.InstanceType)
	probability := s.spotAdvisor.GetProbabilityFor(iDetails.GetRegion(), "Linux", iDetails.InstanceType)
	if saving >= 0 && probability >= 0 && iDetails.IsSpot {
		// prio += saving
		prio -= (probability * s.config.MalusForProbability)
		klog.V(3).Infof("Scorer compute priority for %s\t (probability is %d) prio-=%d*%d (prio=%d)",
			asgName, probability, probability, s.config.MalusForProbability, prio)
		count := s.nodesDistribution.GetCountFor(iDetails.InstanceType, iDetails.AvailabilityZone, "spot")
		prio -= (count * s.config.MalusForNodeDistribution)
		klog.V(3).Infof("Scorer compute priority for %s\t (node distribution, same type in same zone) prio-=%d*%d (prio=%d)",
			asgName, count, s.config.MalusForNodeDistribution, prio)
	} else {
		count := s.nodesDistribution.GetCountForAZ(iDetails.AvailabilityZone)
		prio -= (count * s.config.MalusForNodeDistributionAZOnly)
		klog.V(3).Infof("Scorer compute priority for %s\t (node distribution, same zone) prio-=%d*%d (prio=%d)", asgName, count, s.config.MalusForNodeDistributionAZOnly, prio)
	}

	// prefer smaller instances
	// cores := s.spotAdvisor.GetCoresFor(iDetails.InstanceType)
	// ramgb := s.spotAdvisor.GetRamGbFor(iDetails.InstanceType)
	// if cores > 0 && ramgb > 0 {
	// 	prio -= ((cores * 2) + ramgb)
	// }

	if price, found := s.pricer.GetPriceFor(iDetails.InstanceType, iDetails.AvailabilityZone, iDetails.IsSpot); found {
		prio -= int(price * float64(s.config.MalusForPrice))
		klog.V(3).Infof("Scorer compute priority for %s\t (price) prio-=int(%f*%d) (prio=%d)", asgName, price, s.config.MalusForPrice, prio)
	} else {
		klog.Warningf("no price information for %s", asgName)
	}

	if prio < 0 {
		klog.V(3).Infof("Scorer compute priority for %s\t (prio=%d) return zero as lowest priority", asgName, prio)
		return 0, nil
	}
	return prio, nil
}
