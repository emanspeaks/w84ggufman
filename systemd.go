package main

import (
	"context"
	"fmt"

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

