// Command office-map serves a living, editable version of the office network map.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"officemap/live"
	"officemap/switchread"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8090", "address to listen on")
	dir := flag.String("data", "./data", "data directory (network.json + git history)")
	seedFile := flag.String("seed", "", "network.json to import when the data dir is empty (default: built-in sample)")
	gitName := flag.String("git-name", defaultGitName, "git committer name for edits")
	gitEmail := flag.String("git-email", defaultGitEmail, "git committer email for edits")
	var sc switchread.Config
	flag.StringVar(&sc.Host, "switch-host", "", "switch host or host:port (enables /switch)")
	flag.StringVar(&sc.User, "switch-user", "", "switch SSH user")
	flag.StringVar(&sc.KeyFile, "switch-key", "", "switch SSH private key file")
	flag.StringVar(&sc.KnownHosts, "switch-known-hosts", "", "OpenSSH known_hosts file (host keys are verified strictly)")
	flag.StringVar(&sc.HostKeyAlias, "switch-hostkey-alias", "", "look the switch up in known_hosts under this name")
	flag.StringVar(&sc.Jump, "switch-jump", "", "user@host to hop through (same key)")
	liveOn := flag.Bool("live", false, "poll the switch and ping devices for live status and change alerts (read-only)")
	liveEvery := flag.Duration("live-every", 30*time.Second, "live poll interval")
	offlineAfter := flag.Duration("offline-after", 10*time.Minute, "raise an alert when a device has not answered ping for this long")
	stateDir := flag.String("state", "", "directory for alerts.json (default: ../state next to the data dir; never inside it)")
	flag.StringVar(&themeDir, "theme", "", "directory of extra *.css (and fonts/images) linked after the built-in styles")
	flag.Parse()

	opts := StoreOptions{GitName: *gitName, GitEmail: *gitEmail}
	if *seedFile != "" {
		b, err := os.ReadFile(*seedFile)
		if err != nil {
			log.Fatal(err)
		}
		opts.Seed = b
	}
	st, err := OpenStore(*dir, opts)
	if err != nil {
		log.Fatal(err)
	}
	srv := &Server{st: st, sw: newSSHSwitch(sc)}
	if *liveOn {
		sd := *stateDir
		if sd == "" {
			sd = filepath.Join(filepath.Dir(filepath.Clean(*dir)), "state")
		}
		al, err := live.OpenAlerts(filepath.Join(sd, "alerts.json"))
		if err != nil {
			log.Fatal(err)
		}
		srv.alerts = al
		srv.startLive(context.Background(), live.Config{Every: *liveEvery, OfflineAfter: *offlineAfter})
		log.Printf("live status on (every %s, alerts in %s)", *liveEvery, sd)
	}
	log.Printf("office-map listening on http://%s (data %s)", *listen, *dir)
	log.Fatal(http.ListenAndServe(*listen, srv.routes()))
}
