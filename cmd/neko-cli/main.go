//go:build windows

// neko-cli provides a scriptable Agent CLI for controlling NekoSimulator233
// Android 15 (2c4g default) instances, apps, input, screenshots, and runtime bootstrap.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/neko233-com/NekoSimulator233/internal/avd"
	"github.com/neko233-com/NekoSimulator233/internal/mcpproxy"
)

var Version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printUsage()
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := args[0]
	rest := args[1:]

	switch cmd {
	case "version":
		fmt.Println(Version)
		return nil

	case "ecosystem":
		return printJSON(mcpproxy.DetectEcosystem())

	case "bootstrap":
		return avd.BootstrapRuntime(ctx, "", func(stage string, pct int) {
			fmt.Printf("[%3d%%] %s\n", pct, stage)
		})

	case "list":
		if m, err := avd.DiscoverManager(); err == nil {
			items, err := m.ListInstances(ctx)
			if err != nil {
				return err
			}
			return printJSON(items)
		}
		home, err := avd.LocalAVDHome()
		if err != nil {
			return err
		}
		items, err := avd.ListLocalInstances(ctx, home)
		if err != nil {
			return err
		}
		return printJSON(items)

	case "create":
		fs := flag.NewFlagSet("create", flag.ContinueOnError)
		api := fs.Int("api", 35, "Android API level (default 35 = Android 15)")
		cores := fs.Int("cores", 2, "CPU cores (default 2)")
		ram := fs.Int("ram", 4096, "RAM in MiB (default 4096)")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if fs.NArg() < 1 {
			return fmt.Errorf("usage: neko-cli create <name> [--api 35] [--cores 2] [--ram 4096]")
		}
		name := fs.Arg(0)
		m, err := managerFor(name)
		if err != nil {
			return err
		}
		if err := m.CreateInstanceAPI(ctx, name, *api); err != nil {
			return err
		}
		if err := m.SetResources(ctx, avd.Resources{Cores: *cores, RAMMiB: *ram}); err != nil {
			return err
		}
		return printJSON(map[string]any{"name": name, "api": *api, "cores": *cores, "ram_mib": *ram})

	case "clone":
		if len(rest) < 2 {
			return fmt.Errorf("usage: neko-cli clone <src> <dst>")
		}
		m, err := managerFor(rest[0])
		if err != nil {
			return err
		}
		return m.CloneInstance(ctx, rest[0], rest[1])

	case "start":
		if len(rest) < 1 {
			return fmt.Errorf("usage: neko-cli start <name>")
		}
		m, err := managerFor(rest[0])
		if err != nil {
			return err
		}
		inst, err := m.Start(ctx)
		if err != nil {
			return err
		}
		return printJSON(inst)

	case "stop":
		if len(rest) < 1 {
			return fmt.Errorf("usage: neko-cli stop <name>")
		}
		m, err := managerFor(rest[0])
		if err != nil {
			return err
		}
		return m.Stop(ctx)

	case "restart":
		if len(rest) < 1 {
			return fmt.Errorf("usage: neko-cli restart <name>")
		}
		m, err := managerFor(rest[0])
		if err != nil {
			return err
		}
		inst, err := m.RestartInstance(ctx)
		if err != nil {
			return err
		}
		return printJSON(inst)

	case "delete":
		if len(rest) < 1 {
			return fmt.Errorf("usage: neko-cli delete <name>")
		}
		m, err := managerFor(rest[0])
		if err != nil {
			return err
		}
		return m.DeleteInstance(ctx, rest[0])

	case "apps":
		if len(rest) < 1 {
			return fmt.Errorf("usage: neko-cli apps <name>")
		}
		m, inst, err := runningManagerFor(ctx, rest[0])
		if err != nil {
			return err
		}
		apps, err := m.ListApps(ctx, inst)
		if err != nil {
			return err
		}
		return printJSON(apps)

	case "launch":
		if len(rest) < 2 {
			return fmt.Errorf("usage: neko-cli launch <name> <package>")
		}
		m, inst, err := runningManagerFor(ctx, rest[0])
		if err != nil {
			return err
		}
		return m.LaunchApp(ctx, inst, rest[1])

	case "install":
		if len(rest) < 2 {
			return fmt.Errorf("usage: neko-cli install <name> <apk-path>")
		}
		m, inst, err := runningManagerFor(ctx, rest[0])
		if err != nil {
			return err
		}
		return m.InstallAPK(ctx, inst, rest[1], false)

	case "uninstall":
		if len(rest) < 2 {
			return fmt.Errorf("usage: neko-cli uninstall <name> <package>")
		}
		m, inst, err := runningManagerFor(ctx, rest[0])
		if err != nil {
			return err
		}
		return m.UninstallApp(ctx, inst, rest[1])

	case "install-taptap":
		if len(rest) < 1 {
			return fmt.Errorf("usage: neko-cli install-taptap <name>")
		}
		m, inst, err := runningManagerFor(ctx, rest[0])
		if err != nil {
			return err
		}
		res, err := m.InstallTapTap(ctx, inst)
		if err != nil {
			return err
		}
		return printJSON(res)

	case "tap":
		if len(rest) < 3 {
			return fmt.Errorf("usage: neko-cli tap <name> <x> <y>")
		}
		x, err := strconv.Atoi(rest[1])
		if err != nil {
			return err
		}
		y, err := strconv.Atoi(rest[2])
		if err != nil {
			return err
		}
		_, inst, err := runningManagerFor(ctx, rest[0])
		if err != nil {
			return err
		}
		ctrl, err := inst.Open()
		if err != nil {
			return err
		}
		defer ctrl.Close()
		if err := ctrl.TouchDown(x, y); err != nil {
			return err
		}
		time.Sleep(40 * time.Millisecond)
		return ctrl.TouchUp()

	case "text":
		if len(rest) < 2 {
			return fmt.Errorf("usage: neko-cli text <name> <text>")
		}
		m, inst, err := runningManagerFor(ctx, rest[0])
		if err != nil {
			return err
		}
		return m.InputText(ctx, inst, strings.Join(rest[1:], " "))

	case "gps":
		if len(rest) < 3 {
			return fmt.Errorf("usage: neko-cli gps <name> <lat> <lon>")
		}
		lat, err := strconv.ParseFloat(rest[1], 64)
		if err != nil {
			return err
		}
		lon, err := strconv.ParseFloat(rest[2], 64)
		if err != nil {
			return err
		}
		m, inst, err := runningManagerFor(ctx, rest[0])
		if err != nil {
			return err
		}
		return m.SetGPSLocation(ctx, inst, lat, lon, 10)

	default:
		return fmt.Errorf("unknown command %q (run neko-cli --help)", cmd)
	}
}

func managerFor(name string) (avd.Manager, error) {
	m, err := avd.DiscoverManager()
	if err != nil {
		return avd.Manager{}, err
	}
	return m.WithName(name)
}

func runningManagerFor(ctx context.Context, name string) (avd.Manager, avd.Instance, error) {
	m, err := managerFor(name)
	if err != nil {
		return avd.Manager{}, avd.Instance{}, err
	}
	items, err := m.ListInstances(ctx)
	if err != nil {
		return avd.Manager{}, avd.Instance{}, err
	}
	running := false
	for _, it := range items {
		if strings.EqualFold(it.Name, m.Name) && it.State == "running" {
			running = true
			break
		}
	}
	if !running {
		return avd.Manager{}, avd.Instance{}, fmt.Errorf("instance %q is not running", m.Name)
	}
	inst, err := m.Start(ctx)
	if err != nil {
		return avd.Manager{}, avd.Instance{}, err
	}
	return m, inst, nil
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printUsage() {
	fmt.Println(`neko-cli - NekoSimulator233 Agent CLI (Android 15 · 2c4g default)

Usage:
  neko-cli list                                    List all Android instances
  neko-cli create <name> [--api 35 --cores 2 --ram 4096]
  neko-cli clone <src> <dst>                       Clone an instance
  neko-cli start|stop|restart|delete <name>        Lifecycle control
  neko-cli bootstrap                               Install Android 15 SDK + Emulator
  neko-cli apps <name>                             List installed third-party apps
  neko-cli launch <name> <package>                 Launch an Android app
  neko-cli install <name> <apk-path>               Install an APK
  neko-cli tap <name> <x> <y>                      Tap screen coordinates
  neko-cli text <name> <text>                      Input text into active field
  neko-cli gps <name> <lat> <lon>                  Set virtual GPS location
  neko-cli ecosystem                               Inspect local MCP/SDK toolchain
  neko-cli version                                 Print CLI version`)
}
