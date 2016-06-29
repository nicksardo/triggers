package main

import (
	"fmt"
	"time"

	"github.com/iron-io/iron_go3/worker"
)

type trigger struct {
	Typ   string `json:"type"`
	Value int    `json:"value"`
}

// This process will unmarshal the task payload containing the trigger that
// caused this task to be created.
func main() {
	worker.ParseFlags()

	t := &trigger{}
	err := worker.PayloadFromJSON(t)
	if err != nil {
		fmt.Println(err)
	}

	fmt.Println("Trigger type is", t.Typ)

	switch t.Typ {
	case "min":
		// Be a long duration worker
		time.Sleep(30 * time.Second)
	default:
	}
}
