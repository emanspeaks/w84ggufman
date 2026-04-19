package main

import (
	"context"
	"fmt"
	"time"

	"github.com/coreos/go-systemd/v22/dbus"
)

func restartService(name string) error {
	ctx := context.Background()
	conn, err := dbus.NewSystemConnectionContext(ctx)
	if err != nil {
		return fmt.Errorf("connect to dbus: %w", err)
	}
	defer conn.Close()

	ch := make(chan string, 1)
	if _, err := conn.RestartUnitContext(ctx, name, "replace", ch); err != nil {
		return fmt.Errorf("restart unit: %w", err)
	}
	result := <-ch
	if result != "done" {
		return fmt.Errorf("restart result: %s", result)
	}
	return nil
}

// serviceMainPID returns the MainPID of a running systemd service, or 0 on error.
func serviceMainPID(name string) uint32 {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := dbus.NewSystemConnectionContext(ctx)
	if err != nil {
		return 0
	}
	defer conn.Close()
	props, err := conn.GetUnitPropertiesContext(ctx, name)
	if err != nil {
		return 0
	}
	pid, _ := props["MainPID"].(uint32)
	return pid
}
