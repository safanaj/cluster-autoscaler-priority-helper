package fetcher

import (
	"sync"
	"time"

	"k8s.io/klog"
)

type DataManager struct {
	fetcher    Fetcher
	name       string
	lastChange time.Time
	checksum   string
	interval   time.Duration
	mu         sync.RWMutex
	stopCh     <-chan struct{}
	changesCh  chan<- struct{}
}

func NewDataManager(fetcher Fetcher, name string, interval time.Duration) *DataManager {
	return &DataManager{fetcher: fetcher, name: name, interval: interval}

	// dm := &DataManager{fetcher: fetcher, name: name, interval: interval, stopCh: stopCh, changesCh: changesCh}
	// if err := dm.fetch(); err != nil {
	// 	klog.Errorf("%s failed: %v", dm.name, err)
	// 	return nil
	// }

	// ticker := time.NewTicker(interval)
	// go func() {
	// 	defer ticker.Stop()
	// 	for {
	// 		select {
	// 		case <-stopCh:
	// 			return
	// 		case <-ticker.C:
	// 			if err := dm.fetch(); err != nil {
	// 				klog.Warningf("ERROR %s fetching data: %s", dm.name, err)
	// 			}
	// 		}
	// 	}
	// }()
	// return dm
}

func (m *DataManager) Start(stopCh <-chan struct{}, changesCh chan<- struct{}) error {
	m.stopCh = stopCh
	m.changesCh = changesCh
	if err := m.fetch(); err != nil {
		klog.Errorf("%s failed: %v", m.name, err)
		return err
	}
	ticker := time.NewTicker(m.interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-m.stopCh:
				klog.V(1).Infof("%s: Stopped internal stop channel", m.name)
				return
			case <-ticker.C:
				if err := m.fetch(); err != nil {
					klog.Warningf("ERROR %s fetching data: %s", m.name, err)
				}
			}
		}
	}()
	return nil
}

func (m *DataManager) fetch() error {
	data, err := m.fetcher.GetData()
	if err != nil {
		return err
	}
	checksum := m.fetcher.GetCheckSum(data)
	if m.checksum == checksum {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	err = m.fetcher.ProcessData(data)
	if err == nil {
		m.checksum = checksum
		m.lastChange = time.Now()
		klog.V(2).Infof("%s data changed at %s, checksum: %s - channel at %p", m.name, m.lastChange.String(), m.checksum, m.changesCh)
		// notify for changes w/o blocking
		select {
		case m.changesCh <- struct{}{}:
			klog.V(2).Infof("%s data changed Channel (%p) notified", m.name, m.changesCh)
		default:
		}
	}
	return err
}

func (m *DataManager) GetLastChanges() time.Time { return m.lastChange }

func (m *DataManager) Lock()    { m.mu.Lock() }
func (m *DataManager) Unlock()  { m.mu.Unlock() }
func (m *DataManager) RLock()   { m.mu.RLock() }
func (m *DataManager) RUnlock() { m.mu.RUnlock() }
