package nodes

import (
	"fmt"
	// "strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	listers_v1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	// "k8s.io/client-go/tools/clientcmd"

	"k8s.io/klog"

	"github.com/safanaj/cluster-autoscaler-priority-helper/pkg/utils"
)

const (
	zoneLabel         string = "failure-domain.beta.kubernetes.io/zone"
	instanceTypeLabel string = "beta.kubernetes.io/instance-type"
	tenantLabel       string = "kubernetes.io/tenant"
)

func instanceTypeAZKeyFunc(instanceType, az string, isSpot bool) string {
	return (utils.InstanceDetails{instanceType, az, isSpot}).String()
}

func instanceTypeAZKeyFromNode(node *corev1.Node) (string, bool) {
	var az, iType, tenant string
	var isSpot, ok bool
	if az, ok = node.ObjectMeta.Labels[zoneLabel]; !ok {
		return "", false
	}
	if iType, ok = node.ObjectMeta.Labels[instanceTypeLabel]; !ok {
		return "", false
	}
	if tenant, ok = node.ObjectMeta.Labels[tenantLabel]; !ok {
		return "", false
	} else {
		isSpot = (tenant == "spot")
	}
	return instanceTypeAZKeyFunc(iType, az, isSpot), true
}

type nodesData struct {
	instanceTypeAZCount map[string]int
}

type NodesDistribution struct {
	data         nodesData
	cs           clientset.Interface
	factory      informers.SharedInformerFactory
	nodeInformer cache.SharedIndexInformer
	nodeLister   listers_v1.NodeLister
	lastChange   time.Time
}

func NewNodesDistribution(clientset clientset.Interface) (*NodesDistribution, error) {
	factory := informers.NewSharedInformerFactoryWithOptions(clientset, 0)

	nodes := &NodesDistribution{
		data:         nodesData{instanceTypeAZCount: make(map[string]int)},
		cs:           clientset,
		factory:      factory,
		nodeInformer: factory.Core().V1().Nodes().Informer(),
		nodeLister:   factory.Core().V1().Nodes().Lister(),
	}

	return nodes, nil
}

func (n *NodesDistribution) Start(stopCh <-chan struct{}, changesCh chan<- struct{}) error {
	nodeEventHandler := cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			if node, ok := obj.(*corev1.Node); ok {
				return node.ObjectMeta.Labels["kubernetes.io/role"] != "master"
			}
			return false
		},
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				if node, ok := obj.(*corev1.Node); ok {
					if k, ok := instanceTypeAZKeyFromNode(node); ok {
						if _, ok := n.data.instanceTypeAZCount[k]; ok {
							n.data.instanceTypeAZCount[k]++
						} else {
							n.data.instanceTypeAZCount[k] = 1
						}
						n.lastChange = time.Now()
						klog.V(2).Infof("Nodes distribution changed at %s", n.lastChange.String())
						// notify for changes w/o blocking
						select {
						case changesCh <- struct{}{}:
						default:
						}
					}
				}
			},
			DeleteFunc: func(obj interface{}) {
				if node, ok := obj.(*corev1.Node); ok {
					if k, ok := instanceTypeAZKeyFromNode(node); ok {
						if _, ok := n.data.instanceTypeAZCount[k]; ok {
							n.data.instanceTypeAZCount[k]--
						}
						n.lastChange = time.Now()
						klog.V(2).Infof("Nodes distribution changed at %s", n.lastChange.String())
						// notify for changes w/o blocking
						select {
						case changesCh <- struct{}{}:
						default:
						}
					}
				}
			},
		},
	}
	n.nodeInformer.AddEventHandler(nodeEventHandler)
	n.factory.Start(stopCh)
	for _, ok := range n.factory.WaitForCacheSync(stopCh) {
		if !ok {
			return fmt.Errorf("node informer did not sync")
		}
	}
	return nil
}

func (n *NodesDistribution) GetCountFor(args ...string) (count int) {
	argslen := len(args)
	if argslen < 2 {
		return
	}
	instanceType := args[0]
	az := args[1]
	if argslen == 2 || (argslen > 2 && args[2] == "spot") {
		if countSpot, ok := n.data.instanceTypeAZCount[instanceTypeAZKeyFunc(instanceType, az, true)]; ok {
			count += countSpot
		}
	}
	if argslen == 2 || (argslen > 2 && args[2] != "spot") {
		if countOnDemand, ok := n.data.instanceTypeAZCount[instanceTypeAZKeyFunc(instanceType, az, false)]; ok {
			count += countOnDemand
		}
	}
	return
}

func (n *NodesDistribution) GetCountForInstanceType(args ...string) (count int) {
	argslen := len(args)
	if argslen < 1 {
		return
	}
	instanceType := args[0]

	for k, v := range n.data.instanceTypeAZCount {
		iDetails := utils.InstanceDetails{}
		(&iDetails).FromString(k)
		if iDetails.InstanceType == instanceType {
			if argslen > 1 {
				if args[1] == "spot" {
					if iDetails.IsSpot {
						count += v
					}
				} else {
					if !iDetails.IsSpot {
						count += v
					}
				}
			} else {
				count += v
			}
		}

	}
	return
}

func (n *NodesDistribution) GetCountForAZ(az string) (count int) {
	for k, v := range n.data.instanceTypeAZCount {
		iDetails := utils.InstanceDetails{}
		(&iDetails).FromString(k)
		if iDetails.AvailabilityZone == az {
			count += v
		}
	}
	return
}

func (n *NodesDistribution) GetData() map[string]int {
	return n.data.instanceTypeAZCount
}
