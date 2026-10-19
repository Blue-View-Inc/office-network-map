// Command office-map serves a living, editable version of the office network map.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"

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
	log.Printf("office-map listening on http://%s (data %s)", *listen, *dir)
	log.Fatal(http.ListenAndServe(*listen, srv.routes()))
}
