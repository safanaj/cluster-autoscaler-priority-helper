package fetcher

import "time"

type Fetcher interface {
	GetData() (interface{}, error)
	ProcessData(interface{}) error
	GetLastChanges() time.Time
	GetCheckSum(interface{}) string
}
