package main

import (
	"fmt"

	"github.com/Alsanea-Ala/logmon/cmd/agent"
)

/*
trying to tail /home/ala/.config/Postman/logs/main.log
*/

func main() {

	logPathFile := "app.log"
	go agent.DispayLogs(logPathFile)
	agent.GenLogs(logPathFile)


	fmt.Println("end")
	
}
