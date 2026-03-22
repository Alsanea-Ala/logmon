package agent

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/nxadm/tail"
)

func DispayLogs(logPathFile, label string) {
	// Create a tail
	t, err := tail.TailFile(
		logPathFile, tail.Config{Follow: true, ReOpen: false})
	if err != nil {
		panic(err)
	}

	// Print the text of each received line
	for line := range t.Lines {
		fmt.Println(label, ": ", line.Text)
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
	for counter < 5 {
		log.Printf("%s: log message with counter value: %d", logFilePath, time.Duration(counter))
		counter++
		time.Sleep(time.Millisecond * time.Duration(counter))
	}
}

func GetConfigPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "agent.yaml"
	}

	if filepath.Dir(exe) == os.TempDir() {
		return "agent.yaml"
	}

	return filepath.Join(filepath.Dir(exe), "agent.yaml")
}
