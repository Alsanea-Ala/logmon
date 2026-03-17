package main

import (
	"fmt"
	"log"
	"os"
	// "path/filepath"
	"sync"
	"time"

	"github.com/Alsanea-Ala/logmon/internal/agent"
	"github.com/nxadm/tail"
)

func DispayLogs(logPathFile string) {
	// Create a tail
	t, err := tail.TailFile(
		logPathFile, tail.Config{Follow: true, ReOpen: true})
	if err != nil {
		panic(err)
	}

	// Print the text of each received line
	for line := range t.Lines {
		fmt.Println(line.Text)
	}
}

func GenLogs(logFilePath string) {

	file, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0777)
	if err != nil {
		panic(err)
	}

	defer file.Close()

	log.SetOutput(file)

	counter := 1
	for counter < 100 {
		log.Printf("log message with counter value: %d", time.Duration(counter))
		counter++
		time.Sleep(time.Millisecond * time.Duration(counter))
	}
}

// func getConfigPath() string {
// 	exe, err := os.Executable()
// 	if err != nil {
// 		return "agent.yaml"
// 	}

// 	if filepath.Dir(exe) == os.TempDir() {
// 		return "agent.yaml"
// 	}

// 	return filepath.Join(filepath.Dir(exe), "agent.yaml")
// }

func main() {

	// configPath := getConfigPath()
	const configPath = "./agent.yaml"

	log.Println("configPath: ", configPath)

	if err := agent.GenerateConfig(configPath); err != nil {
		log.Fatal("failed to generate config:", err)
	}

	cfg, err := agent.LoadConfig(configPath)
	if err != nil {
		log.Fatal("failed to load config:", err)
	}

	// for testing
	var wg sync.WaitGroup

	for _, logFile := range cfg.Logs {
		wg.Add(2)
		go func(path string) {
			defer wg.Done()
			DispayLogs(path)
		}(logFile.Path)

		go func(path string) {
			defer wg.Done()
			GenLogs(path)
		}(logFile.Path)
	}

	wg.Wait()
	fmt.Println("end")

}
