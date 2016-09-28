package main

import (
	"encoding/json"
	"fmt"

	"github.com/iron-io/iron_go3/config"
	"github.com/iron-io/iron_go3/worker"
)

type CodeStats struct {
	Running int `json:"running"`
	Queued  int `json:"queued"`
	// ignore other states
}

func workerKey(projectID, codeName string) string {
	return projectID + "|" + codeName
}

func codeStats(env *config.Settings, codeName string) (queued, running int, err error) {
	codeId, exists := s.getCodeId(workerKey(env.ProjectId, codeName))
	if !exists {
		w := &worker.Worker{Settings: *env}
		codes, err := w.CodePackageList(0, 100)
		if err != nil {
			return 0, 0, err
		}

		for _, c := range codes {
			s.setCodeId(workerKey(env.ProjectId, c.Name), c.Id)
			if c.Name == codeName {
				codeId = c.Id
			}
		}
	}

	if len(codeId) == 0 {
		return 0, 0, fmt.Errorf("Could not find id for %q\n", codeName)
	}
	if len(env.ProjectId) == 0 || len(env.Token) == 0 {
		return 0, 0, fmt.Errorf("Could not get env for %q\n", codeName)
	}

	url := fmt.Sprintf("%s://%s/2/projects/%s/codes/%s/stats?oauth=%s", env.Scheme, env.Host, env.ProjectId, codeId, env.Token)
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
