package main

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// sampleJSON is the built-in fictional "Example Office", used when no -seed file is given.
//
//go:embed sample/network.json
var sampleJSON []byte

const dataFile = "network.json"

// Store keeps network.json in a git repo so every edit is a commit.
type Store struct {
	dir      string
	gitName  string
	gitEmail string
	mu       sync.Mutex // serialises edits and restores
}

// StoreOptions configures OpenStore. Zero values pick the generic defaults.
type StoreOptions struct {
	Seed     []byte // initial network.json when the data dir has none (default: built-in sample)
	GitName  string
	GitEmail string
}

const (
	defaultGitName  = "Office Map"
	defaultGitEmail = "office-map@localhost"
)

var hashRe = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

func (s *Store) git(args ...string) (string, error) {
	full := append([]string{"-C", s.dir, "-c", "user.name=" + s.gitName, "-c", "user.email=" + s.gitEmail}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME="+s.gitName, "GIT_AUTHOR_EMAIL="+s.gitEmail,
		"GIT_COMMITTER_NAME="+s.gitName, "GIT_COMMITTER_EMAIL="+s.gitEmail)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// OpenStore prepares the data dir: creates it, seeds network.json and inits the repo on first run.
func OpenStore(dir string, o StoreOptions) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if o.GitName == "" {
		o.GitName = defaultGitName
	}
	if o.GitEmail == "" {
		o.GitEmail = defaultGitEmail
	}
	s := &Store{dir: dir, gitName: o.GitName, gitEmail: o.GitEmail}
	if _, err := os.Stat(filepath.Join(dir, dataFile)); errors.Is(err, os.ErrNotExist) {
		seed := o.Seed
		if len(seed) == 0 {
			seed = sampleJSON
		}
		d, err := parseData(seed)
		if err != nil {
			return nil, fmt.Errorf("seed is not a valid network.json: %w", err)
		}
		if err := s.write(d.marshal()); err != nil {
			return nil, err
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); errors.Is(err, os.ErrNotExist) {
		if _, err := s.git("init", "-q"); err != nil {
			return nil, err
		}
	}
	msg := "startup: uncommitted changes"
	if _, err := s.git("rev-parse", "--verify", "-q", "HEAD"); err != nil {
		msg = "initial import"
	}
	if err := s.commit(msg); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) path() string { return filepath.Join(s.dir, dataFile) }

func (s *Store) Raw() ([]byte, error) { return os.ReadFile(s.path()) }

func (s *Store) Load() (*Data, error) {
	b, err := s.Raw()
	if err != nil {
		return nil, err
	}
	return parseData(b)
}

// write replaces network.json atomically (temp file + rename).
func (s *Store) write(b []byte) error {
	tmp, err := os.CreateTemp(s.dir, ".network-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path())
}

// commit stages everything and commits if anything changed.
func (s *Store) commit(msg string) error {
	if _, err := s.git("add", "-A"); err != nil {
		return err
	}
	out, err := s.git("status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) == "" {
		return nil
	}
	_, err = s.git("commit", "-q", "--author="+s.gitName+" <"+s.gitEmail+">", "-m", msg)
	return err
}

// Save validates, writes and commits one edit. Callers hold s.mu.
func (s *Store) Save(d *Data, msg string) error {
	if errs := d.Validate(); len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	if err := s.write(d.marshal()); err != nil {
		return err
	}
	return s.commit(msg)
}

type Commit struct{ Hash, Date, Subject string }

func (s *Store) History() ([]Commit, error) {
	out, err := s.git("log", "--format=%H%x09%aI%x09%s", "--", dataFile)
	if err != nil {
		return nil, err
	}
	var cs []Commit
	for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
		if p := strings.SplitN(ln, "\t", 3); len(p) == 3 {
			cs = append(cs, Commit{p[0], p[1], p[2]})
		}
	}
	return cs, nil
}

// At returns the document as of a commit.
func (s *Store) At(hash string) (*Data, string, error) {
	if !hashRe.MatchString(hash) {
		return nil, "", errors.New("bad commit id")
	}
	out, err := s.git("show", hash+":"+dataFile)
	if err != nil {
		return nil, "", err
	}
	d, err := parseData([]byte(out))
	if err != nil {
		return nil, "", err
	}
	subj, _ := s.git("log", "-1", "--format=%s", hash)
	return d, strings.TrimSpace(subj), nil
}

// Restore writes an old version as a new commit; history is never rewritten.
func (s *Store) Restore(hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, subj, err := s.At(hash)
	if err != nil {
		return err
	}
	return s.Save(d, fmt.Sprintf("restore: version %s (%s)", hash[:7], subj))
}
