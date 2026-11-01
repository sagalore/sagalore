//go:build !darwin && !windows

package tray

import "context"

type Item struct {
	Title     string
	Tooltip   string
	Disabled  bool
	Separator bool
	OnClick   func()
}

type Status struct {
	User string
}

type Menu struct{}

func Supported() bool { return false }

// Run waits until ctx is cancelled. Linux builds have no tray icon.
func Run(ctx context.Context, _ string, _ []Item, onReady func(*Menu)) {
	if onReady != nil {
		onReady(&Menu{})
	}
	<-ctx.Done()
}

func (m *Menu) SetStatus(Status)                    {}
func (m *Menu) SetVersion(string)                   {}
func (m *Menu) SetAuthAction(_ bool, _ func())      {}
func (m *Menu) SetPendingUpdate(_ string, _ func()) {}
func (m *Menu) SetAutoUpdate(_ bool, _ func(bool))  {}
