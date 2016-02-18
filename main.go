package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/iron-io/iron_go3/api"
	"github.com/iron-io/iron_go3/cache"
	"github.com/iron-io/iron_go3/config"
	"github.com/iron-io/iron_go3/mq"
	"github.com/iron-io/iron_go3/worker"
)

var (
	interval = 10 * time.Second
	runtime  = 30 * time.Minute
	swapi    = "worker-aws-us-east-1.iron.io"
)

const (
	TriggerFixed       = "fixed"
	TriggerProgressive = "progressive"
	TriggerRatio       = "ratio"
)

var (
	prev    map[string]int
	codeIds map[string]string
	client  *http.Client
)

type Config struct {
	Environments map[string]config.Settings `json:"envs"`
	Alerts       []QueueWorkerAlert         `json:"alerts"`
	CacheEnv     string                     `json:"cacheEnv"`
	Interval     *int                       `json:"interval,omitempty"`
	Runtime      *int                       `json:"runtime,omitempty"`
}

type QueueWorkerAlert struct {
	QueueName  string    `json:"queueName"`
	QueueEnv   string    `json:"queueEnv"`
	WorkerName string    `json:"workerName"`
	WorkerEnv  string    `json:"workerEnv"`
	Cluster    string    `json:"cluster"`
	Triggers   []Trigger `json:"triggers"`
}

type Trigger struct {
	Typ   string `json:"type"`
	Value int    `json:"value"`
}

func queueKey(qw QueueWorkerAlert) string {
	return qw.QueueEnv + "|" + qw.QueueName
}

func main() {
	start := time.Now()
	prev = make(map[string]int)
	codeIds = make(map[string]string)
	client = api.HttpClient

	// Retrieve configuration
	c := &Config{}
	worker.ParseFlags()
	err := worker.ConfigFromJSON(c)
	if err != nil {
		log.Fatalln("Could not unparse config", err)
	}

	if len(c.Alerts) == 0 || len(c.Environments) == 0 {
		fmt.Println("No config set")
		return
	}

	if c.Interval != nil {
		interval = time.Duration(*c.Interval) * time.Second
	}

	if c.Runtime != nil {
		runtime = time.Duration(*c.Runtime) * time.Second
	}

	cacheEnv, exists := c.Environments[c.CacheEnv]
	if !exists {
		log.Fatalln("No cache environment set")
		return
	}

	cacheConfig := config.ManualConfig("iron_cache", &cacheEnv)
	queueCache := &cache.Cache{Settings: cacheConfig, Name: "autoscale-prevs"}
	for {
		if time.Since(start) > runtime {
			break
		}

		for _, alert := range c.Alerts {
			if len(alert.Triggers) == 0 {
				fmt.Println("No triggers found for alert")
				continue
			}

			queueSize, prevQueueSize := 0, 0
			key := queueKey(alert)

			// Get previous size
			if _, e := prev[key]; !e {
				v, err := queueCache.Get(key)
				if err != nil {
					if !strings.Contains(err.Error(), "not found") {
						// Print errors not associated with cache/key not found errors
						fmt.Println(err)
					}
				} else {
					prev[key] = int(v.(float64))
				}
			}
			prevQueueSize = prev[key]

			queueEnv, exists := c.Environments[alert.QueueEnv]
			if !exists {
				fmt.Printf("Environment %q is not defined for queue %q\n", alert.QueueEnv, alert.QueueName)
				continue
			}

			queueConfig := config.ManualConfig("iron_mq", &queueEnv)
			q := mq.ConfigNew(alert.QueueName, &queueConfig)
			info, err := q.Info()
			if err != nil {
				fmt.Println("Could not get information about", alert.QueueName, err)
				continue
			}
			queueSize = info.Size
			// Update previous size
			go queueCache.Set(key, queueSize, 900)
			prev[key] = queueSize

			workerEnv, exists := c.Environments[alert.WorkerEnv]
			if !exists {
				fmt.Printf("Environment %q is not defined for worker %q\n", alert.WorkerEnv, alert.WorkerName)
				continue
			}
			queued, running, err := workerStats(&workerEnv, alert.WorkerName)
			if err != nil {
				fmt.Printf("Could not get code stats for %s, %v", alert.WorkerName, err)
				continue
			}

			launch := evalTriggers(queued, running, queueSize, prevQueueSize, alert.Triggers)
			fmt.Printf("%v | Queue: %s (size=%d, prev=%d), CodeName=%s (queued=%d, running=%d), Launching %d\n", time.Now().Format(time.ANSIC), alert.QueueName, queueSize, prevQueueSize, alert.WorkerName, queued, running, launch)

			if launch > 0 {
				workerConfig := config.ManualConfig("iron_worker", &workerEnv)
				w := &worker.Worker{Settings: workerConfig}

				tasks := make([]worker.Task, launch)
				for x := 0; x < len(tasks); x++ {
					tasks[x].CodeName = alert.WorkerName
					tasks[x].Cluster = alert.Cluster
				}

				_, err = w.TaskQueue(tasks...)
				if err != nil {
					fmt.Println("Could not create tasks for", alert.WorkerName)
					continue
				}
			}
		}

		time.Sleep(interval)
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func workerKey(projectID, codeName string) string {
	return projectID + "|" + codeName
}

type CodeStats struct {
	Running int `json:"running"`
	Queued  int `json:"queued"`
	// ignore other states
}

func workerStats(env *config.Settings, codeName string) (queued, running int, err error) {
	codeID, exists := codeIds[workerKey(env.ProjectId, codeName)]
	if !exists {
		workerConfig := config.ManualConfig("iron_worker", env)
		w := &worker.Worker{Settings: workerConfig}
		codes, err := w.CodePackageList(0, 100)
		if err != nil {
			return 0, 0, err
		}

		for _, c := range codes {
			codeIds[workerKey(c.ProjectId, c.Name)] = c.Id
			if c.Name == codeName {
				codeID = c.Id
			}
		}
	}

	if len(codeID) == 0 {
		return 0, 0, fmt.Errorf("Could not get id for %s", codeName)
	}
	if len(env.ProjectId) == 0 || len(env.Token) == 0 {
		return 0, 0, fmt.Errorf("Could not get env for %s", codeName)
	}

	url := fmt.Sprintf("https://%s/2/projects/%s/codes/%s/stats?oauth=%s", swapi, env.ProjectId, codeID, env.Token)
	resp, err := client.Get(url)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	decoder := json.NewDecoder(resp.Body)
	var s CodeStats
	err = decoder.Decode(&s)
	if err != nil {
		return 0, 0, err
	}

	return s.Queued, s.Running, nil
}

func evalTriggers(queued, running, queueSize, prevQueueSize int, triggers []Trigger) (launch int) {
	for _, t := range triggers {
		switch t.Typ {
		case TriggerFixed:
			if queueSize >= t.Value {
				if t.Value <= prevQueueSize {
					continue
				}
				launch = max(launch, 1)
			}
		case TriggerProgressive:
			if queueSize < t.Value {
				continue
			}

			previous_level := prevQueueSize / t.Value
			current_level := queueSize / t.Value
			if current_level > previous_level {
				launch = max(launch, current_level-previous_level)
			}
		case TriggerRatio:
			expected_runners := (queueSize + t.Value - 1) / t.Value // Only have 0 runners if qsize=0

			diff := expected_runners - (queued + running)
			if diff > 0 {
				launch = max(launch, diff)
			}
		}
	}
	return
}
