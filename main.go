package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/iron-io/iron_go/config"
	"github.com/iron-io/iron_go/mq"
	"github.com/iron-io/iron_go/worker"
)

const (
	interval   = 15 * time.Second
	maxRunTime = 30 * time.Minute
	swapi      = "worker-aws-us-east-1.iron.io"
)

const (
	TriggerFixed = iota
	TriggerProgressive
	TriggerRatio
)

var (
	prev    map[string]int
	codeIds map[string]string
)

type Config struct {
	Environments map[string]config.Settings `json:"envs"`
	Alerts       []QueueWorkerAlert
}

type QueueWorkerAlert struct {
	QueueName  string
	QueueEnv   string
	WorkerName string
	WorkerEnv  string
	Cluster    string
	Triggers   []Trigger
}

type Trigger struct {
	Typ   int
	Value int
}

func queueKey(qw QueueWorkerAlert) string {
	return qw.QueueEnv + "|" + qw.QueueName
}

func main() {
	start := time.Now()
	prev = make(map[string]int)
	codeIds = make(map[string]string)

	// Retrieve configuration
	c := &Config{}
	worker.ParseFlags()
	worker.ConfigFromJSON(c)

	for {
		if time.Since(start) > maxRunTime {
			break
		}

		for _, alert := range c.Alerts {
			queueSize, prevQueueSize := 0, 0
			key := queueKey(alert)

			// Get previous size
			if v, e := prev[key]; e {
				prevQueueSize = v
			}

			queueEnv, exists := c.Environments[alert.QueueEnv]
			if !exists {
				fmt.Printf("Environment %s is not defined for queue %s\n", alert.QueueEnv, alert.QueueName)
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
			prev[key] = info.Size

			workerEnv, exists := c.Environments[alert.WorkerEnv]
			if !exists {
				fmt.Printf("Environment %s is not defined for worker %s\n", alert.WorkerEnv, alert.WorkerName)
				continue
			}
			queued, running, err := workerStats(&workerEnv, alert.WorkerName)
			if err != nil {
				fmt.Printf("Could not get code stats for %s, %v", alert.WorkerName, err)
				continue
			}

			launch := evalTriggers(queued, running, queueSize, prevQueueSize, alert.Triggers)
			_ = launch

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

	resp, err := http.Get(fmt.Sprintf("https://%s/2/projects/%s/codes/%s/stats?oauth=%s", swapi, env.ProjectId, codeID, env.Token))
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
				launch++
			}
		case TriggerProgressive:
			if queueSize < t.Value {
				continue
			}

			previous_level := prevQueueSize / t.Value
			current_level := queueSize / t.Value
			if current_level > previous_level {
				launch += current_level - previous_level
			}
		case TriggerRatio:
			expected_runners := (queueSize + t.Value - 1) / t.Value // Only have 0 runners if qsize=0

			diff := expected_runners - (queued + running)
			if diff > 0 {
				launch += diff
			}
		}
	}
	return
}
