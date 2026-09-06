//go:build bindings

package main

import (
	wailsbindings "telegram-companion/internal/transport/wails"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
)

func main() {
	_ = wails.Run(&options.App{Bind: []interface{}{
		new(wailsbindings.ActivationBindings),
		new(wailsbindings.StartupBindings),
		new(wailsbindings.UpdateBindings),
		new(wailsbindings.Bindings),
	}})
}
