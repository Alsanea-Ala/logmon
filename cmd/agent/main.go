package main

import (
	"fmt"
	"github.com/Alsanea-Ala/logmon/internal/agent"
	"log"
	"sync"
	// "github.com/spf13/cobra"
)

func main() {

	configPath := agent.GetConfigPath()
	// const configPath = "./agent.yaml"

	log.Println("configPath: ", configPath)

	if err := agent.GenerateConfig(configPath); err != nil {
		log.Fatal("failed to generate config:", err)
	}

	cfg, err := agent.LoadConfig(configPath)
	if err != nil {
		log.Fatal("failed to load config:", err)
	}

	// for testing
	// for _, logFile := range cfg.Logs {
	// 	GenLogs(logFile.Path)
	// }

	var wg sync.WaitGroup

	for _, logFile := range cfg.Logs {
		wg.Add(1)
		go func(path, label string) {
			defer wg.Done()
			agent.DispayLogs(path, label)
		}(logFile.Path, logFile.Label)

	}

	wg.Wait()
	fmt.Println("end")

}
