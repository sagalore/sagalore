//go:build darwin || windows

package tray

import (
	"context"
	"sync"

	"github.com/getlantern/systray"
)

type Item struct {
	Title     string
	Tooltip   string
	Disabled  bool
	Separator bool
	OnClick   func()
}

// Status is the disabled account line at the top of the tray menu.
type Status struct {
	User string
}

// Menu updates the disabled account line and the login/logout action.
type Menu struct {
	mu          sync.Mutex
	user        *systray.MenuItem
	version     *systray.MenuItem
	auth        *systray.MenuItem
	authClick   func()
	updateItem  *systray.MenuItem
	updateClick func()
	autoItem    *systray.MenuItem
	autoToggle  func(bool)
}

func Supported() bool { return true }

// Run shows the tray menu and blocks until ctx is cancelled or the user quits.
// onReady runs on the systray goroutine once the menu exists; use it to push
// the first Status and wire later updates via SetStatus / SetAuthAction.
// items are the actions below login/logout (sync, quit, …).
func Run(ctx context.Context, tooltip string, items []Item, onReady func(*Menu)) {
	systray.Run(func() {
		systray.SetIcon(Icon())
		systray.SetTooltip(tooltip)

		m := &Menu{
			user:    addDisabled("Not logged in", "Battle.net account"),
			version: addDisabled("Version", "Installed Sagalore version"),
		}
		systray.AddSeparator()

		m.updateItem = systray.AddMenuItem("New version — restart", "Download is ready")
		m.updateItem.Hide()
		go func() {
			for range m.updateItem.ClickedCh {
				m.mu.Lock()
				fn := m.updateClick
				m.mu.Unlock()
				if fn != nil {
					fn()
				}
			}
		}()

		m.autoItem = systray.AddMenuItemCheckbox("Automatic updates", "Check GitHub and download in the background", false)
		go func() {
			for range m.autoItem.ClickedCh {
				var next bool
				if m.autoItem.Checked() {
					m.autoItem.Uncheck()
					next = false
				} else {
					m.autoItem.Check()
					next = true
				}
				m.mu.Lock()
				fn := m.autoToggle
				m.mu.Unlock()
				if fn != nil {
					fn(next)
				}
			}
		}()

		m.auth = systray.AddMenuItem("Log in with Battle.net", "Open the browser and log in")
		go func() {
			for range m.auth.ClickedCh {
				m.mu.Lock()
				fn := m.authClick
				m.mu.Unlock()
				if fn != nil {
					fn()
				}
			}
		}()

		for _, it := range items {
			if it.Separator {
				systray.AddSeparator()
				continue
			}
			menu := systray.AddMenuItem(it.Title, it.Tooltip)
			if it.Disabled {
				menu.Disable()
			}
			it := it
			if it.OnClick == nil {
				continue
			}
			go func() {
				for range menu.ClickedCh {
					it.OnClick()
				}
			}()
		}

		if onReady != nil {
			onReady(m)
		}

		go func() {
			<-ctx.Done()
			systray.Quit()
		}()
	}, func() {})
}

func addDisabled(title, tip string) *systray.MenuItem {
	it := systray.AddMenuItem(title, tip)
	it.Disable()
	return it
}

// SetPendingUpdate shows or hides the restart-to-update item.
func (m *Menu) SetPendingUpdate(tag string, onRestart func()) {
	if m == nil || m.updateItem == nil {
		return
	}
	m.mu.Lock()
	m.updateClick = onRestart
	m.mu.Unlock()
	if tag == "" {
		m.updateItem.Hide()
		return
	}
	title := "New version " + tag + " — restart"
	m.updateItem.SetTitle(title)
	m.updateItem.SetTooltip("Download is ready. Restart Sagalore to update")
	m.updateItem.Show()
}

// SetAutoUpdate sets the checkbox and the toggle handler.
func (m *Menu) SetAutoUpdate(on bool, onToggle func(bool)) {
	if m == nil || m.autoItem == nil {
		return
	}
	m.mu.Lock()
	m.autoToggle = onToggle
	m.mu.Unlock()
	if on {
		m.autoItem.Check()
		return
	}
	m.autoItem.Uncheck()
}

// SetAuthAction sets the login/logout item title and click handler.
func (m *Menu) SetAuthAction(loggedIn bool, onClick func()) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.authClick = onClick
	if m.auth == nil {
		return
	}
	if loggedIn {
		m.auth.SetTitle("Log out")
		m.auth.SetTooltip("Log out of Sagalore on this computer")
		return
	}
	m.auth.SetTitle("Log in with Battle.net")
	m.auth.SetTooltip("Open the browser and log in")
}

// SetStatus refreshes the grayed-out account line.
func (m *Menu) SetStatus(s Status) {
	if m == nil || m.user == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	user := stringsOr(s.User, "Not logged in")
	m.user.SetTitle(user)
	m.user.SetTooltip(user)
}

// SetVersion sets the disabled companion version line (e.g. "Version 0.1.3").
func (m *Menu) SetVersion(v string) {
	if m == nil || m.version == nil {
		return
	}
	title := "Version"
	if v != "" {
		title = "Version " + v
	}
	m.version.SetTitle(title)
	m.version.SetTooltip(title)
}

func stringsOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
