// log.go
package main

import (
	"fmt"
	"log"
	"os"
)

// Function to set up logging (stdout and file for access, error, and debug logs)
func setupLogging() (*log.Logger, *log.Logger, *log.Logger, *log.Logger) {
	// Access log file
	accessLogFile, err := os.OpenFile("/var/log/ragproxy/access.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("Error opening access log file:\n %v", err)
	}

	// Error log file
	errorLogFile, err := os.OpenFile("/var/log/ragproxy/error.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("Error opening error log file: %v\n", err)
	}

	// Debug log file
	debugLogFile, err := os.OpenFile("/var/log/ragproxy/debug.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("Error opening debug log file: %v\n", err)
	}

	// Create separate loggers for stdout (journald), access log, error log, and debug log
	journaldLogger := log.New(os.Stdout, "", log.LstdFlags)
	accessLogger := log.New(accessLogFile, "ACCESS: ", log.LstdFlags)
	errorLogger := log.New(errorLogFile, "ERROR: ", log.LstdFlags)
	debugLogger := log.New(debugLogFile, "DEBUG: ", log.LstdFlags)

	return journaldLogger, accessLogger, errorLogger, debugLogger
}
