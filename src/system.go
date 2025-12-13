package main

import (
	"fmt"
	"os/exec"
)

// Function to check if user ragproxy exists
func checkRagproxyUser() error {
	// Check if user ragproxy exists
	cmd := exec.Command("id", "ragproxy")
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("user 'ragproxy' not found. Please create the user: sudo useradd ragproxy")
	}
	return nil
}
