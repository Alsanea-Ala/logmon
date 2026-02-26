package agent

import (
	"fmt"
	"log"
	"os"
	"time"

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
