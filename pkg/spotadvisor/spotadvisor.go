package spotadvisor

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	// "sync"
	"time"

	"k8s.io/klog"

	"github.com/safanaj/cluster-autoscaler-priority-helper/pkg/fetcher"
)

const DEFAULT_SPOT_ADVISOR_URL string = "https://spot-bid-advisor.s3.amazonaws.com/spot-advisor-data.json"

type instanceTypeData struct {
	S int `json:"s"`
	R int `json:"r"`
}

type SpotAdvisor struct {
	*fetcher.DataManager

	Data          map[string]map[string]map[string]instanceTypeData `json:"spot_advisor"`
	InstanceTypes map[string]struct {
		Cores  uint    `json:"cores"`
		Ram_gb float64 `json:"ram_gb"`
		Emr    bool    `json:"emr"`
	} `json:"instance_types"`
	Ranges []struct {
		Index int    `json:"index"`
		Dots  int    `json:"dots"`
		Max   int    `json:"max"`
		Label string `json:"label"`
	} `json:"ranges"`
	GlobalRate string `json:"global_rate"`

	spotAdvisorDataURL string
}

func NewSpotAdvisor(refreshInterval time.Duration) (*SpotAdvisor, error) {
	return NewSpotAdvisorWithURL("", refreshInterval)
}

func NewSpotAdvisorWithURL(sadurl string, interval time.Duration) (*SpotAdvisor, error) {
	url := sadurl
	if url == "" {
		url = DEFAULT_SPOT_ADVISOR_URL
	}

	sad := &SpotAdvisor{spotAdvisorDataURL: url}
	sad.DataManager = fetcher.NewDataManager(sad, "SpotAdvisor Fetcher", interval)
	if sad.DataManager == nil {
		return nil, fmt.Errorf("NewDataManager failed")
	}
	return sad, nil
}

func fetchSpotAdvisorData(url string) ([]byte, error) {
	var (
		err  error
		body []byte
		resp *http.Response
	)

	if resp, err = http.Get(url); err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if body, err = ioutil.ReadAll(resp.Body); err != nil {
		return nil, err
	}
	return body, nil
}

func (sad *SpotAdvisor) GetData() (interface{}, error) {
	return fetchSpotAdvisorData(sad.spotAdvisorDataURL)
}

func (sad *SpotAdvisor) ProcessData(data interface{}) error {
	jsonData := data.([]byte)
	return json.Unmarshal(jsonData, sad)
}

func (sad *SpotAdvisor) GetCheckSum(data interface{}) string {
	jsonData := data.([]byte)
	return fmt.Sprintf("%x", sha256.Sum256(jsonData))
}

func (sad *SpotAdvisor) GetLastChanges() time.Time {
	return sad.DataManager.GetLastChanges()
}

func (sad *SpotAdvisor) getDataFor(region string, osType string, iType string) instanceTypeData {
	if sad == nil {
		klog.Warningln("WARN: No data from spot advisor endpoint")
		return instanceTypeData{-1, -1}
	}

	sad.DataManager.RLock()
	defer sad.DataManager.RUnlock()

	if _, ok := sad.Data[region]; !ok {
		return instanceTypeData{-1, -1}
	}

	if _, ok := sad.Data[region][osType]; !ok {
		return instanceTypeData{-1, -1}
	}

	if _, ok := sad.Data[region][osType][iType]; !ok {
		return instanceTypeData{-1, -1}
	}

	return sad.Data[region][osType][iType]
}

func (sad *SpotAdvisor) GetProbabilityFor(region string, osType string, iType string) int {
	return sad.getDataFor(region, osType, iType).R
}

func (sad *SpotAdvisor) GetSavingFor(region string, osType string, iType string) int {
	return sad.getDataFor(region, osType, iType).S
}

func (sad *SpotAdvisor) getInstanceTypeFor(iType string) (struct {
	Cores  uint    `json:"cores"`
	Ram_gb float64 `json:"ram_gb"`
	Emr    bool    `json:"emr"`
}, bool) {
	if sad == nil {
		klog.Warningln("WARN: No data from spot advisor endpoint")
		return struct {
			Cores  uint    `json:"cores"`
			Ram_gb float64 `json:"ram_gb"`
			Emr    bool    `json:"emr"`
		}{}, false
	}

	sad.DataManager.RLock()
	defer sad.DataManager.RUnlock()
	res, ok := sad.InstanceTypes[iType]
	return res, ok
}

func (sad *SpotAdvisor) GetCoresFor(iType string) int {
	if res, ok := sad.getInstanceTypeFor(iType); ok {
		return int(res.Cores)
	}
	return -1
}

func (sad *SpotAdvisor) GetRamGbFor(iType string) int {
	if res, ok := sad.getInstanceTypeFor(iType); ok {
		return int(res.Ram_gb)
	}
	return -1
}
