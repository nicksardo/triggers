package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"sync"

	"github.com/iron-io/iron_go3/config"
	"github.com/iron-io/iron_go3/worker"
)

type Config struct {
	Environments map[string]config.Settings `json:"envs"`
	Alerts       []QueueWorkerAlert         `json:"alerts"`
	CacheEnv     string                     `json:"cacheEnv"`
	Runtime      *int                       `json:"runtime,omitempty"`
}

type QueueWorkerAlert struct {
	QueueName  string    `json:"queueName"`
	QueueEnv   string    `json:"queueEnv"`
	WorkerName string    `json:"workerName"`
	WorkerEnv  string    `json:"workerEnv"`
	Cluster    string    `json:"cluster"`
	Priority   int       `json:"priority"`
	Interval   *int      `json:"interval"`
	Triggers   []Trigger `json:"triggers"`
	Min        *int      `json:"min"`
	Max        *int      `json:"max"`
}

type Trigger struct {
	Typ   string `json:"type"`
	Value int    `json:"value"`
}

func getConfig(fileName string) (*Config, error) {
	c := &Config{}
	configData, err := ioutil.ReadFile(fileName)
	if err != nil {
		reader, err := worker.ConfigReader()
		defer reader.Close()
		if err != nil {
			return nil, fmt.Errorf("Could not read config: %v", err)
		}

		configData, err = ioutil.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("Could not read all of config: %v", err)
		}
	}

	if len(configData) == 0 {
		return nil, fmt.Errorf("No config provided")
	}

	err = json.Unmarshal(configData, c)
	if err != nil {
		return nil, fmt.Errorf("Could not unmarshal data %v\n", err)
	}

	return c, nil
}

func (a *QueueWorkerAlert) needPreviousSize() bool {
	for _, t := range a.Triggers {
		if t.Typ == "progressive" {
			return true
		}
	}
	return false
}

func (con *Config) getSettings(product, env string) (*config.Settings, bool) {
	v := config.Presets[product]
	settings := &v

	if productSettings, ok := con.Environments["iron_"+product]; ok {
		settings.UseSettings(&productSettings)
	}

	if envSettings, ok := con.Environments[env]; ok {
		settings.UseSettings(&envSettings)
		return settings, true
	}

	return settings, false
}

type state struct {
	sync.RWMutex
	queueSizes map[string]int
	codeIds    map[string]string
}

func (st *state) setQueueSize(key string, i int) {
	st.Lock()
	defer st.Unlock()

	st.queueSizes[key] = i
}

func (st *state) getQueueSize(key string) (int, bool) {
	st.RLock()
	defer st.RUnlock()

	i, exists := st.queueSizes[key]
	return i, exists
}

func (st *state) setCodeId(name, id string) {
	st.Lock()
	defer st.Unlock()

	st.codeIds[name] = id
}

func (st *state) getCodeId(name string) (string, bool) {
	st.RLock()
	defer st.RUnlock()

	id, exists := st.codeIds[name]
	return id, exists
}
