package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/iron-io/iron_go3/api"
	"github.com/iron-io/iron_go3/cache"
	"github.com/iron-io/iron_go3/config"
	"github.com/iron-io/iron_go3/mq"
	"github.com/iron-io/iron_go3/worker"
)

const (
	defaultInterval = 10 * time.Second
	defaultRuntime  = 30 * time.Minute
	defaultSwapi    = "https://worker-aws-us-east-1.iron.io"
	configFile      = "scale.json"
)

var (
	s = state{
		queueSizes: make(map[string]int),
		codeIds:    make(map[string]string),
	}
	client = api.HttpClient
	c      *Config
	qCache *cache.Cache
)

func main() {
	worker.ParseFlags()

	var err error
	c, err = getConfig(configFile)
	if err != nil {
		log.Fatalln(err)
	}

	// Setup cache settings
	cacheEnv, exists := c.getSettings("cache", c.CacheEnv)
	if !exists {
		log.Fatalln("No cache environment set")
		return
	}
	qCache = &cache.Cache{Settings: *cacheEnv, Name: "autoscale-prevs"}

	// Determine runtime
	runtime := defaultRuntime
	if c.Runtime != nil {
		runtime = time.Duration(*c.Runtime) * time.Second
	}

	// Start watchers
	wg := &sync.WaitGroup{}
	stop := make(chan struct{})
	for _, alert := range c.Alerts {
		if len(alert.Triggers) == 0 {
			continue
		}

		wg.Add(1)
		go watchTriggers(alert, wg, stop)
	}

	// Main goroutine sleeps, stops, then exits
	time.Sleep(runtime)
	close(stop)
	wg.Wait()
}

func watchTriggers(a QueueWorkerAlert, wg *sync.WaitGroup, stop chan struct{}) {
	defer wg.Done()

	// Determine trigger check interval
	interval := defaultInterval
	if a.Interval != nil && *a.Interval >= 1 {
		interval = time.Duration(*a.Interval) * time.Second
	}

	// Queue Settings
	queueEnv, exists := c.getSettings("mq", a.QueueEnv)
	if !exists {
		fmt.Printf("Environment %q is not defined for queue %q\n", a.QueueEnv, a.QueueName)
		return
	}
	q := mq.ConfigNew(a.QueueName, queueEnv)

	// Worker Settings
	workerEnv, exists := c.getSettings("worker", a.WorkerEnv)
	if !exists {
		fmt.Printf("Environment %q is not defined for queue %q\n", a.QueueEnv, a.QueueName)
		return
	}

	knowPrevious := a.needPreviousSize()

Loop:
	for {
		checkTriggers(a, q, workerEnv, a.WorkerName, knowPrevious)

		// Decide to stop looping
		select {
		case <-stop:
			break Loop
		default:
		}

		time.Sleep(interval)
	}
}

func checkTriggers(a QueueWorkerAlert, q mq.Queue, workerEnv *config.Settings, codeName string, knowPrevious bool) {
	qKey := queueKey(a)

	queueSizePrev, sizePrevExists := 0, false
	if knowPrevious {
		queueSizePrev, sizePrevExists = queuePrevSize(qKey)
	}
	queueSizeCurr, sizeCurrErr := queueCurrSize(qKey, q)

	if sizeCurrErr != nil {
		fmt.Printf("Could not get info about %s: %v\n", q.Name, sizeCurrErr)
		return
	} else if knowPrevious {
		// Update cache value
		go qCache.Set(qKey, queueSizeCurr, 900)
	}

	// Update previous value
	if sizePrevExists {
		s.setQueueSize(qKey, queueSizeCurr)
	} else {
		queueSizePrev = queueSizeCurr
	}

	queued, running, err := codeStats(workerEnv, codeName)
	if err != nil {
		fmt.Printf("Could not get code stats for %s, err: %v\n", codeName, err)
		return
	}

	// Determine amount of tasks to launch
	launch, maxTrigger := evalTriggers(queued, running, queueSizeCurr, queueSizePrev, a.Triggers)

	if a.Min != nil && (launch+queued+running) < *a.Min {
		launch = *a.Min - queued - running
		maxTrigger = minTrigger(*a.Min)
	}

	if a.Max != nil && (launch+queued+running) > *a.Max {
		launch = *a.Max - queued - running
	}

	launchStmt := ""
	if launch > 0 {
		launchStmt = " Launching " + strconv.Itoa(launch)

		d, _ := json.Marshal(maxTrigger)
		dstr := string(d)

		w := &worker.Worker{Settings: *workerEnv}
		tasks := make([]worker.Task, launch)
		for x := 0; x < len(tasks); x++ {
			tasks[x].CodeName = a.WorkerName
			tasks[x].Cluster = a.Cluster
			tasks[x].Priority = a.Priority
			tasks[x].Payload = dstr
		}

		_, err = w.TaskQueue(tasks...)
		if err != nil {
			fmt.Println("Could not create tasks for", a.WorkerName)
			return
		}
	}

	prevStmt := ""
	if sizePrevExists {
		prevStmt = ", prev: " + strconv.Itoa(queueSizePrev)
	}
	fmt.Printf("Queue: %s (size: %d%s), CodeName: %s (queued: %d, running: %d)%s\n", q.Name, queueSizeCurr, prevStmt, a.WorkerName, queued, running, launchStmt)

}

func queueKey(qw QueueWorkerAlert) string {
	return qw.QueueEnv + "|" + qw.QueueName
}

func queuePrevSize(key string) (int, bool) {
	if v, exists := s.getQueueSize(key); exists {
		return v, true
	}

	vCached, err := qCache.Get(key)
	if err == nil {
		return int(vCached.(float64)), true
	}
	if !strings.Contains(err.Error(), "not found") {
		fmt.Println(err) // Print errors not associated with cache/key not found errors
	}
	return 0, false
}

func queueCurrSize(key string, q mq.Queue) (int, error) {
	i, err := q.Info()
	if err != nil {
		return 0, err
	}

	return i.Size, err
}

func evalTriggers(queued, running, queueSize, prevQueueSize int, triggers []Trigger) (launch int, maxTrigger Trigger) {
	for _, t := range triggers {
		tlaunch := 0

		switch t.Typ {
		case "fixed":
			if queueSize >= t.Value && (queued+running) == 0 {
				tlaunch = 1
			}
		case "progressive":
			if queueSize < t.Value {
				continue
			}
			previous_level := prevQueueSize / t.Value
			current_level := queueSize / t.Value
			tlaunch = current_level - previous_level
		case "ratio":
			expected_runners := (queueSize + t.Value - 1) / t.Value // Only have 0 runners if qsize=0
			diff := expected_runners - (queued + running)
			tlaunch = diff
		}

		if tlaunch > launch {
			launch = tlaunch
			maxTrigger = t
		}
	}

	return launch, maxTrigger
}

func minTrigger(i int) Trigger {
	return Trigger{
		Typ:   "min",
		Value: i,
	}
}
