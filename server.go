package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Server struct {
	st *Store
	sw switchSource // nil when no switch is configured
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /edit/port/{n}", s.portForm)
	mux.HandleFunc("POST /edit/port/{n}", s.portSave)
	mux.HandleFunc("GET /edit/{section}", s.editForm)
	mux.HandleFunc("POST /edit/{section}", s.editSave)
	mux.HandleFunc("GET /history", s.history)
	mux.HandleFunc("GET /history/{hash}", s.view)
	mux.HandleFunc("POST /history/{hash}/restore", s.restore)
	mux.HandleFunc("GET /switch", s.switchView)
	mux.HandleFunc("POST /switch/sync", s.switchSync)
	mux.HandleFunc("GET /pdf", s.pdf)
	mux.HandleFunc("GET /data.json", s.dataJSON)
	return sameOrigin(mux)
}

// sameOrigin rejects cross-site form posts.
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if o := r.Header.Get("Origin"); o != "" {
				if u, err := url.Parse(o); err != nil || u.Host != r.Host {
					http.Error(w, "cross-origin post refused", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	log.Print(err)
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func html(w http.ResponseWriter, code int, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	fmt.Fprint(w, body)
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	d, err := s.st.Load()
	if err != nil {
		s.fail(w, err)
		return
	}
	// Served without a doctype on purpose: it is the same document the generator wrote.
	html(w, 200, Body(d, Opts{Edit: true}))
}

func (s *Server) dataJSON(w http.ResponseWriter, r *http.Request) {
	b, err := s.st.Raw()
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(b)
}

const fontLink = `<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Manrope:wght@600;700;800&family=Inter:wght@400;500;600&display=swap">`

func page(title, body string) string {
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>` +
		esc(title) + `</title>` + fontLink + `<style>` + editCSS + `</style></head><body><div class="wrap">` + body + `</div></body></html>`
}

func (s *Server) renderForm(w http.ResponseWriter, code int, name string, sec section, d *Data, errs []string, summary string) {
	b := &formB{d: d}
	b.w(`<header><a href="/">&larr; Back to the map</a><h1>Edit: ` + esc(sec.title) + `</h1></header>`)
	if len(errs) > 0 {
		b.w(`<div class="err"><strong>Not saved. Please fix:</strong><ul>`)
		for _, e := range errs {
			b.w("<li>" + esc(e) + "</li>")
		}
		b.w("</ul></div>")
	}
	b.w(`<form method="post" action="/edit/` + name + `">`)
	sec.form(b)
	b.w(`<div class="bar"><input type="text" name="summary" maxlength="72" placeholder="What changed? (commit note)" value="` + esc(summary) + `"><button type="submit">Save</button><a href="/">Cancel</a></div></form>`)
	html(w, code, page("Edit - "+sec.title, b.String()))
}

func (s *Server) editForm(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("section")
	sec, ok := sections[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	d, err := s.st.Load()
	if err != nil {
		s.fail(w, err)
		return
	}
	s.renderForm(w, 200, name, sec, d, nil, "")
}

func (s *Server) editSave(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("section")
	sec, ok := sections[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// One lock around read-modify-write so concurrent edits do not lose each other.
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	d, err := s.st.Load()
	if err != nil {
		s.fail(w, err)
		return
	}
	f := &formP{v: r.PostForm}
	sec.apply(d, f)
	summary := strings.Join(strings.Fields(f.str("summary")), " ")
	errs := append(f.errs, d.Validate()...)
	if len(errs) > 0 {
		s.renderForm(w, http.StatusUnprocessableEntity, name, sec, d, errs, summary)
		return
	}
	if summary == "" {
		summary = "updated"
	}
	if err := s.st.Save(d, name+": "+summary); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	cs, err := s.st.History()
	if err != nil {
		s.fail(w, err)
		return
	}
	var b strings.Builder
	b.WriteString(`<header><a href="/">&larr; Back to the map</a><h1>History</h1><p class="muted">Every save is a commit. Restoring adds a new commit; nothing is ever rewritten.</p></header><div class="tw"><table><thead><tr><th>Version</th><th>Date</th><th>Change</th><th></th></tr></thead><tbody>`)
	for _, c := range cs {
		h := esc(c.Hash)
		b.WriteString(`<tr><td><code>` + h[:7] + `</code></td><td>` + esc(c.Date) + `</td><td>` + esc(c.Subject) + `</td><td>` +
			`<a href="/history/` + h + `">view</a> <form method="post" action="/history/` + h + `/restore" style="display:inline" onsubmit="return confirm('Restore this version as a new commit?')"><button class="alt" type="submit">restore</button></form></td></tr>`)
	}
	b.WriteString(`</tbody></table></div>`)
	html(w, 200, page("History", b.String()))
}

func (s *Server) view(w http.ResponseWriter, r *http.Request) {
	h := r.PathValue("hash")
	d, subj, err := s.st.At(h)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	banner := `<div class="banner"><strong>Read-only: version ` + esc(h[:7]) + `</strong> ` + esc(subj) +
		` <a href="/history">History</a> <form method="post" action="/history/` + esc(h) + `/restore"><button type="submit">Restore this version</button></form></div>` + "\n"
	html(w, 200, Body(d, Opts{Banner: banner, Edit: false})+"<style>\n"+editLinksCSS+"\n</style>")
}

func (s *Server) restore(w http.ResponseWriter, r *http.Request) {
	if err := s.st.Restore(r.PathValue("hash")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func chromeBin() (string, error) {
	for _, n := range []string{"google-chrome", "chromium", "chromium-browser"} {
		if p, err := exec.LookPath(n); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no google-chrome or chromium in PATH")
}

func (s *Server) pdf(w http.ResponseWriter, r *http.Request) {
	d, err := s.st.Load()
	if err != nil {
		s.fail(w, err)
		return
	}
	bin, err := chromeBin()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	tmp, err := os.MkdirTemp("", "office-map-pdf-")
	if err != nil {
		s.fail(w, err)
		return
	}
	defer os.RemoveAll(tmp)
	in, out := filepath.Join(tmp, "print.html"), filepath.Join(tmp, "map.pdf")
	if err := os.WriteFile(in, []byte(PrintHTML(d)), 0o644); err != nil {
		s.fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	args := []string{"--headless=new", "--disable-gpu", "--no-pdf-header-footer", "--user-data-dir=" + filepath.Join(tmp, "profile"), "--print-to-pdf=" + out}
	if os.Geteuid() == 0 {
		args = append(args, "--no-sandbox")
	}
	args = append(args, "file://"+in)
	if o, err := exec.CommandContext(ctx, bin, args...).CombinedOutput(); err != nil {
		s.fail(w, fmt.Errorf("chrome: %v: %s", err, o))
		return
	}
	f, err := os.Open(out)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="Office_Network_Map.pdf"`)
	http.ServeContent(w, r, "Office_Network_Map.pdf", time.Now(), f)
}
