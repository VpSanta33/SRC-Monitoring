package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"src-monitoring/internal/monitor"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error {
	state := flag.String("state", ".state/state", "Durable JSONL checkpoint directory")
	out := flag.String("out", "web/public/data", "Public JSON output directory")
	configPath := flag.String("config", "monitor.config.json", "Source configuration")
	offline := flag.Bool("offline", false, "Export restored checkpoint without fetching")
	flag.Parse()
	config, err := monitor.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	temp, err := os.MkdirTemp("", "src-collect-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	store, err := monitor.OpenStore(filepath.Join(temp, "working.db"))
	if err != nil {
		return err
	}
	defer store.DB.Close()
	// Only a genuinely missing directory is a new baseline. A partial/corrupt checkpoint is fatal.
	if _, err = os.Stat(*state); err == nil {
		if err = store.RestoreCheckpoint(*state); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	} else if *offline {
		return fmt.Errorf("offline mode requires an existing checkpoint")
	}
	for _, platform := range monitor.Platforms {
		if _, err = store.DB.Exec("UPDATE sources SET enabled=?,next_run='' WHERE platform=?", config.Enabled[platform], platform); err != nil {
			return err
		}
	}
	var collectErr error
	if !*offline {
		syncer := monitor.NewSyncer(store)
		collectErr = syncer.Collect()
		syncer.Close()
	}
	if err = store.SaveCheckpoint(*state); err != nil {
		return err
	}
	if err = store.ExportSite(*out, config); err != nil {
		return err
	}
	log.Printf("Public data exported to %s; checkpoint saved to %s", *out, *state)
	// Safe partial results and error state are saved for publication, but CI must report failure.
	return collectErr
}
