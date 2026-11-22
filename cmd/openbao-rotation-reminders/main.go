// Command openbao-rotation-reminders checks the rotation reminders in OpenBao
// KV v2 custom_metadata once and sends a digest of the findings to ntfy.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mcsrobert/openbao-rotation-reminders/internal/app"
)

var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	status := app.Run(ctx, os.Getenv, os.Stdout, os.Stderr, time.Now().UTC())
	stop()
	os.Exit(status)
}
