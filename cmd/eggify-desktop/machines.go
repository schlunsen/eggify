package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/schlunsen/eggify/internal/connectors"
	"github.com/schlunsen/eggify/internal/machineproxy"
	"github.com/schlunsen/eggify/internal/perfmon"
	"github.com/schlunsen/eggify/internal/relay"
	"github.com/schlunsen/eggify/internal/server"
	"github.com/schlunsen/eggify/internal/server/handlers"
)

// Paired machines (ADR 014): other Macs running Eggify that this window can
// switch to (⌥⌘← / ⌥⌘→, tray → Machines). Each is reached through the Eggify relay
// by its own machine proxy. Adding one redeems a pair link from the other
// Mac (Settings → Remote access → Pair a device → Copy), read from the
// clipboard after a confirmation. The session tokens are kept encrypted with
// the connector key (keychain in the notarized app), like remote_access.json.

const (
	machinesFile = "desktop-machines.json"
	maxMachines  = 6
	// machineKeepWarm is under the transport's 90 s idle timeout.
	machineKeepWarm = 25 * time.Second
)

type savedMachine struct {
	ID       string    `json:"id"` // relay install id
	Name     string    `json:"name"`
	BaseURL  string    `json:"base_url"`
	Pin      string    `json:"pin"`
	Token    string    `json:"token"`
	Username string    `json:"username,omitempty"`
	Port     int       `json:"port,omitempty"`
	AddedAt  time.Time `json:"added_at"`
}

func (m savedMachine) machine() machineproxy.Machine {
	return machineproxy.Machine{Name: m.Name, BaseURL: m.BaseURL, Pin: m.Pin}
}

// machineStore is the encrypted machines file.
type machineStore struct {
	path string
	key  []byte
}

type machineStoreFile struct {
	Version int    `json:"version"`
	Data    string `json:"data"` // connectors.Encrypt of the JSON list
}

func (s machineStore) load() ([]savedMachine, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f machineStoreFile
	if err := json.Unmarshal(raw, &f); err != nil || f.Version != 1 {
		return nil, errors.New("machines file is corrupt")
	}
	pt, err := connectors.Decrypt(f.Data, s.key)
	if err != nil {
		return nil, errors.New("machines file does not decrypt")
	}
	var list []savedMachine
	if err := json.Unmarshal([]byte(pt), &list); err != nil {
		return nil, errors.New("machines file is corrupt")
	}
	return list, nil
}

func (s machineStore) save(list []savedMachine) error {
	pt, err := json.Marshal(list)
	if err != nil {
		return err
	}
	ct, err := connectors.Encrypt(string(pt), s.key)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(machineStoreFile{Version: 1, Data: ct})
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// machineManager owns the paired machines: their proxies, the switcher's
// targets and the tray's Machines menu.
type machineManager struct {
	app    *application.App
	sw     *machineSwitcher
	br     *bridge
	notify func(title, body, open string)

	mu      sync.Mutex
	store   *machineStore
	saved   []savedMachine
	proxies map[string]*machineproxy.Proxy
	adding  bool
	usage   map[string]cachedUsage // Usage: other Macs' last readings

	// Tray: fixed slots updated in place (rebuilding the tray menu would
	// detach the remote access items).
	switchItems []*application.MenuItem // [0] = This Mac
	removeMenu  *application.Menu
	removeItems []*application.MenuItem
	addItem     *application.MenuItem
}

func newMachineManager(app *application.App, sw *machineSwitcher, br *bridge, notify func(title, body, open string)) *machineManager {
	mm := &machineManager{app: app, sw: sw, br: br, notify: notify, proxies: map[string]*machineproxy.Proxy{}}
	sw.onChange = mm.refreshMenu
	return mm
}

// addTrayMenu adds the Machines submenu.
func (mm *machineManager) addTrayMenu(m *application.Menu) {
	sub := m.AddSubmenu("Machines")
	for i := 0; i <= maxMachines; i++ {
		i := i
		label := "This Mac"
		if i > 0 {
			label = fmt.Sprintf("Machine %d", i)
		}
		it := sub.AddCheckbox(label, i == 0)
		it.OnClick(func(*application.Context) { mm.switchToSlot(i) })
		it.SetHidden(i > 0)
		mm.switchItems = append(mm.switchItems, it)
	}
	sub.AddSeparator()
	mm.addItem = sub.Add("Add a Mac from Clipboard…")
	mm.addItem.SetEnabled(false) // until the store is open (after server setup)
	mm.addItem.OnClick(func(*application.Context) { go mm.addFromClipboard() })
	mm.removeMenu = sub.AddSubmenu("Remove")
	for i := 0; i < maxMachines; i++ {
		i := i
		it := mm.removeMenu.Add(fmt.Sprintf("Machine %d", i+1))
		it.OnClick(func(*application.Context) { go mm.confirmRemove(i) })
		it.SetHidden(true)
		mm.removeItems = append(mm.removeItems, it)
	}
	m.AddSeparator()
}

// start opens the store (the connector key: call after server setup, which
// picks the secret store backend) and starts a proxy per saved machine.
func (mm *machineManager) start(claudeDir string) error {
	key, err := connectors.LoadOrGenerateConnectorKey(claudeDir)
	if err != nil {
		return err
	}
	st := &machineStore{path: filepath.Join(claudeDir, "eggify", machinesFile), key: key}
	list, err := st.load()
	if err != nil {
		return err
	}
	mm.mu.Lock()
	mm.store = st
	mm.mu.Unlock()
	for _, m := range list {
		if err := mm.startMachine(m); err != nil {
			dlog.Printf("machine %s: %v", m.Name, err)
		}
	}
	if mm.addItem != nil {
		mm.addItem.SetEnabled(true)
	}
	mm.refreshMenu()
	return nil
}

// startMachine runs m's proxy and adds it to the switcher (and the list).
func (mm *machineManager) startMachine(m savedMachine) error {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return err
	}
	id := m.ID
	proxy, err := machineproxy.New(machineproxy.Config{
		Machine: m.machine(),
		Token:   func() string { return mm.token(id) },
		Gate:    hex.EncodeToString(buf),
		Local:   mm.br.proxiedWailsHandler(),
		// The page tells desktop from web by this cookie (utils/desktop.ts).
		BootstrapCookies: []*http.Cookie{{Name: markerCookie, Value: "wails", Path: "/", SameSite: http.SameSiteLaxMode}},
		OnUnauthorized: func() {
			name := mm.name(id)
			dlog.Printf("machine %s: token refused (signed out or revoked)", name)
			mm.notify(name+" signed this Mac out", "Sign in to "+name+" in the window to reconnect.", "")
		},
		// Signing in on the machine's login page reconnects it.
		OnSignIn: func(token string) {
			if mm.setToken(id, token) {
				dlog.Printf("machine %s: signed in again from the window; new token kept", mm.name(id))
			}
		},
	})
	if err != nil {
		return err
	}
	lns, err := machineproxy.ListenPort(m.Port)
	if err != nil {
		return err
	}
	go func() {
		if err := proxy.Serve(lns...); err != nil {
			dlog.Printf("machine proxy %s: %v", m.Name, err)
		}
	}()
	for proxy.Origin() == "" {
		time.Sleep(time.Millisecond)
	}
	m.Port = lns[0].Addr().(*net.TCPAddr).Port
	mm.mu.Lock()
	mm.proxies[id] = proxy
	mm.saved = append(mm.saved, m)
	mm.mu.Unlock()
	mm.sw.add(machineTarget{
		id:            id,
		name:          m.Name,
		hue:           machineHue(m.Name),
		open:          func(path string) string { return proxy.Origin() + safeNext(path) },
		enter:         proxy.BootstrapURL,
		origin:        proxy.Origin,
		bootstrapHops: 1, // the proxy's bootstrap page, then the dashboard
		// A few connections, since WebKit loads the page's files in parallel.
		warm: func() { _ = proxy.Warm(context.Background(), 4) },
	})
	// Keep a couple of relay connections open while the app runs, so a
	// switch after a quiet spell skips the handshakes.
	go proxy.KeepWarm(machineKeepWarm, 2)
	dlog.Printf("machine %s (%s) on %s", m.Name, m.BaseURL, proxy.Origin())
	return nil
}

func (mm *machineManager) token(id string) string {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	for _, m := range mm.saved {
		if m.ID == id {
			return m.Token
		}
	}
	return ""
}

// setToken replaces a machine's session token (a sign-in in its window)
// and saves the list. False when the machine is gone or it is unchanged.
func (mm *machineManager) setToken(id, token string) bool {
	mm.mu.Lock()
	changed := false
	for i := range mm.saved {
		if mm.saved[i].ID == id && mm.saved[i].Token != token {
			mm.saved[i].Token = token
			changed = true
		}
	}
	mm.mu.Unlock()
	if changed {
		mm.persist()
	}
	return changed
}

func (mm *machineManager) name(id string) string {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	for _, m := range mm.saved {
		if m.ID == id {
			return m.Name
		}
	}
	return "The other Mac"
}

func (mm *machineManager) persist() {
	mm.mu.Lock()
	st, list := mm.store, append([]savedMachine(nil), mm.saved...)
	mm.mu.Unlock()
	if st == nil {
		return
	}
	if err := st.save(list); err != nil {
		dlog.Printf("machines: save: %v", err)
	}
}

func (mm *machineManager) switchToSlot(i int) {
	if mm.sw == nil {
		return
	}
	if mm.sw.window != nil {
		mm.sw.window.Show()
		mm.sw.window.Focus()
	}
	mm.sw.switchToMachine(i)
	mm.refreshMenu()
}

// refreshMenu updates the tray slots: names, the current machine's check.
func (mm *machineManager) refreshMenu() {
	if len(mm.switchItems) == 0 {
		return
	}
	targets, cur := mm.sw.snapshot()
	for i, it := range mm.switchItems {
		if i < len(targets) {
			it.SetLabel(targets[i].name)
			it.SetChecked(i == cur)
			it.SetHidden(false)
		} else {
			it.SetHidden(true)
		}
	}
	for i, it := range mm.removeItems {
		if i+1 < len(targets) {
			it.SetLabel(targets[i+1].name + "…")
			it.SetHidden(false)
		} else {
			it.SetHidden(true)
		}
	}
}

// addFromClipboard pairs with the Mac whose pair link is on the clipboard.
func (mm *machineManager) addFromClipboard() {
	mm.mu.Lock()
	if mm.adding || mm.store == nil {
		mm.mu.Unlock()
		return
	}
	mm.adding = true
	n := len(mm.saved)
	mm.mu.Unlock()
	defer func() {
		mm.mu.Lock()
		mm.adding = false
		mm.mu.Unlock()
	}()

	if n >= maxMachines {
		mm.info("Too many machines", fmt.Sprintf("Eggify can switch between at most %d other Macs. Remove one first.", maxMachines))
		return
	}
	text, _ := mm.app.Clipboard.Text()
	link := strings.TrimSpace(text)
	m, _, err := machineproxy.ParsePairLink(link)
	if err != nil {
		msg := "Copy a pair link first. On the other Mac: Settings → Remote access → Eggify relay on → Pair a device → Copy."
		if strings.HasPrefix(link, "eggify://pair") {
			msg = "That pair link can't be used here: " + err.Error() + "."
		}
		mm.info("No pair link on the clipboard", msg)
		return
	}
	id, _ := relay.IDFromPin(m.Pin)
	if mm.has(id) {
		mm.info("Already added", "This Mac is already in your machines. Remove it first to pair it again.")
		return
	}
	// The link could have been put on the clipboard by anything; say what
	// adding does and let the owner decide. Nothing is sent before this.
	if !mm.confirm("Add this Mac?",
		"Eggify will sign in to the Mac at\n"+m.BaseURL+"\nwith the pair link on your clipboard (it works once, for 5 minutes). Only add a Mac you showed the pair link on yourself.",
		"Add Mac") {
		return
	}
	host, _ := os.Hostname()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	p, err := machineproxy.Redeem(ctx, link, strings.TrimSuffix(host, ".local")+" (Eggify desktop)")
	if err != nil {
		mm.info("Couldn't add the Mac", err.Error())
		return
	}
	mm.app.Clipboard.SetText("") // the code is spent; don't leave it around
	name := machineproxy.Hostname(ctx, p.Machine, p.Token)
	if name == "" {
		name = fmt.Sprintf("Mac %d", n+2)
	}
	saved := savedMachine{ID: id, Name: name, BaseURL: p.Machine.BaseURL, Pin: p.Machine.Pin, Token: p.Token, Username: p.Username, AddedAt: time.Now().UTC()}
	if err := mm.startMachine(saved); err != nil {
		mm.info("Couldn't add the Mac", err.Error())
		return
	}
	mm.persist()
	mm.refreshMenu()
	mm.notify(name+" added", "Press ⌥⌘→ to switch to it (or Machines in the menu bar icon).", "")
}

func (mm *machineManager) has(id string) bool {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	for _, m := range mm.saved {
		if m.ID == id {
			return true
		}
	}
	return false
}

// confirmRemove removes the machine in remove slot i (switch slot i+1).
func (mm *machineManager) confirmRemove(i int) {
	mm.mu.Lock()
	if i < 0 || i >= len(mm.saved) {
		mm.mu.Unlock()
		return
	}
	m := mm.saved[i]
	mm.mu.Unlock()
	if !mm.confirm("Remove "+m.Name+"?", "This Mac signs out of "+m.Name+" and forgets it. To add it again you need a new pair link from "+m.Name+".", "Remove") {
		return
	}
	mm.sw.remove(m.ID) // switches back to this Mac first if needed
	mm.mu.Lock()
	proxy := mm.proxies[m.ID]
	delete(mm.proxies, m.ID)
	for j := range mm.saved {
		if mm.saved[j].ID == m.ID {
			mm.saved = append(mm.saved[:j], mm.saved[j+1:]...)
			break
		}
	}
	mm.mu.Unlock()
	mm.persist()
	mm.refreshMenu()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := machineproxy.SignOut(ctx, m.machine(), m.Token); err != nil {
			dlog.Printf("machine %s: sign out: %v", m.Name, err)
		}
		// After the switch away has navigated.
		time.Sleep(2 * time.Second)
		if proxy != nil {
			_ = proxy.Close()
		}
	}()
}

func (mm *machineManager) info(title, msg string) {
	application.InvokeAsync(func() {
		mm.app.Dialog.Info().SetTitle(title).SetMessage(msg).Show()
	})
}

// confirm asks a yes/no question; true when the owner chose ok.
func (mm *machineManager) confirm(title, msg, ok string) bool {
	answer := make(chan bool, 1)
	application.InvokeAsync(func() {
		d := mm.app.Dialog.Question().SetTitle(title).SetMessage(msg)
		d.AddButton(ok).OnClick(func() { answer <- true })
		no := d.AddButton("Cancel").OnClick(func() { answer <- false })
		d.SetDefaultButton(no) // Return never adds or removes a Mac
		d.SetCancelButton(no)
		d.Show()
	})
	select {
	case a := <-answer:
		return a
	case <-time.After(10 * time.Minute):
		return false
	}
}

// The server asks for the paired Macs on behalf of the owner's iPhone
// (GET /api/machines/paired, POST …/:id/pair-code, signed-in admin only).
// machineManager implements server.PairedMachines: names and relay
// addresses go out, the session tokens never do.
var _ server.PairedMachines = (*machineManager)(nil)

// createPairing is replaced by tests.
var createPairing = machineproxy.CreatePairing

// List returns the paired Macs without their tokens.
func (mm *machineManager) List() []server.PairedMachine {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	out := make([]server.PairedMachine, 0, len(mm.saved))
	for _, m := range mm.saved {
		out = append(out, server.PairedMachine{ID: m.ID, Name: m.Name, BaseURL: m.BaseURL})
	}
	return out
}

// PairCode has machine id mint a pair link for another of the owner's
// devices, as this Mac's paired user there (pinned relay connection). Only
// the link comes back; the device redeems it at that Mac and gets its own
// session (its own row under Signed-in devices there).
func (mm *machineManager) PairCode(ctx context.Context, id, deviceName string) (string, time.Time, error) {
	mm.mu.Lock()
	var found *savedMachine
	for i := range mm.saved {
		if mm.saved[i].ID == id {
			m := mm.saved[i]
			found = &m
			break
		}
	}
	mm.mu.Unlock()
	if found == nil {
		return "", time.Time{}, handlers.ErrPairedMachineNotFound
	}
	pc, err := createPairing(ctx, found.machine(), found.Token, deviceName)
	if err != nil {
		return "", time.Time{}, err
	}
	dlog.Printf("machine %s: pair code minted for another device", found.Name)
	return pc.PairURL, pc.ExpiresAt, nil
}

// usageCacheFor is how long another Mac's reading is reused, so several
// pages (or a menu kept open) don't each ask through the relay.
const usageCacheFor = 4 * time.Second

type cachedUsage struct {
	at time.Time
	u  *perfmon.Usage // nil: that Mac did not answer (asleep, older version)
}

// Usage returns CPU and memory per machine id ("" = this Mac, read in
// process). Other Macs are asked in parallel through their proxies; one that
// does not answer within 3 s is left out.
func (mm *machineManager) Usage(ctx context.Context) map[string]*perfmon.Usage {
	here := perfmon.ReadUsage()
	out := map[string]*perfmon.Usage{"": &here}
	mm.mu.Lock()
	if mm.usage == nil {
		mm.usage = map[string]cachedUsage{}
	}
	type job struct {
		id string
		p  *machineproxy.Proxy
	}
	var jobs []job
	for _, m := range mm.saved {
		if c, ok := mm.usage[m.ID]; ok && time.Since(c.at) < usageCacheFor {
			if c.u != nil {
				out[m.ID] = c.u
			}
			continue
		}
		if p := mm.proxies[m.ID]; p != nil {
			jobs = append(jobs, job{m.ID, p})
		}
	}
	mm.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	type result struct {
		id string
		u  *perfmon.Usage
	}
	results := make(chan result, len(jobs))
	for _, j := range jobs {
		go func(j job) {
			u, err := j.p.FetchUsage(ctx)
			if err != nil {
				results <- result{j.id, nil}
				return
			}
			results <- result{j.id, &u}
		}(j)
	}
	for range jobs {
		r := <-results
		mm.mu.Lock()
		mm.usage[r.id] = cachedUsage{at: time.Now(), u: r.u}
		mm.mu.Unlock()
		if r.u != nil {
			out[r.id] = r.u
		}
	}
	return out
}
